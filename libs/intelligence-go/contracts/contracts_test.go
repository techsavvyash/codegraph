package contracts

import (
	"encoding/json"
	"testing"
	"time"
)

func TestRetrievalCandidateRoundTrip(t *testing.T) {
	orig := RetrievalCandidate{
		NodeKey:  "func:main.go#Start",
		NodeType: "Function",
		Scope:    "main",
		ScopeID:  "main",
		Score:    0.95,
		Source:   "graph",
		Metadata: map[string]any{"depth": float64(2)},
	}
	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got RetrievalCandidate
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.NodeKey != orig.NodeKey {
		t.Errorf("NodeKey = %q, want %q", got.NodeKey, orig.NodeKey)
	}
	if got.Score != orig.Score {
		t.Errorf("Score = %v, want %v", got.Score, orig.Score)
	}
	if got.Source != orig.Source {
		t.Errorf("Source = %q, want %q", got.Source, orig.Source)
	}
}

func TestEvidenceRefRoundTrip(t *testing.T) {
	orig := EvidenceRef{
		Kind:    "graph_edge",
		NodeKey: "func:pkg#Foo",
		Detail:  "CALLS relationship",
		Score:   0.8,
	}
	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got EvidenceRef
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Kind != orig.Kind {
		t.Errorf("Kind = %q, want %q", got.Kind, orig.Kind)
	}
	if got.NodeKey != orig.NodeKey {
		t.Errorf("NodeKey = %q, want %q", got.NodeKey, orig.NodeKey)
	}
}

func TestInferenceResultRoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	orig := InferenceResult{
		SourceKey:    "func:a.go#A",
		TargetKey:    "func:b.go#B",
		RelationType: "CALLS",
		Confidence:   0.85,
		Strategy:     "structural",
		Reasons:      []string{"co-located", "name similarity"},
		EvidenceRefs: []EvidenceRef{{Kind: "structural", Detail: "same package"}},
		CreatedAt:    now,
	}
	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got InferenceResult
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.SourceKey != orig.SourceKey {
		t.Errorf("SourceKey = %q, want %q", got.SourceKey, orig.SourceKey)
	}
	if got.Confidence != orig.Confidence {
		t.Errorf("Confidence = %v, want %v", got.Confidence, orig.Confidence)
	}
	if len(got.Reasons) != len(orig.Reasons) {
		t.Errorf("Reasons len = %d, want %d", len(got.Reasons), len(orig.Reasons))
	}
	if !got.CreatedAt.Equal(orig.CreatedAt) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, orig.CreatedAt)
	}
}

func TestContextBundleRoundTrip(t *testing.T) {
	orig := ContextBundle{
		Anchors: []RetrievalCandidate{
			{NodeKey: "func:main.go#Run", NodeType: "Function", Score: 1.0, Source: "graph"},
		},
		Template:  "default",
		MaxTokens: 4096,
		Scope:     "main",
		ScopeID:   "main",
	}
	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got ContextBundle
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Anchors) != 1 {
		t.Fatalf("Anchors len = %d, want 1", len(got.Anchors))
	}
	if got.MaxTokens != orig.MaxTokens {
		t.Errorf("MaxTokens = %d, want %d", got.MaxTokens, orig.MaxTokens)
	}
}

func TestGenerationResultRoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	orig := GenerationResult{
		Content:   "Function A calls B.",
		Citations: []Citation{{StatementIndex: 0, EvidenceRefs: []EvidenceRef{{Kind: "graph_edge"}}}},
		Model:     "gpt-4",
		Template:  "default",
		CreatedAt: now,
	}
	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got GenerationResult
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Content != orig.Content {
		t.Errorf("Content = %q, want %q", got.Content, orig.Content)
	}
	if len(got.Citations) != 1 {
		t.Fatalf("Citations len = %d, want 1", len(got.Citations))
	}
}

func TestCitationRoundTrip(t *testing.T) {
	orig := Citation{
		StatementIndex: 3,
		EvidenceRefs:   []EvidenceRef{{Kind: "text_match", Detail: "line 42"}},
	}
	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got Citation
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.StatementIndex != orig.StatementIndex {
		t.Errorf("StatementIndex = %d, want %d", got.StatementIndex, orig.StatementIndex)
	}
}

func TestVerificationResultRoundTrip(t *testing.T) {
	orig := VerificationResult{
		Passed:            false,
		TotalStatements:   5,
		CitedStatements:   3,
		UnsupportedClaims: []int{2, 4},
		Errors:            []string{"claim 2 has no evidence"},
	}
	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got VerificationResult
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Passed != orig.Passed {
		t.Errorf("Passed = %v, want %v", got.Passed, orig.Passed)
	}
	if got.TotalStatements != orig.TotalStatements {
		t.Errorf("TotalStatements = %d, want %d", got.TotalStatements, orig.TotalStatements)
	}
	if len(got.UnsupportedClaims) != len(orig.UnsupportedClaims) {
		t.Errorf("UnsupportedClaims len = %d, want %d", len(got.UnsupportedClaims), len(orig.UnsupportedClaims))
	}
}

func TestStageConstantsNonEmptyAndDistinct(t *testing.T) {
	stages := []string{
		StageRetrieval,
		StageInference,
		StageBundle,
		StageGeneration,
		StageVerification,
	}
	seen := make(map[string]bool)
	for _, s := range stages {
		if s == "" {
			t.Error("stage constant is empty")
		}
		if seen[s] {
			t.Errorf("duplicate stage constant: %q", s)
		}
		seen[s] = true
	}
}

func TestBackwardCompatibleDefaults(t *testing.T) {
	var rc RetrievalCandidate
	if rc.Score != 0 {
		t.Errorf("default Score = %v, want 0", rc.Score)
	}
	if rc.Metadata != nil {
		t.Errorf("default Metadata = %v, want nil", rc.Metadata)
	}

	var ir InferenceResult
	if ir.Confidence != 0 {
		t.Errorf("default Confidence = %v, want 0", ir.Confidence)
	}
	if ir.Reasons != nil {
		t.Errorf("default Reasons = %v, want nil", ir.Reasons)
	}
	if !ir.CreatedAt.IsZero() {
		t.Errorf("default CreatedAt = %v, want zero", ir.CreatedAt)
	}

	var vr VerificationResult
	if vr.Passed {
		t.Error("default Passed = true, want false")
	}
	if vr.UnsupportedClaims != nil {
		t.Errorf("default UnsupportedClaims = %v, want nil", vr.UnsupportedClaims)
	}

	// Omitted optional fields should unmarshal to zero values
	data := []byte(`{"nodeKey":"k","nodeType":"Function","scope":"main","scopeId":"main","score":0.5,"source":"graph"}`)
	var rc2 RetrievalCandidate
	if err := json.Unmarshal(data, &rc2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if rc2.Metadata != nil {
		t.Errorf("omitted Metadata = %v, want nil", rc2.Metadata)
	}
}
