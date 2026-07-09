package integration

import (
	"context"
	"testing"
	"time"

	neo4jgraph "github.com/context-maximiser/code-graph/internal/graph"
	schema "github.com/context-maximiser/code-graph/internal/graph/schema"
	"github.com/stretchr/testify/require"
)

// The definition-level tests for GetFulltextIndexes live in
// internal/graph/schema/schema_test.go (pure unit tests, no DB). This file
// covers the live-database side: creation, an actual queryNodes round-trip,
// and drop.

// TestFulltextIndexesCreation verifies that CreateSchema creates all fulltext indexes.
func TestFulltextIndexesCreation(t *testing.T) {
	ctx := context.Background()
	client, cleanup := setupTestDBWithScopeCleanup(t, "test-fulltext-indexes")
	defer cleanup()

	// Create schema (which creates indexes)
	sm := schema.NewSchemaManager(client)
	err := sm.CreateSchema(ctx)
	require.NoError(t, err, "failed to create schema")

	// Query for fulltext indexes
	cypher := "SHOW INDEXES YIELD name, type WHERE type = 'FULLTEXT'"
	records, err := client.ExecuteQuery(ctx, cypher, nil)
	require.NoError(t, err, "failed to query indexes")

	// Verify all expected indexes exist
	indexNames := make(map[string]bool)
	for _, record := range records {
		m := record.AsMap()
		if name, ok := m["name"].(string); ok {
			indexNames[name] = true
		}
	}

	expectedIndexNames := []string{
		"function_fulltext",
		"method_fulltext",
		"class_fulltext",
		"interface_fulltext",
		"symbol_fulltext",
		"file_fulltext",
		"variable_fulltext",
	}

	for _, name := range expectedIndexNames {
		require.True(t, indexNames[name], "missing fulltext index: %s", name)
	}
}

// TestFulltextIndexSearchFunctionality verifies that fulltext indexes actually work.
// This is the known-positive test: create a scoped test node, query with the fulltext
// index, and assert the node comes back.
func TestFulltextIndexSearchFunctionality(t *testing.T) {
	ctx := context.Background()
	client, cleanup := setupTestDBWithScopeCleanup(t, "test-fulltext-search")
	defer cleanup()

	// Create schema
	sm := schema.NewSchemaManager(client)
	err := sm.CreateSchema(ctx)
	require.NoError(t, err, "failed to create schema")

	// Create a test function node with a unique name for searching
	testFunctionName := "TestFunctionForFulltextSearch"
	testNodeKey := "test-fulltext-search/func1"
	testServiceName := "test-fulltext-search"

	nodeID, err := client.CreateNode(ctx, []string{"Function"}, map[string]any{
		"nodeKey":     testNodeKey,
		"name":        testFunctionName,
		"signature":   "func() error",
		"serviceName": testServiceName,
		"scopeId":     "test-fulltext-search",
		"filePath":    "test.go",
	})
	require.NoError(t, err, "failed to create test node")
	require.NotEmpty(t, nodeID, "expected non-empty node ID")

	// Query using fulltext index: db.index.fulltext.queryNodes
	// Neo4j 5 FULLTEXT index query syntax
	queryText := "TestFunctionForFulltextSearch"
	fulltextQuery := `
		CALL db.index.fulltext.queryNodes('function_fulltext', $query)
		YIELD node, score
		WHERE node.scopeId = $scopeId
		RETURN node.name AS name, node.signature AS signature
	`

	records, err := client.ExecuteQuery(ctx, fulltextQuery, map[string]any{
		"query":   queryText,
		"scopeId": "test-fulltext-search",
	})
	require.NoError(t, err, "failed to query fulltext index")

	// Verify the test node comes back in search results
	require.Greater(t, len(records), 0, "fulltext search should find the test node")

	foundNode := false
	for _, record := range records {
		m := record.AsMap()
		if name, ok := m["name"].(string); ok && name == testFunctionName {
			foundNode = true
			if sig, ok := m["signature"].(string); ok {
				require.Equal(t, "func() error", sig, "signature should match")
			}
			break
		}
	}

	require.True(t, foundNode, "fulltext search must return the test node with name=%s", testFunctionName)
}

// TestFulltextIndexesDrop verifies that DropSchema removes fulltext indexes.
func TestFulltextIndexesDrop(t *testing.T) {
	ctx := context.Background()
	client, cleanup := setupTestDBWithScopeCleanup(t, "test-fulltext-drop")
	defer cleanup()

	// Create schema
	sm := schema.NewSchemaManager(client)
	err := sm.CreateSchema(ctx)
	require.NoError(t, err, "failed to create schema")

	// Verify indexes exist
	cypher := "SHOW INDEXES YIELD name, type WHERE type = 'FULLTEXT' AND name = 'function_fulltext'"
	records, err := client.ExecuteQuery(ctx, cypher, nil)
	require.NoError(t, err, "query failed")
	require.Greater(t, len(records), 0, "function_fulltext index should exist after CreateSchema")

	// DropSchema wipes ALL constraints and indexes in the shared dev
	// database, not a test-scoped subset — restore the schema on the way out
	// no matter how the assertions below go, so this test never leaves the
	// database schema-less for whatever runs after it. t.Cleanup fires after
	// the deferred cleanup() above has already closed `client`, so this must
	// dial its own connection (same closed-driver ordering trap documented in
	// test/harness/golden_test.go).
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer ccancel()
		restoreClient, err := neo4jgraph.NewClient(neo4jgraph.Config{
			URI: testNeo4jURI, Username: testNeo4jUser, Password: testNeo4jPass, Database: testNeo4jDB,
		})
		if err != nil {
			t.Errorf("failed to reconnect for schema restore after DropSchema test: %v", err)
			return
		}
		defer restoreClient.Close(cctx)
		if err := schema.NewSchemaManager(restoreClient).CreateSchema(cctx); err != nil {
			t.Errorf("failed to restore schema after DropSchema test: %v", err)
		}
	})

	// Drop schema
	err = sm.DropSchema(ctx)
	require.NoError(t, err, "failed to drop schema")

	// Verify indexes are gone
	records, err = client.ExecuteQuery(ctx, cypher, nil)
	require.NoError(t, err, "query failed")
	require.Empty(t, records, "fulltext indexes should be dropped")
}
