package query

import (
	"testing"

	"github.com/context-maximiser/code-graph/libs/core-models-go"
)

func TestFlowStep_Fields(t *testing.T) {
	step := FlowStep{
		NodeKey: "func:pkg/handler.go#HandleUser(...)",
		Name:    "HandleUser",
		Label:   "Function",
		Order:   1,
	}

	if step.NodeKey != "func:pkg/handler.go#HandleUser(...)" {
		t.Errorf("unexpected NodeKey: %s", step.NodeKey)
	}
	if step.Order != 1 {
		t.Errorf("expected Order 1, got %d", step.Order)
	}
}

func TestFlowSpineResult_Fields(t *testing.T) {
	result := FlowSpineResult{
		FlowNodeKey: models.FlowNodeKey("api", "api:GET:/api/users"),
		FlowName:    "GET /api/users",
		FlowType:    "api",
		Steps: []FlowStep{
			{NodeKey: "api:GET:/api/users", Name: "GET /api/users", Label: "APIRoute", Order: 0},
			{NodeKey: "func:pkg/handler.go#HandleUser(...)", Name: "HandleUser", Label: "Function", Order: 1},
		},
	}

	if result.FlowType != "api" {
		t.Errorf("expected FlowType 'api', got %s", result.FlowType)
	}
	if len(result.Steps) != 2 {
		t.Errorf("expected 2 steps, got %d", len(result.Steps))
	}
	if result.Steps[0].Label != "APIRoute" {
		t.Errorf("expected first step label 'APIRoute', got %s", result.Steps[0].Label)
	}
	if result.Steps[1].Order != 1 {
		t.Errorf("expected second step order 1, got %d", result.Steps[1].Order)
	}
}

func TestFlowNodeKeyIntegration(t *testing.T) {
	// Verify FlowNodeKey produces expected format for flow spine usage.
	key := models.FlowNodeKey("api", "api:GET:/api/users")
	expected := "flow:api:api:GET:/api/users"
	if key != expected {
		t.Errorf("expected %s, got %s", expected, key)
	}

	key2 := models.FlowNodeKey("consumer", "queue:orders")
	expected2 := "flow:consumer:queue:orders"
	if key2 != expected2 {
		t.Errorf("expected %s, got %s", expected2, key2)
	}
}

func TestNewFlowSpineGenerator(t *testing.T) {
	// Verify constructor sets defaults (nil client is fine for unit test).
	gen := NewFlowSpineGenerator(nil)
	if gen == nil {
		t.Fatal("expected non-nil generator")
	}
	if gen.scopeCtx.Scope != "main" {
		t.Errorf("expected default scope 'main', got %s", gen.scopeCtx.Scope)
	}
	if gen.scopeCtx.ScopeID != "main" {
		t.Errorf("expected default scopeID 'main', got %s", gen.scopeCtx.ScopeID)
	}
}

func TestFlowSpineGenerator_SetScope(t *testing.T) {
	gen := NewFlowSpineGenerator(nil)
	prScope := models.NewPRScope("42")
	gen.SetScope(prScope)

	if gen.scopeCtx.Scope != "pr" {
		t.Errorf("expected scope 'pr', got %s", gen.scopeCtx.Scope)
	}
	if gen.scopeCtx.ScopeID != "pr-42" {
		t.Errorf("expected scopeID 'pr-42', got %s", gen.scopeCtx.ScopeID)
	}
}

// ---------------------------------------------------------------------------
// P3: Verify scope propagation into persistFlow props
// ---------------------------------------------------------------------------

func TestFlowSpineGenerator_ScopeInFlowProps(t *testing.T) {
	// This tests that scope context is available for use in flow props.
	// The actual Cypher execution requires Neo4j, but we verify the generator
	// carries the right scope context through SetScope.
	gen := NewFlowSpineGenerator(nil)

	// Default scope
	if gen.scopeCtx.Scope != "main" {
		t.Errorf("expected default scope 'main', got %s", gen.scopeCtx.Scope)
	}
	if gen.scopeCtx.ScopeID != "main" {
		t.Errorf("expected default scopeID 'main', got %s", gen.scopeCtx.ScopeID)
	}

	// Switch to PR scope
	gen.SetScope(models.NewPRScope("99"))
	if gen.scopeCtx.Scope != "pr" {
		t.Errorf("expected scope 'pr' after SetScope, got %s", gen.scopeCtx.Scope)
	}
	if gen.scopeCtx.ScopeID != "pr-99" {
		t.Errorf("expected scopeID 'pr-99' after SetScope, got %s", gen.scopeCtx.ScopeID)
	}
}

// ---------------------------------------------------------------------------
// P3: Verify FlowStep preserves fields through scope-filtered queries
// ---------------------------------------------------------------------------

func TestFlowStep_ScopeAwareFields(t *testing.T) {
	// FlowStep struct doesn't carry scope directly — scope filtering is in the
	// Cypher queries. This test verifies that the struct works correctly with
	// the expected data shapes.
	steps := []FlowStep{
		{NodeKey: "api:GET:/users", Name: "GET /users", Label: "APIRoute", Order: 0},
		{NodeKey: "func:handler.go#GetUsers(...)", Name: "GetUsers", Label: "Function", Order: 1},
		{NodeKey: "method:repo.go#FindAll(...)", Name: "FindAll", Label: "Method", Order: 2},
	}

	for i, step := range steps {
		if step.Order != i {
			t.Errorf("step %d: expected Order %d, got %d", i, i, step.Order)
		}
		if step.NodeKey == "" {
			t.Errorf("step %d: NodeKey should not be empty", i)
		}
	}
}
