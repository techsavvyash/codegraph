package resolve

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const localifaceModule = "testdata/localiface"

const (
	pkgRoot  = "example.com/localiface"
	pkgImpls = "example.com/localiface/impls"
)

// localRelKey is a comparable projection of LocalCallRelation used to assert
// the exact set of dispatch relations the resolver produces (line is excluded
// so provenance line-number drift in the fixture doesn't break set assertions;
// the dedup/first-line behavior is asserted separately).
type localRelKey struct {
	callerPkg, callerType, callerName string
	calleePkg, calleeType, calleeName string
	kind                              LocalCallKind
}

func toLocalRelKey(r LocalCallRelation) localRelKey {
	return localRelKey{
		callerPkg: r.CallerPkgPath, callerType: r.CallerType, callerName: r.CallerName,
		calleePkg: r.CalleePkgPath, calleeType: r.CalleeType, calleeName: r.CalleeName,
		kind: r.Kind,
	}
}

// callsTo builds the expected key for a CALLS relation from a root-package free
// function to an impls-package method.
func callsTo(callerFn, calleeType, calleeMethod string) localRelKey {
	return localRelKey{
		callerPkg: pkgRoot, callerName: callerFn,
		calleePkg: pkgImpls, calleeType: calleeType, calleeName: calleeMethod,
		kind: LocalCallInvoke,
	}
}

func usesValue(callerFn, calleeType, calleeMethod string) localRelKey {
	k := callsTo(callerFn, calleeType, calleeMethod)
	k.kind = LocalCallValue
	return k
}

func TestResolveLocalInterfaceCalls_RelationSet(t *testing.T) {
	rels, _, err := ResolveLocalInterfaceCalls(localifaceModule, nil)
	require.NoError(t, err)

	got := make(map[localRelKey]LocalCallRelation, len(rels))
	for _, r := range rels {
		got[toLocalRelKey(r)] = r
	}

	// Both ValModel (value receiver) and PtrModel (pointer receiver) satisfy
	// the `Model() string` interfaces; NearMiss (Model() int) must not appear.
	want := []localRelKey{
		// function-local named interface + type assertion + call
		callsTo("CallLocalNamedIface", "ValModel", "Model"),
		callsTo("CallLocalNamedIface", "PtrModel", "Model"),
		// bare anonymous assertion + call
		callsTo("CallAnonAssertion", "ValModel", "Model"),
		callsTo("CallAnonAssertion", "PtrModel", "Model"),
		// type switch with anonymous-interface case + call
		callsTo("CallTypeSwitchAnon", "ValModel", "Model"),
		callsTo("CallTypeSwitchAnon", "PtrModel", "Model"),
		// method VALUE through a local interface -> USES_VALUE, not CALLS
		usesValue("MethodValueThroughLocalIface", "ValModel", "Model"),
		usesValue("MethodValueThroughLocalIface", "PtrModel", "Model"),
		// generic type param with ANONYMOUS constraint literal
		callsTo("CallGenericAnonConstraint", "Namer", "Name"),
		// call inside a closure -> attributed to the enclosing named function
		callsTo("CallInsideClosure", "ValModel", "Model"),
		callsTo("CallInsideClosure", "PtrModel", "Model"),
		// promoted/embedded method -> callee names the DECLARING type (base)
		callsTo("CallPromotedMethod", "base", "Describe"),
	}

	for _, k := range want {
		_, ok := got[k]
		assert.True(t, ok, "expected relation missing: %+v", k)
	}
	assert.Equal(t, len(want), len(rels), "resolver produced an unexpected relation (extra beyond the intended set)")

	// The NearMiss type has Model() int, not Model() string — it must NEVER be
	// a callee for any Model dispatch.
	for k := range got {
		assert.NotEqual(t, "NearMiss", k.calleeType, "NearMiss has a different signature and must not match any Model() string interface")
	}

	// A method VALUE must be USES_VALUE, never CALLS, and the invocation cases
	// must be CALLS, never USES_VALUE.
	for k := range got {
		if k.callerName == "MethodValueThroughLocalIface" {
			assert.Equal(t, LocalCallValue, k.kind, "method value must be USES_VALUE")
		} else {
			assert.Equal(t, LocalCallInvoke, k.kind, "invocation must be CALLS")
		}
	}

	// The promoted-method callee must be the DECLARING type (base), never the
	// embedding candidate (Embedder).
	for k := range got {
		assert.NotEqual(t, "Embedder", k.calleeType, "promoted method must resolve to the declaring type (base), not the embedder")
	}
}

