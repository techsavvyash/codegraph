package resolve

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const fixtureModule = "testdata/simplemod"

// relKey is a comparable projection of TypeRelationship used to assert set
// membership (type-level: Method fields empty; method-level: both set).
type relKey struct {
	candPkg, candType, candMethod    string
	ifacePkg, ifaceType, ifaceMethod string
}

func toRelKey(r TypeRelationship) relKey {
	return relKey{
		candPkg: r.CandidatePkgPath, candType: r.CandidateType, candMethod: r.CandidateMethod,
		ifacePkg: r.InterfacePkgPath, ifaceType: r.InterfaceType, ifaceMethod: r.InterfaceMethod,
	}
}

func TestResolveGoTypes_FixtureTypeLevel(t *testing.T) {
	rels, stats, err := ResolveGoTypes(fixtureModule)
	require.NoError(t, err)

	// Exactly one non-empty interface in the fixture: a.Storer. a.Empty
	// (0 methods) must be excluded entirely.
	assert.Equal(t, 1, stats.InterfacesConsidered, "only a.Storer should be considered; a.Empty must be skipped as an empty interface")

	// Candidates: FileStore, MemStore, Nope, Wrapped all have >=1 method on
	// T or *T so all four are *considered*, even though Nope does not end
	// up implementing Storer.
	assert.Equal(t, 4, stats.CandidatesConsidered)

	typeLevel := map[relKey]TypeRelationship{}
	for _, r := range rels {
		if r.CandidateMethod == "" {
			typeLevel[toRelKey(r)] = r
		}
	}

	const (
		pkgA = "example.com/simplemod/a"
		pkgB = "example.com/simplemod/b"
	)

	wantPresent := []relKey{
		{candPkg: pkgB, candType: "FileStore", ifacePkg: pkgA, ifaceType: "Storer"},
		{candPkg: pkgB, candType: "MemStore", ifacePkg: pkgA, ifaceType: "Storer"},
		{candPkg: pkgB, candType: "Wrapped", ifacePkg: pkgA, ifaceType: "Storer"},
	}
	for _, k := range wantPresent {
		r, ok := typeLevel[k]
		assert.True(t, ok, "expected type-level relationship %+v to be present", k)
		if ok {
			// FileStore and Wrapped only satisfy Storer via pointer
			// receiver (Wrapped promotes FileStore's pointer-receiver
			// methods, which only land on *Wrapped); MemStore satisfies it
			// via value receiver directly.
			switch k.candType {
			case "FileStore", "Wrapped":
				assert.True(t, r.ViaPointer, "%s should only implement Storer via pointer", k.candType)
			case "MemStore":
				assert.False(t, r.ViaPointer, "MemStore implements Storer via value receiver")
			}
		}
	}

	notWant := relKey{candPkg: pkgB, candType: "Nope", ifacePkg: pkgA, ifaceType: "Storer"}
	_, ok := typeLevel[notWant]
	assert.False(t, ok, "Nope only implements Save, not Load — must NOT be reported as implementing Storer")

	// No relationship should ever target a.Empty: it's an empty interface
	// and is skipped before the O(I×C) pass even starts.
	for k := range typeLevel {
		assert.NotEqual(t, "Empty", k.ifaceType, "empty interface must never appear as a relationship target")
	}

	assert.Len(t, wantPresent, stats.TypeLevelRelationships, "type-level relationship count should match exactly the 3 true implementers")
}

func TestResolveGoTypes_FixtureMethodLevel(t *testing.T) {
	rels, _, err := ResolveGoTypes(fixtureModule)
	require.NoError(t, err)

	methodLevel := map[relKey]TypeRelationship{}
	for _, r := range rels {
		if r.CandidateMethod != "" {
			methodLevel[toRelKey(r)] = r
		}
	}

	const (
		pkgA = "example.com/simplemod/a"
		pkgB = "example.com/simplemod/b"
	)

	// Every true implementer must map both Storer methods.
	for _, cand := range []string{"FileStore", "MemStore", "Wrapped"} {
		for _, method := range []string{"Save", "Load"} {
			k := relKey{candPkg: pkgB, candType: cand, candMethod: method, ifacePkg: pkgA, ifaceType: "Storer", ifaceMethod: method}
			r, ok := methodLevel[k]
			assert.True(t, ok, "expected method-level relationship %+v", k)
			if ok && cand == "Wrapped" {
				// Wrapped's Save/Load are promoted from the embedded
				// FileStore field, not declared directly on Wrapped.
				assert.True(t, r.Promoted, "Wrapped.%s should resolve to a promoted method", method)
			}
			if ok && cand != "Wrapped" {
				assert.False(t, r.Promoted, "%s.%s is declared directly, not promoted", cand, method)
			}
		}
	}

	// Nope must not contribute any method-level relationship, including a
	// partial one for Save (which it does implement in isolation — but
	// only full interface satisfaction produces relationships).
	for k := range methodLevel {
		assert.NotEqual(t, "Nope", k.candType, "Nope does not fully implement Storer, so it must contribute zero method-level relationships")
	}

	assert.Len(t, methodLevel, 6, "3 implementers x 2 methods = 6 method-level relationships")
}

func TestResolveGoTypes_NotAGoModule(t *testing.T) {
	_, _, err := ResolveGoTypes(t.TempDir())
	assert.Error(t, err, "a directory with no go.mod must return an error, not panic or hang")
}
