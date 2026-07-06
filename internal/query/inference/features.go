package inference

import (
	"math"
	"strings"

	"github.com/context-maximiser/code-graph/internal/model/contracts"
)

// FeatureVector holds the extracted features for a candidate pair.
// Each feature is a normalized [0, 1] signal used by the scorer.
type FeatureVector struct {
	// LexicalOverlap measures token overlap between source and target names/content.
	LexicalOverlap float64 `json:"lexicalOverlap"`

	// VectorScore is the normalized cosine similarity from vector retrieval.
	VectorScore float64 `json:"vectorScore"`

	// StructuralSupport indicates graph-structural evidence:
	// CALLS, HAS_STEP, ownership, containment.
	StructuralSupport float64 `json:"structuralSupport"`

	// ExplicitReference indicates whether the source explicitly references the target
	// (e.g., backtick references, import statements).
	ExplicitReference float64 `json:"explicitReference"`

	// FlowMembership indicates whether both source and target are part of the same flow.
	FlowMembership float64 `json:"flowMembership"`

	// ExportedTarget indicates whether the target is an exported/public symbol.
	ExportedTarget float64 `json:"exportedTarget"`
}

// FeatureExtractor computes feature vectors for candidate pairs.
type FeatureExtractor struct{}

// NewFeatureExtractor creates a new feature extractor.
func NewFeatureExtractor() *FeatureExtractor {
	return &FeatureExtractor{}
}

// Extract computes a FeatureVector from a candidate and optional structural evidence.
func (fe *FeatureExtractor) Extract(candidate contracts.RetrievalCandidate, evidence *StructuralEvidence) FeatureVector {
	fv := FeatureVector{
		VectorScore: normalizeVectorScore(candidate.Score, candidate.Source),
	}

	if evidence != nil {
		fv.StructuralSupport = computeStructuralSupport(evidence)
		fv.ExplicitReference = boolToFloat(evidence.HasExplicitReference)
		fv.FlowMembership = boolToFloat(evidence.SharedFlowCount > 0)
		fv.ExportedTarget = boolToFloat(evidence.IsExported)
	}

	return fv
}

// ExtractWithLexical computes a FeatureVector including lexical overlap between
// query text and candidate metadata.
func (fe *FeatureExtractor) ExtractWithLexical(query string, candidate contracts.RetrievalCandidate, evidence *StructuralEvidence) FeatureVector {
	fv := fe.Extract(candidate, evidence)

	// Compute lexical overlap between query and candidate name/content
	targetName, _ := candidate.Metadata["name"].(string)
	targetDesc, _ := candidate.Metadata["description"].(string)
	targetSig, _ := candidate.Metadata["signature"].(string)

	targetText := strings.Join([]string{targetName, targetDesc, targetSig}, " ")
	fv.LexicalOverlap = computeLexicalOverlap(query, targetText)

	return fv
}

// StructuralEvidence holds graph-structural signals about a candidate.
type StructuralEvidence struct {
	// Direct graph relationships
	HasCallsEdge          bool `json:"hasCallsEdge"`
	HasHasStepEdge        bool `json:"hasHasStepEdge"`
	HasContainsEdge       bool `json:"hasContainsEdge"`
	HasOwnershipEdge      bool `json:"hasOwnershipEdge"`
	HasExplicitReference  bool `json:"hasExplicitReference"`

	// Counts
	IncomingCallCount int `json:"incomingCallCount"`
	OutgoingCallCount int `json:"outgoingCallCount"`
	SharedFlowCount   int `json:"sharedFlowCount"`

	// Properties
	IsExported   bool `json:"isExported"`
	HasDocstring bool `json:"hasDocstring"`
}

