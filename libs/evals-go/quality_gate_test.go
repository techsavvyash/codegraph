package evals

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestCheckQualityGate_AllPassing(t *testing.T) {
	input := EvalGateInput{
		RecallAtK:            0.80,
		NDCG:                 0.70,
		MRR:                  0.60,
		PrecisionAtK:         0.40,
		LinkingPrecision:     0.90,
		CitationCoverage:     0.95,
		UnsupportedClaimRate: 0.05,
	}

	result := CheckQualityGate(input, DefaultThresholds)
	if !result.Passed {
		t.Errorf("expected all gates to pass, got violations: %v", result.Violations)
	}
}

func TestCheckQualityGate_RecallFailing(t *testing.T) {
	input := EvalGateInput{
		RecallAtK:            0.30, // Below 0.60 threshold
		NDCG:                 0.70,
		MRR:                  0.60,
		PrecisionAtK:         0.40,
		LinkingPrecision:     0.90,
		CitationCoverage:     0.95,
		UnsupportedClaimRate: 0.05,
	}

	result := CheckQualityGate(input, DefaultThresholds)
	if result.Passed {
		t.Error("expected gate to fail on low recall")
	}
	if len(result.Violations) != 1 {
		t.Fatalf("expected 1 violation, got %d", len(result.Violations))
	}
	if result.Violations[0].Metric != "recall@k" {
		t.Errorf("expected recall@k violation, got %s", result.Violations[0].Metric)
	}
	if result.Violations[0].Direction != "below" {
		t.Errorf("expected direction 'below', got %s", result.Violations[0].Direction)
	}
}

func TestCheckQualityGate_UnsupportedClaimRateFailing(t *testing.T) {
	input := EvalGateInput{
		RecallAtK:            0.80,
		NDCG:                 0.70,
		MRR:                  0.60,
		PrecisionAtK:         0.40,
		LinkingPrecision:     0.90,
		CitationCoverage:     0.95,
		UnsupportedClaimRate: 0.25, // Above 0.10 threshold
	}

	result := CheckQualityGate(input, DefaultThresholds)
	if result.Passed {
		t.Error("expected gate to fail on high unsupported claim rate")
	}
	found := false
	for _, v := range result.Violations {
		if v.Metric == "unsupported_claim_rate" && v.Direction == "above" {
			found = true
		}
	}
	if !found {
		t.Error("expected unsupported_claim_rate violation with direction 'above'")
	}
}

func TestCheckQualityGate_MultipleFailures(t *testing.T) {
	input := EvalGateInput{} // All zeros
	result := CheckQualityGate(input, DefaultThresholds)
	if result.Passed {
		t.Error("expected gate to fail with all zeros")
	}
	// Should have violations for all "minimum" metrics that have non-zero thresholds
	if len(result.Violations) < 5 {
		t.Errorf("expected at least 5 violations, got %d", len(result.Violations))
	}
}

func TestCheckQualityGate_StrictThresholds(t *testing.T) {
	// Passes default but fails strict
	input := EvalGateInput{
		RecallAtK:            0.65,
		NDCG:                 0.55,
		MRR:                  0.45,
		PrecisionAtK:         0.25,
		LinkingPrecision:     0.75,
		CitationCoverage:     0.90,
		UnsupportedClaimRate: 0.08,
	}

	defaultResult := CheckQualityGate(input, DefaultThresholds)
	if !defaultResult.Passed {
		t.Error("expected to pass default thresholds")
	}

	strictResult := CheckQualityGate(input, StrictThresholds)
	if strictResult.Passed {
		t.Error("expected to fail strict thresholds")
	}
}

func TestCheckQualityGate_ZeroThresholdsSkipped(t *testing.T) {
	// Zero thresholds should not trigger violations
	input := EvalGateInput{RecallAtK: 0}
	result := CheckQualityGate(input, QualityThresholds{})
	if !result.Passed {
		t.Error("expected zero thresholds to pass")
	}
}

func TestGateResult_Summary(t *testing.T) {
	pass := GateResult{Passed: true}
	if pass.Summary() != "PASS: all quality gates met" {
		t.Errorf("unexpected pass summary: %s", pass.Summary())
	}

	fail := GateResult{
		Passed: false,
		Violations: []GateViolation{
			{Metric: "recall@k", Actual: 0.3, Threshold: 0.6, Direction: "below"},
		},
	}
	summary := fail.Summary()
	if summary == "" {
		t.Error("expected non-empty fail summary")
	}
}

func TestEvalGateInputFromRun(t *testing.T) {
	run := &EvalRun{
		Aggregate: AggregateMetrics{
			MeanRecallAtK:    0.75,
			MeanNDCG:         0.60,
			MeanMRR:          0.50,
			MeanPrecisionAtK: 0.30,
		},
	}

	input := EvalGateInputFromRun(run)
	if input.RecallAtK != 0.75 {
		t.Errorf("expected RecallAtK 0.75, got %f", input.RecallAtK)
	}

	// Nil run
	nilInput := EvalGateInputFromRun(nil)
	if nilInput.RecallAtK != 0 {
		t.Error("expected zero for nil run")
	}
}

