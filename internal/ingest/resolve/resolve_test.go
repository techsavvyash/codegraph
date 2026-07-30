package resolve

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixtureKnownSymbols is the verbatim symbol-string set scip-go emits for
// testdata/simplemod (captured by running `scip-go` against the fixture
// module and dumping doc.Symbols[*].Symbol — see the resolver task notes).
// Deliberately excludes local N symbols (locals never participate in
// IMPLEMENTS joins) but is otherwise complete, including symbols the
// resolver will never produce a lookup key for (fields, the empty
// interface, free functions) so the "only intended keys get indexed" and
// "missing symbol -> dropped" behavior is exercised against real shapes.
var fixtureKnownSymbolsComplete = []string{
	"scip-go gomod example.com/simplemod f9c7f511d060 `example.com/simplemod/a`/Storer#Save.",
	"scip-go gomod example.com/simplemod f9c7f511d060 `example.com/simplemod/a`/Storer#Load.",
	"scip-go gomod example.com/simplemod f9c7f511d060 `example.com/simplemod/a`/Empty#",
	"scip-go gomod example.com/simplemod f9c7f511d060 `example.com/simplemod/a`/UsesError().",
	"scip-go gomod example.com/simplemod f9c7f511d060 `example.com/simplemod/a`/",
	"scip-go gomod example.com/simplemod f9c7f511d060 `example.com/simplemod/a`/Storer#",
	"scip-go gomod example.com/simplemod f9c7f511d060 `example.com/simplemod/b`/FileStore#path.",
	"scip-go gomod example.com/simplemod f9c7f511d060 `example.com/simplemod/b`/MemStore#",
	"scip-go gomod example.com/simplemod f9c7f511d060 `example.com/simplemod/b`/MemStore#data.",
	"scip-go gomod example.com/simplemod f9c7f511d060 `example.com/simplemod/b`/Nope#",
	"scip-go gomod example.com/simplemod f9c7f511d060 `example.com/simplemod/b`/FileStore#Load().",
	"scip-go gomod example.com/simplemod f9c7f511d060 `example.com/simplemod/b`/MemStore#Save().",
	"scip-go gomod example.com/simplemod f9c7f511d060 `example.com/simplemod/b`/Wrapped#",
	"scip-go gomod example.com/simplemod f9c7f511d060 `example.com/simplemod/b`/Wrapped#FileStore.",
	"scip-go gomod example.com/simplemod f9c7f511d060 `example.com/simplemod/b`/FileStore#Save().",
	"scip-go gomod example.com/simplemod f9c7f511d060 `example.com/simplemod/b`/MemStore#Load().",
	"scip-go gomod example.com/simplemod f9c7f511d060 `example.com/simplemod/b`/Nope#Save().",
	"scip-go gomod example.com/simplemod f9c7f511d060 `example.com/simplemod/b`/",
	"scip-go gomod example.com/simplemod f9c7f511d060 `example.com/simplemod/b`/FileStore#",
	"local 0",
	"local 1",
	"local 6",
	"local 7",
}

const (
	symStorer        = "scip-go gomod example.com/simplemod f9c7f511d060 `example.com/simplemod/a`/Storer#"
	symStorerSave    = "scip-go gomod example.com/simplemod f9c7f511d060 `example.com/simplemod/a`/Storer#Save."
	symStorerLoad    = "scip-go gomod example.com/simplemod f9c7f511d060 `example.com/simplemod/a`/Storer#Load."
	symFileStore     = "scip-go gomod example.com/simplemod f9c7f511d060 `example.com/simplemod/b`/FileStore#"
	symFileStoreSave = "scip-go gomod example.com/simplemod f9c7f511d060 `example.com/simplemod/b`/FileStore#Save()."
	symFileStoreLoad = "scip-go gomod example.com/simplemod f9c7f511d060 `example.com/simplemod/b`/FileStore#Load()."
	symMemStore      = "scip-go gomod example.com/simplemod f9c7f511d060 `example.com/simplemod/b`/MemStore#"
	symMemStoreSave  = "scip-go gomod example.com/simplemod f9c7f511d060 `example.com/simplemod/b`/MemStore#Save()."
	symMemStoreLoad  = "scip-go gomod example.com/simplemod f9c7f511d060 `example.com/simplemod/b`/MemStore#Load()."
	symWrapped       = "scip-go gomod example.com/simplemod f9c7f511d060 `example.com/simplemod/b`/Wrapped#"
	// NOTE: scip-go does NOT emit `Wrapped#Save().` / `Wrapped#Load().`
	// symbols at all in this fixture — promoted methods only ever appear
	// as occurrences of the underlying FileStore's own symbol, not as a
	// distinct Wrapped-qualified symbol. This is real, observed scip-go
	// behavior (see the captured list above), and it means Wrapped's
	// method-level relationships are structurally unjoinable no matter
	// what the resolver does — they MUST be dropped, not silently
	// fabricated. This is the primary "why dropped" case this test proves.
)

