package census

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/context-maximiser/code-graph/internal/verify"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Tree-sitter counting on the shared ts-oracle simplets fixture --------

func simpletsRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err)
	dir := wd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(dir, "tools", "ts-oracle", "testdata", "simplets")
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not find repo root (go.mod) starting from %s", wd)
		}
		dir = parent
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

func TestWalkProject_SimpletsFixture(t *testing.T) {
	root := simpletsRoot(t)
	files, err := WalkProject(root)
	require.NoError(t, err)

	byPath := make(map[string]FileCensus)
	for _, f := range files {
		byPath[f.RelPath] = f
	}

	require.Contains(t, byPath, "src/helpers.ts")
	require.Contains(t, byPath, "src/services.ts")

	// helpers.ts: square, sumOfSquares, doubleSquare (const-bound arrow),
	// usesAnonymousCallback, usesExternalCall = 5 named declarations. The
	// inline `(v) => square(v))` passed to .map is anonymous and must NOT
	// be counted.
	assert.Equal(t, 5, byPath["src/helpers.ts"].Declared)
	assert.False(t, byPath["src/helpers.ts"].HasErrors)

	// services.ts: Logger.log, Store.save, Store.saveAll, Consumer.run = 4
	// named methods.
	assert.Equal(t, 4, byPath["src/services.ts"].Declared)
	assert.False(t, byPath["src/services.ts"].HasErrors)

	// No other TS/JS files exist in the fixture, and package.json/tsconfig
	// carry no tree-sitter grammar, so exactly 2 files should be reported.
	assert.Len(t, files, 2)
}

func TestWalkProject_ExcludesNodeModulesAndFixtureDirs(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, "src", "app.ts"), "export function real() { return 1; }\n")
	writeFile(t, filepath.Join(tmp, "node_modules", "pkg", "index.ts"), "export function ignored() { return 1; }\n")
	writeFile(t, filepath.Join(tmp, "dist", "app.js"), "function ignored() { return 1; }\n")
	writeFile(t, filepath.Join(tmp, "src", "testdata", "fixture.ts"), "function ignored() { return 1; }\n")

	files, err := WalkProject(tmp)
	require.NoError(t, err)

	var paths []string
	for _, f := range files {
		paths = append(paths, f.RelPath)
	}
	assert.Contains(t, paths, "src/app.ts")
	assert.NotContains(t, paths, "node_modules/pkg/index.ts")
	assert.NotContains(t, paths, "dist/app.js")
	assert.NotContains(t, paths, "src/testdata/fixture.ts")
}

func TestWalkProject_IncludesTestFiles(t *testing.T) {
	// Real test files (as opposed to fixture *directories*) must stay in
	// scope — this mirrors shouldExcludePath in the SCIP indexer, which
	// only excludes testdata/ and fixtures/ directories, not *.test.ts
	// files living alongside source.
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, "src", "app.test.ts"), "export function testHelper() { return 1; }\n")

	files, err := WalkProject(tmp)
	require.NoError(t, err)
	require.Len(t, files, 1)
	assert.Equal(t, "src/app.test.ts", files[0].RelPath)
	assert.Equal(t, 1, files[0].Declared)
}

func TestWalkProject_SkipsGoFiles(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, "main.go"), "package main\nfunc main() {}\n")

	files, err := WalkProject(tmp)
	require.NoError(t, err)
	assert.Empty(t, files, "go files have no registered tree-sitter grammar in this package's usage — must be skipped, not miscounted")
}

// --- Pure join/compare tests, no filesystem or Neo4j -----------------------

func TestCompareFiles(t *testing.T) {
	declared := []FileCensus{
		{RelPath: "src/a.ts", Declared: 3},
		{RelPath: "src/b.ts", Declared: 2},
		{RelPath: "src/c.ts", Declared: 5},
		{RelPath: "src/empty.ts", Declared: 0}, // must be excluded entirely
	}
	graphCounts := map[string]int{
		"src/a.ts": 3, // pass: exact match
		"src/b.ts": 1, // warn: partial dropout (1 < 2)
		// src/c.ts: File node exists, zero functions -> fail: whole-file dropout
	}
	knownFiles := map[string]bool{
		"src/a.ts": true,
		"src/b.ts": true,
		"src/c.ts": true,
	}

	statuses := CompareFiles(declared, graphCounts, knownFiles)
	require.Len(t, statuses, 3, "src/empty.ts (0 declared) must be excluded")

	byPath := make(map[string]FileStatus)
	for _, s := range statuses {
		byPath[s.FilePath] = s
	}

	assert.Equal(t, verify.StatusPass, byPath["src/a.ts"].Status)
	assert.Equal(t, verify.StatusWarn, byPath["src/b.ts"].Status)
	assert.Equal(t, verify.StatusFail, byPath["src/c.ts"].Status)
	assert.Equal(t, 0, byPath["src/c.ts"].Indexed)
}

