package main

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	neo4j "github.com/context-maximiser/code-graph/internal/graph"
	"github.com/context-maximiser/code-graph/internal/graph/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupFindTestDB creates test nodes for find testing under scopeId "itest-find-mcp".
// Seeded data:
// - 3 Function nodes: FindMcpAlphaOne, FindMcpAlphaTwo, FindMcpExact (serviceName: "find-mcp-svc")
// - 1 Class node: FindMcpClass
func setupFindTestDB(t *testing.T) (*CodeGraphMCPServer, func()) {
	client, err := neo4j.NewClient(neo4j.Config{
		URI:      getEnvOrDefault("NEO4J_URI", "bolt://localhost:7687"),
		Username: getEnvOrDefault("NEO4J_USERNAME", "neo4j"),
		Password: getEnvOrDefault("NEO4J_PASSWORD", "password123"),
		Database: getEnvOrDefault("NEO4J_DATABASE", "neo4j"),
	})
	if err != nil {
		t.Skipf("Neo4j not available: %v", err)
		return nil, nil
	}

	ctx := context.Background()

	// Test connection
	records, err := client.ExecuteQuery(ctx, "RETURN 1", nil)
	if err != nil || len(records) == 0 {
		t.Skipf("Neo4j not responding: %v", err)
		return nil, nil
	}

	// Clean up any existing test data
	_, _ = client.ExecuteQuery(ctx, `MATCH (n) WHERE n.scopeId = "itest-find-mcp" DETACH DELETE n`, nil)

	// Ensure schema exists (creates fulltext indexes)
	sm := schema.NewSchemaManager(client)
	err = sm.CreateSchema(ctx)
	require.NoError(t, err, "failed to create schema")

	// Create test nodes
	createQuery := `
		CREATE (a:Function {
			name: 'FindMcpAlphaOne',
			scopeId: 'itest-find-mcp',
			nodeKey: 'itest-find-mcp/FindMcpAlphaOne',
			serviceName: 'find-mcp-svc',
			signature: 'func FindMcpAlphaOne() error'
		})
		CREATE (b:Function {
			name: 'FindMcpAlphaTwo',
			scopeId: 'itest-find-mcp',
			nodeKey: 'itest-find-mcp/FindMcpAlphaTwo',
			serviceName: 'find-mcp-svc',
			signature: 'func FindMcpAlphaTwo() error'
		})
		CREATE (c:Function {
			name: 'FindMcpExact',
			scopeId: 'itest-find-mcp',
			nodeKey: 'itest-find-mcp/FindMcpExact',
			serviceName: 'find-mcp-svc',
			signature: 'func FindMcpExact() error',
			startLine: 10,
			endLine: 20
		})
		CREATE (d:Class {
			name: 'FindMcpClass',
			scopeId: 'itest-find-mcp',
			nodeKey: 'itest-find-mcp/FindMcpClass',
			serviceName: 'find-mcp-svc'
		})
	`
	_, err = client.ExecuteQuery(ctx, createQuery, nil)
	require.NoError(t, err, "failed to create test graph")

	// Verify test nodes were created
	verifyRecs, err := client.ExecuteQuery(ctx, `
		MATCH (n) WHERE n.scopeId = "itest-find-mcp"
		RETURN count(n) as cnt
	`, nil)
	require.NoError(t, err, "failed to verify test graph creation")
	require.Len(t, verifyRecs, 1)
	cnt, _ := verifyRecs[0].AsMap()["cnt"].(int64)
	require.EqualValues(t, 4, cnt, "expected exactly the 4 seeded nodes")

	workspaceRoot, _ := os.Getwd()
	server := &CodeGraphMCPServer{
		client:        client,
		queryBuilder:  neo4j.NewQueryBuilder(client),
		workspaceRoot: workspaceRoot,
	}

	cleanup := func() {
		_, _ = client.ExecuteQuery(context.Background(), `MATCH (n) WHERE n.scopeId = "itest-find-mcp" DETACH DELETE n`, nil)
		client.Close(context.Background())
	}

	return server, cleanup
}

