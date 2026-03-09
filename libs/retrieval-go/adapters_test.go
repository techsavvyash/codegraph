package retrieval

import (
	"context"
	"errors"
	"testing"

	models "github.com/context-maximiser/code-graph/libs/core-models-go"
)

// --- Mock implementations ---

type mockGraphStore struct {
	results []GraphResult
	err     error
	calls   int
}

func (m *mockGraphStore) SearchNodes(_ context.Context, _ string, _ models.ScopeContext, _ int) ([]GraphResult, error) {
	m.calls++
	return m.results, m.err
}

type mockVectorStore struct {
	results []VectorResult
	err     error
	calls   int
	lastFilters map[string]any
}

func (m *mockVectorStore) Query(_ context.Context, _ []float64, _ int, filters map[string]any) ([]VectorResult, error) {
	m.calls++
	m.lastFilters = filters
	return m.results, m.err
}

type mockEmbedder struct {
	vector []float64
	err    error
	calls  int
}

func (m *mockEmbedder) Embed(_ context.Context, _ string) ([]float64, error) {
	m.calls++
	return m.vector, m.err
}

type mockTextStore struct {
	results []TextResult
	err     error
	calls   int
}

func (m *mockTextStore) Search(_ context.Context, _ string, _ models.ScopeContext, _ int) ([]TextResult, error) {
	m.calls++
	return m.results, m.err
}

// --- Graph Adapter Tests ---

func TestGraphAdapter_Retrieve(t *testing.T) {
	store := &mockGraphStore{
		results: []GraphResult{
			{NodeKey: "func:main.go#Start", NodeType: "Function", Score: 0.9, Props: map[string]any{"name": "Start"}},
			{NodeKey: "func:main.go#Stop", NodeType: "Function", Score: 0.7, Props: map[string]any{"name": "Stop"}},
		},
	}
	adapter := NewGraphAdapter(store)
	scope := models.DefaultScope()

	candidates, err := adapter.Retrieve(context.Background(), "start function", scope, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(candidates))
	}

	// Verify normalization
	c := candidates[0]
	if c.NodeKey != "func:main.go#Start" {
		t.Errorf("expected nodeKey 'func:main.go#Start', got %q", c.NodeKey)
	}
	if c.Source != SourceGraph {
		t.Errorf("expected source %q, got %q", SourceGraph, c.Source)
	}
	if c.Scope != "main" {
		t.Errorf("expected scope 'main', got %q", c.Scope)
	}
	if c.ScopeID != "main" {
		t.Errorf("expected scopeID 'main', got %q", c.ScopeID)
	}
	if c.Score != 0.9 {
		t.Errorf("expected score 0.9, got %f", c.Score)
	}
}

