package provenance

import (
	"testing"
	"time"

	"github.com/context-maximiser/code-graph/libs/intelligence-go/contracts"
)

func validResult() *contracts.InferenceResult {
	return &contracts.InferenceResult{
		SourceKey:    "func:a.go#A",
		TargetKey:    "func:b.go#B",
		RelationType: "CALLS",
		Confidence:   0.85,
		Strategy:     "structural",
		Reasons:      []string{"co-located"},
		EvidenceRefs: []contracts.EvidenceRef{{Kind: "structural", Detail: "same package"}},
		CreatedAt:    time.Now(),
	}
}

func TestValidResultPasses(t *testing.T) {
	if err := Validate(validResult()); err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}

func TestMissingReasons(t *testing.T) {
	r := validResult()
	r.Reasons = nil
	err := Validate(r)
	if err == nil {
		t.Fatal("expected error for missing Reasons")
	}
	errs := err.(ValidationErrors)
	assertFieldError(t, errs, "Reasons")
}

func TestMissingStrategy(t *testing.T) {
	r := validResult()
	r.Strategy = ""
	err := Validate(r)
	if err == nil {
		t.Fatal("expected error for missing Strategy")
	}
	errs := err.(ValidationErrors)
	assertFieldError(t, errs, "Strategy")
}

func TestZeroCreatedAt(t *testing.T) {
	r := validResult()
	r.CreatedAt = time.Time{}
	err := Validate(r)
	if err == nil {
		t.Fatal("expected error for zero CreatedAt")
	}
	errs := err.(ValidationErrors)
	assertFieldError(t, errs, "CreatedAt")
}

func TestConfidenceNegative(t *testing.T) {
	r := validResult()
	r.Confidence = -0.1
	err := Validate(r)
	if err == nil {
		t.Fatal("expected error for negative Confidence")
	}
	errs := err.(ValidationErrors)
	assertFieldError(t, errs, "Confidence")
}

func TestConfidenceAboveOne(t *testing.T) {
	r := validResult()
	r.Confidence = 1.5
	err := Validate(r)
	if err == nil {
		t.Fatal("expected error for Confidence > 1")
	}
	errs := err.(ValidationErrors)
	assertFieldError(t, errs, "Confidence")
}

func TestEmptyEvidenceRefs(t *testing.T) {
	r := validResult()
	r.EvidenceRefs = nil
	err := Validate(r)
	if err == nil {
		t.Fatal("expected error for empty EvidenceRefs")
	}
	errs := err.(ValidationErrors)
	assertFieldError(t, errs, "EvidenceRefs")
}

func TestEvidenceRefEmptyKind(t *testing.T) {
	r := validResult()
	r.EvidenceRefs = []contracts.EvidenceRef{{Kind: "", Detail: "test"}}
	err := Validate(r)
	if err == nil {
		t.Fatal("expected error for EvidenceRef with empty Kind")
	}
	errs := err.(ValidationErrors)
	assertFieldError(t, errs, "EvidenceRefs[0].Kind")
}

func TestMissingSourceKey(t *testing.T) {
	r := validResult()
	r.SourceKey = ""
	err := Validate(r)
	if err == nil {
		t.Fatal("expected error for missing SourceKey")
	}
	errs := err.(ValidationErrors)
	assertFieldError(t, errs, "SourceKey")
}

func TestMissingTargetKey(t *testing.T) {
	r := validResult()
	r.TargetKey = ""
	err := Validate(r)
	if err == nil {
		t.Fatal("expected error for missing TargetKey")
	}
	errs := err.(ValidationErrors)
	assertFieldError(t, errs, "TargetKey")
}

func TestMultipleErrors(t *testing.T) {
	r := &contracts.InferenceResult{} // everything missing/zero
	err := Validate(r)
	if err == nil {
		t.Fatal("expected errors for completely empty result")
	}
	errs := err.(ValidationErrors)
	if len(errs) < 5 {
		t.Errorf("expected at least 5 errors, got %d: %v", len(errs), errs)
	}
}

func TestMustValidatePanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("MustValidate did not panic on invalid result")
		}
	}()
	MustValidate(&contracts.InferenceResult{})
}

func TestMustValidateValid(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("MustValidate panicked on valid result: %v", r)
		}
	}()
	MustValidate(validResult())
}

// --- ValidateDocProps tests ---

func TestValidateDocProps_Valid(t *testing.T) {
	props := map[string]any{
		"type":      "flow_summary",
		"sourceKey": "flow:api:route",
		"createdAt": "2025-01-01T00:00:00Z",
		"strategy":  "evidence_backed",
	}
	if err := ValidateDocProps(props); err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}

