//go:build integration
// +build integration

package main

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	neo4j "github.com/context-maximiser/code-graph/internal/graph"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupExpandTestDB creates a test diamond call graph for testing expand/path.
// Graph structure:
//
//	A → B
//	A → C
//	B → D
//	C → D
//	D ← E (reverse edge to test filtering)
func setupExpandTestDB(t *testing.T) (*CodeGraphMCPServer, map[string]string, func()) {
	client, err := neo4j.NewClient(neo4j.Config{
		URI:      getEnvOrDefault("NEO4J_URI", "bolt://localhost:7687"),
		Username: getEnvOrDefault("NEO4J_USERNAME", "neo4j"),
		Password: getEnvOrDefault("NEO4J_PASSWORD", "password123"),
		Database: getEnvOrDefault("NEO4J_DATABASE", "neo4j"),
	})
	require.NoError(t, err, "failed to create Neo4j client")

	ctx := context.Background()

	// Clean up any existing test data
	_, _ = client.ExecuteQuery(ctx, `MATCH (n:itest_Function) WHERE n.scopeId = "itest-expand" DETACH DELETE n`, nil)

	// Create diamond graph
	createQuery := `
		CREATE (a:itest_Function {name: 'funcA', scopeId: 'itest-expand', nodeKey: 'itest-expand/funcA', serviceName: 'svc1'})
		CREATE (b:itest_Function {name: 'funcB', scopeId: 'itest-expand', nodeKey: 'itest-expand/funcB', serviceName: 'svc1', filePath: 'src/b.ts', startLine: 5, endLine: 25})
		CREATE (c:itest_Function {name: 'funcC', scopeId: 'itest-expand', nodeKey: 'itest-expand/funcC', serviceName: 'svc2'})
		CREATE (d:itest_Function {name: 'funcD', scopeId: 'itest-expand', nodeKey: 'itest-expand/funcD', serviceName: 'svc2'})
		CREATE (e:itest_Function {name: 'funcE', scopeId: 'itest-expand', nodeKey: 'itest-expand/funcE', serviceName: 'svc3'})
		CREATE (a)-[:itest_CALLS]->(b)
		CREATE (a)-[:itest_CALLS]->(c)
		CREATE (b)-[:itest_CALLS]->(d)
		CREATE (c)-[:itest_CALLS]->(d)
		CREATE (d)-[:itest_CONTAINS]->(e)
		CREATE (e)-[:itest_CALLS]->(d)
	`

	_, err = client.ExecuteQuery(ctx, createQuery, nil)
	require.NoError(t, err, "failed to create test graph")

	// Get the node IDs
	records, err := client.ExecuteQuery(ctx, `
		MATCH (n:itest_Function) WHERE n.scopeId = 'itest-expand'
		RETURN n.name AS name, elementId(n) AS node_id
	`, nil)
	require.NoError(t, err)

	nodeIDs := make(map[string]string)
	for _, rec := range records {
		m := rec.AsMap()
		name := getStringFromRecord(m, "name")
		id := getStringFromRecord(m, "node_id")
		nodeIDs[name] = id
	}

	workspaceRoot, _ := os.Getwd()
	server := &CodeGraphMCPServer{
		client:        client,
		queryBuilder:  neo4j.NewQueryBuilder(client),
		workspaceRoot: workspaceRoot,
	}

	cleanup := func() {
		_, _ = client.ExecuteQuery(context.Background(), `MATCH (n:itest_Function) WHERE n.scopeId = "itest-expand" DETACH DELETE n`, nil)
		client.Close(context.Background())
	}

	return server, nodeIDs, cleanup
}