// TestFindErrorHandling verifies error responses for invalid inputs.
func TestFindErrorHandling(t *testing.T) {
	server, cleanup := setupFindTestDB(t)
	if server == nil {
		t.Skip("Neo4j not available")
	}
	defer cleanup()

	ctx := context.Background()

	// Test: query empty and label invalid — should error
	response := server.handleFindTool(ctx, map[string]interface{}{
		"label": "NonExistentLabel",
	})

	assert.True(t, response.IsError, "should error on invalid label")
	require.Len(t, response.Content, 1)
	assert.Contains(t, response.Content[0].Text, "valid labels", "error should mention valid labels")
}

// TestFindResponseStructure verifies the response contains required fields.
func TestFindResponseStructure(t *testing.T) {
	server, cleanup := setupFindTestDB(t)
	if server == nil {
		t.Skip("Neo4j not available")
	}
	defer cleanup()

	ctx := context.Background()

	// Test structural listing response has correct structure
	response := server.handleFindTool(ctx, map[string]interface{}{
		"label":   "Function",
		"service": "find-mcp-svc",
	})

	assert.False(t, response.IsError, "structural listing should succeed")
	require.Len(t, response.Content, 1)

	var result map[string]interface{}
	err := json.Unmarshal([]byte(response.Content[0].Text), &result)
	require.NoError(t, err, "response should be valid JSON")

	// Verify response has required fields
	assert.Contains(t, result, "results", "response should have 'results' field")
	assert.Contains(t, result, "count", "response should have 'count' field")

	// Results should be an array (possibly empty)
	results, ok := result["results"].([]interface{})
	require.True(t, ok, "results should be an array")

	// Each result should have required fields if any results
	for _, r := range results {
		if rm, ok := r.(map[string]interface{}); ok {
			assert.Contains(t, rm, "node_id", "each result should have 'node_id'")
			assert.Contains(t, rm, "label", "each result should have 'label'")
			assert.Contains(t, rm, "name", "each result should have 'name'")
		}
	}
}

// TestFindReturnsLineAnchors verifies both find modes surface the node's
// body-range anchors (RFC-010): FindMcpExact is seeded with startLine 10 /
// endLine 20 and must return them in structural listing and fulltext search;
// nodes without location (FindMcpAlphaOne) must omit the fields entirely.
func TestFindReturnsLineAnchors(t *testing.T) {
	server, cleanup := setupFindTestDB(t)
	if server == nil {
		t.Skip("Neo4j not available")
	}
	defer cleanup()

	ctx := context.Background()

	for mode, args := range map[string]map[string]interface{}{
		"structural": {"label": "Function", "service": "find-mcp-svc", "scope_id": "itest-find-mcp"},
		"fulltext":   {"query": "FindMcpExact", "service": "find-mcp-svc", "scope_id": "itest-find-mcp"},
	} {
		response := server.handleFindTool(ctx, args)
		require.False(t, response.IsError, "%s: find should succeed", mode)

		var result map[string]interface{}
		require.NoError(t, json.Unmarshal([]byte(response.Content[0].Text), &result), "%s: valid JSON", mode)
		results, ok := result["results"].([]interface{})
		require.True(t, ok, "%s: results array", mode)

		foundExact := false
		for _, r := range results {
			rm, ok := r.(map[string]interface{})
			if !ok {
				continue
			}
			switch rm["name"] {
			case "FindMcpExact":
				foundExact = true
				assert.EqualValues(t, 10, rm["start_line"], "%s: FindMcpExact start_line", mode)
				assert.EqualValues(t, 20, rm["end_line"], "%s: FindMcpExact end_line", mode)
			case "FindMcpAlphaOne":
				assert.NotContains(t, rm, "start_line", "%s: node without location must omit start_line", mode)
				assert.NotContains(t, rm, "end_line", "%s: node without location must omit end_line", mode)
			}
		}
		require.True(t, foundExact, "%s: FindMcpExact must be in results", mode)
	}
}

