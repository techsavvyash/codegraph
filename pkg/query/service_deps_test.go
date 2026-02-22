package query

import (
	"testing"

	"github.com/context-maximiser/code-graph/pkg/models"
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

// ---------------------------------------------------------------------------
// P4: Verify CallsServiceRel constant is available
// ---------------------------------------------------------------------------

func TestCallsServiceRelConstant(t *testing.T) {
	if string(models.CallsServiceRel) != "CALLS_SERVICE" {
		t.Errorf("expected CALLS_SERVICE, got %s", models.CallsServiceRel)
	}
}

// ---------------------------------------------------------------------------
// P4: Verify NewServiceDepsQuery constructor
// ---------------------------------------------------------------------------

func TestNewServiceDepsQuery(t *testing.T) {
	q := NewServiceDepsQuery(nil)
	if q == nil {
		t.Fatal("expected non-nil ServiceDepsQuery")
	}
	if q.client != nil {
		t.Error("expected nil client in test")
	}
}

// ---------------------------------------------------------------------------
// P4: Verify InterServiceEdge uses sdk_call_inference source naming
// ---------------------------------------------------------------------------

func TestInterServiceEdge_SDKSource(t *testing.T) {
	dep := InterServiceEdge{
		FromService: "api-gateway",
		ToService:   "user-service",
		RelType:     string(models.CallsServiceRel),
		Confidence:  0.7,
		Source:      "sdk_call_inference",
		Evidence:    []string{"/api/users", "/api/users/profile"},
	}

	if dep.RelType != "CALLS_SERVICE" {
		t.Errorf("expected RelType CALLS_SERVICE, got %s", dep.RelType)
	}
	if dep.Source != "sdk_call_inference" {
		t.Errorf("expected Source sdk_call_inference, got %s", dep.Source)
	}
	if len(dep.Evidence) != 2 {
		t.Errorf("expected 2 evidence items, got %d", len(dep.Evidence))
	}
}
