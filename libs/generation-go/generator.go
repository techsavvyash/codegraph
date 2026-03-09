package generation

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/context-maximiser/code-graph/libs/intelligence-go/contracts"
)

// LLMClient is the interface for calling a language model.
type LLMClient interface {
	// Complete sends a prompt and returns the raw completion text.
	Complete(ctx context.Context, prompt string, maxTokens int) (string, error)
	// ModelName returns the model identifier.
	ModelName() string
}

// ResponseParser parses LLM output into structured statements with citations.
type ResponseParser interface {
	// Parse extracts statements and their citation indices from raw LLM output.
	Parse(raw string) ([]Statement, error)
}

// Statement represents a single generated statement with citation references.
type Statement struct {
	Text         string   `json:"text"`
	CitationRefs []string `json:"citationRefs"` // NodeKeys referenced by this statement
}

// Generator produces text with statement-level citations from a context bundle.
// It implements the contracts.Generator interface.
type Generator struct {
	llm           LLMClient
	parser        ResponseParser
	promptBuilder PromptBuilder
}

// PromptBuilder constructs prompts from context bundles.
type PromptBuilder interface {
	BuildPrompt(bundle *contracts.ContextBundle) (string, error)
}

// NewGenerator creates a generator with the given LLM client and parser.
func NewGenerator(llm LLMClient, parser ResponseParser) *Generator {
	return &Generator{
		llm:           llm,
		parser:        parser,
		promptBuilder: &DefaultPromptBuilder{},
	}
}

// WithPromptBuilder sets a custom prompt builder.
func (g *Generator) WithPromptBuilder(pb PromptBuilder) *Generator {
	g.promptBuilder = pb
	return g
}

// Generate produces a GenerationResult from a ContextBundle.
func (g *Generator) Generate(ctx context.Context, bundle *contracts.ContextBundle) (*contracts.GenerationResult, error) {
	if bundle == nil {
		return nil, fmt.Errorf("bundle is nil")
	}

	// Build prompt from bundle
	prompt, err := g.promptBuilder.BuildPrompt(bundle)
	if err != nil {
		return nil, fmt.Errorf("building prompt: %w", err)
	}

	// Call LLM
	raw, err := g.llm.Complete(ctx, prompt, bundle.MaxTokens)
	if err != nil {
		return nil, fmt.Errorf("LLM completion: %w", err)
	}

	// Parse response into statements
	statements, err := g.parser.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}

	// Build citation index from bundle's available evidence
	evidenceIndex := buildEvidenceIndex(bundle)

	// Convert statements to citations, validating references
	citations, validationErrors := buildCitations(statements, evidenceIndex)

	// Build content from statements
	content := buildContent(statements)

	result := &contracts.GenerationResult{
		Content:   content,
		Citations: citations,
		Model:     g.llm.ModelName(),
		Template:  bundle.Template,
		CreatedAt: time.Now(),
	}

	// If there are validation errors, wrap them but still return the result
	if len(validationErrors) > 0 {
		return result, &CitationValidationError{Errors: validationErrors}
	}

	return result, nil
}

// CitationValidationError indicates some statements have invalid citation references.
type CitationValidationError struct {
	Errors []StatementError
}

func (e *CitationValidationError) Error() string {
	return fmt.Sprintf("%d statements have citation issues", len(e.Errors))
}

// StatementError describes a citation issue for a specific statement.
type StatementError struct {
	StatementIndex int      `json:"statementIndex"`
	MissingRefs    []string `json:"missingRefs"`
}

// buildEvidenceIndex creates a lookup of available evidence from the bundle.
func buildEvidenceIndex(bundle *contracts.ContextBundle) map[string]bool {
	index := make(map[string]bool)
	for _, a := range bundle.Anchors {
		index[a.NodeKey] = true
	}
	for _, e := range bundle.Expansions {
		index[e.NodeKey] = true
	}
	for _, inf := range bundle.Inferences {
		index[inf.SourceKey] = true
		index[inf.TargetKey] = true
	}
	return index
}

