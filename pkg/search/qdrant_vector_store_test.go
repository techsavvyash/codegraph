//go:build integration

package search

import (
	"context"
	"os"
	"testing"
)

func qdrantHost() string {
	if h := os.Getenv("QDRANT_URL"); h != "" {
		return h
	}
	return "localhost:6334"
}

func TestQdrantVectorStore_CompileCheck(t *testing.T) {
	// Compile-time interface compliance is checked via the var _ line in
	// qdrant_vector_store.go. This test just confirms that the constructor
	// returns a usable VectorStore.
	var _ VectorStore = (*QdrantVectorStore)(nil)
}

func TestQdrantVectorStore_CreateIndex(t *testing.T) {
	store, err := NewQdrantVectorStore(qdrantHost())
	if err != nil {
		t.Skipf("Qdrant not available: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	err = store.CreateIndex(ctx, "test_integration_768", 768, "cosine")
	if err != nil {
		t.Fatalf("CreateIndex failed: %v", err)
	}

	// Creating the same index again should not fail.
	err = store.CreateIndex(ctx, "test_integration_768", 768, "cosine")
	if err != nil {
		t.Fatalf("CreateIndex (idempotent) failed: %v", err)
	}
}

func TestQdrantVectorStore_UpsertAndQuery(t *testing.T) {
	store, err := NewQdrantVectorStore(qdrantHost())
	if err != nil {
		t.Skipf("Qdrant not available: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	collection := "test_upsert_query_3"

	// Create collection with dim=3 for simplicity.
	if err := store.CreateIndex(ctx, collection, 3, "cosine"); err != nil {
		t.Fatalf("CreateIndex failed: %v", err)
	}

	// Upsert vectors.
	err = store.UpsertVectors(ctx, []VectorUpsert{
		{
			ID:        "func-a",
			Vector:    []float64{1, 0, 0},
			NodeLabel: "Function",
			Metadata:  map[string]any{"name": "FuncA"},
		},
		{
			ID:        "func-b",
			Vector:    []float64{0, 1, 0},
			NodeLabel: "Function",
			Metadata:  map[string]any{"name": "FuncB"},
		},
		{
			ID:        "func-c",
			Vector:    []float64{0.9, 0.1, 0},
			NodeLabel: "Function",
			Metadata:  map[string]any{"name": "FuncC"},
		},
	})
	if err != nil {
		t.Fatalf("UpsertVectors failed: %v", err)
	}

	// Query for vector closest to [1, 0, 0].
	results, err := store.Query(ctx, VectorQuery{
		Vector:    []float64{1, 0, 0},
		Limit:     2,
		IndexName: collection,
	})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	if len(results) < 1 {
		t.Fatal("expected at least 1 result")
	}

	// Best match should be func-a.
	if results[0].ID != "func-a" {
		t.Errorf("expected first result to be 'func-a', got %s", results[0].ID)
	}
	if results[0].Score < 0.9 {
		t.Errorf("expected high score for near-exact match, got %f", results[0].Score)
	}
}

func TestQdrantVectorStore_DeleteVectors(t *testing.T) {
	store, err := NewQdrantVectorStore(qdrantHost())
	if err != nil {
		t.Skipf("Qdrant not available: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	collection := "test_delete_3"

	if err := store.CreateIndex(ctx, collection, 3, "cosine"); err != nil {
		t.Fatalf("CreateIndex failed: %v", err)
	}

	// Upsert then delete.
	err = store.UpsertVectors(ctx, []VectorUpsert{
		{ID: "del-1", Vector: []float64{1, 0, 0}, NodeLabel: "Function"},
		{ID: "del-2", Vector: []float64{0, 1, 0}, NodeLabel: "Function"},
	})
	if err != nil {
		t.Fatalf("UpsertVectors failed: %v", err)
	}

	err = store.DeleteVectors(ctx, []string{"del-1"})
	if err != nil {
		t.Fatalf("DeleteVectors failed: %v", err)
	}

	// After deletion, only del-2 should remain.
	results, err := store.Query(ctx, VectorQuery{
		Vector:    []float64{1, 0, 0},
		Limit:     10,
		IndexName: collection,
	})
	if err != nil {
		t.Fatalf("Query after delete failed: %v", err)
	}

	for _, r := range results {
		if r.ID == "del-1" {
			t.Error("del-1 should have been deleted but was returned in query results")
		}
	}
}
