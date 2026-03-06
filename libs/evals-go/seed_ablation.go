package evals

import "fmt"

// SeedAblationResult compares heuristic vs graph-structural seed detection.
type SeedAblationResult struct {
	HeuristicOnly []string `json:"heuristicOnly"` // Seeds unique to heuristic approach
	GraphOnly     []string `json:"graphOnly"`      // Seeds unique to graph-structural approach
	Shared        []string `json:"shared"`         // Seeds found by both approaches

	HeuristicPrecision float64 `json:"heuristicPrecision"`
	HeuristicRecall    float64 `json:"heuristicRecall"`
	HeuristicF1        float64 `json:"heuristicF1"`

	GraphPrecision float64 `json:"graphPrecision"`
	GraphRecall    float64 `json:"graphRecall"`
	GraphF1        float64 `json:"graphF1"`

	PrecisionDelta float64 `json:"precisionDelta"` // graph - heuristic
	RecallDelta    float64 `json:"recallDelta"`
	F1Delta        float64 `json:"f1Delta"`
}

// ComputeSeedAblation performs a side-by-side comparison of two seed detection
// approaches against the same golden set.
func ComputeSeedAblation(goldenPath string, heuristicSeeds, graphSeeds []string) (*SeedAblationResult, error) {
	runner := NewSeedEvalRunner(goldenPath)

	hResult, err := runner.Evaluate(heuristicSeeds)
	if err != nil {
		return nil, fmt.Errorf("heuristic eval: %w", err)
	}

	gResult, err := runner.Evaluate(graphSeeds)
	if err != nil {
		return nil, fmt.Errorf("graph eval: %w", err)
	}

	hSet := toSet(heuristicSeeds)
	gSet := toSet(graphSeeds)

	result := &SeedAblationResult{
		HeuristicPrecision: hResult.Precision,
		HeuristicRecall:    hResult.Recall,
		HeuristicF1:        hResult.F1,
		GraphPrecision:     gResult.Precision,
		GraphRecall:        gResult.Recall,
		GraphF1:            gResult.F1,
		PrecisionDelta:     gResult.Precision - hResult.Precision,
		RecallDelta:        gResult.Recall - hResult.Recall,
		F1Delta:            gResult.F1 - hResult.F1,
	}

	for k := range hSet {
		if gSet[k] {
			result.Shared = append(result.Shared, k)
		} else {
			result.HeuristicOnly = append(result.HeuristicOnly, k)
		}
	}
	for k := range gSet {
		if !hSet[k] {
			result.GraphOnly = append(result.GraphOnly, k)
		}
	}

	return result, nil
}

// Summary returns a human-readable summary of the ablation.
func (r *SeedAblationResult) Summary() string {
	return fmt.Sprintf(
		"Shared: %d, Heuristic-only: %d, Graph-only: %d | "+
			"Precision delta: %+.3f, Recall delta: %+.3f, F1 delta: %+.3f",
		len(r.Shared), len(r.HeuristicOnly), len(r.GraphOnly),
		r.PrecisionDelta, r.RecallDelta, r.F1Delta)
}

func toSet(items []string) map[string]bool {
	s := make(map[string]bool, len(items))
	for _, item := range items {
		s[item] = true
	}
	return s
}
