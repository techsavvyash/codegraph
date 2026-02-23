package search

import (
	"testing"
)

// ---------------------------------------------------------------------------
// IntelligentDocumentLinker scope tests
// ---------------------------------------------------------------------------

func TestIntelligentDocumentLinker_DefaultScope(t *testing.T) {
	idl := &IntelligentDocumentLinker{scopeID: "main"}
	if idl.scopeID != "main" {
		t.Errorf("expected default scopeID 'main', got %q", idl.scopeID)
	}
}

func TestIntelligentDocumentLinker_SetScope(t *testing.T) {
	idl := &IntelligentDocumentLinker{scopeID: "main"}
	idl.SetScope("pr-42")
	if idl.scopeID != "pr-42" {
		t.Errorf("expected scopeID 'pr-42', got %q", idl.scopeID)
	}
}

func TestIntelligentDocumentLinker_SetScope_EmptyDefaultsToMain(t *testing.T) {
	idl := &IntelligentDocumentLinker{scopeID: "pr-42"}
	idl.SetScope("")
	if idl.scopeID != "main" {
		t.Errorf("expected scopeID 'main' after empty SetScope, got %q", idl.scopeID)
	}
}

// ---------------------------------------------------------------------------
// Helper function tests
// ---------------------------------------------------------------------------

func TestGetStringValue(t *testing.T) {
	m := map[string]any{
		"name":  "Foo",
		"count": 42,
		"empty": "",
		"nil":   nil,
	}
	if getStringValue(m, "name") != "Foo" {
		t.Error("expected Foo")
	}
	if getStringValue(m, "count") != "" {
		t.Error("expected empty string for non-string value")
	}
	if getStringValue(m, "missing") != "" {
		t.Error("expected empty string for missing key")
	}
	if getStringValue(m, "empty") != "" {
		t.Error("expected empty string for empty value")
	}
	if getStringValue(m, "nil") != "" {
		t.Error("expected empty string for nil value")
	}
}

func TestContains(t *testing.T) {
	tests := []struct {
		s, substr string
		expected  bool
	}{
		{"hello world", "world", true},
		{"hello", "hello", true},
		{"hello", "hell", true},
		{"hello", "ello", true},
		{"hello", "lo", true},
		{"hello", "xyz", false},
		{"hello", "", false},
		{"", "hello", false},
		{"", "", false},
		{"abc", "abcd", false},
	}
	for _, tt := range tests {
		got := contains(tt.s, tt.substr)
		if got != tt.expected {
			t.Errorf("contains(%q, %q) = %v, want %v", tt.s, tt.substr, got, tt.expected)
		}
	}
}

func TestFindSubstring(t *testing.T) {
	if !findSubstring("hello world", "lo w") {
		t.Error("expected to find 'lo w' in 'hello world'")
	}
	if findSubstring("hello", "xyz") {
		t.Error("expected not to find 'xyz' in 'hello'")
	}
}

func TestMinInt(t *testing.T) {
	if minInt(3, 5) != 3 {
		t.Error("expected 3")
	}
	if minInt(5, 3) != 3 {
		t.Error("expected 3")
	}
	if minInt(3, 3) != 3 {
		t.Error("expected 3")
	}
}

// ---------------------------------------------------------------------------
// Confidence calculation tests
// ---------------------------------------------------------------------------

func TestCalculateSemanticConfidence(t *testing.T) {
	idl := &IntelligentDocumentLinker{}

	// similarity=1.0 → confidence capped at 1.0
	c := idl.calculateSemanticConfidence(1.0)
	if c != 1.0 {
		t.Errorf("expected 1.0, got %f", c)
	}

	// similarity=0.15 → confidence=0 (floor)
	c = idl.calculateSemanticConfidence(0.15)
	if c != 0.0 {
		t.Errorf("expected 0.0 for similarity=0.15, got %f", c)
	}

	// similarity=0.5 → positive confidence
	c = idl.calculateSemanticConfidence(0.5)
	if c < 0.5 || c > 0.55 {
		t.Errorf("expected ~0.54 for similarity=0.5, got %f", c)
	}

	// similarity below 0.15 → confidence=0
	c = idl.calculateSemanticConfidence(0.1)
	if c != 0 {
		t.Errorf("expected 0 for similarity=0.1, got %f", c)
	}
}

func TestCalculateHybridConfidence(t *testing.T) {
	idl := &IntelligentDocumentLinker{}

	// RRF-scale score normalization: score/0.05
	c := idl.calculateHybridConfidence(0.01, "query", "someName")
	if c <= 0 || c > 1.0 {
		t.Errorf("confidence should be in (0, 1.0], got %f", c)
	}

	// Boost when query appears in function name
	cNormal := idl.calculateHybridConfidence(0.01, "Handle", "notMatching")
	cBoosted := idl.calculateHybridConfidence(0.01, "Handle", "HandleRequest")
	if cBoosted <= cNormal {
		t.Errorf("expected boost when query in name: boosted=%f, normal=%f", cBoosted, cNormal)
	}

	// Short query (len <= 2) should not get boost
	c1 := idl.calculateHybridConfidence(0.01, "ab", "abcdef")
	c2 := idl.calculateHybridConfidence(0.01, "ab", "xyz")
	if c1 != c2 {
		t.Errorf("short queries should not get boost: c1=%f, c2=%f", c1, c2)
	}
}

// ---------------------------------------------------------------------------
// Deduplication tests
// ---------------------------------------------------------------------------

