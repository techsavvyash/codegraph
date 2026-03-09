package retrieval

import (
	"context"
	"errors"
	"testing"

	models "github.com/context-maximiser/code-graph/libs/core-models-go"
	"github.com/context-maximiser/code-graph/libs/intelligence-go/contracts"
)

// --- Overlay Filter Tests ---

func TestDefaultOverlayFilter_MainScope_PassThrough(t *testing.T) {
	f := NewDefaultOverlayFilter(nil)
	candidates := []contracts.RetrievalCandidate{
		{NodeKey: "func:a", ScopeID: "main", Score: 0.9},
		{NodeKey: "func:b", ScopeID: "main", Score: 0.7},
	}

	result, err := f.FilterCandidates(context.Background(), candidates, models.DefaultScope())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 candidates (pass-through for main), got %d", len(result))
	}
}

func TestDefaultOverlayFilter_OverlayWinsOverMain(t *testing.T) {
	tc := &mockTombstoneChecker{tombstoned: map[string]bool{}}
	f := NewDefaultOverlayFilter(tc)

	candidates := []contracts.RetrievalCandidate{
		{NodeKey: "func:shared", ScopeID: "pr-42", Scope: "pr", Score: 0.8},
		{NodeKey: "func:shared", ScopeID: "main", Scope: "main", Score: 0.9},
	}

	scope := models.NewPRScope("42")
	result, err := f.FilterCandidates(context.Background(), candidates, scope)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 candidate (overlay wins), got %d", len(result))
	}
	if result[0].ScopeID != "pr-42" {
		t.Errorf("expected overlay candidate (pr-42), got %q", result[0].ScopeID)
	}
}

func TestDefaultOverlayFilter_TombstoneHidesMain(t *testing.T) {
	tc := &mockTombstoneChecker{
		tombstoned: map[string]bool{
			"func:deleted": true,
		},
	}
	f := NewDefaultOverlayFilter(tc)

	candidates := []contracts.RetrievalCandidate{
		{NodeKey: "func:deleted", ScopeID: "main", Scope: "main", Score: 0.9},
		{NodeKey: "func:alive", ScopeID: "main", Scope: "main", Score: 0.7},
	}

	scope := models.NewPRScope("42")
	result, err := f.FilterCandidates(context.Background(), candidates, scope)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 candidate (tombstoned hidden), got %d", len(result))
	}
	if result[0].NodeKey != "func:alive" {
		t.Errorf("expected func:alive, got %q", result[0].NodeKey)
	}
}

func TestDefaultOverlayFilter_MainFallback(t *testing.T) {
	tc := &mockTombstoneChecker{tombstoned: map[string]bool{}}
	f := NewDefaultOverlayFilter(tc)

	candidates := []contracts.RetrievalCandidate{
		{NodeKey: "func:main-only", ScopeID: "main", Scope: "main", Score: 0.8},
	}

	scope := models.NewPRScope("42")
	result, err := f.FilterCandidates(context.Background(), candidates, scope)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 candidate (main fallback), got %d", len(result))
	}
}

func TestDefaultOverlayFilter_ComplexScenario(t *testing.T) {
	tc := &mockTombstoneChecker{
		tombstoned: map[string]bool{
			"func:deleted-in-pr": true,
		},
	}
	f := NewDefaultOverlayFilter(tc)

	candidates := []contracts.RetrievalCandidate{
		// Overlay wins for shared key
		{NodeKey: "func:shared", ScopeID: "pr-42", Scope: "pr", Score: 0.8},
		{NodeKey: "func:shared", ScopeID: "main", Scope: "main", Score: 0.95},
		// Tombstoned in PR
		{NodeKey: "func:deleted-in-pr", ScopeID: "main", Scope: "main", Score: 0.9},
		// Main fallback (not tombstoned)
		{NodeKey: "func:main-only", ScopeID: "main", Scope: "main", Score: 0.7},
		// Overlay only
		{NodeKey: "func:new-in-pr", ScopeID: "pr-42", Scope: "pr", Score: 0.6},
	}

	scope := models.NewPRScope("42")
	result, err := f.FilterCandidates(context.Background(), candidates, scope)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 3 {
		t.Fatalf("expected 3 candidates, got %d", len(result))
	}

	// Verify each result
	expected := map[string]string{
		"func:shared":    "pr-42",     // overlay wins
		"func:main-only": "main",      // main fallback
		"func:new-in-pr": "pr-42",     // overlay only
	}
	for _, c := range result {
		expScope, ok := expected[c.NodeKey]
		if !ok {
			t.Errorf("unexpected candidate: %q", c.NodeKey)
			continue
		}
		if c.ScopeID != expScope {
			t.Errorf("candidate %q: expected scopeID %q, got %q", c.NodeKey, expScope, c.ScopeID)
		}
	}

	// Verify tombstoned candidate is NOT in results
	for _, c := range result {
		if c.NodeKey == "func:deleted-in-pr" {
			t.Error("tombstoned candidate should not appear in results")
		}
	}
}

func TestDefaultOverlayFilter_TombstoneError(t *testing.T) {
	tc := &mockTombstoneChecker{err: errors.New("db error")}
	f := NewDefaultOverlayFilter(tc)

	candidates := []contracts.RetrievalCandidate{
		{NodeKey: "func:a", ScopeID: "main", Scope: "main", Score: 0.9},
	}

	scope := models.NewPRScope("42")
	_, err := f.FilterCandidates(context.Background(), candidates, scope)
	if err == nil {
		t.Fatal("expected error from tombstone checker")
	}
}

