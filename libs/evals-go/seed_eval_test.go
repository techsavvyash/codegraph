package evals

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSeedEvalPerfectScore(t *testing.T) {
	// Create a temp golden file.
	dir := t.TempDir()
	goldenPath := filepath.Join(dir, "golden.yaml")
	golden := `
version: "1.0"
description: test
expected_seeds:
  - nodeKey: "a"
    name: "A"
    tier: 1
    expected_type: "api_exposed"
  - nodeKey: "b"
    name: "B"
    tier: 3
    expected_type: "topological_root"
known_false_positives: []
`
	os.WriteFile(goldenPath, []byte(golden), 0644)

	runner := NewSeedEvalRunner(goldenPath)
	result, err := runner.Evaluate([]string{"a", "b"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Precision != 1.0 {
		t.Errorf("precision = %.2f, want 1.0", result.Precision)
	}
	if result.Recall != 1.0 {
		t.Errorf("recall = %.2f, want 1.0", result.Recall)
	}
	if result.F1 != 1.0 {
		t.Errorf("F1 = %.2f, want 1.0", result.F1)
	}
}

func TestSeedEvalPartialMatch(t *testing.T) {
	dir := t.TempDir()
	goldenPath := filepath.Join(dir, "golden.yaml")
	golden := `
version: "1.0"
description: test
expected_seeds:
  - nodeKey: "a"
    name: "A"
    tier: 1
    expected_type: "api_exposed"
  - nodeKey: "b"
    name: "B"
    tier: 3
    expected_type: "topological_root"
  - nodeKey: "c"
    name: "C"
    tier: 3
    expected_type: "topological_root"
known_false_positives: []
`
	os.WriteFile(goldenPath, []byte(golden), 0644)

	runner := NewSeedEvalRunner(goldenPath)
	// Detect a, b (missing c), plus extra d.
	result, err := runner.Evaluate([]string{"a", "b", "d"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.TruePositives != 2 {
		t.Errorf("TP = %d, want 2", result.TruePositives)
	}
	if result.FalsePositives != 1 {
		t.Errorf("FP = %d, want 1", result.FalsePositives)
	}
	if result.FalseNegatives != 1 {
		t.Errorf("FN = %d, want 1", result.FalseNegatives)
	}

	// Precision = 2/3 ≈ 0.667
	if result.Precision < 0.66 || result.Precision > 0.67 {
		t.Errorf("precision = %.3f, want ~0.667", result.Precision)
	}
	// Recall = 2/3 ≈ 0.667
	if result.Recall < 0.66 || result.Recall > 0.67 {
		t.Errorf("recall = %.3f, want ~0.667", result.Recall)
	}
}

func TestSeedQualityGate(t *testing.T) {
	good := &SeedEvalResult{Precision: 0.7, Recall: 0.9}
	if err := CheckSeedQualityGate(good); err != nil {
		t.Errorf("expected pass, got: %v", err)
	}

	lowRecall := &SeedEvalResult{Precision: 0.8, Recall: 0.5}
	if err := CheckSeedQualityGate(lowRecall); err == nil {
		t.Error("expected recall gate failure")
	}

	lowPrecision := &SeedEvalResult{Precision: 0.3, Recall: 0.9}
	if err := CheckSeedQualityGate(lowPrecision); err == nil {
		t.Error("expected precision gate failure")
	}
}
