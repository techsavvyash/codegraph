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
		edge(id(pkg, "", "compute"), id(pkg, "", "add")):            true,
		edge(id(pkg, "Counter", "Increment"), id(pkg, "Counter", "bump")): true,
		edge(id(pkg, "", "useGreeters"), id(pkg, "", "greetVia")):   true,
		// Generic instantiations dedupe to the origin identity.
		edge(id(pkg, "", "useGenerics"), id(pkg, "", "identity")): true,
		// Closure folding: the call to `add` made INSIDE withClosure's
		// closure literal is attributed to withClosure itself, matching
		// scip-go's containment-based (not SSA-node-based) attribution.
		edge(id(pkg, "", "withClosure"), id(pkg, "", "add")): true,
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
