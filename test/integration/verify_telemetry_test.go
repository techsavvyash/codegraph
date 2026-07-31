package integration

import (
	"context"
	"testing"
	"time"

	graphclient "github.com/context-maximiser/code-graph/internal/graph"
	"github.com/context-maximiser/code-graph/internal/verify/telemetry"
)

// TestVerifyTelemetry_RecordDiffList exercises RFC-013 Layer 3 end to end
// against a real Neo4j instance: build a small itest-scoped service graph,
// record an IndexRun, mutate the graph, record a second IndexRun, and assert
// exact counters, drift, and list ordering.
func TestVerifyTelemetry_RecordDiffList(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	const scopeID = "itest-verify-telemetry"
	const serviceName = "itest-svc-telemetry"
	client := createTestGraphClient(t)
	defer cleanupTestData(t, ctx, client, scopeID)

	seedTelemetryFixture(t, ctx, client, serviceName, scopeID)

	run1, err := telemetry.RecordIndexRun(ctx, client, serviceName, scopeID, "2026-08-01T00:00:00Z", "2026-08-01T00:01:00Z")
	if err != nil {
		t.Fatalf("RecordIndexRun (1st) failed: %v", err)
	}

	// --- Assert every counter exact against the fixture built above ---
	if run1.Files != 2 {
		t.Errorf("run1.Files = %d, want 2", run1.Files)
	}
	if run1.Functions != 1 {
		t.Errorf("run1.Functions = %d, want 1", run1.Functions)
	}
	if run1.Methods != 2 {
		t.Errorf("run1.Methods = %d, want 2", run1.Methods)
	}
	if run1.Symbols != 3 {
		t.Errorf("run1.Symbols = %d, want 3", run1.Symbols)
	}
	if run1.CallsEdges != 2 {
		t.Errorf("run1.CallsEdges = %d, want 2", run1.CallsEdges)
	}
	if run1.ImplementsEdges != 1 {
		t.Errorf("run1.ImplementsEdges = %d, want 1", run1.ImplementsEdges)
	}
	if run1.APIRoutes != 1 {
		t.Errorf("run1.APIRoutes = %d, want 1", run1.APIRoutes)
	}
	wantCallsPerFn := 2.0 / 3.0 // 2 calls / (1 function + 2 methods)
	if diff := run1.CallsPerFunction - wantCallsPerFn; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("run1.CallsPerFunction = %v, want %v", run1.CallsPerFunction, wantCallsPerFn)
	}
	if run1.RangeSourceDist["treesitter"] != 3 {
		t.Errorf("run1.RangeSourceDist[treesitter] = %d, want 3", run1.RangeSourceDist["treesitter"])
	}
	if run1.DetectionSourceDist["scip"] != 1 {
		t.Errorf("run1.DetectionSourceDist[scip] = %d, want 1", run1.DetectionSourceDist["scip"])
	}
	if run1.DetectionSourceDist["decorator"] != 1 {
		t.Errorf("run1.DetectionSourceDist[decorator] = %d, want 1", run1.DetectionSourceDist["decorator"])
	}
	if run1.DecoratedFunctions != 1 {
		t.Errorf("run1.DecoratedFunctions = %d, want 1", run1.DecoratedFunctions)
	}
	if run1.RunID != serviceName+"@2026-08-01T00:01:00Z" {
		t.Errorf("run1.RunID = %q, want %q", run1.RunID, serviceName+"@2026-08-01T00:01:00Z")
	}

	// A single run: DiffLastRuns must report no drift and no Previous.
	diff0, err := telemetry.DiffLastRuns(ctx, client, serviceName)
	if err != nil {
		t.Fatalf("DiffLastRuns after 1st run failed: %v", err)
	}
	if diff0.Previous != nil {
		t.Errorf("diff0.Previous = %+v, want nil after only one run", diff0.Previous)
	}
	if len(diff0.Drifts) != 0 {
		t.Errorf("diff0.Drifts = %+v, want empty after only one run", diff0.Drifts)
	}

	// --- Mutate the graph: delete one CALLS edge, add a function ---
	mutateTelemetryFixture(t, ctx, client, serviceName, scopeID)

	run2, err := telemetry.RecordIndexRun(ctx, client, serviceName, scopeID, "2026-08-01T01:00:00Z", "2026-08-01T01:01:00Z")
	if err != nil {
		t.Fatalf("RecordIndexRun (2nd) failed: %v", err)
	}
	if run2.CallsEdges != 1 {
		t.Errorf("run2.CallsEdges = %d, want 1 after deleting one CALLS edge", run2.CallsEdges)
	}
	if run2.Functions != 2 {
		t.Errorf("run2.Functions = %d, want 2 after adding one function", run2.Functions)
	}

	// --- DiffLastRuns must report the exact drift entries ---
	diff, err := telemetry.DiffLastRuns(ctx, client, serviceName)
	if err != nil {
		t.Fatalf("DiffLastRuns failed: %v", err)
	}
	if diff.Previous == nil || diff.Previous.RunID != run1.RunID {
		t.Fatalf("diff.Previous = %+v, want run1 (%s)", diff.Previous, run1.RunID)
	}
	if diff.Current == nil || diff.Current.RunID != run2.RunID {
		t.Fatalf("diff.Current = %+v, want run2 (%s)", diff.Current, run2.RunID)
	}

	driftByCounter := map[string]telemetry.Drift{}
	for _, d := range diff.Drifts {
		driftByCounter[d.Counter] = d
	}
	// callsEdges: 2 -> 1 is -50%, above the 25% threshold.
	if _, ok := driftByCounter["callsEdges"]; !ok {
		t.Errorf("expected callsEdges drift, got %+v", diff.Drifts)
	}
	// functions: 1 -> 2 is +100%, above the 25% threshold.
	if _, ok := driftByCounter["functions"]; !ok {
		t.Errorf("expected functions drift, got %+v", diff.Drifts)
	}
	// methods, files, symbols, implementsEdges, apiRoutes are all unchanged
	// and must NOT be reported.
	for _, unchanged := range []string{"files", "methods", "symbols", "implementsEdges", "apiRoutes"} {
		if _, ok := driftByCounter[unchanged]; ok {
			t.Errorf("did not expect drift on unchanged counter %q, got %+v", unchanged, driftByCounter[unchanged])
		}
	}

	// --- ListRuns: newest first ---
	runs, err := telemetry.ListRuns(ctx, client, serviceName, 10)
	if err != nil {
		t.Fatalf("ListRuns failed: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("ListRuns returned %d runs, want 2", len(runs))
	}
	if runs[0].RunID != run2.RunID {
		t.Errorf("runs[0].RunID = %q, want run2 (%s) — expected newest first", runs[0].RunID, run2.RunID)
	}
	if runs[1].RunID != run1.RunID {
		t.Errorf("runs[1].RunID = %q, want run1 (%s)", runs[1].RunID, run1.RunID)
	}

	// --- Pruning: record 12 runs total (2 already recorded + 10 more), only 10 must remain ---
	for i := 0; i < 10; i++ {
		startedAt := time.Date(2026, 8, 2, 0, i, 0, 0, time.UTC).Format(time.RFC3339)
		finishedAt := time.Date(2026, 8, 2, 0, i, 30, 0, time.UTC).Format(time.RFC3339)
		if _, err := telemetry.RecordIndexRun(ctx, client, serviceName, scopeID, startedAt, finishedAt); err != nil {
			t.Fatalf("RecordIndexRun (pruning loop, iteration %d) failed: %v", i, err)
		}
	}
	allRuns, err := telemetry.ListRuns(ctx, client, serviceName, 100)
	if err != nil {
		t.Fatalf("ListRuns after pruning loop failed: %v", err)
	}
	if len(allRuns) != 10 {
		t.Fatalf("ListRuns after 12 total RecordIndexRun calls returned %d runs, want 10 (pruned to last 10)", len(allRuns))
	}
	// The two oldest runs (run1, run2) must have been pruned away.
	for _, r := range allRuns {
		if r.RunID == run1.RunID {
			t.Errorf("run1 (%s) should have been pruned but is still present", run1.RunID)
		}
	}
}

