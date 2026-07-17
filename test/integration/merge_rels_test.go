package integration

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMergeRelsBatch verifies that MergeRelsBatch is idempotent: calling it
// twice with the same (from, to, relType) should result in exactly one
// relationship with merged properties, not duplicates.
func TestMergeRelsBatch(t *testing.T) {
	client, cleanup := setupTestDBWithScopeCleanup(t, "test-merge-rels")
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create two nodes for the merge test
	fromID, err := client.CreateNode(ctx, []string{"Function"}, map[string]any{
		"nodeKey":   "test-merge-rels/from-func",
		"name":      "fromFunc",
		"scopeId":   "test-merge-rels",
		"filePath":  "test.go",
		"startLine": 1,
		"endLine":   5,
	})
	require.NoError(t, err, "failed to create source node")

	toID, err := client.CreateNode(ctx, []string{"Function"}, map[string]any{
		"nodeKey":   "test-merge-rels/to-func",
		"name":      "toFunc",
		"scopeId":   "test-merge-rels",
		"filePath":  "test.go",
		"startLine": 10,
		"endLine":   15,
	})
	require.NoError(t, err, "failed to create target node")

	// First MergeRelsBatch call with initial props
	rels1 := []map[string]any{
		{
			"fromId": fromID,
			"toId":   toID,
			"props": map[string]any{
				"count": 1,
				"line":  10,
			},
		},
	}
	err = client.MergeRelsBatch(ctx, "CALLS", rels1, 500)
	require.NoError(t, err, "first MergeRelsBatch failed")

	// Verify exactly one relationship exists after first merge
	records, err := client.ExecuteQuery(ctx, `
		MATCH (a)-[r:CALLS]->(b)
		WHERE elementId(a) = $fromId AND elementId(b) = $toId
		RETURN count(r) as relCount, properties(r) as props
	`, map[string]any{
		"fromId": fromID,
		"toId":   toID,
	})
	require.NoError(t, err, "query for relationships failed")
	require.Len(t, records, 1, "expected exactly one relationship after first merge")

	relCount1 := records[0].AsMap()["relCount"]
	assert.Equal(t, int64(1), relCount1, "expected exactly one relationship after first merge")

	props1 := records[0].AsMap()["props"].(map[string]any)
	assert.Equal(t, int64(1), props1["count"], "expected count=1 after first merge")
	assert.Equal(t, int64(10), props1["line"], "expected line=10 after first merge")

	// Second MergeRelsBatch call with same fromId/toId but updated props
	// SET r += means properties are merged (new values override old values)
	rels2 := []map[string]any{
		{
			"fromId": fromID,
			"toId":   toID,
			"props": map[string]any{
				"count": 2,
				"line":  20,
			},
		},
	}
	err = client.MergeRelsBatch(ctx, "CALLS", rels2, 500)
	require.NoError(t, err, "second MergeRelsBatch failed")

	// Verify still exactly one relationship after second merge
	records, err = client.ExecuteQuery(ctx, `
		MATCH (a)-[r:CALLS]->(b)
		WHERE elementId(a) = $fromId AND elementId(b) = $toId
		RETURN count(r) as relCount, properties(r) as props
	`, map[string]any{
		"fromId": fromID,
		"toId":   toID,
	})
	require.NoError(t, err, "query for relationships failed after second merge")
	require.Len(t, records, 1, "expected exactly one relationship after second merge")

	relCount2 := records[0].AsMap()["relCount"]
	assert.Equal(t, int64(1), relCount2, "expected exactly one relationship after second merge (idempotency)")

	props2 := records[0].AsMap()["props"].(map[string]any)
	assert.Equal(t, int64(2), props2["count"], "expected count=2 after second merge (SET r += semantics)")
	assert.Equal(t, int64(20), props2["line"], "expected line=20 after second merge (SET r += semantics)")
}

// TestCreateRelsBatchNonIdempotent verifies that CreateRelsBatch is NOT idempotent:
// calling it twice with the same (from, to, relType) creates two relationships.
// This documents the existing behavior that MergeRelsBatch replaces for idempotency.
func TestCreateRelsBatchNonIdempotent(t *testing.T) {
	client, cleanup := setupTestDBWithScopeCleanup(t, "test-create-rels")
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create two nodes for the create test
	fromID, err := client.CreateNode(ctx, []string{"Function"}, map[string]any{
		"nodeKey":   "test-create-rels/from-func",
		"name":      "fromFunc",
		"scopeId":   "test-create-rels",
		"filePath":  "test.go",
		"startLine": 1,
		"endLine":   5,
	})
	require.NoError(t, err, "failed to create source node")

	toID, err := client.CreateNode(ctx, []string{"Function"}, map[string]any{
		"nodeKey":   "test-create-rels/to-func",
		"name":      "toFunc",
		"scopeId":   "test-create-rels",
		"filePath":  "test.go",
		"startLine": 10,
		"endLine":   15,
	})
	require.NoError(t, err, "failed to create target node")

	// First CreateRelsBatch call
	rels1 := []map[string]any{
		{
			"fromId": fromID,
			"toId":   toID,
			"props": map[string]any{
				"count": 1,
				"line":  10,
			},
		},
	}
	err = client.CreateRelsBatch(ctx, "CALLS", rels1, 500)
	require.NoError(t, err, "first CreateRelsBatch failed")

	// Verify exactly one relationship exists after first create
	records, err := client.ExecuteQuery(ctx, `
		MATCH (a)-[r:CALLS]->(b)
		WHERE elementId(a) = $fromId AND elementId(b) = $toId
		RETURN count(r) as relCount
	`, map[string]any{
		"fromId": fromID,
		"toId":   toID,
	})
	require.NoError(t, err, "query for relationships failed")
	require.Len(t, records, 1, "expected exactly one relationship after first create")

	relCount1 := records[0].AsMap()["relCount"]
	assert.Equal(t, int64(1), relCount1, "expected exactly one relationship after first create")

	// Second CreateRelsBatch call with identical item
	rels2 := []map[string]any{
		{
			"fromId": fromID,
			"toId":   toID,
			"props": map[string]any{
				"count": 1,
				"line":  10,
			},
		},
	}
	err = client.CreateRelsBatch(ctx, "CALLS", rels2, 500)
	require.NoError(t, err, "second CreateRelsBatch failed")

	// Verify now TWO relationships exist (demonstrating non-idempotency)
	records, err = client.ExecuteQuery(ctx, `
		MATCH (a)-[r:CALLS]->(b)
		WHERE elementId(a) = $fromId AND elementId(b) = $toId
		RETURN count(r) as relCount
	`, map[string]any{
		"fromId": fromID,
		"toId":   toID,
	})
	require.NoError(t, err, "query for relationships failed after second create")
	require.Len(t, records, 1, "expected one record with count")

	relCount2 := records[0].AsMap()["relCount"]
	assert.Equal(t, int64(2), relCount2, "expected TWO relationships after second create (CreateRelsBatch is not idempotent)")
}
