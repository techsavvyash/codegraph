package evals

import (
	"math"
	"testing"
)

func TestComputeAblation(t *testing.T) {
	baseline := 0.80
	ablated := map[AblationFeature]float64{
		FeatureVectorScore:       0.65, // Big impact
		FeatureLexicalOverlap:    0.78, // Small impact
		FeatureStructuralSupport: 0.70, // Medium impact
		FeatureExplicitReference: 0.75, // Medium impact
		FeatureFlowMembership:    0.79, // Very small impact
		FeatureExportedTarget:    0.80, // No impact
	}

	report := ComputeAblation("ndcg", baseline, ablated)
	if report.MetricName != "ndcg" {
		t.Errorf("expected metric name ndcg, got %s", report.MetricName)
	}
	if report.Baseline != 0.80 {
		t.Errorf("expected baseline 0.80, got %f", report.Baseline)
	}
	if len(report.Results) != 6 {
		t.Fatalf("expected 6 ablation results, got %d", len(report.Results))
	}

	// Check deltas are negative (feature removal hurts)
	for _, r := range report.Results {
		if r.RemovedFeature == FeatureExportedTarget {
			continue // No impact
		}
		if r.Delta > 0 {
			t.Errorf("removing %s should decrease metric, delta=%f", r.RemovedFeature, r.Delta)
		}
	}
}

func TestAblationReport_MostImpactful(t *testing.T) {
	report := ComputeAblation("ndcg", 0.80, map[AblationFeature]float64{
		FeatureVectorScore:       0.50, // -0.30
		FeatureStructuralSupport: 0.70, // -0.10
	})

	most := report.MostImpactful()
	if most == nil {
		t.Fatal("expected non-nil most impactful")
	}
	if most.RemovedFeature != FeatureVectorScore {
		t.Errorf("expected vector_score as most impactful, got %s", most.RemovedFeature)
	}
}

func TestAblationReport_LeastImpactful(t *testing.T) {
	report := ComputeAblation("ndcg", 0.80, map[AblationFeature]float64{
		FeatureVectorScore:       0.50, // -0.30
		FeatureStructuralSupport: 0.79, // -0.01
	})

	least := report.LeastImpactful()
	if least == nil {
		t.Fatal("expected non-nil least impactful")
	}
	if least.RemovedFeature != FeatureStructuralSupport {
		t.Errorf("expected structural_support as least impactful, got %s", least.RemovedFeature)
	}
}

func TestAblationReport_Empty(t *testing.T) {
	report := ComputeAblation("ndcg", 0.80, map[AblationFeature]float64{})
	if report.MostImpactful() != nil {
		t.Error("expected nil for empty report")
	}
	if report.LeastImpactful() != nil {
		t.Error("expected nil for empty report")
	}
}

func TestAblationResult_RelativeDelta(t *testing.T) {
	report := ComputeAblation("ndcg", 0.80, map[AblationFeature]float64{
		FeatureVectorScore: 0.60,
	})

	result := report.Results[0]
	expectedRelative := -0.20 / 0.80
	if math.Abs(result.RelativeDelta-expectedRelative) > 0.001 {
		t.Errorf("expected relative delta %f, got %f", expectedRelative, result.RelativeDelta)
	}
}

func TestComputeDrift_NoDrift(t *testing.T) {
	previous := EvalGateInput{
		RecallAtK:    0.70,
		NDCG:         0.60,
		MRR:          0.50,
		PrecisionAtK: 0.30,
	}
	current := EvalGateInput{
		RecallAtK:    0.72, // Small improvement
		NDCG:         0.61,
		MRR:          0.50,
		PrecisionAtK: 0.29, // Tiny drop, within tolerance
	}

	report := ComputeDrift(previous, current, 0.05)
	if report.HasDrift {
		t.Error("expected no drift within tolerance")
	}
	if report.Regressions != 0 {
		t.Errorf("expected 0 regressions, got %d", report.Regressions)
	}
}

func TestComputeDrift_WithRegression(t *testing.T) {
	previous := EvalGateInput{
		RecallAtK: 0.80,
		NDCG:      0.70,
	}
	current := EvalGateInput{
		RecallAtK: 0.60, // -0.20, beyond tolerance
		NDCG:      0.70,
	}

	report := ComputeDrift(previous, current, 0.05)
	if !report.HasDrift {
		t.Error("expected drift detected")
	}
	if report.Regressions < 1 {
		t.Errorf("expected at least 1 regression, got %d", report.Regressions)
	}

	// Check recall@k specifically
	foundRecall := false
	for _, c := range report.Checks {
		if c.MetricName == "recall@k" && c.Regressed {
			foundRecall = true
			if math.Abs(c.Delta-(-0.20)) > 0.001 {
				t.Errorf("expected delta ~-0.20, got %f", c.Delta)
			}
		}
	}
	if !foundRecall {
		t.Error("expected recall@k regression")
	}
}

func TestComputeDrift_UnsupportedClaimRateRegression(t *testing.T) {
	previous := EvalGateInput{
		UnsupportedClaimRate: 0.05,
	}
	current := EvalGateInput{
		UnsupportedClaimRate: 0.20, // Got worse (higher is bad)
	}

	report := ComputeDrift(previous, current, 0.05)
	if !report.HasDrift {
		t.Error("expected drift for unsupported claim rate increase")
	}

	foundUCR := false
	for _, c := range report.Checks {
		if c.MetricName == "unsupported_claim_rate" && c.Regressed {
			foundUCR = true
		}
	}
	if !foundUCR {
		t.Error("expected unsupported_claim_rate regression")
	}
}

func TestComputeDrift_Improvement(t *testing.T) {
	previous := EvalGateInput{RecallAtK: 0.50}
	current := EvalGateInput{RecallAtK: 0.80}

	report := ComputeDrift(previous, current, 0.05)
	if report.HasDrift {
		t.Error("improvements should not be flagged as drift")
	}
}

func TestDriftReport_Summary(t *testing.T) {
	noDrift := &DriftReport{HasDrift: false}
	if noDrift.Summary() != "No significant drift detected" {
		t.Errorf("unexpected summary: %s", noDrift.Summary())
	}

	withDrift := &DriftReport{HasDrift: true, Regressions: 2, Checks: make([]DriftCheck, 5)}
	summary := withDrift.Summary()
	if summary == "" {
		t.Error("expected non-empty drift summary")
	}
}

func TestAllAblationFeatures(t *testing.T) {
	features := AllAblationFeatures()
	if len(features) != 6 {
		t.Errorf("expected 6 features, got %d", len(features))
	}
}
