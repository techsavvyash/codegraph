package generated

import (
	"encoding/json"
	"testing"

	"github.com/context-maximiser/code-graph/libs/core-models-go"
	"github.com/context-maximiser/code-graph/libs/intelligence-go/contracts"
)

func TestDocTypeConstants(t *testing.T) {
	if DocTypePRSummary != "pr_summary" {
		t.Errorf("expected pr_summary, got %s", DocTypePRSummary)
	}
	if DocTypeFlowSummary != "flow_summary" {
		t.Errorf("expected flow_summary, got %s", DocTypeFlowSummary)
	}
	if DocTypeDocstringSuggestion != "docstring_suggestion" {
		t.Errorf("expected docstring_suggestion, got %s", DocTypeDocstringSuggestion)
	}
}

func TestNewContextGenerator(t *testing.T) {
	gen := NewContextGenerator(nil)
	if gen == nil {
		t.Fatal("expected non-nil generator")
	}
	if gen.scopeCtx.Scope != "main" {
		t.Errorf("expected default scope 'main', got %s", gen.scopeCtx.Scope)
	}
}

func TestContextGenerator_SetScope(t *testing.T) {
	gen := NewContextGenerator(nil)
	prScope := models.NewPRScope("42")
	gen.SetScope(prScope)

	if gen.scopeCtx.Scope != "pr" {
		t.Errorf("expected scope 'pr', got %s", gen.scopeCtx.Scope)
	}
	if gen.scopeCtx.ScopeID != "pr-42" {
		t.Errorf("expected scopeID 'pr-42', got %s", gen.scopeCtx.ScopeID)
	}
}

func TestPullRequestNodeKey(t *testing.T) {
	key := models.PullRequestNodeKey("123")
	if key != "pr:123" {
		t.Errorf("expected pr:123, got %s", key)
	}
}

func TestGeneratedDocNodeKeys(t *testing.T) {
	// PR summary
	prKey := models.GeneratedDocNodeKey(DocTypePRSummary, "pr:123")
	expected := "gendoc:pr_summary:pr:123"
	if prKey != expected {
		t.Errorf("expected %s, got %s", expected, prKey)
	}

	// Flow summary
	flowKey := models.GeneratedDocNodeKey(DocTypeFlowSummary, "flow:api:api:GET:/users")
	expected2 := "gendoc:flow_summary:flow:api:api:GET:/users"
	if flowKey != expected2 {
		t.Errorf("expected %s, got %s", expected2, flowKey)
	}

	// Docstring suggestion
	dsKey := models.GeneratedDocNodeKey(DocTypeDocstringSuggestion, "func:pkg/foo.go#Bar()")
	expected3 := "gendoc:docstring_suggestion:func:pkg/foo.go#Bar()"
	if dsKey != expected3 {
		t.Errorf("expected %s, got %s", expected3, dsKey)
	}

	// All three must be unique
	if prKey == flowKey || prKey == dsKey || flowKey == dsKey {
		t.Error("generated doc node keys should be unique across types")
	}
}

func TestPullRequestModel(t *testing.T) {
	pr := models.PullRequest{
		PRID:       "123",
		Title:      "Add feature X",
		Author:     "dev",
		BaseBranch: "main",
		HeadBranch: "feat/x",
		Status:     "open",
	}

	if pr.PRID != "123" {
		t.Errorf("unexpected PRID: %s", pr.PRID)
	}
	if pr.Status != "open" {
		t.Errorf("unexpected Status: %s", pr.Status)
	}
}

func TestGeneratedDocModel(t *testing.T) {
	doc := models.GeneratedDoc{
		Type:       DocTypePRSummary,
		Title:      "PR #123 Summary",
		Content:    "This PR adds...",
		Model:      "claude-sonnet-4-20250514",
		SourceType: "pull_request",
		SourceKey:  "pr:123",
	}

	if doc.Type != DocTypePRSummary {
		t.Errorf("unexpected Type: %s", doc.Type)
	}
	if doc.Model != "claude-sonnet-4-20250514" {
		t.Errorf("unexpected Model: %s", doc.Model)
	}
}

