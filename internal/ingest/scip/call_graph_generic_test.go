package static

import (
	"context"
	"strings"
	"testing"

	"github.com/context-maximiser/code-graph/internal/ingest/structure"
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

// TestResolveGenericCallEdgesKeepsSelfCallsDropsUnresolvedCallers verifies a
// reference with no enclosing caller is dropped, while a caller calling
// itself (recursion) DOES produce a self-loop edge — shared
// collapseToMinLinePerPair behavior with the Go builder, fixed together
// under RFC-013 (the oracle found 0 self-loop CALLS edges anywhere in the
// live graph, Go or otherwise).
func TestResolveGenericCallEdgesKeepsSelfCallsDropsUnresolvedCallers(t *testing.T) {
	funcs := []genericFuncInfo{
		{ID: "main", StartLine: 1, EndLine: 10},
	}
	refs := []refInfo{
		{line: 5, targetID: "main"},     // self-call: main calls itself
		{line: 100, targetID: "helper"}, // line 100 has no enclosing caller
	}

	edges := resolveGenericCallEdges(refs, funcs)
	if len(edges) != 1 {
		t.Fatalf("expected 1 edge (the self-call survives, the unresolved-caller row is dropped), got %d: %+v", len(edges), edges)
	}
	if edges[0].CallerID != "main" || edges[0].TargetID != "main" || edges[0].Line != 5 {
		t.Errorf("unexpected self-call edge: %+v", edges[0])
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

// TestApplyStructureRanges locks the RFC-010 range resolution: functions map
// to their tree-sitter node by identifier position (innermost containment),
// and anything unmapped — nil structure or a definition lost to a parse-error
// region — keeps a declaration-line stub with no byte range, never a guess.
func TestApplyStructureRanges(t *testing.T) {
	src := `function outer(): void {
  const inner = (): void => {
    console.log("deep");
  };
  inner();
}

function last(): void {}
`
	lang, ok := structure.ForFile("x.ts")
	if !ok {
		t.Fatal("typescript grammar not wired")
	}
	fs, err := structure.Extract(lang, []byte(src))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	funcs := []genericFuncInfo{
		// SCIP identifier positions (1-based line, 0-based col).
		{ID: "outer", StartLine: 1, StartCol: 9},
		{ID: "inner", StartLine: 2, StartCol: 8},
		{ID: "last", StartLine: 8, StartCol: 9},
		// A definition the parse never saw (e.g. inside an ERROR region).
		{ID: "ghost", StartLine: 40, StartCol: 0},
	}
	applyStructureRanges(funcs, fs)

	want := []struct {
		id                 string
		startLine, endLine int
		mapped             bool
	}{
		{"outer", 1, 6, true},
		// inner maps to the declarator-widened arrow, not to outer.
		{"inner", 2, 4, true},
		// last ends at its own brace — the old declaration-order code gave
		// the final function startLine+10000.
		{"last", 8, 8, true},
		// ghost keeps a stub: endLine == startLine, no byte range.
		{"ghost", 40, 40, false},
	}
	for i, w := range want {
		f := funcs[i]
		if f.StartLine != w.startLine || f.EndLine != w.endLine || f.Mapped != w.mapped {
			t.Errorf("%s: got start=%d end=%d mapped=%v, want start=%d end=%d mapped=%v",
				w.id, f.StartLine, f.EndLine, f.Mapped, w.startLine, w.endLine, w.mapped)
		}
		if !w.mapped && (f.StartByte != -1 || f.EndByte != -1) {
			t.Errorf("%s: fallback stub must keep byte sentinels, got %d..%d", w.id, f.StartByte, f.EndByte)
		}
		if w.mapped && (f.StartByte < 0 || f.EndByte <= f.StartByte) {
			t.Errorf("%s: mapped function missing byte range: %d..%d", w.id, f.StartByte, f.EndByte)
		}
	}
}

// TestApplyStructureRangesNilStructure: no grammar / unreadable file — every
// function degrades to the declaration-line stub.
func TestApplyStructureRangesNilStructure(t *testing.T) {
	funcs := []genericFuncInfo{{ID: "a", StartLine: 7, StartCol: 2, EndLine: 7}}
	applyStructureRanges(funcs, nil)
	f := funcs[0]
	if f.Mapped || f.StartLine != 7 || f.EndLine != 7 || f.StartByte != -1 || f.EndByte != -1 {
		t.Errorf("nil structure must yield a pure stub, got %+v", f)
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

// TestApplyDecorators locks decorator resolution: a function's own
// decorators and its enclosing class's decorators are read from the SAME
// tree-sitter structure used for range resolution, keyed off the same SCIP
// identifier position, and encoded as "Name" / "Name:arg" strings.
func TestApplyDecorators(t *testing.T) {
	src := `@Controller('users')
class UsersController {
  @Get() findAll() {}
  @Get(':id') findOne() {}
}

class Plain {
  plainMethod() {}
}
`
	lang, ok := structure.ForFile("x.ts")
	if !ok {
		t.Fatal("typescript grammar not wired")
	}
	fs, err := structure.Extract(lang, []byte(src))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	// SCIP identifier positions for findAll, findOne, plainMethod.
	funcs := []genericFuncInfo{
		{ID: "findAll", StartLine: 3, StartCol: 9},
		{ID: "findOne", StartLine: 4, StartCol: 14},
		{ID: "plainMethod", StartLine: 8, StartCol: 2},
	}
	applyDecorators(funcs, fs)

	byID := make(map[string]genericFuncInfo, len(funcs))
	for _, f := range funcs {
		byID[f.ID] = f
	}

	if got := byID["findAll"].Decorators; len(got) != 1 || got[0] != "Get" {
		t.Errorf("findAll.Decorators = %v, want [Get]", got)
	}
	if got := byID["findAll"].ClassDecorators; len(got) != 1 || got[0] != "Controller:users" {
		t.Errorf("findAll.ClassDecorators = %v, want [Controller:users]", got)
	}

	if got := byID["findOne"].Decorators; len(got) != 1 || got[0] != "Get::id" {
		t.Errorf("findOne.Decorators = %v, want [Get::id]", got)
	}
	if got := byID["findOne"].ClassDecorators; len(got) != 1 || got[0] != "Controller:users" {
		t.Errorf("findOne.ClassDecorators = %v, want [Controller:users]", got)
	}

	if got := byID["plainMethod"].Decorators; got != nil {
		t.Errorf("plainMethod.Decorators = %v, want nil", got)
	}
	if got := byID["plainMethod"].ClassDecorators; got != nil {
		t.Errorf("plainMethod.ClassDecorators (Plain has no class decorator) = %v, want nil", got)
	}
}

// TestApplyDecoratorsNilStructure: no grammar / unreadable file — decorators
// stay nil, mirroring applyStructureRanges' nil-structure fallback.
func TestApplyDecoratorsNilStructure(t *testing.T) {
	funcs := []genericFuncInfo{{ID: "a", StartLine: 7, StartCol: 2}}
	applyDecorators(funcs, nil)
	if funcs[0].Decorators != nil || funcs[0].ClassDecorators != nil {
		t.Errorf("nil structure must leave decorators nil, got %+v", funcs[0])
	}
}

// TestEncodeDecorators locks the "Name" / "Name:arg" encoding, including the
// documented limitation that ':' inside an arg is not escaped.
func TestEncodeDecorators(t *testing.T) {
	tests := []struct {
		name string
		in   []structure.DecoratorInfo
		want []string
	}{
		{"empty", nil, nil},
		{"no-arg", []structure.DecoratorInfo{{Name: "Injectable"}}, []string{"Injectable"}},
		{"with-arg", []structure.DecoratorInfo{{Name: "Get", Arg: "id"}}, []string{"Get:id"}},
		{
			"multiple",
			[]structure.DecoratorInfo{{Name: "UseGuards"}, {Name: "Post", Arg: "create"}},
			[]string{"UseGuards", "Post:create"},
		},
		{
			"arg contains colon (known limitation, not escaped)",
			[]structure.DecoratorInfo{{Name: "Cron", Arg: "0 0 * * *"}},
			[]string{"Cron:0 0 * * *"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := encodeDecorators(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("encodeDecorators(%+v) = %v, want %v", tt.in, got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("encodeDecorators(%+v)[%d] = %q, want %q", tt.in, i, got[i], tt.want[i])
				}
			}
		})
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