func TestDeduplicateMatches(t *testing.T) {
	idl := &IntelligentDocumentLinker{}
	matches := []CodeMatch{
		{NodeKey: "k1", Confidence: 0.8},
		{NodeKey: "k2", Confidence: 0.6},
		{NodeKey: "k1", Confidence: 0.9}, // duplicate
		{NodeKey: "k3", Confidence: 0.7},
	}
	unique := idl.deduplicateMatches(matches)
	if len(unique) != 3 {
		t.Errorf("expected 3 unique matches, got %d", len(unique))
	}

	// First occurrence wins.
	for _, m := range unique {
		if m.NodeKey == "k1" && m.Confidence != 0.8 {
			t.Errorf("expected first occurrence (0.8), got %f", m.Confidence)
		}
	}
}

func TestDeduplicateMatches_Empty(t *testing.T) {
	idl := &IntelligentDocumentLinker{}
	result := idl.deduplicateMatches(nil)
	if len(result) != 0 {
		t.Errorf("expected 0 results for nil input, got %d", len(result))
	}
}

// ---------------------------------------------------------------------------
// Consolidation tests
// ---------------------------------------------------------------------------

func TestConsolidateMatches(t *testing.T) {
	idl := &IntelligentDocumentLinker{}
	direct := []CodeMatch{
		{NodeKey: "k1", Confidence: 0.8, MatchReasons: []string{"direct"}},
	}
	semantic := []CodeMatch{
		{NodeKey: "k1", Confidence: 0.9, MatchReasons: []string{"semantic"}},
		{NodeKey: "k2", Confidence: 0.6, MatchReasons: []string{"semantic"}},
	}
	callGraph := []CodeMatch{
		{NodeKey: "k3", Confidence: 0.5, MatchReasons: []string{"call_graph"}},
	}

	result := idl.consolidateMatches(direct, semantic, callGraph)
	if len(result) != 3 {
		t.Fatalf("expected 3 consolidated matches, got %d", len(result))
	}

	// k1 should have the higher confidence (0.9) and merged reasons.
	for _, m := range result {
		if m.NodeKey == "k1" {
			if m.Confidence != 0.9 {
				t.Errorf("expected consolidated confidence 0.9, got %f", m.Confidence)
			}
			if len(m.MatchReasons) != 2 {
				t.Errorf("expected 2 merged reasons, got %d", len(m.MatchReasons))
			}
		}
	}

	// Result should be sorted by confidence descending.
	for i := 1; i < len(result); i++ {
		if result[i].Confidence > result[i-1].Confidence {
			t.Errorf("results not sorted by confidence: %f > %f at index %d",
				result[i].Confidence, result[i-1].Confidence, i)
		}
	}
}

func TestConsolidateMatches_AllEmpty(t *testing.T) {
	idl := &IntelligentDocumentLinker{}
	result := idl.consolidateMatches(nil, nil, nil)
	if len(result) != 0 {
		t.Errorf("expected 0 results for all-empty input, got %d", len(result))
	}
}

// ---------------------------------------------------------------------------
// Code symbol extraction tests
// ---------------------------------------------------------------------------

func TestExtractCodeSymbols(t *testing.T) {
	idl := &IntelligentDocumentLinker{}
	content := "Use `MergeNode()` and `Client.Close()` to manage the database. Also see `fmt`."
	symbols := idl.extractCodeSymbols(content)

	found := map[string]bool{}
	for _, s := range symbols {
		found[s] = true
	}

	if !found["MergeNode()"] {
		t.Error("expected MergeNode()")
	}
	if !found["Client.Close()"] {
		t.Error("expected Client.Close()")
	}
}

func TestExtractCodeSymbols_Empty(t *testing.T) {
	idl := &IntelligentDocumentLinker{}
	symbols := idl.extractCodeSymbols("No code references here at all.")
	if len(symbols) != 0 {
		t.Errorf("expected 0 symbols, got %d: %v", len(symbols), symbols)
	}
}

func TestIsLikelyCodeSymbol(t *testing.T) {
	idl := &IntelligentDocumentLinker{}

	tests := []struct {
		input    string
		expected bool
	}{
		{"MergeNode", true},      // has uppercase
		{"some_func", true},      // has underscore
		{"pkg.Func", true},       // has dot
		{"the", false},           // common word
		{"if", false},            // common word
		{"hello", false},         // no uppercase/underscore/dot
		{"", false},              // empty
		{"A", true},              // single uppercase
		{"CreateUser", true},     // PascalCase
		{"CONSTANT_VALUE", true}, // ALL_CAPS with underscore
	}
	for _, tt := range tests {
		got := idl.isLikelyCodeSymbol(tt.input)
		if got != tt.expected {
			t.Errorf("isLikelyCodeSymbol(%q) = %v, want %v", tt.input, got, tt.expected)
		}
	}
}

// ---------------------------------------------------------------------------
// CodeMatch struct tests
// ---------------------------------------------------------------------------

func TestCodeMatch_Fields(t *testing.T) {
	m := CodeMatch{
		NodeKey:        "func:pkg/foo.go#Bar()",
		NodeType:       "Function",
		Name:           "Bar",
		Signature:      "func Bar(ctx context.Context) error",
		FilePath:       "pkg/foo.go",
		Confidence:     0.85,
		MatchReasons:   []string{"direct_reference", "backtick"},
		CallGraphDepth: 2,
	}

	if m.NodeKey != "func:pkg/foo.go#Bar()" {
		t.Errorf("unexpected NodeKey: %s", m.NodeKey)
	}
	if m.CallGraphDepth != 2 {
		t.Errorf("unexpected CallGraphDepth: %d", m.CallGraphDepth)
	}
	if len(m.MatchReasons) != 2 {
		t.Errorf("expected 2 reasons, got %d", len(m.MatchReasons))
	}
}
