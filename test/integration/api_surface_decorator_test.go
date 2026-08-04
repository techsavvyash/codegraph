package integration

import (
	"context"
	"testing"
	"time"

	static "github.com/context-maximiser/code-graph/internal/ingest/scip"
	models "github.com/context-maximiser/code-graph/internal/model"
)

// TestAPISurfaceDetector_DecoratorRoutes exercises Strategy 4
// (detectDecoratorRoutes, RFC-005): decorator-routed handlers (NestJS
// @Controller/@Get/etc.) never produce CALLS edges, so strategies 1-3 in
// api_surface.go structurally cannot see them. This test builds a Service and
// Function/Method nodes with decorators/classDecorators properties set BY
// HAND (simulating what call_graph_generic.go's updateBodyRanges writes from
// the tree-sitter structure pass), then runs the exported
// APISurfaceDetector.Detect and asserts:
//   - An APIRoute is created for the @Get(':id') handler with the correct
//     nodeKey/method/path/detectionSource.
//   - An EXPOSES_API edge connects the handler to that route.
//   - A messaging-decorated handler gets consumesBroker=true.
//   - A scheduling-decorated handler gets scheduledTask=true.
//   - A function with decorators but no HTTP-verb decorator produces no
//     APIRoute (messaging/scheduling-only functions must not synthesize
//     routes).
func TestAPISurfaceDetector_DecoratorRoutes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const scopeID = "itest-api-decorator-rfc005"
	const serviceName = "itest-svc-decorator"
	client := createTestGraphClient(t)
	defer cleanupTestData(t, ctx, client, scopeID)

	// Service node — required by SetServiceName-scoped detection, mirroring
	// how call_graph_generic.go's Service->File->fn walk expects a Service to
	// exist, even though detectDecoratorRoutes itself only filters by
	// fn.serviceName (no Service/File hop).
	if _, err := client.MergeNode(ctx, []string{"Service"},
		map[string]interface{}{"nodeKey": "svc:" + serviceName, "scopeId": scopeID},
		map[string]interface{}{
			"nodeKey": "svc:" + serviceName, "name": serviceName, "scopeId": scopeID, "scope": "main",
		}); err != nil {
		t.Fatalf("failed to create Service: %v", err)
	}

	// findOne: @Get(':id') method inside a @Controller('users') class.
	// Simulates call_graph_generic.go's encodeDecorators output directly.
	findOneKey := "method:itest/users.controller.ts#UsersController#findOne"
	if _, err := client.MergeNode(ctx, []string{"Method"},
		map[string]interface{}{"nodeKey": findOneKey, "scopeId": scopeID},
		map[string]interface{}{
			"nodeKey": findOneKey, "scopeId": scopeID, "scope": "main",
			"name": "findOne", "serviceName": serviceName,
			"filePath":        "itest/users.controller.ts",
			"decorators":      []interface{}{"Get::id"},
			"classDecorators": []interface{}{"Controller:users"},
		}); err != nil {
		t.Fatalf("failed to create findOne: %v", err)
	}

	// handleEvt: @EventPattern('evt') — messaging only, no HTTP verb, no
	// class decorator at all (bare function-style handler).
	handleEvtKey := "function:itest/events.ts#handleEvt"
	if _, err := client.MergeNode(ctx, []string{"Function"},
		map[string]interface{}{"nodeKey": handleEvtKey, "scopeId": scopeID},
		map[string]interface{}{
			"nodeKey": handleEvtKey, "scopeId": scopeID, "scope": "main",
			"name": "handleEvt", "serviceName": serviceName,
			"filePath":   "itest/events.ts",
			"decorators": []interface{}{"EventPattern:evt"},
		}); err != nil {
		t.Fatalf("failed to create handleEvt: %v", err)
	}

	// runJob: @Cron('* * * * *') — scheduling only.
	runJobKey := "function:itest/jobs.ts#runJob"
	if _, err := client.MergeNode(ctx, []string{"Function"},
		map[string]interface{}{"nodeKey": runJobKey, "scopeId": scopeID},
		map[string]interface{}{
			"nodeKey": runJobKey, "scopeId": scopeID, "scope": "main",
			"name": "runJob", "serviceName": serviceName,
			"filePath":   "itest/jobs.ts",
			"decorators": []interface{}{"Cron:* * * * *"},
		}); err != nil {
		t.Fatalf("failed to create runJob: %v", err)
	}

	// mixed: BOTH an HTTP-verb decorator AND a scheduling decorator — proves
	// categories are independent, not mutually exclusive.
	mixedKey := "method:itest/mixed.controller.ts#MixedController#mixed"
	if _, err := client.MergeNode(ctx, []string{"Method"},
		map[string]interface{}{"nodeKey": mixedKey, "scopeId": scopeID},
		map[string]interface{}{
			"nodeKey": mixedKey, "scopeId": scopeID, "scope": "main",
			"name": "mixed", "serviceName": serviceName,
			"filePath":        "itest/mixed.controller.ts",
			"decorators":      []interface{}{"Post:refresh", "Cron:0 0 * * *"},
			"classDecorators": []interface{}{"Controller:mixed"},
		}); err != nil {
		t.Fatalf("failed to create mixed: %v", err)
	}

	// Run the exported detector — detectDecoratorRoutes is unexported
	// (package static), so this test goes through Detect() like a real
	// indexing run would.
	detector := static.NewAPISurfaceDetector(client, "")
	detector.SetScope(models.ScopeContext{Scope: "main", ScopeID: scopeID})
	detector.SetServiceName(serviceName)
	if err := detector.Detect(ctx); err != nil {
		t.Fatalf("Detect failed: %v", err)
	}

	// --- Assert the findOne APIRoute ---
	wantNodeKey := models.APIRouteNodeKey("GET", "/users/:id")
	records, err := client.ExecuteQuery(ctx, `
		MATCH (r:APIRoute {nodeKey: $nodeKey, scopeId: $scopeId})
		RETURN r.method AS method, r.path AS path, r.detectionSource AS detectionSource,
		       r.framework AS framework, r.protocol AS protocol
	`, map[string]interface{}{"nodeKey": wantNodeKey, "scopeId": scopeID})
	if err != nil {
		t.Fatalf("APIRoute query failed: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected exactly 1 APIRoute with nodeKey %s, got %d", wantNodeKey, len(records))
	}
	rm := records[0].AsMap()
	if rm["method"] != "GET" {
		t.Errorf("APIRoute.method = %v, want GET", rm["method"])
	}
	if rm["path"] != "/users/:id" {
		t.Errorf("APIRoute.path = %v, want /users/:id", rm["path"])
	}
	if rm["detectionSource"] != "decorator" {
		t.Errorf("APIRoute.detectionSource = %v, want decorator", rm["detectionSource"])
	}
	if rm["framework"] != "nestjs" {
		t.Errorf("APIRoute.framework = %v, want nestjs", rm["framework"])
	}
	if rm["protocol"] != "HTTP" {
		t.Errorf("APIRoute.protocol = %v, want HTTP", rm["protocol"])
	}

	// --- Assert the EXPOSES_API edge from findOne to that route ---
	edgeRecords, err := client.ExecuteQuery(ctx, `
		MATCH (fn:Method {nodeKey: $fnKey, scopeId: $scopeId})-[:EXPOSES_API]->(r:APIRoute {nodeKey: $routeKey, scopeId: $scopeId})
		RETURN count(*) AS cnt
	`, map[string]interface{}{"fnKey": findOneKey, "routeKey": wantNodeKey, "scopeId": scopeID})
	if err != nil {
		t.Fatalf("EXPOSES_API query failed: %v", err)
	}
	if cnt, _ := edgeRecords[0].AsMap()["cnt"].(int64); cnt != 1 {
		t.Errorf("expected exactly 1 EXPOSES_API edge findOne->route, got %v", edgeRecords[0].AsMap()["cnt"])
	}

	// --- Assert consumesBroker on handleEvt ---
	brokerRecords, err := client.ExecuteQuery(ctx, `
		MATCH (fn:Function {nodeKey: $key, scopeId: $scopeId})
		RETURN coalesce(fn.consumesBroker, false) AS consumesBroker
	`, map[string]interface{}{"key": handleEvtKey, "scopeId": scopeID})
	if err != nil {
		t.Fatalf("consumesBroker query failed: %v", err)
	}
	if consumesBroker, _ := brokerRecords[0].AsMap()["consumesBroker"].(bool); !consumesBroker {
		t.Errorf("handleEvt.consumesBroker = %v, want true", brokerRecords[0].AsMap()["consumesBroker"])
	}

	// handleEvt (messaging only, no HTTP verb decorator) must NOT get an
	// APIRoute.
	noRouteRecords, err := client.ExecuteQuery(ctx, `
		MATCH (fn:Function {nodeKey: $key, scopeId: $scopeId})
		OPTIONAL MATCH (fn)-[:EXPOSES_API]->(r:APIRoute)
		RETURN count(r) AS cnt
	`, map[string]interface{}{"key": handleEvtKey, "scopeId": scopeID})
	if err != nil {
		t.Fatalf("handleEvt route-absence query failed: %v", err)
	}
	if cnt, _ := noRouteRecords[0].AsMap()["cnt"].(int64); cnt != 0 {
		t.Errorf("handleEvt (messaging-only) must not expose an APIRoute, got %d", cnt)
	}

	// --- Assert scheduledTask on runJob ---
	schedRecords, err := client.ExecuteQuery(ctx, `
		MATCH (fn:Function {nodeKey: $key, scopeId: $scopeId})
		RETURN coalesce(fn.scheduledTask, false) AS scheduledTask
	`, map[string]interface{}{"key": runJobKey, "scopeId": scopeID})
	if err != nil {
		t.Fatalf("scheduledTask query failed: %v", err)
	}
	if scheduledTask, _ := schedRecords[0].AsMap()["scheduledTask"].(bool); !scheduledTask {
		t.Errorf("runJob.scheduledTask = %v, want true", schedRecords[0].AsMap()["scheduledTask"])
	}

	// --- Assert the mixed function: BOTH scheduledTask=true AND an APIRoute ---
	mixedNodeKey := models.APIRouteNodeKey("POST", "/mixed/refresh")
	mixedRouteRecords, err := client.ExecuteQuery(ctx, `
		MATCH (fn:Method {nodeKey: $fnKey, scopeId: $scopeId})-[:EXPOSES_API]->(r:APIRoute {nodeKey: $routeKey, scopeId: $scopeId})
		RETURN fn.scheduledTask AS scheduledTask, r.method AS method, r.path AS path
	`, map[string]interface{}{"fnKey": mixedKey, "routeKey": mixedNodeKey, "scopeId": scopeID})
	if err != nil {
		t.Fatalf("mixed function query failed: %v", err)
	}
	if len(mixedRouteRecords) != 1 {
		t.Fatalf("expected mixed function to expose APIRoute %s via EXPOSES_API, got %d matches", mixedNodeKey, len(mixedRouteRecords))
	}
	mm := mixedRouteRecords[0].AsMap()
	if scheduledTask, _ := mm["scheduledTask"].(bool); !scheduledTask {
		t.Errorf("mixed.scheduledTask = %v, want true (HTTP + Cron categories are independent)", mm["scheduledTask"])
	}
	if mm["method"] != "POST" || mm["path"] != "/mixed/refresh" {
		t.Errorf("mixed APIRoute = %+v, want method=POST path=/mixed/refresh", mm)
	}
}

