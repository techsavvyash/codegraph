package static

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	models "github.com/context-maximiser/code-graph/internal/model"
)

func TestParseFuncRanges(t *testing.T) {
	dir := t.TempDir()
	src := `package example

func TopLevel() {
	fmt.Println("hello")
}

type Foo struct{}

func (f *Foo) Method() {
	fmt.Println("method")
}

func init() {
	setup()
}
`
	tmpFile := filepath.Join(dir, "example.go")
	if err := os.WriteFile(tmpFile, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}

	ranges, err := parseFuncRanges(tmpFile)
	if err != nil {
		t.Fatal(err)
	}

	if len(ranges) != 3 {
		t.Fatalf("expected 3 func ranges, got %d", len(ranges))
	}

	// Check TopLevel
	if ranges[0].Name != "TopLevel" {
		t.Errorf("expected TopLevel, got %s", ranges[0].Name)
	}
	if ranges[0].DeclLine != 3 {
		t.Errorf("TopLevel DeclLine: got %d, want 3", ranges[0].DeclLine)
	}
	if ranges[0].StartLine != 3 || ranges[0].EndLine != 5 {
		t.Errorf("TopLevel range: got %d-%d, want 3-5", ranges[0].StartLine, ranges[0].EndLine)
	}

	// Check Method with receiver
	if ranges[1].Name != "Foo.Method" {
		t.Errorf("expected Foo.Method, got %s", ranges[1].Name)
	}

	// Check init
	if ranges[2].Name != "init" {
		t.Errorf("expected init, got %s", ranges[2].Name)
	}
}