// normalizeVectorScore converts raw scores from different sources to [0, 1].
// Different providers produce different score ranges:
// - OpenAI text-embedding-3-small: cosine similarity 0.0-1.0 (often 0.2-0.6 for related content)
// - Gemini: 0.5-0.9 for related content
// - BM25/fulltext: 0-20+ (unbounded)
// - RRF: 0-0.05 (very small)
func normalizeVectorScore(score float64, source string) float64 {
	switch source {
	case "vector":
		// Cosine similarity, already ~[0,1] but OpenAI tends to be lower.
		// Map 0.15-0.80 → 0-1
		return clamp((score-0.15)/0.65, 0, 1)
	case "text":
		// BM25 scores: typically 0-20. Map 0-10 → 0-1
		return clamp(score/10.0, 0, 1)
	case "graph":
		// Graph relevance scores are already [0,1]
		return clamp(score, 0, 1)
	case "hybrid":
		// RRF scores: typically 0-0.05. Map 0-0.03 → 0-1
		return clamp(score/0.03, 0, 1)
	default:
		return clamp(score, 0, 1)
	}
}

// computeStructuralSupport converts structural evidence into a [0, 1] signal.
func computeStructuralSupport(ev *StructuralEvidence) float64 {
	score := 0.0

	// Direct relationship signals (strongest)
	if ev.HasCallsEdge {
		score += 0.35
	}
	if ev.HasHasStepEdge {
		score += 0.30
	}
	if ev.HasContainsEdge {
		score += 0.25
	}
	if ev.HasOwnershipEdge {
		score += 0.20
	}

	// Call graph centrality
	if ev.IncomingCallCount > 0 {
		score += math.Min(0.15, float64(ev.IncomingCallCount)*0.03)
	}
	if ev.OutgoingCallCount > 0 {
		score += math.Min(0.10, float64(ev.OutgoingCallCount)*0.02)
	}

	// Properties
	if ev.IsExported {
		score += 0.05
	}
	if ev.HasDocstring {
		score += 0.03
	}

	return clamp(score, 0, 1)
}

// computeLexicalOverlap computes normalized token overlap between two texts.
func computeLexicalOverlap(a, b string) float64 {
	tokensA := tokenize(a)
	tokensB := tokenize(b)

	if len(tokensA) == 0 || len(tokensB) == 0 {
		return 0
	}

	setB := make(map[string]bool, len(tokensB))
	for _, t := range tokensB {
		setB[t] = true
	}

	overlap := 0
	for _, t := range tokensA {
		if setB[t] {
			overlap++
		}
	}

	// Jaccard-like: overlap / min(|A|, |B|) for asymmetric matching
	minLen := len(tokensA)
	if len(tokensB) < minLen {
		minLen = len(tokensB)
	}

	return float64(overlap) / float64(minLen)
}

// tokenize splits text into lowercase tokens, splitting on non-alphanumeric characters
// and camelCase boundaries (e.g., "HandleUserRequest" → ["handle", "user", "request"]).
func tokenize(text string) []string {
	if text == "" {
		return nil
	}

	// First pass: split camelCase boundaries by inserting a separator before uppercase letters
	// that follow lowercase letters (e.g., "HandleUser" → "Handle User")
	var expanded strings.Builder
	runes := []rune(text)
	for i, r := range runes {
		if i > 0 && r >= 'A' && r <= 'Z' {
			prev := runes[i-1]
			if prev >= 'a' && prev <= 'z' {
				expanded.WriteRune(' ')
			}
		}
		expanded.WriteRune(r)
	}

	lower := strings.ToLower(expanded.String())

	// Split on non-alphanumeric
	var tokens []string
	var current strings.Builder
	for _, r := range lower {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			current.WriteRune(r)
		} else {
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
		}
	}
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}

	// Filter out very short tokens
	var filtered []string
	for _, t := range tokens {
		if len(t) >= 2 {
			filtered = append(filtered, t)
		}
	}

	return filtered
}

func clamp(v, min, max float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func boolToFloat(b bool) float64 {
	if b {
		return 1.0
	}
	return 0.0
}