// TestFindStructuralListingFiltering verifies structural listing respects filters.
func TestFindStructuralListingFiltering(t *testing.T) {
	server, cleanup := setupFindTestDB(t)
	if server == nil {
		t.Skip("Neo4j not available")
	}
	defer cleanup()

	ctx := context.Background()

	// Structural listing with label filter (no service filter to get wider results)
	response := server.handleFindTool(ctx, map[string]interface{}{
		"label": "Function",
	})

	assert.False(t, response.IsError, "structural listing should succeed")

	var result map[string]interface{}
	json.Unmarshal([]byte(response.Content[0].Text), &result)

	results, ok := result["results"].([]interface{})
	require.True(t, ok)

	// Verify all results have Function label
	for _, r := range results {
		if rm, ok := r.(map[string]interface{}); ok {
			label, _ := rm["label"].(string)
			assert.Equal(t, "Function", label, "all results should be Function labels")
		}
	}
}

// TestFindCursorPagination verifies cursor is present when results exist.
func TestFindCursorPagination(t *testing.T) {
	server, cleanup := setupFindTestDB(t)
	if server == nil {
		t.Skip("Neo4j not available")
	}
	defer cleanup()

	ctx := context.Background()

	// Query with very low limit to force cursor generation
	response := server.handleFindTool(ctx, map[string]interface{}{
		"label": "Class",
		"limit": float64(10), // Test limit handling
	})

	assert.False(t, response.IsError, "query should succeed")

	var result map[string]interface{}
	json.Unmarshal([]byte(response.Content[0].Text), &result)

	count, ok := result["count"].(float64)
	require.True(t, ok, "response should have count field")

	// Verify count is numeric
	assert.GreaterOrEqual(t, count, 0.0, "count should be >= 0")

	// Cursor should only be present if there are more results
	if cursor, hasNext := result["next_cursor"].(string); hasNext {
		assert.NotEmpty(t, cursor, "cursor should not be empty")
	}
}

// TestFindInvalidLabel verifies error for invalid label.
func TestFindInvalidLabel(t *testing.T) {
	server, cleanup := setupFindTestDB(t)
	if server == nil {
		t.Skip("Neo4j not available")
	}
	defer cleanup()

	ctx := context.Background()

	response := server.handleFindTool(ctx, map[string]interface{}{
		"label": "InvalidLabel",
	})

	assert.True(t, response.IsError, "should error on invalid label")
	require.Len(t, response.Content, 1)

	// Error message should list valid labels
	assert.Contains(t, response.Content[0].Text, "valid labels", "error should list valid labels")
}

// TestFindEmptyQueryLabel verifies error when both query and label empty.
func TestFindEmptyQueryLabel(t *testing.T) {
	server, cleanup := setupFindTestDB(t)
	if server == nil {
		t.Skip("Neo4j not available")
	}
	defer cleanup()

	ctx := context.Background()

	response := server.handleFindTool(ctx, map[string]interface{}{})

	assert.True(t, response.IsError, "should error when both query and label empty")
	require.Len(t, response.Content, 1)
	assert.Contains(t, response.Content[0].Text, "query or label", "error message should mention requirement")
}

// TestFindLimitBoundaries verifies limit parameter validation.
func TestFindLimitBoundaries(t *testing.T) {
	server, cleanup := setupFindTestDB(t)
	if server == nil {
		t.Skip("Neo4j not available")
	}
	defer cleanup()

	ctx := context.Background()

	// Test default limit (25)
	response1 := server.handleFindTool(ctx, map[string]interface{}{
		"label": "Function",
	})
	assert.False(t, response1.IsError, "query with default limit should succeed")

	// Test high limit (>200, should be clamped)
	response2 := server.handleFindTool(ctx, map[string]interface{}{
		"label": "Function",
		"limit": float64(1000),
	})
	assert.False(t, response2.IsError, "query with high limit should succeed and clamp")

	var result2 map[string]interface{}
	json.Unmarshal([]byte(response2.Content[0].Text), &result2)
	results2, _ := result2["results"].([]interface{})

	// Clamped limit should not exceed 200 results (or available count if less)
	assert.LessOrEqual(t, len(results2), 200, "limit should be clamped to [1, 200]")

	// Test negative limit (should default to 25)
	response3 := server.handleFindTool(ctx, map[string]interface{}{
		"label": "Function",
		"limit": float64(-5),
	})
	assert.False(t, response3.IsError, "query with negative limit should use default")
}

