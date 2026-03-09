package replay

import (
	"context"
	"fmt"
	"time"
)

// StageStatus represents the outcome of a previous stage execution.
type StageStatus string

const (
	StatusSuccess StageStatus = "success"
	StatusFailed  StageStatus = "failed"
	StatusSkipped StageStatus = "skipped"
	StatusPending StageStatus = "pending"
)

// StageState captures the last known state of a pipeline stage.
type StageState struct {
	Stage       string      `json:"stage"`
	Status      StageStatus `json:"status"`
	LastRunAt   time.Time   `json:"lastRunAt"`
	InputHash   string      `json:"inputHash,omitempty"`
	OutputHash  string      `json:"outputHash,omitempty"`
	Error       string      `json:"error,omitempty"`
}

// ReplayPlan describes which stages need to be re-executed.
type ReplayPlan struct {
	StagesToReplay []string           `json:"stagesToReplay"`
	StagesToSkip   []string           `json:"stagesToSkip"`
	Reason         map[string]string  `json:"reason"` // stage → reason for replay
}

// NeedsReplay returns true if the plan has any stages to replay.
func (p *ReplayPlan) NeedsReplay() bool {
	return len(p.StagesToReplay) > 0
}

// StageExecutor runs a single derivation stage.
type StageExecutor interface {
	// Execute runs the stage and returns the output hash.
	Execute(ctx context.Context, stage string) (outputHash string, err error)
}

// StateStore persists and retrieves stage state.
type StateStore interface {
	// GetStageStates returns the last known states for all stages.
	GetStageStates(ctx context.Context) ([]StageState, error)
	// SaveStageState persists a stage state.
	SaveStageState(ctx context.Context, state StageState) error
}

// InputChangeDetector determines whether stage inputs have changed.
type InputChangeDetector interface {
	// ComputeInputHash returns a hash of the current inputs for a stage.
	ComputeInputHash(ctx context.Context, stage string) (string, error)
}

// Planner builds a replay plan by checking which stages need re-execution.
type Planner struct {
	stages          []string // Ordered list of all stages
	changeDetector  InputChangeDetector
}

// NewPlanner creates a replay planner for the given ordered stages.
func NewPlanner(stages []string, detector InputChangeDetector) *Planner {
	return &Planner{
		stages:         stages,
		changeDetector: detector,
	}
}

// Plan determines which stages need to be replayed based on previous state
// and current input hashes.
func (p *Planner) Plan(ctx context.Context, previousStates []StageState) (*ReplayPlan, error) {
	plan := &ReplayPlan{
		Reason: make(map[string]string),
	}

	stateMap := make(map[string]*StageState)
	for i := range previousStates {
		stateMap[previousStates[i].Stage] = &previousStates[i]
	}

	// Once a stage needs replay, all downstream stages also need replay
	downstreamDirty := false

	for _, stage := range p.stages {
		prev, hasPrev := stateMap[stage]

		if !hasPrev {
			// Never run before
			plan.StagesToReplay = append(plan.StagesToReplay, stage)
			plan.Reason[stage] = "never_executed"
			downstreamDirty = true
			continue
		}

		if prev.Status == StatusFailed {
			plan.StagesToReplay = append(plan.StagesToReplay, stage)
			plan.Reason[stage] = "previous_failure"
			downstreamDirty = true
			continue
		}

		if downstreamDirty {
			plan.StagesToReplay = append(plan.StagesToReplay, stage)
			plan.Reason[stage] = "upstream_changed"
			continue
		}

		// Check if inputs changed
		if p.changeDetector != nil {
			currentHash, err := p.changeDetector.ComputeInputHash(ctx, stage)
			if err != nil {
				plan.StagesToReplay = append(plan.StagesToReplay, stage)
				plan.Reason[stage] = fmt.Sprintf("hash_error: %v", err)
				downstreamDirty = true
				continue
			}

			if currentHash != prev.InputHash {
				plan.StagesToReplay = append(plan.StagesToReplay, stage)
				plan.Reason[stage] = "input_changed"
				downstreamDirty = true
				continue
			}
		}

		// Stage is clean, skip it
		plan.StagesToSkip = append(plan.StagesToSkip, stage)
	}

	return plan, nil
}

// Executor runs a replay plan stage by stage.
type Executor struct {
	stageExecutor StageExecutor
	stateStore    StateStore
}

// NewExecutor creates a replay executor.
func NewExecutor(executor StageExecutor, store StateStore) *Executor {
	return &Executor{
		stageExecutor: executor,
		stateStore:    store,
	}
}

// ExecutePlan runs all stages in the replay plan, persisting state after each.
func (e *Executor) ExecutePlan(ctx context.Context, plan *ReplayPlan) ([]StageState, error) {
	var results []StageState

	for _, stage := range plan.StagesToReplay {
		state := StageState{
			Stage:     stage,
			LastRunAt: time.Now(),
		}

		outputHash, err := e.stageExecutor.Execute(ctx, stage)
		if err != nil {
			state.Status = StatusFailed
			state.Error = err.Error()
		} else {
			state.Status = StatusSuccess
			state.OutputHash = outputHash
		}

		if e.stateStore != nil {
			if saveErr := e.stateStore.SaveStageState(ctx, state); saveErr != nil {
				// Log but don't fail the execution
				state.Error += fmt.Sprintf("; state save error: %v", saveErr)
			}
		}

		results = append(results, state)

		// Stop on failure — downstream stages depend on this
		if state.Status == StatusFailed {
			break
		}
	}

	return results, nil
}
