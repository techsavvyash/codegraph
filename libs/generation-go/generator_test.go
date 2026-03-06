package generation

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/context-maximiser/code-graph/libs/intelligence-go/contracts"
)

// --- Mock implementations ---

type mockLLM struct {
	response string
	err      error
}

func (m *mockLLM) Complete(_ context.Context, _ string, _ int) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	return m.response, nil
}

func (m *mockLLM) ModelName() string {
	return "mock-model"
}

type mockParser struct {
	statements []Statement
	err        error
}

func (m *mockParser) Parse(_ string) ([]Statement, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.statements, nil
}

// --- Tests ---

func TestGenerator_Generate_Basic(t *testing.T) {
	llm := &mockLLM{response: "raw output"}
	parser := &mockParser{
		statements: []Statement{
			{Text: "Function A calls Function B.", CitationRefs: []string{"func:a", "func:b"}},
			{Text: "Function B processes data.", CitationRefs: []string{"func:b"}},
		},
	}

	gen := NewGenerator(llm, parser)
	bundle := &contracts.ContextBundle{
		Anchors: []contracts.RetrievalCandidate{
			{NodeKey: "func:a"},
			{NodeKey: "func:b"},
		},
		Template:  "flow_summary",
		MaxTokens: 1000,
	}

	result, err := gen.Generate(context.Background(), bundle)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Model != "mock-model" {
		t.Errorf("expected model mock-model, got %s", result.Model)
	}
	if result.Template != "flow_summary" {
		t.Errorf("expected template flow_summary, got %s", result.Template)
	}
	if len(result.Citations) != 2 {
		t.Fatalf("expected 2 citations, got %d", len(result.Citations))
	}

	// First statement should cite func:a and func:b
	if len(result.Citations[0].EvidenceRefs) != 2 {
		t.Errorf("expected 2 refs for first citation, got %d", len(result.Citations[0].EvidenceRefs))
	}
}

func TestGenerator_Generate_NilBundle(t *testing.T) {
	gen := NewGenerator(&mockLLM{}, &mockParser{})
	_, err := gen.Generate(context.Background(), nil)
	if err == nil {
		t.Error("expected error for nil bundle")
	}
}

func TestGenerator_Generate_LLMError(t *testing.T) {
	llm := &mockLLM{err: fmt.Errorf("timeout")}
	gen := NewGenerator(llm, &mockParser{})

	bundle := &contracts.ContextBundle{Template: "test", MaxTokens: 100}
	_, err := gen.Generate(context.Background(), bundle)
	if err == nil {
		t.Error("expected error from LLM failure")
	}
}

func TestGenerator_Generate_ParserError(t *testing.T) {
	llm := &mockLLM{response: "raw"}
	parser := &mockParser{err: fmt.Errorf("parse error")}
	gen := NewGenerator(llm, parser)

	bundle := &contracts.ContextBundle{Template: "test", MaxTokens: 100}
	_, err := gen.Generate(context.Background(), bundle)
	if err == nil {
		t.Error("expected error from parser failure")
	}
}

func TestGenerator_Generate_InvalidCitationRefs(t *testing.T) {
	llm := &mockLLM{response: "raw"}
	parser := &mockParser{
		statements: []Statement{
			{Text: "Statement", CitationRefs: []string{"func:a", "func:nonexistent"}},
		},
	}

	gen := NewGenerator(llm, parser)
	bundle := &contracts.ContextBundle{
		Anchors:   []contracts.RetrievalCandidate{{NodeKey: "func:a"}},
		Template:  "test",
		MaxTokens: 100,
	}

	result, err := gen.Generate(context.Background(), bundle)
	// Should return result AND a validation error
	if result == nil {
		t.Fatal("expected non-nil result even with validation errors")
	}

	var valErr *CitationValidationError
	if err == nil {
		t.Fatal("expected CitationValidationError")
	}

	// Type assert
	valErr, ok := err.(*CitationValidationError)
	if !ok {
		t.Fatalf("expected *CitationValidationError, got %T", err)
	}

	if len(valErr.Errors) != 1 {
		t.Errorf("expected 1 validation error, got %d", len(valErr.Errors))
	}
	if valErr.Errors[0].MissingRefs[0] != "func:nonexistent" {
		t.Errorf("expected missing ref 'func:nonexistent', got %v", valErr.Errors[0].MissingRefs)
	}
}