func TestGraphAdapter_RetrieveError(t *testing.T) {
	store := &mockGraphStore{err: errors.New("db down")}
	adapter := NewGraphAdapter(store)

	_, err := adapter.Retrieve(context.Background(), "q", models.DefaultScope(), 10)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGraphAdapter_PRScope(t *testing.T) {
	store := &mockGraphStore{
		results: []GraphResult{
			{NodeKey: "func:api.go#Handle", NodeType: "Function", Score: 0.8},
		},
	}
	adapter := NewGraphAdapter(store)
	scope := models.NewPRScope("42")

	candidates, err := adapter.Retrieve(context.Background(), "handle", scope, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if candidates[0].Scope != "pr" {
		t.Errorf("expected scope 'pr', got %q", candidates[0].Scope)
	}
	if candidates[0].ScopeID != "pr-42" {
		t.Errorf("expected scopeID 'pr-42', got %q", candidates[0].ScopeID)
	}
}

// --- Vector Adapter Tests ---

func TestVectorAdapter_Retrieve(t *testing.T) {
	store := &mockVectorStore{
		results: []VectorResult{
			{ID: "v1", NodeKey: "func:main.go#Start", NodeType: "Function", Score: 0.95, Metadata: map[string]any{"name": "Start"}},
		},
	}
	embedder := &mockEmbedder{vector: []float64{0.1, 0.2, 0.3}}
	adapter := NewVectorAdapter(store, embedder)

	candidates, err := adapter.Retrieve(context.Background(), "start", models.DefaultScope(), 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}
	if candidates[0].Source != SourceVector {
		t.Errorf("expected source %q, got %q", SourceVector, candidates[0].Source)
	}
	if embedder.calls != 1 {
		t.Errorf("expected 1 embed call, got %d", embedder.calls)
	}
}

func TestVectorAdapter_EmbedError(t *testing.T) {
	store := &mockVectorStore{}
	embedder := &mockEmbedder{err: errors.New("embed fail")}
	adapter := NewVectorAdapter(store, embedder)

	_, err := adapter.Retrieve(context.Background(), "q", models.DefaultScope(), 10)
	if err == nil {
		t.Fatal("expected error from embed failure")
	}
}

func TestVectorAdapter_ScopeFilters_Main(t *testing.T) {
	store := &mockVectorStore{results: []VectorResult{}}
	embedder := &mockEmbedder{vector: []float64{0.1}}
	adapter := NewVectorAdapter(store, embedder)

	_, _ = adapter.Retrieve(context.Background(), "q", models.DefaultScope(), 10)

	// Main scope should only filter for "main"
	scopeFilter, ok := store.lastFilters["scopeId"].([]string)
	if !ok {
		t.Fatal("expected scopeId filter to be []string")
	}
	if len(scopeFilter) != 1 || scopeFilter[0] != "main" {
		t.Errorf("expected [main], got %v", scopeFilter)
	}
}

func TestVectorAdapter_ScopeFilters_PR(t *testing.T) {
	store := &mockVectorStore{results: []VectorResult{}}
	embedder := &mockEmbedder{vector: []float64{0.1}}
	adapter := NewVectorAdapter(store, embedder)

	_, _ = adapter.Retrieve(context.Background(), "q", models.NewPRScope("42"), 10)

	// PR scope should include both PR and main
	scopeFilter, ok := store.lastFilters["scopeId"].([]string)
	if !ok {
		t.Fatal("expected scopeId filter to be []string")
	}
	if len(scopeFilter) != 2 {
		t.Fatalf("expected 2 scope filters, got %d: %v", len(scopeFilter), scopeFilter)
	}
	if scopeFilter[0] != "pr-42" || scopeFilter[1] != "main" {
		t.Errorf("expected [pr-42, main], got %v", scopeFilter)
	}
}

// --- Text Adapter Tests ---

func TestTextAdapter_Retrieve(t *testing.T) {
	store := &mockTextStore{
		results: []TextResult{
			{NodeKey: "doc:readme.md", NodeType: "Document", Score: 5.2, Snippet: "Getting started..."},
		},
	}
	adapter := NewTextAdapter(store)

	candidates, err := adapter.Retrieve(context.Background(), "getting started", models.DefaultScope(), 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}
	if candidates[0].Source != SourceText {
		t.Errorf("expected source %q, got %q", SourceText, candidates[0].Source)
	}
	snippet, ok := candidates[0].Metadata["snippet"].(string)
	if !ok || snippet != "Getting started..." {
		t.Errorf("expected snippet in metadata, got %v", candidates[0].Metadata)
	}
}

func TestTextAdapter_RetrieveError(t *testing.T) {
	store := &mockTextStore{err: errors.New("index unavailable")}
	adapter := NewTextAdapter(store)

	_, err := adapter.Retrieve(context.Background(), "q", models.DefaultScope(), 10)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestTextAdapter_NilMetadata(t *testing.T) {
	store := &mockTextStore{
		results: []TextResult{
			{NodeKey: "doc:test", Score: 1.0},
		},
	}
	adapter := NewTextAdapter(store)

	candidates, err := adapter.Retrieve(context.Background(), "q", models.DefaultScope(), 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if candidates[0].Metadata == nil {
		t.Error("expected non-nil metadata even when source has nil")
	}
}

// --- buildScopeFilters Tests ---

func TestBuildScopeFilters(t *testing.T) {
	tests := []struct {
		name     string
		scope    models.ScopeContext
		expected []string
	}{
		{"main scope", models.DefaultScope(), []string{"main"}},
		{"empty scope", models.ScopeContext{}, []string{"main"}},
		{"PR scope", models.NewPRScope("42"), []string{"pr-42", "main"}},
		{"PR scope 99", models.NewPRScope("99"), []string{"pr-99", "main"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filters := buildScopeFilters(tt.scope)
			scopeFilter, ok := filters["scopeId"].([]string)
			if !ok {
				t.Fatal("expected scopeId filter")
			}
			if len(scopeFilter) != len(tt.expected) {
				t.Fatalf("expected %d filters, got %d", len(tt.expected), len(scopeFilter))
			}
			for i, exp := range tt.expected {
				if scopeFilter[i] != exp {
					t.Errorf("filter[%d]: expected %q, got %q", i, exp, scopeFilter[i])
				}
			}
		})
	}
}
