package inference

import (
	"math"
	"testing"

	"github.com/context-maximiser/code-graph/internal/model/contracts"
)

func TestFeatureExtractor_Extract_VectorOnly(t *testing.T) {
	fe := NewFeatureExtractor()
	candidate := contracts.RetrievalCandidate{
		NodeKey: "func:a", Score: 0.75, Source: "vector",
	}

	fv := fe.Extract(candidate, nil)

	if fv.VectorScore == 0 {
		t.Error("expected non-zero vector score")
	}
	if fv.StructuralSupport != 0 {
		t.Error("expected zero structural support without evidence")
	}
}

func TestFeatureExtractor_Extract_WithEvidence(t *testing.T) {
	fe := NewFeatureExtractor()
	candidate := contracts.RetrievalCandidate{
		NodeKey: "func:a", Score: 0.8, Source: "vector",
	}
	evidence := &StructuralEvidence{
		HasCallsEdge:    true,
		IsExported:      true,
		IncomingCallCount: 3,
	}

	fv := fe.Extract(candidate, evidence)

	if fv.StructuralSupport == 0 {
		t.Error("expected non-zero structural support")
	}
	if fv.ExportedTarget != 1.0 {
		t.Errorf("expected exported=1.0, got %f", fv.ExportedTarget)
	}
}

func TestFeatureExtractor_ExtractWithLexical(t *testing.T) {
	fe := NewFeatureExtractor()
	candidate := contracts.RetrievalCandidate{
		NodeKey: "func:a", Score: 0.5, Source: "vector",
		Metadata: map[string]any{
			"name":      "HandleUserRequest",
			"signature": "func HandleUserRequest(ctx context.Context)",
		},
	}

	fv := fe.ExtractWithLexical("user request handler", candidate, nil)

	if fv.LexicalOverlap == 0 {
		t.Error("expected non-zero lexical overlap")
	}
}

func TestNormalizeVectorScore(t *testing.T) {
	tests := []struct {
		name     string
		score    float64
		source   string
		expected float64
	}{
		{"vector low", 0.15, "vector", 0.0},
		{"vector mid", 0.475, "vector", 0.5},
		{"vector high", 0.80, "vector", 1.0},
		{"text low", 0, "text", 0.0},
		{"text mid", 5.0, "text", 0.5},
		{"text high", 10, "text", 1.0},
		{"hybrid", 0.015, "hybrid", 0.5},
		{"graph", 0.5, "graph", 0.5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeVectorScore(tt.score, tt.source)
			if math.Abs(got-tt.expected) > 0.01 {
				t.Errorf("normalizeVectorScore(%f, %q) = %f, want ~%f", tt.score, tt.source, got, tt.expected)
			}
		})
	}
}

func TestComputeStructuralSupport(t *testing.T) {
	// No evidence
	got := computeStructuralSupport(&StructuralEvidence{})
	if got != 0 {
		t.Errorf("expected 0 for empty evidence, got %f", got)
	}

	// Strong evidence
	got = computeStructuralSupport(&StructuralEvidence{
		HasCallsEdge:   true,
		HasHasStepEdge: true,
		IsExported:     true,
	})
	if got < 0.5 {
		t.Errorf("expected high structural support, got %f", got)
	}

	// Capped at 1
	got = computeStructuralSupport(&StructuralEvidence{
		HasCallsEdge:      true,
		HasHasStepEdge:    true,
		HasContainsEdge:   true,
		HasOwnershipEdge:  true,
		IncomingCallCount: 100,
		OutgoingCallCount: 100,
		IsExported:        true,
		HasDocstring:      true,
	})
	if got > 1.0 {
		t.Errorf("expected capped at 1.0, got %f", got)
	}
}

func TestComputeLexicalOverlap(t *testing.T) {
	tests := []struct {
		a, b     string
		expected float64
	}{
		{"", "", 0},
		{"hello", "", 0},
		{"hello world", "hello world", 1.0},
		{"user handler request", "handle user request", 0.6667}, // 2/3 overlap
		{"abc def", "xyz ghi", 0},
	}

	for _, tt := range tests {
		got := computeLexicalOverlap(tt.a, tt.b)
		if math.Abs(got-tt.expected) > 0.01 {
			t.Errorf("computeLexicalOverlap(%q, %q) = %f, want ~%f", tt.a, tt.b, got, tt.expected)
		}
	}
}

func TestTokenize(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{"", nil},
		{"hello", []string{"hello"}},
		{"hello world", []string{"hello", "world"}},
		{"HandleUserRequest", []string{"handle", "user", "request"}},
		{"func:main.go#Start", []string{"func", "main", "go", "start"}},
		{"a b c", nil}, // all single chars filtered
	}

	for _, tt := range tests {
		got := tokenize(tt.input)
		if len(got) != len(tt.expected) {
			t.Errorf("tokenize(%q) = %v, want %v", tt.input, got, tt.expected)
			continue
		}
		for i, tok := range got {
			if tok != tt.expected[i] {
				t.Errorf("tokenize(%q)[%d] = %q, want %q", tt.input, i, tok, tt.expected[i])
			}
		}
	}
}

func TestClamp(t *testing.T) {
	if clamp(-1, 0, 1) != 0 {
		t.Error("expected clamped to 0")
	}
	if clamp(2, 0, 1) != 1 {
		t.Error("expected clamped to 1")
	}
	if clamp(0.5, 0, 1) != 0.5 {
		t.Error("expected 0.5")
	}
}
