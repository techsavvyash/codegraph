package telemetry

import (
	"sync"
	"time"
)

// StageRecord captures telemetry for a single pipeline stage execution.
type StageRecord struct {
	Stage      string        `json:"stage"`
	StartedAt  time.Time     `json:"startedAt"`
	Duration   time.Duration `json:"duration"`
	Success    bool          `json:"success"`
	Error      string        `json:"error,omitempty"`
	ItemCount  int           `json:"itemCount"`
	TokenCost  int           `json:"tokenCost,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

// RunRecord captures telemetry for a complete pipeline run.
type RunRecord struct {
	RunID      string          `json:"runId"`
	CommitSHA  string          `json:"commitSha,omitempty"`
	BuildID    string          `json:"buildId,omitempty"`
	StartedAt  time.Time       `json:"startedAt"`
	Duration   time.Duration   `json:"duration"`
	Stages     []StageRecord   `json:"stages"`
	Quality    QualitySnapshot `json:"quality,omitempty"`
}

// QualitySnapshot captures quality metrics at run time.
type QualitySnapshot struct {
	RecallAtK            float64 `json:"recallAtK,omitempty"`
	NDCG                 float64 `json:"ndcg,omitempty"`
	LinkingPrecision     float64 `json:"linkingPrecision,omitempty"`
	CitationCoverage     float64 `json:"citationCoverage,omitempty"`
	UnsupportedClaimRate float64 `json:"unsupportedClaimRate,omitempty"`
}

// RunSummary provides aggregate statistics across a run.
type RunSummary struct {
	TotalStages   int           `json:"totalStages"`
	SuccessCount  int           `json:"successCount"`
	FailCount     int           `json:"failCount"`
	TotalDuration time.Duration `json:"totalDuration"`
	TotalTokens   int           `json:"totalTokens"`
	FailRate      float64       `json:"failRate"`
}

// Summarize computes summary statistics for a run.
func Summarize(run *RunRecord) RunSummary {
	if run == nil {
		return RunSummary{}
	}

	s := RunSummary{
		TotalStages:   len(run.Stages),
		TotalDuration: run.Duration,
	}

	for _, stage := range run.Stages {
		s.TotalTokens += stage.TokenCost
		if stage.Success {
			s.SuccessCount++
		} else {
			s.FailCount++
		}
	}

	if s.TotalStages > 0 {
		s.FailRate = float64(s.FailCount) / float64(s.TotalStages)
	}

	return s
}

// Recorder provides a thread-safe way to build a RunRecord.
type Recorder struct {
	mu       sync.Mutex
	run      RunRecord
	active   map[string]time.Time
}

// NewRecorder creates a new telemetry recorder.
func NewRecorder(runID string) *Recorder {
	return &Recorder{
		run: RunRecord{
			RunID:     runID,
			StartedAt: time.Now(),
		},
		active: make(map[string]time.Time),
	}
}

// WithCommit sets the commit SHA for this run.
func (r *Recorder) WithCommit(sha string) *Recorder {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.run.CommitSHA = sha
	return r
}

// WithBuild sets the build ID for this run.
func (r *Recorder) WithBuild(buildID string) *Recorder {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.run.BuildID = buildID
	return r
}

// StartStage marks the beginning of a stage.
func (r *Recorder) StartStage(stage string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.active[stage] = time.Now()
}

// EndStage marks the successful completion of a stage.
func (r *Recorder) EndStage(stage string, itemCount int, tokenCost int, metadata map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()

	start, ok := r.active[stage]
	if !ok {
		start = time.Now()
	}
	delete(r.active, stage)

	r.run.Stages = append(r.run.Stages, StageRecord{
		Stage:     stage,
		StartedAt: start,
		Duration:  time.Since(start),
		Success:   true,
		ItemCount: itemCount,
		TokenCost: tokenCost,
		Metadata:  metadata,
	})
}

// FailStage marks a stage as failed.
func (r *Recorder) FailStage(stage string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	start, ok := r.active[stage]
	if !ok {
		start = time.Now()
	}
	delete(r.active, stage)

	errMsg := ""
	if err != nil {
		errMsg = err.Error()
	}

	r.run.Stages = append(r.run.Stages, StageRecord{
		Stage:     stage,
		StartedAt: start,
		Duration:  time.Since(start),
		Success:   false,
		Error:     errMsg,
	})
}

// SetQuality records quality metrics for the run.
func (r *Recorder) SetQuality(q QualitySnapshot) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.run.Quality = q
}

// Finish finalizes the run record.
func (r *Recorder) Finish() *RunRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.run.Duration = time.Since(r.run.StartedAt)
	return &r.run
}

// TrendPoint represents a single data point in a trendline.
type TrendPoint struct {
	CommitSHA string          `json:"commitSha"`
	BuildID   string          `json:"buildId"`
	Timestamp time.Time       `json:"timestamp"`
	Quality   QualitySnapshot `json:"quality"`
	Summary   RunSummary      `json:"summary"`
}

// BuildTrendline constructs trendline data from a series of run records.
func BuildTrendline(runs []*RunRecord) []TrendPoint {
	points := make([]TrendPoint, 0, len(runs))
	for _, run := range runs {
		points = append(points, TrendPoint{
			CommitSHA: run.CommitSHA,
			BuildID:   run.BuildID,
			Timestamp: run.StartedAt,
			Quality:   run.Quality,
			Summary:   Summarize(run),
		})
	}
	return points
}
