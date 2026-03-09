package verification

import (
	"fmt"
	"strings"
	"time"

	"github.com/context-maximiser/code-graph/libs/intelligence-go/contracts"
)

// PersistencePolicy defines thresholds for allowing generated content to be persisted.
type PersistencePolicy struct {
	// MaxUnsupportedClaimRate is the maximum fraction of uncited statements allowed.
	// If exceeded, the draft is rejected for persistence.
	MaxUnsupportedClaimRate float64 `json:"maxUnsupportedClaimRate"`

	// MinCitationCoverage is the minimum fraction of statements that must have valid citations.
	MinCitationCoverage float64 `json:"minCitationCoverage"`

	// RejectOnVerificationErrors rejects persistence if verification produced any errors.
	RejectOnVerificationErrors bool `json:"rejectOnVerificationErrors"`

	// MinContentWords rejects content that is too short to be useful.
	MinContentWords int `json:"minContentWords"`

	// MinPRSummaryWords enforces a stricter minimum for PR summaries.
	MinPRSummaryWords int `json:"minPRSummaryWords"`

	// MinFlowSummaryWords enforces a minimum for flow summaries.
	MinFlowSummaryWords int `json:"minFlowSummaryWords"`

	// MinDocstringWords enforces a minimum for docstring suggestions.
	MinDocstringWords int `json:"minDocstringWords"`

	// DisallowedPhrases are boilerplate phrases that indicate low-information output.
	DisallowedPhrases []string `json:"disallowedPhrases,omitempty"`
}

// DefaultPolicy returns a production-grade persistence policy.
var DefaultPolicy = PersistencePolicy{
	MaxUnsupportedClaimRate:    0.1,  // At most 10% uncited
	MinCitationCoverage:        0.85, // At least 85% cited
	RejectOnVerificationErrors: true,
	MinContentWords:            10,
	MinPRSummaryWords:          24,
	MinFlowSummaryWords:        14,
	MinDocstringWords:          8,
	DisallowedPhrases: []string{
		"ready for the next steps",
		"successfully passed",
		"critical issues identified",
		"enhance the overall",
	},
}

// LenientPolicy returns a more permissive policy for development/testing.
var LenientPolicy = PersistencePolicy{
	MaxUnsupportedClaimRate:    0.3,
	MinCitationCoverage:        0.5,
	RejectOnVerificationErrors: false,
	MinContentWords:            4,
	MinPRSummaryWords:          8,
	MinFlowSummaryWords:        8,
	MinDocstringWords:          4,
}

// PersistenceDecision is the outcome of the policy gate.
type PersistenceDecision struct {
	// Allowed indicates whether the content may be persisted as published context.
	Allowed bool `json:"allowed"`

	// Reason explains the decision.
	Reason string `json:"reason"`

	// Diagnostics contains the draft and verification details when persistence is rejected.
	Diagnostics *RejectedDraft `json:"diagnostics,omitempty"`
}

// RejectedDraft captures a failed draft for diagnostic storage.
type RejectedDraft struct {
	GenerationResult   *contracts.GenerationResult   `json:"generationResult"`
	VerificationResult *contracts.VerificationResult `json:"verificationResult"`
	PolicyViolations   []string                      `json:"policyViolations"`
	RejectedAt         time.Time                     `json:"rejectedAt"`
}

// PolicyGate evaluates whether a generation result may be persisted.
type PolicyGate struct {
	policy PersistencePolicy
}

// NewPolicyGate creates a policy gate with the given policy.
func NewPolicyGate(policy PersistencePolicy) *PolicyGate {
	return &PolicyGate{policy: policy}
}

// Evaluate checks the generation and verification results against policy.
func (pg *PolicyGate) Evaluate(gen *contracts.GenerationResult, ver *contracts.VerificationResult) PersistenceDecision {
	if gen == nil || ver == nil {
		return PersistenceDecision{
			Allowed: false,
			Reason:  "missing generation or verification result",
		}
	}

	var violations []string

	// Check verification passed
	if !ver.Passed && pg.policy.RejectOnVerificationErrors {
		violations = append(violations, "verification did not pass")
	}

	// Check unsupported claim rate
	ucr := UnsupportedClaimRate(ver)
	if ucr > pg.policy.MaxUnsupportedClaimRate {
		violations = append(violations, fmt.Sprintf(
			"unsupported claim rate %.2f exceeds maximum %.2f",
			ucr, pg.policy.MaxUnsupportedClaimRate,
		))
	}

	// Check citation coverage
	coverage := 0.0
	if ver.TotalStatements > 0 {
		coverage = float64(ver.CitedStatements) / float64(ver.TotalStatements)
	}
	if coverage < pg.policy.MinCitationCoverage {
		violations = append(violations, fmt.Sprintf(
			"citation coverage %.2f below minimum %.2f",
			coverage, pg.policy.MinCitationCoverage,
		))
	}

	wordCount := len(strings.Fields(strings.TrimSpace(gen.Content)))
	minWords := pg.minWordsForTemplate(gen.Template)
	if wordCount < minWords {
		violations = append(violations, fmt.Sprintf(
			"content too short (%d words < %d)",
			wordCount, minWords,
		))
	}

	lower := strings.ToLower(gen.Content)
	for _, phrase := range pg.policy.DisallowedPhrases {
		if phrase == "" {
			continue
		}
		if strings.Contains(lower, strings.ToLower(phrase)) {
			violations = append(violations, fmt.Sprintf("content contains generic phrase %q", phrase))
		}
	}

	if len(violations) > 0 {
		return PersistenceDecision{
			Allowed: false,
			Reason:  violations[0],
			Diagnostics: &RejectedDraft{
				GenerationResult:   gen,
				VerificationResult: ver,
				PolicyViolations:   violations,
				RejectedAt:         time.Now(),
			},
		}
	}

	return PersistenceDecision{
		Allowed: true,
		Reason:  "all policy checks passed",
	}
}

func (pg *PolicyGate) minWordsForTemplate(template string) int {
	switch template {
	case "pr_summary":
		if pg.policy.MinPRSummaryWords > 0 {
			return pg.policy.MinPRSummaryWords
		}
	case "flow_summary":
		if pg.policy.MinFlowSummaryWords > 0 {
			return pg.policy.MinFlowSummaryWords
		}
	case "docstring_suggestion":
		if pg.policy.MinDocstringWords > 0 {
			return pg.policy.MinDocstringWords
		}
	}
	if pg.policy.MinContentWords > 0 {
		return pg.policy.MinContentWords
	}
	return 1
}