// seedTelemetryFixture builds a minimal itest-scoped service graph:
//   - 2 Files
//   - 1 Function, 2 Methods (all rangeSource=treesitter)
//   - 3 Symbols, each DEFINES-linked from one of the above code nodes
//   - 2 CALLS edges with both endpoints inside the service
//   - 1 IMPLEMENTS edge (detectionSource=scip) with both endpoints inside the service
//   - 1 APIRoute EXPOSED by the Method, detectionSource=decorator
//   - decorators property set on the exposing Method (so DecoratedFunctions=1)
func seedTelemetryFixture(t *testing.T, ctx context.Context, client *graphclient.Client, serviceName, scopeID string) {
	t.Helper()

	err := client.ExecuteQueryWithoutRecords(ctx, `
		CREATE (svc:Service {nodeKey: $svcKey, scopeId: $scopeId, scope: 'main', name: $serviceName})
		CREATE (f1:File {nodeKey: $f1Key, scopeId: $scopeId, scope: 'main', serviceName: $serviceName, path: 'a.ts'})
		CREATE (f2:File {nodeKey: $f2Key, scopeId: $scopeId, scope: 'main', serviceName: $serviceName, path: 'b.ts'})

		CREATE (fn:Function {nodeKey: $fnKey, scopeId: $scopeId, scope: 'main', serviceName: $serviceName,
			name: 'helper', filePath: 'a.ts', rangeSource: 'treesitter'})
		CREATE (m1:Method {nodeKey: $m1Key, scopeId: $scopeId, scope: 'main', serviceName: $serviceName,
			name: 'findOne', filePath: 'b.ts', rangeSource: 'treesitter', decorators: ['Get::id']})
		CREATE (m2:Method {nodeKey: $m2Key, scopeId: $scopeId, scope: 'main', serviceName: $serviceName,
			name: 'save', filePath: 'b.ts', rangeSource: 'treesitter'})

		CREATE (s1:Symbol {nodeKey: $s1Key, scopeId: $scopeId, scope: 'main', symbol: 'sym1', displayName: 'helper'})
		CREATE (s2:Symbol {nodeKey: $s2Key, scopeId: $scopeId, scope: 'main', symbol: 'sym2', displayName: 'findOne'})
		CREATE (s3:Symbol {nodeKey: $s3Key, scopeId: $scopeId, scope: 'main', symbol: 'sym3', displayName: 'save'})
		CREATE (fn)-[:DEFINES]->(s1)
		CREATE (m1)-[:DEFINES]->(s2)
		CREATE (m2)-[:DEFINES]->(s3)

		CREATE (m1)-[:CALLS]->(fn)
		CREATE (m2)-[:CALLS]->(fn)
		CREATE (m2)-[:IMPLEMENTS {detectionSource: 'scip'}]->(m1)

		CREATE (route:APIRoute {nodeKey: $routeKey, scopeId: $scopeId, scope: 'main',
			method: 'GET', path: '/things/:id', detectionSource: 'decorator', framework: 'nestjs', protocol: 'HTTP'})
		CREATE (m1)-[:EXPOSES_API]->(route)
	`, map[string]interface{}{
		"scopeId":     scopeID,
		"serviceName": serviceName,
		"svcKey":      "svc:" + serviceName,
		"f1Key":       "file:" + serviceName + "/a.ts",
		"f2Key":       "file:" + serviceName + "/b.ts",
		"fnKey":       "function:" + serviceName + "/a.ts#helper",
		"m1Key":       "method:" + serviceName + "/b.ts#findOne",
		"m2Key":       "method:" + serviceName + "/b.ts#save",
		"s1Key":       "symbol:" + serviceName + "#sym1",
		"s2Key":       "symbol:" + serviceName + "#sym2",
		"s3Key":       "symbol:" + serviceName + "#sym3",
		"routeKey":    "route:GET:/things/:id",
	})
	if err != nil {
		t.Fatalf("failed to seed telemetry fixture: %v", err)
	}
}

