package resolve

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Real observed scip-typescript symbol shapes, taken verbatim from the task
// brief (captured from a live NestJS graph).
const (
	symDoughClientType   = "scip-typescript npm dough-gateway 0.1.0 src/dough/`dough.client.ts`/DoughHttpClient#"
	symDoughGetWorkload  = "scip-typescript npm dough-gateway 0.1.0 src/dough/`dough.client.ts`/DoughHttpClient#getWorkload()."
	symLoggerRootType    = "scip-typescript npm tiny-ts 1.0.0 src/`logger.ts`/Logger#"
	symLoggerAbstractLog = "scip-typescript npm tiny-ts 1.0.0 src/`logger.ts`/Logger#log."
	symNestedOuterInner  = "scip-typescript npm dough-gateway 0.1.0 src/`nested.ts`/Outer#Inner#method()."
)

func TestParseTSSymbolDescriptor(t *testing.T) {
	cases := []struct {
		name        string
		sym         string
		wantRelFile string
		wantType    string
		wantMember  string
		wantOK      bool
	}{
		{
			name:        "concrete method with nested dir path",
			sym:         symDoughGetWorkload,
			wantRelFile: "src/dough/dough.client.ts",
			wantType:    "DoughHttpClient",
			wantMember:  "getWorkload",
			wantOK:      true,
		},
		{
			name:        "type-level symbol with nested dir path",
			sym:         symDoughClientType,
			wantRelFile: "src/dough/dough.client.ts",
			wantType:    "DoughHttpClient",
			wantMember:  "",
			wantOK:      true,
		},
		{
			name:        "type-level symbol, dirless root file",
			sym:         symLoggerRootType,
			wantRelFile: "src/logger.ts",
			wantType:    "Logger",
			wantMember:  "",
			wantOK:      true,
		},
		{
			name:        "abstract/property member (trailing dot, no parens), dirless root file",
			sym:         symLoggerAbstractLog,
			wantRelFile: "src/logger.ts",
			wantType:    "Logger",
			wantMember:  "log",
			wantOK:      true,
		},
		{
			name:   "nested container (Outer#Inner#method()) must be rejected, not mis-joined",
			sym:    symNestedOuterInner,
			wantOK: false,
		},
		{
			name:   "no backtick-quoted filename at all",
			sym:    "scip-typescript npm pkg 1.0.0 src/dough/dough.client.ts",
			wantOK: false,
		},
		{
			name:   "bare file symbol, no type",
			sym:    "scip-typescript npm tiny-ts 1.0.0 src/`logger.ts`/",
			wantOK: false,
		},
		{
			name:   "unterminated backtick",
			sym:    "scip-typescript npm tiny-ts 1.0.0 src/`logger.ts/Logger#",
			wantOK: false,
		},
		{
			name:   "local symbol is not a 5-part descriptor",
			sym:    "local 3",
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
			relFile, typ, member, ok := parseTSSymbolDescriptor(tc.sym)
			assert.Equal(t, tc.wantOK, ok)
			if tc.wantOK {
				assert.Equal(t, tc.wantRelFile, relFile)
				assert.Equal(t, tc.wantType, typ)
				assert.Equal(t, tc.wantMember, member)
			}
		})
	}
}

func TestBuildTSSymbolLookup(t *testing.T) {
	known := []string{
		symDoughClientType,
		symDoughGetWorkload,
		symLoggerRootType,
		symLoggerAbstractLog,
		symNestedOuterInner, // must not appear under any key: unjoinable
		"local 0",
	}
	lk := buildTSSymbolLookup(known)

	got, ok := lk.byType[tsTypeKey("src/dough/dough.client.ts", "DoughHttpClient")]
	require.True(t, ok)
	assert.Equal(t, symDoughClientType, got)

	got, ok = lk.byMember[tsMemberKey("src/dough/dough.client.ts", "DoughHttpClient", "getWorkload")]
	require.True(t, ok)
	assert.Equal(t, symDoughGetWorkload, got)

	got, ok = lk.byType[tsTypeKey("src/logger.ts", "Logger")]
	require.True(t, ok)
	assert.Equal(t, symLoggerRootType, got)

	got, ok = lk.byMember[tsMemberKey("src/logger.ts", "Logger", "log")]
	require.True(t, ok)
	assert.Equal(t, symLoggerAbstractLog, got)

	// The nested-container symbol must not be indexed under any key.
	for _, m := range []map[tsSymbolDescriptor]string{lk.byType, lk.byMember} {
		for _, sym := range m {
			assert.NotEqual(t, symNestedOuterInner, sym)
		}
	}
}

