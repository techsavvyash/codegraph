package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	neo4j "github.com/context-maximiser/code-graph/internal/graph"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newFlowsTestClient connects to the shared dev Neo4j, skipping the test when
// the database is unavailable. These handler tests are DB-backed and run
// locally and in the integration environment, not in the unit-test CI job —
// same skip convention as handlers_find_test.go and handlers_source_test.go.
func newFlowsTestClient(t *testing.T) *neo4j.Client {
	t.Helper()
	client, err := neo4j.NewClient(neo4j.Config{
		URI:      getEnvOrDefault("NEO4J_URI", "bolt://localhost:7687"),
		Username: getEnvOrDefault("NEO4J_USERNAME", "neo4j"),
		Password: getEnvOrDefault("NEO4J_PASSWORD", "password123"),
		Database: getEnvOrDefault("NEO4J_DATABASE", "neo4j"),
	})
	if err != nil {
		t.Skipf("Neo4j not available: %v", err)
	}
	if records, err := client.ExecuteQuery(context.Background(), "RETURN 1", nil); err != nil || len(records) == 0 {
		t.Skipf("Neo4j not responding: %v", err)
	}
	return client
}

// setupFlowsTestDB creates a test flow graph for testing flows and entry_points tools.
// Graph structure:
//
//	Service: flows-mcp-svc
//	APIRoute: GET /api/data
//	Handler: GetData
//	Handler → FetchData (CALLS)
//	FetchData → QueryDB (CALLS)
func setupFlowsTestDB(t *testing.T) (*CodeGraphMCPServer, map[string]string, func()) {
	client := newFlowsTestClient(t)

	ctx := context.Background()

	// Clean up any existing test data
	_, _ = client.ExecuteQuery(ctx, `MATCH (n) WHERE n.scopeId = "itest-flows-mcp" DETACH DELETE n`, nil)

	// Create Service node
	serviceKey := "svc:flows-mcp-svc"
	serviceProps := map[string]interface{}{
		"nodeKey": serviceKey,
		"name":    "flows-mcp-svc",
		"scopeId": "itest-flows-mcp",
		"scope":   "main",
		"type":    "service",
	}
	_, err := client.MergeNode(ctx, []string{"Service"},
		map[string]interface{}{"nodeKey": serviceKey, "scopeId": "itest-flows-mcp"},
		serviceProps)
	require.NoError(t, err, "failed to create Service node")

	// Create APIRoute node
	routeKey := "route:GET:/api/data"
	routeProps := map[string]interface{}{
		"nodeKey": routeKey,
		"name":    "GET /api/data",
		"method":  "GET",
		"path":    "/api/data",
		"scopeId": "itest-flows-mcp",
		"scope":   "main",
	}
	_, err = client.MergeNode(ctx, []string{"APIRoute"},
		map[string]interface{}{"nodeKey": routeKey, "scopeId": "itest-flows-mcp"},
		routeProps)
	require.NoError(t, err, "failed to create APIRoute node")

	// Create Handler function
	handlerKey := "func:handler.go#GetData"
	handlerProps := map[string]interface{}{
		"nodeKey":        handlerKey,
		"name":           "GetData",
		"scopeId":        "itest-flows-mcp",
		"scope":          "main",
		"serviceName":    "flows-mcp-svc",
		"filePath":       "handler.go",
		"isExported":     true,
		"isTestFunction": false,
		"inDegree":       0,
		"outDegree":      1,
	}
	_, err = client.MergeNode(ctx, []string{"Function"},
		map[string]interface{}{"nodeKey": handlerKey, "scopeId": "itest-flows-mcp"},
		handlerProps)
	require.NoError(t, err, "failed to create handler Function")

	// Create FetchData function
	fnAKey := "func:service.go#FetchData"
	fnAProps := map[string]interface{}{
		"nodeKey":        fnAKey,
		"name":           "FetchData",
		"scopeId":        "itest-flows-mcp",
		"scope":          "main",
		"serviceName":    "flows-mcp-svc",
		"filePath":       "service.go",
		"isExported":     true,
		"isTestFunction": false,
		"inDegree":       1,
		"outDegree":      1,
	}
	_, err = client.MergeNode(ctx, []string{"Function"},
		map[string]interface{}{"nodeKey": fnAKey, "scopeId": "itest-flows-mcp"},
		fnAProps)
	require.NoError(t, err, "failed to create FetchData function")

	// Create QueryDB function
	fnBKey := "func:db.go#QueryDB"
	fnBProps := map[string]interface{}{
		"nodeKey":        fnBKey,
		"name":           "QueryDB",
		"scopeId":        "itest-flows-mcp",
		"scope":          "main",
		"serviceName":    "flows-mcp-svc",
		"filePath":       "db.go",
		"isExported":     true,
		"isTestFunction": false,
		"inDegree":       1,
		"outDegree":      0,
	}
	_, err = client.MergeNode(ctx, []string{"Function"},
		map[string]interface{}{"nodeKey": fnBKey, "scopeId": "itest-flows-mcp"},
		fnBProps)
	require.NoError(t, err, "failed to create QueryDB function")

	// Create EXPOSES_API edge: Handler → Route
	err = client.ExecuteQueryWithoutRecords(ctx, `
		MATCH (h:Function {nodeKey: $handlerKey, scopeId: $scopeId})
		MATCH (r:APIRoute {nodeKey: $routeKey, scopeId: $scopeId})
		MERGE (h)-[rel:EXPOSES_API]->(r)
		SET rel.scope = $scope, rel.scopeId = $scopeId`, map[string]interface{}{
		"handlerKey": handlerKey,
		"routeKey":   routeKey,
		"scopeId":    "itest-flows-mcp",
		"scope":      "main",
	})
	require.NoError(t, err, "failed to create EXPOSES_API edge")

	// Create CALLS edge: Handler → FetchData
	err = client.ExecuteQueryWithoutRecords(ctx, `
		MATCH (h:Function {nodeKey: $handlerKey, scopeId: $scopeId})
		MATCH (fa:Function {nodeKey: $fnAKey, scopeId: $scopeId})
		MERGE (h)-[rel:CALLS]->(fa)
		SET rel.scope = $scope, rel.scopeId = $scopeId`, map[string]interface{}{
		"handlerKey": handlerKey,
		"fnAKey":     fnAKey,
		"scopeId":    "itest-flows-mcp",
		"scope":      "main",
	})
	require.NoError(t, err, "failed to create CALLS edge handler→FetchData")

	// Create CALLS edge: FetchData → QueryDB
	err = client.ExecuteQueryWithoutRecords(ctx, `
		MATCH (fa:Function {nodeKey: $fnAKey, scopeId: $scopeId})
		MATCH (fb:Function {nodeKey: $fnBKey, scopeId: $scopeId})
		MERGE (fa)-[rel:CALLS]->(fb)
		SET rel.scope = $scope, rel.scopeId = $scopeId`, map[string]interface{}{
		"fnAKey":  fnAKey,
		"fnBKey":  fnBKey,
		"scopeId": "itest-flows-mcp",
		"scope":   "main",
	})
	require.NoError(t, err, "failed to create CALLS edge FetchData→QueryDB")

	// entry_points drops entries whose filePath does not exist under the
	// workspace root, so give the fake nodes a real workspace to live in.
	workspaceRoot := t.TempDir()
	for _, f := range []string{"handler.go", "service.go", "db.go"} {
		require.NoError(t, os.WriteFile(filepath.Join(workspaceRoot, f), []byte("package fake\n"), 0644))
	}
	server := &CodeGraphMCPServer{
		client:        client,
		queryBuilder:  neo4j.NewQueryBuilder(client),
		workspaceRoot: workspaceRoot,
	}

	nodeIDs := map[string]string{
		"GetData":   handlerKey,
		"FetchData": fnAKey,
		"QueryDB":   fnBKey,
		"route":     routeKey,
	}

	cleanup := func() {
		_, _ = client.ExecuteQuery(context.Background(), `MATCH (n) WHERE n.scopeId = "itest-flows-mcp" DETACH DELETE n`, nil)
		client.Close(context.Background())
	}

	return server, nodeIDs, cleanup
}

