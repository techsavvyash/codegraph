package harness_test

import (
	"context"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	models "github.com/context-maximiser/code-graph/libs/core-models-go"
	"github.com/context-maximiser/code-graph/libs/indexer-go/static"
	neo4j "github.com/context-maximiser/code-graph/libs/neo4j-go"
	schema "github.com/context-maximiser/code-graph/libs/schema-go"
	"github.com/context-maximiser/code-graph/test/harness"
)

var updateGolden = flag.Bool("update-golden", false, "rewrite golden files instead of comparing")

// TestGoldenTinyGo indexes the tiny-go fixture with SCIPIndexer, dumps the resulting
// graph through the snapshot harness, and compares it to test/fixtures/tiny-go/golden.json.
//
// First-run flow:
//
//	go test -update-golden ./test/harness/   # writes the golden file
//	go test ./test/harness/                  # compares
//
// Requires Neo4j on bolt://localhost:7687 (configurable via NEO4J_URI / NEO4J_PASSWORD)
// and scip-go on PATH.
func TestGoldenTinyGo(t *testing.T) {
	ctx := context.Background()
	client := connectNeo4j(t, ctx)
	defer client.Close(ctx)

	resetGraph(t, ctx, client)

	repoRoot := findRepoRoot(t)
	fixturePath := filepath.Join(repoRoot, "test", "fixtures", "tiny-go")

	// SCIPIndexer shells out to scip-go inside the fixture; force GOWORK=off so it
	// doesn't inherit the parent codegraph workspace.
	t.Setenv("GOWORK", "off")

	if _, err := exec.LookPath("scip-go"); err != nil {
		t.Skip("scip-go not installed; install: go install github.com/sourcegraph/scip-go/cmd/scip-go@latest")
	}

	indexer := static.NewSCIPIndexerWithLanguage(client, "tinygo", "v0.0.0", "https://example.com/tinygo", static.LanguageGo)
	if err := indexer.IndexProject(ctx, fixturePath); err != nil {
		t.Fatalf("IndexProject: %v", err)
	}
	// scip-go drops index.scip into the fixture directory; clean it.
	defer os.Remove(filepath.Join(fixturePath, "index.scip"))

	dumpAndCompare(t, ctx, client, filepath.Join(fixturePath, "golden.json"))
}

// TestGoldenTinyTS indexes the tiny-ts fixture with SCIPIndexer (TypeScript) and
// compares the resulting graph snapshot to test/fixtures/tiny-ts/golden.json.
//
// Requires Neo4j on bolt://localhost:7687 and scip-typescript on PATH.
func TestGoldenTinyTS(t *testing.T) {
	ctx := context.Background()
	client := connectNeo4j(t, ctx)
	defer client.Close(ctx)

	if _, err := exec.LookPath("scip-typescript"); err != nil {
		t.Skip("scip-typescript not installed; install: npm install -g @sourcegraph/scip-typescript")
	}

	resetGraph(t, ctx, client)

	repoRoot := findRepoRoot(t)
	fixturePath := filepath.Join(repoRoot, "test", "fixtures", "tiny-ts")
	ensureNodeModules(t, fixturePath)

	indexer := static.NewSCIPIndexerWithLanguage(client, "tinyts", "v0.0.0", "https://example.com/tinyts", static.LanguageTypeScript)
	if err := indexer.IndexProject(ctx, fixturePath); err != nil {
		t.Fatalf("IndexProject: %v", err)
	}
	defer os.Remove(filepath.Join(fixturePath, "index.scip"))

	dumpAndCompare(t, ctx, client, filepath.Join(fixturePath, "golden.json"))
}

