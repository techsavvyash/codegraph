package pipeline

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/context-maximiser/code-graph/libs/core-models-go"
)

// fakeStage is a test stage with configurable behavior.
type fakeStage struct {
	name     StageName
	optional bool
	items    int
	err      error
	ran      bool
}

func (f *fakeStage) Name() StageName { return f.name }
func (f *fakeStage) Optional() bool  { return f.optional }
func (f *fakeStage) Run(ctx context.Context, cfg *PipelineConfig) (int, error) {
	f.ran = true
	return f.items, f.err
}

func TestPipeline_AllStagesRun(t *testing.T) {
	s1 := &fakeStage{name: "A", items: 5}
	s2 := &fakeStage{name: "B", items: 3}
	p := New(s1, s2)
	cfg := &PipelineConfig{ScopeCtx: models.DefaultScope()}
	results := p.Run(context.Background(), cfg)

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if !s1.ran || !s2.ran {
		t.Fatal("expected both stages to run")
	}
	if results[0].Items != 5 || results[1].Items != 3 {
		t.Error("unexpected items count")
	}
}

func TestPipeline_NonOptionalFailureAborts(t *testing.T) {
	s1 := &fakeStage{name: "A", items: 1}
	s2 := &fakeStage{name: "B", err: errors.New("boom")}
	s3 := &fakeStage{name: "C", items: 2}
	p := New(s1, s2, s3)
	cfg := &PipelineConfig{ScopeCtx: models.DefaultScope()}
	results := p.Run(context.Background(), cfg)

	if len(results) != 2 {
		t.Fatalf("expected 2 results (aborted at B), got %d", len(results))
	}
	if s3.ran {
		t.Error("stage C should not have run after non-optional failure")
	}
}

func TestPipeline_OptionalFailureContinues(t *testing.T) {
	s1 := &fakeStage{name: "A", items: 1}
	s2 := &fakeStage{name: "B", optional: true, err: errors.New("soft fail")}
	s3 := &fakeStage{name: "C", items: 2}
	p := New(s1, s2, s3)
	cfg := &PipelineConfig{ScopeCtx: models.DefaultScope()}
	results := p.Run(context.Background(), cfg)

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	if !s3.ran {
		t.Error("stage C should run after optional failure")
	}
	if !results[1].Skipped {
		t.Error("failed optional stage should be marked as skipped")
	}
}

func TestPipeline_RunParallel(t *testing.T) {
	s1 := &fakeStage{name: "A", items: 5}
	s2 := &fakeStage{name: "B", items: 3, optional: true}
	s3 := &fakeStage{name: "C", items: 2, optional: true}
	s4 := &fakeStage{name: "D", items: 1}

	tiers := []StageTier{
		{Stages: []Stage{s1}},
		{Stages: []Stage{s2, s3}},
		{Stages: []Stage{s4}},
	}

	p := New(s1, s2, s3, s4)
	cfg := &PipelineConfig{ScopeCtx: models.DefaultScope()}
	results := p.RunParallel(context.Background(), cfg, tiers)

	if len(results) != 4 {
		t.Fatalf("expected 4 results, got %d", len(results))
	}
	if !s1.ran || !s2.ran || !s3.ran || !s4.ran {
		t.Fatal("expected all stages to run")
	}
}

func TestPipeline_RunParallelAbortOnNonOptionalFailure(t *testing.T) {
	s1 := &fakeStage{name: "A", items: 1}
	s2 := &fakeStage{name: "B", err: errors.New("boom")} // non-optional
	s3 := &fakeStage{name: "C", items: 2, optional: true}
	s4 := &fakeStage{name: "D", items: 1}

	tiers := []StageTier{
		{Stages: []Stage{s1}},
		{Stages: []Stage{s2, s3}}, // B fails non-optionally
		{Stages: []Stage{s4}},     // should not run
	}

	p := New(s1, s2, s3, s4)
	cfg := &PipelineConfig{ScopeCtx: models.DefaultScope()}
	results := p.RunParallel(context.Background(), cfg, tiers)

	// Tier 0 (1 result) + tier 1 (up to 2 results, aborts at non-optional failure)
	if len(results) > 3 {
		t.Fatalf("expected at most 3 results (abort in tier 1), got %d", len(results))
	}
	if s4.ran {
		t.Error("stage D should not have run after non-optional failure in previous tier")
	}
}