func TestQualityGateEnforcement(t *testing.T) {
	// Verify golden datasets are structurally valid
	datasets := []string{
		"testdata/linking_golden.yaml",
		"testdata/flow_derivation_golden.yaml",
		"testdata/citation_validity_golden.yaml",
	}
	for _, path := range datasets {
		ds, err := LoadDataset(path)
		if err != nil {
			t.Fatalf("LoadDataset(%s): %v", path, err)
		}
		categories := make(map[string]bool)
		for _, q := range ds.Queries {
			categories[q.Category] = true
			if len(q.Expected) == 0 {
				t.Errorf("%s: query %s has no expected results", path, q.ID)
			}
		}
		if len(categories) < 3 {
			t.Errorf("%s: covers %d domains, want >= 3", path, len(categories))
		}
	}

	// Verify DefaultThresholds are non-zero (catch accidental zeroing)
	if DefaultThresholds.MinRecallAtK == 0 {
		t.Error("DefaultThresholds.MinRecallAtK is zero")
	}
	if DefaultThresholds.MinNDCG == 0 {
		t.Error("DefaultThresholds.MinNDCG is zero")
	}
	if DefaultThresholds.MinLinkingPrecision == 0 {
		t.Error("DefaultThresholds.MinLinkingPrecision is zero")
	}
	if DefaultThresholds.MinCitationCoverage == 0 {
		t.Error("DefaultThresholds.MinCitationCoverage is zero")
	}
	if DefaultThresholds.MaxUnsupportedClaimRate == 0 {
		t.Error("DefaultThresholds.MaxUnsupportedClaimRate is zero")
	}

	// Gate passes with above-threshold input
	passing := EvalGateInput{
		RecallAtK:            0.80,
		NDCG:                 0.70,
		MRR:                  0.60,
		PrecisionAtK:         0.40,
		LinkingPrecision:     0.90,
		CitationCoverage:     0.95,
		UnsupportedClaimRate: 0.05,
	}
	result := CheckQualityGate(passing, DefaultThresholds)
	if !result.Passed {
		t.Errorf("expected passing input to pass gate, got violations: %v", result.Summary())
	}

	// Gate fails with all-zero input
	failing := EvalGateInput{}
	result = CheckQualityGate(failing, DefaultThresholds)
	if result.Passed {
		t.Error("expected all-zero input to fail gate")
	}
	if len(result.Violations) < 5 {
		t.Errorf("expected >= 5 violations for all-zero input, got %d", len(result.Violations))
	}

	// Gate fails with single metric violation
	singleFail := EvalGateInput{
		RecallAtK:            0.80,
		NDCG:                 0.70,
		MRR:                  0.60,
		PrecisionAtK:         0.40,
		LinkingPrecision:     0.90,
		CitationCoverage:     0.95,
		UnsupportedClaimRate: 0.50, // way above 0.10 threshold
	}
	result = CheckQualityGate(singleFail, DefaultThresholds)
	if result.Passed {
		t.Error("expected gate to fail on high unsupported claim rate")
	}
	found := false
	for _, v := range result.Violations {
		if v.Metric == "unsupported_claim_rate" {
			found = true
		}
	}
	if !found {
		t.Error("expected unsupported_claim_rate violation")
	}
}

func TestWriteMetricsArtifact(t *testing.T) {
	input := EvalGateInput{
		RecallAtK:            0.75,
		NDCG:                 0.65,
		MRR:                  0.55,
		PrecisionAtK:         0.35,
		LinkingPrecision:     0.80,
		CitationCoverage:     0.90,
		UnsupportedClaimRate: 0.08,
	}
	result := CheckQualityGate(input, DefaultThresholds)

	var buf bytes.Buffer
	err := WriteGateReport(&buf, input, DefaultThresholds, result)
	if err != nil {
		t.Fatalf("WriteGateReport: %v", err)
	}

	// Verify valid JSON
	var report GateReport
	if err := json.Unmarshal(buf.Bytes(), &report); err != nil {
		t.Fatalf("invalid JSON output: %v", err)
	}

	// Verify fields
	if !report.Result.Passed {
		t.Error("expected report to show passing result")
	}
	if report.Input.RecallAtK != 0.75 {
		t.Errorf("expected RecallAtK 0.75, got %f", report.Input.RecallAtK)
	}
	if report.Thresholds.MinRecallAtK != DefaultThresholds.MinRecallAtK {
		t.Error("thresholds not preserved in report")
	}
	if report.Timestamp.IsZero() {
		t.Error("expected non-zero timestamp")
	}

	// Verify failing report captures violations
	failInput := EvalGateInput{}
	failResult := CheckQualityGate(failInput, DefaultThresholds)
	buf.Reset()
	if err := WriteGateReport(&buf, failInput, DefaultThresholds, failResult); err != nil {
		t.Fatalf("WriteGateReport (fail): %v", err)
	}
	var failReport GateReport
	if err := json.Unmarshal(buf.Bytes(), &failReport); err != nil {
		t.Fatalf("invalid JSON for fail report: %v", err)
	}
	if failReport.Result.Passed {
		t.Error("expected failing report")
	}
	if len(failReport.Result.Violations) == 0 {
		t.Error("expected violations in fail report")
	}
}

func TestLoadGoldenDatasets(t *testing.T) {
	datasets := []string{
		"testdata/linking_golden.yaml",
		"testdata/flow_derivation_golden.yaml",
		"testdata/citation_validity_golden.yaml",
	}

	for _, path := range datasets {
		ds, err := LoadDataset(path)
		if err != nil {
			t.Fatalf("LoadDataset(%s): %v", path, err)
		}
		if len(ds.Queries) == 0 {
			t.Errorf("%s has no queries", path)
		}

		// Verify 3 domains
		categories := make(map[string]int)
		for _, q := range ds.Queries {
			categories[q.Category]++
		}
		if len(categories) < 3 {
			t.Errorf("%s covers %d domains, want at least 3: %v", path, len(categories), categories)
		}

		// Verify each query has expected results
		for _, q := range ds.Queries {
			if len(q.Expected) == 0 {
				t.Errorf("%s: query %s has no expected results", path, q.ID)
			}
			if q.ID == "" {
				t.Errorf("%s: query has empty ID", path)
			}
		}
	}
}
