package integration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	neo4j "github.com/context-maximiser/code-graph/internal/graph"
	"github.com/context-maximiser/code-graph/internal/graph/schema"
	static "github.com/context-maximiser/code-graph/internal/ingest/scip"
	models "github.com/context-maximiser/code-graph/internal/model"
)

// reindexTestServices are the service names used by the two tests below.
// The scopeId sweep in deleteReindexTestData is the primary cleanup (both
// scopeIds are compile-time constants); matching by these names as well is
// belt-and-braces for any node a bug left without a scopeId, mirroring
// test/harness/golden_test.go's deleteFixtureData legs.
var reindexTestServices = []string{"reindex-idem-tiny-go", "reindex-route-removal-tiny-go"}

// reindexTestModuleMarkers identify the shared, FQN-merged node types
// (Symbol/Class/Interface/Module) that carry no serviceName property. Both
// tests below index a copy of the tiny-go fixture, whose go.mod module is
// "example.com/tinygo" — embedded directly in every SCIP symbol string they
// produce. Matching by this marker, not an unscoped "no remaining DEFINES"
// orphan sweep, is what keeps cleanup from ever touching a real dev graph's
// legitimately orphaned Symbols (every stdlib symbol referenced-but-undefined
// looks "orphaned" too — see deletePreviousSubgraph's own orphan query for
// the production-path version of this same distinction).
var reindexTestModuleMarkers = []string{"example.com/tinygo"}

// reindexTestScopeIDs are the scope IDs both tests below index under. Needed
// only to key the PullRequest anchor node each creates (Scope: "pr" in
// SetScope): PullRequest carries neither a serviceName nor an FQN marker, so
// neither of the two legs above would find it. createPullRequestNode derives
// its nodeKey from strings.TrimPrefix(scopeId, "pr-"); since neither ID here
// actually has that prefix, TrimPrefix is a no-op and the nodeKey is exactly
// models.PullRequestNodeKey(scopeID).
var reindexTestScopeIDs = []string{"itest-reindex-idem", "itest-reindex-route-removal"}

// setupReindexTestDB connects to Neo4j, ensures the schema exists, sweeps any
// crash residue left by a prior run of the tests in this file that never
// reached its own t.Cleanup (killed by a test timeout, panic, etc.), and
// registers the same sweep as this test's own teardown. Returns nil (causing
// the caller to t.Skip) if Neo4j isn't reachable.
func setupReindexTestDB(t *testing.T) *neo4j.Client {
	t.Helper()
	client := createTestClient(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := deleteReindexTestData(ctx, client); err != nil {
		t.Fatalf("crash-residue sweep failed: %v", err)
	}
	if err := schema.NewSchemaManager(client).CreateSchema(ctx); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer ccancel()
		if err := deleteReindexTestData(cctx, client); err != nil {
			t.Errorf("teardown cleanup failed: %v", err)
		}
		client.Close(cctx)
	})
	return client
}

