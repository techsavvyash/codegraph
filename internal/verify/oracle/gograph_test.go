package oracle

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestJoinGoGraphEdges_ConcreteMethodAndFunction(t *testing.T) {
	rows := []goCallsRow{
		{
			fromSymbol: "scip-go gomod example.com/goproj v0.0.0 `example.com/goproj`/compute().",
			toSymbol:   "scip-go gomod example.com/goproj v0.0.0 `example.com/goproj`/add().",
		},
		{
			fromSymbol: "scip-go gomod example.com/goproj v0.0.0 `example.com/goproj`/Counter#Increment().",
			toSymbol:   "scip-go gomod example.com/goproj v0.0.0 `example.com/goproj`/Counter#bump().",
		},
	}

	join := joinGoGraphEdges(rows)

	assert.Equal(t, 0, join.Abstract)
	assert.Equal(t, 0, join.Unmappable)
	assert.Len(t, join.Edges, 2)
	assert.Contains(t, join.Edges, edge(
		id("example.com/goproj", "", "compute"),
		id("example.com/goproj", "", "add"),
	))
	assert.Contains(t, join.Edges, edge(
		id("example.com/goproj", "Counter", "Increment"),
		id("example.com/goproj", "Counter", "bump"),
	))
}

func TestJoinGoGraphEdges_AbstractInterfaceMethodExcluded(t *testing.T) {
	rows := []goCallsRow{
		{
			fromSymbol: "scip-go gomod example.com/goproj v0.0.0 `example.com/goproj`/greetVia().",
			// Abstract: trailing bare dot, no call parens — an interface
			// method slot, never a concrete SSA-graph node.
			toSymbol: "scip-go gomod example.com/goproj v0.0.0 `example.com/goproj`/Greeter#Greet.",
		},
	}

	join := joinGoGraphEdges(rows)

	assert.Equal(t, 1, join.Abstract)
	assert.Equal(t, 0, join.Unmappable)
	assert.Empty(t, join.Edges)
}

func TestJoinGoGraphEdges_TypeLevelAndUnparseableAreUnmappable(t *testing.T) {
	rows := []goCallsRow{
		{
			// Type-level symbol (no method component) is not a callable.
			fromSymbol: "scip-go gomod example.com/goproj v0.0.0 `example.com/goproj`/Counter#",
			toSymbol:   "scip-go gomod example.com/goproj v0.0.0 `example.com/goproj`/add().",
		},
		{
			fromSymbol: "scip-go gomod example.com/goproj v0.0.0 `example.com/goproj`/add().",
			// Malformed / non-Go symbol string.
			toSymbol: "local 42",
		},
	}

	join := joinGoGraphEdges(rows)

	assert.Equal(t, 0, join.Abstract)
	assert.Equal(t, 2, join.Unmappable)
	assert.Empty(t, join.Edges)
}

func TestCallableFuncID(t *testing.T) {
	id, ok := callableFuncID("scip-go gomod example.com/goproj v0.0.0 `example.com/goproj`/Counter#Increment().")
	assert.True(t, ok)
	assert.Equal(t, "example.com/goproj", id.pkgPath)
	assert.Equal(t, "Counter", id.typeName)
	assert.Equal(t, "Increment", id.funcName)

	_, ok = callableFuncID("scip-go gomod example.com/goproj v0.0.0 `example.com/goproj`/Counter#")
	assert.False(t, ok, "type-level symbol has no method component")

	_, ok = callableFuncID("not a scip symbol")
	assert.False(t, ok)
}

func TestCallableFuncID_FreeFunction(t *testing.T) {
	fid, ok := callableFuncID("scip-go gomod example.com/goproj v0.0.0 `example.com/goproj`/add().")
	assert.True(t, ok)
	assert.Equal(t, "example.com/goproj", fid.pkgPath)
	assert.Equal(t, "", fid.typeName)
	assert.Equal(t, "add", fid.funcName)
}

func TestParseFreeFunctionSymbol_PackageTermExcluded(t *testing.T) {
	// A package-level term (var/const), not a call: bare trailing dot.
	_, _, ok := parseFreeFunctionSymbol("scip-go gomod example.com/goproj v0.0.0 `example.com/goproj`/Version.")
	assert.False(t, ok)

	// Type/method-bound symbols are rejected here — that's
	// resolve.ParseGoSymbolDescriptor's job, not this function's.
	_, _, ok = parseFreeFunctionSymbol("scip-go gomod example.com/goproj v0.0.0 `example.com/goproj`/Counter#Increment().")
	assert.False(t, ok)
}

func TestIsAbstractMethodSymbol(t *testing.T) {
	assert.True(t, isAbstractMethodSymbol(
		"scip-go gomod example.com/goproj v0.0.0 `example.com/goproj`/Greeter#Greet."))
	assert.False(t, isAbstractMethodSymbol(
		"scip-go gomod example.com/goproj v0.0.0 `example.com/goproj`/Counter#Increment()."),
		"concrete method call suffix is not abstract")
	assert.False(t, isAbstractMethodSymbol(
		"scip-go gomod example.com/goproj v0.0.0 `example.com/goproj`/add()."),
		"free function has no type/method shape at all")
}
