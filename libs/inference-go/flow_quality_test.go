package inference

import (
	"testing"
)

func TestTraversalBudget_IsNodeAllowed(t *testing.T) {
	budget := DefaultTraversalBudget

	if !budget.IsNodeAllowed("Function") {
		t.Error("expected Function to be allowed")
	}
	if !budget.IsNodeAllowed("Method") {
		t.Error("expected Method to be allowed")
	}
	if budget.IsNodeAllowed("Variable") {
		t.Error("expected Variable to NOT be allowed")
	}

	// Empty allowed list means all allowed
	noBudget := TraversalBudget{}
	if !noBudget.IsNodeAllowed("anything") {
		t.Error("expected all allowed when AllowedNodeTypes is empty")
	}
}

func TestTraversalBudget_IsNameBlocked(t *testing.T) {
	budget := DefaultTraversalBudget

	if !budget.IsNameBlocked("log") {
		t.Error("expected 'log' to be blocked")
	}
	if !budget.IsNameBlocked("print") {
		t.Error("expected 'print' to be blocked")
	}
	if budget.IsNameBlocked("ProcessData") {
		t.Error("expected 'ProcessData' to NOT be blocked")
	}

	// Empty blocked list means nothing blocked
	noBudget := TraversalBudget{}
	if noBudget.IsNameBlocked("anything") {
		t.Error("expected nothing blocked when BlockedPatterns is empty")
	}
}

func TestFlowDeduplicator_Deduplicate(t *testing.T) {
	dedup := NewFlowDeduplicator()

	steps := []FlowStepInfo{
		{NodeKey: "func:a", Name: "Start", NodeType: "Function", Order: 0},
		{NodeKey: "func:b", Name: "Process", NodeType: "Function", Order: 1},
		{NodeKey: "func:a", Name: "Start", NodeType: "Function", Order: 2}, // duplicate
		{NodeKey: "func:c", Name: "End", NodeType: "Function", Order: 3},
	}

	result := dedup.Deduplicate(steps, DefaultTraversalBudget)
	if len(result) != 3 {
		t.Fatalf("expected 3 steps after dedup, got %d", len(result))
	}

	// Verify order is re-assigned
	for i, s := range result {
		if s.Order != i {
			t.Errorf("step %d: expected order %d, got %d", i, i, s.Order)
		}
	}
}

func TestFlowDeduplicator_BlockedNames(t *testing.T) {
	dedup := NewFlowDeduplicator()

	steps := []FlowStepInfo{
		{NodeKey: "func:a", Name: "Start", NodeType: "Function", Order: 0},
		{NodeKey: "func:b", Name: "log", NodeType: "Function", Order: 1},   // blocked
		{NodeKey: "func:c", Name: "print", NodeType: "Function", Order: 2}, // blocked
		{NodeKey: "func:d", Name: "End", NodeType: "Function", Order: 3},
	}

	result := dedup.Deduplicate(steps, DefaultTraversalBudget)
	if len(result) != 2 {
		t.Fatalf("expected 2 steps after blocking, got %d", len(result))
	}
}

func TestFlowDeduplicator_DisallowedNodeTypes(t *testing.T) {
	dedup := NewFlowDeduplicator()

	steps := []FlowStepInfo{
		{NodeKey: "func:a", Name: "Start", NodeType: "Function", Order: 0},
		{NodeKey: "var:x", Name: "X", NodeType: "Variable", Order: 1},     // not allowed
		{NodeKey: "func:b", Name: "End", NodeType: "Function", Order: 2},
	}

	result := dedup.Deduplicate(steps, DefaultTraversalBudget)
	if len(result) != 2 {
		t.Fatalf("expected 2 steps after type filter, got %d", len(result))
	}
}

func TestFlowDeduplicator_MaxSteps(t *testing.T) {
	dedup := NewFlowDeduplicator()
	budget := TraversalBudget{
		MaxSteps: 2,
		AllowedNodeTypes: []string{"Function"},
	}

	steps := []FlowStepInfo{
		{NodeKey: "func:a", Name: "A", NodeType: "Function", Order: 0},
		{NodeKey: "func:b", Name: "B", NodeType: "Function", Order: 1},
		{NodeKey: "func:c", Name: "C", NodeType: "Function", Order: 2},
		{NodeKey: "func:d", Name: "D", NodeType: "Function", Order: 3},
	}

	result := dedup.Deduplicate(steps, budget)
	if len(result) != 2 {
		t.Fatalf("expected 2 steps (max), got %d", len(result))
	}
}

func TestComputeFlowQuality(t *testing.T) {
	original := []FlowStepInfo{
		{NodeKey: "func:a", Name: "Start", NodeType: "Function"},
		{NodeKey: "func:a", Name: "Start", NodeType: "Function"}, // duplicate
		{NodeKey: "func:b", Name: "log", NodeType: "Function"},   // blocked
		{NodeKey: "func:c", Name: "End", NodeType: "Function"},
		{NodeKey: "var:x", Name: "X", NodeType: "Variable"},      // not allowed
	}

	deduped := []FlowStepInfo{
		{NodeKey: "func:a", Name: "Start", NodeType: "Function"},
		{NodeKey: "func:c", Name: "End", NodeType: "Function"},
	}

	metrics := ComputeFlowQuality(original, deduped, DefaultTraversalBudget)

	if metrics.TotalSteps != 5 {
		t.Errorf("expected 5 total steps, got %d", metrics.TotalSteps)
	}
	if metrics.UniqueSteps != 2 {
		t.Errorf("expected 2 unique steps, got %d", metrics.UniqueSteps)
	}
	if metrics.DuplicateSteps != 3 {
		t.Errorf("expected 3 deduplicated, got %d", metrics.DuplicateSteps)
	}
	if metrics.BlockedSteps != 2 {
		t.Errorf("expected 2 blocked, got %d", metrics.BlockedSteps)
	}
	if metrics.SpuriousStepRate == 0 {
		t.Error("expected non-zero spurious step rate")
	}
}

func TestComputeFlowQuality_Empty(t *testing.T) {
	metrics := ComputeFlowQuality(nil, nil, DefaultTraversalBudget)
	if metrics.SpuriousStepRate != 0 {
		t.Error("expected 0 spurious rate for empty")
	}
}
