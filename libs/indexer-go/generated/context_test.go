package generated

import (
	"testing"

	"github.com/context-maximiser/code-graph/libs/core-models-go"
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
