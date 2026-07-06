package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	neo4jdriver "github.com/neo4j/neo4j-go-driver/v5/neo4j"
	"github.com/neo4j/neo4j-go-driver/v5/neo4j/dbtype"
)

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
// (1) regex keyword pre-check after stripping comments, (2) read-only Neo4j
// transaction (driver-level enforcement), (3) bounded timeout via context,
// (4) row cap enforced during result iteration.
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

	timeoutMs := 3000
	if t, ok := args["timeout_ms"].(float64); ok {
		timeoutMs = int(t)
	}
	if timeoutMs < 100 {
		timeoutMs = 100
	}
	if timeoutMs > 5000 {
		timeoutMs = 5000
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

	ctx, cancel := context.WithTimeout(parentCtx, time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	type cypherResult struct {
		rows      []map[string]interface{}
		keys      []string
		truncated bool
	}

	result, err := s.client.ExecuteRead(ctx, func(tx neo4jdriver.ManagedTransaction) (any, error) {
		// Layer 2: driver-level read-only enforcement (any write here would
		// fail with a transaction-mode error).
		res, err := tx.Run(ctx, query, params)
		if err != nil {
			return nil, err
		}
		out := &cypherResult{}
		keys, kerr := res.Keys()
		if kerr == nil {
			out.keys = keys
		}
		// Layer 4: row cap during iteration.
		for res.Next(ctx) {
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
			for res.Next(ctx) {
			}
		}
		if rerr := res.Err(); rerr != nil {
			return out, rerr
		}
		return out, nil
	})

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return errorResponse(fmt.Sprintf("cypher: query timed out after %dms", timeoutMs))
		}
		return errorResponse(fmt.Sprintf("cypher: %v", err))
	}

	cr, _ := result.(*cypherResult)
	if cr == nil {
		return errorResponse("cypher: internal error (nil result)")
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
	return ToolCallResponse{Content: []ToolContent{{Type: "text", Text: string(body)}}}
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

// handlePathTool finds paths between two nodes filtered by relationship types
// and direction. Defaults to all shortest paths; pass shortest=false to get up
// to 25 paths up to max_hops.