func TestResolveLocalInterfaceCalls_Stats(t *testing.T) {
	_, stats, err := ResolveLocalInterfaceCalls(localifaceModule, nil)
	require.NoError(t, err)

	assert.Equal(t, 2, stats.PackagesLoaded, "root + impls")
	assert.Equal(t, 0, stats.PackagesWithErrors)

	// 9 interface method selections total: 7 handled (through graph-invisible
	// interfaces) + 2 package-named skips (CallGenericNamedConstraint via a
	// named constraint, CallThroughPkgIface via the named interface directly).
	assert.Equal(t, 9, stats.SitesSeen)
	assert.Equal(t, 2, stats.PackageNamedSkipped, "the two package-scope-named dispatch sites must be skipped")
	assert.Equal(t, 7, stats.HandledSites)
	assert.Equal(t, 0, stats.ModuleScopeSkipped, "fixture has no package-level var-initializer interface call")
	assert.False(t, stats.CapExceeded)

	// Candidates are the impls package's named types with a method set:
	// ValModel, PtrModel, NearMiss, base, Embedder, Namer = 6.
	assert.Equal(t, 6, stats.CandidatesConsidered)
	assert.Equal(t, 12, stats.RelationsFound)
}

// TestResolveLocalInterfaceCalls_Dedup asserts that the same (caller, callee,
// kind) triple, reachable through more than one candidate/site, collapses to a
// single relation. In the fixture, CallLocalNamedIface reaches ValModel#Model
// and PtrModel#Model each exactly once; the assertion here is that no relation
// key is duplicated in the output slice.
func TestResolveLocalInterfaceCalls_Dedup(t *testing.T) {
	rels, _, err := ResolveLocalInterfaceCalls(localifaceModule, nil)
	require.NoError(t, err)

	seen := make(map[localRelKey]int)
	for _, r := range rels {
		seen[toLocalRelKey(r)]++
	}
	for k, n := range seen {
		assert.Equalf(t, 1, n, "relation %+v appeared %d times; must be deduped to one edge per (caller, callee, kind)", k, n)
	}
}

func TestResolveLocalInterfaceCalls_NotAGoModule(t *testing.T) {
	_, _, err := ResolveLocalInterfaceCalls(t.TempDir(), nil)
	assert.Error(t, err, "a directory with no go.mod must return an error, not panic or hang")
}

// fabricated symbol strings for the join tests. Caller (a free function) and
// method callees follow scip-go's grammar exactly.
const (
	symCallLocalNamedIface = "scip-go gomod example.com/localiface v0.0.0 `example.com/localiface`/CallLocalNamedIface()."
	symValModelModel       = "scip-go gomod example.com/localiface v0.0.0 `example.com/localiface/impls`/ValModel#Model()."
	symPtrModelModel       = "scip-go gomod example.com/localiface v0.0.0 `example.com/localiface/impls`/PtrModel#Model()."
	symBaseDescribe        = "scip-go gomod example.com/localiface v0.0.0 `example.com/localiface/impls`/base#Describe()."
)