func TestValidateDocProps_MissingFields(t *testing.T) {
	props := map[string]any{"type": "flow_summary"}
	err := ValidateDocProps(props)
	if err == nil {
		t.Fatal("expected error for missing fields")
	}
	errs := err.(ValidationErrors)
	if len(errs) < 3 {
		t.Errorf("expected at least 3 errors, got %d", len(errs))
	}
}

func TestValidateDocProps_EmptyValues(t *testing.T) {
	props := map[string]any{
		"type":      "",
		"sourceKey": "key",
		"createdAt": "2025-01-01T00:00:00Z",
		"strategy":  "",
	}
	err := ValidateDocProps(props)
	if err == nil {
		t.Fatal("expected error for empty values")
	}
}

// --- ValidateMentionEdgeProps tests ---

func TestValidateMentionEdgeProps_Valid(t *testing.T) {
	props := map[string]any{
		"confidence": 0.8,
		"reasons":    []string{"backtick_reference"},
		"createdAt":  "2025-01-01T00:00:00Z",
		"model":      "backtick_extraction",
		"scopeId":    "main",
	}
	if err := ValidateMentionEdgeProps(props); err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}

func TestValidateMentionEdgeProps_ValidWithStrategy(t *testing.T) {
	props := map[string]any{
		"confidence": 0.8,
		"reasons":    []string{"backtick_reference"},
		"createdAt":  "2025-01-01T00:00:00Z",
		"strategy":   "backtick_extraction",
		"scopeId":    "main",
	}
	if err := ValidateMentionEdgeProps(props); err != nil {
		t.Errorf("expected nil with strategy instead of model, got %v", err)
	}
}

func TestValidateMentionEdgeProps_Missing(t *testing.T) {
	props := map[string]any{}
	err := ValidateMentionEdgeProps(props)
	if err == nil {
		t.Fatal("expected error for empty props")
	}
	errs := err.(ValidationErrors)
	// confidence, reasons, createdAt, model, scopeId = 5
	if len(errs) < 5 {
		t.Errorf("expected at least 5 errors, got %d: %v", len(errs), errs)
	}
}

func TestValidateMentionEdgeProps_MissingScopeId(t *testing.T) {
	props := map[string]any{
		"confidence": 0.8,
		"reasons":    []string{"test"},
		"createdAt":  "2025-01-01T00:00:00Z",
		"model":      "test",
	}
	err := ValidateMentionEdgeProps(props)
	if err == nil {
		t.Fatal("expected error for missing scopeId")
	}
	errs := err.(ValidationErrors)
	assertFieldError(t, errs, "scopeId")
}

func TestValidateMentionEdgeProps_BadConfidence(t *testing.T) {
	props := map[string]any{
		"confidence": 1.5,
		"reasons":    []string{"test"},
		"createdAt":  "2025-01-01T00:00:00Z",
		"model":      "test",
		"scopeId":    "main",
	}
	err := ValidateMentionEdgeProps(props)
	if err == nil {
		t.Fatal("expected error for confidence > 1")
	}
}

// --- BuildMentionEdgeProps tests ---

func TestBuildMentionEdgeProps_Valid(t *testing.T) {
	props, err := BuildMentionEdgeProps(0.85, []string{"backtick"}, "intelligent_linking", "2025-01-01T00:00:00Z", "pr-audit")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if props["confidence"] != 0.85 {
		t.Errorf("confidence = %v, want 0.85", props["confidence"])
	}
	if props["model"] != "intelligent_linking" {
		t.Errorf("model = %v, want intelligent_linking", props["model"])
	}
	if props["scopeId"] != "pr-audit" {
		t.Errorf("scopeId = %v, want pr-audit", props["scopeId"])
	}
}

func TestBuildMentionEdgeProps_EmptyStrategy(t *testing.T) {
	_, err := BuildMentionEdgeProps(0.85, []string{"backtick"}, "", "2025-01-01T00:00:00Z", "main")
	if err == nil {
		t.Fatal("expected error for empty strategy")
	}
}

func TestBuildMentionEdgeProps_EmptyScopeId(t *testing.T) {
	_, err := BuildMentionEdgeProps(0.85, []string{"backtick"}, "test", "2025-01-01T00:00:00Z", "")
	if err == nil {
		t.Fatal("expected error for empty scopeId")
	}
}

func assertFieldError(t *testing.T, errs ValidationErrors, field string) {
	t.Helper()
	for _, e := range errs {
		if e.Field == field {
			return
		}
	}
	t.Errorf("expected error for field %q, got errors: %v", field, errs)
}
