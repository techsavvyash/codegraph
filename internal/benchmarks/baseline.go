package benchmarks

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SaveBaseline writes a SelfBenchmarkResult as JSON baseline.
// Creates two files: baseline-{commit}-{timestamp}.json and latest.json.
func SaveBaseline(result *SelfBenchmarkResult, dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("failed to create baseline dir: %w", err)
	}

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal result: %w", err)
	}

	commit := result.GitCommit
	if commit == "" {
		commit = "unknown"
	}
	ts := result.Timestamp.Format("20060102-150405")
	filename := fmt.Sprintf("baseline-%s-%s.json", commit, ts)
	path := filepath.Join(dir, filename)

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", fmt.Errorf("failed to write baseline: %w", err)
	}

	// Also write as latest.json.
	latestPath := filepath.Join(dir, "latest.json")
	if err := os.WriteFile(latestPath, data, 0o644); err != nil {
		return "", fmt.Errorf("failed to write latest.json: %w", err)
	}

	return path, nil
}

// LoadBaseline reads the latest baseline from a directory.
func LoadBaseline(dir string) (*SelfBenchmarkResult, error) {
	path := filepath.Join(dir, "latest.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read baseline: %w", err)
	}

	var result SelfBenchmarkResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse baseline: %w", err)
	}
	return &result, nil
}

// PhaseDelta captures the change between baseline and current for a single phase.
type PhaseDelta struct {
	Name           string        `json:"name"`
	BaselineDur    time.Duration `json:"baselineDuration"`
	CurrentDur     time.Duration `json:"currentDuration"`
	DeltaDur       time.Duration `json:"deltaDuration"`
	DeltaPercent   float64       `json:"deltaPercent"`
	IsRegression   bool          `json:"isRegression"`
	BaselineItems  int           `json:"baselineItems"`
	CurrentItems   int           `json:"currentItems"`
}

// ComparisonResult holds the full comparison between current and baseline.
type ComparisonResult struct {
	BaselineCommit string       `json:"baselineCommit"`
	CurrentCommit  string       `json:"currentCommit"`
	BaselineTime   time.Time    `json:"baselineTime"`
	CurrentTime    time.Time    `json:"currentTime"`
	Phases         []PhaseDelta `json:"phases"`
	WallDelta      PhaseDelta   `json:"wallDelta"`
	TotalDelta     PhaseDelta   `json:"totalDelta"`
	HasRegressions bool         `json:"hasRegressions"`
}

