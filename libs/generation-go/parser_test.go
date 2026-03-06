package generation

import "testing"

func TestSimpleResponseParser_ParseStructuredJSON(t *testing.T) {
	parser := NewSimpleResponseParser()
	raw := `{"statements":[{"text":"Handler validates input.","citationRefs":["func:handler"]},{"text":"Service persists data.","citationRefs":["func:service","func:service"]}]}`

	statements, err := parser.Parse(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(statements) != 2 {
		t.Fatalf("expected 2 statements, got %d", len(statements))
	}
	if len(statements[1].CitationRefs) != 1 {
		t.Fatalf("expected duplicate refs to be de-duplicated, got %d refs", len(statements[1].CitationRefs))
	}
}

func TestSimpleResponseParser_ParseFallbackLines(t *testing.T) {
	parser := NewSimpleResponseParser()
	raw := "Handles request [func:handler]\nPersists entity [func:repo]"

	statements, err := parser.Parse(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(statements) != 2 {
		t.Fatalf("expected 2 statements, got %d", len(statements))
	}
	if statements[0].CitationRefs[0] != "func:handler" {
		t.Fatalf("expected citation func:handler, got %v", statements[0].CitationRefs)
	}
}
