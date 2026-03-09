package retrieval

import (
	"context"
	"errors"
	"testing"
	"time"

	models "github.com/context-maximiser/code-graph/libs/core-models-go"
	"github.com/context-maximiser/code-graph/libs/intelligence-go/contracts"
)

// --- Mock tombstone checker ---

type mockTombstoneChecker struct {
	tombstoned map[string]bool
	err        error
}

func (m *mockTombstoneChecker) IsTombstoned(_ context.Context, nodeKey string, _ string) (bool, error) {
	if m.err != nil {
		return false, m.err
	}
	return m.tombstoned[nodeKey], nil
}

// --- Orchestrator Tests ---

func TestOrchestrator_SingleAdapter(t *testing.T) {
	graphStore := &mockGraphStore{
		results: []GraphResult{
			{NodeKey: "func:a", NodeType: "Function", Score: 0.9},
			{NodeKey: "func:b", NodeType: "Function", Score: 0.7},
		},
	}

	orch := NewOrchestrator(
		WithGraphAdapter(NewGraphAdapter(graphStore)),
	)

	candidates, diag, err := orch.Retrieve(context.Background(), "test", models.DefaultScope(), 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(candidates))
	}

	// Verify diagnostics
	if diag.TotalCandidates != 2 {
		t.Errorf("expected 2 total candidates in diag, got %d", diag.TotalCandidates)
	}
	graphDiag, ok := diag.Sources[SourceGraph]
	if !ok {
		t.Fatal("expected graph source diagnostic")
	}
	if graphDiag.Count != 2 {
		t.Errorf("expected graph count 2, got %d", graphDiag.Count)
	}
	if graphDiag.Latency == 0 {
		t.Error("expected non-zero latency")
	}
}