// TestAPISurfaceDetector_DecoratorRoutes_PostTestCleanup verifies that
// cleanupTestData actually removes every node created under this test's
// scopeId — the constraint brief requires confirming itest-scoped writes are
// cleaned up, not just asserted as cleaned up by convention.
func TestAPISurfaceDetector_DecoratorRoutes_PostTestCleanup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const scopeID = "itest-api-decorator-cleanup-check"
	client := createTestGraphClient(t)

	// Create a throwaway node under the scope, then clean it up immediately
	// and verify zero nodes remain — this exercises the exact cleanup path
	// TestAPISurfaceDetector_DecoratorRoutes relies on via defer.
	if _, err := client.MergeNode(ctx, []string{"Function"},
		map[string]interface{}{"nodeKey": "function:itest/cleanup.ts#Probe", "scopeId": scopeID},
		map[string]interface{}{
			"nodeKey": "function:itest/cleanup.ts#Probe", "scopeId": scopeID, "scope": "main",
			"name": "Probe",
		}); err != nil {
		t.Fatalf("failed to create probe node: %v", err)
	}

	cleanupTestData(t, ctx, client, scopeID)

	records, err := client.ExecuteQuery(ctx, `
		MATCH (n {scopeId: $scopeId})
		RETURN count(n) AS cnt
	`, map[string]interface{}{"scopeId": scopeID})
	if err != nil {
		t.Fatalf("post-cleanup count query failed: %v", err)
	}
	if cnt, _ := records[0].AsMap()["cnt"].(int64); cnt != 0 {
		t.Fatalf("expected 0 nodes under scopeId %s after cleanup, got %d", scopeID, cnt)
	}
}
