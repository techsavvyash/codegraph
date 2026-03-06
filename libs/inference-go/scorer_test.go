package inference

import (
	"context"
	"math"
	"testing"

	models "github.com/context-maximiser/code-graph/libs/core-models-go"
	"github.com/context-maximiser/code-graph/libs/intelligence-go/contracts"
)

func TestScorer_Score_ZeroFeatures(t *testing.T) {
	scorer := NewScorer()
	fv := FeatureVector{}

	score := scorer.Score(fv)
	if score >= 0.5 {
		t.Errorf("expected low score for zero features, got %f", score)
	}
}

func TestScorer_Score_MaxFeatures(t *testing.T) {
	scorer := NewScorer()
	fv := FeatureVector{
		LexicalOverlap:    1.0,
		VectorScore:       1.0,
		StructuralSupport: 1.0,
		ExplicitReference: 1.0,
		FlowMembership:    1.0,
		ExportedTarget:    1.0,
	}

	score := scorer.Score(fv)
	if score < 0.9 {
		t.Errorf("expected high score for max features, got %f", score)
	}
}

func TestScorer_Score_Monotonic(t *testing.T) {
	scorer := NewScorer()

	// More features should give higher scores
	low := scorer.Score(FeatureVector{VectorScore: 0.2})
	mid := scorer.Score(FeatureVector{VectorScore: 0.5, StructuralSupport: 0.3})
	high := scorer.Score(FeatureVector{VectorScore: 0.8, StructuralSupport: 0.7, ExplicitReference: 1.0})

	if low >= mid {
		t.Errorf("expected low(%f) < mid(%f)", low, mid)
	}
	if mid >= high {
		t.Errorf("expected mid(%f) < high(%f)", mid, high)
	}
}

func TestScorer_WithWeights(t *testing.T) {
	scorer := NewScorer()

	// Custom weights that emphasize structural support
	custom := scorer.WithWeights(ScorerWeights{
		StructuralSupport: 1.0,
	})

	fv := FeatureVector{VectorScore: 1.0, StructuralSupport: 0}
	// With only structural weight, vector score shouldn't help
	score := custom.Score(fv)
	if score >= 0.5 {
		t.Errorf("expected low score when only structural weight matters and structural=0, got %f", score)
	}
}

func TestCalibrate(t *testing.T) {
	// Very low input → near 0
	if calibrate(0.0) > 0.05 {
		t.Errorf("calibrate(0.0) should be near 0, got %f", calibrate(0.0))
	}

	// Midpoint input → ~0.5
	mid := calibrate(0.4)
	if math.Abs(mid-0.5) > 0.01 {
		t.Errorf("calibrate(0.4) should be ~0.5, got %f", mid)
	}

	// High input → near 1
	if calibrate(1.0) < 0.95 {
		t.Errorf("calibrate(1.0) should be near 1, got %f", calibrate(1.0))
	}

	// Monotonic
	for i := 0; i < 10; i++ {
		a := float64(i) / 10
		b := float64(i+1) / 10
		if calibrate(a) > calibrate(b) {
			t.Errorf("calibrate not monotonic: calibrate(%f)=%f > calibrate(%f)=%f",
				a, calibrate(a), b, calibrate(b))
		}
	}
}

func TestBuildReasons(t *testing.T) {
	fv := FeatureVector{
		ExplicitReference: 1.0,
		VectorScore:       0.8,
		StructuralSupport: 0.6,
	}

	reasons := BuildReasons(fv)
	if len(reasons) == 0 {
		t.Error("expected non-empty reasons")
	}

	// Should contain explicit_reference and vector similarity
	hasExplicit := false
	hasVector := false
	hasStructural := false
	for _, r := range reasons {
		if r == "explicit_reference" {
			hasExplicit = true
		}
		if r == "high_vector_similarity" {
			hasVector = true
		}
		if r == "strong_structural_support" {
			hasStructural = true
		}
	}
	if !hasExplicit {
		t.Error("expected explicit_reference reason")
	}
	if !hasVector {
		t.Error("expected high_vector_similarity reason")
	}
	if !hasStructural {
		t.Error("expected strong_structural_support reason")
	}
}

func TestBuildReasons_WeakSignal(t *testing.T) {
	fv := FeatureVector{} // all zeros
	reasons := BuildReasons(fv)
	if len(reasons) != 1 || reasons[0] != "weak_signal" {
		t.Errorf("expected ['weak_signal'], got %v", reasons)
	}
}

