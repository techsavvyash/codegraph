package integration

import (
	"context"
	"strings"
	"testing"
	"time"

	graphclient "github.com/context-maximiser/code-graph/internal/graph"
	models "github.com/context-maximiser/code-graph/internal/model"
	"github.com/context-maximiser/code-graph/internal/query"
	"github.com/context-maximiser/code-graph/internal/query/inference"
	neo4jdriver "github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// TestFlowSpineGenerator_GenerateFromAPIEndpoints_KnownPositive creates a small
// call graph by hand and verifies that GenerateFromAPIEndpoints produces a flow
// that includes all expected steps in the correct order.
func TestFlowSpineGenerator_GenerateFromAPIEndpoints_KnownPositive(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client := createTestGraphClient(t)
	defer func() {
		cleanupTestData(t, ctx, client, "itest-flows-query")
	}()

	// Create a service
	serviceName := "test-service"
	serviceProps := map[string]interface{}{
		"name":    serviceName,
		"nodeKey": "svc:test-service",
		"scopeId": "itest-flows-query",
		"scope":   "main",
	}
	_, err := client.MergeNode(ctx, []string{"Service"},
		map[string]interface{}{"nodeKey": "svc:test-service", "scopeId": "itest-flows-query"},
		serviceProps)
	if err != nil {
		t.Fatalf("failed to create Service: %v", err)
	}

	// Create an API route
	routeKey := "api:GET:/api/users"
	routeProps := map[string]interface{}{
		"nodeKey": routeKey,
		"name":    "GET /api/users",
		"method":  "GET",
		"path":    "/api/users",
		"scopeId": "itest-flows-query",
		"scope":   "main",
	}
	_, err = client.MergeNode(ctx, []string{"APIRoute"},
		map[string]interface{}{"nodeKey": routeKey, "scopeId": "itest-flows-query"},
		routeProps)
	if err != nil {
		t.Fatalf("failed to create APIRoute: %v", err)
	}

	// Create handler function
	handlerKey := "func:handler.go#GetUsers"
	handlerProps := map[string]interface{}{
		"nodeKey":     handlerKey,
		"name":        "GetUsers",
		"scopeId":     "itest-flows-query",
		"scope":       "main",
		"serviceName": serviceName,
		"filePath":    "handler.go",
	}
	_, err = client.MergeNode(ctx, []string{"Function"},
		map[string]interface{}{"nodeKey": handlerKey, "scopeId": "itest-flows-query"},
		handlerProps)
	if err != nil {
		t.Fatalf("failed to create handler Function: %v", err)
	}

	// Create function A
	fnAKey := "func:service.go#FetchData"
	fnAProps := map[string]interface{}{
		"nodeKey":     fnAKey,
		"name":        "FetchData",
		"scopeId":     "itest-flows-query",
		"scope":       "main",
		"serviceName": serviceName,
		"filePath":    "service.go",
	}
	_, err = client.MergeNode(ctx, []string{"Function"},
		map[string]interface{}{"nodeKey": fnAKey, "scopeId": "itest-flows-query"},
		fnAProps)
	if err != nil {
		t.Fatalf("failed to create fnA: %v", err)
	}

	// Create function B
	fnBKey := "func:db.go#Query"
	fnBProps := map[string]interface{}{
		"nodeKey":     fnBKey,
		"name":        "Query",
		"scopeId":     "itest-flows-query",
		"scope":       "main",
		"serviceName": serviceName,
		"filePath":    "db.go",
	}
	_, err = client.MergeNode(ctx, []string{"Function"},
		map[string]interface{}{"nodeKey": fnBKey, "scopeId": "itest-flows-query"},
		fnBProps)
	if err != nil {
		t.Fatalf("failed to create fnB: %v", err)
	}

	// Create edges: EXPOSES_API from handler to route
	err = client.ExecuteQueryWithoutRecords(ctx, `
		MATCH (h:Function {nodeKey: $handlerKey, scopeId: $scopeId})
		MATCH (r:APIRoute {nodeKey: $routeKey, scopeId: $scopeId})
		MERGE (h)-[rel:EXPOSES_API]->(r)
		SET rel.scope = $scope, rel.scopeId = $scopeId`, map[string]interface{}{
		"handlerKey": handlerKey,
		"routeKey":   routeKey,
		"scopeId":    "itest-flows-query",
		"scope":      "main",
	})
	if err != nil {
		t.Fatalf("failed to create EXPOSES_API edge: %v", err)
	}

	// Create call chain: handler -> fnA -> fnB
	err = client.ExecuteQueryWithoutRecords(ctx, `
		MATCH (h:Function {nodeKey: $handlerKey, scopeId: $scopeId})
		MATCH (fa:Function {nodeKey: $fnAKey, scopeId: $scopeId})
		MERGE (h)-[rel:CALLS]->(fa)
		SET rel.scope = $scope, rel.scopeId = $scopeId`, map[string]interface{}{
		"handlerKey": handlerKey,
		"fnAKey":     fnAKey,
		"scopeId":    "itest-flows-query",
		"scope":      "main",
	})
	if err != nil {
		t.Fatalf("failed to create CALLS edge handler->fnA: %v", err)
	}

	err = client.ExecuteQueryWithoutRecords(ctx, `
		MATCH (fa:Function {nodeKey: $fnAKey, scopeId: $scopeId})
		MATCH (fb:Function {nodeKey: $fnBKey, scopeId: $scopeId})
		MERGE (fa)-[rel:CALLS]->(fb)
		SET rel.scope = $scope, rel.scopeId = $scopeId`, map[string]interface{}{
		"fnAKey":  fnAKey,
		"fnBKey":  fnBKey,
		"scopeId": "itest-flows-query",
		"scope":   "main",
	})
	if err != nil {
		t.Fatalf("failed to create CALLS edge fnA->fnB: %v", err)
	}

	// Run flow generator
	gen := query.NewFlowSpineGenerator(client)
	gen.SetScope(models.ScopeContext{Scope: "main", ScopeID: "itest-flows-query"})

	flows, err := gen.GenerateFromAPIEndpoints(ctx, 5)
	if err != nil {
		t.Fatalf("GenerateFromAPIEndpoints failed: %v", err)
	}

	// Verify at least one flow was generated
	if len(flows) == 0 {
		t.Fatal("expected at least one flow, got none")
	}

	// Find the flow for our API route
	var flow *query.FlowSpineResult
	for i := range flows {
		if strings.Contains(flows[i].FlowName, "GET /api/users") {
			flow = &flows[i]
			break
		}
	}
	if flow == nil {
		t.Fatalf("expected flow for GET /api/users, got flows: %+v", flows)
	}

	// Verify steps include all expected nodes
	stepKeys := make(map[string]bool)
	for _, step := range flow.Steps {
		stepKeys[step.NodeKey] = true
	}

	expectedKeys := []string{routeKey, handlerKey, fnAKey, fnBKey}
	for _, key := range expectedKeys {
		if !stepKeys[key] {
			t.Errorf("expected step with nodeKey %s, got steps: %+v", key, flow.Steps)
		}
	}

	// Verify orders are strictly sequential from 0
	for i, step := range flow.Steps {
		if step.Order != i {
			t.Errorf("step %d: expected Order %d, got %d", i, i, step.Order)
		}
	}
}

// TestFlowSpineGenerator_TraceCallees_TreeConsistency creates a seed with
// more callees at distance 1 than the fanout budget allows, and callees of
// callees at distance 2. It verifies traceCallees (invoked via
// GenerateFlowFromNode) drops distance-2 steps whose ParentKey names a
// distance-1 node that got capped out by the fanout limit — the "child of a
// fanout-capped parent" case called out in the RFC-005 brief. It also checks
// Depth/ParentKey are populated correctly for the surviving steps.
func TestFlowSpineGenerator_TraceCallees_TreeConsistency(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client := createTestGraphClient(t)
	scopeID := "itest-flows-tree-consistency"
	defer cleanupTestData(t, ctx, client, scopeID)

	seedKey := "func:seed.go#Seed"
	_, err := client.MergeNode(ctx, []string{"Function"},
		map[string]interface{}{"nodeKey": seedKey, "scopeId": scopeID},
		map[string]interface{}{"nodeKey": seedKey, "name": "Seed", "scopeId": scopeID, "scope": "main", "serviceName": "svc"})
	if err != nil {
		t.Fatalf("failed to create seed: %v", err)
	}

	// Three distance-1 callees, named so alphabetical ORDER BY (the traceCallees
	// query's tiebreaker) makes AChild/BChild survive a fanout cap of 2 and
	// CChild get dropped.
	distance1 := []string{"AChild", "BChild", "CChild"}
	for _, name := range distance1 {
		key := "func:child.go#" + name
		_, err := client.MergeNode(ctx, []string{"Function"},
			map[string]interface{}{"nodeKey": key, "scopeId": scopeID},
			map[string]interface{}{"nodeKey": key, "name": name, "scopeId": scopeID, "scope": "main", "serviceName": "svc"})
		if err != nil {
			t.Fatalf("failed to create %s: %v", name, err)
		}
		if err := client.ExecuteQueryWithoutRecords(ctx, `
			MATCH (s:Function {nodeKey: $seedKey, scopeId: $scopeId})
			MATCH (c:Function {nodeKey: $childKey, scopeId: $scopeId})
			MERGE (s)-[r:CALLS]->(c)
			SET r.scope = $scope, r.scopeId = $scopeId`, map[string]interface{}{
			"seedKey": seedKey, "childKey": key, "scopeId": scopeID, "scope": "main",
		}); err != nil {
			t.Fatalf("failed to create CALLS seed->%s: %v", name, err)
		}
	}

	// A distance-2 grandchild under the callee that WILL be pruned (CChild)
	// and one under a callee that WILL survive (AChild).
	orphanKey := "func:grandchild.go#OrphanGrandchild"
	survivorKey := "func:grandchild.go#SurvivorGrandchild"
	for parentName, gcKey := range map[string]string{"CChild": orphanKey, "AChild": survivorKey} {
		parentKey := "func:child.go#" + parentName
		gcName := "OrphanGrandchild"
		if gcKey == survivorKey {
			gcName = "SurvivorGrandchild"
		}
		_, err := client.MergeNode(ctx, []string{"Function"},
			map[string]interface{}{"nodeKey": gcKey, "scopeId": scopeID},
			map[string]interface{}{"nodeKey": gcKey, "name": gcName, "scopeId": scopeID, "scope": "main", "serviceName": "svc"})
		if err != nil {
			t.Fatalf("failed to create %s: %v", gcName, err)
		}
		if err := client.ExecuteQueryWithoutRecords(ctx, `
			MATCH (p:Function {nodeKey: $parentKey, scopeId: $scopeId})
			MATCH (c:Function {nodeKey: $childKey, scopeId: $scopeId})
			MERGE (p)-[r:CALLS]->(c)
			SET r.scope = $scope, r.scopeId = $scopeId`, map[string]interface{}{
			"parentKey": parentKey, "childKey": gcKey, "scopeId": scopeID, "scope": "main",
		}); err != nil {
			t.Fatalf("failed to create CALLS %s->%s: %v", parentName, gcName, err)
		}
	}

	gen := query.NewFlowSpineGenerator(client)
	gen.SetScope(models.ScopeContext{Scope: "main", ScopeID: scopeID})
	gen.SetBudget(inference.TraversalBudget{MaxDepth: 5, MaxFanout: 2, MaxSteps: 50})
	gen.SetPersist(false)

	flows, err := gen.GenerateFlowFromNode(ctx, seedKey, "Seed", "Function", 3)
	if err != nil {
		t.Fatalf("GenerateFlowFromNode failed: %v", err)
	}
	if len(flows) != 1 {
		t.Fatalf("expected 1 flow, got %d", len(flows))
	}

	byName := make(map[string]query.FlowStep)
	for _, s := range flows[0].Steps {
		byName[s.Name] = s
	}

	if _, ok := byName["CChild"]; ok {
		t.Errorf("CChild should have been dropped by the fanout cap (budget=2, 3 distance-1 candidates), got steps: %+v", flows[0].Steps)
	}
	if _, ok := byName["OrphanGrandchild"]; ok {
		t.Errorf("OrphanGrandchild's parent (CChild) was fanout-capped; it must be dropped too (tree consistency), got steps: %+v", flows[0].Steps)
	}

	aChild, ok := byName["AChild"]
	if !ok {
		t.Fatalf("AChild should have survived the fanout cap, got steps: %+v", flows[0].Steps)
	}
	if aChild.Depth != 1 {
		t.Errorf("expected AChild Depth 1, got %d", aChild.Depth)
	}
	if aChild.ParentKey != seedKey {
		t.Errorf("expected AChild ParentKey %s, got %s", seedKey, aChild.ParentKey)
	}

	survivor, ok := byName["SurvivorGrandchild"]
	if !ok {
		t.Fatalf("SurvivorGrandchild (child of surviving AChild) should be present, got steps: %+v", flows[0].Steps)
	}
	if survivor.Depth != 2 {
		t.Errorf("expected SurvivorGrandchild Depth 2, got %d", survivor.Depth)
	}
	if survivor.ParentKey != "func:child.go#AChild" {
		t.Errorf("expected SurvivorGrandchild ParentKey func:child.go#AChild, got %s", survivor.ParentKey)
	}

	seed, ok := byName["Seed"]
	if !ok {
		t.Fatalf("Seed entry step missing, got steps: %+v", flows[0].Steps)
	}
	if seed.Depth != 0 {
		t.Errorf("expected Seed (entry) Depth 0, got %d", seed.Depth)
	}
	if seed.ParentKey != "" {
		t.Errorf("expected Seed (entry) ParentKey empty, got %q", seed.ParentKey)
	}
}

// planHasOperator recursively searches a typed driver query plan for an
// operator whose type contains the given name (operator types come suffixed,
// e.g. "AllNodesScan@neo4j", so substring match is required).
func planHasOperator(plan neo4jdriver.Plan, operatorName string) bool {
	if plan == nil {
		return false
	}
	if strings.Contains(plan.Operator(), operatorName) {
		return true
	}
	for _, child := range plan.Children() {
		if planHasOperator(child, operatorName) {
			return true
		}
	}
	return false
}

// TestFlowSpineGenerator_TraceCallees_QueryPlan_UsesNodeIndexSeek verifies that
// the traceCallees spanningTree query uses NodeIndexSeek (not AllNodesScan) for
// the seed match, thanks to label expressions and nodeKey indexing.
func TestFlowSpineGenerator_TraceCallees_QueryPlan_UsesNodeIndexSeek(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client := createTestGraphClient(t)
	defer func() {
		cleanupTestData(t, ctx, client, "itest-flows-explain")
	}()

	// Create minimal test nodes so the query is valid
	scopeID := "itest-flows-explain"

	seedKey := "func:test.go#TestFunc"
	_, err := client.MergeNode(ctx, []string{"Function"},
		map[string]interface{}{"nodeKey": seedKey, "scopeId": scopeID},
		map[string]interface{}{
			"nodeKey": seedKey,
			"name":    "TestFunc",
			"scopeId": scopeID,
			"scope":   "main",
		})
	if err != nil {
		t.Fatalf("failed to create test Function: %v", err)
	}

	// EXPLAIN the exact cypher production runs — pinning a copy here would let
	// the two drift apart and the test would keep passing while the real query
	// regressed.
	explainCypher := "EXPLAIN " + query.TraceCalleesSpanningTreeCypher

	_, summary, err := client.ExecuteQueryWithSummary(ctx, explainCypher, map[string]interface{}{
		"nodeKey":   seedKey,
		"scopeId":   scopeID,
		"maxDepth":  3,
		"nodeLimit": 100,
	})
	if err != nil {
		t.Fatalf("EXPLAIN query failed: %v", err)
	}

	if summary == nil {
		t.Fatal("expected ResultSummary, got nil")
	}

	plan := summary.Plan()
	if plan == nil {
		t.Fatal("expected query plan, got nil")
	}

	// Check that NodeIndexSeek is present (using label expressions enables this)
	if !planHasOperator(plan, "NodeIndexSeek") {
		t.Error("expected NodeIndexSeek in query plan, not found")
	}

	// Verify AllNodesScan is NOT present (that would indicate labelless lookup)
	if planHasOperator(plan, "AllNodesScan") {
		t.Error("found AllNodesScan in plan; label expressions should prevent this")
	}
}

// TestFlowSpineGenerator_LabellessQuery_ProducesAllNodesScan is a control test
// proving that the plan-checker works: a deliberately labelless query should
// contain AllNodesScan.
func TestFlowSpineGenerator_LabellessQuery_ProducesAllNodesScan(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client := createTestGraphClient(t)
	defer func() {
		cleanupTestData(t, ctx, client, "itest-flows-labelless")
	}()

	scopeID := "itest-flows-labelless"

	// Create a test node
	seedKey := "func:test.go#TestFunc"
	_, err := client.MergeNode(ctx, []string{"Function"},
		map[string]interface{}{"nodeKey": seedKey, "scopeId": scopeID},
		map[string]interface{}{
			"nodeKey": seedKey,
			"name":    "TestFunc",
			"scopeId": scopeID,
			"scope":   "main",
		})
	if err != nil {
		t.Fatalf("failed to create test Function: %v", err)
	}

	// EXPLAIN a deliberately labelless query
	explainCypher := `EXPLAIN
MATCH (n {nodeKey: $nodeKey})
WHERE n.scopeId = $scopeId
RETURN n.nodeKey`

	_, summary, err := client.ExecuteQueryWithSummary(ctx, explainCypher, map[string]interface{}{
		"nodeKey": seedKey,
		"scopeId": scopeID,
	})
	if err != nil {
		t.Fatalf("EXPLAIN query failed: %v", err)
	}

	if summary == nil {
		t.Fatal("expected ResultSummary, got nil")
	}

	plan := summary.Plan()
	if plan == nil {
		t.Fatal("expected query plan, got nil")
	}

	// This query should use AllNodesScan since there's no label expression
	if !planHasOperator(plan, "AllNodesScan") {
		t.Error("expected AllNodesScan in labelless query plan, not found; this proves the checker works")
	}
}

// createTestGraphClient opens a connection to Neo4j for integration testing.
func createTestGraphClient(t *testing.T) *graphclient.Client {
	config := graphclient.Config{
		URI:      "bolt://localhost:7687",
		Username: "neo4j",
		Password: "password123",
		Database: "neo4j",
	}

	client, err := graphclient.NewClient(config)
	if err != nil {
		t.Fatalf("failed to create Neo4j client: %v", err)
	}

	return client
}

// cleanupTestData deletes all nodes and edges created under a specific scopeId.
// Must create a new client inside the cleanup function because the test's client
// may be closed by the time cleanup runs.
func cleanupTestData(t *testing.T, ctx context.Context, client *graphclient.Client, scopeID string) {
	// Create a fresh client for cleanup since the test's client may be exhausted
	cleanupClient, err := graphclient.NewClient(graphclient.Config{
		URI:      "bolt://localhost:7687",
		Username: "neo4j",
		Password: "password123",
		Database: "neo4j",
	})
	if err != nil {
		t.Logf("failed to create cleanup client: %v", err)
		return
	}
	defer cleanupClient.Close(ctx)

	err = cleanupClient.ExecuteQueryWithoutRecords(ctx, `
		MATCH (n {scopeId: $scopeId})
		DETACH DELETE n`, map[string]interface{}{
		"scopeId": scopeID,
	})
	if err != nil {
		t.Logf("cleanup failed for scopeId %s: %v", scopeID, err)
	}
}

// TestGraphSeedFinder_ServiceFilterBeatsGlobalCap reproduces the seed-starvation
// bug: FindSeeds caps results to budget.MaxSteps AFTER the priority sort, so
// without in-query service filtering a small service's tier-3 seeds lose the
// cut to other services' tier-1 seeds and the service indexes with zero flows.
// With SetServiceFilter the cap must apply within the requested service.
func TestGraphSeedFinder_ServiceFilterBeatsGlobalCap(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const scopeID = "itest-seed-starvation"
	client := createTestGraphClient(t)
	defer func() {
		cleanupTestData(t, ctx, client, scopeID)
	}()

	mergeFn := func(nodeKey string, props map[string]interface{}) {
		t.Helper()
		props["nodeKey"] = nodeKey
		props["scopeId"] = scopeID
		props["scope"] = "main"
		if _, err := client.MergeNode(ctx, []string{"Function"},
			map[string]interface{}{"nodeKey": nodeKey, "scopeId": scopeID}, props); err != nil {
			t.Fatalf("failed to merge %s: %v", nodeKey, err)
		}
	}

	// "Big" service: a tier-1 seed (EXPOSES_API handler, priority >= 85).
	bigHandlerKey := "func:itest-big/handler.go#Handle"
	mergeFn(bigHandlerKey, map[string]interface{}{
		"name": "Handle", "serviceName": "itest-svc-big", "filePath": "itest-big/handler.go",
	})
	routeKey := "api:itest:GET:/itest"
	if _, err := client.MergeNode(ctx, []string{"APIRoute"},
		map[string]interface{}{"nodeKey": routeKey, "scopeId": scopeID},
		map[string]interface{}{
			"nodeKey": routeKey, "scopeId": scopeID, "scope": "main",
			"name": "GET /itest", "method": "GET", "path": "/itest",
		}); err != nil {
		t.Fatalf("failed to merge route: %v", err)
	}
	if err := client.ExecuteQueryWithoutRecords(ctx, `
		MATCH (h:Function {nodeKey: $h, scopeId: $s})
		MATCH (r:APIRoute {nodeKey: $r, scopeId: $s})
		MERGE (h)-[rel:EXPOSES_API]->(r)
		SET rel.scope = 'main', rel.scopeId = $s`,
		map[string]interface{}{"h": bigHandlerKey, "r": routeKey, "s": scopeID}); err != nil {
		t.Fatalf("failed to create EXPOSES_API: %v", err)
	}

	// "Small" service: only a tier-3 seed (exported topological root, priority ~70).
	smallRootKey := "func:itest-small/root.go#Root"
	mergeFn(smallRootKey, map[string]interface{}{
		"name": "Root", "serviceName": "itest-svc-small", "filePath": "itest-small/root.go",
		"isExported": true, "inDegree": 0, "outDegree": 1,
	})

	finder := inference.NewGraphSeedFinder(client)
	finder.SetScope(models.ScopeContext{Scope: "main", ScopeID: scopeID})
	budget := inference.DefaultTraversalBudget
	budget.MaxSteps = 1 // force the cap so priority sorting decides who survives
	finder.SetBudget(budget)

	// Unfiltered: the single surviving seed must be a tier-1 seed, i.e. the
	// small service is starved out of the capped seed set.
	seeds, err := finder.FindSeeds(ctx)
	if err != nil {
		t.Fatalf("unfiltered FindSeeds failed: %v", err)
	}
	if len(seeds) != 1 {
		t.Fatalf("expected exactly 1 seed under MaxSteps=1, got %d", len(seeds))
	}
	if seeds[0].NodeKey == smallRootKey {
		t.Fatalf("tier-3 seed unexpectedly beat tier-1 seeds in the priority sort")
	}

	// Service-filtered: the same cap must now apply within the small service,
	// so its tier-3 root survives.
	finder.SetServiceFilter([]string{"itest-svc-small"}, "")
	seeds, err = finder.FindSeeds(ctx)
	if err != nil {
		t.Fatalf("filtered FindSeeds failed: %v", err)
	}
	if len(seeds) != 1 {
		t.Fatalf("expected exactly 1 seed for itest-svc-small, got %d", len(seeds))
	}
	if seeds[0].NodeKey != smallRootKey {
		t.Fatalf("expected small-service root %s, got %s", smallRootKey, seeds[0].NodeKey)
	}
	if seeds[0].Tier != 3 {
		t.Fatalf("expected tier 3 seed, got tier %d", seeds[0].Tier)
	}

	// Prefix filtering must behave the same way.
	finder.SetServiceFilter(nil, "itest-svc-small")
	seeds, err = finder.FindSeeds(ctx)
	if err != nil {
		t.Fatalf("prefix-filtered FindSeeds failed: %v", err)
	}
	if len(seeds) != 1 || seeds[0].NodeKey != smallRootKey {
		t.Fatalf("prefix filter: expected only %s, got %+v", smallRootKey, seeds)
	}
}

// TestGraphSeedFinder_InterfaceImplSeeds_MethodLevelEdges reproduces the tier-2
// dead spot: SCIP relationship ingestion emits method-level IMPLEMENTS edges
// Method→Method (abstract member), while the old tier-2 query demanded
// fn-[:IMPLEMENTS]->(:Interface) — a shape only Class→Interface edges have —
// so tier 2 returned zero for every service.
func TestGraphSeedFinder_InterfaceImplSeeds_MethodLevelEdges(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const scopeID = "itest-iface-impl-seeds"
	client := createTestGraphClient(t)
	defer func() {
		cleanupTestData(t, ctx, client, scopeID)
	}()

	implKey := "method:itest/impl.go#Store#Save"
	abstractKey := "method:itest/iface.go#Storer#Save"
	for key, name := range map[string]string{implKey: "Save", abstractKey: "Save"} {
		if _, err := client.MergeNode(ctx, []string{"Method"},
			map[string]interface{}{"nodeKey": key, "scopeId": scopeID},
			map[string]interface{}{
				"nodeKey": key, "scopeId": scopeID, "scope": "main",
				"name": name, "serviceName": "itest-iface-svc", "inDegree": 0,
			}); err != nil {
			t.Fatalf("failed to merge %s: %v", key, err)
		}
	}
	if err := client.ExecuteQueryWithoutRecords(ctx, `
		MATCH (a:Method {nodeKey: $a, scopeId: $s})
		MATCH (b:Method {nodeKey: $b, scopeId: $s})
		MERGE (a)-[r:IMPLEMENTS]->(b)
		SET r.scope = 'main', r.scopeId = $s`,
		map[string]interface{}{"a": implKey, "b": abstractKey, "s": scopeID}); err != nil {
		t.Fatalf("failed to create IMPLEMENTS: %v", err)
	}

	finder := inference.NewGraphSeedFinder(client)
	finder.SetScope(models.ScopeContext{Scope: "main", ScopeID: scopeID})
	finder.SetServiceFilter([]string{"itest-iface-svc"}, "")

	seeds, err := finder.FindSeeds(ctx)
	if err != nil {
		t.Fatalf("FindSeeds failed: %v", err)
	}
	var impl *inference.GraphSeed
	for i := range seeds {
		if seeds[i].NodeKey == implKey {
			impl = &seeds[i]
		}
	}
	if impl == nil {
		t.Fatalf("expected method-level implementer %s as a seed, got %+v", implKey, seeds)
	}
	if impl.Tier != 2 {
		t.Fatalf("expected tier 2, got %d", impl.Tier)
	}
}