// deleteReindexTestData removes every node the tests in this file create.
// See test/harness/golden_test.go's deleteFixtureData for the identical
// four-leg pattern (serviceName, Service-by-name, FQN-marker, disconnected
// APIRoute/SDKCall) and the reasoning against unscoped sweeps.
func deleteReindexTestData(ctx context.Context, client *neo4j.Client) error {
	queries := []string{
		// Everything the indexer writes carries this run's scopeId, and both
		// test scopeIds are compile-time constants used nowhere else, so this
		// one leg already covers nodes the marker-based legs below can't see —
		// e.g. stdlib Symbols (fmt.Println has neither a serviceName nor an
		// example.com/tinygo marker in its nodeKey, but does get the scopeId).
		`MATCH (n)
		 WHERE n.scopeId IN $scopeIds
		 CALL { WITH n DETACH DELETE n } IN TRANSACTIONS OF 1000 ROWS`,
		`MATCH (n)
		 WHERE n.serviceName IS NOT NULL AND n.serviceName IN $services
		 CALL { WITH n DETACH DELETE n } IN TRANSACTIONS OF 1000 ROWS`,
		`MATCH (svc:Service)
		 WHERE svc.name IN $services
		 CALL { WITH svc DETACH DELETE svc } IN TRANSACTIONS OF 1000 ROWS`,
		`MATCH (n)
		 WHERE (n:Symbol OR n:Class OR n:Interface OR n:Module OR n:APIRoute OR n:SDKCall)
		   AND any(m IN $markers WHERE n.nodeKey CONTAINS m)
		 CALL { WITH n DETACH DELETE n } IN TRANSACTIONS OF 1000 ROWS`,
		`MATCH (n)
		 WHERE (n:APIRoute OR n:SDKCall) AND NOT (n)--()
		 CALL { WITH n DETACH DELETE n } IN TRANSACTIONS OF 1000 ROWS`,
		`MATCH (pr:PullRequest)
		 WHERE pr.nodeKey IN $prNodeKeys
		 CALL { WITH pr DETACH DELETE pr } IN TRANSACTIONS OF 1000 ROWS`,
	}
	prNodeKeys := make([]string, len(reindexTestScopeIDs))
	for i, id := range reindexTestScopeIDs {
		prNodeKeys[i] = models.PullRequestNodeKey(id)
	}
	params := map[string]any{
		"scopeIds":   reindexTestScopeIDs,
		"services":   reindexTestServices,
		"markers":    reindexTestModuleMarkers,
		"prNodeKeys": prNodeKeys,
	}
	for _, q := range queries {
		if _, err := client.ExecuteQuery(ctx, q, params); err != nil {
			return err
		}
	}
	return nil
}

// TestReindexIdempotency is RFC-006 Phase 1's exit criterion for the
// idempotent-write + delete-before-write work: indexing the exact same
// project into the exact same (service, scope) twice must produce identical
// per-label node counts and per-type relationship counts. Before this unit,
// every relationship batch in scip_indexer.go used CreateRelsBatch (CREATE
// semantics), so a second index of unchanged source doubled every DEFINES/
// CONTAINS/REFERENCES/IMPLEMENTS edge (and, before an earlier fix, could even
// double CALLS edges nondeterministically). This test locks both the
// MergeRelsBatch/MergeRelationship conversion and the scope-bounded
// delete-before-write pass in deletePreviousSubgraph.
func TestReindexIdempotency(t *testing.T) {
	const scopeID = "itest-reindex-idem"
	const serviceName = "reindex-idem-tiny-go"

	client := setupReindexTestDB(t)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	repoRoot := findIntegrationRepoRoot(t)
	fixturePath := filepath.Join(repoRoot, "test", "fixtures", "tiny-go")

	// scip-go shells out inside the fixture; force GOWORK=off so it doesn't
	// inherit the parent codegraph workspace (see test/harness/golden_test.go
	// for the same guard).
	t.Setenv("GOWORK", "off")

	indexer := static.NewSCIPIndexerWithLanguage(client, serviceName, "v0.0.0", "https://example.com/reindex-idem", static.LanguageGo)
	indexer.SetScope(models.ScopeContext{Scope: "pr", ScopeID: scopeID})
	if err := indexer.ValidateEnvironment(); err != nil {
		t.Skipf("scip-go not available: %v", err)
	}
	// scip-go drops index.scip into the fixture directory; clean it up
	// regardless of which run leaves it behind last.
	defer os.Remove(filepath.Join(fixturePath, "index.scip"))

	// ---- Run 1 ----
	require.NoError(t, indexer.IndexProject(ctx, fixturePath), "first IndexProject run")
	run1Nodes := countNodesByLabel(t, ctx, client, scopeID)
	run1Rels := countRelsByType(t, ctx, client, scopeID)

	require.Greater(t, run1Nodes["Function"], int64(0), "sanity: expected Function nodes after first index")
	require.Greater(t, run1Rels["CALLS"], int64(0), "sanity: expected CALLS relationships in the tiny-go fixture")

	// ---- Run 2: same service, same scope, same source ----
	require.NoError(t, indexer.IndexProject(ctx, fixturePath), "second IndexProject run")
	run2Nodes := countNodesByLabel(t, ctx, client, scopeID)
	run2Rels := countRelsByType(t, ctx, client, scopeID)

	t.Log(renderCountTable("node labels", run1Nodes, run2Nodes))
	t.Log(renderCountTable("relationship types", run1Rels, run2Rels))

	assertCountsEqual(t, "node", run1Nodes, run2Nodes)
	assertCountsEqual(t, "relationship", run1Rels, run2Rels)

	// Called out explicitly per the historical failure mode: CALLS edges
	// must not double (or otherwise drift) on re-index.
	if run2Rels["CALLS"] != run1Rels["CALLS"] {
		t.Errorf("CALLS relationship count changed on re-index: run1=%d run2=%d", run1Rels["CALLS"], run2Rels["CALLS"])
	}
}

