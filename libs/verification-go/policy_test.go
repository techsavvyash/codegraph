package verification

import (
	"testing"
	"time"

	"github.com/context-maximiser/code-graph/libs/intelligence-go/contracts"
)

func TestPolicyGate_AllPassing(t *testing.T) {
	gate := NewPolicyGate(DefaultPolicy)

	gen := &contracts.GenerationResult{
		Content:   "This flow summary documents concrete steps, handler transitions, and cited evidence for the request lifecycle.",
		Template:  "flow_summary",
		CreatedAt: time.Now(),
		Citations: []contracts.Citation{
			{StatementIndex: 0, EvidenceRefs: []contracts.EvidenceRef{{NodeKey: "func:a"}}},
			{StatementIndex: 1, EvidenceRefs: []contracts.EvidenceRef{{NodeKey: "func:b"}}},
		},
	}

	ver := &contracts.VerificationResult{
		Passed:          true,
		TotalStatements: 2,
		CitedStatements: 2,
	}

	decision := gate.Evaluate(gen, ver)
	if !decision.Allowed {
		t.Errorf("expected allowed, got rejected: %s", decision.Reason)
	}
	if decision.Diagnostics != nil {
		t.Error("expected no diagnostics for passing result")
	}
}

func TestPolicyGate_HighUnsupportedRate(t *testing.T) {
	gate := NewPolicyGate(DefaultPolicy)

	gen := &contracts.GenerationResult{Content: "This summary includes enough words to satisfy minimum quality checks for policy evaluation."}
	ver := &contracts.VerificationResult{
		Passed:            false,
		TotalStatements:   10,
		CitedStatements:   5,
		UnsupportedClaims: []int{0, 1, 2, 3, 4}, // 50% unsupported
	}

	decision := gate.Evaluate(gen, ver)
	if decision.Allowed {
		t.Error("expected rejection for high unsupported rate")
	}
	if decision.Diagnostics == nil {
		t.Fatal("expected diagnostics on rejection")
	}
	if len(decision.Diagnostics.PolicyViolations) == 0 {
		t.Error("expected policy violations")
	}
}

func TestPolicyGate_LowCitationCoverage(t *testing.T) {
	gate := NewPolicyGate(DefaultPolicy)

	gen := &contracts.GenerationResult{Content: "This summary includes enough words to satisfy minimum quality checks for policy evaluation."}
	ver := &contracts.VerificationResult{
		Passed:          true,
		TotalStatements: 10,
		CitedStatements: 5, // 50% coverage, below 85% minimum
	}

	decision := gate.Evaluate(gen, ver)
	if decision.Allowed {
		t.Error("expected rejection for low citation coverage")
	}
}

func TestPolicyGate_VerificationErrors(t *testing.T) {
	gate := NewPolicyGate(DefaultPolicy) // RejectOnVerificationErrors = true

	gen := &contracts.GenerationResult{Content: "This summary includes enough words to satisfy minimum quality checks for policy evaluation."}
	ver := &contracts.VerificationResult{
		Passed:          false,
		TotalStatements: 2,
		CitedStatements: 2,
		Errors:          []string{"error checking func:a: timeout"},
	}

	decision := gate.Evaluate(gen, ver)
	if decision.Allowed {
		t.Error("expected rejection when verification has errors")
	}
}

func TestPolicyGate_LenientPolicy(t *testing.T) {
	gate := NewPolicyGate(LenientPolicy)

	gen := &contracts.GenerationResult{Content: "This lenient flow summary provides enough context for the relaxed policy mode."}
	ver := &contracts.VerificationResult{
		Passed:            false, // Has errors but lenient doesn't care
		TotalStatements:   10,
		CitedStatements:   6,           // 60% > 50% minimum
		UnsupportedClaims: []int{0, 1}, // 20% < 30% max
		Errors:            []string{"some error"},
	}

	decision := gate.Evaluate(gen, ver)
	if !decision.Allowed {
		t.Errorf("expected lenient policy to allow, got: %s", decision.Reason)
	}
}

func TestPolicyGate_NilInputs(t *testing.T) {
	gate := NewPolicyGate(DefaultPolicy)

	decision := gate.Evaluate(nil, nil)
	if decision.Allowed {
		t.Error("expected rejection for nil inputs")
	}

	decision = gate.Evaluate(&contracts.GenerationResult{}, nil)
	if decision.Allowed {
		t.Error("expected rejection for nil verification")
	}
}

func TestPolicyGate_DiagnosticsCapture(t *testing.T) {
	gate := NewPolicyGate(DefaultPolicy)

	gen := &contracts.GenerationResult{Content: "This flow summary includes enough concrete text to avoid length-based policy rejection.", Template: "flow_summary"}
	ver := &contracts.VerificationResult{
		Passed:            false,
		TotalStatements:   5,
		CitedStatements:   1,
		UnsupportedClaims: []int{1, 2, 3, 4},
	}

	decision := gate.Evaluate(gen, ver)
	if decision.Allowed {
		t.Fatal("expected rejection")
	}
	if decision.Diagnostics == nil {
		t.Fatal("expected diagnostics")
	}
	if decision.Diagnostics.GenerationResult != gen {
		t.Error("expected generation result in diagnostics")
	}
	if decision.Diagnostics.VerificationResult != ver {
		t.Error("expected verification result in diagnostics")
	}
	if decision.Diagnostics.RejectedAt.IsZero() {
		t.Error("expected non-zero rejection time")
	}
}

func TestPolicyGate_EdgeCase_ZeroStatements(t *testing.T) {
	gate := NewPolicyGate(DefaultPolicy)

	gen := &contracts.GenerationResult{Content: ""}
	ver := &contracts.VerificationResult{
		Passed:          true,
		TotalStatements: 0,
		CitedStatements: 0,
	}

	// Zero statements: coverage would be 0/0=0 which is below 85%
	decision := gate.Evaluate(gen, ver)
	if decision.Allowed {
		t.Error("expected rejection for zero statements (coverage below minimum)")
	}
}

func TestPolicyGate_RejectsGenericPhrase(t *testing.T) {
	gate := NewPolicyGate(DefaultPolicy)

	gen := &contracts.GenerationResult{
		Template: "pr_summary",
		Content:  "The pull request was successfully passed and is ready for the next steps in the workflow.",
	}
	ver := &contracts.VerificationResult{Passed: true, TotalStatements: 1, CitedStatements: 1}

	decision := gate.Evaluate(gen, ver)
	if decision.Allowed {
		t.Fatal("expected generic phrase to be rejected")
	}
}

func TestPolicyGate_RejectsTooShortDocstring(t *testing.T) {
	gate := NewPolicyGate(DefaultPolicy)

	gen := &contracts.GenerationResult{
		Template: "docstring_suggestion",
		Content:  "Short note.",
	}
	ver := &contracts.VerificationResult{Passed: true, TotalStatements: 1, CitedStatements: 1}

	decision := gate.Evaluate(gen, ver)
	if decision.Allowed {
		t.Fatal("expected short content to be rejected")
	}
}
