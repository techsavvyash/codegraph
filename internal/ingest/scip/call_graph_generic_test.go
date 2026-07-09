package static

import (
	"context"
	"strings"
	"testing"

	models "github.com/context-maximiser/code-graph/internal/model"
)

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

// TestLineRangeByteOffsets locks the non-Go byte-offset estimate (fix D):
// startByte/endByte span the start of startLine through the end of endLine's
// content, excluding the trailing newline, with endLine clamped to the
// file's actual line count so the EOF-proxy value used for the last
// declaration-order function in a file never runs past the file.
func TestLineRangeByteOffsets(t *testing.T) {
	// "line1\nline22\nline333\n" -> lines = ["line1", "line22", "line333", ""]
	lines := []string{"line1", "line22", "line333", ""}

	tests := []struct {
		name      string
		startLine int
		endLine   int
		wantStart int
		wantEnd   int
	}{
		// line1: bytes [0,5); line22: [6,12); line333: [13,20)
		{"first_line_only", 1, 1, 0, 5},
		{"first_two_lines", 1, 2, 0, 12},
		{"middle_line_only", 2, 2, 6, 12},
		{"spans_all_real_lines", 1, 3, 0, 20},
		// EOF-proxy: endLine far beyond the file must clamp to the last line.
		{"end_line_clamped_to_eof", 2, 10000, 6, 20},
		{"start_line_out_of_bounds", 5, 6, -1, -1},
		{"start_line_zero", 0, 1, -1, -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start, end := lineRangeByteOffsets(lines, tt.startLine, tt.endLine)
			if start != tt.wantStart || end != tt.wantEnd {
				t.Errorf("lineRangeByteOffsets(lines, %d, %d) = (%d, %d), want (%d, %d)",
					tt.startLine, tt.endLine, start, end, tt.wantStart, tt.wantEnd)
			}
		})
	}
}

// TestLineRangeByteOffsetsMatchesContent verifies the computed range actually
// slices out the expected substring from real file content, not just
// plausible-looking numbers.
func TestLineRangeByteOffsetsMatchesContent(t *testing.T) {
	content := "func one() {}\nfunc two() {\n  return\n}\nfunc three() {}\n"
	lines := []string{
		"func one() {}",
		"func two() {",
		"  return",
		"}",
		"func three() {}",
		"",
	}
	start, end := lineRangeByteOffsets(lines, 2, 4)
	if start < 0 || end > len(content) || start >= end {
		t.Fatalf("invalid range: start=%d end=%d (len=%d)", start, end, len(content))
	}
	got := content[start:end]
	want := "func two() {\n  return\n}"
	if got != want {
		t.Errorf("content[start:end] = %q, want %q", got, want)
	}
}

// TestGenericDegreeQueryServiceScoped locks the fix requiring the generic
// (non-Go) degree computation's SET target to be constrained to a single
// service — via the Service{name:$serviceName}->File->fn walk — with the
// parameter actually bound.
func TestGenericDegreeQueryServiceScoped(t *testing.T) {
	scope := models.ScopeContext{Scope: "main", ScopeID: "main"}
	cypher, params := genericDegreeQuery("svc-b", scope)

	for _, sub := range []string{"(s:Service {name: $serviceName})", "-[:CONTAINS]->(:File)", "-[:CONTAINS]->(fn)"} {
		if !strings.Contains(cypher, sub) {
			t.Errorf("cypher must contain %q (Service{name:$serviceName}->File->fn walk):\n%s", sub, cypher)
		}
	}
	if params["serviceName"] != "svc-b" {
		t.Errorf("params[serviceName] = %v, want %q", params["serviceName"], "svc-b")
	}
	if params["scopeId"] != "main" {
		t.Errorf("params[scopeId] = %v, want %q", params["scopeId"], "main")
	}
}

// TestGenericBuildCallGraphRequiresServiceName locks the cross-service
// isolation guard: file paths are service-relative, so a builder without a
// bound service would merge same-named files across services. The guard fires
// before any query, so no Neo4j connection is needed.
func TestGenericBuildCallGraphRequiresServiceName(t *testing.T) {
	cg := NewGenericCallGraphBuilder(nil)
	err := cg.BuildCallGraph(context.Background())
	if err == nil {
		t.Fatal("BuildCallGraph without a service name must error, got nil")
	}
	if !strings.Contains(err.Error(), "service name") {
		t.Errorf("error should name the missing service name, got: %v", err)
	}
}