func TestDefaultOverlayFilter_NilTombstoneChecker(t *testing.T) {
	// When tombstone checker is nil, main candidates pass through
	f := NewDefaultOverlayFilter(nil)

	candidates := []contracts.RetrievalCandidate{
		{NodeKey: "func:a", ScopeID: "main", Scope: "main", Score: 0.9},
	}

	scope := models.NewPRScope("42")
	result, err := f.FilterCandidates(context.Background(), candidates, scope)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(result))
	}
}

func TestDefaultOverlayFilter_OverlayWinsEvenWithHigherMainScore(t *testing.T) {
	tc := &mockTombstoneChecker{tombstoned: map[string]bool{}}
	f := NewDefaultOverlayFilter(tc)

	// Main has higher score but overlay should still win
	candidates := []contracts.RetrievalCandidate{
		{NodeKey: "func:x", ScopeID: "main", Scope: "main", Score: 0.99},
		{NodeKey: "func:x", ScopeID: "pr-42", Scope: "pr", Score: 0.50},
	}

	scope := models.NewPRScope("42")
	result, err := f.FilterCandidates(context.Background(), candidates, scope)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(result))
	}
	if result[0].ScopeID != "pr-42" {
		t.Errorf("expected overlay to win even with lower score, got scopeID %q", result[0].ScopeID)
	}
}

func TestDefaultOverlayFilter_ScopedPrefixStripping(t *testing.T) {
	tc := &mockTombstoneChecker{tombstoned: map[string]bool{}}
	f := NewDefaultOverlayFilter(tc)

	candidates := []contracts.RetrievalCandidate{
		{NodeKey: "pr-42::func:a", ScopeID: "pr-42", Scope: "pr", Score: 0.8},
		{NodeKey: "func:a", ScopeID: "main", Scope: "main", Score: 0.9},
	}

	scope := models.NewPRScope("42")
	result, err := f.FilterCandidates(context.Background(), candidates, scope)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should dedup to 1 candidate (overlay wins)
	if len(result) != 1 {
		t.Fatalf("expected 1 candidate after dedup, got %d", len(result))
	}
	if result[0].ScopeID != "pr-42" {
		t.Errorf("expected overlay candidate, got %q", result[0].ScopeID)
	}
}

// --- NoopOverlayFilter Tests ---

func TestNoopOverlayFilter_PassThrough(t *testing.T) {
	f := &NoopOverlayFilter{}
	candidates := []contracts.RetrievalCandidate{
		{NodeKey: "func:a", Score: 0.9},
		{NodeKey: "func:b", Score: 0.7},
	}

	result, err := f.FilterCandidates(context.Background(), candidates, models.NewPRScope("42"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 candidates (noop), got %d", len(result))
	}
}

// --- stripScopePrefix Tests ---

func TestStripScopePrefix(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"func:main.go#Start", "func:main.go#Start"},
		{"main::func:main.go#Start", "func:main.go#Start"},
		{"pr-42::func:api.go#Handle", "func:api.go#Handle"},
		{"", ""},
	}

	for _, tt := range tests {
		got := stripScopePrefix(tt.input)
		if got != tt.expected {
			t.Errorf("stripScopePrefix(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

// --- Full Orchestrator + Overlay Integration ---

func TestOrchestrator_WithOverlayFilter_PRScope(t *testing.T) {
	tc := &mockTombstoneChecker{
		tombstoned: map[string]bool{"func:removed": true},
	}

	// Graph store returns results with their actual scopeIDs.
	// "func:alive" is from main (not tombstoned), "func:removed" is from main (tombstoned).
	graphStore := &mockGraphStore{
		results: []GraphResult{
			{NodeKey: "func:alive", NodeType: "Function", ScopeID: "main", Score: 0.9},
			{NodeKey: "func:removed", NodeType: "Function", ScopeID: "main", Score: 0.8},
		},
	}

	orch := NewOrchestrator(
		WithGraphAdapter(NewGraphAdapter(graphStore)),
		WithOverlayFilter(NewDefaultOverlayFilter(tc)),
	)

	scope := models.NewPRScope("42")
	candidates, diag, err := orch.Retrieve(context.Background(), "test", scope, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate (tombstoned hidden), got %d", len(candidates))
	}
	if candidates[0].NodeKey != "func:alive" {
		t.Errorf("expected func:alive, got %q", candidates[0].NodeKey)
	}
	if !diag.OverlayApplied {
		t.Error("expected overlayApplied=true")
	}
}

func TestOrchestrator_WithOverlayFilter_MainScope_Skipped(t *testing.T) {
	tc := &mockTombstoneChecker{
		tombstoned: map[string]bool{"func:a": true},
	}

	graphStore := &mockGraphStore{
		results: []GraphResult{
			{NodeKey: "func:a", Score: 0.9},
		},
	}

	orch := NewOrchestrator(
		WithGraphAdapter(NewGraphAdapter(graphStore)),
		WithOverlayFilter(NewDefaultOverlayFilter(tc)),
	)

	// Main scope should NOT apply overlay filter
	candidates, diag, err := orch.Retrieve(context.Background(), "test", models.DefaultScope(), 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate (no overlay for main), got %d", len(candidates))
	}
	if diag.OverlayApplied {
		t.Error("expected overlayApplied=false for main scope")
	}
}