// TestFlowsFromNodeByName generates a flow from a specific node by name and verifies
// the step chain is included in order.
func TestFlowsFromNodeByName(t *testing.T) {
	server, _, cleanup := setupFlowsTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Generate flow from GetData by name
	response := server.handleFlowsToolV2(ctx, map[string]interface{}{
		"from_name": "GetData",
		"max_depth": float64(3),
		"format":    "json",
		"scope_id":  "itest-flows-mcp",
	})

	assert.False(t, response.IsError, "flows from_name should succeed")
	require.Len(t, response.Content, 1)

	var result map[string]interface{}
	err := json.Unmarshal([]byte(response.Content[0].Text), &result)
	require.NoError(t, err, "response should be valid JSON")

	// Verify flow structure
	assert.Contains(t, result, "flow_count")
	assert.Contains(t, result, "flows")

	flowCount, ok := result["flow_count"].(float64)
	require.True(t, ok)
	assert.Equal(t, float64(1), flowCount, "should return exactly 1 flow")

	flows, ok := result["flows"].([]interface{})
	require.True(t, ok)
	require.Len(t, flows, 1)

	flow, ok := flows[0].(map[string]interface{})
	require.True(t, ok)

	// Verify flow name and type
	flowName, _ := flow["flowName"].(string)
	assert.Equal(t, "GetData", flowName, "flow should be named GetData")

	flowType, _ := flow["flowType"].(string)
	assert.Equal(t, "manual", flowType, "flow type should be 'manual'")

	// Verify step chain: GetData → FetchData → QueryDB
	stepsRaw, ok := flow["steps"].([]interface{})
	require.True(t, ok)
	require.GreaterOrEqual(t, len(stepsRaw), 2, "flow should have at least GetData and FetchData")

	// Collect step names
	stepNames := make([]string, 0, len(stepsRaw))
	for _, s := range stepsRaw {
		if stepMap, ok := s.(map[string]interface{}); ok {
			if name, ok := stepMap["name"].(string); ok {
				stepNames = append(stepNames, name)
			}
		}
	}

	// The full chain must be present and ordered: GetData → FetchData → QueryDB
	// (max_depth 3 covers distance 2). No conditional assertions — a flow that
	// lost its tail would otherwise pass silently.
	require.Len(t, stepNames, 3, "expected the full 3-step chain, got %v", stepNames)
	assert.Equal(t, []string{"GetData", "FetchData", "QueryDB"}, stepNames)
}