// mutateTelemetryFixture deletes one CALLS edge (save->helper) and adds a
// second Function, so the next RecordIndexRun sees callsEdges 2->1 and
// functions 1->2.
func mutateTelemetryFixture(t *testing.T, ctx context.Context, client *graphclient.Client, serviceName, scopeID string) {
	t.Helper()

	err := client.ExecuteQueryWithoutRecords(ctx, `
		MATCH (m2:Method {nodeKey: $m2Key, scopeId: $scopeId})-[c:CALLS]->(fn:Function {nodeKey: $fnKey, scopeId: $scopeId})
		DELETE c
		WITH 1 AS _
		CREATE (fn2:Function {nodeKey: $fn2Key, scopeId: $scopeId, scope: 'main', serviceName: $serviceName,
			name: 'helper2', filePath: 'a.ts', rangeSource: 'treesitter'})
	`, map[string]interface{}{
		"scopeId":     scopeID,
		"serviceName": serviceName,
		"m2Key":       "method:" + serviceName + "/b.ts#save",
		"fnKey":       "function:" + serviceName + "/a.ts#helper",
		"fn2Key":      "function:" + serviceName + "/a.ts#helper2",
	})
	if err != nil {
		t.Fatalf("failed to mutate telemetry fixture: %v", err)
	}
}
