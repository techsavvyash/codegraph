package telemetry

import (
	"fmt"
	"testing"
	"time"
)

func TestRecorder_BasicFlow(t *testing.T) {
	rec := NewRecorder("run-001").WithCommit("abc123").WithBuild("build-42")

	rec.StartStage("retrieval")
	time.Sleep(1 * time.Millisecond)
	rec.EndStage("retrieval", 100, 0, nil)

	rec.StartStage("inference")
	time.Sleep(1 * time.Millisecond)
	rec.EndStage("inference", 50, 500, map[string]any{"model": "gpt-4"})

	run := rec.Finish()

	if run.RunID != "run-001" {
		t.Errorf("expected run ID run-001, got %s", run.RunID)
	}
	if run.CommitSHA != "abc123" {
		t.Errorf("expected commit abc123, got %s", run.CommitSHA)
	}
	if run.BuildID != "build-42" {
		t.Errorf("expected build build-42, got %s", run.BuildID)
	}
	if len(run.Stages) != 2 {
		t.Fatalf("expected 2 stages, got %d", len(run.Stages))
	}
	if !run.Stages[0].Success {
		t.Error("expected first stage to be successful")
	}
	if run.Stages[1].TokenCost != 500 {
		t.Errorf("expected 500 tokens, got %d", run.Stages[1].TokenCost)
	}
	if run.Duration <= 0 {
		t.Error("expected positive run duration")
	}
}

func TestRecorder_FailStage(t *testing.T) {
	rec := NewRecorder("run-002")

	rec.StartStage("generation")
	rec.FailStage("generation", fmt.Errorf("LLM timeout"))

	run := rec.Finish()
	if len(run.Stages) != 1 {
		t.Fatalf("expected 1 stage, got %d", len(run.Stages))
	}
	if run.Stages[0].Success {
		t.Error("expected stage to be failed")
	}
	if run.Stages[0].Error != "LLM timeout" {
		t.Errorf("expected error 'LLM timeout', got %s", run.Stages[0].Error)
	}
}

func TestRecorder_SetQuality(t *testing.T) {
	rec := NewRecorder("run-003")
	rec.SetQuality(QualitySnapshot{
		RecallAtK:        0.75,
		NDCG:             0.60,
		CitationCoverage: 0.90,
	})

	run := rec.Finish()
	if run.Quality.RecallAtK != 0.75 {
		t.Errorf("expected recall 0.75, got %f", run.Quality.RecallAtK)
	}
}

func TestSummarize(t *testing.T) {
	run := &RunRecord{
		Stages: []StageRecord{
			{Stage: "retrieval", Success: true, TokenCost: 0},
			{Stage: "inference", Success: true, TokenCost: 500},
			{Stage: "generation", Success: false, TokenCost: 1000},
		},
		Duration: 5 * time.Second,
	}

	summary := Summarize(run)
	if summary.TotalStages != 3 {
		t.Errorf("expected 3 total stages, got %d", summary.TotalStages)
	}
	if summary.SuccessCount != 2 {
		t.Errorf("expected 2 successes, got %d", summary.SuccessCount)
	}
	if summary.FailCount != 1 {
		t.Errorf("expected 1 failure, got %d", summary.FailCount)
	}
	if summary.TotalTokens != 1500 {
		t.Errorf("expected 1500 tokens, got %d", summary.TotalTokens)
	}
	expectedFailRate := 1.0 / 3.0
	if summary.FailRate < expectedFailRate-0.01 || summary.FailRate > expectedFailRate+0.01 {
		t.Errorf("expected fail rate ~%.3f, got %.3f", expectedFailRate, summary.FailRate)
	}
}

func TestSummarize_Nil(t *testing.T) {
	summary := Summarize(nil)
	if summary.TotalStages != 0 {
		t.Error("expected 0 stages for nil run")
	}
}

func TestBuildTrendline(t *testing.T) {
	runs := []*RunRecord{
		{
			CommitSHA: "aaa",
			BuildID:   "1",
			StartedAt: time.Now().Add(-2 * time.Hour),
			Duration:  1 * time.Minute,
			Stages:    []StageRecord{{Success: true}},
			Quality:   QualitySnapshot{RecallAtK: 0.70},
		},
		{
			CommitSHA: "bbb",
			BuildID:   "2",
			StartedAt: time.Now().Add(-1 * time.Hour),
			Duration:  2 * time.Minute,
			Stages:    []StageRecord{{Success: true}, {Success: false}},
			Quality:   QualitySnapshot{RecallAtK: 0.75},
		},
	}

	trend := BuildTrendline(runs)
	if len(trend) != 2 {
		t.Fatalf("expected 2 trend points, got %d", len(trend))
	}
	if trend[0].CommitSHA != "aaa" {
		t.Errorf("expected first commit aaa, got %s", trend[0].CommitSHA)
	}
	if trend[1].Quality.RecallAtK != 0.75 {
		t.Errorf("expected recall 0.75, got %f", trend[1].Quality.RecallAtK)
	}
	if trend[1].Summary.FailCount != 1 {
		t.Errorf("expected 1 failure in second run, got %d", trend[1].Summary.FailCount)
	}
}

func TestBuildTrendline_Empty(t *testing.T) {
	trend := BuildTrendline(nil)
	if len(trend) != 0 {
		t.Errorf("expected 0 trend points, got %d", len(trend))
	}
}

func TestRecorder_StageWithoutStart(t *testing.T) {
	rec := NewRecorder("run-004")
	// EndStage without StartStage should still work
	rec.EndStage("orphan", 10, 0, nil)

	run := rec.Finish()
	if len(run.Stages) != 1 {
		t.Fatalf("expected 1 stage, got %d", len(run.Stages))
	}
	if !run.Stages[0].Success {
		t.Error("expected stage to be successful")
	}
}