// parseGoSymbolDescriptor unit coverage: exercise the descriptor grammar
// directly against real scip-go output plus edge cases (type-level,
// concrete method, interface abstract method, free function, field,
// package symbol, local) so the join layer's parsing is verified
// independently of the full resolver pipeline.
func TestParseGoSymbolDescriptor(t *testing.T) {
	cases := []struct {
		name       string
		sym        string
		wantPkg    string
		wantType   string
		wantMethod string
		wantOK     bool
	}{
		{
			name:    "type-level interface symbol",
			sym:     symStorer,
			wantPkg: "example.com/simplemod/a", wantType: "Storer", wantMethod: "", wantOK: true,
		},
		{
			name:    "interface abstract method (term descriptor, no parens)",
			sym:     symStorerSave,
			wantPkg: "example.com/simplemod/a", wantType: "Storer", wantMethod: "Save", wantOK: true,
		},
		{
			name:    "concrete method (parens)",
			sym:     symFileStoreSave,
			wantPkg: "example.com/simplemod/b", wantType: "FileStore", wantMethod: "Save", wantOK: true,
		},
		{
			name:    "struct field (term descriptor) parses like a method key",
			sym:     "scip-go gomod example.com/simplemod f9c7f511d060 `example.com/simplemod/b`/FileStore#path.",
			wantPkg: "example.com/simplemod/b", wantType: "FileStore", wantMethod: "path", wantOK: true,
		},
		{
			name:   "free function has no type, no join key",
			sym:    "scip-go gomod example.com/simplemod f9c7f511d060 `example.com/simplemod/a`/UsesError().",
			wantOK: false,
		},
		{
			name:   "bare package symbol has no join key",
			sym:    "scip-go gomod example.com/simplemod f9c7f511d060 `example.com/simplemod/a`/",
			wantOK: false,
		},
		{
			name:   "local symbol is not a 5-part descriptor",
			sym:    "local 0",
			wantOK: false,
		},
		{
			name:   "malformed string",
			sym:    "not a scip symbol",
			wantOK: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pkg, typ, method, ok := parseGoSymbolDescriptor(tc.sym)
			assert.Equal(t, tc.wantOK, ok)
			if tc.wantOK {
				assert.Equal(t, tc.wantPkg, pkg)
				assert.Equal(t, tc.wantType, typ)
				assert.Equal(t, tc.wantMethod, method)
			}
		})
	}
}

func TestBuildSymbolLookup(t *testing.T) {
	lk := buildSymbolLookup(fixtureKnownSymbolsComplete)

	// Type-level keys resolve for every type symbol present, including
	// Empty (buildSymbolLookup indexes whatever parses — filtering empty
	// interfaces out is the resolver's job, not the lookup's).
	got, ok := lk.byType[typeKey("example.com/simplemod/a", "Storer")]
	require.True(t, ok)
	assert.Equal(t, symStorer, got)

	got, ok = lk.byType[typeKey("example.com/simplemod/b", "Wrapped")]
	require.True(t, ok)
	assert.Equal(t, symWrapped, got)

	// Method-level keys resolve for both interface abstract methods
	// (term descriptor) and concrete methods (paren descriptor).
	got, ok = lk.byMethod[methodKey("example.com/simplemod/a", "Storer", "Save")]
	require.True(t, ok)
	assert.Equal(t, symStorerSave, got)

	got, ok = lk.byMethod[methodKey("example.com/simplemod/b", "FileStore", "Save")]
	require.True(t, ok)
	assert.Equal(t, symFileStoreSave, got)

	// Wrapped has no Save()/Load() symbol of its own in this fixture's
	// known-symbols set (see the const block comment above) — the lookup
	// must NOT contain a key for it.
	_, ok = lk.byMethod[methodKey("example.com/simplemod/b", "Wrapped", "Save")]
	assert.False(t, ok, "no Wrapped#Save() symbol exists in scip-go's real output for this fixture")

	// A symbol with no join-relevant shape (free function, package,
	// local) must not appear under any key.
	for _, rels := range []map[symbolDescriptor]string{lk.byType, lk.byMethod} {
		for _, sym := range rels {
			assert.NotEqual(t, "local 0", sym)
		}
	}
}

