package search

import (
	"context"
	"math"
	"sort"
	"testing"
)

// FakeVectorStore is an in-memory VectorStore for testing.
type FakeVectorStore struct {
	vectors map[string]VectorUpsert
	indexes map[string]bool
}

func NewFakeVectorStore() *FakeVectorStore {
	return &FakeVectorStore{
		vectors: make(map[string]VectorUpsert),
		indexes: make(map[string]bool),
	}
}

func (f *FakeVectorStore) UpsertVectors(ctx context.Context, vectors []VectorUpsert) error {
	for _, v := range vectors {
		f.vectors[v.ID] = v
	}
	return nil
}

func (f *FakeVectorStore) Query(ctx context.Context, q VectorQuery) ([]VectorResult, error) {
	type scored struct {
		id    string
		score float64
		meta  map[string]any
	}

	var results []scored
	for id, v := range f.vectors {
		score := cosineSimilarity(q.Vector, v.Vector)
		results = append(results, scored{id: id, score: score, meta: v.Metadata})
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].score > results[j].score
	})

	limit := q.Limit
	if limit <= 0 {
		limit = 10
	}
	if len(results) > limit {
		results = results[:limit]
	}

	var vr []VectorResult
	for _, r := range results {
		vr = append(vr, VectorResult{ID: r.id, Score: r.score, Metadata: r.meta})
	}
	return vr, nil
}

func (f *FakeVectorStore) DeleteVectors(ctx context.Context, ids []string) error {
	for _, id := range ids {
		delete(f.vectors, id)
	}
	return nil
}

func (f *FakeVectorStore) CreateIndex(ctx context.Context, name string, dimensions int, similarity string) error {
	f.indexes[name] = true
	return nil
}

func cosineSimilarity(a, b []float64) float64 {
	if len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

// Compile-time check.
var _ VectorStore = (*FakeVectorStore)(nil)

func TestFakeVectorStore_UpsertAndQuery(t *testing.T) {
	store := NewFakeVectorStore()
	ctx := context.Background()

	// Upsert vectors.
	err := store.UpsertVectors(ctx, []VectorUpsert{
		{ID: "a", Vector: []float64{1, 0, 0}, Metadata: map[string]any{"label": "Function"}},
		{ID: "b", Vector: []float64{0, 1, 0}, Metadata: map[string]any{"label": "Class"}},
		{ID: "c", Vector: []float64{0.9, 0.1, 0}, Metadata: map[string]any{"label": "Function"}},
	})
	if err != nil {
		t.Fatalf("UpsertVectors failed: %v", err)
	}

	// Query for vector closest to [1, 0, 0].
	results, err := store.Query(ctx, VectorQuery{Vector: []float64{1, 0, 0}, Limit: 2})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// "a" should be the best match (exact).
	if results[0].ID != "a" {
		t.Errorf("expected first result to be 'a', got %s", results[0].ID)
	}
	if results[0].Score < 0.99 {
		t.Errorf("expected high score for exact match, got %f", results[0].Score)
	}

	// "c" should be second (close to [1,0,0]).
	if results[1].ID != "c" {
		t.Errorf("expected second result to be 'c', got %s", results[1].ID)
	}
}

func TestFakeVectorStore_Delete(t *testing.T) {
	store := NewFakeVectorStore()
	ctx := context.Background()

	store.UpsertVectors(ctx, []VectorUpsert{
		{ID: "x", Vector: []float64{1, 0}},
		{ID: "y", Vector: []float64{0, 1}},
	})

	err := store.DeleteVectors(ctx, []string{"x"})
	if err != nil {
		t.Fatalf("DeleteVectors failed: %v", err)
	}

	results, _ := store.Query(ctx, VectorQuery{Vector: []float64{1, 0}, Limit: 10})
	if len(results) != 1 {
		t.Fatalf("expected 1 result after delete, got %d", len(results))
	}
	if results[0].ID != "y" {
		t.Errorf("expected remaining result to be 'y', got %s", results[0].ID)
	}
}

func TestFakeVectorStore_CreateIndex(t *testing.T) {
	store := NewFakeVectorStore()
	ctx := context.Background()

	err := store.CreateIndex(ctx, "test_index", 768, "cosine")
	if err != nil {
		t.Fatalf("CreateIndex failed: %v", err)
	}
	if !store.indexes["test_index"] {
		t.Error("expected index to be created")
	}
}

func TestCosineSimilarity(t *testing.T) {
	// Identical vectors → 1.0
	s := cosineSimilarity([]float64{1, 0, 0}, []float64{1, 0, 0})
	if math.Abs(s-1.0) > 0.001 {
		t.Errorf("expected 1.0, got %f", s)
	}

	// Orthogonal vectors → 0.0
	s = cosineSimilarity([]float64{1, 0, 0}, []float64{0, 1, 0})
	if math.Abs(s) > 0.001 {
		t.Errorf("expected 0.0, got %f", s)
	}

	// Different lengths → 0
	s = cosineSimilarity([]float64{1, 0}, []float64{1, 0, 0})
	if s != 0 {
		t.Errorf("expected 0 for mismatched lengths, got %f", s)
	}
}
