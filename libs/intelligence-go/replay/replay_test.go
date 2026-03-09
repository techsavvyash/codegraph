package replay

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// --- Mocks ---

type mockChangeDetector struct {
	hashes map[string]string
	err    error
}

func (m *mockChangeDetector) ComputeInputHash(_ context.Context, stage string) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	return m.hashes[stage], nil
}

type mockStageExecutor struct {
	results map[string]string
	errors  map[string]error
}

func (m *mockStageExecutor) Execute(_ context.Context, stage string) (string, error) {
	if err, ok := m.errors[stage]; ok {
		return "", err
	}
	return m.results[stage], nil
}

type mockStateStore struct {
	states []StageState
	err    error
}

func (m *mockStateStore) GetStageStates(_ context.Context) ([]StageState, error) {
	return m.states, m.err
}

func (m *mockStateStore) SaveStageState(_ context.Context, state StageState) error {
	m.states = append(m.states, state)
	return m.err
}

// --- Planner Tests ---

func TestPlanner_AllNew(t *testing.T) {
	planner := NewPlanner(
		[]string{"retrieval", "inference", "generation"},
		nil, // no change detector
	)

	plan, err := planner.Plan(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !plan.NeedsReplay() {
		t.Error("expected replay needed for all new stages")
	}
	if len(plan.StagesToReplay) != 3 {
		t.Errorf("expected 3 stages to replay, got %d", len(plan.StagesToReplay))
	}
	for _, stage := range plan.StagesToReplay {
		if plan.Reason[stage] == "" {
			t.Errorf("expected reason for stage %s", stage)
		}
	}
}

func TestPlanner_AllClean(t *testing.T) {
	detector := &mockChangeDetector{
		hashes: map[string]string{
			"retrieval":  "hash1",
			"inference":  "hash2",
			"generation": "hash3",
		},
	}

	planner := NewPlanner(
		[]string{"retrieval", "inference", "generation"},
		detector,
	)

	previousStates := []StageState{
		{Stage: "retrieval", Status: StatusSuccess, InputHash: "hash1"},
		{Stage: "inference", Status: StatusSuccess, InputHash: "hash2"},
		{Stage: "generation", Status: StatusSuccess, InputHash: "hash3"},
	}

	plan, err := planner.Plan(context.Background(), previousStates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.NeedsReplay() {
		t.Error("expected no replay when all clean")
	}
	if len(plan.StagesToSkip) != 3 {
		t.Errorf("expected 3 stages to skip, got %d", len(plan.StagesToSkip))
	}
}

func TestPlanner_FailedStageReplayed(t *testing.T) {
	planner := NewPlanner(
		[]string{"retrieval", "inference", "generation"},
		nil,
	)

	previousStates := []StageState{
		{Stage: "retrieval", Status: StatusSuccess, InputHash: "hash1"},
		{Stage: "inference", Status: StatusFailed},
		{Stage: "generation", Status: StatusSuccess, InputHash: "hash3"},
	}

	plan, err := planner.Plan(context.Background(), previousStates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// inference failed → replay inference + generation (downstream)
	if len(plan.StagesToReplay) != 2 {
		t.Errorf("expected 2 stages to replay, got %d: %v", len(plan.StagesToReplay), plan.StagesToReplay)
	}
	if plan.StagesToReplay[0] != "inference" {
		t.Errorf("expected inference first, got %s", plan.StagesToReplay[0])
	}
	if plan.Reason["inference"] != "previous_failure" {
		t.Errorf("expected reason 'previous_failure', got %s", plan.Reason["inference"])
	}
	if plan.Reason["generation"] != "upstream_changed" {
		t.Errorf("expected reason 'upstream_changed', got %s", plan.Reason["generation"])
	}
}

func TestPlanner_InputChanged(t *testing.T) {
	detector := &mockChangeDetector{
		hashes: map[string]string{
			"retrieval":  "new-hash", // Changed!
			"inference":  "hash2",
			"generation": "hash3",
		},
	}

	planner := NewPlanner(
		[]string{"retrieval", "inference", "generation"},
		detector,
	)

	previousStates := []StageState{
		{Stage: "retrieval", Status: StatusSuccess, InputHash: "old-hash"},
		{Stage: "inference", Status: StatusSuccess, InputHash: "hash2"},
		{Stage: "generation", Status: StatusSuccess, InputHash: "hash3"},
	}

	plan, err := planner.Plan(context.Background(), previousStates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// retrieval input changed → all downstream too
	if len(plan.StagesToReplay) != 3 {
		t.Errorf("expected 3 stages to replay, got %d", len(plan.StagesToReplay))
	}
	if plan.Reason["retrieval"] != "input_changed" {
		t.Errorf("expected 'input_changed', got %s", plan.Reason["retrieval"])
	}
}

func TestPlanner_MiddleStageChangedOnly(t *testing.T) {
	detector := &mockChangeDetector{
		hashes: map[string]string{
			"retrieval":  "hash1",     // Same
			"inference":  "new-hash2", // Changed
			"generation": "hash3",     // Same (but downstream of changed)
		},
	}

	planner := NewPlanner(
		[]string{"retrieval", "inference", "generation"},
		detector,
	)

	previousStates := []StageState{
		{Stage: "retrieval", Status: StatusSuccess, InputHash: "hash1"},
		{Stage: "inference", Status: StatusSuccess, InputHash: "hash2"},
		{Stage: "generation", Status: StatusSuccess, InputHash: "hash3"},
	}

	plan, err := planner.Plan(context.Background(), previousStates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(plan.StagesToSkip) != 1 || plan.StagesToSkip[0] != "retrieval" {
		t.Errorf("expected only retrieval skipped, got %v", plan.StagesToSkip)
	}
	if len(plan.StagesToReplay) != 2 {
		t.Errorf("expected 2 stages to replay, got %d", len(plan.StagesToReplay))
	}
}

// --- Executor Tests ---

func TestExecutor_SuccessfulReplay(t *testing.T) {
	executor := &mockStageExecutor{
		results: map[string]string{
			"inference":  "out-hash-1",
			"generation": "out-hash-2",
		},
	}
	store := &mockStateStore{}

	e := NewExecutor(executor, store)
	plan := &ReplayPlan{
		StagesToReplay: []string{"inference", "generation"},
	}

	results, err := e.ExecutePlan(context.Background(), plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Status != StatusSuccess {
		t.Error("expected first stage success")
	}
	if results[0].OutputHash != "out-hash-1" {
		t.Errorf("expected output hash 'out-hash-1', got %s", results[0].OutputHash)
	}
}

func TestExecutor_StopsOnFailure(t *testing.T) {
	executor := &mockStageExecutor{
		results: map[string]string{"generation": "out"},
		errors:  map[string]error{"inference": fmt.Errorf("model error")},
	}

	e := NewExecutor(executor, nil)
	plan := &ReplayPlan{
		StagesToReplay: []string{"inference", "generation"},
	}

	results, err := e.ExecutePlan(context.Background(), plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should stop after inference failure
	if len(results) != 1 {
		t.Fatalf("expected 1 result (stopped on failure), got %d", len(results))
	}
	if results[0].Status != StatusFailed {
		t.Error("expected failed status")
	}
}

func TestExecutor_PersistsState(t *testing.T) {
	executor := &mockStageExecutor{
		results: map[string]string{"retrieval": "hash1"},
	}
	store := &mockStateStore{}

	e := NewExecutor(executor, store)
	plan := &ReplayPlan{StagesToReplay: []string{"retrieval"}}

	_, err := e.ExecutePlan(context.Background(), plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(store.states) != 1 {
		t.Errorf("expected 1 saved state, got %d", len(store.states))
	}
}

func TestReplayPlan_NeedsReplay(t *testing.T) {
	empty := &ReplayPlan{}
	if empty.NeedsReplay() {
		t.Error("expected false for empty plan")
	}

	withStages := &ReplayPlan{StagesToReplay: []string{"a"}}
	if !withStages.NeedsReplay() {
		t.Error("expected true for plan with stages")
	}
}

func TestStageState_Timestamps(t *testing.T) {
	state := StageState{
		Stage:     "test",
		Status:    StatusSuccess,
		LastRunAt: time.Now(),
	}
	if state.LastRunAt.IsZero() {
		t.Error("expected non-zero timestamp")
	}
}