// TestFlowsFromNameAmbiguity returns candidates response when multiple nodes
// match the same name.
func TestFlowsFromNameAmbiguity(t *testing.T) {
	client := newFlowsTestClient(t)
	defer client.Close(context.Background())

	ctx := context.Background()

	// Clean up any existing test data
	_, _ = client.ExecuteQuery(ctx, `MATCH (n) WHERE n.scopeId = "itest-flows-ambig" DETACH DELETE n`, nil)
	t.Cleanup(func() {
		// The test's own client is already closed when cleanups run — a
		// swallowed error here leaves the nodes in the shared database.
		cctx := context.Background()
		cclient, err := neo4j.NewClient(neo4j.Config{
			URI:      getEnvOrDefault("NEO4J_URI", "bolt://localhost:7687"),
			Username: getEnvOrDefault("NEO4J_USERNAME", "neo4j"),
			Password: getEnvOrDefault("NEO4J_PASSWORD", "password123"),
			Database: getEnvOrDefault("NEO4J_DATABASE", "neo4j"),
		})
		if err != nil {
			t.Errorf("cleanup: connect: %v", err)
			return
		}
		defer cclient.Close(cctx)
		if _, err := cclient.ExecuteQuery(cctx, `MATCH (n) WHERE n.scopeId = "itest-flows-ambig" DETACH DELETE n`, nil); err != nil {
			t.Errorf("cleanup failed: %v", err)
		}
	})

	// Create two functions with the same name
	for i := 1; i <= 2; i++ {
		fnKey := "func:file" + string(rune('a'+i-1)) + ".go#Process"
		fnProps := map[string]interface{}{
			"nodeKey":        fnKey,
			"name":           "Process",
			"scopeId":        "itest-flows-ambig",
			"scope":          "main",
			"serviceName":    "test-svc",
			"filePath":       "file" + string(rune('a'+i-1)) + ".go",
			"isExported":     true,
			"isTestFunction": false,
		}
		_, err := client.MergeNode(ctx, []string{"Function"},
			map[string]interface{}{"nodeKey": fnKey, "scopeId": "itest-flows-ambig"},
			fnProps)
		require.NoError(t, err)
	}

	workspaceRoot, _ := os.Getwd()
	server := &CodeGraphMCPServer{
		client:        client,
		queryBuilder:  neo4j.NewQueryBuilder(client),
		workspaceRoot: workspaceRoot,
	}

	// Try to generate flow from ambiguous name
	response := server.handleFlowsToolV2(ctx, map[string]interface{}{
		"from_name": "Process",
		"scope_id":  "itest-flows-ambig",
		"format":    "json",
	})

	// Should return ambiguity response (IsError=false with candidates)
	require.Len(t, response.Content, 1)

	var result map[string]interface{}
	err := json.Unmarshal([]byte(response.Content[0].Text), &result)
	require.NoError(t, err)

	// Ambiguity response has "error" field set to "ambiguous"
	errField, ok := result["error"].(string)
	assert.True(t, ok, "should have error field indicating ambiguity")
	assert.Equal(t, "ambiguous", errField, "error should indicate ambiguity")

	// Should have candidates list
	candidates, ok := result["candidates"].([]interface{})
	assert.True(t, ok, "should have candidates list")
	assert.GreaterOrEqual(t, len(candidates), 2, "should list multiple candidates")
}