func TestPipeline_RunParallelOptionalFailureContinues(t *testing.T) {
	s1 := &fakeStage{name: "A", items: 1}
	s2 := &fakeStage{name: "B", optional: true, err: errors.New("soft fail")}
	s3 := &fakeStage{name: "C", items: 2, optional: true}
	s4 := &fakeStage{name: "D", items: 3}

	tiers := []StageTier{
		{Stages: []Stage{s1}},
		{Stages: []Stage{s2, s3}}, // B fails optionally
		{Stages: []Stage{s4}},     // should still run
	}

	p := New(s1, s2, s3, s4)
	cfg := &PipelineConfig{ScopeCtx: models.DefaultScope()}
	results := p.RunParallel(context.Background(), cfg, tiers)

	if len(results) != 4 {
		t.Fatalf("expected 4 results, got %d", len(results))
	}
	if !s4.ran {
		t.Error("stage D should run after optional failure in previous tier")
	}

	// B should be marked as skipped.
	for _, r := range results {
		if r.Name == "B" {
			if !r.Skipped {
				t.Error("optional failure B should be marked skipped")
			}
		}
	}
}

func TestPipeline_EmptyStages(t *testing.T) {
	p := New()
	cfg := &PipelineConfig{ScopeCtx: models.DefaultScope()}
	results := p.Run(context.Background(), cfg)
	if len(results) != 0 {
		t.Errorf("expected 0 results for empty pipeline, got %d", len(results))
	}
}

func TestPipeline_RunParallelEmptyTiers(t *testing.T) {
	p := New()
	cfg := &PipelineConfig{ScopeCtx: models.DefaultScope()}
	results := p.RunParallel(context.Background(), cfg, nil)
	if len(results) != 0 {
		t.Errorf("expected 0 results for nil tiers, got %d", len(results))
	}
}

func TestPipeline_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	s1 := &fakeStage{name: "A", items: 1}
	p := New(s1)
	cfg := &PipelineConfig{ScopeCtx: models.DefaultScope()}
	results := p.Run(ctx, cfg)

	// Stage still runs (it doesn't check ctx), but the pipeline should complete.
	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}
}

func TestDefaultStages_Count(t *testing.T) {
	stages := DefaultStages()
	if len(stages) != 4 {
		t.Fatalf("expected 4 default stages, got %d", len(stages))
	}
}

func TestDefaultStages_Names(t *testing.T) {
	stages := DefaultStages()
	expectedNames := []StageName{
		StageIngestCode,
		StageComputeGraphMetrics,
		StageInferServiceDeps,
		StageGenerateFlowSpines,
	}

	if len(stages) != len(expectedNames) {
		t.Fatalf("stage count drift: expected %d names, got %d stages", len(expectedNames), len(stages))
	}
	for i, stage := range stages {
		if stage.Name() != expectedNames[i] {
			t.Errorf("stage %d: expected name %q, got %q", i, expectedNames[i], stage.Name())
		}
	}
}

func TestDefaultStages_FirstIsRequired(t *testing.T) {
	stages := DefaultStages()
	if stages[0].Optional() {
		t.Error("IngestCode (first stage) must not be optional")
	}
}

func TestDefaultStages_OptionalFlags(t *testing.T) {
	stages := DefaultStages()
	// Only IngestCode is required; the rest are optional.
	requiredStages := map[StageName]bool{
		StageIngestCode: true,
	}
	for i, stage := range stages {
		if requiredStages[stage.Name()] {
			if stage.Optional() {
				t.Errorf("stage %d (%s) should be required", i, stage.Name())
			}
		} else {
			if !stage.Optional() {
				t.Errorf("stage %d (%s) should be optional", i, stage.Name())
			}
		}
	}
}

