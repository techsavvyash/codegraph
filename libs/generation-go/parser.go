package generation

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

var citationRefPattern = regexp.MustCompile(`\[([^\]\s]+)\]`)

type parsedResponse struct {
	Statements []Statement `json:"statements"`
}

// SimpleResponseParser is a default ResponseParser that splits raw LLM output
// into statements (one per non-empty line) and extracts citation references
// from [nodeKey] patterns.
type SimpleResponseParser struct{}

// NewSimpleResponseParser creates a new SimpleResponseParser.
func NewSimpleResponseParser() *SimpleResponseParser {
	return &SimpleResponseParser{}
}

// Parse splits raw LLM output into statements with extracted citation refs.
func (p *SimpleResponseParser) Parse(raw string) ([]Statement, error) {
	if structured, ok := parseStructuredStatements(raw); ok {
		if len(structured) == 0 {
			return nil, fmt.Errorf("structured response contains no statements")
		}
		return structured, nil
	}

	lines := strings.Split(raw, "\n")
	var statements []Statement

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		refs := extractCitationRefs(line)
		statements = append(statements, Statement{
			Text:         line,
			CitationRefs: refs,
		})
	}

	if len(statements) == 0 && raw != "" {
		return []Statement{{Text: raw}}, nil
	}

	return statements, nil
}

// extractCitationRefs finds all [nodeKey] patterns in text.
func extractCitationRefs(text string) []string {
	matches := citationRefPattern.FindAllStringSubmatch(text, -1)
	var refs []string
	for _, m := range matches {
		if len(m) > 1 {
			refs = append(refs, m[1])
		}
	}
	return refs
}

func parseStructuredStatements(raw string) ([]Statement, bool) {
	candidates := []string{strings.TrimSpace(raw)}
	if fenced := extractJSONCodeFence(raw); fenced != "" {
		candidates = append([]string{fenced}, candidates...)
	}

	for _, candidate := range candidates {
		if candidate == "" || !strings.Contains(candidate, "\"statements\"") {
			continue
		}
		var response parsedResponse
		if err := json.Unmarshal([]byte(candidate), &response); err != nil {
			continue
		}
		for i := range response.Statements {
			response.Statements[i].Text = strings.TrimSpace(response.Statements[i].Text)
			response.Statements[i].CitationRefs = uniqueNonEmpty(response.Statements[i].CitationRefs)
		}
		return response.Statements, true
	}

	return nil, false
}

func extractJSONCodeFence(raw string) string {
	start := strings.Index(raw, "```")
	if start == -1 {
		return ""
	}
	remaining := raw[start+3:]
	if nl := strings.Index(remaining, "\n"); nl >= 0 {
		remaining = remaining[nl+1:]
	}
	end := strings.Index(remaining, "```")
	if end == -1 {
		return ""
	}
	return strings.TrimSpace(remaining[:end])
}

func uniqueNonEmpty(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		key := strings.TrimSpace(value)
		if key == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, key)
	}
	return out
}