// TestEntryPointsHandlerTier1 verifies entry_points returns the handler as Tier 1
// when it's exposed via APIRoute.
func TestEntryPointsHandlerTier1(t *testing.T) {
	server, _, cleanup := setupFlowsTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Call entry_points for the flows-mcp-svc service
	response := server.handleEntryPointsToolV2(ctx, map[string]interface{}{
		"service_name": "flows-mcp-svc",
		"scope_id":     "itest-flows-mcp",
		"format":       "json",
	})

	assert.False(t, response.IsError, "entry_points should succeed")
	require.Len(t, response.Content, 1)

	var result map[string]interface{}
	err := json.Unmarshal([]byte(response.Content[0].Text), &result)
	require.NoError(t, err, "response should be valid JSON")

	// Verify structure
	assert.Contains(t, result, "count")
	assert.Contains(t, result, "entries")

	entries, ok := result["entries"].([]interface{})
	require.True(t, ok)

	// Find the GetData handler
	var getDataEntry map[string]interface{}
	for _, e := range entries {
		if entryMap, ok := e.(map[string]interface{}); ok {
			if name, ok := entryMap["name"].(string); ok && name == "GetData" {
				getDataEntry = entryMap
				break
			}
		}
	}

	require.NotNil(t, getDataEntry, "GetData handler should be in entry points")

	// Verify it's Tier 1 (API-exposed)
	tier, ok := getDataEntry["tier"].(float64)
	require.True(t, ok)
	assert.Equal(t, float64(1), tier, "GetData should be Tier 1 (API-exposed)")

	tierLabel, ok := getDataEntry["tier_label"].(string)
	assert.True(t, ok)
	assert.Equal(t, "API-exposed", tierLabel, "tier_label should be 'API-exposed'")
}

// TestEntryPointsTier3TopologicalRoot: the tier-3 query silently returned
// nothing for its whole life — RETURN DISTINCT made ORDER BY calleeCount a
// syntax error and runTier swallowed it. GetData (no callers, one callee) is
// a known-positive topological root.
func TestEntryPointsTier3TopologicalRoot(t *testing.T) {
	server, _, cleanup := setupFlowsTestDB(t)
	defer cleanup()

	ctx := context.Background()

	response := server.handleEntryPointsToolV2(ctx, map[string]interface{}{
		"service_name": "flows-mcp-svc",
		"scope_id":     "itest-flows-mcp",
		"tier":         float64(3),
		"format":       "json",
	})

	require.False(t, response.IsError)
	require.Len(t, response.Content, 1)

	var result map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(response.Content[0].Text), &result))

	if tierErrs, ok := result["tier_errors"].([]interface{}); ok {
		t.Fatalf("tier query errored: %v", tierErrs)
	}

	entries, ok := result["entries"].([]interface{})
	require.True(t, ok)
	found := false
	for _, e := range entries {
		if em, ok := e.(map[string]interface{}); ok {
			if em["name"] == "GetData" {
				found = true
				tier, _ := em["tier"].(float64)
				assert.Equal(t, float64(3), tier)
			}
		}
	}
	assert.True(t, found, "GetData (no callers, has callee) must be a tier-3 topological root, got: %v", entries)
}