func TestDefaultTiers_Count(t *testing.T) {
	tiers := DefaultTiers()
	if len(tiers) != 3 {
		t.Fatalf("expected 3 tiers, got %d", len(tiers))
	}
}

func TestDefaultTiers_StageDistribution(t *testing.T) {
	tiers := DefaultTiers()
	expected := []int{1, 1, 2} // IngestCode | Metrics | (Deps,Flows)
	if len(tiers) != len(expected) {
		t.Fatalf("expected %d tiers, got %d", len(expected), len(tiers))
	}
	for i, want := range expected {
		if got := len(tiers[i].Stages); got != want {
			t.Errorf("tier %d: expected %d stages, got %d", i, want, got)
		}
	}
}

func TestDefaultTiers_TotalStagesMatch(t *testing.T) {
	tiers := DefaultTiers()
	total := 0
	for _, tier := range tiers {
		total += len(tier.Stages)
	}
	if total != len(DefaultStages()) {
		t.Errorf("tier stage total %d should equal DefaultStages count %d", total, len(DefaultStages()))
	}
}

func TestStageNameConstants(t *testing.T) {
	// Freeze the string values — they're used in logs and might be referenced externally.
	names := map[StageName]string{
		StageIngestCode:          "IngestCode",
		StageInferServiceDeps:    "InferServiceDependencies",
		StageGenerateFlowSpines:  "GenerateFlowSpines",
		StageComputeGraphMetrics: "ComputeGraphMetrics",
	}
	for constant, expected := range names {
		if string(constant) != expected {
			t.Errorf("StageName constant %q != expected %q", constant, expected)
		}
	}
}

func TestStageResult_Fields(t *testing.T) {
	r := StageResult{
		Name:    StageIngestCode,
		Items:   42,
		Skipped: false,
		Err:     nil,
	}
	if r.Name != StageIngestCode {
		t.Errorf("unexpected Name: %s", r.Name)
	}
	if r.Items != 42 {
		t.Errorf("unexpected Items: %d", r.Items)
	}
}

func TestSummary(t *testing.T) {
	results := []StageResult{
		{Name: "A", Items: 5},
		{Name: "B", Items: 3, Err: errors.New("fail")},
	}
	s := Summary(results)
	if s == "" {
		t.Error("summary should not be empty")
	}
}

func TestSummary_NoFailures(t *testing.T) {
	results := []StageResult{
		{Name: "A", Items: 5},
		{Name: "B", Items: 3},
	}
	s := Summary(results)
	if s == "" {
		t.Error("summary should not be empty")
	}
	// Should report 0 failed.
	if !strings.Contains(s, "0 failed") {
		t.Errorf("expected '0 failed' in summary, got: %s", s)
	}
}

func TestSummary_SkippedNotCountedAsFailed(t *testing.T) {
	results := []StageResult{
		{Name: "A", Items: 5},
		{Name: "B", Items: 0, Err: errors.New("soft"), Skipped: true},
	}
	s := Summary(results)
	if !strings.Contains(s, "0 failed") {
		t.Errorf("skipped stages should not count as failed: %s", s)
	}
}

func TestSummary_Empty(t *testing.T) {
	s := Summary(nil)
	if s == "" {
		t.Error("summary of nil should still produce output")
	}
}

func TestPipeline_NonOptionalStageAbortsPipeline(t *testing.T) {
	s1 := &fakeStage{name: "PreStage", items: 1}
	// Simulate a required stage failing.
	s2 := &fakeStage{name: StageIngestCode, optional: false, err: errors.New("ingest failed")}
	s3 := &fakeStage{name: "PostStage", items: 1}

	p := New(s1, s2, s3)
	cfg := &PipelineConfig{ScopeCtx: models.DefaultScope()}
	results := p.Run(context.Background(), cfg)

	// Pipeline should abort at the required stage — PostStage must not run.
	if s3.ran {
		t.Error("PostStage should not run after non-optional stage failure")
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results (aborted at required stage), got %d", len(results))
	}
	if results[1].Err == nil {
		t.Error("required stage result should contain an error")
	}
}
