package static

import "testing"

// TestResolveGenericCallEdgesDeterministicMinLine mirrors
// TestResolveCallEdgesDeterministicMinLine (call_graph_scip_test.go) for the
// language-agnostic builder: getReferencesInFile's Cypher query has no
// ORDER BY, so if a caller invokes the same target from two lines, "first
// row wins" would make the resulting CALLS.line property flip
// nondeterministically between indexing runs. resolveGenericCallEdges must
// collapse the two rows into one edge and always pick the smallest line,
// regardless of the order refs are supplied in.
func TestResolveGenericCallEdgesDeterministicMinLine(t *testing.T) {
	funcs := []genericFuncInfo{
		{ID: "main", StartLine: 1, EndLine: 30},
	}

	// Two call sites from "main" to "greet": lines 15 and 10. The order
	// below is deliberately "line 15 first" to reproduce the exact ordering
	// that used to win incorrectly.
	refsLine15First := []refInfo{
		{line: 15, targetID: "greet"},
		{line: 10, targetID: "greet"},
	}
	refsLine10First := []refInfo{
		{line: 10, targetID: "greet"},
		{line: 15, targetID: "greet"},
	}

	edgesA := resolveGenericCallEdges(refsLine15First, funcs)
	edgesB := resolveGenericCallEdges(refsLine10First, funcs)

	for name, edges := range map[string][]genericCallEdge{"line15First": edgesA, "line10First": edgesB} {
		if len(edges) != 1 {
			t.Fatalf("%s: expected exactly 1 collapsed edge for the (main, greet) pair, got %d: %+v", name, len(edges), edges)
		}
		if edges[0].CallerID != "main" || edges[0].TargetID != "greet" {
			t.Fatalf("%s: unexpected edge endpoints: %+v", name, edges[0])
		}
		if edges[0].Line != 10 {
			t.Errorf("%s: expected edge.Line == 10 (the smallest call-site line), got %d", name, edges[0].Line)
		}
	}

	// The two orderings must agree exactly — this is the determinism
	// property, not just "both happen to equal 10".
	if edgesA[0] != edgesB[0] {
		t.Errorf("resolveGenericCallEdges is order-dependent: line15First=%+v line10First=%+v", edgesA[0], edgesB[0])
	}
}

// TestResolveGenericCallEdgesIgnoresSelfCallsAndUnresolvedCallers verifies
// the two skip conditions carried over from the original inline loop: a
// reference with no enclosing caller is dropped, and a caller calling itself
// (recursion) does not produce a self-loop edge.
func TestResolveGenericCallEdgesIgnoresSelfCallsAndUnresolvedCallers(t *testing.T) {
	funcs := []genericFuncInfo{
		{ID: "main", StartLine: 1, EndLine: 10},
	}
	refs := []refInfo{
		{line: 5, targetID: "main"},     // self-call: main calls itself
		{line: 100, targetID: "helper"}, // line 100 has no enclosing caller
	}

	edges := resolveGenericCallEdges(refs, funcs)
	if len(edges) != 0 {
		t.Fatalf("expected 0 edges (self-call and unresolved-caller rows both dropped), got %d: %+v", len(edges), edges)
	}
}

// TestResolveGenericCallEdgesMultipleDistinctPairs ensures unrelated
// (caller, target) pairs are preserved independently — the dedup/min-line
// logic must only collapse rows that share the exact same pair, not merge
// across different pairs.
func TestResolveGenericCallEdgesMultipleDistinctPairs(t *testing.T) {
	funcs := []genericFuncInfo{
		{ID: "main", StartLine: 1, EndLine: 30},
		{ID: "helper", StartLine: 40, EndLine: 60},
	}
	refs := []refInfo{
		{line: 15, targetID: "greet"},
		{line: 10, targetID: "greet"}, // same pair as above, smaller line wins
		{line: 45, targetID: "greet"}, // different caller (helper), same target
	}

	edges := resolveGenericCallEdges(refs, funcs)
	if len(edges) != 2 {
		t.Fatalf("expected 2 distinct edges, got %d: %+v", len(edges), edges)
	}

	byCaller := map[string]genericCallEdge{}
	for _, e := range edges {
		byCaller[e.CallerID] = e
	}
	if e, ok := byCaller["main"]; !ok || e.Line != 10 {
		t.Errorf("main->greet edge: got %+v, want Line=10", e)
	}
	if e, ok := byCaller["helper"]; !ok || e.Line != 45 {
		t.Errorf("helper->greet edge: got %+v, want Line=45", e)
	}
}