// TestFlowsFromNode_DepthAndParentKey verifies that steps traced from a node
// carry Depth (BFS distance from the entry) and ParentKey (spanning-tree
// parent nodeKey), and that the entry step has Depth 0 and no ParentKey.
func TestFlowsFromNode_DepthAndParentKey(t *testing.T) {
	server, nodeIDs, cleanup := setupFlowsTestDB(t)
	defer cleanup()

	ctx := context.Background()

	response := server.handleFlowsToolV2(ctx, map[string]interface{}{
		"from_name": "GetData",
		"max_depth": float64(3),
		"format":    "json",
		"scope_id":  "itest-flows-mcp",
		"persist":   false,
	})

	require.False(t, response.IsError, "flows from_name should succeed")
	require.Len(t, response.Content, 1)

	var result map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(response.Content[0].Text), &result))

	flows := result["flows"].([]interface{})
	require.Len(t, flows, 1)
	steps := flows[0].(map[string]interface{})["steps"].([]interface{})
	require.Len(t, steps, 3, "expected GetData -> FetchData -> QueryDB")

	byName := make(map[string]map[string]interface{}, len(steps))
	for _, s := range steps {
		sm := s.(map[string]interface{})
		byName[sm["name"].(string)] = sm
	}

	entry := byName["GetData"]
	require.NotNil(t, entry)
	assert.Equal(t, float64(0), entry["depth"], "entry step must have depth 0")
	_, hasParent := entry["parentKey"]
	assert.False(t, hasParent, "entry step must omit parentKey")

	fetch := byName["FetchData"]
	require.NotNil(t, fetch)
	assert.Equal(t, float64(1), fetch["depth"], "FetchData is 1 hop from GetData")
	assert.Equal(t, nodeIDs["GetData"], fetch["parentKey"], "FetchData's parent must be GetData")

	queryStep := byName["QueryDB"]
	require.NotNil(t, queryStep)
	assert.Equal(t, float64(2), queryStep["depth"], "QueryDB is 2 hops from GetData")
	assert.Equal(t, nodeIDs["FetchData"], queryStep["parentKey"], "QueryDB's parent must be FetchData")
}

// TestFlowsFromNode_EnrichmentFillsNodeIDAndFilePath verifies the
// post-generation enrichment pass fills nodeId/filePath/startLine on steps
// that resolve, and leaves them empty (rather than erroring) for steps whose
// nodeKey doesn't resolve to a live node.
func TestFlowsFromNode_EnrichmentFillsNodeIDAndFilePath(t *testing.T) {
	server, _, cleanup := setupFlowsTestDB(t)
	defer cleanup()

	ctx := context.Background()

	response := server.handleFlowsToolV2(ctx, map[string]interface{}{
		"from_name": "GetData",
		"max_depth": float64(3),
		"format":    "json",
		"scope_id":  "itest-flows-mcp",
		"persist":   false,
	})

	require.False(t, response.IsError)
	require.Len(t, response.Content, 1)

	var result map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(response.Content[0].Text), &result))

	flows := result["flows"].([]interface{})
	require.Len(t, flows, 1)
	steps := flows[0].(map[string]interface{})["steps"].([]interface{})
	require.Len(t, steps, 3)

	for _, s := range steps {
		sm := s.(map[string]interface{})
		nodeID, _ := sm["nodeId"].(string)
		filePath, _ := sm["filePath"].(string)
		assert.NotEmpty(t, nodeID, "step %v should have nodeId filled by enrichment", sm["name"])
		assert.NotEmpty(t, filePath, "step %v should have filePath filled by enrichment", sm["name"])
	}
}

