package integration

import (
	"context"
	"testing"

	neo4j "github.com/context-maximiser/code-graph/internal/graph"
	"github.com/context-maximiser/code-graph/internal/graph/schema"
	static "github.com/context-maximiser/code-graph/internal/ingest/scip"
	models "github.com/context-maximiser/code-graph/internal/model"
	"github.com/stretchr/testify/require"
)

// TestCallGraphSameNameMethodsDoNotClobber is the direct regression test for
// the same-name range-clobbering bug found by RFC-013's Go differential
// oracle: two structs (FooStage, BarStage) in one file each declare a
// method named Run, calling different helpers. Before the fix,
// graphNodesByName collapsed both same-named nodes to a single map entry
// (last Cypher row wins, nondeterministically), so updateFunctionBodyRanges
// stamped one AST body range onto both nodes and findEnclosingCaller
// attributed calls made inside either method's body to whichever single
// node won the collision — misattributing one Run's call onto the other's
// node, exactly as observed on the live codegraph graph
// (internal/ingest/pipeline/stages.go's six same-named Run methods).
func TestCallGraphSameNameMethodsDoNotClobber(t *testing.T) {
	config := &neo4j.Config{
		URI:      "bolt://localhost:7687",
		Username: "neo4j",
		Password: "password123",
		Database: "neo4j",
	}
	client, err := neo4j.NewClient(*config)
	require.NoError(t, err)
	// Close via t.Cleanup, registered BEFORE the data cleanup: cleanups run
	// LIFO, so the delete still has a live client. A `defer client.Close`
	// here would fire before t.Cleanup callbacks and the delete would
	// silently no-op against a closed client — that exact bug leaked this
	// fixture's whole subgraph and was caught by the post-index
	// scope-hygiene integrity check (RFC-013 L1).
	t.Cleanup(func() { _ = client.Close(context.Background()) })

	ctx := context.Background()
	scopeID := "itest-samename-go"

	cleanup := func() {
		if _, err := client.ExecuteQuery(ctx, `MATCH (n) WHERE n.scopeId = $scope DETACH DELETE n`,
			map[string]any{"scope": scopeID}); err != nil {
			t.Errorf("scope cleanup failed (leaks %s residue): %v", scopeID, err)
		}
	}
	cleanup()
	t.Cleanup(cleanup)

	schemaManager := schema.NewSchemaManager(client)
	require.NoError(t, schemaManager.CreateSchema(ctx))

	indexer := static.NewSCIPIndexer(client, "itest-samename-go-service", "v1.0.0", "https://example.com/samename-go")
	indexer.SetScope(models.ScopeContext{Scope: "main", ScopeID: scopeID})
	require.NoError(t, indexer.ValidateEnvironment())
	require.NoError(t, indexer.IndexProject(ctx, "../fixtures/samename-go"))

	// Both Run nodes must exist, each with its OWN body range — not a
	// range stolen from (or given away to) the other same-named method.
	// FooStage.Run: func (s *FooStage) Run() int {\n\treturn helperA()\n}
	//   -> decl line 9 ("func (s *FooStage) Run() int {"), body lines 9-11.
	// BarStage.Run: decl line 15, body lines 15-17.
	// (Line numbers per test/fixtures/samename-go/stages.go; asserted via
	// range NON-overlap rather than exact numbers so the test survives
	// harmless whitespace edits to the fixture without going stale.)
	rows, err := client.ExecuteQuery(ctx, `
		MATCH (fn:Method)
		WHERE fn.serviceName = 'itest-samename-go-service' AND fn.scopeId = $scope
		  AND fn.name = 'Run'
		RETURN fn.receiverType AS recv, fn.startLine AS startLine, fn.endLine AS endLine
		ORDER BY fn.receiverType
	`, map[string]any{"scope": scopeID})
	require.NoError(t, err)
	require.Len(t, rows, 2, "expected exactly two Run methods (FooStage, BarStage), got %d", len(rows))

	ranges := make(map[string][2]int64)
	for _, r := range rows {
		m := r.AsMap()
		recv, _ := m["recv"].(string)
		start, _ := m["startLine"].(int64)
		end, _ := m["endLine"].(int64)
		ranges[recv] = [2]int64{start, end}
	}

	fooRange, ok := ranges["*FooStage"]
	require.True(t, ok, "FooStage.Run node missing or receiverType not stamped as *FooStage; got ranges=%v", ranges)
	barRange, ok := ranges["*BarStage"]
	require.True(t, ok, "BarStage.Run node missing or receiverType not stamped as *BarStage; got ranges=%v", ranges)

	require.NotEqual(t, fooRange, barRange,
		"FooStage.Run and BarStage.Run share an identical body range — the range-clobbering bug: "+
			"one node's range was overwritten by the other's, foo=%v bar=%v", fooRange, barRange)
	// Ranges must not overlap at all — each Run's own body is a disjoint
	// line span in the fixture (FooStage.Run then BarStage.Run, in order).
	require.False(t, fooRange[0] <= barRange[1] && barRange[0] <= fooRange[1],
		"FooStage.Run and BarStage.Run body ranges overlap: foo=%v bar=%v — clobbering symptom", fooRange, barRange)

	// CALLS attribution: each Run must call its OWN helper, and must NOT
	// call the other Run's helper (the exact misattribution the bug
	// produced on the live graph, e.g. IngestDocsStage#Run wrongly showing
	// gds.* calls that belong to ComputeGraphMetricsStage#Run).
	assertCalls := func(receiverType, calleeName string, wantEdge bool) {
		t.Helper()
		rows, err := client.ExecuteQuery(ctx, `
			MATCH (caller:Method {name: 'Run'})
			WHERE caller.serviceName = 'itest-samename-go-service' AND caller.scopeId = $scope
			  AND caller.receiverType = $recv
			MATCH (caller)-[:CALLS]->(callee:Function {name: $callee})
			WHERE callee.serviceName = 'itest-samename-go-service' AND callee.scopeId = $scope
			RETURN count(*) AS c
		`, map[string]any{"scope": scopeID, "recv": receiverType, "callee": calleeName})
		require.NoError(t, err)
		require.Len(t, rows, 1)
		count, _ := rows[0].AsMap()["c"].(int64)
		if wantEdge {
			require.Equal(t, int64(1), count, "%s.Run -> %s: expected the edge to exist", receiverType, calleeName)
		} else {
			require.Equal(t, int64(0), count, "%s.Run -> %s: this edge must NOT exist (misattribution)", receiverType, calleeName)
		}
	}

	assertCalls("*FooStage", "helperA", true)
	assertCalls("*BarStage", "helperB", true)
	assertCalls("*FooStage", "helperB", false)
	assertCalls("*BarStage", "helperA", false)
}
