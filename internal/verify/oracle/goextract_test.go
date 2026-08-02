package oracle

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func id(pkg, typ, fn string) goFuncID {
	return goFuncID{pkgPath: pkg, typeName: typ, funcName: fn}
}

func edge(from, to goFuncID) edgeKey {
	return edgeKey{from: from, to: to}
}

// TestExtractGoCallGraphs_Fixture exercises the full packages.Load + SSA +
// static/CHA pipeline against testdata/goproj and asserts the exact
// must-edge set and a may-edge superset, covering every construct called
// out in the RFC-013 brief: package function call, method call, an
// interface called through two implementations (absent from static,
// present in CHA for both), a closure whose call is excluded but whose
// body call folds up to the enclosing named function, and a generic
// function called at two instantiations deduped to its origin.
func TestExtractGoCallGraphs_Fixture(t *testing.T) {
	root, err := filepath.Abs("testdata/goproj")
	require.NoError(t, err)

	ex, err := extractGoCallGraphs(root)
	require.NoError(t, err)
	require.Equal(t, "example.com/goproj", ex.ModulePath)

	pkg := "example.com/goproj"

	wantMust := map[edgeKey]bool{
		edge(id(pkg, "", "main"), id(pkg, "", "compute")):           true,
		edge(id(pkg, "", "main"), id(pkg, "Counter", "Increment")):  true,
		edge(id(pkg, "", "main"), id(pkg, "", "useGreeters")):       true,
		edge(id(pkg, "", "main"), id(pkg, "", "withClosure")):       true,
		edge(id(pkg, "", "main"), id(pkg, "", "useGenerics")):       true,
		// main -> runHandler is a plain static edge; the dispatch it performs
		// (runHandler -> myHandler.Handle) is may-only, asserted separately.
		edge(id(pkg, "", "main"), id(pkg, "", "runHandler")): true,
		edge(id(pkg, "", "main"), id(pkg, "", "register")):   true,
		// escapedNotCalled/pkgEscaped are never CALLED, but their bodies'
		// static edges still exist in the fold (static/CHA graphs cover all
		// functions, not just reachable ones).
		edge(id(pkg, "", "escapedNotCalled"), id(pkg, "", "reachedViaEscape")): true,
		edge(id(pkg, "", "pkgEscaped"), id(pkg, "", "reachedViaPkgVar")):       true,
		edge(id(pkg, "", "compute"), id(pkg, "", "add")):            true,
		edge(id(pkg, "Counter", "Increment"), id(pkg, "Counter", "bump")): true,
		edge(id(pkg, "", "useGreeters"), id(pkg, "", "greetVia")):   true,
		// Generic instantiations dedupe to the origin identity.
		edge(id(pkg, "", "useGenerics"), id(pkg, "", "identity")): true,
		// Closure folding: the call to `add` made INSIDE withClosure's
		// closure literal is attributed to withClosure itself, matching
		// scip-go's containment-based (not SSA-node-based) attribution.
		edge(id(pkg, "", "withClosure"), id(pkg, "", "add")): true,
		// runHandler -> myHandler.Handle is NOT a static edge (dispatch is
		// through the Handler interface), so only the body call
		// myHandler.Handle -> reachedViaDispatch is a must-edge here.
		edge(id(pkg, "myHandler", "Handle"), id(pkg, "", "reachedViaDispatch")): true,
	}
	assert.Equal(t, wantMust, ex.MustEdges, "static (must) edge set")

	// The interface call must NOT appear in static (that's the whole point
	// of the sandwich: polymorphic dispatch is a may-edge, not a must-edge).
	assert.NotContains(t, ex.MustEdges, edge(id(pkg, "", "greetVia"), id(pkg, "EnglishGreeter", "Greet")))
	assert.NotContains(t, ex.MustEdges, edge(id(pkg, "", "greetVia"), id(pkg, "FrenchGreeter", "Greet")))

	// CHA must be a superset of static, and must additionally resolve the
	// interface call to BOTH concrete implementations.
	for k := range ex.MustEdges {
		assert.Contains(t, ex.MayEdges, k, "may-edges must be a superset of must-edges: missing %v", k)
	}
	assert.Contains(t, ex.MayEdges, edge(id(pkg, "", "greetVia"), id(pkg, "EnglishGreeter", "Greet")))
	assert.Contains(t, ex.MayEdges, edge(id(pkg, "", "greetVia"), id(pkg, "FrenchGreeter", "Greet")))

	// The closure literal itself never appears as an edge endpoint on
	// either side — as a callee it's excluded outright (no graph node to
	// join against); as a caller it's folded up to withClosure by
	// enclosingNamedFunc before reaching the edge sets at all.
	for k := range ex.MustEdges {
		assert.NotEqual(t, "withClosure$1", k.to.funcName)
		assert.NotEqual(t, "withClosure$1", k.from.funcName)
	}
	for k := range ex.MayEdges {
		assert.NotEqual(t, "withClosure$1", k.to.funcName)
		assert.NotEqual(t, "withClosure$1", k.from.funcName)
	}
}