func TestJoinTSRelationships(t *testing.T) {
	t.Run("full join: both endpoints known at type and method level", func(t *testing.T) {
		parsed := &TSResolverOutput{
			Relationships: []TSRelationship{
				{FromFile: "src/impl.ts", FromType: "GoodStore", ToFile: "src/iface.ts", ToType: "Store"},
				{FromFile: "src/impl.ts", FromType: "GoodStore", FromMethod: "save", ToFile: "src/iface.ts", ToType: "Store", ToMethod: "save"},
				{FromFile: "src/impl.ts", FromType: "GoodStore", FromMethod: "load", ToFile: "src/iface.ts", ToType: "Store", ToMethod: "load"},
			},
		}
		known := []string{
			"scip-typescript npm simplets 1.0.0 src/`impl.ts`/GoodStore#",
			"scip-typescript npm simplets 1.0.0 src/`impl.ts`/GoodStore#save().",
			"scip-typescript npm simplets 1.0.0 src/`impl.ts`/GoodStore#load().",
			"scip-typescript npm simplets 1.0.0 src/`iface.ts`/Store#",
			"scip-typescript npm simplets 1.0.0 src/`iface.ts`/Store#save.",
			"scip-typescript npm simplets 1.0.0 src/`iface.ts`/Store#load.",
		}

		rels, stats := JoinTSRelationships(parsed, known)
		assert.Equal(t, 1, stats.TypeLevelEmitted)
		assert.Equal(t, 2, stats.MethodLevelEmitted)
		assert.Equal(t, 0, stats.DroppedMissingSymbol)
		require.Len(t, rels, 3)
		for _, r := range rels {
			assert.True(t, r.IsImplementation)
			assert.False(t, r.IsReference)
			assert.False(t, r.IsTypeDefinition)
		}
	})

	t.Run("missing interface side: drops counted, nothing emitted", func(t *testing.T) {
		parsed := &TSResolverOutput{
			Relationships: []TSRelationship{
				{FromFile: "src/impl.ts", FromType: "GoodStore", ToFile: "src/iface.ts", ToType: "Store"},
				{FromFile: "src/impl.ts", FromType: "GoodStore", FromMethod: "save", ToFile: "src/iface.ts", ToType: "Store", ToMethod: "save"},
			},
		}
		known := []string{
			"scip-typescript npm simplets 1.0.0 src/`impl.ts`/GoodStore#",
			"scip-typescript npm simplets 1.0.0 src/`impl.ts`/GoodStore#save().",
			// Store's symbols deliberately omitted entirely.
		}

		rels, stats := JoinTSRelationships(parsed, known)
		assert.Empty(t, rels)
		assert.Equal(t, 0, stats.TypeLevelEmitted)
		assert.Equal(t, 0, stats.MethodLevelEmitted)
		assert.Equal(t, 2, stats.DroppedMissingSymbol)
	})

	t.Run("missing method symbol only: type-level still emits, method-level drops", func(t *testing.T) {
		parsed := &TSResolverOutput{
			Relationships: []TSRelationship{
				{FromFile: "src/impl.ts", FromType: "GoodStore", ToFile: "src/iface.ts", ToType: "Store"},
				{FromFile: "src/impl.ts", FromType: "GoodStore", FromMethod: "save", ToFile: "src/iface.ts", ToType: "Store", ToMethod: "save"},
			},
		}
		known := []string{
			"scip-typescript npm simplets 1.0.0 src/`impl.ts`/GoodStore#",
			"scip-typescript npm simplets 1.0.0 src/`iface.ts`/Store#",
			// GoodStore#save() and Store#save. omitted -> method-level join fails.
		}

		rels, stats := JoinTSRelationships(parsed, known)
		require.Len(t, rels, 1)
		assert.Equal(t, 1, stats.TypeLevelEmitted)
		assert.Equal(t, 0, stats.MethodLevelEmitted)
		assert.Equal(t, 1, stats.DroppedMissingSymbol)
		assert.Equal(t, "scip-typescript npm simplets 1.0.0 src/`impl.ts`/GoodStore#", rels[0].FromSymbol)
		assert.Equal(t, "scip-typescript npm simplets 1.0.0 src/`iface.ts`/Store#", rels[0].ToSymbol)
	})

	t.Run("nil parsed output is a no-op", func(t *testing.T) {
		rels, stats := JoinTSRelationships(nil, []string{"anything"})
		assert.Nil(t, rels)
		assert.Equal(t, TSJoinStats{}, stats)
	})
}

