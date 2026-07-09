package harness_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	neo4j "github.com/context-maximiser/code-graph/internal/graph"
	static "github.com/context-maximiser/code-graph/internal/ingest/scip"
	"github.com/stretchr/testify/require"
)

const decoyService = "itest-isolation-decoy"

// TestCrossServiceIsolationOnSharedFilePath is the regression test for the
// cross-service body-range contamination bug: File paths are service-relative,
// so two services routinely share paths like src/index.ts. The call-graph
// builders used to match files and references by (path, scopeId) alone, which
// merged both services' functions into one declaration order — each run then
// wrote the other service's line ranges and call edges.
//
// The test seeds a decoy service whose File shares tiny-ts's src/index.ts
// path, with functions positioned both BEFORE and AFTER the fixture's own
// (either side corrupts a different victim: the earlier decoy would have its
// endLine clamped by the fixture's first function; a merged order would clamp
// the fixture's greet() to the later decoy's start line). It then indexes the
// tiny-ts fixture and asserts neither side bled into the other.
func TestCrossServiceIsolationOnSharedFilePath(t *testing.T) {
	ctx := context.Background()
	client := connectNeo4j(t, ctx)
	defer client.Close(ctx)

	if _, err := exec.LookPath("scip-typescript"); err != nil {
		t.Skip("scip-typescript not installed; install: npm install -g @sourcegraph/scip-typescript")
	}

	resetGraph(t, ctx, client)
	t.Cleanup(func() {
		cctx := context.Background()
		cclient, err := neo4j.NewClient(neo4jTestConfig())
		if err != nil {
			t.Errorf("cleanup: connect: %v", err)
			return
		}
		defer cclient.Close(cctx)
		if err := deleteFixtureData(cctx, cclient); err != nil {
			t.Errorf("fixture cleanup: %v", err)
		}
		if _, err := cclient.ExecuteQuery(cctx,
			`MATCH (n) WHERE n.serviceName = $svc OR (n:Service AND n.name = $svc) DETACH DELETE n`,
			map[string]any{"svc": decoyService}); err != nil {
			t.Errorf("decoy cleanup: %v", err)
		}
	})

	// Seed the decoy: a foreign service owning its own src/index.ts, with two
	// functions bracketing the fixture's declaration lines (greet is at line 3)
	// and a Reference in that file. All carry known-good ranges that must
	// survive the fixture's indexing untouched.
	_, err := client.ExecuteQuery(ctx, `
		CREATE (s:Service {name: $svc, nodeKey: 'service:' + $svc, scopeId: 'main', scope: 'main'})
		CREATE (f:File {path: 'src/index.ts', nodeKey: 'file:' + $svc + ':src/index.ts',
		                serviceName: $svc, scopeId: 'main', scope: 'main'})
		CREATE (s)-[:CONTAINS]->(f)
		CREATE (early:Function {name: 'decoyEarlyFunc', nodeKey: 'func:' + $svc + ':early',
		                        filePath: 'src/index.ts', serviceName: $svc,
		                        scopeId: 'main', scope: 'main',
		                        startLine: 2, endLine: 4, startByte: 10, endByte: 40,
		                        signature: $svc + ' src/index.ts/decoyEarlyFunc().'})
		CREATE (late:Function {name: 'decoyLateFunc', nodeKey: 'func:' + $svc + ':late',
		                       filePath: 'src/index.ts', serviceName: $svc,
		                       scopeId: 'main', scope: 'main',
		                       startLine: 50, endLine: 55, startByte: 900, endByte: 990,
		                       signature: $svc + ' src/index.ts/decoyLateFunc().'})
		CREATE (f)-[:CONTAINS]->(early)
		CREATE (f)-[:CONTAINS]->(late)
		CREATE (sym:Symbol {nodeKey: 'symbol:' + $svc + ':decoyLateFunc', name: 'decoyLateFunc',
		                    scopeId: 'main', scope: 'main'})
		CREATE (late)<-[:DEFINES]-(sym)<-[:DEFINES]-(early)
		CREATE (ref:Reference {filePath: 'src/index.ts', serviceName: $svc,
		                       scopeId: 'main', scope: 'main', startLine: 3,
		                       nodeKey: 'ref:' + $svc + ':3'})
		CREATE (ref)-[:REFERENCES]->(sym)
	`, map[string]any{"svc": decoyService})
	require.NoError(t, err, "seed decoy service")

	repoRoot := findRepoRoot(t)
	fixturePath := filepath.Join(repoRoot, "test", "fixtures", "tiny-ts")
	ensureNodeModules(t, fixturePath)

	indexer := static.NewSCIPIndexerWithLanguage(client, "tinyts", "v0.0.0", "https://example.com/tinyts", static.LanguageTypeScript)
	require.NoError(t, indexer.IndexProject(ctx, fixturePath), "IndexProject")
	defer os.Remove(filepath.Join(fixturePath, "index.scip"))

	// Decoy ranges must be exactly as seeded: the fixture's declaration-order
	// inference must not have rewritten them.
	records, err := client.ExecuteQuery(ctx, `
		MATCH (fn:Function {serviceName: $svc})
		RETURN fn.name AS name, fn.startLine AS startLine, fn.endLine AS endLine,
		       fn.startByte AS startByte, fn.endByte AS endByte
		ORDER BY fn.startLine
	`, map[string]any{"svc": decoyService})
	require.NoError(t, err)
	require.Len(t, records, 2, "decoy functions missing — cleanup or seeding broke")

	early := records[0].AsMap()
	require.Equal(t, "decoyEarlyFunc", early["name"])
	require.EqualValues(t, 4, early["endLine"], "decoy endLine rewritten by fixture indexing")
	require.EqualValues(t, 10, early["startByte"], "decoy startByte rewritten by fixture indexing")
	late := records[1].AsMap()
	require.Equal(t, "decoyLateFunc", late["name"])
	require.EqualValues(t, 55, late["endLine"], "decoy endLine rewritten by fixture indexing")
	require.EqualValues(t, 900, late["startByte"], "decoy startByte rewritten by fixture indexing")

	// The fixture's own greet() (src/index.ts line 3) must keep its open-ended
	// tail estimate (startLine + 10000, pinned in tiny-ts/golden.json) instead
	// of being clamped to decoyLateFunc's startLine - 1 = 49 by a merged
	// declaration order.
	records, err = client.ExecuteQuery(ctx, `
		MATCH (fn:Function {serviceName: 'tinyts', name: 'greet'})
		RETURN fn.endLine AS endLine
	`, nil)
	require.NoError(t, err)
	require.Len(t, records, 1, "fixture greet() not indexed")
	require.EqualValues(t, 10003, records[0].AsMap()["endLine"],
		"fixture function endLine clamped by decoy service's declaration order")

	// No call edge may cross the service boundary in either direction: the
	// decoy Reference at line 3 sits inside greet()'s range and would have
	// produced a tinyts->decoy CALLS edge under the unbounded reference match.
	records, err = client.ExecuteQuery(ctx, `
		MATCH (a)-[r:CALLS]-(b)
		WHERE a.serviceName = $svc AND b.serviceName = 'tinyts'
		RETURN count(r) AS n
	`, map[string]any{"svc": decoyService})
	require.NoError(t, err)
	require.EqualValues(t, 0, records[0].AsMap()["n"],
		"CALLS edges crossed the service boundary")
}