// TestExtractGoCallGraphs_MainReachable asserts the raw-graph reachability
// set that the dead-verdict cross-check consumes. Unlike the folded edge
// sets, this must traverse:
//   - in-module interface dispatch: main -> runHandler -> (interface call)
//     -> myHandler.Handle -> reachedViaDispatch. There is no static edge
//     runHandler -> myHandler.Handle (the receiver is the Handler interface),
//     so reachedViaDispatch is reachable ONLY through the CHA may-edge. This
//     is the in-module analog of the real cobra bug the fix targets: a binary
//     reaches its handler through dynamic dispatch. (Cross-MODULE dispatch is
//     intentionally not traversed — see the load-mode comment in
//     goextract.go.)
//   - init roots: reachedViaInit is called only from a declared init(), so
//     it is reachable only because init is treated as a BFS root.
//
// neverCalled is reachable from nothing and must be absent.
func TestExtractGoCallGraphs_MainReachable(t *testing.T) {
	root, err := filepath.Abs("testdata/goproj")
	require.NoError(t, err)

	ex, err := extractGoCallGraphs(root)
	require.NoError(t, err)
	require.NotNil(t, ex.MainReachable)

	pkg := "example.com/goproj"

	// In-module interface dispatch is traversed. Guard the premise first: the
	// dispatch must genuinely be may-only, not a static edge — otherwise the
	// test would pass without exercising CHA reachability at all.
	require.NotContains(t, ex.MustEdges, edge(id(pkg, "", "runHandler"), id(pkg, "myHandler", "Handle")),
		"runHandler -> myHandler.Handle must be a may-only (interface dispatch) edge for this test to mean anything")
	assert.Contains(t, ex.MayEdges, edge(id(pkg, "", "runHandler"), id(pkg, "myHandler", "Handle")))

	assert.True(t, ex.MainReachable[id(pkg, "myHandler", "Handle")],
		"myHandler.Handle must be reachable via interface dispatch (main -> runHandler -> h.Handle)")
	assert.True(t, ex.MainReachable[id(pkg, "", "reachedViaDispatch")],
		"reachedViaDispatch (called from myHandler.Handle) must be reachable through the dispatch hop")

	// init roots are honored.
	assert.True(t, ex.MainReachable[id(pkg, "", "reachedViaInit")],
		"reachedViaInit is called only from a declared init(); it must be reachable because init is a BFS root")

	// Sanity: ordinary main-reachable functions are present.
	assert.True(t, ex.MainReachable[id(pkg, "", "compute")])
	assert.True(t, ex.MainReachable[id(pkg, "Counter", "Increment")])

	// Escape rule: escapedNotCalled is never invoked in-module — main only
	// STORES it into a registry field (the cobra RunE registration pattern) —
	// so no call edge enters it. Guard the premise: it must not be a static
	// or CHA callee of anything, or the assertion below would pass without
	// exercising the operand-scan escape rule.
	for k := range ex.MayEdges {
		require.NotEqual(t, id(pkg, "", "escapedNotCalled"), k.to,
			"escapedNotCalled must have no incoming may-edge for the escape-rule test to mean anything (found from %v)", k.from)
	}
	assert.True(t, ex.MainReachable[id(pkg, "", "escapedNotCalled")],
		"escapedNotCalled is address-taken by reachable code (register) and must be reachable via the escape rule")
	assert.True(t, ex.MainReachable[id(pkg, "", "reachedViaEscape")],
		"traversal must continue INTO an escaped function's body: reachedViaEscape is called only by escapedNotCalled")

	// Escape rule, package-level var form: `var pkgHandler = pkgEscaped` takes
	// pkgEscaped's address in the synthesized package initializer (a BFS root)
	// and never calls it. Guard the premise (no incoming may-edge), then assert
	// both pkgEscaped and its body callee are reachable via the operand scan.
	for k := range ex.MayEdges {
		require.NotEqual(t, id(pkg, "", "pkgEscaped"), k.to,
			"pkgEscaped must have no incoming may-edge for the package-level escape test to mean anything (found from %v)", k.from)
	}
	assert.True(t, ex.MainReachable[id(pkg, "", "pkgEscaped")],
		"pkgEscaped is address-taken by a package-level var initializer (init root) and must be reachable via the escape rule")
	assert.True(t, ex.MainReachable[id(pkg, "", "reachedViaPkgVar")],
		"traversal must continue into pkgEscaped's body: reachedViaPkgVar is called only by pkgEscaped")

	// A function called from nothing must not be reachable.
	assert.False(t, ex.MainReachable[id(pkg, "", "neverCalled")],
		"neverCalled has no caller and must be absent from MainReachable")
}