// ---------------------------------------------------------------------------
// Doc type constant stability tests
// ---------------------------------------------------------------------------

func TestDocTypeConstantsAreDistinct(t *testing.T) {
	types := []string{DocTypePRSummary, DocTypeFlowSummary, DocTypeDocstringSuggestion}
	seen := map[string]bool{}
	for _, dt := range types {
		if seen[dt] {
			t.Errorf("duplicate doc type constant: %s", dt)
		}
		seen[dt] = true
	}
}

func TestDocTypeConstantsNotEmpty(t *testing.T) {
	for _, dt := range []string{DocTypePRSummary, DocTypeFlowSummary, DocTypeDocstringSuggestion} {
		if dt == "" {
			t.Error("doc type constant must not be empty")
		}
	}
}

// ---------------------------------------------------------------------------
// ContextGenerator scope behavior
// ---------------------------------------------------------------------------

func TestContextGenerator_DefaultScopeIsMain(t *testing.T) {
	gen := NewContextGenerator(nil)
	if gen.scopeCtx.ScopeID != "main" {
		t.Errorf("expected default scopeID 'main', got %q", gen.scopeCtx.ScopeID)
	}
}

func TestContextGenerator_SetScopePR(t *testing.T) {
	gen := NewContextGenerator(nil)
	gen.SetScope(models.NewPRScope("100"))
	if gen.scopeCtx.ScopeID != "pr-100" {
		t.Errorf("expected scopeID 'pr-100', got %q", gen.scopeCtx.ScopeID)
	}
	if gen.scopeCtx.Scope != "pr" {
		t.Errorf("expected scope 'pr', got %q", gen.scopeCtx.Scope)
	}
}

func TestContextGenerator_SetScopeWithTenantAndRepo(t *testing.T) {
	gen := NewContextGenerator(nil)
	sc := models.DefaultScope()
	sc.TenantID = "org-abc"
	sc.Repo = "my-repo"
	gen.SetScope(sc)

	if gen.scopeCtx.TenantID != "org-abc" {
		t.Errorf("expected TenantID 'org-abc', got %q", gen.scopeCtx.TenantID)
	}
	if gen.scopeCtx.Repo != "my-repo" {
		t.Errorf("expected Repo 'my-repo', got %q", gen.scopeCtx.Repo)
	}
}

// ---------------------------------------------------------------------------
// GeneratedDocNodeKey determinism tests
// ---------------------------------------------------------------------------

func TestGeneratedDocNodeKey_Deterministic(t *testing.T) {
	for i := 0; i < 100; i++ {
		a := models.GeneratedDocNodeKey(DocTypePRSummary, "pr:42")
		b := models.GeneratedDocNodeKey(DocTypePRSummary, "pr:42")
		if a != b {
			t.Fatal("GeneratedDocNodeKey is not deterministic")
		}
	}
}

func TestGeneratedDocNodeKey_DifferentSourcesDiffer(t *testing.T) {
	k1 := models.GeneratedDocNodeKey(DocTypePRSummary, "pr:1")
	k2 := models.GeneratedDocNodeKey(DocTypePRSummary, "pr:2")
	if k1 == k2 {
		t.Error("different sources should produce different keys")
	}
}

func TestGeneratedDocNodeKey_DifferentTypesDiffer(t *testing.T) {
	k1 := models.GeneratedDocNodeKey(DocTypePRSummary, "pr:1")
	k2 := models.GeneratedDocNodeKey(DocTypeFlowSummary, "pr:1")
	if k1 == k2 {
		t.Error("different doc types should produce different keys")
	}
}

// ---------------------------------------------------------------------------
// marshalCitationProps tests
// ---------------------------------------------------------------------------

