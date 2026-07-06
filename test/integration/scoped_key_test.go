package integration

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	neo4j "github.com/context-maximiser/code-graph/internal/graph"
	"github.com/context-maximiser/code-graph/internal/graph/schema"
)

// TestScopedKeyDerivation verifies that scopedKey is automatically derived
// when nodes are created via CreateNode and MergeNodesBatch.
func TestScopedKeyDerivation(t *testing.T) {
	// Use unique scope with timestamp to avoid constraint conflicts
	testScope := fmt.Sprintf("test-scoped-key-derivation-%d", time.Now().UnixNano())
	client, cleanup := setupTestDBWithScopeCleanup(t, testScope)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Test 1: CreateNode derives scopedKey
	nodeID, err := client.CreateNode(ctx, []string{"Function"}, map[string]any{
		"nodeKey":  testScope + "/func1",
		"name":     "testFunc1",
		"scopeId":  testScope,
		"filePath": "test.go",
	})
	require.NoError(t, err, "CreateNode failed")

	// Verify scopedKey was set
	records, err := client.ExecuteQuery(ctx, `
		MATCH (n:Function)
		WHERE elementId(n) = $id
		RETURN n.scopedKey AS scopedKey
	`, map[string]any{"id": nodeID})
	require.NoError(t, err, "query failed")
	require.Len(t, records, 1, "expected one node")

	scopedKey1 := records[0].AsMap()["scopedKey"].(string)
	expectedKey1 := neo4j.ScopedKey(testScope+"/func1", testScope)
	assert.Equal(t, expectedKey1, scopedKey1, "CreateNode should derive scopedKey")

	// Test 2: CreateNode with missing scopeId defaults to main
	nodeID2, err := client.CreateNode(ctx, []string{"Class"}, map[string]any{
		"nodeKey":  testScope + "/class1",
		"name":     "testClass1",
		"filePath": "test.go",
		// scopeId intentionally omitted
	})
	require.NoError(t, err, "CreateNode with no scopeId failed")
	// This node has no scopeId property, so the scope-based cleanup can't
	// find it — delete it explicitly.
	defer func() {
		_, _ = client.ExecuteQuery(context.Background(),
			`MATCH (n:Class {nodeKey: $nodeKey}) DETACH DELETE n`,
			map[string]any{"nodeKey": testScope + "/class1"})
	}()

	records, err = client.ExecuteQuery(ctx, `
		MATCH (n:Class)
		WHERE elementId(n) = $id
		RETURN n.scopedKey AS scopedKey
	`, map[string]any{"id": nodeID2})
	require.NoError(t, err, "query failed")
	require.Len(t, records, 1, "expected one node")

	scopedKey2 := records[0].AsMap()["scopedKey"].(string)
	expectedKey2 := neo4j.ScopedKey(testScope+"/class1", "main")
	assert.Equal(t, expectedKey2, scopedKey2, "CreateNode should default scopeId to main")

	// Test 3: MergeNode derives scopedKey
	mergeID, err := client.MergeNode(ctx, []string{"Variable"}, map[string]any{
		"nodeKey": testScope + "/var1",
		"scopeId": testScope,
	}, map[string]any{
		"name":     "testVar1",
		"filePath": "test.go",
	})
	require.NoError(t, err, "MergeNode failed")

	records, err = client.ExecuteQuery(ctx, `
		MATCH (n:Variable)
		WHERE elementId(n) = $id
		RETURN n.scopedKey AS scopedKey
	`, map[string]any{"id": mergeID})
	require.NoError(t, err, "query failed")
	require.Len(t, records, 1, "expected one node")

	scopedKeyVar := records[0].AsMap()["scopedKey"].(string)
	expectedKeyVar := neo4j.ScopedKey(testScope+"/var1", testScope)
	assert.Equal(t, expectedKeyVar, scopedKeyVar, "MergeNode should derive scopedKey")

	// Test 4: MergeNodesBatch derives scopedKey
	batchItems := []map[string]any{
		{
			"nodeKey": testScope + "/func3",
			"scopeId": testScope,
			"props": map[string]any{
				"name":     "testFunc3",
				"filePath": "test.go",
			},
		},
	}
	ids, err := client.MergeNodesBatch(ctx, "Function", batchItems, 500)
	require.NoError(t, err, "MergeNodesBatch failed")
	require.NotEmpty(t, ids, "expected node ID from MergeNodesBatch")

	records, err = client.ExecuteQuery(ctx, `
		MATCH (n:Function)
		WHERE n.nodeKey = $nodeKey
		RETURN n.scopedKey AS scopedKey
	`, map[string]any{"nodeKey": testScope + "/func3"})
	require.NoError(t, err, "query failed")
	require.Len(t, records, 1, "expected one node")

	scopedKey3 := records[0].AsMap()["scopedKey"].(string)
	expectedKey3 := neo4j.ScopedKey(testScope+"/func3", testScope)
	assert.Equal(t, expectedKey3, scopedKey3, "MergeNodesBatch should derive scopedKey")
}

// TestScopedKeyConstraintCreation verifies that schema manager creates
// scopedKey UNIQUE constraints after the migrate command.
func TestScopedKeyConstraintCreation(t *testing.T) {
	client, cleanup := setupTestDBWithScopeCleanup(t, "test-scoped-constraints")
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Ensure schema exists (idempotent)
	sm := schema.NewSchemaManager(client)
	if err := sm.CreateSchema(ctx); err != nil {
		t.Fatalf("failed to create schema: %v", err)
	}

	// Verify the constraint is actually registered
	records, err := client.ExecuteQuery(ctx, `
		SHOW CONSTRAINTS YIELD name, type, labelsOrTypes, properties
		WHERE name = 'Symbol_scoped_key_unique'
		RETURN name, type, labelsOrTypes, properties
	`, nil)
	require.NoError(t, err, "SHOW CONSTRAINTS failed")
	require.Len(t, records, 1, "Symbol_scoped_key_unique constraint must exist after CreateSchema")

	rec := records[0].AsMap()
	assert.Equal(t, "UNIQUENESS", rec["type"], "constraint must be a uniqueness constraint")
	assert.Equal(t, []any{"Symbol"}, rec["labelsOrTypes"], "constraint must target the Symbol label")
	assert.Equal(t, []any{"scopedKey"}, rec["properties"], "constraint must be on scopedKey")

	// Behavioral check: a second node with the same (nodeKey, scopeId) — and
	// therefore the same derived scopedKey — must be rejected.
	_, err = client.CreateNode(ctx, []string{"Symbol"}, map[string]any{
		"nodeKey":     "test-scoped-constraints/sym1",
		"name":        "testSymbol",
		"scopeId":     "test-scoped-constraints",
		"displayName": "TestSymbol",
	})
	require.NoError(t, err, "failed to create test node")

	_, err = client.CreateNode(ctx, []string{"Symbol"}, map[string]any{
		"nodeKey":     "test-scoped-constraints/sym1",
		"name":        "duplicateSymbol",
		"scopeId":     "test-scoped-constraints",
		"displayName": "DuplicateSymbol",
	})
	require.Error(t, err, "duplicate scopedKey must violate the UNIQUE constraint")
	assert.Contains(t, err.Error(), "already exists", "error should be a uniqueness violation")
}
