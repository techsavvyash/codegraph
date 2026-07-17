package static

import "testing"

// TestCollapseToMinLinePerPairDeterministic is the direct test of the shared
// dedup kernel both call-graph builders now depend on (resolveCallEdges via
// call_graph_scip.go, resolveGenericCallEdges via call_graph_generic.go).
// Feeding the same two triples in both orders must produce byte-identical
// results, and the surviving line must be the smallest of the two.
func TestCollapseToMinLinePerPairDeterministic(t *testing.T) {
	line15First := []minLineEdge{
		{CallerID: "main", TargetID: "greet", Line: 15},
		{CallerID: "main", TargetID: "greet", Line: 10},
	}
	line10First := []minLineEdge{
		{CallerID: "main", TargetID: "greet", Line: 10},
		{CallerID: "main", TargetID: "greet", Line: 15},
	}

	got15First := collapseToMinLinePerPair(line15First)
	got10First := collapseToMinLinePerPair(line10First)

	for name, got := range map[string][]minLineEdge{"line15First": got15First, "line10First": got10First} {
		if len(got) != 1 {
			t.Fatalf("%s: expected 1 collapsed edge, got %d: %+v", name, len(got), got)
		}
		if got[0].Line != 10 {
			t.Errorf("%s: expected Line == 10 (smallest), got %d", name, got[0].Line)
		}
	}
	if got15First[0] != got10First[0] {
		t.Errorf("collapseToMinLinePerPair is order-dependent: line15First=%+v line10First=%+v", got15First[0], got10First[0])
	}
}

// TestCollapseToMinLinePerPairDropsSelfCalls verifies self-referencing
// triples (a function "calling itself" per the raw reference data — e.g.
// recursion, or a resolution artifact) never produce a self-loop edge.
func TestCollapseToMinLinePerPairDropsSelfCalls(t *testing.T) {
	got := collapseToMinLinePerPair([]minLineEdge{
		{CallerID: "main", TargetID: "main", Line: 5},
	})
	if len(got) != 0 {
		t.Fatalf("expected self-call to be dropped, got %+v", got)
	}
}

// TestCollapseToMinLinePerPairKeepsDistinctPairsIndependent ensures the
// dedup keys strictly on the full (caller, target) pair: two different
// callers targeting the same function must not be merged into one edge, and
// unrelated pairs must each keep their own line untouched by another pair's
// minimum.
func TestCollapseToMinLinePerPairKeepsDistinctPairsIndependent(t *testing.T) {
	got := collapseToMinLinePerPair([]minLineEdge{
		{CallerID: "main", TargetID: "greet", Line: 15},
		{CallerID: "main", TargetID: "greet", Line: 10},
		{CallerID: "helper", TargetID: "greet", Line: 45},
	})
	if len(got) != 2 {
		t.Fatalf("expected 2 distinct edges, got %d: %+v", len(got), got)
	}
	byCaller := map[string]minLineEdge{}
	for _, e := range got {
		byCaller[e.CallerID] = e
	}
	if e, ok := byCaller["main"]; !ok || e.Line != 10 {
		t.Errorf("main->greet: got %+v, want Line=10", e)
	}
	if e, ok := byCaller["helper"]; !ok || e.Line != 45 {
		t.Errorf("helper->greet: got %+v, want Line=45", e)
	}
}

// TestCollapseToMinLinePerPairEmptyInput guards the zero-value/empty-slice
// path returns an empty (not nil-panicking) result.
func TestCollapseToMinLinePerPairEmptyInput(t *testing.T) {
	got := collapseToMinLinePerPair(nil)
	if len(got) != 0 {
		t.Fatalf("expected empty result for nil input, got %+v", got)
	}
}
