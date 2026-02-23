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

func TestDetectFlowCandidates_MixedPatterns(t *testing.T) {
	content := "The `AuthHandler` processes POST /api/login and calls `ValidateToken`."
	candidates := DetectFlowCandidates(content)

	names := map[string]string{} // name → pattern
	for _, c := range candidates {
		names[c.name] = c.patterns[0]
	}

	if names["AuthHandler"] != "backtick" {
		t.Error("expected AuthHandler with backtick pattern")
	}
	if names["ValidateToken"] != "backtick" {
		t.Error("expected ValidateToken with backtick pattern")
	}
	if names["POST /api/login"] != "http_route" {
		t.Error("expected POST /api/login with http_route pattern")
	}
}

func TestDetectFlowCandidates_AllHTTPMethods(t *testing.T) {
	methods := []string{"GET", "POST", "PUT", "DELETE", "PATCH"}
	for _, method := range methods {
		content := method + " /api/test processes data."
		candidates := DetectFlowCandidates(content)
		found := false
		for _, c := range candidates {
			if c.name == method+" /api/test" {
				found = true
			}
		}
		if !found {
			t.Errorf("expected to detect %s /api/test", method)
		}
	}
}

func TestDetectFlowCandidates_CaseInsensitiveHTTP(t *testing.T) {
	content := "The get /api/items endpoint returns data."
	candidates := DetectFlowCandidates(content)
	found := false
	for _, c := range candidates {
		if c.name == "GET /api/items" {
			found = true
		}
	}
	if !found {
		t.Error("expected case-insensitive HTTP method detection")
	}
}

func TestNewFlowLinker_DefaultScope(t *testing.T) {
	fl := NewFlowLinker(nil)
	if fl.scopeID != "main" {
		t.Errorf("expected default scopeID 'main', got %q", fl.scopeID)
	}
}

func TestFlowLinker_SetScope(t *testing.T) {
	fl := NewFlowLinker(nil)
	fl.SetScope("pr-99")
	if fl.scopeID != "pr-99" {
		t.Errorf("expected scopeID 'pr-99', got %q", fl.scopeID)
	}
}

func TestDetectFlowCandidates_DottedBacktick(t *testing.T) {
	content := "The `auth.HandleLogin` function validates credentials."
	candidates := DetectFlowCandidates(content)
	found := false
	for _, c := range candidates {
		if c.name == "auth.HandleLogin" {
			found = true
		}
	}
	if !found {
		t.Error("expected to detect dotted backtick reference auth.HandleLogin")
	}
}

func TestDetectFlowCandidates_URLPathVariants(t *testing.T) {
	content := "DELETE /api/users/{id} removes a user. PATCH /api/items/:itemId updates."
	candidates := DetectFlowCandidates(content)
	names := map[string]bool{}
	for _, c := range candidates {
		names[c.name] = true
	}
	if !names["DELETE /api/users/{id}"] {
		t.Error("expected DELETE /api/users/{id}")
	}
	if !names["PATCH /api/items/:itemId"] {
		t.Error("expected PATCH /api/items/:itemId")
	}
}
