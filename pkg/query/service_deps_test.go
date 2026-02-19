package query

import (
	"testing"
)

func TestInterServiceEdge_Fields(t *testing.T) {
	dep := InterServiceEdge{
		FromService: "auth-service",
		ToService:   "user-service",
		RelType:     "CALLS_SERVICE",
		Confidence:  0.85,
		Source:      "http_call_inference",
		Evidence:    []string{"http://user-service/api/users"},
	}

	if dep.FromService != "auth-service" {
		t.Errorf("expected FromService 'auth-service', got %s", dep.FromService)
	}
	if dep.ToService != "user-service" {
		t.Errorf("expected ToService 'user-service', got %s", dep.ToService)
	}
	if dep.Confidence != 0.85 {
		t.Errorf("expected Confidence 0.85, got %f", dep.Confidence)
	}
	if len(dep.Evidence) != 1 {
		t.Errorf("expected 1 evidence item, got %d", len(dep.Evidence))
	}
}

func TestStrVal(t *testing.T) {
	m := map[string]any{
		"name":  "test",
		"count": 42,
	}

	if strVal(m, "name") != "test" {
		t.Error("expected 'test'")
	}
	if strVal(m, "count") != "" {
		t.Error("expected empty string for non-string value")
	}
	if strVal(m, "missing") != "" {
		t.Error("expected empty string for missing key")
	}
}
