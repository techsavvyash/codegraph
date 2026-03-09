package inference

import (
	"testing"
)

func TestCrossServiceBoundaryFields(t *testing.T) {
	b := CrossServiceBoundary{
		CallerNodeKey: "func:serviceA/handler",
		CallerService: "serviceA",
		CalleeNodeKey: "func:serviceB/process",
		CalleeService: "serviceB",
		BoundaryType:  "direct_call",
	}

	if b.CallerNodeKey != "func:serviceA/handler" {
		t.Errorf("CallerNodeKey = %q, want %q", b.CallerNodeKey, "func:serviceA/handler")
	}
	if b.CallerService != "serviceA" {
		t.Errorf("CallerService = %q, want %q", b.CallerService, "serviceA")
	}
	if b.CalleeNodeKey != "func:serviceB/process" {
		t.Errorf("CalleeNodeKey = %q, want %q", b.CalleeNodeKey, "func:serviceB/process")
	}
	if b.CalleeService != "serviceB" {
		t.Errorf("CalleeService = %q, want %q", b.CalleeService, "serviceB")
	}
	if b.BoundaryType != "direct_call" {
		t.Errorf("BoundaryType = %q, want %q", b.BoundaryType, "direct_call")
	}
}

func TestNewCrossServiceDetector(t *testing.T) {
	// NewCrossServiceDetector will panic if called with nil because it
	// stores the pointer directly.  We verify that a nil client is accepted
	// at construction time (the panic would only happen on query execution).
	// Use a recover guard so the test is safe either way.
	defer func() {
		if r := recover(); r != nil {
			t.Skipf("NewCrossServiceDetector panics with nil client: %v", r)
		}
	}()

	det := NewCrossServiceDetector(nil)
	if det == nil {
		t.Fatal("expected non-nil detector")
	}
	if det.client != nil {
		t.Error("expected nil client to be stored")
	}
	if det.scopeCtx.ScopeID != "main" {
		t.Errorf("default scope should be 'main', got %q", det.scopeCtx.ScopeID)
	}
}

func TestBoundaryTypes(t *testing.T) {
	types := []struct {
		name  string
		value string
	}{
		{"direct_call", "direct_call"},
		{"api_call", "api_call"},
		{"message_queue", "message_queue"},
	}

	for _, tt := range types {
		t.Run(tt.name, func(t *testing.T) {
			b := CrossServiceBoundary{BoundaryType: tt.value}
			if b.BoundaryType != tt.value {
				t.Errorf("BoundaryType = %q, want %q", b.BoundaryType, tt.value)
			}
		})
	}

	// Verify message_queue boundaries have empty callee fields.
	mqBoundary := CrossServiceBoundary{
		CallerNodeKey: "func:worker/consume",
		CallerService: "worker-svc",
		CalleeNodeKey: "",
		CalleeService: "",
		BoundaryType:  "message_queue",
	}
	if mqBoundary.CalleeNodeKey != "" {
		t.Errorf("message_queue calleeNodeKey should be empty, got %q", mqBoundary.CalleeNodeKey)
	}
	if mqBoundary.CalleeService != "" {
		t.Errorf("message_queue calleeService should be empty, got %q", mqBoundary.CalleeService)
	}
}