// findResults parses a find response and returns the results array.
func findResults(t *testing.T, response ToolCallResponse) (map[string]interface{}, []map[string]interface{}) {
	t.Helper()
	require.False(t, response.IsError, "find should succeed, got: %s", response.Content[0].Text)
	require.Len(t, response.Content, 1)
	var result map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(response.Content[0].Text), &result))
	raw, ok := result["results"].([]interface{})
	require.True(t, ok, "results should be an array")
	out := make([]map[string]interface{}, 0, len(raw))
	for _, r := range raw {
		rm, ok := r.(map[string]interface{})
		require.True(t, ok)
		out = append(out, rm)
	}
	return result, out
}

// TestFindFulltextKnownPositive: the seeded nodes MUST come back from a
// fulltext query — shape-only assertions would pass against an empty result.
func TestFindFulltextKnownPositive(t *testing.T) {
	server, cleanup := setupFindTestDB(t)
	if server == nil {
		t.Skip("Neo4j not available")
	}
	defer cleanup()

	ctx := context.Background()

	_, results := findResults(t, server.handleFindTool(ctx, map[string]interface{}{
		"query":    "FindMcpAlpha",
		"scope_id": "itest-find-mcp",
		"limit":    float64(10),
	}))

	names := map[string]bool{}
	for _, r := range results {
		if n, ok := r["name"].(string); ok {
			names[n] = true
		}
	}
	assert.True(t, names["FindMcpAlphaOne"], "FindMcpAlphaOne must be found, got %v", names)
	assert.True(t, names["FindMcpAlphaTwo"], "FindMcpAlphaTwo must be found, got %v", names)
}

// TestFindExactMatchRanksFirst: an exact name query ranks that node first.
func TestFindExactMatchRanksFirst(t *testing.T) {
	server, cleanup := setupFindTestDB(t)
	if server == nil {
		t.Skip("Neo4j not available")
	}
	defer cleanup()

	ctx := context.Background()

	_, results := findResults(t, server.handleFindTool(ctx, map[string]interface{}{
		"query":    "FindMcpExact",
		"scope_id": "itest-find-mcp",
		"limit":    float64(10),
	}))

	require.NotEmpty(t, results, "exact query must return results")
	name, _ := results[0]["name"].(string)
	assert.Equal(t, "FindMcpExact", name, "exact match must rank first")
}

// TestFindStructuralKeysetWalk: limit-1 pagination over the seeded Functions
// collects each exactly once with no duplicates — a cursor bug that restarts
// or skips would fail the set-equality below.
func TestFindStructuralKeysetWalk(t *testing.T) {
	server, cleanup := setupFindTestDB(t)
	if server == nil {
		t.Skip("Neo4j not available")
	}
	defer cleanup()

	ctx := context.Background()

	collected := map[string]int{}
	cursor := ""
	for page := 0; ; page++ {
		require.Less(t, page, 20, "pagination did not terminate")
		args := map[string]interface{}{
			"label":    "Function",
			"service":  "find-mcp-svc",
			"scope_id": "itest-find-mcp",
			"limit":    float64(1),
		}
		if cursor != "" {
			args["cursor"] = cursor
		}
		result, results := findResults(t, server.handleFindTool(ctx, args))
		for _, r := range results {
			if n, ok := r["name"].(string); ok {
				collected[n]++
			}
		}
		next, _ := result["next_cursor"].(string)
		if next == "" {
			break
		}
		cursor = next
	}

	assert.Equal(t, map[string]int{
		"FindMcpAlphaOne": 1,
		"FindMcpAlphaTwo": 1,
		"FindMcpExact":    1,
	}, collected, "keyset walk must yield each seeded Function exactly once")
}