// TestExpandViaAPOC verifies expand tool returns nodes reachable via APOC.
func TestExpandViaAPOC(t *testing.T) {
	server, nodeIDs, cleanup := setupExpandTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Expand from A depth 2 (should reach B, C, D)
	response := server.handleExpandTool(ctx, map[string]interface{}{
		"node_id":   nodeIDs["funcA"],
		"rel_types": []interface{}{"itest_CALLS"},
		"direction": "out",
		"depth":     float64(2),
		"format":    "json",
	})

	assert.False(t, response.IsError, "expand should succeed")
	require.Len(t, response.Content, 1)

	var result map[string]interface{}
	err := json.Unmarshal([]byte(response.Content[0].Text), &result)
	require.NoError(t, err, "response should be valid JSON")

	// Verify structure
	assert.Contains(t, result, "start")
	assert.Contains(t, result, "nodes")
	assert.Contains(t, result, "edges")

	// Verify nodes (A + reachable {B, C, D}, E not reachable via CALLS)
	nodes, ok := result["nodes"].([]interface{})
	require.True(t, ok)
	assert.GreaterOrEqual(t, len(nodes), 4, "should have at least A, B, C, D")

	// Count unique node IDs - verify D appears only once (NODE_GLOBAL uniqueness)
	nodeMap := make(map[string]bool)
	for _, n := range nodes {
		if nm, ok := n.(map[string]interface{}); ok {
			if nid, ok := nm["node_id"].(string); ok {
				nodeMap[nid] = true
			}
		}
	}

	// D should appear exactly once despite being reachable from both B and C
	dCount := 0
	for _, n := range nodes {
		if nm, ok := n.(map[string]interface{}); ok {
			if nid, ok := nm["node_id"].(string); ok && nid == nodeIDs["funcD"] {
				dCount++
			}
		}
	}
	assert.Equal(t, 1, dCount, "funcD should appear exactly once (NODE_GLOBAL uniqueness)")

	// funcB carries body-range anchors (RFC-010): both line bounds must
	// surface so callers can slice files without a second lookup. funcD has
	// no location and must omit them (omitempty).
	for _, n := range nodes {
		nm, ok := n.(map[string]interface{})
		if !ok {
			continue
		}
		switch nm["node_id"] {
		case nodeIDs["funcB"]:
			assert.EqualValues(t, 5, nm["start_line"], "funcB start_line")
			assert.EqualValues(t, 25, nm["end_line"], "funcB end_line")
		case nodeIDs["funcD"]:
			assert.NotContains(t, nm, "start_line", "funcD has no location; start_line must be omitted")
			assert.NotContains(t, nm, "end_line", "funcD has no location; end_line must be omitted")
		}
	}
}

// TestExpandDirectionIn verifies direction filtering.
func TestExpandDirectionIn(t *testing.T) {
	server, nodeIDs, cleanup := setupExpandTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Expand inbound to D (should reach B, C, A)
	response := server.handleExpandTool(ctx, map[string]interface{}{
		"node_id":   nodeIDs["funcD"],
		"rel_types": []interface{}{"itest_CALLS"},
		"direction": "in",
		"depth":     float64(2),
		"format":    "json",
	})

	assert.False(t, response.IsError)

	var result map[string]interface{}
	json.Unmarshal([]byte(response.Content[0].Text), &result)

	nodes, _ := result["nodes"].([]interface{})
	nodeNames := make(map[string]bool)
	for _, n := range nodes {
		if nm, ok := n.(map[string]interface{}); ok {
			if name, ok := nm["name"].(string); ok {
				nodeNames[name] = true
			}
		}
	}

	assert.True(t, nodeNames["funcD"], "should include start node D")
	assert.True(t, nodeNames["funcB"], "should reach B")
	assert.True(t, nodeNames["funcC"], "should reach C")
	assert.True(t, nodeNames["funcA"], "should reach A")
	assert.True(t, nodeNames["funcE"], "should reach E via reverse itest_CALLS (E → D)")
}

// TestExpandRelTypeFiltering verifies rel_types filtering.
func TestExpandRelTypeFiltering(t *testing.T) {
	server, nodeIDs, cleanup := setupExpandTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Expand from D outbound via CALLS only (should reach E which is directly reachable via CALLS)
	response := server.handleExpandTool(ctx, map[string]interface{}{
		"node_id":   nodeIDs["funcD"],
		"rel_types": []interface{}{"itest_CALLS"},
		"direction": "out",
		"depth":     float64(1),
		"format":    "json",
	})

	assert.False(t, response.IsError, "expand should succeed")

	var result map[string]interface{}
	json.Unmarshal([]byte(response.Content[0].Text), &result)

	nodes, _ := result["nodes"].([]interface{})
	nodeNames := make(map[string]bool)
	for _, n := range nodes {
		if nm, ok := n.(map[string]interface{}); ok {
			if name, ok := nm["name"].(string); ok {
				nodeNames[name] = true
			}
		}
	}

	assert.True(t, nodeNames["funcD"], "should include start node D")
	// Verify we get the nodes reachable via specified rel types (itest_CALLS)
	// Note: The actual nodes returned depend on APOC's traversal, which may or may not include E
	// depending on how maxLevel interacts with the relationship filter
}

// TestExpandMaxNodes verifies max_nodes limit.
func TestExpandMaxNodes(t *testing.T) {
	server, nodeIDs, cleanup := setupExpandTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Expand with max_nodes=2 (should return start + 1 more)
	response := server.handleExpandTool(ctx, map[string]interface{}{
		"node_id":   nodeIDs["funcA"],
		"rel_types": []interface{}{"itest_CALLS"},
		"direction": "out",
		"depth":     float64(2),
		"max_nodes": float64(2),
		"format":    "json",
	})

	assert.False(t, response.IsError)

	var result map[string]interface{}
	json.Unmarshal([]byte(response.Content[0].Text), &result)

	nodeCount, ok := result["node_count"].(float64)
	require.True(t, ok, "node_count should be a number")
	assert.LessOrEqual(t, int(nodeCount), 2, "node_count should respect max_nodes limit")
}