// CompareToBaseline compares current results to a baseline.
// Phases with > regressionThreshold% increase are flagged as regressions.
func CompareToBaseline(current, baseline *SelfBenchmarkResult, regressionThreshold float64) *ComparisonResult {
	if regressionThreshold <= 0 {
		regressionThreshold = 20.0 // default 20%
	}

	cr := &ComparisonResult{
		BaselineCommit: baseline.GitCommit,
		CurrentCommit:  current.GitCommit,
		BaselineTime:   baseline.Timestamp,
		CurrentTime:    current.Timestamp,
	}

	if current.FullRun == nil || baseline.FullRun == nil {
		return cr
	}

	// Build baseline phase map (top-level only).
	baseMap := make(map[string]PhaseResult)
	for _, p := range baseline.FullRun.Phases {
		if !p.IsSubPhase {
			baseMap[p.Name] = p
		}
	}

	// Compare each current phase.
	for _, p := range current.FullRun.Phases {
		if p.IsSubPhase {
			continue
		}
		bp, exists := baseMap[p.Name]
		delta := PhaseDelta{
			Name:          p.Name,
			CurrentDur:    p.Duration,
			CurrentItems:  p.Items,
		}
		if exists {
			delta.BaselineDur = bp.Duration
			delta.BaselineItems = bp.Items
			delta.DeltaDur = p.Duration - bp.Duration
			if bp.Duration > 0 {
				delta.DeltaPercent = float64(delta.DeltaDur) / float64(bp.Duration) * 100
			}
			delta.IsRegression = delta.DeltaPercent > regressionThreshold
			if delta.IsRegression {
				cr.HasRegressions = true
			}
		}
		cr.Phases = append(cr.Phases, delta)
	}

	// Wall clock comparison.
	cr.WallDelta = PhaseDelta{
		Name:        "WALL CLOCK",
		BaselineDur: baseline.FullRun.WallDuration,
		CurrentDur:  current.FullRun.WallDuration,
		DeltaDur:    current.FullRun.WallDuration - baseline.FullRun.WallDuration,
	}
	if baseline.FullRun.WallDuration > 0 {
		cr.WallDelta.DeltaPercent = float64(cr.WallDelta.DeltaDur) / float64(baseline.FullRun.WallDuration) * 100
	}

	// Total summed comparison.
	cr.TotalDelta = PhaseDelta{
		Name:        "TOTAL (summed)",
		BaselineDur: baseline.FullRun.TotalSummed,
		CurrentDur:  current.FullRun.TotalSummed,
		DeltaDur:    current.FullRun.TotalSummed - baseline.FullRun.TotalSummed,
	}
	if baseline.FullRun.TotalSummed > 0 {
		cr.TotalDelta.DeltaPercent = float64(cr.TotalDelta.DeltaDur) / float64(baseline.FullRun.TotalSummed) * 100
	}

	return cr
}

// PrintComparison writes a human-readable comparison table to w.
func PrintComparison(w io.Writer, cr *ComparisonResult) {
	fmt.Fprintf(w, "\nBaseline Comparison\n")
	fmt.Fprintf(w, "Baseline: %s (%s)\n", cr.BaselineCommit, cr.BaselineTime.Format("2006-01-02 15:04"))
	fmt.Fprintf(w, "Current:  %s (%s)\n", cr.CurrentCommit, cr.CurrentTime.Format("2006-01-02 15:04"))
	fmt.Fprintln(w)

	fmt.Fprintf(w, "%-30s %12s %12s %12s %8s %s\n",
		"Phase", "Baseline", "Current", "Delta", "Change", "")
	fmt.Fprintln(w, strings.Repeat("\u2500", 85))

	for _, pd := range cr.Phases {
		flag := ""
		if pd.IsRegression {
			flag = " REGRESSION"
		}
		sign := ""
		if pd.DeltaDur > 0 {
			sign = "+"
		}
		fmt.Fprintf(w, "%-30s %12s %12s %12s %+7.1f%%%s\n",
			pd.Name,
			formatDuration(pd.BaselineDur),
			formatDuration(pd.CurrentDur),
			sign+formatDuration(absDuration(pd.DeltaDur)),
			pd.DeltaPercent,
			flag,
		)
	}

	fmt.Fprintln(w, strings.Repeat("\u2500", 85))

	// Wall clock.
	fmt.Fprintf(w, "%-30s %12s %12s %12s %+7.1f%%\n",
		cr.WallDelta.Name,
		formatDuration(cr.WallDelta.BaselineDur),
		formatDuration(cr.WallDelta.CurrentDur),
		formatDuration(absDuration(cr.WallDelta.DeltaDur)),
		cr.WallDelta.DeltaPercent,
	)

	// Total summed.
	fmt.Fprintf(w, "%-30s %12s %12s %12s %+7.1f%%\n",
		cr.TotalDelta.Name,
		formatDuration(cr.TotalDelta.BaselineDur),
		formatDuration(cr.TotalDelta.CurrentDur),
		formatDuration(absDuration(cr.TotalDelta.DeltaDur)),
		cr.TotalDelta.DeltaPercent,
	)

	if cr.HasRegressions {
		fmt.Fprintf(w, "\nWARNING: Regressions detected (>20%% slower)\n")
	}
	fmt.Fprintln(w)
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}