// TestFlowsFromNode_PersistFalseSkipsWrite verifies persist=false does not
// MERGE a Flow node into the graph, while the default (persist omitted /
// true) does — same call, only the flag differs.
func TestFlowsFromNode_PersistFalseSkipsWrite(t *testing.T) {
	server, _, cleanup := setupFlowsTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// persist=false: no Flow node should be written.
	response := server.handleFlowsToolV2(ctx, map[string]interface{}{
		"from_name": "GetData",
		"max_depth": float64(2),
		"format":    "json",
		"scope_id":  "itest-flows-mcp",
		"persist":   false,
	})
	require.False(t, response.IsError)

	records, err := server.client.ExecuteQuery(ctx, `MATCH (f:Flow) WHERE f.scopeId = "itest-flows-mcp" RETURN count(f) AS c`, nil)
	require.NoError(t, err)
	require.Len(t, records, 1)
	count := records[0].AsMap()["c"].(int64)
	assert.Equal(t, int64(0), count, "persist=false must not write a Flow node")

	// Default (persist omitted): a Flow node should be written.
	response = server.handleFlowsToolV2(ctx, map[string]interface{}{
		"from_name": "GetData",
		"max_depth": float64(2),
		"format":    "json",
		"scope_id":  "itest-flows-mcp",
	})
	require.False(t, response.IsError)

	records, err = server.client.ExecuteQuery(ctx, `MATCH (f:Flow) WHERE f.scopeId = "itest-flows-mcp" RETURN count(f) AS c`, nil)
	require.NoError(t, err)
	require.Len(t, records, 1)
	count = records[0].AsMap()["c"].(int64)
	assert.Equal(t, int64(1), count, "default persist=true must write exactly one Flow node")
}

// TestFlows_EmptyResultReturnsValidJSON verifies format=json with zero flows
// returns a parseable {"flow_count":0,"flows":[]} body instead of the
// plain-text sentence used for format=text, which would break a JSON parser
// on the Studio side.
func TestFlows_EmptyResultReturnsValidJSON(t *testing.T) {
	client := newFlowsTestClient(t)
	defer client.Close(context.Background())

	ctx := context.Background()
	// Empty, never-used scope: guaranteed to produce zero discovery-mode flows.
	scopeID := "itest-flows-empty-scope-does-not-exist"
	_, _ = client.ExecuteQuery(ctx, `MATCH (n) WHERE n.scopeId = $scopeId DETACH DELETE n`, map[string]interface{}{"scopeId": scopeID})
	// Discovery over a PR-style scope falls back to main-scope functions by
	// design, so this handler call persists Flow nodes stamped with scopeID
	// even though the response reports zero — without this cleanup every run
	// leaked ~35 Flow nodes into the dev graph (caught by the post-index
	// scope-hygiene integrity check). Fresh client: the outer one is
	// defer-closed before t.Cleanup callbacks fire.
	t.Cleanup(func() {
		cctx := context.Background()
		cclient, err := neo4j.NewClient(neo4j.Config{
			URI: getEnvOrDefault("NEO4J_URI", "bolt://localhost:7687"), Username: getEnvOrDefault("NEO4J_USERNAME", "neo4j"),
			Password: getEnvOrDefault("NEO4J_PASSWORD", "password123"), Database: getEnvOrDefault("NEO4J_DATABASE", "neo4j"),
		})
		if err != nil {
			t.Errorf("cleanup: connect: %v", err)
			return
		}
		defer cclient.Close(cctx)
		if _, err := cclient.ExecuteQuery(cctx, `MATCH (n) WHERE n.scopeId = $scopeId DETACH DELETE n`, map[string]interface{}{"scopeId": scopeID}); err != nil {
			t.Errorf("cleanup failed (leaks %s residue): %v", scopeID, err)
		}
	})

	workspaceRoot, _ := os.Getwd()
	server := &CodeGraphMCPServer{
		client:        client,
		queryBuilder:  neo4j.NewQueryBuilder(client),
		workspaceRoot: workspaceRoot,
	}

	response := server.handleFlowsToolV2(ctx, map[string]interface{}{
		"scope_id": scopeID,
		"format":   "json",
	})

	require.False(t, response.IsError)
	require.Len(t, response.Content, 1)

	var result map[string]interface{}
	err := json.Unmarshal([]byte(response.Content[0].Text), &result)
	require.NoError(t, err, "format=json with zero flows must be valid JSON, got: %s", response.Content[0].Text)

	assert.Equal(t, float64(0), result["flow_count"])
	flows, ok := result["flows"].([]interface{})
	require.True(t, ok, "flows field must be a JSON array even when empty")
	assert.Len(t, flows, 0)
}