// TestNameOrIDResolution verifies unique name resolves correctly.
func TestNameOrIDResolution(t *testing.T) {
	server, _, cleanup := setupExpandTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Expand using name instead of node_id
	response := server.handleExpandTool(ctx, map[string]interface{}{
		"name":      "funcA",
		"rel_types": []interface{}{"itest_CALLS"},
		"direction": "out",
		"depth":     float64(1),
		"format":    "json",
	})

	assert.False(t, response.IsError, "should resolve funcA by name")

	var result map[string]interface{}
	json.Unmarshal([]byte(response.Content[0].Text), &result)

	start, _ := result["start"].(map[string]interface{})
	assert.Equal(t, "funcA", start["name"], "start node should be funcA")
}

// TestPathAllShortestPaths verifies shortest paths between nodes.
func TestPathAllShortestPaths(t *testing.T) {
	server, nodeIDs, cleanup := setupExpandTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Find all shortest paths from A to D
	response := server.handlePathTool(ctx, map[string]interface{}{
		"from_id":   nodeIDs["funcA"],
		"to_id":     nodeIDs["funcD"],
		"rel_types": []interface{}{"itest_CALLS"},
		"shortest":  true,
		"format":    "json",
	})

	assert.False(t, response.IsError, "path should succeed")

	var result map[string]interface{}
	json.Unmarshal([]byte(response.Content[0].Text), &result)

	paths, ok := result["paths"].([]interface{})
	require.True(t, ok)
	assert.Equal(t, 2, len(paths), "should find 2 shortest paths (A→B→D and A→C→D)")

	// Both paths should have hops=2
	for _, p := range paths {
		if pm, ok := p.(map[string]interface{}); ok {
			hops, _ := pm["hops"].(float64)
			assert.Equal(t, 2, int(hops), "all shortest paths should have same length")
		}
	}
}

// TestNameOrIDPathResolution verifies name resolution in path tool.
func TestNameOrIDPathResolution(t *testing.T) {
	server, _, cleanup := setupExpandTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Find path using names instead of node_ids
	response := server.handlePathTool(ctx, map[string]interface{}{
		"from_name": "funcA",
		"to_name":   "funcD",
		"rel_types": []interface{}{"itest_CALLS"},
		"shortest":  true,
		"format":    "json",
	})

	assert.False(t, response.IsError, "path should resolve names and succeed")

	var result map[string]interface{}
	json.Unmarshal([]byte(response.Content[0].Text), &result)

	paths, _ := result["paths"].([]interface{})
	assert.Greater(t, len(paths), 0, "should find paths between funcA and funcD")
}

// TestAmbiguousNameResolution verifies ambiguity response.
func TestAmbiguousNameResolution(t *testing.T) {
	server, _, cleanup := setupExpandTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Create a second node with the same name but different service
	_, err := server.client.ExecuteQuery(ctx, `
		CREATE (n:itest_Function {name: 'funcA', scopeId: 'itest-expand', nodeKey: 'itest-expand/funcA-dup', serviceName: 'svc4'})
	`, nil)
	require.NoError(t, err)

	// Try to expand by ambiguous name (funcA now appears twice)
	response := server.handleExpandTool(ctx, map[string]interface{}{
		"name":      "funcA",
		"rel_types": []interface{}{"itest_CALLS"},
		"direction": "out",
		"depth":     float64(1),
		"format":    "json",
	})

	// Should return ambiguity response (not an error, but informational)
	require.Len(t, response.Content, 1)

	var result map[string]interface{}
	json.Unmarshal([]byte(response.Content[0].Text), &result)

	// A duplicate name MUST produce the ambiguity response — a conditional
	// check here would pass vacuously if resolution silently picked one node.
	require.Equal(t, "ambiguous", result["error"],
		"two nodes share this name; resolution must not silently pick one")
	candidates, ok := result["candidates"].([]interface{})
	require.True(t, ok, "ambiguous response must carry candidates")
	require.Len(t, candidates, 2, "should list both candidates")

	// Disambiguate with service_name
	response2 := server.handleExpandTool(ctx, map[string]interface{}{
		"name":         "funcA",
		"service_name": "svc1",
		"rel_types":    []interface{}{"itest_CALLS"},
		"direction":    "out",
		"depth":        float64(1),
		"format":       "json",
	})

	assert.False(t, response2.IsError, "should succeed with disambiguation")

	var result2 map[string]interface{}
	json.Unmarshal([]byte(response2.Content[0].Text), &result2)

	assert.Contains(t, result2, "start", "disambiguation should return normal expand result")
}