func TestBuildEvidenceRefs(t *testing.T) {
	fv := FeatureVector{
		VectorScore:       0.8,
		StructuralSupport: 0.5,
		LexicalOverlap:    0.3,
		ExplicitReference: 1.0,
	}
	candidate := contracts.RetrievalCandidate{NodeKey: "func:a"}

	refs := BuildEvidenceRefs(fv, candidate)
	if len(refs) != 4 {
		t.Errorf("expected 4 evidence refs, got %d", len(refs))
	}

	kinds := make(map[string]bool)
	for _, r := range refs {
		kinds[r.Kind] = true
	}
	for _, expected := range []string{"vector_match", "structural", "text_match"} {
		if !kinds[expected] {
			t.Errorf("expected evidence ref kind %q", expected)
		}
	}
}

// --- Mock structural evidence lookup ---

type mockEvidenceLookup struct {
	evidence map[string]*StructuralEvidence
	err      error
}

func (m *mockEvidenceLookup) LookupEvidence(_ context.Context, nodeKeys []string, _ models.ScopeContext) (map[string]*StructuralEvidence, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.evidence, nil
}

func TestLinkInferrer_Infer(t *testing.T) {
	inferrer := NewLinkInferrer()
	inferrer.WithEvidenceLookup(&mockEvidenceLookup{
		evidence: map[string]*StructuralEvidence{
			"func:a": {HasCallsEdge: true, IsExported: true},
			"func:b": {},
		},
	})

	candidates := []contracts.RetrievalCandidate{
		{NodeKey: "func:a", Score: 0.8, Source: "vector"},
		{NodeKey: "func:b", Score: 0.2, Source: "vector"},
	}

	results, err := inferrer.Infer(context.Background(), candidates, models.DefaultScope())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one result")
	}

	// Results should be sorted by confidence
	if len(results) > 1 && results[0].Confidence < results[1].Confidence {
		t.Error("expected results sorted by confidence descending")
	}

	// First result should have higher confidence (has structural evidence)
	if results[0].SourceKey != "func:a" {
		t.Errorf("expected func:a as top result, got %q", results[0].SourceKey)
	}
}

func TestLinkInferrer_Infer_Empty(t *testing.T) {
	inferrer := NewLinkInferrer()
	results, err := inferrer.Infer(context.Background(), nil, models.DefaultScope())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if results != nil {
		t.Errorf("expected nil results, got %v", results)
	}
}

func TestLinkInferrer_MinConfidenceFilter(t *testing.T) {
	inferrer := NewLinkInferrer().WithMinConfidence(0.9)

	// Very weak candidate
	candidates := []contracts.RetrievalCandidate{
		{NodeKey: "func:weak", Score: 0.01, Source: "vector"},
	}

	results, err := inferrer.Infer(context.Background(), candidates, models.DefaultScope())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results after min confidence filter, got %d", len(results))
	}
}

func TestLinkInferrer_GracefulWithoutEvidenceLookup(t *testing.T) {
	// No evidence lookup set — should still work, just without structural features
	inferrer := NewLinkInferrer()

	candidates := []contracts.RetrievalCandidate{
		{NodeKey: "func:a", Score: 0.9, Source: "vector"},
	}

	results, err := inferrer.Infer(context.Background(), candidates, models.DefaultScope())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one result")
	}
}

func TestCalibrationError(t *testing.T) {
	// Perfectly calibrated: predictions match outcomes
	predictions := []float64{0.9, 0.9, 0.1, 0.1}
	outcomes := []bool{true, true, false, false}
	ece := CalibrationError(predictions, outcomes, 10)
	if ece > 0.15 {
		t.Errorf("expected low ECE for perfect calibration, got %f", ece)
	}

	// Badly calibrated: high confidence, wrong outcomes
	badPredictions := []float64{0.9, 0.9, 0.9, 0.9}
	badOutcomes := []bool{false, false, false, false}
	badECE := CalibrationError(badPredictions, badOutcomes, 10)
	if badECE < 0.5 {
		t.Errorf("expected high ECE for bad calibration, got %f", badECE)
	}

	// Empty
	if CalibrationError(nil, nil, 10) != 0 {
		t.Error("expected 0 for empty inputs")
	}
}