func TestMarshalCitationProps_NilGenResult(t *testing.T) {
	props := map[string]any{"type": "test"}
	marshalCitationProps(props, nil)
	if _, ok := props["citations"]; ok {
		t.Error("nil genResult should not add citations")
	}
	if _, ok := props["statements"]; ok {
		t.Error("nil genResult should not add statements")
	}
}

func TestMarshalCitationProps_EmptyCitations(t *testing.T) {
	props := map[string]any{"type": "test"}
	marshalCitationProps(props, &contracts.GenerationResult{
		Content:   "test",
		Citations: nil,
	})
	if _, ok := props["citations"]; ok {
		t.Error("empty citations should not add citations prop")
	}
}

func TestMarshalCitationProps_WithCitations(t *testing.T) {
	props := map[string]any{"type": "test"}
	genResult := &contracts.GenerationResult{
		Content: "Statement A.\nStatement B.",
		Citations: []contracts.Citation{
			{
				StatementIndex: 0,
				EvidenceRefs: []contracts.EvidenceRef{
					{Kind: "citation", NodeKey: "func:a", Score: 0.95},
				},
			},
			{
				StatementIndex: 1,
				EvidenceRefs: []contracts.EvidenceRef{
					{Kind: "citation", NodeKey: "func:b", Score: 0.88},
					{Kind: "graph_edge", NodeKey: "func:c", Score: 0.72},
				},
			},
		},
		Model: "test-model",
	}

	marshalCitationProps(props, genResult)

	// Check citations is valid JSON
	citationsRaw, ok := props["citations"].(string)
	if !ok {
		t.Fatal("citations prop should be a string")
	}
	var citations []contracts.Citation
	if err := json.Unmarshal([]byte(citationsRaw), &citations); err != nil {
		t.Fatalf("citations should be valid JSON: %v", err)
	}
	if len(citations) != 2 {
		t.Errorf("expected 2 citations, got %d", len(citations))
	}
	if len(citations[0].EvidenceRefs) != 1 {
		t.Errorf("expected 1 evidence ref for first citation, got %d", len(citations[0].EvidenceRefs))
	}
	if len(citations[1].EvidenceRefs) != 2 {
		t.Errorf("expected 2 evidence refs for second citation, got %d", len(citations[1].EvidenceRefs))
	}

	// Check statements is valid JSON
	stmtsRaw, ok := props["statements"].(string)
	if !ok {
		t.Fatal("statements prop should be a string")
	}
	var stmts []map[string]any
	if err := json.Unmarshal([]byte(stmtsRaw), &stmts); err != nil {
		t.Fatalf("statements should be valid JSON: %v", err)
	}
	if len(stmts) != 2 {
		t.Errorf("expected 2 statement entries, got %d", len(stmts))
	}
}

func TestGeneratedDocModel_WithCitations(t *testing.T) {
	doc := models.GeneratedDoc{
		Type:       DocTypePRSummary,
		Title:      "PR #123 Summary",
		Content:    "This PR adds feature X.",
		Model:      "test-model",
		SourceType: "pull_request",
		SourceKey:  "pr:123",
		Statements: `[{"index":0,"refs":2}]`,
		Citations:  `[{"statementIndex":0,"evidenceRefs":[{"kind":"citation","nodeKey":"func:a"}]}]`,
	}

	if doc.Statements == "" {
		t.Error("expected non-empty Statements")
	}
	if doc.Citations == "" {
		t.Error("expected non-empty Citations")
	}

	// Verify Citations field is valid JSON
	var citations []contracts.Citation
	if err := json.Unmarshal([]byte(doc.Citations), &citations); err != nil {
		t.Fatalf("Citations field should be valid JSON: %v", err)
	}
	if len(citations) != 1 {
		t.Errorf("expected 1 citation, got %d", len(citations))
	}
	if citations[0].EvidenceRefs[0].NodeKey != "func:a" {
		t.Errorf("expected nodeKey func:a, got %s", citations[0].EvidenceRefs[0].NodeKey)
	}
}
