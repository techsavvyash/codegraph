package evals

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

// QualityThresholds defines the minimum acceptable values for each metric.
// If any metric falls below its threshold, the quality gate fails.
type QualityThresholds struct {
	MinRecallAtK          float64 `json:"minRecallAtK" yaml:"minRecallAtK"`
	MinNDCG               float64 `json:"minNDCG" yaml:"minNDCG"`
	MinMRR                float64 `json:"minMRR" yaml:"minMRR"`
	MinPrecisionAtK       float64 `json:"minPrecisionAtK" yaml:"minPrecisionAtK"`
	MinLinkingPrecision   float64 `json:"minLinkingPrecision" yaml:"minLinkingPrecision"`
	MinCitationCoverage   float64 `json:"minCitationCoverage" yaml:"minCitationCoverage"`
	MaxUnsupportedClaimRate float64 `json:"maxUnsupportedClaimRate" yaml:"maxUnsupportedClaimRate"`
}

// DefaultThresholds returns production-grade quality gate thresholds.
var DefaultThresholds = QualityThresholds{
	MinRecallAtK:            0.60,
	MinNDCG:                 0.50,
	MinMRR:                  0.40,
	MinPrecisionAtK:         0.20,
	MinLinkingPrecision:     0.70,
	MinCitationCoverage:     0.85,
	MaxUnsupportedClaimRate: 0.10,
}

// StrictThresholds returns tighter thresholds for high-quality enforcement.
var StrictThresholds = QualityThresholds{
	MinRecallAtK:            0.75,
	MinNDCG:                 0.65,
	MinMRR:                  0.55,
	MinPrecisionAtK:         0.30,
	MinLinkingPrecision:     0.85,
	MinCitationCoverage:     0.95,
	MaxUnsupportedClaimRate: 0.05,
}

// GateViolation describes a single threshold violation.
type GateViolation struct {
	Metric    string  `json:"metric"`
	Threshold float64 `json:"threshold"`
	Actual    float64 `json:"actual"`
	Direction string  `json:"direction"` // "below" or "above"
}

func (v GateViolation) String() string {
	return fmt.Sprintf("%s: actual=%.4f %s threshold=%.4f", v.Metric, v.Actual, v.Direction, v.Threshold)
}

// GateResult is the outcome of a quality gate evaluation.
type GateResult struct {
	Passed     bool            `json:"passed"`
	Violations []GateViolation `json:"violations,omitempty"`
}

// Summary returns a human-readable summary of the gate result.
func (r GateResult) Summary() string {
	if r.Passed {
		return "PASS: all quality gates met"
	}
	var lines []string
	lines = append(lines, fmt.Sprintf("FAIL: %d quality gate(s) violated", len(r.Violations)))
	for _, v := range r.Violations {
		lines = append(lines, "  - "+v.String())
	}
	return strings.Join(lines, "\n")
}

// EvalGateInput holds the metrics to be evaluated against thresholds.
type EvalGateInput struct {
	RecallAtK            float64 `json:"recallAtK"`
	NDCG                 float64 `json:"ndcg"`
	MRR                  float64 `json:"mrr"`
	PrecisionAtK         float64 `json:"precisionAtK"`
	LinkingPrecision     float64 `json:"linkingPrecision"`
	CitationCoverage     float64 `json:"citationCoverage"`
	UnsupportedClaimRate float64 `json:"unsupportedClaimRate"`
}

// EvalGateInputFromRun extracts gate input from an EvalRun's aggregate metrics.
func EvalGateInputFromRun(run *EvalRun) EvalGateInput {
	if run == nil {
		return EvalGateInput{}
	}
	return EvalGateInput{
		RecallAtK:    run.Aggregate.MeanRecallAtK,
		NDCG:         run.Aggregate.MeanNDCG,
		MRR:          run.Aggregate.MeanMRR,
		PrecisionAtK: run.Aggregate.MeanPrecisionAtK,
	}
}

// CheckQualityGate evaluates all metrics against the given thresholds.
func CheckQualityGate(input EvalGateInput, thresholds QualityThresholds) GateResult {
	var violations []GateViolation

	// "minimum" checks: actual must be >= threshold
	minChecks := []struct {
		metric    string
		actual    float64
		threshold float64
	}{
		{"recall@k", input.RecallAtK, thresholds.MinRecallAtK},
		{"ndcg", input.NDCG, thresholds.MinNDCG},
		{"mrr", input.MRR, thresholds.MinMRR},
		{"precision@k", input.PrecisionAtK, thresholds.MinPrecisionAtK},
		{"linking_precision", input.LinkingPrecision, thresholds.MinLinkingPrecision},
		{"citation_coverage", input.CitationCoverage, thresholds.MinCitationCoverage},
	}

	for _, check := range minChecks {
		if check.threshold > 0 && check.actual < check.threshold {
			violations = append(violations, GateViolation{
				Metric:    check.metric,
				Threshold: check.threshold,
				Actual:    check.actual,
				Direction: "below",
			})
		}
	}

	// "maximum" checks: actual must be <= threshold
	if thresholds.MaxUnsupportedClaimRate > 0 && input.UnsupportedClaimRate > thresholds.MaxUnsupportedClaimRate {
		violations = append(violations, GateViolation{
			Metric:    "unsupported_claim_rate",
			Threshold: thresholds.MaxUnsupportedClaimRate,
			Actual:    input.UnsupportedClaimRate,
			Direction: "above",
		})
	}

	return GateResult{
		Passed:     len(violations) == 0,
		Violations: violations,
	}
}

// GateReport is the JSON-serializable report written by CI.
type GateReport struct {
	Timestamp  time.Time        `json:"timestamp"`
	Input      EvalGateInput    `json:"input"`
	Thresholds QualityThresholds `json:"thresholds"`
	Result     GateResult       `json:"result"`
}

// WriteGateReport serializes a quality gate evaluation to JSON.
func WriteGateReport(w io.Writer, input EvalGateInput, thresholds QualityThresholds, result GateResult) error {
	report := GateReport{
		Timestamp:  time.Now().UTC(),
		Input:      input,
		Thresholds: thresholds,
		Result:     result,
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(report)
}
