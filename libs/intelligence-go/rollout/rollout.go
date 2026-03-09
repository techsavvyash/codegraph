package rollout

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Strategy represents the rollout mode for a pipeline stage.
type Strategy string

const (
	// StrategyLive means the strategy is the active production strategy.
	StrategyLive Strategy = "live"

	// StrategyShadow means the strategy runs alongside live but results are not persisted.
	StrategyShadow Strategy = "shadow"

	// StrategyCanary means the strategy handles a fraction of traffic.
	StrategyCanary Strategy = "canary"

	// StrategyDisabled means the strategy is not running.
	StrategyDisabled Strategy = "disabled"
)

// StageConfig describes the rollout configuration for a single stage.
type StageConfig struct {
	Stage           string   `json:"stage"`
	LiveStrategy    string   `json:"liveStrategy"`
	ShadowStrategy  string   `json:"shadowStrategy,omitempty"`
	CanaryPercent   float64  `json:"canaryPercent,omitempty"` // 0.0-1.0
}

// Config holds the complete rollout configuration.
type Config struct {
	Stages []StageConfig `json:"stages"`
}

// GetStageConfig returns the config for a named stage.
func (c *Config) GetStageConfig(stage string) *StageConfig {
	for i := range c.Stages {
		if c.Stages[i].Stage == stage {
			return &c.Stages[i]
		}
	}
	return nil
}

// StrategyRunner executes a named inference strategy.
type StrategyRunner interface {
	// Run executes the strategy and returns the output.
	Run(ctx context.Context, stage string, strategyName string, input any) (output any, err error)
}

// ComparisonResult captures the outputs of live and shadow strategies for comparison.
type ComparisonResult struct {
	Stage          string        `json:"stage"`
	LiveStrategy   string        `json:"liveStrategy"`
	ShadowStrategy string        `json:"shadowStrategy"`
	LiveOutput     any           `json:"liveOutput"`
	ShadowOutput   any           `json:"shadowOutput"`
	LiveDuration   time.Duration `json:"liveDuration"`
	ShadowDuration time.Duration `json:"shadowDuration"`
	LiveError      string        `json:"liveError,omitempty"`
	ShadowError    string        `json:"shadowError,omitempty"`
	Timestamp      time.Time     `json:"timestamp"`
}

// ComparisonStore persists shadow comparison results for analysis.
type ComparisonStore interface {
	SaveComparison(ctx context.Context, result ComparisonResult) error
}

// ShadowExecutor runs both live and shadow strategies, persisting the shadow
// comparison but only returning the live result.
type ShadowExecutor struct {
	runner          StrategyRunner
	comparisonStore ComparisonStore
	mu              sync.Mutex
	comparisons     []ComparisonResult
}

// NewShadowExecutor creates a shadow executor.
func NewShadowExecutor(runner StrategyRunner) *ShadowExecutor {
	return &ShadowExecutor{
		runner: runner,
	}
}

// WithComparisonStore sets the store for persisting comparison results.
func (se *ShadowExecutor) WithComparisonStore(store ComparisonStore) *ShadowExecutor {
	se.comparisonStore = store
	return se
}

// Execute runs both live and shadow strategies. Returns the live output.
// Shadow execution happens concurrently and does not affect the live result.
func (se *ShadowExecutor) Execute(ctx context.Context, cfg StageConfig, input any) (liveOutput any, err error) {
	if cfg.ShadowStrategy == "" {
		// No shadow configured, just run live
		return se.runner.Run(ctx, cfg.Stage, cfg.LiveStrategy, input)
	}

	type result struct {
		output   any
		err      error
		duration time.Duration
	}

	liveCh := make(chan result, 1)
	shadowCh := make(chan result, 1)

	// Run live strategy
	go func() {
		start := time.Now()
		out, runErr := se.runner.Run(ctx, cfg.Stage, cfg.LiveStrategy, input)
		liveCh <- result{output: out, err: runErr, duration: time.Since(start)}
	}()

	// Run shadow strategy (with a separate context so cancellation doesn't affect live)
	shadowCtx, shadowCancel := context.WithTimeout(ctx, 30*time.Second)
	defer shadowCancel()
	go func() {
		start := time.Now()
		out, runErr := se.runner.Run(shadowCtx, cfg.Stage, cfg.ShadowStrategy, input)
		shadowCh <- result{output: out, err: runErr, duration: time.Since(start)}
	}()

	// Wait for live result (required)
	liveResult := <-liveCh

	// Wait for shadow result (best effort)
	var shadowResult result
	select {
	case shadowResult = <-shadowCh:
	case <-time.After(30 * time.Second):
		shadowResult = result{err: fmt.Errorf("shadow timeout")}
	}

	// Record comparison
	comparison := ComparisonResult{
		Stage:          cfg.Stage,
		LiveStrategy:   cfg.LiveStrategy,
		ShadowStrategy: cfg.ShadowStrategy,
		LiveOutput:     liveResult.output,
		ShadowOutput:   shadowResult.output,
		LiveDuration:   liveResult.duration,
		ShadowDuration: shadowResult.duration,
		Timestamp:      time.Now(),
	}
	if liveResult.err != nil {
		comparison.LiveError = liveResult.err.Error()
	}
	if shadowResult.err != nil {
		comparison.ShadowError = shadowResult.err.Error()
	}

	se.mu.Lock()
	se.comparisons = append(se.comparisons, comparison)
	se.mu.Unlock()

	if se.comparisonStore != nil {
		// Best effort persist, don't fail the main execution
		_ = se.comparisonStore.SaveComparison(ctx, comparison)
	}

	return liveResult.output, liveResult.err
}

// GetComparisons returns all recorded comparisons (for testing/analysis).
func (se *ShadowExecutor) GetComparisons() []ComparisonResult {
	se.mu.Lock()
	defer se.mu.Unlock()
	result := make([]ComparisonResult, len(se.comparisons))
	copy(result, se.comparisons)
	return result
}

// CanaryRouter decides whether a request should be routed to the canary strategy.
type CanaryRouter struct {
	counter uint64
	mu      sync.Mutex
}

// NewCanaryRouter creates a new canary router.
func NewCanaryRouter() *CanaryRouter {
	return &CanaryRouter{}
}

// ShouldUseCanary returns true if this request should use the canary strategy
// based on the configured percentage.
func (cr *CanaryRouter) ShouldUseCanary(canaryPercent float64) bool {
	cr.mu.Lock()
	defer cr.mu.Unlock()
	cr.counter++
	// Deterministic modular routing
	threshold := uint64(canaryPercent * 100)
	return (cr.counter % 100) < threshold
}
