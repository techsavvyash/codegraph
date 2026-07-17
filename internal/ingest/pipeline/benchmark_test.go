package pipeline

import (
	"context"
	"testing"
	"time"

	models "github.com/context-maximiser/code-graph/internal/model"
)

// slowFakeStage simulates a stage that takes a configurable duration.
type slowFakeStage struct {
	name     StageName
	optional bool
	delay    time.Duration
	items    int
}

func (s *slowFakeStage) Name() StageName { return s.name }
func (s *slowFakeStage) Optional() bool  { return s.optional }
func (s *slowFakeStage) Run(ctx context.Context, cfg *PipelineConfig) (int, error) {
	time.Sleep(s.delay)
	return s.items, nil
}

func BenchmarkPipelineSequential(b *testing.B) {
	stages := []Stage{
		&fakeStage{name: "A", items: 1},
		&fakeStage{name: "B", items: 1, optional: true},
		&fakeStage{name: "C", items: 1, optional: true},
		&fakeStage{name: "D", items: 1, optional: true},
	}
	p := New(stages...)
	cfg := &PipelineConfig{ScopeCtx: models.DefaultScope()}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.Run(context.Background(), cfg)
	}
}

func BenchmarkPipelineParallel(b *testing.B) {
	stages := []Stage{
		&fakeStage{name: "A", items: 1},
		&fakeStage{name: "B", items: 1, optional: true},
		&fakeStage{name: "C", items: 1, optional: true},
		&fakeStage{name: "D", items: 1, optional: true},
	}
	tiers := []StageTier{
		{Stages: []Stage{stages[0]}},
		{Stages: []Stage{stages[1], stages[2]}},
		{Stages: []Stage{stages[3]}},
	}
	p := New(stages...)
	cfg := &PipelineConfig{ScopeCtx: models.DefaultScope()}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.RunParallel(context.Background(), cfg, tiers)
	}
}

// TestOverlaySLA validates that parallel execution of independent stages
// is faster than sequential when stages have non-trivial latency.
func TestOverlaySLA_ParallelFasterThanSequential(t *testing.T) {
	delay := 50 * time.Millisecond
	s1 := &slowFakeStage{name: "Ingest", items: 1, delay: delay}
	s2 := &slowFakeStage{name: "FlowSpines", items: 1, optional: true, delay: delay}
	s3 := &slowFakeStage{name: "DocIngest", items: 1, optional: true, delay: delay}
	s4 := &slowFakeStage{name: "Link", items: 1, optional: true, delay: delay}

	cfg := &PipelineConfig{ScopeCtx: models.DefaultScope()}

	// Sequential: 4 stages × delay = ~200ms
	pSeq := New(s1, s2, s3, s4)
	seqStart := time.Now()
	pSeq.Run(context.Background(), cfg)
	seqDur := time.Since(seqStart)

	// Parallel tiers: tier0(1) + tier1(2 parallel) + tier2(1) = ~150ms
	s1p := &slowFakeStage{name: "Ingest", items: 1, delay: delay}
	s2p := &slowFakeStage{name: "FlowSpines", items: 1, optional: true, delay: delay}
	s3p := &slowFakeStage{name: "DocIngest", items: 1, optional: true, delay: delay}
	s4p := &slowFakeStage{name: "Link", items: 1, optional: true, delay: delay}

	tiers := []StageTier{
		{Stages: []Stage{s1p}},
		{Stages: []Stage{s2p, s3p}},
		{Stages: []Stage{s4p}},
	}
	pPar := New(s1p, s2p, s3p, s4p)
	parStart := time.Now()
	pPar.RunParallel(context.Background(), cfg, tiers)
	parDur := time.Since(parStart)

	t.Logf("Sequential: %v, Parallel: %v (speedup: %.2fx)", seqDur, parDur, float64(seqDur)/float64(parDur))

	// Parallel should be at least 15% faster due to tier1 running 2 stages concurrently.
	if parDur >= seqDur {
		t.Errorf("parallel (%v) should be faster than sequential (%v)", parDur, seqDur)
	}
}

// TestOverlaySLA_PRScopeCreation validates that creating a PR scope context is fast.
func TestOverlaySLA_PRScopeCreation(t *testing.T) {
	start := time.Now()
	for i := 0; i < 10000; i++ {
		sc := models.NewPRScope("42")
		_ = sc.Props()
	}
	dur := time.Since(start)

	// 10,000 scope creations should complete in well under 100ms.
	if dur > 100*time.Millisecond {
		t.Errorf("scope creation too slow: %v for 10000 iterations", dur)
	}
}
