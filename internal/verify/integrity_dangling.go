package verify

import (
	"context"
	"fmt"
	"strings"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j/dbtype"

	neo4j "github.com/context-maximiser/code-graph/internal/graph"
)

// relShape declares the label sets that are legal on each end of a
// relationship type. Derived empirically from the live dev graph
// (docker exec ... cypher-shell) plus internal/model/relationship.go doc
// comments — see rfc/013-graph-correctness.md check 1.
//
// CONTAINS is flat in this graph: File contains Function/Method/Class/
// Interface/Variable/Module/Reference directly (no File->Class->Method
// nesting), and Service contains File/Document. IMPLEMENTS legitimately
// spans Method->Method, Variable->Method (decorator-bound arrows promoted
// to callable, see kind_promotion.go), and Class/Interface->Interface/
// Symbol/Class (structural + LLM-validated realization).
type relShape struct {
	relType    string
	fromLabels [][]string // any-of: node must have at least one label from ANY of these sets
	toLabels   [][]string
}

// hasAnyLabelSet reports whether nodeLabels contains at least one label
// present in any of the allowed sets.
func hasAnyLabelSet(nodeLabels []string, allowed [][]string) bool {
	nodeSet := make(map[string]bool, len(nodeLabels))
	for _, l := range nodeLabels {
		nodeSet[l] = true
	}
	for _, set := range allowed {
		for _, l := range set {
			if nodeSet[l] {
				return true
			}
		}
	}
	return false
}

func danglingEndpointShapes() []relShape {
	return []relShape{
		{
			relType: "CALLS",
			// File callers are module-scope call sites (package-level var
			// initializers, top-level statements) attributed to the file
			// itself — import-time invocation, see call_sites.go.
			fromLabels: [][]string{{"Function"}, {"Method"}, {"File"}},
			toLabels:   [][]string{{"Function"}, {"Method"}},
		},
		{
			relType: "USES_VALUE",
			// Address-taken function references (`cfg.Fn = handler`): same
			// caller shapes as CALLS, same targets — see call_sites.go.
			fromLabels: [][]string{{"Function"}, {"Method"}, {"File"}},
			toLabels:   [][]string{{"Function"}, {"Method"}},
		},
		{
			relType:    "IMPLEMENTS",
			fromLabels: [][]string{{"Method"}, {"Variable"}, {"Class"}, {"Interface"}},
			toLabels:   [][]string{{"Method"}, {"Class"}, {"Interface"}, {"Symbol"}},
		},
		{
			relType:    "CONTAINS",
			fromLabels: [][]string{{"Service"}, {"File"}, {"Class"}, {"Interface"}, {"Module"}},
			toLabels: [][]string{
				{"File"}, {"Document"}, // Service ->
				{"Function"}, {"Method"}, {"Class"}, {"Interface"}, {"Variable"}, {"Module"}, {"Reference"}, // File ->
			},
		},
		{
			relType:    "DEFINES",
			fromLabels: [][]string{{"Variable"}, {"Method"}, {"Function"}, {"Class"}, {"Interface"}, {"Module"}},
			toLabels:   [][]string{{"Symbol"}},
		},
		{
			relType:    "REFERENCES",
			fromLabels: [][]string{{"Reference"}},
			toLabels:   [][]string{{"Symbol"}},
		},
		{
			relType:    "EXPOSES_API",
			fromLabels: [][]string{{"Function"}, {"Method"}},
			toLabels:   [][]string{{"APIRoute"}},
		},
	}
}

// checkDanglingEndpoints implements RFC-013 invariant 1: relationship
// endpoints must carry one of the expected label sets for that relationship
// type. Anything else (a mislabeled node, a stray edge to e.g. a Symbol
// where a Function was expected) fails.
func checkDanglingEndpoints(ctx context.Context, client *neo4j.Client, sf scopeFilter, sampleLimit int) []CheckResult {
	var results []CheckResult
	for _, shape := range danglingEndpointShapes() {
		results = append(results, checkOneRelShape(ctx, client, shape, sf, sampleLimit))
	}
	return results
}

func checkOneRelShape(ctx context.Context, client *neo4j.Client, shape relShape, sf scopeFilter, sampleLimit int) CheckResult {
	name := fmt.Sprintf("dangling-endpoints:%s", shape.relType)

	// Scope filter applies to whichever endpoint actually carries
	// serviceName/scopeId in this schema. Both a and b are checked when the
	// filter is non-empty so we don't miss cross-scope leakage; a relationship
	// counts as "in scope" if either endpoint matches.
	var scopeClause string
	params := map[string]any{"limit": int64(sampleLimit)}
	if !sf.empty() {
		aClause := strings.ReplaceAll(sf.clause, "n.", "a.")
		bClause := strings.ReplaceAll(sf.clause, "n.", "b.")
		scopeClause = fmt.Sprintf("WHERE (%s) OR (%s)", aClause, bClause)
		for k, v := range sf.params {
			params[k] = v
		}
	}

	cypher := fmt.Sprintf(`
		MATCH (a)-[r:%s]->(b)
		%s
		RETURN a, b, labels(a) AS aLabels, labels(b) AS bLabels
	`, neo4j.Ident(shape.relType), scopeClause)

	records, err := client.ExecuteQuery(ctx, cypher, params)
	if err != nil {
		return CheckResult{
			Name:   name,
			Status: StatusFail,
			Detail: fmt.Sprintf("query failed: %v", err),
		}
	}

	var samples []string
	var count int64
	for _, rec := range records {
		m := rec.AsMap()
		aLabels := toStringSlice(m["aLabels"])
		bLabels := toStringSlice(m["bLabels"])

		fromOK := hasAnyLabelSet(aLabels, shape.fromLabels)
		toOK := hasAnyLabelSet(bLabels, shape.toLabels)
		if fromOK && toOK {
			continue
		}
		count++
		if int64(len(samples)) < int64(sampleLimit) {
			aNode, _ := m["a"].(dbtype.Node)
			bNode, _ := m["b"].(dbtype.Node)
			samples = append(samples, fmt.Sprintf("%s -[%s]-> %s", describeNode(aNode), shape.relType, describeNode(bNode)))
		}
	}

	if count == 0 {
		return CheckResult{Name: name, Status: StatusPass, Count: 0}
	}
	return CheckResult{
		Name:    name,
		Status:  StatusFail,
		Detail:  fmt.Sprintf("%d %s edges have an endpoint outside the expected label set", count, shape.relType),
		Count:   count,
		Samples: samples,
	}
}

func toStringSlice(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