func TestParseTSResolverOutput(t *testing.T) {
	raw := []byte(`{
		"resolver": "ts-structural",
		"tsVersion": "5.9.3",
		"relationships": [
			{"fromFile":"src/impl.ts","fromType":"GoodStore","fromMethod":"","toFile":"src/iface.ts","toType":"Store","toMethod":""}
		],
		"stats": {"interfaces":1,"classes":2,"pairsChecked":2,"typeLevel":1,"methodLevel":0,"skippedEmptyInterfaces":1}
	}`)

	out, err := ParseTSResolverOutput(raw)
	require.NoError(t, err)
	assert.Equal(t, "ts-structural", out.Resolver)
	assert.Equal(t, "5.9.3", out.TSVersion)
	require.Len(t, out.Relationships, 1)
	assert.Equal(t, "GoodStore", out.Relationships[0].FromType)
	assert.Equal(t, 1, out.Stats.Interfaces)
	assert.Equal(t, 1, out.Stats.SkippedEmptyInterfaces)
	assert.False(t, out.Stats.CapExceeded)
}

func TestParseTSResolverOutput_Malformed(t *testing.T) {
	_, err := ParseTSResolverOutput([]byte("not json"))
	assert.Error(t, err)
}

// --- Node-dependent integration test -------------------------------------

// findUsableTypeScript locates a `typescript` install usable for the
// --ts-module override, checking web/studio then web/chat-ui (in that
// order), per the task brief. Returns "" if neither is present.
func findUsableTypeScript(t *testing.T) string {
	t.Helper()
	repoRoot := repoRootFromTestFile(t)
	candidates := []string{
		filepath.Join(repoRoot, "web", "studio", "node_modules", "typescript", "lib", "typescript.js"),
		filepath.Join(repoRoot, "web", "chat-ui", "node_modules", "typescript", "lib", "typescript.js"),
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

// repoRootFromTestFile walks up from this test file's own directory to the
// repository root (identified by go.mod), since tests may run with a
// working directory anywhere.
func repoRootFromTestFile(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err)
	dir := wd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not find repo root (go.mod) starting from %s", wd)
		}
		dir = parent
	}
}