// TestGoldenTinyPolyglot indexes the tiny-polyglot fixture (Go backend +
// TypeScript frontend) via IndexProjectPolyglot and compares the merged graph
// snapshot to test/fixtures/tiny-polyglot/golden.json.
//
// Requires Neo4j, scip-go, and scip-typescript on PATH.
func TestGoldenTinyPolyglot(t *testing.T) {
	ctx := context.Background()
	client := connectNeo4j(t, ctx)
	defer client.Close(ctx)

	if _, err := exec.LookPath("scip-go"); err != nil {
		t.Skip("scip-go not installed")
	}
	if _, err := exec.LookPath("scip-typescript"); err != nil {
		t.Skip("scip-typescript not installed")
	}

	resetGraph(t, ctx, client)

	repoRoot := findRepoRoot(t)
	fixturePath := filepath.Join(repoRoot, "test", "fixtures", "tiny-polyglot")
	ensureNodeModules(t, filepath.Join(fixturePath, "frontend"))

	t.Setenv("GOWORK", "off")

	indexer := static.NewSCIPIndexerWithLanguage(client, "polyglot", "v0.0.0", "https://example.com/polyglot", static.LanguageGo)
	if err := indexer.IndexProjectPolyglot(ctx, fixturePath); err != nil {
		t.Fatalf("IndexProjectPolyglot: %v", err)
	}
	defer os.Remove(filepath.Join(fixturePath, "backend", "index.scip"))
	defer os.Remove(filepath.Join(fixturePath, "frontend", "index.scip"))

	dumpAndCompare(t, ctx, client, filepath.Join(fixturePath, "golden.json"))
}

// dumpAndCompare snapshots the main scope from Neo4j and compares (or rewrites)
// the canonical JSON at goldenPath.
func dumpAndCompare(t *testing.T, ctx context.Context, client *neo4j.Client, goldenPath string) {
	t.Helper()
	snapshot, err := harness.Dump(ctx, client, harness.Options{
		ScopeID: models.ScopeMain,
	})
	if err != nil {
		t.Fatalf("Dump: %v", err)
	}
	got, err := snapshot.MarshalCanonical()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	compareGolden(t, goldenPath, got)
}

// ensureNodeModules runs `npm install` in dir if node_modules is missing.
// scip-typescript needs a populated node_modules to walk type information.
func ensureNodeModules(t *testing.T, dir string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(dir, "node_modules")); err == nil {
		return
	}
	if _, err := exec.LookPath("npm"); err != nil {
		t.Skipf("npm not installed; needed to bootstrap %s", dir)
	}
	cmd := exec.Command("npm", "install", "--no-audit", "--no-fund", "--silent")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("npm install in %s failed: %v\n%s", dir, err, out)
	}
}

func connectNeo4j(t *testing.T, ctx context.Context) *neo4j.Client {
	t.Helper()
	cfg := neo4j.Config{
		URI:      envOr("NEO4J_URI", "bolt://localhost:7687"),
		Username: envOr("NEO4J_USERNAME", "neo4j"),
		Password: envOr("NEO4J_PASSWORD", "password123"),
		Database: envOr("NEO4J_DATABASE", "neo4j"),
	}
	client, err := neo4j.NewClient(cfg)
	if err != nil {
		t.Skipf("Neo4j not reachable at %s: %v", cfg.URI, err)
	}
	return client
}

func resetGraph(t *testing.T, ctx context.Context, client *neo4j.Client) {
	t.Helper()
	if _, err := client.ExecuteQuery(ctx, "MATCH (n) DETACH DELETE n", nil); err != nil {
		t.Fatalf("wipe graph: %v", err)
	}
	if err := schema.NewSchemaManager(client).CreateSchema(ctx); err != nil {
		t.Fatalf("create schema: %v", err)
	}
}

func compareGolden(t *testing.T, path string, got []byte) {
	t.Helper()
	if *updateGolden {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		t.Logf("updated golden: %s", path)
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (run with -update-golden first time)", path, err)
	}
	if string(want) != string(got) {
		gotPath := path + ".got"
		_ = os.WriteFile(gotPath, got, 0o644)
		t.Fatalf("snapshot mismatch.\n  golden: %s\n  got:    %s\nDiff with: diff %s %s", path, gotPath, path, gotPath)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func findRepoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	dir := filepath.Dir(file)
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("could not find repo root from %s", file)
	return ""
}