func TestGenerator_Generate_IncludesExpansionAndInferenceEvidence(t *testing.T) {
	llm := &mockLLM{response: "raw"}
	parser := &mockParser{
		statements: []Statement{
			{Text: "S1", CitationRefs: []string{"func:expanded"}},
			{Text: "S2", CitationRefs: []string{"func:inferred-target"}},
		},
	}

	gen := NewGenerator(llm, parser)
	bundle := &contracts.ContextBundle{
		Anchors:    []contracts.RetrievalCandidate{{NodeKey: "func:a"}},
		Expansions: []contracts.RetrievalCandidate{{NodeKey: "func:expanded"}},
		Inferences: []contracts.InferenceResult{
			{SourceKey: "func:a", TargetKey: "func:inferred-target"},
		},
		Template:  "test",
		MaxTokens: 100,
	}

	result, err := gen.Generate(context.Background(), bundle)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Both citations should resolve
	for i, c := range result.Citations {
		if len(c.EvidenceRefs) == 0 {
			t.Errorf("citation %d has no evidence refs", i)
		}
	}
}

func TestValidateGenerationResult_AllCited(t *testing.T) {
	result := &contracts.GenerationResult{
		Citations: []contracts.Citation{
			{StatementIndex: 0, EvidenceRefs: []contracts.EvidenceRef{{NodeKey: "func:a"}}},
			{StatementIndex: 1, EvidenceRefs: []contracts.EvidenceRef{{NodeKey: "func:b"}}},
		},
	}

	uncited := ValidateGenerationResult(result)
	if len(uncited) != 0 {
		t.Errorf("expected 0 uncited, got %d", len(uncited))
	}
}

func TestValidateGenerationResult_WithUncited(t *testing.T) {
	result := &contracts.GenerationResult{
		Citations: []contracts.Citation{
			{StatementIndex: 0, EvidenceRefs: []contracts.EvidenceRef{{NodeKey: "func:a"}}},
			{StatementIndex: 1, EvidenceRefs: nil}, // uncited
			{StatementIndex: 2, EvidenceRefs: []contracts.EvidenceRef{{NodeKey: "func:c"}}},
		},
	}

	uncited := ValidateGenerationResult(result)
	if len(uncited) != 1 || uncited[0] != 1 {
		t.Errorf("expected [1] uncited, got %v", uncited)
	}
}

func TestValidateGenerationResult_Nil(t *testing.T) {
	uncited := ValidateGenerationResult(nil)
	if uncited != nil {
		t.Errorf("expected nil for nil result, got %v", uncited)
	}
}

func TestBuildContent(t *testing.T) {
	statements := []Statement{
		{Text: "Line 1."},
		{Text: "Line 2."},
		{Text: "Line 3."},
	}
	content := buildContent(statements)
	expected := "Line 1.\nLine 2.\nLine 3."
	if content != expected {
		t.Errorf("expected %q, got %q", expected, content)
	}
}

func TestBuildContent_Empty(t *testing.T) {
	content := buildContent(nil)
	if content != "" {
		t.Errorf("expected empty, got %q", content)
	}
}

func TestBuildEvidenceIndex(t *testing.T) {
	bundle := &contracts.ContextBundle{
		Anchors:    []contracts.RetrievalCandidate{{NodeKey: "func:a"}, {NodeKey: "func:b"}},
		Expansions: []contracts.RetrievalCandidate{{NodeKey: "func:x"}},
		Inferences: []contracts.InferenceResult{{SourceKey: "func:a", TargetKey: "func:y"}},
	}

	index := buildEvidenceIndex(bundle)
	for _, key := range []string{"func:a", "func:b", "func:x", "func:y"} {
		if !index[key] {
			t.Errorf("expected %s in evidence index", key)
		}
	}
	if index["func:z"] {
		t.Error("unexpected func:z in evidence index")
	}
}

func TestDefaultPromptBuilder(t *testing.T) {
	pb := &DefaultPromptBuilder{}
	bundle := &contracts.ContextBundle{
		Anchors: []contracts.RetrievalCandidate{
			{NodeKey: "func:a", NodeType: "Function", Metadata: map[string]any{"name": "Start"}},
		},
		Template: "flow_summary",
	}

	prompt, err := pb.BuildPrompt(bundle)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if prompt == "" {
		t.Error("expected non-empty prompt")
	}
	if !containsAll(prompt, "Return strict JSON", "\"statements\"", "citationRef") {
		t.Fatalf("expected structured JSON contract in prompt, got: %s", prompt)
	}
}

func TestDefaultPromptBuilder_Nil(t *testing.T) {
	pb := &DefaultPromptBuilder{}
	_, err := pb.BuildPrompt(nil)
	if err == nil {
		t.Error("expected error for nil bundle")
	}
}

func TestGenerator_ImplementsInterface(t *testing.T) {
	var _ contracts.Generator = (*Generator)(nil)
}

func containsAll(value string, fragments ...string) bool {
	for _, fragment := range fragments {
		if !strings.Contains(value, fragment) {
			return false
		}
	}
	return true
}