func TestCompareFiles_UnindexedFileIsWarnNotFail(t *testing.T) {
	// A file with declarations but NO File node was never seen by the
	// indexer — usually the project's own excludes (khaata's tsconfig
	// excludes **/*.spec.ts and api/). That's a warn to verify, not a
	// whole-file dropout fail.
	declared := []FileCensus{{RelPath: "src/x.spec.ts", Declared: 10}}
	graphCounts := map[string]int{}
	knownFiles := map[string]bool{} // not in graph at all

	statuses := CompareFiles(declared, graphCounts, knownFiles)
	require.Len(t, statuses, 1)
	assert.Equal(t, verify.StatusWarn, statuses[0].Status)
	assert.False(t, statuses[0].InGraph)

	report := BuildReport("svc", statuses, 5)
	require.Len(t, report.Checks, 4)
	assert.Equal(t, verify.StatusPass, report.Checks[1].Status, "whole-file dropouts must stay clean")
	assert.Equal(t, int64(0), report.Checks[1].Count)
	notIndexed := report.Checks[3]
	assert.Equal(t, "census: files not indexed", notIndexed.Name)
	assert.Equal(t, verify.StatusWarn, notIndexed.Status)
	assert.Equal(t, int64(1), notIndexed.Count)
	require.Len(t, notIndexed.Samples, 1)
	assert.Contains(t, notIndexed.Samples[0], "src/x.spec.ts")
	assert.Contains(t, notIndexed.Samples[0], "no File node")
}

func TestCompareFiles_ExtraGraphNodesStillPass(t *testing.T) {
	// More graph nodes than tree-sitter found (promotions, overloads
	// counted as separate nodes, etc.) is fine — never a failure signal.
	declared := []FileCensus{{RelPath: "src/a.ts", Declared: 2}}
	graphCounts := map[string]int{"src/a.ts": 7}

	statuses := CompareFiles(declared, graphCounts, map[string]bool{"src/a.ts": true})
	require.Len(t, statuses, 1)
	assert.Equal(t, verify.StatusPass, statuses[0].Status)
}

func TestBuildReport(t *testing.T) {
	statuses := []FileStatus{
		{FilePath: "src/a.ts", Declared: 3, Indexed: 3, Status: verify.StatusPass},
		{FilePath: "src/b.ts", Declared: 2, Indexed: 1, Status: verify.StatusWarn},
		{FilePath: "src/c.ts", Declared: 5, Indexed: 0, Status: verify.StatusFail},
	}

	report := BuildReport("my-service", statuses, 5)
	require.Len(t, report.Checks, 4)

	summary := report.Checks[0]
	assert.Equal(t, verify.StatusFail, summary.Status, "any fail promotes the summary check to fail")
	assert.Equal(t, int64(3), summary.Count)

	wholeFile := report.Checks[1]
	assert.Equal(t, verify.StatusFail, wholeFile.Status)
	assert.Equal(t, int64(1), wholeFile.Count)
	require.Len(t, wholeFile.Samples, 1)
	assert.Contains(t, wholeFile.Samples[0], "src/c.ts")
	assert.Contains(t, wholeFile.Samples[0], "5 declared, 0 indexed")

	partial := report.Checks[2]
	assert.Equal(t, verify.StatusWarn, partial.Status)
	assert.Equal(t, int64(1), partial.Count)
	require.Len(t, partial.Samples, 1)
	assert.Contains(t, partial.Samples[0], "src/b.ts")
	assert.Contains(t, partial.Samples[0], "2 declared, 1 indexed")
}

func TestBuildReport_AllPass(t *testing.T) {
	statuses := []FileStatus{
		{FilePath: "src/a.ts", Declared: 3, Indexed: 3, Status: verify.StatusPass},
	}
	report := BuildReport("svc", statuses, 5)
	pass, warn, fail := report.Counts()
	assert.Equal(t, 4, pass)
	assert.Equal(t, 0, warn)
	assert.Equal(t, 0, fail)
}

func TestBuildReport_SampleLimit(t *testing.T) {
	var statuses []FileStatus
	for i := 0; i < 10; i++ {
		statuses = append(statuses, FileStatus{
			FilePath: fmt.Sprintf("src/file%d.ts", i),
			Declared: 1, Indexed: 0, Status: verify.StatusFail,
		})
	}
	report := BuildReport("svc", statuses, 3)
	wholeFile := report.Checks[1]
	assert.Equal(t, int64(10), wholeFile.Count, "count reflects all offenders")
	assert.Len(t, wholeFile.Samples, 3, "samples respect the limit")
}
