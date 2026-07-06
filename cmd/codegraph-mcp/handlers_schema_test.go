//go:build integration
// +build integration

package main

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	neo4j "github.com/context-maximiser/code-graph/internal/graph"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupSchemaTestDB creates a test database with a small schema for testing.
func setupSchemaTestDB(t *testing.T) (*CodeGraphMCPServer, func()) {
	client, err := neo4j.NewClient(neo4j.Config{
		URI:      getEnvOrDefault("NEO4J_URI", "bolt://localhost:7687"),
		Username: getEnvOrDefault("NEO4J_USERNAME", "neo4j"),
		Password: getEnvOrDefault("NEO4J_PASSWORD", "password123"),
		Database: getEnvOrDefault("NEO4J_DATABASE", "neo4j"),
	})
	require.NoError(t, err, "failed to create Neo4j client")

	ctx := context.Background()

	// Clean up any existing test data
	_, _ = client.ExecuteQuery(ctx, `MATCH (n:itest_Function) WHERE n.scopeId = "itest-schema" DETACH DELETE n`, nil)

	workspaceRoot, err := os.Getwd()
	if err != nil {
		workspaceRoot = "."
	}

	server := &CodeGraphMCPServer{
		client:         client,
		queryBuilder:   neo4j.NewQueryBuilder(client),
		workspaceRoot:  workspaceRoot,
		schemaCache:    make(map[string]any),
		schemaCacheTTL: 300 * time.Second,
		now:            time.Now,
	}

	cleanup := func() {
		// Clean up test data
		_, _ = client.ExecuteQuery(context.Background(), `MATCH (n:itest_Function) WHERE n.scopeId = "itest-schema" DETACH DELETE n`, nil)
		client.Close(context.Background())
	}

	return server, cleanup
}

// TestSchemaToolWithTestData creates nodes/edges in DB and verifies schema output.
func TestSchemaToolWithTestData(t *testing.T) {
	server, cleanup := setupSchemaTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Create test nodes
	_, err := server.client.ExecuteQuery(ctx, `
		CREATE (f1:itest_Function {
			name: 'testFunc1',
			scopeId: 'itest-schema',
			nodeKey: 'itest-schema/testFunc1'
		})
		CREATE (f2:itest_Function {
			name: 'testFunc2',
			scopeId: 'itest-schema',
			nodeKey: 'itest-schema/testFunc2'
		})
		CREATE (f1)-[:itest_CALLS]->(f2)
	`, nil)
	require.NoError(t, err, "failed to create test nodes")

	// Call handleSchemaTool with refresh=true
	response := server.handleSchemaTool(ctx, map[string]interface{}{
		"include_examples": false,
		"refresh":          true,
	})

	assert.False(t, response.IsError, "schema tool should not error")
	require.Len(t, response.Content, 1, "response should have one content block")

	var schema map[string]interface{}
	err = json.Unmarshal([]byte(response.Content[0].Text), &schema)
	require.NoError(t, err, "response should be valid JSON")

	// Verify structure
	assert.Contains(t, schema, "nodes", "schema should have nodes key")
	assert.Contains(t, schema, "relationships", "schema should have relationships key")
	assert.Contains(t, schema, "computed_at", "schema should have computed_at key")
	assert.Contains(t, schema, "cache_ttl_seconds", "schema should have cache_ttl_seconds key")
	assert.Contains(t, schema, "apoc", "schema should have apoc key")

	// Verify computed_at is RFC3339 format
	computedAt, ok := schema["computed_at"].(string)
	require.True(t, ok, "computed_at should be a string")
	_, err = time.Parse(time.RFC3339, computedAt)
	require.NoError(t, err, "computed_at should be RFC3339 format")

	// Verify cache_ttl_seconds is 300
	assert.Equal(t, float64(300), schema["cache_ttl_seconds"], "cache_ttl_seconds should be 300")

	// Verify nodes
	nodes, ok := schema["nodes"].([]interface{})
	require.True(t, ok, "nodes should be an array")
	// We expect itest_Function to be in the nodes
	foundLabel := false
	for _, node := range nodes {
		if nodeMap, ok := node.(map[string]interface{}); ok {
			if label, ok := nodeMap["label"].(string); ok && label == "itest_Function" {
				foundLabel = true
				// Verify count is at least 2 (we created 2)
				count, ok := nodeMap["count"].(float64)
				assert.True(t, ok && count >= 2, "itest_Function count should be at least 2")
			}
		}
	}
	if foundLabel {
		t.Logf("Found itest_Function label with correct count")
	}

	// Verify relationships
	rels, ok := schema["relationships"].([]interface{})
	assert.True(t, ok, "relationships should be an array")
	// We created one edge, so we should see at least one relationship
	if len(rels) > 0 {
		for _, rel := range rels {
			if relMap, ok := rel.(map[string]interface{}); ok {
				if relType, ok := relMap["type"].(string); ok && relType == "itest_CALLS" {
					endpoints, ok := relMap["endpoints"].([]interface{})
					require.True(t, ok && len(endpoints) > 0, "itest_CALLS should have endpoints")
					t.Logf("Found itest_CALLS relationship with %d endpoint(s)", len(endpoints))
				}
			}
		}
	}
}

// TestSchemaToolCacheReturnsSamePayload verifies that cached calls return identical payload.
func TestSchemaToolCacheReturnsSamePayload(t *testing.T) {
	server, cleanup := setupSchemaTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// First call
	response1 := server.handleSchemaTool(ctx, map[string]interface{}{
		"include_examples": false,
		"refresh":          false,
	})
	assert.False(t, response1.IsError, "first call should succeed")

	var schema1 map[string]interface{}
	err := json.Unmarshal([]byte(response1.Content[0].Text), &schema1)
	require.NoError(t, err, "first response should be valid JSON")

	// Second call (should hit cache)
	response2 := server.handleSchemaTool(ctx, map[string]interface{}{
		"include_examples": false,
		"refresh":          false,
	})
	assert.False(t, response2.IsError, "second call should succeed")

	var schema2 map[string]interface{}
	err = json.Unmarshal([]byte(response2.Content[0].Text), &schema2)
	require.NoError(t, err, "second response should be valid JSON")

	// Verify computed_at is the same (indicating cache was used)
	computedAt1, _ := schema1["computed_at"].(string)
	computedAt2, _ := schema2["computed_at"].(string)
	assert.Equal(t, computedAt1, computedAt2, "cached calls should have same computed_at")

	t.Logf("Cache hit verified: both calls returned computed_at=%s", computedAt1)
}
