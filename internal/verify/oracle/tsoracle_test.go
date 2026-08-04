package oracle

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Pure unit tests: join logic, no Neo4j, no node -----------------------

func TestTsClassFromSignature(t *testing.T) {
	cases := []struct {
		name      string
		signature string
		want      string
	}{
		{
			name:      "method with nested dir path",
			signature: "scip-typescript npm dough-gateway 0.1.0 src/dough/`dough.client.ts`/DoughHttpClient#getWorkload().",
			want:      "DoughHttpClient",
		},
		{
			name:      "type-level symbol, dirless root file",
			signature: "scip-typescript npm tiny-ts 1.0.0 src/`logger.ts`/Logger#",
			want:      "Logger",
		},
		{
			name:      "property/abstract member (trailing dot)",
			signature: "scip-typescript npm tiny-ts 1.0.0 src/`logger.ts`/Logger#log.",
			want:      "Logger",
		},
		{
			name:      "function symbol (no type container)",
			signature: "scip-typescript npm simplets 1.0.0 src/`helpers.ts`/square().",
			want:      "",
		},
		{
			name:      "no backtick at all",
			signature: "local 3",
			want:      "",
		},
		{
			name:      "empty",
			signature: "",
			want:      "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tsClassFromSignature(tc.signature)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestTSCallEndpointKey_Uniqueness(t *testing.T) {
	// Different (file, container, name) triples must never collide, and
	// identical triples must always match — this is the entire join
	// mechanism between sampled sites and graph nodes.
	a := tsCallEndpointKey("src/a.ts", "Foo", "bar")
	b := tsCallEndpointKey("src/a.ts", "Foo", "bar")
	assert.Equal(t, a, b)

	distinct := [][3]string{
		{"src/a.ts", "Foo", "bar"},
		{"src/b.ts", "Foo", "bar"},
		{"src/a.ts", "Baz", "bar"},
		{"src/a.ts", "Foo", "qux"},
		{"src/a.ts", "", "bar"},
	}
	seen := make(map[string]bool)
	for _, d := range distinct {
		k := tsCallEndpointKey(d[0], d[1], d[2])
		assert.False(t, seen[k], "unexpected key collision for %+v", d)
		seen[k] = true
	}
}

// buildFakeCallsIndex constructs the same (edges, nodeExists) shape
// fetchCallsIndex would return, from hand-built rows — this is the "pure-Go
// unit test for the join logic on hand-built edges/sites" the brief calls
// for, independent of Neo4j.
func buildFakeCallsIndex(edgeRows [][2][3]string, extraNodes [][3]string) (map[string]map[string]bool, map[string]bool) {
	edges := make(map[string]map[string]bool)
	nodeExists := make(map[string]bool)
	for _, row := range edgeRows {
		callerKey := tsCallEndpointKey(row[0][0], row[0][2], row[0][1])
		calleeKey := tsCallEndpointKey(row[1][0], row[1][2], row[1][1])
		nodeExists[callerKey] = true
		nodeExists[calleeKey] = true
		if edges[callerKey] == nil {
			edges[callerKey] = make(map[string]bool)
		}
		edges[callerKey][calleeKey] = true
	}
	for _, n := range extraNodes {
		nodeExists[tsCallEndpointKey(n[0], n[2], n[1])] = true
	}
	return edges, nodeExists
}

func TestRunTSOracle_JoinLogic_PureGo(t *testing.T) {
	// Mirrors RunTSOracle's core loop without touching Neo4j or node: builds
	// the edges/nodeExists maps directly (as fetchCallsIndex would) and a
	// slice of TSCallSite (as oracle.mjs would emit), then re-implements
	// just the classification loop to assert covered/missingNodes/recall
	// math in isolation. Each row: {file, container, name}.
	edgeRows := [][2][3]string{
		{{"src/helpers.ts", "", "sumOfSquares"}, {"src/helpers.ts", "", "square"}},
		{{"src/services.ts", "Store", "save"}, {"src/services.ts", "Logger", "log"}},
	}
	extraNodes := [][3]string{
		{"src/services.ts", "Consumer", "run"}, // exists, but has no matching CALLS edge below
	}
	edges, nodeExists := buildFakeCallsIndex(edgeRows, extraNodes)

	sites := []TSCallSite{
		// Covered: matches the edge exactly.
		{CallerFile: "src/helpers.ts", CallerName: "sumOfSquares", CalleeFile: "src/helpers.ts", CalleeName: "square"},
		// Covered: method-level match.
		{CallerFile: "src/services.ts", CallerContainer: "Store", CallerName: "save", CalleeFile: "src/services.ts", CalleeContainer: "Logger", CalleeName: "log"},
		// Both nodes exist, but no CALLS edge between them: recall gap.
		{CallerFile: "src/services.ts", CallerContainer: "Consumer", CallerName: "run", CalleeFile: "src/services.ts", CalleeContainer: "Store", CalleeName: "save"},
		// Callee node doesn't exist at all: excluded from denominator.
		{CallerFile: "src/helpers.ts", CallerName: "sumOfSquares", CalleeFile: "src/nowhere.ts", CalleeName: "ghost"},
	}

	var covered, missingNodes, gaps int
	for _, site := range sites {
		callerKey := tsCallEndpointKey(site.CallerFile, site.CallerName, site.CallerContainer)
		calleeKey := tsCallEndpointKey(site.CalleeFile, site.CalleeName, site.CalleeContainer)
		if !nodeExists[callerKey] || !nodeExists[calleeKey] {
			missingNodes++
			continue
		}
		if edges[callerKey] != nil && edges[callerKey][calleeKey] {
			covered++
			continue
		}
		gaps++
	}

	assert.Equal(t, 2, covered)
	assert.Equal(t, 1, gaps)
	assert.Equal(t, 1, missingNodes)

	denominator := len(sites) - missingNodes
	require.Equal(t, 3, denominator)
	recall := float64(covered) / float64(denominator)
	assert.InDelta(t, 2.0/3.0, recall, 1e-9)
}

func TestParseTSOracleOutput(t *testing.T) {
	raw := []byte(`{
		"sites": [
			{"callerFile":"src/a.ts","callerName":"foo","callerContainer":"","callerLine":1,
			 "calleeFile":"src/a.ts","calleeName":"bar","calleeContainer":"","calleeLine":5}
		],
		"stats": {"filesScanned":1,"callSitesSeen":3,"qualifying":1,"sampled":1,
			"skippedExternal":1,"skippedAnonymousCaller":1,"skippedAnonymousCallee":0,
			"skippedUnresolved":0,"skippedNoEnclosure":0,"skippedSuperOrDynamic":0}
	}`)
	out, err := ParseTSOracleOutput(raw)
	require.NoError(t, err)
	require.Len(t, out.Sites, 1)
	assert.Equal(t, "foo", out.Sites[0].CallerName)
	assert.Equal(t, "bar", out.Sites[0].CalleeName)
	assert.Equal(t, 1, out.Stats.Qualifying)
	assert.Equal(t, 1, out.Stats.SkippedExternal)
}

func TestParseTSOracleOutput_Malformed(t *testing.T) {
	_, err := ParseTSOracleOutput([]byte("not json"))
	assert.Error(t, err)
}

// --- Node-dependent integration test: run oracle.mjs against the fixture --

func findUsableTypeScriptForOracle(t *testing.T) string {
	t.Helper()
	repoRoot := repoRootFromOracleTestFile(t)
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

func repoRootFromOracleTestFile(t *testing.T) string {
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

func TestOracleMjs_AgainstSimpletsFixture(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not found in PATH")
	}
	tsModule := findUsableTypeScriptForOracle(t)
	if tsModule == "" {
		t.Skip("no usable typescript install found under web/studio/node_modules or web/chat-ui/node_modules")
	}

	repoRoot := repoRootFromOracleTestFile(t)
	scriptPath := filepath.Join(repoRoot, "tools", "ts-oracle", "oracle.mjs")
	projectRoot := filepath.Join(repoRoot, "tools", "ts-oracle", "testdata", "simplets")
	outPath := filepath.Join(t.TempDir(), "out.json")

	cmd := exec.CommandContext(context.Background(), "node", scriptPath,
		"--project", projectRoot,
		"--out", outPath,
		"--ts-module", tsModule,
	)
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "oracle.mjs failed: %s", string(output))

	data, err := os.ReadFile(outPath)
	require.NoError(t, err)

	var raw map[string]any
	require.NoError(t, json.Unmarshal(data, &raw))

	parsed, err := ParseTSOracleOutput(data)
	require.NoError(t, err)

	// Exact expected counts for the fixture (see testdata/simplets/src):
	// call sites seen = 10: square(a), square(b), square(n) [doubleSquare],
	// values.map(...), square(v) [anon callback], JSON.parse(raw),
	// console.log [Logger.log], this.logger.log x2 [save, saveAll's loop],
	// this.store.save [run].
	assert.Equal(t, 2, parsed.Stats.FilesScanned)
	assert.Equal(t, 10, parsed.Stats.CallSitesSeen)
	assert.Equal(t, 6, parsed.Stats.Qualifying)
	assert.Equal(t, 6, parsed.Stats.Sampled)
	// External: values.map(...), JSON.parse(raw), console.log = 3.
	assert.Equal(t, 3, parsed.Stats.SkippedExternal)
	// Anonymous caller: square(v) inside the arrow callback passed to .map.
	assert.Equal(t, 1, parsed.Stats.SkippedAnonymousCaller)
	assert.Equal(t, 0, parsed.Stats.SkippedAnonymousCallee)
	assert.Equal(t, 0, parsed.Stats.SkippedUnresolved)
	assert.Equal(t, 0, parsed.Stats.SkippedNoEnclosure)
	assert.Equal(t, 0, parsed.Stats.SkippedSuperOrDynamic)

	require.Len(t, parsed.Sites, 6)

	type key struct{ callerName, callerContainer, calleeName, calleeContainer string }
	got := make(map[key]bool)
	for _, s := range parsed.Sites {
		got[key{s.CallerName, s.CallerContainer, s.CalleeName, s.CalleeContainer}] = true
		assert.NotEmpty(t, s.CallerFile)
		assert.NotEmpty(t, s.CalleeFile)
	}

	// Two sumOfSquares -> square sites collapse to one distinct key (both
	// calls inside sumOfSquares target square) but both are present in the
	// raw Sites slice — assert the expected distinct relationships exist.
	assert.True(t, got[key{"sumOfSquares", "", "square", ""}], "sumOfSquares -> square")
	assert.True(t, got[key{"doubleSquare", "", "square", ""}], "doubleSquare -> square")
	assert.True(t, got[key{"save", "Store", "log", "Logger"}], "Store.save -> Logger.log")
	assert.True(t, got[key{"saveAll", "Store", "log", "Logger"}], "Store.saveAll -> Logger.log")
	assert.True(t, got[key{"run", "Consumer", "save", "Store"}], "Consumer.run -> Store.save")

	// Never mention the anonymous callback or external calls as named sites.
	for _, s := range parsed.Sites {
		assert.NotEqual(t, "usesAnonymousCallback", s.CallerName)
		assert.NotEqual(t, "usesExternalCall", s.CallerName)
		assert.NotEqual(t, "parse", s.CalleeName)
		assert.NotEqual(t, "map", s.CalleeName)
	}
}

func TestOracleMjs_UsageError_ExitCode1(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not found in PATH")
	}
	repoRoot := repoRootFromOracleTestFile(t)
	scriptPath := filepath.Join(repoRoot, "tools", "ts-oracle", "oracle.mjs")

	cmd := exec.CommandContext(context.Background(), "node", scriptPath)
	err := cmd.Run()
	require.Error(t, err)

	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, 1, exitErr.ExitCode())
}

func TestOracleMjs_MissingTypeScript_ExitCode2(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not found in PATH")
	}
	repoRoot := repoRootFromOracleTestFile(t)
	scriptPath := filepath.Join(repoRoot, "tools", "ts-oracle", "oracle.mjs")
	projectRoot := filepath.Join(repoRoot, "tools", "ts-oracle", "testdata", "simplets")
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
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, 2, exitErr.ExitCode())
}

func TestRunTSOracleScript_EnvironmentMissing(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not found in PATH")
	}
	repoRoot := repoRootFromOracleTestFile(t)
	scriptPath := filepath.Join(repoRoot, "tools", "ts-oracle", "oracle.mjs")
	projectRoot := filepath.Join(repoRoot, "tools", "ts-oracle", "testdata", "simplets")

	_, err := RunTSOracleScript(context.Background(), scriptPath, projectRoot, 0, 30*time.Second)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrTSOracleEnvironmentMissing)
}
