package bundles

import (
	"context"
	"testing"

	models "github.com/context-maximiser/code-graph/libs/core-models-go"
	"github.com/context-maximiser/code-graph/libs/intelligence-go/contracts"
)

// --- Mock implementations ---

type mockExpander struct {
	results map[string][]ExpansionNode
	err     error
}

func (m *mockExpander) Expand(_ context.Context, anchorKey string, _ int, maxPerAnchor int, _ models.ScopeContext) ([]ExpansionNode, error) {
	if m.err != nil {
		return nil, m.err
	}
	nodes := m.results[anchorKey]
	if len(nodes) > maxPerAnchor {
		nodes = nodes[:maxPerAnchor]
	}
	return nodes, nil
}

type mockInferenceProvider struct {
	results []contracts.InferenceResult
	err     error
}

func (m *mockInferenceProvider) InferForAnchors(_ context.Context, _ []contracts.RetrievalCandidate, _ models.ScopeContext) ([]contracts.InferenceResult, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.results, nil
}

type fixedTokenEstimator struct {
	tokensPerNode int
}

func (f *fixedTokenEstimator) EstimateTokens(_ contracts.RetrievalCandidate) int {
	return f.tokensPerNode
}

// --- Builder Tests ---

func TestBuilder_Build_Empty(t *testing.T) {
	builder := NewBuilder()
	bundle, err := builder.Build(context.Background(), nil, "flow_summary", models.DefaultScope())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bundle == nil {
		t.Fatal("expected non-nil bundle")
	}
	if bundle.Template != "flow_summary" {
		t.Errorf("expected template flow_summary, got %s", bundle.Template)
	}
	if bundle.Scope != "main" {
		t.Errorf("expected scope main, got %s", bundle.Scope)
	}
	if len(bundle.Anchors) != 0 {
		t.Errorf("expected 0 anchors, got %d", len(bundle.Anchors))
	}
}

func TestBuilder_Build_AnchorsOnly(t *testing.T) {
	builder := NewBuilder()
	anchors := []contracts.RetrievalCandidate{
		{NodeKey: "func:b", Score: 0.8, Source: "vector"},
		{NodeKey: "func:a", Score: 0.9, Source: "vector"},
	}

	bundle, err := builder.Build(context.Background(), anchors, "flow_summary", models.DefaultScope())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(bundle.Anchors) != 2 {
		t.Fatalf("expected 2 anchors, got %d", len(bundle.Anchors))
	}

	// Verify deterministic ordering by nodeKey
	if bundle.Anchors[0].NodeKey != "func:a" {
		t.Errorf("expected first anchor func:a, got %s", bundle.Anchors[0].NodeKey)
	}
	if bundle.Anchors[1].NodeKey != "func:b" {
		t.Errorf("expected second anchor func:b, got %s", bundle.Anchors[1].NodeKey)
	}
}

func TestBuilder_Build_AnchorTruncation(t *testing.T) {
	builder := NewBuilder().WithBudget(ExpansionBudget{
		MaxTotalAnchors: 2,
		MaxBundleTokens: 100000,
	})

	anchors := []contracts.RetrievalCandidate{
		{NodeKey: "func:a", Score: 0.5},
		{NodeKey: "func:b", Score: 0.9},
		{NodeKey: "func:c", Score: 0.7},
		{NodeKey: "func:d", Score: 0.3},
	}

	bundle, err := builder.Build(context.Background(), anchors, "test", models.DefaultScope())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(bundle.Anchors) != 2 {
		t.Fatalf("expected 2 anchors after truncation, got %d", len(bundle.Anchors))
	}

	// Should keep top-scored: func:b (0.9) and func:c (0.7)
	keys := map[string]bool{}
	for _, a := range bundle.Anchors {
		keys[a.NodeKey] = true
	}
	if !keys["func:b"] || !keys["func:c"] {
		t.Errorf("expected func:b and func:c as top anchors, got %v", bundle.Anchors)
	}
}

