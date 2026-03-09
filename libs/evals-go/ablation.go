package evals

import (
	"fmt"
	"math"
)

// AblationFeature represents a single feature that can be ablated (removed).
type AblationFeature string

const (
	FeatureVectorScore       AblationFeature = "vector_score"
	FeatureLexicalOverlap    AblationFeature = "lexical_overlap"
	FeatureStructuralSupport AblationFeature = "structural_support"
	FeatureExplicitReference AblationFeature = "explicit_reference"
	FeatureFlowMembership    AblationFeature = "flow_membership"
	FeatureExportedTarget    AblationFeature = "exported_target"
)

// AllAblationFeatures returns the complete list of features for ablation.
func AllAblationFeatures() []AblationFeature {
	return []AblationFeature{
		FeatureVectorScore,
		FeatureLexicalOverlap,
		FeatureStructuralSupport,
		FeatureExplicitReference,
		FeatureFlowMembership,
		FeatureExportedTarget,
	}
}

// AblationResult holds the impact of removing a single feature.
type AblationResult struct {
	RemovedFeature AblationFeature `json:"removedFeature"`
	BaselineMetric float64         `json:"baselineMetric"`
	AblatedMetric  float64         `json:"ablatedMetric"`
	Delta          float64         `json:"delta"`          // ablated - baseline (negative = feature helps)
	RelativeDelta  float64         `json:"relativeDelta"`  // delta / baseline (percentage change)
}

// AblationReport summarizes the full ablation study.
type AblationReport struct {
	MetricName string           `json:"metricName"`
	Baseline   float64          `json:"baseline"`
	Results    []AblationResult `json:"results"`
}

// MostImpactful returns the feature whose removal causes the largest drop.
func (r *AblationReport) MostImpactful() *AblationResult {
	if len(r.Results) == 0 {
		return nil
	}
	var worst *AblationResult
	for i := range r.Results {
		if worst == nil || r.Results[i].Delta < worst.Delta {
			worst = &r.Results[i]
		}
	}
	return worst
}

// LeastImpactful returns the feature whose removal causes the smallest change.
func (r *AblationReport) LeastImpactful() *AblationResult {
	if len(r.Results) == 0 {
		return nil
	}
	var best *AblationResult
	for i := range r.Results {
		absDelta := math.Abs(r.Results[i].Delta)
		if best == nil || absDelta < math.Abs(best.Delta) {
			best = &r.Results[i]
		}
	}
	return best
}

// ComputeAblation computes the impact of removing each feature.
// baseline is the metric with all features active.
// ablatedScores maps each feature to the metric value when that feature is removed.
func ComputeAblation(metricName string, baseline float64, ablatedScores map[AblationFeature]float64) *AblationReport {
	report := &AblationReport{
		MetricName: metricName,
		Baseline:   baseline,
	}

	for _, feature := range AllAblationFeatures() {
		ablated, ok := ablatedScores[feature]
		if !ok {
			continue
		}

		delta := ablated - baseline
		var relativeDelta float64
		if baseline != 0 {
			relativeDelta = delta / baseline
		}

		report.Results = append(report.Results, AblationResult{
			RemovedFeature: feature,
			BaselineMetric: baseline,
			AblatedMetric:  ablated,
			Delta:          delta,
			RelativeDelta:  relativeDelta,
		})
	}

	return report
}

// DriftCheck compares two eval runs and reports metric deltas.
type DriftCheck struct {
	MetricName string  `json:"metricName"`
	Previous   float64 `json:"previous"`
	Current    float64 `json:"current"`
	Delta      float64 `json:"delta"`
	Regressed  bool    `json:"regressed"`
}

// DriftReport summarizes changes between two runs.
type DriftReport struct {
	Checks     []DriftCheck `json:"checks"`
	HasDrift   bool         `json:"hasDrift"`
	Regressions int         `json:"regressions"`
}

// Summary returns a human-readable summary.
func (r *DriftReport) Summary() string {
	if !r.HasDrift {
		return "No significant drift detected"
	}
	return fmt.Sprintf("%d regression(s) detected across %d metrics", r.Regressions, len(r.Checks))
}

// ComputeDrift compares previous and current metrics, flagging regressions
// where the metric dropped below the tolerance threshold.
func ComputeDrift(previous, current EvalGateInput, tolerance float64) *DriftReport {
	checks := []struct {
		name     string
		prev     float64
		curr     float64
		higherIsBetter bool
	}{
		{"recall@k", previous.RecallAtK, current.RecallAtK, true},
		{"ndcg", previous.NDCG, current.NDCG, true},
		{"mrr", previous.MRR, current.MRR, true},
		{"precision@k", previous.PrecisionAtK, current.PrecisionAtK, true},
		{"linking_precision", previous.LinkingPrecision, current.LinkingPrecision, true},
		{"citation_coverage", previous.CitationCoverage, current.CitationCoverage, true},
		{"unsupported_claim_rate", previous.UnsupportedClaimRate, current.UnsupportedClaimRate, false},
	}

	report := &DriftReport{}

	for _, c := range checks {
		delta := c.curr - c.prev
		regressed := false

		if c.higherIsBetter {
			// Regression if current is significantly lower
			regressed = delta < -tolerance
		} else {
			// Regression if current is significantly higher (for "lower is better" metrics)
			regressed = delta > tolerance
		}

		report.Checks = append(report.Checks, DriftCheck{
			MetricName: c.name,
			Previous:   c.prev,
			Current:    c.curr,
			Delta:      delta,
			Regressed:  regressed,
		})

		if regressed {
			report.HasDrift = true
			report.Regressions++
		}
	}

	return report
}