// TestReindexRemovesStaleAPIRoute locks a real ordering bug found while
// building deletePreviousSubgraph: it initially deleted Function/Method/File
// (the serviceScopedLabels) BEFORE trying to delete APIRoute/SDKCall, but
// the latter are found by traversing Service-CONTAINS->File-CONTAINS->fn
// -EXPOSES_API/CALLS_API->target — a traversal that no longer matches
// anything once fn/File are already gone. TestReindexIdempotency alone can't
// catch this: re-indexing identical source re-converges onto the same
// APIRoute node via MergeNode/MergeRelationship regardless of whether the
// stale-deletion step actually ran. This test changes the *source* between
// runs — removing the one call site that the structural API detector flags
// as a route — so the fix only passes if deletePreviousSubgraph actually
// tears down the now-stale APIRoute before the second index runs.
func TestReindexRemovesStaleAPIRoute(t *testing.T) {
	const scopeID = "itest-reindex-route-removal"
	const serviceName = "reindex-route-removal-tiny-go"

	client := setupReindexTestDB(t)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	repoRoot := findIntegrationRepoRoot(t)
	origFixture := filepath.Join(repoRoot, "test", "fixtures", "tiny-go")

	// Work from a temp copy: the second run rewrites main.go in place, and
	// the checked-in fixture must stay untouched.
	workDir := t.TempDir()
	copyDir(t, origFixture, workDir)

	t.Setenv("GOWORK", "off")

	indexer := static.NewSCIPIndexerWithLanguage(client, serviceName, "v0.0.0", "https://example.com/reindex-route-removal", static.LanguageGo)
	indexer.SetScope(models.ScopeContext{Scope: "pr", ScopeID: scopeID})
	if err := indexer.ValidateEnvironment(); err != nil {
		t.Skipf("scip-go not available: %v", err)
	}
	defer os.Remove(filepath.Join(workDir, "index.scip"))

	// ---- Run 1: original fixture, main.go calls formal.Shout(...) across
	// the greeter package boundary, which the structural API detector flags
	// as an exposed route (this is the same route captured in
	// test/fixtures/tiny-go/golden.json as "api:ANY:/greeter/Shout"). ----
	require.NoError(t, indexer.IndexProject(ctx, workDir), "first IndexProject run")
	run1Nodes := countNodesByLabel(t, ctx, client, scopeID)
	require.EqualValues(t, 1, run1Nodes["APIRoute"], "sanity: expected exactly 1 APIRoute after indexing the unmodified fixture")

	// ---- Run 2: same service/scope, but main.go no longer calls
	// formal.Shout(...) at all — the route-worthy call site is gone from
	// source, so a correct re-index must not leave the old APIRoute behind.
	mainGoWithoutShoutCall := `package main

import (
	"fmt"

	"example.com/tinygo/greeter"
)

func main() {
	g := greeter.NewEnglishGreeter()
	fmt.Println(greet(g, "world"))

	fmt.Println(greet(greeter.NewLoudGreeter(), "world"))
}

// greet is unexported; NewEnglishGreeter/NewLoudGreeter are exported.
func greet(g greeter.Greeter, name string) string {
	return g.Greet(name)
}
`
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "main.go"), []byte(mainGoWithoutShoutCall), 0644))

	require.NoError(t, indexer.IndexProject(ctx, workDir), "second IndexProject run (route removed from source)")
	run2Nodes := countNodesByLabel(t, ctx, client, scopeID)

	if run2Nodes["APIRoute"] != 0 {
		t.Errorf("expected the stale APIRoute to be deleted once its call site is removed from source, got %d remaining", run2Nodes["APIRoute"])
	}
}