func TestBuilder_Build_WithExpansions(t *testing.T) {
	expander := &mockExpander{
		results: map[string][]ExpansionNode{
			"func:a": {
				{NodeKey: "func:x", NodeType: "Function", Name: "X", RelationType: "CALLS", Depth: 1, AnchorKey: "func:a", Score: 0.6},
				{NodeKey: "func:y", NodeType: "Function", Name: "Y", RelationType: "CALLS", Depth: 1, AnchorKey: "func:a", Score: 0.5},
			},
			"func:b": {
				{NodeKey: "func:z", NodeType: "Function", Name: "Z", RelationType: "CALLS", Depth: 1, AnchorKey: "func:b", Score: 0.4},
			},
		},
	}

	builder := NewBuilder().WithExpander(expander)
	anchors := []contracts.RetrievalCandidate{
		{NodeKey: "func:a", Score: 0.9, Source: "vector"},
		{NodeKey: "func:b", Score: 0.8, Source: "vector"},
	}

	bundle, err := builder.Build(context.Background(), anchors, "flow_summary", models.DefaultScope())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(bundle.Expansions) != 3 {
		t.Fatalf("expected 3 expansions, got %d", len(bundle.Expansions))
	}

	// Verify deterministic ordering
	if bundle.Expansions[0].NodeKey != "func:x" {
		t.Errorf("expected first expansion func:x, got %s", bundle.Expansions[0].NodeKey)
	}

	// Verify expansion metadata
	for _, exp := range bundle.Expansions {
		if exp.Source != "expansion" {
			t.Errorf("expected source 'expansion', got %s", exp.Source)
		}
		if exp.Metadata["anchorKey"] == nil {
			t.Error("expected anchorKey in metadata")
		}
	}
}

func TestBuilder_Build_ExpansionDedup(t *testing.T) {
	expander := &mockExpander{
		results: map[string][]ExpansionNode{
			"func:a": {
				{NodeKey: "func:shared", NodeType: "Function", RelationType: "CALLS", AnchorKey: "func:a", Score: 0.6},
			},
			"func:b": {
				{NodeKey: "func:shared", NodeType: "Function", RelationType: "CALLS", AnchorKey: "func:b", Score: 0.5},
			},
		},
	}

	builder := NewBuilder().WithExpander(expander)
	anchors := []contracts.RetrievalCandidate{
		{NodeKey: "func:a", Score: 0.9},
		{NodeKey: "func:b", Score: 0.8},
	}

	bundle, err := builder.Build(context.Background(), anchors, "test", models.DefaultScope())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// shared should appear only once
	if len(bundle.Expansions) != 1 {
		t.Errorf("expected 1 expansion (deduped), got %d", len(bundle.Expansions))
	}
}

func TestBuilder_Build_ExpansionBudgetLimit(t *testing.T) {
	expander := &mockExpander{
		results: map[string][]ExpansionNode{
			"func:a": {
				{NodeKey: "func:1", NodeType: "Function", RelationType: "CALLS", AnchorKey: "func:a"},
				{NodeKey: "func:2", NodeType: "Function", RelationType: "CALLS", AnchorKey: "func:a"},
				{NodeKey: "func:3", NodeType: "Function", RelationType: "CALLS", AnchorKey: "func:a"},
			},
		},
	}

	builder := NewBuilder().WithExpander(expander).WithBudget(ExpansionBudget{
		MaxTotalExpansions:     2,
		MaxExpansionsPerAnchor: 10,
		MaxExpansionDepth:      2,
		MaxBundleTokens:        100000,
	})

	anchors := []contracts.RetrievalCandidate{
		{NodeKey: "func:a", Score: 0.9},
	}

	bundle, err := builder.Build(context.Background(), anchors, "test", models.DefaultScope())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(bundle.Expansions) != 2 {
		t.Errorf("expected 2 expansions (budget limit), got %d", len(bundle.Expansions))
	}
}

func TestBuilder_Build_NodeTypeFilter(t *testing.T) {
	expander := &mockExpander{
		results: map[string][]ExpansionNode{
			"func:a": {
				{NodeKey: "func:ok", NodeType: "Function", RelationType: "CALLS", AnchorKey: "func:a"},
				{NodeKey: "var:blocked", NodeType: "Variable", RelationType: "CONTAINS", AnchorKey: "func:a"},
			},
		},
	}

	builder := NewBuilder().WithExpander(expander).WithBudget(ExpansionBudget{
		MaxTotalExpansions:     10,
		MaxExpansionsPerAnchor: 10,
		MaxExpansionDepth:      2,
		MaxBundleTokens:        100000,
		AllowedExpansionTypes:  []string{"Function"},
		AllowedRelationTypes:   []string{"CALLS"},
	})

	anchors := []contracts.RetrievalCandidate{
		{NodeKey: "func:a", Score: 0.9},
	}

	bundle, err := builder.Build(context.Background(), anchors, "test", models.DefaultScope())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(bundle.Expansions) != 1 {
		t.Errorf("expected 1 expansion (type filtered), got %d", len(bundle.Expansions))
	}
	if bundle.Expansions[0].NodeKey != "func:ok" {
		t.Errorf("expected func:ok, got %s", bundle.Expansions[0].NodeKey)
	}
}

