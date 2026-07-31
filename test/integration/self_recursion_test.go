package integration

import (
	"context"
	"testing"

	neo4j "github.com/context-maximiser/code-graph/internal/graph"
	"github.com/context-maximiser/code-graph/internal/graph/schema"
	static "github.com/context-maximiser/code-graph/internal/ingest/scip"
	models "github.com/context-maximiser/code-graph/internal/model"
	"github.com/context-maximiser/code-graph/internal/query/inference"
	"github.com/stretchr/testify/require"
)

// TestSelfRecursionSurvivesAndInDegreeExcludesSelfLoops is the direct
// regression test for RFC-013 bug #2 (self-loop CALLS drop) and its
// required companion fix (inDegree/outDegree must exclude self-loops, or
// every recursive function looks "used" regardless of external callers).
// It proves the fix all the way through to consumer behavior: a self
// -recursive function with NO external caller must still qualify as a
// GraphSeedFinder Tier-3 topological-root seed.
func TestSelfRecursionSurvivesAndInDegreeExcludesSelfLoops(t *testing.T) {
	config := &neo4j.Config{
		URI:      "bolt://localhost:7687",
		Username: "neo4j",
		Password: "password123",
		Database: "neo4j",
	}
	client, err := neo4j.NewClient(*config)
	require.NoError(t, err)
	defer client.Close(context.Background())

	ctx := context.Background()
	scopeID := "itest-selfrecursion-go"
	serviceName := "itest-selfrecursion-go-service"

	cleanup := func() {
		_, _ = client.ExecuteQuery(ctx, `MATCH (n) WHERE n.scopeId = $scope DETACH DELETE n`,
			map[string]any{"scope": scopeID})
	}
	cleanup()
	t.Cleanup(cleanup)

	schemaManager := schema.NewSchemaManager(client)
	require.NoError(t, schemaManager.CreateSchema(ctx))

	indexer := static.NewSCIPIndexer(client, serviceName, "v1.0.0", "https://example.com/selfrecursion-go")
	scope := models.ScopeContext{Scope: "main", ScopeID: scopeID}
	indexer.SetScope(scope)
	require.NoError(t, indexer.ValidateEnvironment())
	require.NoError(t, indexer.IndexProject(ctx, "../fixtures/selfrecursion-go"))

	// --- Part 1: self-loop CALLS edges survive at all ---
	selfLoopCount := func(name string) int64 {
		t.Helper()
		rows, err := client.ExecuteQuery(ctx, `
			MATCH (fn {name: $name})-[:CALLS]->(fn)
			WHERE (fn:Function OR fn:Method)
			  AND fn.serviceName = $service AND fn.scopeId = $scope
			RETURN count(*) AS c
		`, map[string]any{"name": name, "service": serviceName, "scope": scopeID})
		require.NoError(t, err)
		require.Len(t, rows, 1)
		c, _ := rows[0].AsMap()["c"].(int64)
		return c
	}
	require.Equal(t, int64(1), selfLoopCount("CalledRecursive"),
		"CalledRecursive -[:CALLS]-> CalledRecursive must exist (self-loop survives)")
	require.Equal(t, int64(1), selfLoopCount("OrphanRecursive"),
		"OrphanRecursive -[:CALLS]-> OrphanRecursive must exist (self-loop survives)")

	// --- Part 2: stored inDegree excludes the self-loop ---
	inDegree := func(name string) int64 {
		t.Helper()
		rows, err := client.ExecuteQuery(ctx, `
			MATCH (fn {name: $name})
			WHERE (fn:Function OR fn:Method)
			  AND fn.serviceName = $service AND fn.scopeId = $scope
			RETURN coalesce(fn.inDegree, -1) AS inDegree
		`, map[string]any{"name": name, "service": serviceName, "scope": scopeID})
		require.NoError(t, err)
		require.Len(t, rows, 1)
		d, _ := rows[0].AsMap()["inDegree"].(int64)
		return d
	}
	require.Equal(t, int64(1), inDegree("CalledRecursive"),
		"CalledRecursive.inDegree must be 1 (main's call only) — the self-loop must NOT inflate it")
	require.Equal(t, int64(0), inDegree("OrphanRecursive"),
		"OrphanRecursive.inDegree must be 0 — self-recursion alone is not an external caller")

	// --- Part 3: the fix reaches GraphSeedFinder's topological-root tier ---
	finder := inference.NewGraphSeedFinder(client)
	finder.SetScope(scope)
	// The scopeId filter alone (fn.scopeId = $scopeId OR fn.scopeId = 'main')
	// would also pull in every node from the live dev graph's 'main' scope;
	// the service filter narrows FindSeeds to just this fixture's data so
	// the test doesn't depend on what else happens to be indexed.
	finder.SetServiceFilter([]string{serviceName}, "")
	seeds, err := finder.FindSeeds(ctx)
	require.NoError(t, err)

	topoRootNames := make(map[string]bool)
	for _, s := range seeds {
		if s.SeedType == inference.GraphSeedTopoRoot {
			topoRootNames[s.Name] = true
		}
	}
	require.True(t, topoRootNames["OrphanRecursive"],
		"OrphanRecursive must qualify as a Tier-3 topological-root seed despite calling itself — "+
			"got topo-root seeds: %v", topoRootNames)
	require.False(t, topoRootNames["CalledRecursive"],
		"CalledRecursive must NOT qualify as a topological-root seed — it has a real external caller (main), "+
			"so inDegree must correctly read 1, not 0 — got topo-root seeds: %v", topoRootNames)
}