// TestParseFuncRangesBodyByteOffsets locks fix C (RFC-006 Phase 1): the byte
// range persisted for a Go Function/Method must cover the whole declaration —
// the "func" keyword through the closing brace — not just the identifier,
// so source-code retrieval returns the complete function rather than a
// ~15-character identifier-only slice.
func TestParseFuncRangesBodyByteOffsets(t *testing.T) {
	dir := t.TempDir()
	src := `package example

func Add(a, b int) int {
	return a + b
}
`
	tmpFile := filepath.Join(dir, "add.go")
	if err := os.WriteFile(tmpFile, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}

	ranges, err := parseFuncRanges(tmpFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(ranges) != 1 {
		t.Fatalf("expected 1 func range, got %d: %+v", len(ranges), ranges)
	}
	fr := ranges[0]

	if fr.StartByte < 0 || fr.EndByte <= fr.StartByte || fr.EndByte > len(src) {
		t.Fatalf("invalid byte range: StartByte=%d EndByte=%d (len(src)=%d)", fr.StartByte, fr.EndByte, len(src))
	}

	extracted := src[fr.StartByte:fr.EndByte]
	wantPrefix := "func Add(a, b int) int {"
	if !strings.HasPrefix(extracted, wantPrefix) {
		t.Errorf("extracted body must start with %q, got %q", wantPrefix, extracted)
	}
	if !strings.HasSuffix(extracted, "}") {
		t.Errorf("extracted body must end with the closing brace, got %q", extracted)
	}
	if !strings.Contains(extracted, "return a + b") {
		t.Errorf("extracted body must contain the function body, got %q", extracted)
	}
}

// TestParseFuncRangesClosureVarByteOffsets mirrors
// TestParseFuncRangesBodyByteOffsets for the `var X = func(...){}` closure
// case: the byte range must cover the FuncLit itself ("func(...) {...}"),
// not just the variable name.
func TestParseFuncRangesClosureVarByteOffsets(t *testing.T) {
	dir := t.TempDir()
	src := `package example

var buildManager = func() error {
	return nil
}
`
	tmpFile := filepath.Join(dir, "closure.go")
	if err := os.WriteFile(tmpFile, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}

	ranges, err := parseFuncRanges(tmpFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(ranges) != 1 {
		t.Fatalf("expected 1 func range, got %d: %+v", len(ranges), ranges)
	}
	fr := ranges[0]

	if fr.StartByte < 0 || fr.EndByte <= fr.StartByte || fr.EndByte > len(src) {
		t.Fatalf("invalid byte range: StartByte=%d EndByte=%d (len(src)=%d)", fr.StartByte, fr.EndByte, len(src))
	}
	extracted := src[fr.StartByte:fr.EndByte]
	if !strings.HasPrefix(extracted, "func() error {") {
		t.Errorf("extracted closure must start with the func literal, got %q", extracted)
	}
	if !strings.HasSuffix(extracted, "}") {
		t.Errorf("extracted closure must end with the closing brace, got %q", extracted)
	}
}

// TestScipDegreeQueryServiceScoped locks the fix requiring the degree
// computation's SET target to be constrained by serviceName with the
// parameter actually bound, not just a Go field being set on the struct.
func TestScipDegreeQueryServiceScoped(t *testing.T) {
	scope := models.ScopeContext{Scope: "main", ScopeID: "main"}
	cypher, params := scipDegreeQuery("svc-a", scope)

	if !strings.Contains(cypher, "fn.serviceName = $serviceName") {
		t.Errorf("cypher must constrain fn.serviceName = $serviceName:\n%s", cypher)
	}
	if params["serviceName"] != "svc-a" {
		t.Errorf("params[serviceName] = %v, want %q", params["serviceName"], "svc-a")
	}
	if params["scopeId"] != "main" {
		t.Errorf("params[scopeId] = %v, want %q", params["scopeId"], "main")
	}
}

func TestFindEnclosingFunc(t *testing.T) {
	ranges := []funcRange{
		{Name: "outer", StartLine: 5, EndLine: 20},
		{Name: "inner", StartLine: 10, EndLine: 15},
		{Name: "other", StartLine: 25, EndLine: 30},
	}

	tests := []struct {
		line int
		want string
	}{
		{1, ""},       // before any function
		{5, "outer"},  // at outer start
		{8, "outer"},  // inside outer, before inner
		{10, "inner"}, // at inner start (inner is narrower)
		{12, "inner"}, // inside inner
		{15, "inner"}, // at inner end
		{18, "outer"}, // inside outer, after inner
		{20, "outer"}, // at outer end
		{22, ""},      // between functions
		{27, "other"}, // inside other
		{35, ""},      // after all functions
	}

	for _, tc := range tests {
		got := findEnclosingFunc(ranges, tc.line)
		name := ""
		if got != nil {
			name = got.Name
		}
		if name != tc.want {
			t.Errorf("line %d: got %q, want %q", tc.line, name, tc.want)
		}
	}
}

func TestExprName(t *testing.T) {
	// This is a unit test for the exprName helper, but since it works on
	// ast.Expr which requires constructing AST nodes, we test it indirectly
	// via parseFuncRanges (the Foo.Method test above covers pointer receivers).
	// Here we just test the isGoFile helper.
	if !isGoFile("main.go") {
		t.Error("expected main.go to be a Go file")
	}
	if isGoFile("main.ts") {
		t.Error("expected main.ts to NOT be a Go file")
	}
	if isGoFile("") {
		t.Error("expected empty string to NOT be a Go file")
	}
}

func TestReadModulePath(t *testing.T) {
	dir := t.TempDir()
	goMod := filepath.Join(dir, "go.mod")
	content := "module github.com/example/myproject\n\ngo 1.24\n"
	if err := os.WriteFile(goMod, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	got := readModulePath(dir)
	want := "github.com/example/myproject"
	if got != want {
		t.Errorf("readModulePath() = %q, want %q", got, want)
	}
}

func TestReadModulePathMissing(t *testing.T) {
	dir := t.TempDir()
	got := readModulePath(dir)
	if got != "" {
		t.Errorf("readModulePath() = %q, want empty string for missing go.mod", got)
	}
}

func TestReadModulePathMalformed(t *testing.T) {
	dir := t.TempDir()
	goMod := filepath.Join(dir, "go.mod")
	if err := os.WriteFile(goMod, []byte("not a valid go.mod\n"), 0644); err != nil {
		t.Fatal(err)
	}

	got := readModulePath(dir)
	if got != "" {
		t.Errorf("readModulePath() = %q, want empty string for malformed go.mod", got)
	}
}

// TestParseFuncRangesClosureVar locks the C2 fix: top-level
// `var X = func(...){}` must be returned as a funcRange with
// IsClosureVar=true so that calls inside the closure body get
// attributed to X (and the var node can be promoted to :Function
// for cross-file caller resolution).
func TestParseFuncRangesClosureVar(t *testing.T) {
	dir := t.TempDir()
	src := `package example

import "fmt"

func host() {
	buildManager()
}

var buildManager = func() error {
	fmt.Println("called")
	return nil
}

var notAFunc = 42
`
	tmpFile := filepath.Join(dir, "closure.go")
	if err := os.WriteFile(tmpFile, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
	ranges, err := parseFuncRanges(tmpFile)
	if err != nil {
		t.Fatal(err)
	}
	// Expect: host (FuncDecl) + buildManager (closure-var); notAFunc must NOT
	// appear (not a FuncLit value).
	if len(ranges) != 2 {
		t.Fatalf("expected 2 ranges, got %d: %+v", len(ranges), ranges)
	}
	var host, bm *funcRange
	for i := range ranges {
		switch ranges[i].Name {
		case "host":
			host = &ranges[i]
		case "buildManager":
			bm = &ranges[i]
		}
	}
	if host == nil || host.IsClosureVar {
		t.Errorf("host: want FuncDecl, got %+v", host)
	}
	if bm == nil {
		t.Fatalf("buildManager closure-var range missing")
	}
	if !bm.IsClosureVar {
		t.Errorf("buildManager: want IsClosureVar=true, got false")
	}
	// Body should span the FuncLit braces, not the var keyword line.
	if bm.StartLine != 9 || bm.EndLine != 12 {
		t.Errorf("buildManager body range: got %d-%d, want 9-12", bm.StartLine, bm.EndLine)
	}
}

func TestParseFuncRangesInvalidFile(t *testing.T) {
	_, err := parseFuncRanges("/nonexistent/path.go")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestParseFuncRangesInvalidGo(t *testing.T) {
	dir := t.TempDir()
	tmpFile := filepath.Join(dir, "bad.go")
	if err := os.WriteFile(tmpFile, []byte("not valid go"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := parseFuncRanges(tmpFile)
	if err == nil {
		t.Error("expected error for invalid Go file")
	}
}

// TestResolveCallEdgesDeterministicMinLine locks the fix for the
// nondeterministic CALLS.line bug found via test/fixtures/tiny-go: when a
// caller invokes the same target from two call sites, Neo4j's unordered
// query results previously meant "line" was set to whichever row happened
// to arrive first, flipping between indexing runs. resolveCallEdges must
// collapse the two rows into one edge and always pick the smallest line,
// regardless of the order rows are supplied in.
func TestResolveCallEdgesDeterministicMinLine(t *testing.T) {
	callers := []callerInfo{
		{ID: "main", StartLine: 1, EndLine: 30},
	}

	// Two call sites from "main" to "greet": lines 15 and 10. The row order
	// below is deliberately "line 15 first" to reproduce the exact ordering
	// that used to win incorrectly.
	rowsLine15First := []callRefRow{
		{Line: 15, TargetID: "greet"},
		{Line: 10, TargetID: "greet"},
	}
	rowsLine10First := []callRefRow{
		{Line: 10, TargetID: "greet"},
		{Line: 15, TargetID: "greet"},
	}

	edgesA, _ := resolveCallEdges(rowsLine15First, callers, nil, nil, "")
	edgesB, _ := resolveCallEdges(rowsLine10First, callers, nil, nil, "")

	for name, edges := range map[string][]callEdge{"line15First": edgesA, "line10First": edgesB} {
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
		t.Errorf("resolveCallEdges is order-dependent: line15First=%+v line10First=%+v", edgesA[0], edgesB[0])
	}
}

// TestResolveCallEdgesBranchMetadataFollowsWinningLine ensures branchDepth
// and isConditional are computed for the winning (minimum-line) call site,
// not left over from whichever row happened to be processed last.
func TestResolveCallEdgesBranchMetadataFollowsWinningLine(t *testing.T) {
	callers := []callerInfo{
		{ID: "main", StartLine: 1, EndLine: 30},
	}
	branches := []branchRange{
		// Line 15's call site is inside a conditional; line 10's is not.
		{StartLine: 14, EndLine: 16, Depth: 1},
	}

	rows := []callRefRow{
		{Line: 15, TargetID: "greet"}, // inside the if-block
		{Line: 10, TargetID: "greet"}, // not inside any conditional, and smaller
	}

	edges, _ := resolveCallEdges(rows, callers, branches, nil, "")
	if len(edges) != 1 {
		t.Fatalf("expected 1 edge, got %d: %+v", len(edges), edges)
	}
	if edges[0].Line != 10 {
		t.Fatalf("expected winning line 10, got %d", edges[0].Line)
	}
	if edges[0].IsConditional {
		t.Errorf("expected IsConditional=false for line 10 (outside the branch), got true: %+v", edges[0])
	}
	if edges[0].BranchDepth != 0 {
		t.Errorf("expected BranchDepth=0 for line 10, got %d", edges[0].BranchDepth)
	}
}

// TestResolveCallEdgesKeepsSelfCallsDropsUnresolvedCallers verifies a
// reference with no enclosing caller is dropped, while a caller calling
// itself (recursion) DOES produce a self-loop edge — the drop-self-calls
// behavior this test used to assert was the exact bug RFC-013's oracle
// caught (0 self-loop CALLS edges anywhere in the live graph); recursion
// must be a real edge like any other call.
func TestResolveCallEdgesKeepsSelfCallsDropsUnresolvedCallers(t *testing.T) {
	callers := []callerInfo{
		{ID: "main", StartLine: 1, EndLine: 10},
	}
	rows := []callRefRow{
		{Line: 5, TargetID: "main"},     // self-call: main calls itself
		{Line: 100, TargetID: "helper"}, // line 100 has no enclosing caller
	}

	edges, _ := resolveCallEdges(rows, callers, nil, nil, "")
	if len(edges) != 1 {
		t.Fatalf("expected 1 edge (the self-call survives, the unresolved-caller row is dropped), got %d: %+v", len(edges), edges)
	}
	if edges[0].CallerID != "main" || edges[0].TargetID != "main" || edges[0].Line != 5 {
		t.Errorf("unexpected self-call edge: %+v", edges[0])
	}
}

// TestSCIPBuildCallGraphRequiresServiceName locks the cross-service isolation
// guard on the Go builder — same rationale as the generic builder's test:
// service-relative file paths make unbounded queries merge foreign services'
// same-named files. The guard fires before any query, so no Neo4j needed.
func TestSCIPBuildCallGraphRequiresServiceName(t *testing.T) {
	cg := NewSCIPCallGraphBuilder(nil, t.TempDir())
	err := cg.BuildCallGraph(context.Background())
	if err == nil {
		t.Fatal("BuildCallGraph without a service name must error, got nil")
	}
	if !strings.Contains(err.Error(), "service name") {
		t.Errorf("error should name the missing service name, got: %v", err)
	}
}

// TestSelectCallerCandidateDisambiguatesByDefLine is the direct regression
// test for the same-name range-clobbering bug (RFC-013): a file with two
// same-named methods on different receivers must resolve each AST funcRange
// to ITS OWN node, chosen by definition-line containment, never by
// map-overwrite order.
func TestSelectCallerCandidateDisambiguatesByDefLine(t *testing.T) {
	// Mirrors the real-world shape found in internal/ingest/pipeline/stages.go:
	// two structs, each with a method named "Run", declared in the same file.
	// FooStage.Run: decl/def line 10, body lines 10-15.
	// BarStage.Run: decl/def line 20, body lines 20-30.
	candidates := []graphNodeCandidate{
		{ID: "node-foo-run", DefLine: 10},
		{ID: "node-bar-run", DefLine: 20},
	}

	fooRange := funcRange{Name: "FooStage.Run", DeclLine: 10, StartLine: 10, EndLine: 15}
	barRange := funcRange{Name: "BarStage.Run", DeclLine: 20, StartLine: 20, EndLine: 30}

	gotFoo, ok := selectCallerCandidate(candidates, fooRange)
	if !ok {
		t.Fatalf("FooStage.Run range should resolve unambiguously")
	}
	if gotFoo.ID != "node-foo-run" {
		t.Errorf("FooStage.Run resolved to %s, want node-foo-run (its own node, not BarStage's)", gotFoo.ID)
	}

	gotBar, ok := selectCallerCandidate(candidates, barRange)
	if !ok {
		t.Fatalf("BarStage.Run range should resolve unambiguously")
	}
	if gotBar.ID != "node-bar-run" {
		t.Errorf("BarStage.Run resolved to %s, want node-bar-run (its own node, not FooStage's)", gotBar.ID)
	}
}

// TestSelectCallerCandidateSkipsAmbiguousOrUnmatchedRanges verifies the
// "never guess" contract: zero matches and 2+ matches both return ok=false
// rather than picking an arbitrary candidate.
func TestSelectCallerCandidateSkipsAmbiguousOrUnmatchedRanges(t *testing.T) {
	t.Run("zero matches", func(t *testing.T) {
		candidates := []graphNodeCandidate{{ID: "node-a", DefLine: 100}}
		fr := funcRange{Name: "Foo", DeclLine: 10, StartLine: 10, EndLine: 15}
		if _, ok := selectCallerCandidate(candidates, fr); ok {
			t.Fatal("expected ok=false when no candidate's DefLine falls in range")
		}
	})

	t.Run("two matches", func(t *testing.T) {
		// Defensive case: should not arise given DefLine's precision in
		// practice, but the contract must hold regardless.
		candidates := []graphNodeCandidate{
			{ID: "node-a", DefLine: 12},
			{ID: "node-b", DefLine: 13},
		}
		fr := funcRange{Name: "Foo", DeclLine: 10, StartLine: 10, EndLine: 15}
		if _, ok := selectCallerCandidate(candidates, fr); ok {
			t.Fatal("expected ok=false when 2+ candidates match — must not guess")
		}
	})
}

// TestUpdateFunctionBodyRangesRejectsDuplicateNodeID is the explicit
// invariant guard requested alongside the disambiguation fix: a batch that
// would write the same node ID twice must error rather than silently let
// the second write clobber the first (the exact failure mode being fixed).
func TestUpdateFunctionBodyRangesRejectsDuplicateNodeID(t *testing.T) {
	cg := NewSCIPCallGraphBuilder(nil, t.TempDir())
	callers := []callerInfo{
		{ID: "dup-node", StartLine: 1, EndLine: 5},
		{ID: "dup-node", StartLine: 10, EndLine: 20},
	}
	err := cg.updateFunctionBodyRanges(context.Background(), callers)
	if err == nil {
		t.Fatal("expected an error for a batch writing the same node ID twice, got nil")
	}
	if !strings.Contains(err.Error(), "2+ range updates") {
		t.Errorf("error should describe the duplicate-write condition, got: %v", err)
	}
}
