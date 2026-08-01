package static

import (
	"os"
	"path/filepath"
	"testing"
)

// --- parseGoCallSites -------------------------------------------------------

func TestParseGoCallSites(t *testing.T) {
	src := `package p

var precomputed = buildTable()
var savedFn = helperFn
var handler = wrap(process)

func run() {
	direct()
	pkgvar.Method()
	generic[int](1)
	cb := callback
	_ = cb
}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "f.go")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	idx, err := parseGoCallSites(path)
	if err != nil {
		t.Fatal(err)
	}

	// Module-scope call: buildTable() on line 3, callee ident at 0-based col 18.
	if !idx.isCallSite(3, 18, "buildTable") {
		t.Errorf("buildTable() at module scope not indexed: %+v", idx.exact)
	}
	// Value references must NOT be call sites — the whole point.
	if idx.isCallSite(4, 14, "helperFn") {
		t.Error("helperFn value reference wrongly indexed as call site")
	}
	// wrap(process): wrap IS a call, its argument process is NOT.
	if !idx.isCallSite(5, 14, "wrap") {
		t.Error("wrap() not indexed")
	}
	if idx.isCallSite(5, 19, "process") {
		t.Error("process (call argument, value use) wrongly indexed as call site")
	}
	// In-body calls: direct(), selector method, generic instantiation.
	if !idx.isCallSite(8, 1, "direct") {
		t.Error("direct() not indexed")
	}
	if !idx.isCallSite(9, 8, "Method") {
		t.Error("pkgvar.Method() must index the Sel identifier")
	}
	if idx.isCallSite(9, 1, "pkgvar") {
		t.Error("selector base wrongly indexed as call site")
	}
	if !idx.isCallSite(10, 1, "generic") {
		t.Error("generic[int]() instantiation call not indexed")
	}
	// cb := callback is a value use.
	if idx.isCallSite(11, 7, "callback") {
		t.Error("callback value reference wrongly indexed")
	}
}

func TestCallSiteIndexNameFallback(t *testing.T) {
	idx := newCallSiteIndex()
	idx.add(10, 4, "doWork")

	// Exact position.
	if !idx.isCallSite(10, 4, "doWork") {
		t.Error("exact match failed")
	}
	// Column drift (UTF-16 vs byte columns): same line + same callee name
	// must still classify as a call.
	if !idx.isCallSite(10, 6, "doWork") {
		t.Error("name fallback on column drift failed")
	}
	// Unknown column sentinel (-1) with a matching name.
	if !idx.isCallSite(10, -1, "doWork") {
		t.Error("name fallback with unknown column failed")
	}
	// Different name on the same line is NOT a call.
	if idx.isCallSite(10, 20, "other") {
		t.Error("unrelated name wrongly classified as call")
	}
	// nil index: no evidence, never claims call.
	var nilIdx *callSiteIndex
	if nilIdx.isCallSite(10, 4, "doWork") {
		t.Error("nil index must report false")
	}
}

// --- classification in resolveCallEdges (Go builder) ------------------------

func TestResolveCallEdges_ValueRefsAndModuleScope(t *testing.T) {
	callers := []callerInfo{{ID: "fnA", StartLine: 10, EndLine: 20}}
	sites := newCallSiteIndex()
	sites.add(12, 4, "target")  // in-body call
	sites.add(3, 8, "initFn")   // module-scope call (package var initializer)

	rows := []callRefRow{
		{Line: 12, Col: 4, Name: "target", TargetID: "t1"},  // call inside fnA
		{Line: 15, Col: 2, Name: "valueFn", TargetID: "t2"}, // value ref inside fnA -> drop
		{Line: 3, Col: 8, Name: "initFn", TargetID: "t3"},   // module-scope call -> File edge
		{Line: 5, Col: 0, Name: "savedFn", TargetID: "t4"},  // module-scope value ref -> drop
	}

	edges := resolveCallEdges(rows, callers, nil, sites, "file-node-id")
	if len(edges) != 2 {
		t.Fatalf("expected 2 edges (1 in-body call, 1 module-scope call), got %d: %+v", len(edges), edges)
	}
	byTarget := map[string]callEdge{}
	for _, e := range edges {
		byTarget[e.TargetID] = e
	}
	if e := byTarget["t1"]; e.CallerID != "fnA" {
		t.Errorf("in-body call: got caller %q, want fnA", e.CallerID)
	}
	if e := byTarget["t3"]; e.CallerID != "file-node-id" {
		t.Errorf("module-scope call: got caller %q, want the File node", e.CallerID)
	}
	if _, ok := byTarget["t2"]; ok {
		t.Error("in-body value reference produced an edge")
	}
	if _, ok := byTarget["t4"]; ok {
		t.Error("module-scope value reference produced an edge")
	}
}

func TestResolveCallEdges_NilSitesPreservesLegacyBehavior(t *testing.T) {
	callers := []callerInfo{{ID: "fnA", StartLine: 10, EndLine: 20}}
	rows := []callRefRow{
		{Line: 15, Col: 2, Name: "anything", TargetID: "t1"}, // in body
		{Line: 3, Col: 0, Name: "initFn", TargetID: "t2"},    // module scope
	}
	edges := resolveCallEdges(rows, callers, nil, nil, "file-node-id")
	if len(edges) != 1 || edges[0].CallerID != "fnA" {
		t.Fatalf("nil sites must keep in-body refs and drop module-scope refs (no evidence to attribute): %+v", edges)
	}
}

// --- classification in resolveGenericCallEdges ------------------------------

func TestResolveGenericCallEdges_ValueRefsAndModuleScope(t *testing.T) {
	funcs := []genericFuncInfo{{ID: "fnA", StartLine: 10, EndLine: 20}}
	sites := newCallSiteIndex()
	sites.add(12, 4, "target")
	sites.add(2, 6, "bootstrap")

	refs := []refInfo{
		{line: 12, col: 4, name: "target", targetID: "t1"},    // in-body call
		{line: 15, col: 2, name: "valueFn", targetID: "t2"},   // in-body value ref
		{line: 2, col: 6, name: "bootstrap", targetID: "t3"},  // top-level call
		{line: 4, col: 0, name: "aliasOnly", targetID: "t4"},  // top-level value ref
	}

	edges := resolveGenericCallEdges(refs, funcs, sites, "file-node-id")
	if len(edges) != 2 {
		t.Fatalf("expected 2 edges, got %d: %+v", len(edges), edges)
	}
	byTarget := map[string]genericCallEdge{}
	for _, e := range edges {
		byTarget[e.TargetID] = e
	}
	if e := byTarget["t1"]; e.CallerID != "fnA" {
		t.Errorf("in-body call: got caller %q, want fnA", e.CallerID)
	}
	if e := byTarget["t3"]; e.CallerID != "file-node-id" {
		t.Errorf("top-level call: got caller %q, want the File node", e.CallerID)
	}
}

func TestResolveGenericCallEdges_ModuleScopeDedupToMinLine(t *testing.T) {
	sites := newCallSiteIndex()
	sites.add(8, 0, "boot")
	sites.add(3, 0, "boot")

	refs := []refInfo{
		{line: 8, col: 0, name: "boot", targetID: "t1"},
		{line: 3, col: 0, name: "boot", targetID: "t1"},
	}
	edges := resolveGenericCallEdges(refs, nil, sites, "file-node-id")
	if len(edges) != 1 || edges[0].Line != 3 || edges[0].CallerID != "file-node-id" {
		t.Fatalf("module-scope edges must dedup to min line per (File, target) pair: %+v", edges)
	}
}
