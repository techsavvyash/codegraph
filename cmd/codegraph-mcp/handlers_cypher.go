package main

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	neo4jdriver "github.com/neo4j/neo4j-go-driver/v5/neo4j"
	"github.com/neo4j/neo4j-go-driver/v5/neo4j/dbtype"
)

// writeKeywordRegex matches Cypher write/DDL keywords as whole words. Defense
// in depth — ExecuteRead also rejects writes at the driver level. Keyword
// matches inside string literals will produce false positives; users hitting
// those should rephrase. Comments are stripped before matching.
var writeKeywordRegex = regexp.MustCompile(`(?i)\b(CREATE|MERGE|DELETE|SET|REMOVE|DROP|FOREACH|LOAD\s+CSV|CALL\s+\{[^}]*\b(?:CREATE|MERGE|DELETE|SET|REMOVE|DROP)\b)`)

// stripCypherComments removes // line and /* block */ comments from a query
// to reduce false negatives in keyword detection.
func stripCypherComments(q string) string {
	// Remove block comments first.
	for {
		i := strings.Index(q, "/*")
		if i < 0 {
			break
		}
		j := strings.Index(q[i:], "*/")
		if j < 0 {
			q = q[:i]
			break
		}
		q = q[:i] + q[i+j+2:]
	}
	// Remove line comments.
	var b strings.Builder
	for _, line := range strings.Split(q, "\n") {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// handleCypherTool runs a read-only Cypher query directly. Defense in depth:
// (1) regex keyword pre-check after stripping comments, (2) EXPLAIN validation
// to catch syntax/semantic errors cheaply, (3) read-only Neo4j transaction
// (driver-level enforcement), (4) bounded timeout via context (in handleToolCall),
// (5) row cap enforced during result iteration.
func (s *CodeGraphMCPServer) handleCypherTool(parentCtx context.Context, args map[string]interface{}) ToolCallResponse {
	query, _ := args["query"].(string)
	query = strings.TrimSpace(query)
	if query == "" {
		return errorResponse("cypher: query is required")
	}

	// Layer 1: keyword check on de-commented query.
	stripped := stripCypherComments(query)
	if writeKeywordRegex.MatchString(stripped) {
		return errorResponse("cypher: write keywords (CREATE/MERGE/DELETE/SET/REMOVE/DROP/FOREACH/LOAD CSV) are not allowed in this tool. Use the indexer for graph mutations.")
	}

	rowLimit := 100
	if r, ok := args["row_limit"].(float64); ok {
		rowLimit = int(r)
	}
	if rowLimit < 1 {
		rowLimit = 100
	}
	if rowLimit > 1000 {
		rowLimit = 1000
	}

	params := map[string]any{}
	if rawParams, ok := args["params"].(map[string]interface{}); ok {
		for k, v := range rawParams {
			params[k] = v
		}
	}

	// Layer 2: EXPLAIN validation — compiles the plan without side effects.
	// If EXPLAIN fails, abort without executing the user's query.
	explainQuery := "EXPLAIN " + query
	_, explainSummary, err := s.client.ExecuteQueryWithSummary(parentCtx, explainQuery, params)
	if err != nil {
		return errorResponse(fmt.Sprintf("cypher: query validation failed: %v", err))
	}

	// Check the plan for AllNodesScan and collect warnings
	var warnings []string
	if explainSummary != nil && planHasOperator(explainSummary.Plan(), "AllNodesScan") {
		warnings = append(warnings, "warning: query plan contains AllNodesScan — add a label qualifier to avoid scanning the whole graph")
	}

	type cypherResult struct {
		rows      []map[string]interface{}
		keys      []string
		truncated bool
		warnings  []string
	}

	result, err := s.client.ExecuteRead(parentCtx, func(tx neo4jdriver.ManagedTransaction) (any, error) {
		// Layer 3: driver-level read-only enforcement (any write here would
		// fail with a transaction-mode error).
		res, err := tx.Run(parentCtx, query, params)
		if err != nil {
			return nil, err
		}
		out := &cypherResult{warnings: warnings}
		keys, kerr := res.Keys()
		if kerr == nil {
			out.keys = keys
		}
		// Layer 5: row cap during iteration.
		for res.Next(parentCtx) {
			if len(out.rows) >= rowLimit {
				out.truncated = true
				break
			}
			rec := res.Record()
			row := make(map[string]interface{}, len(out.keys))
			for _, k := range out.keys {
				v, _ := rec.Get(k)
				row[k] = sanitizeCypherValue(v)
			}
			out.rows = append(out.rows, row)
		}
		// If we broke out of iteration due to cap, drain res.Next so the
		// driver doesn't complain. Else surface streaming errors.
		if out.truncated {
			for res.Next(parentCtx) {
			}
		}
		if rerr := res.Err(); rerr != nil {
			return out, rerr
		}
		return out, nil
	})

	if err != nil {
		if parentCtx.Err() == context.DeadlineExceeded {
			// handleToolCall owns the deadline; it rewrites this into a
			// message that includes the effective timeout_ms.
			return errorResponse("cypher: query timed out")
		}
		return errorResponse(fmt.Sprintf("cypher: %v", err))
	}

	cr, _ := result.(*cypherResult)
	if cr == nil {
		return errorResponse("cypher: internal error (nil result)")
	}

	// Build response with warnings prepended
	var responseText string
	if len(cr.warnings) > 0 {
		responseText = strings.Join(cr.warnings, "\n") + "\n\n"
	}

	out := map[string]interface{}{
		"columns":   cr.keys,
		"rows":      cr.rows,
		"row_count": len(cr.rows),
		"truncated": cr.truncated,
	}
	body, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return errorResponse(fmt.Sprintf("cypher: encode failed: %v", err))
	}
	responseText += string(body)

	return ToolCallResponse{Content: []ToolContent{{Type: "text", Text: responseText}}}
}

// sanitizeCypherValue converts Neo4j driver types into JSON-friendly values
// without losing useful identifying information (elementIds, labels, props).
func sanitizeCypherValue(v interface{}) interface{} {
	switch x := v.(type) {
	case dbtype.Node:
		return map[string]interface{}{
			"_type":   "node",
			"_id":     x.ElementId,
			"_labels": x.Labels,
			"props":   x.Props,
		}
	case dbtype.Relationship:
		return map[string]interface{}{
			"_type":  "relationship",
			"_id":    x.ElementId,
			"_rtype": x.Type,
			"_start": x.StartElementId,
			"_end":   x.EndElementId,
			"props":  x.Props,
		}
	case []interface{}:
		out := make([]interface{}, len(x))
		for i, item := range x {
			out[i] = sanitizeCypherValue(item)
		}
		return out
	case map[string]interface{}:
		out := make(map[string]interface{}, len(x))
		for k, item := range x {
			out[k] = sanitizeCypherValue(item)
		}
		return out
	default:
		return v
	}
}

type pathNode struct {
	NodeID string `json:"node_id"`
	Label  string `json:"label"`
	Name   string `json:"name,omitempty"`
}

type pathEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Type string `json:"type"`
}

type pathResult struct {
	Hops  int        `json:"hops"`
	Nodes []pathNode `json:"nodes"`
	Edges []pathEdge `json:"edges"`
}

// planHasOperator recursively walks a driver query plan checking whether any
// operator's name contains the given substring (e.g. "AllNodesScan").
func planHasOperator(plan neo4jdriver.Plan, op string) bool {
	if plan == nil {
		return false
	}
	if strings.Contains(plan.Operator(), op) {
		return true
	}
	for _, child := range plan.Children() {
		if planHasOperator(child, op) {
			return true
		}
	}
	return false
}
