package rollout

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// --- Mocks ---

type mockRunner struct {
	results  map[string]any
	errors   map[string]error
	duration time.Duration
}

func (m *mockRunner) Run(_ context.Context, _ string, strategyName string, _ any) (any, error) {
	if m.duration > 0 {
		time.Sleep(m.duration)
	}
	if err, ok := m.errors[strategyName]; ok {
		return nil, err
	}
	return m.results[strategyName], nil
}

type mockComparisonStore struct {
	comparisons []ComparisonResult
}

func (m *mockComparisonStore) SaveComparison(_ context.Context, result ComparisonResult) error {
	m.comparisons = append(m.comparisons, result)
	return nil
}

// --- Config Tests ---

func TestConfig_GetStageConfig(t *testing.T) {
	cfg := &Config{
		Stages: []StageConfig{
			{Stage: "inference", LiveStrategy: "v1", ShadowStrategy: "v2"},
			{Stage: "generation", LiveStrategy: "gpt4"},
		},
	}

	sc := cfg.GetStageConfig("inference")
	if sc == nil {
		t.Fatal("expected non-nil config")
	}
	if sc.ShadowStrategy != "v2" {
		t.Errorf("expected shadow v2, got %s", sc.ShadowStrategy)
	}

	missing := cfg.GetStageConfig("nonexistent")
	if missing != nil {
		t.Error("expected nil for missing stage")
	}
}

// --- ShadowExecutor Tests ---

func TestShadowExecutor_LiveOnly(t *testing.T) {
	runner := &mockRunner{
		results: map[string]any{"v1": "live-result"},
	}
	exec := NewShadowExecutor(runner)

	cfg := StageConfig{Stage: "inference", LiveStrategy: "v1"}
	out, err := exec.Execute(context.Background(), cfg, "input")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "live-result" {
		t.Errorf("expected 'live-result', got %v", out)
	}
}

func TestShadowExecutor_WithShadow(t *testing.T) {
	runner := &mockRunner{
		results: map[string]any{
			"v1": "live-result",
			"v2": "shadow-result",
		},
	}
	store := &mockComparisonStore{}
	exec := NewShadowExecutor(runner).WithComparisonStore(store)

	cfg := StageConfig{
		Stage:          "inference",
		LiveStrategy:   "v1",
		ShadowStrategy: "v2",
	}

	out, err := exec.Execute(context.Background(), cfg, "input")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should return live result
	if out != "live-result" {
		t.Errorf("expected 'live-result', got %v", out)
	}

	// Wait a moment for shadow goroutine
	time.Sleep(10 * time.Millisecond)

	// Should have recorded comparison
	comparisons := exec.GetComparisons()
	if len(comparisons) != 1 {
		t.Fatalf("expected 1 comparison, got %d", len(comparisons))
	}

	c := comparisons[0]
	if c.LiveOutput != "live-result" {
		t.Errorf("expected live output 'live-result', got %v", c.LiveOutput)
	}
	if c.ShadowOutput != "shadow-result" {
		t.Errorf("expected shadow output 'shadow-result', got %v", c.ShadowOutput)
	}
	if c.LiveError != "" {
		t.Errorf("expected no live error, got %s", c.LiveError)
	}

	// Should have persisted to store
	if len(store.comparisons) != 1 {
		t.Errorf("expected 1 persisted comparison, got %d", len(store.comparisons))
	}
}

func TestShadowExecutor_ShadowErrorDoesNotAffectLive(t *testing.T) {
	runner := &mockRunner{
		results: map[string]any{"v1": "live-result"},
		errors:  map[string]error{"v2": fmt.Errorf("shadow error")},
	}
	exec := NewShadowExecutor(runner)

	cfg := StageConfig{
		Stage:          "inference",
		LiveStrategy:   "v1",
		ShadowStrategy: "v2",
	}

	out, err := exec.Execute(context.Background(), cfg, "input")
	if err != nil {
		t.Fatalf("shadow error should not affect live: %v", err)
	}
	if out != "live-result" {
		t.Errorf("expected 'live-result', got %v", out)
	}

	time.Sleep(10 * time.Millisecond)
	comparisons := exec.GetComparisons()
	if len(comparisons) != 1 {
		t.Fatalf("expected 1 comparison, got %d", len(comparisons))
	}
	if comparisons[0].ShadowError == "" {
		t.Error("expected shadow error recorded")
	}
}

func TestShadowExecutor_LiveErrorPropagated(t *testing.T) {
	runner := &mockRunner{
		errors: map[string]error{
			"v1": fmt.Errorf("live error"),
		},
		results: map[string]any{"v2": "shadow-ok"},
	}
	exec := NewShadowExecutor(runner)

	cfg := StageConfig{
		Stage:          "inference",
		LiveStrategy:   "v1",
		ShadowStrategy: "v2",
	}

	_, err := exec.Execute(context.Background(), cfg, "input")
	if err == nil {
		t.Error("expected live error to propagate")
	}
}

// --- CanaryRouter Tests ---

func TestCanaryRouter_ZeroPercent(t *testing.T) {
	router := NewCanaryRouter()
	for i := 0; i < 100; i++ {
		if router.ShouldUseCanary(0) {
			t.Error("expected no canary at 0%")
		}
	}
}

func TestCanaryRouter_HundredPercent(t *testing.T) {
	router := NewCanaryRouter()
	canaryCount := 0
	for i := 0; i < 100; i++ {
		if router.ShouldUseCanary(1.0) {
			canaryCount++
		}
	}
	if canaryCount != 100 {
		t.Errorf("expected 100 canary at 100%%, got %d", canaryCount)
	}
}

func TestCanaryRouter_FiftyPercent(t *testing.T) {
	router := NewCanaryRouter()
	canaryCount := 0
	total := 200
	for i := 0; i < total; i++ {
		if router.ShouldUseCanary(0.5) {
			canaryCount++
		}
	}
	// Should be roughly 50%
	ratio := float64(canaryCount) / float64(total)
	if ratio < 0.4 || ratio > 0.6 {
		t.Errorf("expected ~50%% canary, got %.0f%%", ratio*100)
	}
}

func TestCanaryRouter_Deterministic(t *testing.T) {
	// Same sequence of calls should give same results
	router1 := NewCanaryRouter()
	router2 := NewCanaryRouter()

	for i := 0; i < 100; i++ {
		r1 := router1.ShouldUseCanary(0.3)
		r2 := router2.ShouldUseCanary(0.3)
		if r1 != r2 {
			t.Errorf("non-deterministic at iteration %d", i)
			break
		}
	}
}
