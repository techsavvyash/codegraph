package inference

import (
	"context"
	"math"
	"sort"
	"time"

	models "github.com/context-maximiser/code-graph/internal/model"
	"github.com/context-maximiser/code-graph/internal/model/contracts"
)

// ScorerWeights controls how feature signals are combined into a confidence score.
type ScorerWeights struct {
	LexicalOverlap    float64 `json:"lexicalOverlap"`
	VectorScore       float64 `json:"vectorScore"`
	StructuralSupport float64 `json:"structuralSupport"`
	ExplicitReference float64 `json:"explicitReference"`
	FlowMembership    float64 `json:"flowMembership"`
	ExportedTarget    float64 `json:"exportedTarget"`
}

// DefaultScorerWeights are calibrated for typical codegraph workloads.
var DefaultScorerWeights = ScorerWeights{
	LexicalOverlap:    0.15,
	VectorScore:       0.25,
	StructuralSupport: 0.30,
	ExplicitReference: 0.15,
	FlowMembership:    0.10,
	ExportedTarget:    0.05,
}

// Scorer computes calibrated confidence scores from feature vectors.
type Scorer struct {
	weights   ScorerWeights
	extractor *FeatureExtractor
}

// NewScorer creates a scorer with default weights.
func NewScorer() *Scorer {
	return &Scorer{
		weights:   DefaultScorerWeights,
		extractor: NewFeatureExtractor(),
	}
}

// WithWeights returns a scorer with custom weights.
func (s *Scorer) WithWeights(w ScorerWeights) *Scorer {
	return &Scorer{weights: w, extractor: s.extractor}
}

// Score computes a calibrated confidence from a feature vector.
// Returns a value in [0, 1] that is intended to be well-calibrated:
// a score of 0.8 means roughly 80% of inferences at that score are correct.
func (s *Scorer) Score(fv FeatureVector) float64 {
	w := s.weights

	// Weighted linear combination
	raw := fv.LexicalOverlap*w.LexicalOverlap +
		fv.VectorScore*w.VectorScore +
		fv.StructuralSupport*w.StructuralSupport +
		fv.ExplicitReference*w.ExplicitReference +
		fv.FlowMembership*w.FlowMembership +
		fv.ExportedTarget*w.ExportedTarget

	// Normalize by total weight
	totalWeight := w.LexicalOverlap + w.VectorScore + w.StructuralSupport +
		w.ExplicitReference + w.FlowMembership + w.ExportedTarget
	if totalWeight == 0 {
		return 0
	}
	normalized := raw / totalWeight

	// Apply sigmoid calibration to push scores toward 0 or 1,
	// reducing the "mushy middle" problem.
	return calibrate(normalized)
}

// calibrate applies a sigmoid-like mapping that:
// - Maps very low signals (< 0.1) → near 0
// - Maps moderate signals (0.3-0.5) → 0.3-0.6
// - Maps strong signals (> 0.7) → near 1.0
func calibrate(raw float64) float64 {
	// Logistic function: 1 / (1 + exp(-k*(x - midpoint)))
	// k=10, midpoint=0.4 gives good spread
	return 1.0 / (1.0 + math.Exp(-10*(raw-0.4)))
}

// BuildReasons generates human-readable reasons from a feature vector.
func BuildReasons(fv FeatureVector) []string {
	var reasons []string

	if fv.ExplicitReference > 0 {
		reasons = append(reasons, "explicit_reference")
	}
	if fv.StructuralSupport > 0.5 {
		reasons = append(reasons, "strong_structural_support")
	} else if fv.StructuralSupport > 0.2 {
		reasons = append(reasons, "structural_support")
	}
	if fv.VectorScore > 0.7 {
		reasons = append(reasons, "high_vector_similarity")
	} else if fv.VectorScore > 0.3 {
		reasons = append(reasons, "vector_similarity")
	}
	if fv.LexicalOverlap > 0.5 {
		reasons = append(reasons, "strong_lexical_overlap")
	} else if fv.LexicalOverlap > 0.2 {
		reasons = append(reasons, "lexical_overlap")
	}
	if fv.FlowMembership > 0 {
		reasons = append(reasons, "shared_flow")
	}
	if fv.ExportedTarget > 0 {
		reasons = append(reasons, "exported_symbol")
	}

	if len(reasons) == 0 {
		reasons = append(reasons, "weak_signal")
	}

	return reasons
}

// BuildEvidenceRefs creates evidence references from a feature vector and candidate.
func BuildEvidenceRefs(fv FeatureVector, candidate contracts.RetrievalCandidate) []contracts.EvidenceRef {
	var refs []contracts.EvidenceRef

	if fv.VectorScore > 0 {
		refs = append(refs, contracts.EvidenceRef{
			Kind:    "vector_match",
			NodeKey: candidate.NodeKey,
			Score:   fv.VectorScore,
		})
	}
	if fv.StructuralSupport > 0 {
		refs = append(refs, contracts.EvidenceRef{
			Kind:   "structural",
			Detail: "graph_structural_support",
			Score:  fv.StructuralSupport,
		})
	}
	if fv.LexicalOverlap > 0 {
		refs = append(refs, contracts.EvidenceRef{
			Kind:   "text_match",
			Detail: "lexical_overlap",
			Score:  fv.LexicalOverlap,
		})
	}
	if fv.ExplicitReference > 0 {
		refs = append(refs, contracts.EvidenceRef{
			Kind:   "structural",
			Detail: "explicit_reference",
			Score:  1.0,
		})
	}

	return refs
}