func TestResolveMjs_AgainstSimpletsFixture(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not found in PATH")
	}

	tsModule := findUsableTypeScript(t)
	if tsModule == "" {
		t.Skip("no usable typescript install found under web/studio/node_modules or web/chat-ui/node_modules")
	}
	t.Logf("using typescript module: %s", tsModule)

	repoRoot := repoRootFromTestFile(t)
	scriptPath := filepath.Join(repoRoot, "tools", "ts-resolver", "resolve.mjs")
	projectRoot := filepath.Join(repoRoot, "tools", "ts-resolver", "testdata", "simplets")

	outPath := filepath.Join(t.TempDir(), "out.json")

	cmd := exec.CommandContext(context.Background(), "node", scriptPath,
		"--project", projectRoot,
		"--out", outPath,
		"--ts-module", tsModule,
	)
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "resolve.mjs failed: %s", string(output))

	data, err := os.ReadFile(outPath)
	require.NoError(t, err)

	var raw map[string]any
	require.NoError(t, json.Unmarshal(data, &raw))

	parsed, err := ParseTSResolverOutput(data)
	require.NoError(t, err)

	assert.Equal(t, "ts-structural", parsed.Resolver)
	assert.False(t, parsed.Stats.CapExceeded)

	// Exact stats: only Store qualifies. Marker is empty (skipped);
	// Options (all-optional) and Payment (required data members, no
	// callables) are skipped for lacking a required callable member — the
	// noise filter that keeps universally-assignable shapes out of the
	// graph. 5 classes: GoodStore, ExplicitStore, BadStore, WrongSig, Outer.
	assert.Equal(t, 1, parsed.Stats.Interfaces, "only Store has a required callable member")
	assert.Equal(t, 5, parsed.Stats.Classes)
	assert.Equal(t, 1, parsed.Stats.SkippedEmptyInterfaces)
	assert.Equal(t, 2, parsed.Stats.SkippedNoRequiredCallable, "Options (all-optional) and Payment (data shape) must be skipped")
	assert.Equal(t, 5, parsed.Stats.PairsChecked, "1 interface x 5 classes")
	assert.Equal(t, 2, parsed.Stats.TypeLevel, "GoodStore and ExplicitStore both satisfy Store")
	assert.Equal(t, 4, parsed.Stats.MethodLevel, "2 implementers x 2 methods (save, load)")

	byFromType := make(map[string][]TSRelationship)
	for _, r := range parsed.Relationships {
		byFromType[r.FromType] = append(byFromType[r.FromType], r)
	}

	require.Contains(t, byFromType, "GoodStore")
	require.Contains(t, byFromType, "ExplicitStore")
	assert.NotContains(t, byFromType, "BadStore", "BadStore only implements half of Store — must not appear")
	assert.NotContains(t, byFromType, "WrongSig", "WrongSig has an incompatible save() signature — must not appear")
	assert.NotContains(t, byFromType, "Outer", "Outer has no relationship to Store at all")

	for _, name := range []string{"GoodStore", "ExplicitStore"} {
		rels := byFromType[name]
		require.Len(t, rels, 3, "%s: expect 1 type-level + 2 method-level relationships", name)

		var sawTypeLevel, sawSave, sawLoad bool
		for _, r := range rels {
			assert.Equal(t, "src/impl.ts", r.FromFile)
			assert.Equal(t, "src/iface.ts", r.ToFile)
			assert.Equal(t, "Store", r.ToType)
			switch {
			case r.FromMethod == "" && r.ToMethod == "":
				sawTypeLevel = true
			case r.FromMethod == "save" && r.ToMethod == "save":
				sawSave = true
			case r.FromMethod == "load" && r.ToMethod == "load":
				sawLoad = true
			default:
				t.Fatalf("%s: unexpected relationship %+v", name, r)
			}
		}
		assert.True(t, sawTypeLevel, "%s: missing type-level relationship", name)
		assert.True(t, sawSave, "%s: missing save method-level relationship", name)
		assert.True(t, sawLoad, "%s: missing load method-level relationship", name)
	}

	// Never mention Marker anywhere in the output.
	for _, r := range parsed.Relationships {
		assert.NotEqual(t, "Marker", r.FromType)
		assert.NotEqual(t, "Marker", r.ToType)
	}
}

func TestResolveMjs_MissingTypeScript_ExitCode2(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not found in PATH")
	}

	repoRoot := repoRootFromTestFile(t)
	scriptPath := filepath.Join(repoRoot, "tools", "ts-resolver", "resolve.mjs")
	projectRoot := filepath.Join(repoRoot, "tools", "ts-resolver", "testdata", "simplets")
	outPath := filepath.Join(t.TempDir(), "out.json")

	// Deliberately omit --ts-module: the fixture project has no node_modules
	// of its own, so typescript resolution must fail with exit code 2.
	cmd := exec.CommandContext(context.Background(), "node", scriptPath,
		"--project", projectRoot,
		"--out", outPath,
	)
	err := cmd.Run()
	require.Error(t, err)

	var exitErr *exec.ExitError
	require.True(t, errors.As(err, &exitErr))
	assert.Equal(t, 2, exitErr.ExitCode())
}

func TestRunTSResolver_EnvironmentMissing(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not found in PATH")
	}

	repoRoot := repoRootFromTestFile(t)
	scriptPath := filepath.Join(repoRoot, "tools", "ts-resolver", "resolve.mjs")
	projectRoot := filepath.Join(repoRoot, "tools", "ts-resolver", "testdata", "simplets")

	_, err := RunTSResolver(context.Background(), scriptPath, projectRoot, 30*time.Second)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrTSResolverEnvironmentMissing), "expected ErrTSResolverEnvironmentMissing, got: %v", err)
}
