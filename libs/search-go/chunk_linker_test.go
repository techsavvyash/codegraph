package search

import (
	"testing"
)

func TestExtractBacktickRefs(t *testing.T) {
	content := "Use `MergeNode()` and `Client.Close()` for database ops. Also see `fmt`."
	refs := extractBacktickRefs(content)

	found := map[string]bool{}
	for _, r := range refs {
		found[r] = true
	}

	if !found["MergeNode()"] {
		t.Error("expected to find MergeNode()")
	}
	if !found["Client.Close()"] {
		t.Error("expected to find Client.Close()")
	}
	// "fmt" alone (no uppercase/underscore) should be filtered out.
	if found["fmt"] {
		t.Error("expected fmt to be filtered out (no code-like patterns)")
	}
}

func TestExtractBacktickRefs_NoDuplicates(t *testing.T) {
	content := "Call `Foo()` then `Foo()` again."
	refs := extractBacktickRefs(content)
	if len(refs) != 1 {
		t.Errorf("expected 1 ref (deduplicated), got %d", len(refs))
	}
}

func TestExtractBacktickRefs_Empty(t *testing.T) {
	refs := extractBacktickRefs("No code references here.")
	if len(refs) != 0 {
		t.Errorf("expected 0 refs, got %d", len(refs))
	}
}

func TestExtractHeadingRefs(t *testing.T) {
	// CamelCase/PascalCase words should be extracted.
	refs := extractHeadingRefs("Architecture > VectorSearch > QueryBuilder")

	found := map[string]bool{}
	for _, r := range refs {
		found[r] = true
	}

	if !found["VectorSearch"] {
		t.Error("expected to find VectorSearch")
	}
	if !found["QueryBuilder"] {
		t.Error("expected to find QueryBuilder")
	}
}

func TestExtractHeadingRefs_Empty(t *testing.T) {
	refs := extractHeadingRefs("")
	if len(refs) != 0 {
		t.Errorf("expected 0 refs for empty heading, got %d", len(refs))
	}
}

func TestExtractHeadingRefs_NoCode(t *testing.T) {
	refs := extractHeadingRefs("Introduction > Overview")
	// No PascalCase multi-word identifiers.
	if len(refs) != 0 {
		t.Errorf("expected 0 code refs, got %d: %v", len(refs), refs)
	}
}

func TestIsCodeLikeReference(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"MergeNode", true},
		{"Client.Close()", true},
		{"some_var", true},
		{"fmt", false},       // No uppercase/underscore
		{"x", false},         // Too short
		{"", false},          // Empty
		{"CreateIndex", true},
	}

	for _, tt := range tests {
		got := isCodeLikeReference(tt.input)
		if got != tt.expected {
			t.Errorf("isCodeLikeReference(%q) = %v, want %v", tt.input, got, tt.expected)
		}
	}
}

// ---------------------------------------------------------------------------
// ChunkLinker scope tests
// ---------------------------------------------------------------------------

func TestChunkLinker_NewDefaultsToMain(t *testing.T) {
	cl := NewChunkLinker(nil)
	if cl.scopeID != "main" {
		t.Errorf("expected default scopeID 'main', got %q", cl.scopeID)
	}
}

func TestChunkLinker_SetScope(t *testing.T) {
	cl := NewChunkLinker(nil)
	cl.SetScope("pr-42")
	if cl.scopeID != "pr-42" {
		t.Errorf("expected scopeID 'pr-42', got %q", cl.scopeID)
	}
}

func TestChunkLinker_SetScope_EmptyDefaultsToMain(t *testing.T) {
	cl := NewChunkLinker(nil)
	cl.SetScope("pr-42")
	cl.SetScope("") // reset
	if cl.scopeID != "main" {
		t.Errorf("expected scopeID 'main' after empty SetScope, got %q", cl.scopeID)
	}
}

func TestDeduplicateEdges(t *testing.T) {
	edges := []ChunkMentionEdge{
		{ChunkNodeKey: "c1", TargetNodeKey: "t1", Confidence: 0.6, Reasons: []string{"heading"}},
		{ChunkNodeKey: "c1", TargetNodeKey: "t1", Confidence: 0.8, Reasons: []string{"backtick"}},
		{ChunkNodeKey: "c1", TargetNodeKey: "t2", Confidence: 0.7, Reasons: []string{"backtick"}},
	}

	result := deduplicateEdges(edges)

	if len(result) != 2 {
		t.Fatalf("expected 2 deduplicated edges, got %d", len(result))
	}

	// Find the edge for t1 — it should have the higher confidence.
	for _, e := range result {
		if e.TargetNodeKey == "t1" {
			if e.Confidence != 0.8 {
				t.Errorf("expected confidence 0.8 for t1, got %f", e.Confidence)
			}
			if len(e.Reasons) != 2 {
				t.Errorf("expected 2 merged reasons, got %d", len(e.Reasons))
			}
		}
	}
}
