package pipeline

import (
	"context"
	"errors"
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
