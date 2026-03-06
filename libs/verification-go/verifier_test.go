package verification

import (
	"context"
	"fmt"
	"testing"

	models "github.com/context-maximiser/code-graph/libs/core-models-go"
	"github.com/context-maximiser/code-graph/libs/intelligence-go/contracts"
)

// --- Mock implementations ---

type mockResolver struct {
	existing map[string]bool
	err      error
}

func (m *mockResolver) NodeExists(_ context.Context, nodeKey string, _ models.ScopeContext) (bool, error) {
	if m.err != nil {
		return false, m.err
	}
	return m.existing[nodeKey], nil
}

// --- Verifier Tests ---

func TestVerifier_Verify_AllValid(t *testing.T) {
	resolver := &mockResolver{
		existing: map[string]bool{"func:a": true, "func:b": true},
	}
	v := NewVerifier(resolver)

	result := &contracts.GenerationResult{
		Citations: []contracts.Citation{
			{StatementIndex: 0, EvidenceRefs: []contracts.EvidenceRef{{Kind: "citation", NodeKey: "func:a"}}},
			{StatementIndex: 1, EvidenceRefs: []contracts.EvidenceRef{{Kind: "citation", NodeKey: "func:b"}}},
		},
	}

	vr, err := v.Verify(context.Background(), result, models.DefaultScope())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !vr.Passed {
		t.Error("expected verification to pass")
	}
	if vr.TotalStatements != 2 {
		t.Errorf("expected 2 total statements, got %d", vr.TotalStatements)
	}
	if vr.CitedStatements != 2 {
		t.Errorf("expected 2 cited statements, got %d", vr.CitedStatements)
	}
	if len(vr.UnsupportedClaims) != 0 {
		t.Errorf("expected 0 unsupported claims, got %d", len(vr.UnsupportedClaims))
	}
}

func TestVerifier_Verify_UncitedStatements(t *testing.T) {
	resolver := &mockResolver{existing: map[string]bool{"func:a": true}}
	v := NewVerifier(resolver)

	result := &contracts.GenerationResult{
		Citations: []contracts.Citation{
			{StatementIndex: 0, EvidenceRefs: []contracts.EvidenceRef{{Kind: "citation", NodeKey: "func:a"}}},
			{StatementIndex: 1, EvidenceRefs: nil}, // uncited
		},
	}

	vr, err := v.Verify(context.Background(), result, models.DefaultScope())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vr.Passed {
		t.Error("expected verification to fail")
	}
	if len(vr.UnsupportedClaims) != 1 || vr.UnsupportedClaims[0] != 1 {
		t.Errorf("expected unsupported claim at index 1, got %v", vr.UnsupportedClaims)
	}
}

func TestVerifier_Verify_MissingNodeInScope(t *testing.T) {
	resolver := &mockResolver{
		existing: map[string]bool{"func:a": true},
		// func:b does NOT exist in scope
	}
	v := NewVerifier(resolver)

	result := &contracts.GenerationResult{
		Citations: []contracts.Citation{
			{StatementIndex: 0, EvidenceRefs: []contracts.EvidenceRef{{Kind: "citation", NodeKey: "func:a"}}},
			{StatementIndex: 1, EvidenceRefs: []contracts.EvidenceRef{{Kind: "citation", NodeKey: "func:b"}}},
		},
	}

	vr, err := v.Verify(context.Background(), result, models.DefaultScope())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vr.Passed {
		t.Error("expected verification to fail")
	}
	if vr.CitedStatements != 1 {
		t.Errorf("expected 1 cited, got %d", vr.CitedStatements)
	}
	if len(vr.Errors) == 0 {
		t.Error("expected errors about missing node")
	}
}

func TestVerifier_Verify_ResolverError(t *testing.T) {
	resolver := &mockResolver{err: fmt.Errorf("db error")}
	v := NewVerifier(resolver)

	result := &contracts.GenerationResult{
		Citations: []contracts.Citation{
			{StatementIndex: 0, EvidenceRefs: []contracts.EvidenceRef{{Kind: "citation", NodeKey: "func:a"}}},
		},
	}

	vr, err := v.Verify(context.Background(), result, models.DefaultScope())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vr.Passed {
		t.Error("expected verification to fail on resolver error")
	}
	if len(vr.Errors) == 0 {
		t.Error("expected errors from resolver failure")
	}
}

func TestVerifier_Verify_NilResult(t *testing.T) {
	v := NewVerifier(nil)
	vr, err := v.Verify(context.Background(), nil, models.DefaultScope())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !vr.Passed {
		t.Error("expected passed for nil result")
	}
}

func TestVerifier_Verify_NilResolver(t *testing.T) {
	// Without resolver, all cited statements should pass
	v := NewVerifier(nil)
	result := &contracts.GenerationResult{
		Citations: []contracts.Citation{
			{StatementIndex: 0, EvidenceRefs: []contracts.EvidenceRef{{Kind: "citation", NodeKey: "func:a"}}},
		},
	}

	vr, err := v.Verify(context.Background(), result, models.DefaultScope())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !vr.Passed {
		t.Error("expected passed without resolver")
	}
}

func TestUnsupportedClaimRate(t *testing.T) {
	vr := &contracts.VerificationResult{
		TotalStatements:   4,
		UnsupportedClaims: []int{1, 3},
	}
	rate := UnsupportedClaimRate(vr)
	if rate != 0.5 {
		t.Errorf("expected 0.5, got %f", rate)
	}
}

func TestUnsupportedClaimRate_Empty(t *testing.T) {
	if UnsupportedClaimRate(nil) != 0 {
		t.Error("expected 0 for nil")
	}
	if UnsupportedClaimRate(&contracts.VerificationResult{}) != 0 {
		t.Error("expected 0 for empty")
	}
}

func TestVerifier_ImplementsInterface(t *testing.T) {
	var _ contracts.Verifier = (*Verifier)(nil)
}