// TestEntryPoints_ServiceNameBypassesWorkspaceFilter verifies that passing an
// explicit service_name returns entries even when the server's workspaceRoot
// doesn't contain the indexed files — the scenario hit by the Studio MCP
// bridge, which spawns codegraph-mcp with cwd=bin/.
func TestEntryPoints_ServiceNameBypassesWorkspaceFilter(t *testing.T) {
	client := newFlowsTestClient(t)
	defer client.Close(context.Background())

	ctx := context.Background()
	scopeID := "itest-flows-cwd-bypass"
	_, _ = client.ExecuteQuery(ctx, `MATCH (n) WHERE n.scopeId = $scopeId DETACH DELETE n`, map[string]interface{}{"scopeId": scopeID})
	t.Cleanup(func() {
		cctx := context.Background()
		cclient, err := neo4j.NewClient(neo4j.Config{
			URI: getEnvOrDefault("NEO4J_URI", "bolt://localhost:7687"), Username: getEnvOrDefault("NEO4J_USERNAME", "neo4j"),
			Password: getEnvOrDefault("NEO4J_PASSWORD", "password123"), Database: getEnvOrDefault("NEO4J_DATABASE", "neo4j"),
		})
		if err != nil {
			t.Errorf("cleanup: connect: %v", err)
			return
		}
		defer cclient.Close(cctx)
		if _, err := cclient.ExecuteQuery(cctx, `MATCH (n) WHERE n.scopeId = $scopeId DETACH DELETE n`, map[string]interface{}{"scopeId": scopeID}); err != nil {
			t.Errorf("cleanup failed: %v", err)
		}
	})

	routeKey := "route:GET:/api/bypass"
	handlerKey := "func:bypass.go#Bypass"
	_, err := client.MergeNode(ctx, []string{"APIRoute"},
		map[string]interface{}{"nodeKey": routeKey, "scopeId": scopeID},
		map[string]interface{}{"nodeKey": routeKey, "name": "GET /api/bypass", "method": "GET", "path": "/api/bypass", "scopeId": scopeID, "scope": "main"})
	require.NoError(t, err)
	_, err = client.MergeNode(ctx, []string{"Function"}, map[string]interface{}{"nodeKey": handlerKey, "scopeId": scopeID},
		map[string]interface{}{
			"nodeKey": handlerKey, "name": "Bypass", "scopeId": scopeID, "scope": "main",
			"serviceName": "bypass-svc", "filePath": "bypass.go", "isExported": true, "isTestFunction": false,
		})
	require.NoError(t, err)
	err = client.ExecuteQueryWithoutRecords(ctx, `
		MATCH (h:Function {nodeKey: $handlerKey, scopeId: $scopeId})
		MATCH (r:APIRoute {nodeKey: $routeKey, scopeId: $scopeId})
		MERGE (h)-[rel:EXPOSES_API]->(r)
		SET rel.scope = $scope, rel.scopeId = $scopeId`, map[string]interface{}{
		"handlerKey": handlerKey, "routeKey": routeKey, "scopeId": scopeID, "scope": "main",
	})
	require.NoError(t, err)

	// workspaceRoot deliberately does NOT contain bypass.go (an empty temp
	// dir), simulating the MCP server spawned with a cwd that has nothing to
	// do with the indexed repo.
	server := &CodeGraphMCPServer{
		client:        client,
		queryBuilder:  neo4j.NewQueryBuilder(client),
		workspaceRoot: t.TempDir(),
	}

	// Without service_name: workspace filtering drops the entry (file isn't
	// under workspaceRoot).
	noScope := server.handleEntryPointsToolV2(ctx, map[string]interface{}{
		"scope_id": scopeID,
		"format":   "json",
	})
	require.False(t, noScope.IsError)
	var noScopeResult map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(noScope.Content[0].Text), &noScopeResult))
	noScopeEntries := noScopeResult["entries"].([]interface{})
	assert.Len(t, noScopeEntries, 0, "without service_name, workspace filtering should drop the out-of-workspace entry")

	// With service_name: bypasses the workspace check, entry comes back.
	withScope := server.handleEntryPointsToolV2(ctx, map[string]interface{}{
		"scope_id":     scopeID,
		"service_name": "bypass-svc",
		"format":       "json",
	})
	require.False(t, withScope.IsError)
	var withScopeResult map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(withScope.Content[0].Text), &withScopeResult))
	withScopeEntries := withScopeResult["entries"].([]interface{})
	require.Len(t, withScopeEntries, 1, "explicit service_name must bypass workspace-cwd filtering")

	entry := withScopeEntries[0].(map[string]interface{})
	assert.Equal(t, "Bypass", entry["name"])
	assert.NotEmpty(t, entry["node_id"], "entry should carry node_id (elementId)")
	assert.Equal(t, "Function", entry["label"])
}
