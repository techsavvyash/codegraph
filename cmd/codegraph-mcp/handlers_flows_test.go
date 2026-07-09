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

// setupFlowsTestDB creates a test flow graph for testing flows and entry_points tools.
// Graph structure:
//
//	Service: flows-mcp-svc
//	APIRoute: GET /api/data
//	Handler: GetData
//	Handler → FetchData (CALLS)
//	FetchData → QueryDB (CALLS)
func setupFlowsTestDB(t *testing.T) (*CodeGraphMCPServer, map[string]string, func()) {
	client, err := neo4j.NewClient(neo4j.Config{
		URI:      getEnvOrDefault("NEO4J_URI", "bolt://localhost:7687"),
		Username: getEnvOrDefault("NEO4J_USERNAME", "neo4j"),
		Password: getEnvOrDefault("NEO4J_PASSWORD", "password123"),
		Database: getEnvOrDefault("NEO4J_DATABASE", "neo4j"),
	})
	require.NoError(t, err, "failed to create Neo4j client")

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
	_, err = client.MergeNode(ctx, []string{"Service"},
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
	client, err := neo4j.NewClient(neo4j.Config{
		URI:      getEnvOrDefault("NEO4J_URI", "bolt://localhost:7687"),
		Username: getEnvOrDefault("NEO4J_USERNAME", "neo4j"),
		Password: getEnvOrDefault("NEO4J_PASSWORD", "password123"),
		Database: getEnvOrDefault("NEO4J_DATABASE", "neo4j"),
	})
	require.NoError(t, err)
	defer client.Close(context.Background())

	ctx := context.Background()

	// Clean up any existing test data
	_, _ = client.ExecuteQuery(ctx, `MATCH (n) WHERE n.scopeId = "itest-flows-ambig" DETACH DELETE n`, nil)
	t.Cleanup(func() {
		_, _ = client.ExecuteQuery(context.Background(), `MATCH (n) WHERE n.scopeId = "itest-flows-ambig" DETACH DELETE n`, nil)
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
	err = json.Unmarshal([]byte(response.Content[0].Text), &result)
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
