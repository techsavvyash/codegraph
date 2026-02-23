package search

import (
	"testing"
)

func TestHybridSearchManager_SetScope(t *testing.T) {
	hsm := &HybridSearchManager{}
	hsm.SetScope("pr-42")
	if hsm.scopeID != "pr-42" {
		t.Errorf("expected scopeID 'pr-42', got %q", hsm.scopeID)
	}
}

func TestHybridSearchManager_SetScope_Empty(t *testing.T) {
	hsm := &HybridSearchManager{scopeID: "pr-42"}
	hsm.SetScope("")
	if hsm.scopeID != "" {
		t.Errorf("expected empty scopeID, got %q", hsm.scopeID)
	}
}

func TestHybridSearchManager_WithTextStore(t *testing.T) {
	hsm := &HybridSearchManager{}
	if hsm.textStore != nil {
		t.Error("expected nil textStore by default")
	}

	// Use a mock that satisfies the interface — we don't need a real one.
	// Just verify WithTextStore wires the field.
	// Note: We can't import textindex in this test without a real mock,
	// but we can verify the method returns *HybridSearchManager for chaining.
	result := hsm.WithTextStore(nil)
	if result != hsm {
		t.Error("WithTextStore should return the same manager for chaining")
	}
}