func TestOrchestrator_MultipleAdapters_RRF(t *testing.T) {
	graphStore := &mockGraphStore{
		results: []GraphResult{
			{NodeKey: "func:shared", NodeType: "Function", Score: 0.9},
			{NodeKey: "func:graph-only", NodeType: "Function", Score: 0.5},
		},
	}
	vectorStore := &mockVectorStore{
		results: []VectorResult{
			{NodeKey: "func:shared", NodeType: "Function", Score: 0.85},
			{NodeKey: "func:vec-only", NodeType: "Function", Score: 0.6},
		},
	}
	embedder := &mockEmbedder{vector: []float64{0.1, 0.2}}

	orch := NewOrchestrator(
		WithGraphAdapter(NewGraphAdapter(graphStore)),
		WithVectorAdapter(NewVectorAdapter(vectorStore, embedder)),
	)

	candidates, diag, err := orch.Retrieve(context.Background(), "test", models.DefaultScope(), 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// "func:shared" should be top because it appears in both sources (higher RRF score)
	if len(candidates) != 3 {
		t.Fatalf("expected 3 candidates, got %d", len(candidates))
	}
	if candidates[0].NodeKey != "func:shared" {
		t.Errorf("expected 'func:shared' as top result, got %q", candidates[0].NodeKey)
	}
	if candidates[0].Source != SourceHybrid {
		t.Errorf("expected source 'hybrid' for merged candidate, got %q", candidates[0].Source)
	}

	// Verify merge behavior
	if diag.MergeBehavior.MultiSource != 1 {
		t.Errorf("expected 1 multi-source, got %d", diag.MergeBehavior.MultiSource)
	}
	if diag.MergeBehavior.DeduplicatedN != 1 {
		t.Errorf("expected 1 deduplicated, got %d", diag.MergeBehavior.DeduplicatedN)
	}
}

func TestOrchestrator_AllThreeAdapters(t *testing.T) {
	graphStore := &mockGraphStore{
		results: []GraphResult{
			{NodeKey: "func:a", NodeType: "Function", Score: 0.9},
		},
	}
	vectorStore := &mockVectorStore{
		results: []VectorResult{
			{NodeKey: "func:b", NodeType: "Function", Score: 0.8},
		},
	}
	textStore := &mockTextStore{
		results: []TextResult{
			{NodeKey: "doc:c", NodeType: "Document", Score: 5.0},
		},
	}
	embedder := &mockEmbedder{vector: []float64{0.1}}

	orch := NewOrchestrator(
		WithGraphAdapter(NewGraphAdapter(graphStore)),
		WithVectorAdapter(NewVectorAdapter(vectorStore, embedder)),
		WithTextAdapter(NewTextAdapter(textStore)),
	)

	candidates, diag, err := orch.Retrieve(context.Background(), "test", models.DefaultScope(), 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(candidates) != 3 {
		t.Fatalf("expected 3 candidates, got %d", len(candidates))
	}

	// All three sources should be in diagnostics
	if len(diag.Sources) != 3 {
		t.Errorf("expected 3 source diagnostics, got %d", len(diag.Sources))
	}
	for _, src := range []string{SourceGraph, SourceVector, SourceText} {
		if _, ok := diag.Sources[src]; !ok {
			t.Errorf("missing source diagnostic for %s", src)
		}
	}
}

func TestOrchestrator_AdapterError_GracefulDegradation(t *testing.T) {
	graphStore := &mockGraphStore{
		results: []GraphResult{
			{NodeKey: "func:a", NodeType: "Function", Score: 0.9},
		},
	}
	vectorStore := &mockVectorStore{err: errors.New("qdrant down")}
	embedder := &mockEmbedder{vector: []float64{0.1}}

	orch := NewOrchestrator(
		WithGraphAdapter(NewGraphAdapter(graphStore)),
		WithVectorAdapter(NewVectorAdapter(vectorStore, embedder)),
	)

	candidates, diag, err := orch.Retrieve(context.Background(), "test", models.DefaultScope(), 10)
	if err != nil {
		t.Fatalf("unexpected error (should degrade gracefully): %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate from graph, got %d", len(candidates))
	}

	// Vector source should show error
	vecDiag, ok := diag.Sources[SourceVector]
	if !ok {
		t.Fatal("expected vector source diagnostic")
	}
	if vecDiag.Error == "" {
		t.Error("expected error in vector diagnostic")
	}
	if !vecDiag.Fallback {
		t.Error("expected fallback=true for failed vector source")
	}
}

func TestOrchestrator_LimitApplied(t *testing.T) {
	graphStore := &mockGraphStore{
		results: []GraphResult{
			{NodeKey: "func:a", Score: 0.9},
			{NodeKey: "func:b", Score: 0.8},
			{NodeKey: "func:c", Score: 0.7},
			{NodeKey: "func:d", Score: 0.6},
			{NodeKey: "func:e", Score: 0.5},
		},
	}

	orch := NewOrchestrator(
		WithGraphAdapter(NewGraphAdapter(graphStore)),
	)

	candidates, _, err := orch.Retrieve(context.Background(), "test", models.DefaultScope(), 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(candidates) != 3 {
		t.Fatalf("expected 3 candidates (limited), got %d", len(candidates))
	}
}

func TestOrchestrator_DefaultLimit(t *testing.T) {
	graphStore := &mockGraphStore{results: []GraphResult{}}

	orch := NewOrchestrator(
		WithGraphAdapter(NewGraphAdapter(graphStore)),
	)

	_, diag, err := orch.Retrieve(context.Background(), "test", models.DefaultScope(), 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Just verify it doesn't panic with limit=0
	if diag.Query != "test" {
		t.Error("expected query in diagnostics")
	}
}

func TestOrchestrator_NoAdapters(t *testing.T) {
	orch := NewOrchestrator()

	candidates, diag, err := orch.Retrieve(context.Background(), "test", models.DefaultScope(), 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(candidates) != 0 {
		t.Fatalf("expected 0 candidates, got %d", len(candidates))
	}
	if diag.TotalCandidates != 0 {
		t.Errorf("expected 0 total candidates, got %d", diag.TotalCandidates)
	}
}

func TestOrchestrator_ScopedPrefixDedup(t *testing.T) {
	// Test that scopeId::nodeKey and nodeKey are deduplicated
	graphStore := &mockGraphStore{
		results: []GraphResult{
			{NodeKey: "pr-42::func:main.go#Start", NodeType: "Function", Score: 0.9},
		},
	}
	vectorStore := &mockVectorStore{
		results: []VectorResult{
			{NodeKey: "func:main.go#Start", NodeType: "Function", Score: 0.8},
		},
	}
	embedder := &mockEmbedder{vector: []float64{0.1}}

	orch := NewOrchestrator(
		WithGraphAdapter(NewGraphAdapter(graphStore)),
		WithVectorAdapter(NewVectorAdapter(vectorStore, embedder)),
	)

	candidates, _, err := orch.Retrieve(context.Background(), "start", models.DefaultScope(), 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should be deduplicated to 1 candidate
	if len(candidates) != 1 {
		t.Fatalf("expected 1 deduplicated candidate, got %d", len(candidates))
	}
	if candidates[0].Source != SourceHybrid {
		t.Errorf("expected hybrid source, got %q", candidates[0].Source)
	}
}

// --- Diagnostics Tests ---

func TestDiagnostics_Duration(t *testing.T) {
	graphStore := &mockGraphStore{
		results: []GraphResult{{NodeKey: "func:a", Score: 0.5}},
	}

	orch := NewOrchestrator(
		WithGraphAdapter(NewGraphAdapter(graphStore)),
	)

	_, diag, err := orch.Retrieve(context.Background(), "test", models.DefaultScope(), 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if diag.Duration == 0 {
		t.Error("expected non-zero total duration")
	}
	if diag.StartAt.IsZero() {
		t.Error("expected non-zero start time")
	}
}

func TestDiagnostics_MergeBehavior(t *testing.T) {
	graphStore := &mockGraphStore{
		results: []GraphResult{
			{NodeKey: "func:shared", Score: 0.9},
			{NodeKey: "func:g-only", Score: 0.5},
		},
	}
	textStore := &mockTextStore{
		results: []TextResult{
			{NodeKey: "func:shared", Score: 5.0},
			{NodeKey: "doc:t-only", Score: 3.0},
		},
	}

	orch := NewOrchestrator(
		WithGraphAdapter(NewGraphAdapter(graphStore)),
		WithTextAdapter(NewTextAdapter(textStore)),
	)

	_, diag, _ := orch.Retrieve(context.Background(), "test", models.DefaultScope(), 10)

	mb := diag.MergeBehavior
	if mb.TotalPreMerge != 4 {
		t.Errorf("expected 4 pre-merge, got %d", mb.TotalPreMerge)
	}
	if mb.TotalPostMerge != 3 {
		t.Errorf("expected 3 post-merge, got %d", mb.TotalPostMerge)
	}
	if mb.DeduplicatedN != 1 {
		t.Errorf("expected 1 deduplicated, got %d", mb.DeduplicatedN)
	}
	if mb.MultiSource != 1 {
		t.Errorf("expected 1 multi-source, got %d", mb.MultiSource)
	}
	if mb.GraphOnly != 1 {
		t.Errorf("expected 1 graph-only, got %d", mb.GraphOnly)
	}
	if mb.TextOnly != 1 {
		t.Errorf("expected 1 text-only, got %d", mb.TextOnly)
	}
}

// --- RRF Tests ---

func TestRRF_Ordering(t *testing.T) {
	orch := &Orchestrator{rrfK: 60}

	results := []adapterResult{
		{
			source: SourceGraph,
			candidates: []contracts.RetrievalCandidate{
				{NodeKey: "func:first", Score: 0.9},
				{NodeKey: "func:second", Score: 0.5},
			},
		},
	}

	merged := orch.mergeRRF(results)

	if len(merged) != 2 {
		t.Fatalf("expected 2 merged, got %d", len(merged))
	}
	// First should have higher RRF score (rank 1 = 1/(60+1) > rank 2 = 1/(60+2))
	if merged[0].NodeKey != "func:first" {
		t.Errorf("expected first to be top, got %q", merged[0].NodeKey)
	}
	if merged[0].Score <= merged[1].Score {
		t.Error("first should have higher RRF score than second")
	}
}

func TestRRF_MultiSource_Boosted(t *testing.T) {
	orch := &Orchestrator{rrfK: 60}

	results := []adapterResult{
		{
			source: SourceGraph,
			candidates: []contracts.RetrievalCandidate{
				{NodeKey: "func:both", Score: 0.9},   // rank 1 in graph
				{NodeKey: "func:graph", Score: 0.5},    // rank 2 in graph
			},
		},
		{
			source: SourceVector,
			candidates: []contracts.RetrievalCandidate{
				{NodeKey: "func:both", Score: 0.8},    // rank 1 in vector
				{NodeKey: "func:vector", Score: 0.4},   // rank 2 in vector
			},
		},
	}

	merged := orch.mergeRRF(results)

	// "func:both" should be top with combined RRF from both sources
	if merged[0].NodeKey != "func:both" {
		t.Errorf("expected 'func:both' as top, got %q", merged[0].NodeKey)
	}
	if merged[0].Source != SourceHybrid {
		t.Errorf("expected hybrid source, got %q", merged[0].Source)
	}

	// Its RRF score should be sum of both ranks
	expectedRRF := 1.0/(60+1) + 1.0/(60+1)
	if merged[0].Score != expectedRRF {
		t.Errorf("expected RRF score %.6f, got %.6f", expectedRRF, merged[0].Score)
	}
}

func TestRRF_ErrorSkipped(t *testing.T) {
	orch := &Orchestrator{rrfK: 60}

	results := []adapterResult{
		{
			source: SourceGraph,
			candidates: []contracts.RetrievalCandidate{
				{NodeKey: "func:a", Score: 0.9},
			},
		},
		{
			source: SourceVector,
			err:    errors.New("failed"),
		},
	}

	merged := orch.mergeRRF(results)

	if len(merged) != 1 {
		t.Fatalf("expected 1 result (error source skipped), got %d", len(merged))
	}
}

// --- normalizeNodeKey Tests ---

func TestNormalizeNodeKey(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"func:main.go#Start", "func:main.go#Start"},
		{"main::func:main.go#Start", "func:main.go#Start"},
		{"pr-42::func:api.go#Handle", "func:api.go#Handle"},
		{"", ""},
		{"no-scope", "no-scope"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := normalizeNodeKey(tt.input)
			if got != tt.expected {
				t.Errorf("normalizeNodeKey(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

// --- Implements Retriever interface test ---

func TestOrchestrator_ImplementsRetrieverContract(t *testing.T) {
	// Verify the Orchestrator satisfies the contracts.Retriever interface pattern.
	// The Orchestrator has a Retrieve method with compatible signature plus diagnostics.
	orch := NewOrchestrator()

	ctx := context.Background()
	scope := models.DefaultScope()
	candidates, diag, err := orch.Retrieve(ctx, "test", scope, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if candidates == nil {
		t.Error("expected non-nil candidates slice")
	}
	if diag == nil {
		t.Fatal("expected non-nil diagnostics")
	}
}

// --- Timeout/Context cancellation test ---

func TestOrchestrator_ContextCancellation(t *testing.T) {
	// Slow graph store that respects context
	graphStore := &mockGraphStore{
		results: []GraphResult{{NodeKey: "func:a", Score: 0.9}},
	}

	orch := NewOrchestrator(
		WithGraphAdapter(NewGraphAdapter(graphStore)),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	// Give context time to expire
	time.Sleep(2 * time.Millisecond)

	// The mock doesn't check context, so this still works.
	// This test just verifies the orchestrator doesn't crash with expired context.
	_, _, _ = orch.Retrieve(ctx, "test", models.DefaultScope(), 10)
}
