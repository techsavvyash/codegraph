package evals

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// SeedGoldenSet represents the golden set for seed detection evaluation.
type SeedGoldenSet struct {
	Version        string     `yaml:"version"`
	Description    string     `yaml:"description"`
	Seeds          []GoldenSeed `yaml:"expected_seeds"`
	FalsePositives []GoldenFP `yaml:"known_false_positives"`
}

// GoldenSeed represents an expected seed in the golden set.
type GoldenSeed struct {
	NodeKey      string `yaml:"nodeKey"`
	Name         string `yaml:"name"`
	Tier         int    `yaml:"tier"`
	ExpectedType string `yaml:"expected_type"`
}

// GoldenFP represents a known false positive in the golden set.
type GoldenFP struct {
	NodeKey string `yaml:"nodeKey"`
	Name    string `yaml:"name"`
	Reason  string `yaml:"reason"`
}

// SeedEvalResult holds precision/recall/F1 for seed detection.
type SeedEvalResult struct {
	TruePositives  int     `json:"truePositives"`
	FalsePositives int     `json:"falsePositives"`
	FalseNegatives int     `json:"falseNegatives"`
	Precision      float64 `json:"precision"`
	Recall         float64 `json:"recall"`
	F1             float64 `json:"f1"`
}

// SeedEvalRunner evaluates seed detection against a golden set.
type SeedEvalRunner struct {
	GoldenPath string
}

// NewSeedEvalRunner creates a runner pointing to the golden set file.
func NewSeedEvalRunner(goldenPath string) *SeedEvalRunner {
	return &SeedEvalRunner{GoldenPath: goldenPath}
}

// LoadGoldenSet loads and parses the golden YAML file.
func (r *SeedEvalRunner) LoadGoldenSet() (*SeedGoldenSet, error) {
	data, err := os.ReadFile(r.GoldenPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read golden set: %w", err)
	}
	var gs SeedGoldenSet
	if err := yaml.Unmarshal(data, &gs); err != nil {
		return nil, fmt.Errorf("failed to parse golden set: %w", err)
	}
	return &gs, nil
}

// Evaluate computes precision, recall, and F1 for the detected seeds
// against the golden set.
func (r *SeedEvalRunner) Evaluate(detectedNodeKeys []string) (*SeedEvalResult, error) {
	gs, err := r.LoadGoldenSet()
	if err != nil {
		return nil, err
	}

	expectedSet := make(map[string]bool, len(gs.Seeds))
	for _, s := range gs.Seeds {
		expectedSet[s.NodeKey] = true
	}

	fpSet := make(map[string]bool, len(gs.FalsePositives))
	for _, fp := range gs.FalsePositives {
		fpSet[fp.NodeKey] = true
	}

	detectedSet := make(map[string]bool, len(detectedNodeKeys))
	for _, k := range detectedNodeKeys {
		detectedSet[k] = true
	}

	tp := 0
	for k := range detectedSet {
		if expectedSet[k] {
			tp++
		}
	}

	fp := 0
	for k := range detectedSet {
		if !expectedSet[k] {
			fp++
		}
	}

	fn := 0
	for k := range expectedSet {
		if !detectedSet[k] {
			fn++
		}
	}

	result := &SeedEvalResult{
		TruePositives:  tp,
		FalsePositives: fp,
		FalseNegatives: fn,
	}

	if tp+fp > 0 {
		result.Precision = float64(tp) / float64(tp+fp)
	}
	if tp+fn > 0 {
		result.Recall = float64(tp) / float64(tp+fn)
	}
	if result.Precision+result.Recall > 0 {
		result.F1 = 2 * result.Precision * result.Recall / (result.Precision + result.Recall)
	}

	return result, nil
}

// CheckSeedQualityGate returns an error if the eval result fails the quality gate.
// Default gates: recall >= 0.8, precision >= 0.6.
func CheckSeedQualityGate(result *SeedEvalResult) error {
	if result.Recall < 0.8 {
		return fmt.Errorf("recall %.2f below threshold 0.80", result.Recall)
	}
	if result.Precision < 0.6 {
		return fmt.Errorf("precision %.2f below threshold 0.60", result.Precision)
	}
	return nil
}