func TestJoinLocalCalls_BothEndpointsResolve(t *testing.T) {
	rels := []LocalCallRelation{
		{
			CallerPkgPath: pkgRoot, CallerName: "CallLocalNamedIface",
			CalleePkgPath: pkgImpls, CalleeType: "ValModel", CalleeName: "Model",
			Kind: LocalCallInvoke, Line: 27,
		},
	}
	known := []string{symCallLocalNamedIface, symValModelModel, symPtrModelModel}

	joined, stats := JoinLocalCalls(rels, known)
	require.Len(t, joined, 1)
	assert.Equal(t, symCallLocalNamedIface, joined[0].FromSymbol, "free-function caller must join to its `pkg`/Name(). symbol")
	assert.Equal(t, symValModelModel, joined[0].ToSymbol)
	assert.Equal(t, LocalCallInvoke, joined[0].Kind)
	assert.Equal(t, 27, joined[0].Line, "provenance line preserved through join")
	assert.Equal(t, 1, stats.Emitted)
	assert.Equal(t, 0, stats.DroppedMissingSymbol)
}

func TestJoinLocalCalls_DropsMissingCallee(t *testing.T) {
	rels := []LocalCallRelation{
		{
			CallerPkgPath: pkgRoot, CallerName: "CallLocalNamedIface",
			CalleePkgPath: pkgImpls, CalleeType: "ValModel", CalleeName: "Model",
			Kind: LocalCallInvoke,
		},
	}
	// Callee symbol deliberately absent from knownSymbols (mirrors scip-go not
	// emitting a symbol for a promoted/unnameable method).
	known := []string{symCallLocalNamedIface}

	joined, stats := JoinLocalCalls(rels, known)
	assert.Empty(t, joined)
	assert.Equal(t, 0, stats.Emitted)
	assert.Equal(t, 1, stats.DroppedMissingSymbol)
}

func TestJoinLocalCalls_DropsMissingCaller(t *testing.T) {
	rels := []LocalCallRelation{
		{
			CallerPkgPath: pkgRoot, CallerName: "CallLocalNamedIface",
			CalleePkgPath: pkgImpls, CalleeType: "ValModel", CalleeName: "Model",
			Kind: LocalCallInvoke,
		},
	}
	// Caller symbol absent.
	known := []string{symValModelModel}

	joined, stats := JoinLocalCalls(rels, known)
	assert.Empty(t, joined)
	assert.Equal(t, 1, stats.DroppedMissingSymbol)
}

// TestJoinLocalCalls_EndToEnd runs the real resolver against the fixture and
// joins against a fabricated known-symbol set that covers ValModel/PtrModel/
// base/Namer methods and every root-package caller, asserting the resolver's
// go/types identities map cleanly onto scip-go's symbol grammar.
func TestJoinLocalCalls_EndToEnd(t *testing.T) {
	rels, _, err := ResolveLocalInterfaceCalls(localifaceModule, nil)
	require.NoError(t, err)

	known := []string{
		// callers (free functions)
		freeFuncSym("CallLocalNamedIface"),
		freeFuncSym("CallAnonAssertion"),
		freeFuncSym("CallTypeSwitchAnon"),
		freeFuncSym("MethodValueThroughLocalIface"),
		freeFuncSym("CallGenericAnonConstraint"),
		freeFuncSym("CallInsideClosure"),
		freeFuncSym("CallPromotedMethod"),
		// callees (methods)
		methodSym("impls", "ValModel", "Model"),
		methodSym("impls", "PtrModel", "Model"),
		methodSym("impls", "Namer", "Name"),
		methodSym("impls", "base", "Describe"),
	}

	joined, stats := JoinLocalCalls(rels, known)
	// All 12 resolver relations have both endpoints in the fabricated set.
	assert.Equal(t, 12, stats.Emitted)
	assert.Equal(t, 0, stats.DroppedMissingSymbol)
	require.Len(t, joined, 12)

	for _, r := range joined {
		assert.NotEmpty(t, r.FromSymbol)
		assert.NotEmpty(t, r.ToSymbol)
	}
}

func freeFuncSym(name string) string {
	return "scip-go gomod example.com/localiface v0.0.0 `example.com/localiface`/" + name + "()."
}

func methodSym(subpkg, typ, method string) string {
	return "scip-go gomod example.com/localiface v0.0.0 `example.com/localiface/" + subpkg + "`/" + typ + "#" + method + "()."
}