// TestResolveImplementations_SymbolJoin is the isolated symbol-join test
// required by the brief: given a synthetic knownSymbols list that includes
// some fixture symbols and omits others, assert emitted relationships only
// reference present symbols and the dropped count matches exactly.
func TestResolveImplementations_SymbolJoin(t *testing.T) {
	t.Run("complete known-symbols set: only Wrapped method-level relationships drop", func(t *testing.T) {
		rels, stats, err := ResolveImplementations(fixtureModule, fixtureKnownSymbolsComplete)
		require.NoError(t, err)

		// Type-level: FileStore, MemStore, Wrapped all have Storer's
		// type symbol AND their own type symbol present -> all 3 emit.
		assert.Equal(t, 3, stats.TypeLevelEmitted)

		// Method-level: FileStore x2 + MemStore x2 = 4 emit. Wrapped's 2
		// method-level relationships are dropped because scip-go's real
		// output has no Wrapped#Save()/Load() symbol to join onto.
		assert.Equal(t, 4, stats.MethodLevelEmitted)
		assert.Equal(t, 2, stats.DroppedMissingSymbol, "exactly Wrapped.Save and Wrapped.Load should drop")

		// Every emitted relationship must reference a symbol string that
		// is verbatim present in the known-symbols set.
		known := make(map[string]bool, len(fixtureKnownSymbolsComplete))
		for _, s := range fixtureKnownSymbolsComplete {
			known[s] = true
		}
		for _, r := range rels {
			assert.True(t, known[r.FromSymbol], "FromSymbol %q must be a known symbol", r.FromSymbol)
			assert.True(t, known[r.ToSymbol], "ToSymbol %q must be a known symbol", r.ToSymbol)
			assert.True(t, r.IsImplementation)
			assert.False(t, r.IsReference)
			assert.False(t, r.IsTypeDefinition)
		}

		assert.Len(t, rels, stats.TypeLevelEmitted+stats.MethodLevelEmitted)
	})

	t.Run("sparse known-symbols set: only FileStore's own symbols known, everything else drops", func(t *testing.T) {
		sparse := []string{
			symFileStore,
			symFileStoreSave,
			symFileStoreLoad,
			// Storer's own symbols deliberately omitted: even though
			// FileStore structurally implements Storer, neither the
			// type-level nor method-level relationship can be emitted
			// without the interface-side symbol also being known.
		}

		rels, stats, err := ResolveImplementations(fixtureModule, sparse)
		require.NoError(t, err)

		assert.Empty(t, rels, "no relationship can be emitted when the interface-side symbol is entirely absent")
		assert.Equal(t, 0, stats.TypeLevelEmitted)
		assert.Equal(t, 0, stats.MethodLevelEmitted)

		// Dropped count: every relationship the go/types pass produced
		// (3 type-level + 6 method-level = 9) drops because in every
		// single one, at least the interface-side symbol is missing.
		assert.Equal(t, stats.TypeLevelRelationships+stats.MethodLevelRelationships, stats.DroppedMissingSymbol)
	})

	t.Run("both sides known for FileStore only: FileStore relationships emit, MemStore/Wrapped drop", func(t *testing.T) {
		partial := []string{
			symStorer, symStorerSave, symStorerLoad,
			symFileStore, symFileStoreSave, symFileStoreLoad,
			// MemStore and Wrapped symbols omitted entirely.
		}

		rels, stats, err := ResolveImplementations(fixtureModule, partial)
		require.NoError(t, err)

		assert.Equal(t, 1, stats.TypeLevelEmitted, "only FileStore -> Storer type-level")
		assert.Equal(t, 2, stats.MethodLevelEmitted, "only FileStore.Save/Load -> Storer.Save/Load")

		for _, r := range rels {
			assert.Contains(t, r.FromSymbol, "FileStore", "the only candidate with both sides known is FileStore")
		}

		// MemStore contributes 1 type-level + 2 method-level = 3 drops.
		// Wrapped contributes 1 type-level + 2 method-level = 3 drops
		// (already partially unjoinable even in the complete set, but
		// here fully unjoinable since Wrapped's own symbols are absent
		// too).
		assert.Equal(t, 6, stats.DroppedMissingSymbol)
	})
}