// StructuralEvidenceLookup resolves structural evidence for candidates.
// This is an interface so implementations can use Neo4j or mocks.
type StructuralEvidenceLookup interface {
	// LookupEvidence fetches structural evidence for the given candidate node keys.
	LookupEvidence(ctx context.Context, nodeKeys []string, scope models.ScopeContext) (map[string]*StructuralEvidence, error)
}

// LinkInferrer implements the contracts.Inferrer interface using feature scoring.
type LinkInferrer struct {
	scorer         *Scorer
	extractor      *FeatureExtractor
	evidenceLookup StructuralEvidenceLookup
	minConfidence  float64
	strategy       string
}

// NewLinkInferrer creates a link inferrer with defaults.
func NewLinkInferrer() *LinkInferrer {
	return &LinkInferrer{
		scorer:        NewScorer(),
		extractor:     NewFeatureExtractor(),
		minConfidence: 0.1,
		strategy:      "feature_scored_v1",
	}
}

// WithEvidenceLookup sets the structural evidence lookup.
func (li *LinkInferrer) WithEvidenceLookup(lookup StructuralEvidenceLookup) *LinkInferrer {
	li.evidenceLookup = lookup
	return li
}

// WithMinConfidence sets the minimum confidence threshold.
func (li *LinkInferrer) WithMinConfidence(min float64) *LinkInferrer {
	li.minConfidence = min
	return li
}

// Infer scores candidates and produces InferenceResults.
// Implements the contracts.Inferrer interface.
func (li *LinkInferrer) Infer(ctx context.Context, candidates []contracts.RetrievalCandidate, scope models.ScopeContext) ([]contracts.InferenceResult, error) {
	if len(candidates) == 0 {
		return nil, nil
	}

	// Batch-fetch structural evidence if a lookup is configured
	var evidenceMap map[string]*StructuralEvidence
	if li.evidenceLookup != nil {
		nodeKeys := make([]string, len(candidates))
		for i, c := range candidates {
			nodeKeys[i] = c.NodeKey
		}
		var err error
		evidenceMap, err = li.evidenceLookup.LookupEvidence(ctx, nodeKeys, scope)
		if err != nil {
			// Degrade gracefully: score without structural evidence
			evidenceMap = nil
		}
	}

	var results []contracts.InferenceResult
	now := time.Now()

	for _, c := range candidates {
		var evidence *StructuralEvidence
		if evidenceMap != nil {
			evidence = evidenceMap[c.NodeKey]
		}

		fv := li.extractor.Extract(c, evidence)
		confidence := li.scorer.Score(fv)

		if confidence < li.minConfidence {
			continue
		}

		results = append(results, contracts.InferenceResult{
			SourceKey:    c.NodeKey,
			TargetKey:    c.NodeKey,
			RelationType: "LINKED_TO",
			Confidence:   confidence,
			Strategy:     li.strategy,
			Reasons:      BuildReasons(fv),
			EvidenceRefs: BuildEvidenceRefs(fv, c),
			CreatedAt:    now,
		})
	}

	// Sort by confidence descending
	sort.Slice(results, func(i, j int) bool {
		return results[i].Confidence > results[j].Confidence
	})

	return results, nil
}

// CalibrationError computes the Expected Calibration Error (ECE) given predictions
// and binary outcomes. This measures how well confidence scores predict actual correctness.
// Lower is better; 0 means perfectly calibrated.
func CalibrationError(predictions []float64, outcomes []bool, numBins int) float64 {
	if len(predictions) != len(outcomes) || len(predictions) == 0 {
		return 0
	}
	if numBins <= 0 {
		numBins = 10
	}

	type binData struct {
		sumConf    float64
		sumCorrect float64
		count      int
	}

	bins := make([]binData, numBins)
	for i := range predictions {
		binIdx := int(predictions[i] * float64(numBins))
		if binIdx >= numBins {
			binIdx = numBins - 1
		}
		bins[binIdx].sumConf += predictions[i]
		if outcomes[i] {
			bins[binIdx].sumCorrect += 1
		}
		bins[binIdx].count++
	}

	ece := 0.0
	n := float64(len(predictions))
	for _, b := range bins {
		if b.count == 0 {
			continue
		}
		avgConf := b.sumConf / float64(b.count)
		avgAccuracy := b.sumCorrect / float64(b.count)
		ece += (float64(b.count) / n) * math.Abs(avgConf-avgAccuracy)
	}

	return ece
}