// buildCitations converts statements into citation arrays, checking each reference.
func buildCitations(statements []Statement, evidenceIndex map[string]bool) ([]contracts.Citation, []StatementError) {
	var citations []contracts.Citation
	var errors []StatementError

	for i, stmt := range statements {
		var refs []contracts.EvidenceRef
		var missingRefs []string

		for _, ref := range stmt.CitationRefs {
			if evidenceIndex[ref] {
				refs = append(refs, contracts.EvidenceRef{
					Kind:    "citation",
					NodeKey: ref,
				})
			} else {
				missingRefs = append(missingRefs, ref)
			}
		}

		citations = append(citations, contracts.Citation{
			StatementIndex: i,
			EvidenceRefs:   refs,
		})

		if len(missingRefs) > 0 {
			errors = append(errors, StatementError{
				StatementIndex: i,
				MissingRefs:    missingRefs,
			})
		}
	}

	return citations, errors
}

// buildContent concatenates statement texts into the final content.
func buildContent(statements []Statement) string {
	if len(statements) == 0 {
		return ""
	}
	content := ""
	for i, stmt := range statements {
		if i > 0 {
			content += "\n"
		}
		content += stmt.Text
	}
	return content
}

// ValidateGenerationResult checks that every statement has at least one citation.
// Returns uncited statement indices.
func ValidateGenerationResult(result *contracts.GenerationResult) []int {
	if result == nil {
		return nil
	}

	citedStatements := make(map[int]bool)
	for _, c := range result.Citations {
		if len(c.EvidenceRefs) > 0 {
			citedStatements[c.StatementIndex] = true
		}
	}

	var uncited []int
	for i := range result.Citations {
		if !citedStatements[i] {
			uncited = append(uncited, i)
		}
	}
	return uncited
}

// DefaultPromptBuilder is a simple prompt builder that formats bundle contents.
type DefaultPromptBuilder struct{}

func (d *DefaultPromptBuilder) BuildPrompt(bundle *contracts.ContextBundle) (string, error) {
	if bundle == nil {
		return "", fmt.Errorf("nil bundle")
	}

	var prompt strings.Builder
	prompt.WriteString(fmt.Sprintf("Task template: %s\n", bundle.Template))
	prompt.WriteString("Return strict JSON only. Do not include markdown fences or extra commentary.\n")
	prompt.WriteString("JSON schema: {\"statements\":[{\"text\":\"...\",\"citationRefs\":[\"node:key\"]}]}\n")
	prompt.WriteString("Requirements:\n")
	prompt.WriteString("- Every statement MUST include at least one citationRef that exactly matches an evidence key.\n")
	prompt.WriteString("- Do not invent evidence keys.\n")
	prompt.WriteString("- Use concrete repository facts only; avoid generic filler (for example 'ready for the next steps').\n")
	prompt.WriteString("- Keep statements concise and specific to this repository context.\n")
	prompt.WriteString(templateSpecificGuidance(bundle.Template))
	prompt.WriteString("\nAvailable evidence:\n")

	for _, a := range bundle.Anchors {
		name, _ := a.Metadata["name"].(string)
		prompt.WriteString(fmt.Sprintf("- [%s] %s (%s, anchor)\n", a.NodeKey, name, a.NodeType))
	}

	for _, e := range bundle.Expansions {
		name, _ := e.Metadata["name"].(string)
		prompt.WriteString(fmt.Sprintf("- [%s] %s (%s, expansion)\n", e.NodeKey, name, e.NodeType))
	}

	for _, inf := range bundle.Inferences {
		prompt.WriteString(fmt.Sprintf("- [%s -> %s] inferred:%s (confidence=%.2f)\n", inf.SourceKey, inf.TargetKey, inf.RelationType, inf.Confidence))
	}

	prompt.WriteString("\nReturn between 3 and 8 statements unless evidence is very sparse.")
	return prompt.String(), nil
}

func templateSpecificGuidance(template string) string {
	switch template {
	case "pr_summary":
		return "- Output should describe what changed, why it matters, and likely impact areas.\n"
	case "flow_summary":
		return "- Output should describe entrypoint, major execution steps, and downstream effects.\n"
	case "docstring_suggestion":
		return "- Output should read like a docstring draft: purpose, key parameters/returns, and behavior caveats.\n"
	default:
		return "- Output should be a factual summary tied to cited evidence.\n"
	}
}