func TestBuilder_Build_TokenBudget(t *testing.T) {
	builder := NewBuilder().
		WithTokenEstimator(&fixedTokenEstimator{tokensPerNode: 100}).
		WithBudget(ExpansionBudget{
			MaxBundleTokens: 250, // Room for 2.5 nodes → only 2 anchors
			MaxTotalAnchors: 10,
		})

	anchors := []contracts.RetrievalCandidate{
		{NodeKey: "func:a", Score: 0.9},
		{NodeKey: "func:b", Score: 0.8},
		{NodeKey: "func:c", Score: 0.7},
		{NodeKey: "func:d", Score: 0.6},
	}

	bundle, err := builder.Build(context.Background(), anchors, "test", models.DefaultScope())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(bundle.Anchors) != 2 {
		t.Errorf("expected 2 anchors within token budget, got %d", len(bundle.Anchors))
	}
}

func TestBuilder_Build_WithInferences(t *testing.T) {
	provider := &mockInferenceProvider{
		results: []contracts.InferenceResult{
			{SourceKey: "func:b", TargetKey: "func:a", Confidence: 0.9},
			{SourceKey: "func:a", TargetKey: "func:b", Confidence: 0.8},
		},
	}

	builder := NewBuilder().WithInferenceProvider(provider)
	anchors := []contracts.RetrievalCandidate{
		{NodeKey: "func:a", Score: 0.9},
		{NodeKey: "func:b", Score: 0.8},
	}

	bundle, err := builder.Build(context.Background(), anchors, "test", models.DefaultScope())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(bundle.Inferences) != 2 {
		t.Fatalf("expected 2 inferences, got %d", len(bundle.Inferences))
	}

	// Verify deterministic ordering by sourceKey
	if bundle.Inferences[0].SourceKey != "func:a" {
		t.Errorf("expected first inference source func:a, got %s", bundle.Inferences[0].SourceKey)
	}
}

func TestBuilder_Build_GracefulInferenceError(t *testing.T) {
	provider := &mockInferenceProvider{
		err: context.DeadlineExceeded,
	}

	builder := NewBuilder().WithInferenceProvider(provider)
	anchors := []contracts.RetrievalCandidate{
		{NodeKey: "func:a", Score: 0.9},
	}

	bundle, err := builder.Build(context.Background(), anchors, "test", models.DefaultScope())
	if err != nil {
		t.Fatalf("expected graceful degradation, got error: %v", err)
	}
	if len(bundle.Inferences) != 0 {
		t.Errorf("expected 0 inferences on error, got %d", len(bundle.Inferences))
	}
}

func TestBuilder_Build_ScopeThreading(t *testing.T) {
	scope := models.NewPRScope("42")
	builder := NewBuilder()
	anchors := []contracts.RetrievalCandidate{
		{NodeKey: "func:a", Score: 0.9},
	}

	bundle, err := builder.Build(context.Background(), anchors, "pr_summary", scope)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if bundle.Scope != "pr" {
		t.Errorf("expected scope pr, got %s", bundle.Scope)
	}
	if bundle.ScopeID != "pr-42" {
		t.Errorf("expected scopeId pr-42, got %s", bundle.ScopeID)
	}
}

func TestBuilder_Build_DeterministicOrdering(t *testing.T) {
	builder := NewBuilder()
	anchors := []contracts.RetrievalCandidate{
		{NodeKey: "func:z", Score: 0.5},
		{NodeKey: "func:a", Score: 0.3},
		{NodeKey: "func:m", Score: 0.7},
	}

	// Build multiple times and verify same ordering
	var prevKeys []string
	for i := 0; i < 5; i++ {
		bundle, err := builder.Build(context.Background(), anchors, "test", models.DefaultScope())
		if err != nil {
			t.Fatalf("iteration %d: unexpected error: %v", i, err)
		}

		var keys []string
		for _, a := range bundle.Anchors {
			keys = append(keys, a.NodeKey)
		}

		if prevKeys != nil {
			for j, k := range keys {
				if k != prevKeys[j] {
					t.Errorf("non-deterministic ordering at iteration %d: %v vs %v", i, keys, prevKeys)
					break
				}
			}
		}
		prevKeys = keys
	}
}

func TestBuilder_ImplementsBundleBuilder(t *testing.T) {
	// Compile-time check that Builder implements contracts.BundleBuilder
	var _ contracts.BundleBuilder = (*Builder)(nil)
}
