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

func TestChunkMentionEdge_Fields(t *testing.T) {
	edge := ChunkMentionEdge{
		ChunkNodeKey:  "chunk:doc:readme.md#0",
		TargetNodeKey: "func:pkg/foo.go#Bar()",
		TargetLabel:   "Function",
		Confidence:    0.85,
		Reasons:       []string{"backtick_reference", "mentioned as `Bar()`"},
		Model:         "backtick_extraction",
	}

	if edge.ChunkNodeKey != "chunk:doc:readme.md#0" {
		t.Errorf("unexpected ChunkNodeKey: %s", edge.ChunkNodeKey)
	}
	if edge.Confidence != 0.85 {
		t.Errorf("unexpected Confidence: %f", edge.Confidence)
	}
	if len(edge.Reasons) != 2 {
		t.Errorf("expected 2 reasons, got %d", len(edge.Reasons))
	}
}

func TestStrVal(t *testing.T) {
	m := map[string]any{
		"name":  "Foo",
		"count": 42,
		"empty": "",
	}
	if strVal(m, "name") != "Foo" {
		t.Error("expected Foo")
	}
	if strVal(m, "count") != "" {
		t.Error("expected empty string for non-string value")
	}
	if strVal(m, "missing") != "" {
		t.Error("expected empty string for missing key")
	}
	if strVal(m, "empty") != "" {
		t.Error("expected empty string for empty value")
	}
}

func TestRemoveDuplicateStringsSearch(t *testing.T) {
	input := []string{"Foo", "foo", "Bar", "FOO", "bar", "Baz"}
	result := removeDuplicateStringsSearch(input)

	// Case-insensitive dedup: Foo, Bar, Baz
	if len(result) != 3 {
		t.Errorf("expected 3 unique strings, got %d: %v", len(result), result)
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

func TestDeduplicateEdges_Empty(t *testing.T) {
	result := deduplicateEdges(nil)
	if len(result) != 0 {
		t.Errorf("expected 0 edges for nil input, got %d", len(result))
	}
}

func TestDeduplicateEdges_NoDuplicates(t *testing.T) {
	edges := []ChunkMentionEdge{
		{ChunkNodeKey: "c1", TargetNodeKey: "t1", Confidence: 0.9},
		{ChunkNodeKey: "c1", TargetNodeKey: "t2", Confidence: 0.8},
		{ChunkNodeKey: "c2", TargetNodeKey: "t1", Confidence: 0.7},
	}
	result := deduplicateEdges(edges)
	if len(result) != 3 {
		t.Errorf("expected 3 edges (no duplicates), got %d", len(result))
	}
}

func TestDeduplicateEdges_SameChunkDifferentTargets(t *testing.T) {
	edges := []ChunkMentionEdge{
		{ChunkNodeKey: "c1", TargetNodeKey: "t1", Confidence: 0.5},
		{ChunkNodeKey: "c1", TargetNodeKey: "t2", Confidence: 0.6},
		{ChunkNodeKey: "c1", TargetNodeKey: "t3", Confidence: 0.7},
	}
	result := deduplicateEdges(edges)
	if len(result) != 3 {
		t.Errorf("expected 3 edges (different targets), got %d", len(result))
	}
}

// TestExtractBacktickRefs_ComplexPatterns tests more complex code patterns.
func TestExtractBacktickRefs_ComplexPatterns(t *testing.T) {
	content := "See `pkg.NewClient()` and `neo4j.Client.Close` for details."
	refs := extractBacktickRefs(content)

	found := map[string]bool{}
	for _, r := range refs {
		found[r] = true
	}

	if !found["pkg.NewClient()"] {
		t.Error("expected to find pkg.NewClient()")
	}
	if !found["neo4j.Client.Close"] {
		t.Error("expected to find neo4j.Client.Close")
	}
}

// TestExtractHeadingRefs_MultipleSections tests multi-level heading paths.
func TestExtractHeadingRefs_MultipleSections(t *testing.T) {
	refs := extractHeadingRefs("Architecture > DataStore > QueryBuilder > FilterEngine")
	found := map[string]bool{}
	for _, r := range refs {
		found[r] = true
	}
	if !found["DataStore"] {
		t.Error("expected DataStore")
	}
	if !found["QueryBuilder"] {
		t.Error("expected QueryBuilder")
	}
	if !found["FilterEngine"] {
		t.Error("expected FilterEngine")
	}
}
