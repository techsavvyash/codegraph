package search

import (
	"testing"
)

func TestDetectFlowCandidates_BacktickPatterns(t *testing.T) {
	content := "This module calls `HandleCreateUser` and `ValidateInput` to process requests."
	candidates := DetectFlowCandidates(content)
	if len(candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(candidates))
	}
	names := map[string]bool{}
	for _, c := range candidates {
		names[c.name] = true
		if c.patterns[0] != "backtick" {
			t.Errorf("expected backtick pattern, got %s", c.patterns[0])
		}
	}
	if !names["HandleCreateUser"] || !names["ValidateInput"] {
		t.Errorf("unexpected candidates: %v", candidates)
	}
}

func TestDetectFlowCandidates_HTTPRoutes(t *testing.T) {
	content := "The endpoint POST /api/users creates a new user account."
	candidates := DetectFlowCandidates(content)
	found := false
	for _, c := range candidates {
		if c.name == "POST /api/users" {
			found = true
			if c.patterns[0] != "http_route" {
				t.Errorf("expected http_route pattern, got %s", c.patterns[0])
			}
		}
	}
	if !found {
		t.Error("expected to find POST /api/users candidate")
	}
}

func TestDetectFlowCandidates_Empty(t *testing.T) {
	candidates := DetectFlowCandidates("No code references here.")
	if len(candidates) != 0 {
		t.Errorf("expected 0 candidates, got %d", len(candidates))
	}
}

func TestDetectFlowCandidates_Dedup(t *testing.T) {
	content := "Call `doWork` then call `doWork` again."
	candidates := DetectFlowCandidates(content)
	if len(candidates) != 1 {
		t.Errorf("expected 1 candidate (deduped), got %d", len(candidates))
	}
}