// copyDir recursively copies src into dst (both must exist; dst is the
// target directory itself, not its parent).
func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	entries, err := os.ReadDir(src)
	require.NoError(t, err)
	for _, e := range entries {
		srcPath := filepath.Join(src, e.Name())
		dstPath := filepath.Join(dst, e.Name())
		if e.IsDir() {
			require.NoError(t, os.MkdirAll(dstPath, 0755))
			copyDir(t, srcPath, dstPath)
			continue
		}
		if e.Name() == "index.scip" || e.Name() == "golden.json" || e.Name() == "golden.json.got" {
			continue
		}
		data, err := os.ReadFile(srcPath)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(dstPath, data, 0644))
	}
}

// countNodesByLabel returns, for every label present on at least one node in
// scopeID, the count of nodes with that label. Multi-label nodes are counted
// once per label they carry (UNWIND labels(n) fans a multi-labeled node out
// to one row per label), matching how the golden harness snapshot treats
// labels.
func countNodesByLabel(t *testing.T, ctx context.Context, client *neo4j.Client, scopeID string) map[string]int64 {
	t.Helper()
	cypher := `
		MATCH (n) WHERE n.scopeId = $scopeId
		UNWIND labels(n) AS label
		RETURN label, count(*) AS c
	`
	records, err := client.ExecuteQuery(ctx, cypher, map[string]any{"scopeId": scopeID})
	require.NoError(t, err, "countNodesByLabel query failed")

	counts := make(map[string]int64, len(records))
	for _, rec := range records {
		m := rec.AsMap()
		label, _ := m["label"].(string)
		c, _ := m["c"].(int64)
		counts[label] = c
	}
	return counts
}

// countRelsByType returns, for every relationship type present on at least
// one relationship scoped to scopeID, the count of relationships of that
// type.
func countRelsByType(t *testing.T, ctx context.Context, client *neo4j.Client, scopeID string) map[string]int64 {
	t.Helper()
	cypher := `
		MATCH ()-[r]->() WHERE r.scopeId = $scopeId
		RETURN type(r) AS relType, count(*) AS c
	`
	records, err := client.ExecuteQuery(ctx, cypher, map[string]any{"scopeId": scopeID})
	require.NoError(t, err, "countRelsByType query failed")

	counts := make(map[string]int64, len(records))
	for _, rec := range records {
		m := rec.AsMap()
		relType, _ := m["relType"].(string)
		c, _ := m["c"].(int64)
		counts[relType] = c
	}
	return counts
}

func renderCountTable(title string, run1, run2 map[string]int64) string {
	keys := make(map[string]struct{}, len(run1)+len(run2))
	for k := range run1 {
		keys[k] = struct{}{}
	}
	for k := range run2 {
		keys[k] = struct{}{}
	}
	sorted := make([]string, 0, len(keys))
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)

	out := fmt.Sprintf("%s:\n%-24s %10s %10s\n", title, "name", "run1", "run2")
	for _, k := range sorted {
		out += fmt.Sprintf("%-24s %10d %10d\n", k, run1[k], run2[k])
	}
	return out
}

func assertCountsEqual(t *testing.T, kind string, run1, run2 map[string]int64) {
	t.Helper()
	keys := make(map[string]struct{}, len(run1)+len(run2))
	for k := range run1 {
		keys[k] = struct{}{}
	}
	for k := range run2 {
		keys[k] = struct{}{}
	}
	sorted := make([]string, 0, len(keys))
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)

	for _, k := range sorted {
		if run1[k] != run2[k] {
			t.Errorf("%s %q count differs after re-index: run1=%d run2=%d (expected exact match)", kind, k, run1[k], run2[k])
		}
	}
}

// findIntegrationRepoRoot walks up from this source file to the directory
// containing go.mod, mirroring test/harness/golden_test.go's findRepoRoot.
func findIntegrationRepoRoot(t *testing.T) string {
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
