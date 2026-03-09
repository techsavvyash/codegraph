package provenance

import (
	"fmt"
	"strings"

	"github.com/context-maximiser/code-graph/libs/intelligence-go/contracts"
)

// RequiredFields lists the fields that must be populated on every InferenceResult
// before it can be persisted.
var RequiredFields = []string{
	"Confidence",
	"Reasons",
	"CreatedAt",
	"Strategy",
	"EvidenceRefs",
}

// ValidationError describes a provenance validation failure.
type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func (e *ValidationError) Error() string {
	return e.Field + ": " + e.Message
}

// ValidationErrors is a collection of validation errors.
type ValidationErrors []ValidationError

func (ve ValidationErrors) Error() string {
	if len(ve) == 0 {
		return "no errors"
	}
	msgs := make([]string, len(ve))
	for i, e := range ve {
		msgs[i] = e.Error()
	}
	return strings.Join(msgs, "; ")
}

// Validate checks that an InferenceResult has all mandatory provenance fields.
// Returns nil if valid, or ValidationErrors listing all missing/invalid fields.
func Validate(r *contracts.InferenceResult) error {
	var errs ValidationErrors

	if r.Confidence < 0 || r.Confidence > 1 {
		errs = append(errs, ValidationError{Field: "Confidence", Message: "must be between 0 and 1"})
	}
	if len(r.Reasons) == 0 {
		errs = append(errs, ValidationError{Field: "Reasons", Message: "must not be empty"})
	}
	if r.CreatedAt.IsZero() {
		errs = append(errs, ValidationError{Field: "CreatedAt", Message: "must not be zero"})
	}
	if r.Strategy == "" {
		errs = append(errs, ValidationError{Field: "Strategy", Message: "must not be empty"})
	}
	if len(r.EvidenceRefs) == 0 {
		errs = append(errs, ValidationError{Field: "EvidenceRefs", Message: "must not be empty"})
	}
	for i, ref := range r.EvidenceRefs {
		if ref.Kind == "" {
			errs = append(errs, ValidationError{
				Field:   fmt.Sprintf("EvidenceRefs[%d].Kind", i),
				Message: "must not be empty",
			})
		}
	}
	if r.SourceKey == "" {
		errs = append(errs, ValidationError{Field: "SourceKey", Message: "must not be empty"})
	}
	if r.TargetKey == "" {
		errs = append(errs, ValidationError{Field: "TargetKey", Message: "must not be empty"})
	}

	if len(errs) > 0 {
		return errs
	}
	return nil
}

// MustValidate panics if validation fails.
func MustValidate(r *contracts.InferenceResult) {
	if err := Validate(r); err != nil {
		panic("provenance: validation failed: " + err.Error())
	}
}

// GeneratedDocFields lists the provenance fields required on every GeneratedDoc
// write before it can be persisted.
var GeneratedDocFields = []string{
	"type",
	"sourceKey",
	"createdAt",
	"strategy",
}

// ValidateDocProps checks that a GeneratedDoc property map has all mandatory
// provenance fields populated. Returns nil if valid.
func ValidateDocProps(props map[string]any) error {
	var errs ValidationErrors

	for _, field := range GeneratedDocFields {
		v, ok := props[field]
		if !ok {
			errs = append(errs, ValidationError{Field: field, Message: "must be present"})
			continue
		}
		if s, ok := v.(string); ok && s == "" {
			errs = append(errs, ValidationError{Field: field, Message: "must not be empty"})
		}
	}

	if len(errs) > 0 {
		return errs
	}
	return nil
}

// ValidateMentionEdgeProps checks that a MENTIONS edge property map has all
// mandatory provenance fields (scope, scopeId, confidence, reasons, createdAt,
// strategy, evidenceRefs).
func ValidateMentionEdgeProps(props map[string]any) error {
	var errs ValidationErrors

	if _, ok := props["confidence"]; !ok {
		errs = append(errs, ValidationError{Field: "confidence", Message: "must be present"})
	} else if c, ok := props["confidence"].(float64); ok && (c < 0 || c > 1) {
		errs = append(errs, ValidationError{Field: "confidence", Message: "must be between 0 and 1"})
	}

	for _, field := range []string{"reasons", "createdAt"} {
		v, ok := props[field]
		if !ok {
			errs = append(errs, ValidationError{Field: field, Message: "must be present"})
			continue
		}
		switch val := v.(type) {
		case string:
			if val == "" {
				errs = append(errs, ValidationError{Field: field, Message: "must not be empty"})
			}
		case []string:
			if len(val) == 0 {
				errs = append(errs, ValidationError{Field: field, Message: "must not be empty"})
			}
		}
	}

	if !hasNonEmptyString(props, "strategy") {
		errs = append(errs, ValidationError{Field: "strategy", Message: "must be present"})
	}

	if !hasNonEmptyString(props, "scope") {
		errs = append(errs, ValidationError{Field: "scope", Message: "must be present"})
	}

	// scopeId must be present and non-empty.
	if !hasNonEmptyString(props, "scopeId") {
		errs = append(errs, ValidationError{Field: "scopeId", Message: "must be present"})
	}

	v, ok := props["evidenceRefs"]
	if !ok {
		errs = append(errs, ValidationError{Field: "evidenceRefs", Message: "must be present"})
	} else {
		switch refs := v.(type) {
		case []string:
			if len(refs) == 0 {
				errs = append(errs, ValidationError{Field: "evidenceRefs", Message: "must not be empty"})
			}
		case []any:
			if len(refs) == 0 {
				errs = append(errs, ValidationError{Field: "evidenceRefs", Message: "must not be empty"})
			}
		default:
			errs = append(errs, ValidationError{Field: "evidenceRefs", Message: "must be a list"})
		}
	}

	if len(errs) > 0 {
		return errs
	}
	return nil
}

func scopeFromScopeID(scopeID string) string {
	if scopeID == "" || scopeID == "main" {
		return "main"
	}
	if strings.HasPrefix(scopeID, "pr-") {
		return "pr"
	}
	return "main"
}

// hasNonEmptyString returns true if props[key] is a non-empty string.
func hasNonEmptyString(props map[string]any, key string) bool {
	v, ok := props[key]
	if !ok {
		return false
	}
	s, ok := v.(string)
	return ok && s != ""
}

// BuildMentionEdgeProps constructs a validated MENTIONS edge property map.
// Returns an error if any required field is missing or invalid.
func BuildMentionEdgeProps(confidence float64, reasons []string, strategy, createdAt, scopeId string, evidenceRefs []string) (map[string]any, error) {
	props := map[string]any{
		"confidence":   confidence,
		"reasons":      reasons,
		"strategy":     strategy,
		"model":        strategy, // retained for backward compatibility with existing readers
		"createdAt":    createdAt,
		"scope":        scopeFromScopeID(scopeId),
		"scopeId":      scopeId,
		"evidenceRefs": evidenceRefs,
	}
	if err := ValidateMentionEdgeProps(props); err != nil {
		return nil, err
	}
	return props, nil
}
