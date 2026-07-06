package harness_test

import (
	"context"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	neo4j "github.com/context-maximiser/code-graph/internal/graph"
	schema "github.com/context-maximiser/code-graph/internal/graph/schema"
	static "github.com/context-maximiser/code-graph/internal/ingest/scip"
	models "github.com/context-maximiser/code-graph/internal/model"
	"github.com/context-maximiser/code-graph/test/harness"
	"github.com/stretchr/testify/require"
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
	t.Cleanup(func() {
		// The test's own client is already closed by its defer when cleanups
		// run — dial a short-lived one.
		cctx := context.Background()
		cclient, err := neo4j.NewClient(neo4jTestConfig())
		if err != nil {
			t.Errorf("fixture cleanup: connect: %v", err)
			return
		}
		defer cclient.Close(cctx)
		if err := deleteFixtureData(cctx, cclient); err != nil {
			t.Errorf("fixture cleanup: %v", err)
		}
	})

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
	t.Cleanup(func() {
		// The test's own client is already closed by its defer when cleanups
		// run — dial a short-lived one.
		cctx := context.Background()
		cclient, err := neo4j.NewClient(neo4jTestConfig())
		if err != nil {
			t.Errorf("fixture cleanup: connect: %v", err)
			return
		}
		defer cclient.Close(cctx)
		if err := deleteFixtureData(cctx, cclient); err != nil {
			t.Errorf("fixture cleanup: %v", err)
		}
	})

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
	t.Cleanup(func() {
		// The test's own client is already closed by its defer when cleanups
		// run — dial a short-lived one.
		cctx := context.Background()
		cclient, err := neo4j.NewClient(neo4jTestConfig())
		if err != nil {
			t.Errorf("fixture cleanup: connect: %v", err)
			return
		}
		defer cclient.Close(cctx)
		if err := deleteFixtureData(cctx, cclient); err != nil {
			t.Errorf("fixture cleanup: %v", err)
		}
	})

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

// TestVerifyNoFixtureLeaks asserts that the golden tests cleaned up after
// themselves (each registers a t.Cleanup). Declared after them, so by the time
// it runs every fixture node must be gone. Uses the same predicates as the
// cleanup itself so the check cannot drift.
func TestVerifyNoFixtureLeaks(t *testing.T) {
	ctx := context.Background()
	client := connectNeo4j(t, ctx)
	defer client.Close(ctx)

	count, sample, err := countFixtureLeftovers(ctx, client)
	require.NoError(t, err, "leftover query failed")
	for _, s := range sample {
		t.Logf("leaked fixture node: %s", s)
	}
	require.Zero(t, count, "fixture nodes leaked into the shared database")
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

// fixtureServices are the service names the golden tests index under.
// Polyglot indexing derives sub-services ("polyglot/backend", "polyglot/frontend"),
// so matching must cover both the exact name and the "<name>/" prefix.
var fixtureServices = []string{"tinygo", "tinyts", "polyglot"}

// fixtureModuleMarkers are module identifiers embedded in the nodeKeys of the
// shared, FQN-merged node types (Symbol/Class/Interface/Module) that carry no
// serviceName property. Deleting by marker keeps the cleanup strictly
// fixture-scoped — an unscoped "orphaned Symbol" sweep would destroy every
// externally-referenced Symbol in a real dev graph (stdlib symbols are
// referenced but never defined, so they are all "orphans").
var fixtureModuleMarkers = []string{"example.com/tinygo", "example.com/polyglot/", "tiny-ts", "tiny-polyglot-frontend"}

// deleteFixtureData removes every node the golden fixtures create. Three legs:
// nodes carrying a fixture serviceName (including sub-services), the Service
// nodes themselves, and marker-scoped shared nodes. Each leg matches nodes
// directly by property instead of traversing CONTAINS* — deleting via a
// variable-length pattern can remove an intermediate node before paths through
// it are expanded, stranding its descendants (observed with polyglot
// sub-service trees).
func deleteFixtureData(ctx context.Context, client *neo4j.Client) error {
	queries := []string{
		`MATCH (n)
		 WHERE n.serviceName IS NOT NULL
		   AND any(svc IN $services WHERE n.serviceName = svc OR n.serviceName STARTS WITH svc + '/')
		 CALL { WITH n DETACH DELETE n } IN TRANSACTIONS OF 1000 ROWS`,
		`MATCH (svc:Service)
		 WHERE any(s IN $services WHERE svc.name = s OR svc.name STARTS WITH s + '/')
		 CALL { WITH svc DETACH DELETE svc } IN TRANSACTIONS OF 1000 ROWS`,
		`MATCH (n)
		 WHERE (n:Symbol OR n:Class OR n:Interface OR n:Module OR n:APIRoute OR n:SDKCall)
		   AND any(m IN $markers WHERE n.nodeKey CONTAINS m)
		 CALL { WITH n DETACH DELETE n } IN TRANSACTIONS OF 1000 ROWS`,
		// Structural APIRoute/SDKCall nodes carry no serviceName and key on
		// bare paths ("api:ANY:/greeter/Shout"), so the legs above can't
		// attribute them. They only exist alongside their EXPOSES_API/CALLS_API
		// edges — once the fixture functions are gone, fully-disconnected ones
		// are garbage by construction (routes shared with a live service keep
		// their edges and survive).
		`MATCH (n)
		 WHERE (n:APIRoute OR n:SDKCall) AND NOT (n)--()
		 CALL { WITH n DETACH DELETE n } IN TRANSACTIONS OF 1000 ROWS`,
	}
	params := map[string]any{"services": fixtureServices, "markers": fixtureModuleMarkers}
	for _, q := range queries {
		if _, err := client.ExecuteQuery(ctx, q, params); err != nil {
			return err
		}
	}
	return nil
}

// countFixtureLeftovers returns how many fixture-owned nodes remain, using the
// same predicates as deleteFixtureData so the check cannot drift from the
// cleanup.
func countFixtureLeftovers(ctx context.Context, client *neo4j.Client) (int64, []string, error) {
	cypher := `
		MATCH (n)
		WHERE (n.serviceName IS NOT NULL
		       AND any(svc IN $services WHERE n.serviceName = svc OR n.serviceName STARTS WITH svc + '/'))
		   OR (n:Service AND any(s IN $services WHERE n.name = s OR n.name STARTS WITH s + '/'))
		   OR ((n:Symbol OR n:Class OR n:Interface OR n:Module OR n:APIRoute OR n:SDKCall)
		       AND any(m IN $markers WHERE n.nodeKey CONTAINS m))
		   OR ((n:APIRoute OR n:SDKCall) AND NOT (n)--())
		RETURN count(n) AS c, collect(coalesce(n.nodeKey, n.name))[..10] AS sample`
	params := map[string]any{"services": fixtureServices, "markers": fixtureModuleMarkers}
	records, err := client.ExecuteQuery(ctx, cypher, params)
	if err != nil || len(records) == 0 {
		return 0, nil, err
	}
	m := records[0].AsMap()
	count, _ := m["c"].(int64)
	var sample []string
	if raw, ok := m["sample"].([]any); ok {
		for _, v := range raw {
			if s, ok := v.(string); ok {
				sample = append(sample, s)
			}
		}
	}
	return count, sample, nil
}

func neo4jTestConfig() neo4j.Config {
	return neo4j.Config{
		URI:      envOr("NEO4J_URI", "bolt://localhost:7687"),
		Username: envOr("NEO4J_USERNAME", "neo4j"),
		Password: envOr("NEO4J_PASSWORD", "password123"),
		Database: envOr("NEO4J_DATABASE", "neo4j"),
	}
}

func connectNeo4j(t *testing.T, ctx context.Context) *neo4j.Client {
	t.Helper()
	cfg := neo4jTestConfig()
	client, err := neo4j.NewClient(cfg)
	if err != nil {
		t.Skipf("Neo4j not reachable at %s: %v", cfg.URI, err)
	}
	return client
}

// resetGraph clears fixture leftovers (crash residue from a previous run) and
// ensures the schema exists. It deliberately does NOT wipe the whole graph —
// the golden suite runs against a shared dev database.
func resetGraph(t *testing.T, ctx context.Context, client *neo4j.Client) {
	t.Helper()
	if err := deleteFixtureData(ctx, client); err != nil {
		t.Fatalf("delete fixture data: %v", err)
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
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
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
