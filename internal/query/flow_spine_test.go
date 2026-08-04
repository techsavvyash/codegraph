package query

import (
	"testing"

	models "github.com/context-maximiser/code-graph/internal/model"
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

// ---------------------------------------------------------------------------
// RFC-005: Depth/ParentKey/NodeID/FilePath/StartLine on FlowStep, SetPersist,
// and tree-consistency pruning in dropOrphanedDescendants.
// ---------------------------------------------------------------------------

func TestFlowStep_NewFields(t *testing.T) {
	step := FlowStep{
		NodeKey:   "func:pkg/handler.go#Handle(...)",
		Name:      "Handle",
		Label:     "Function",
		Order:     1,
		Depth:     1,
		ParentKey: "func:pkg/root.go#Root(...)",
		NodeID:    "4:abc:123",
		FilePath:  "pkg/handler.go",
		StartLine: 42,
	}

	if step.Depth != 1 {
		t.Errorf("expected Depth 1, got %d", step.Depth)
	}
	if step.ParentKey != "func:pkg/root.go#Root(...)" {
		t.Errorf("unexpected ParentKey: %s", step.ParentKey)
	}
	if step.NodeID != "4:abc:123" {
		t.Errorf("unexpected NodeID: %s", step.NodeID)
	}
	if step.FilePath != "pkg/handler.go" {
		t.Errorf("unexpected FilePath: %s", step.FilePath)
	}
	if step.StartLine != 42 {
		t.Errorf("expected StartLine 42, got %d", step.StartLine)
	}
}

func TestFlowSpineGenerator_SetPersist(t *testing.T) {
	gen := NewFlowSpineGenerator(nil)
	if !gen.persist {
		t.Fatal("expected persist to default to true")
	}

	gen.SetPersist(false)
	if gen.persist {
		t.Fatal("expected persist to be false after SetPersist(false)")
	}

	gen.SetPersist(true)
	if !gen.persist {
		t.Fatal("expected persist to be true after SetPersist(true)")
	}
}

// TestDropOrphanedDescendants_KeepsWellFormedTree verifies a tree where every
// non-root ParentKey resolves to a surviving step passes through unchanged.
func TestDropOrphanedDescendants_KeepsWellFormedTree(t *testing.T) {
	steps := []FlowStep{
		{NodeKey: "root", Name: "root", Label: "Function", Order: 0, Depth: 0},
		{NodeKey: "child1", Name: "child1", Label: "Function", Order: 1, Depth: 1, ParentKey: "root"},
		{NodeKey: "child2", Name: "child2", Label: "Function", Order: 2, Depth: 1, ParentKey: "root"},
		{NodeKey: "grandchild", Name: "grandchild", Label: "Function", Order: 3, Depth: 2, ParentKey: "child1"},
	}

	out := dropOrphanedDescendants(steps)

	if len(out) != 4 {
		t.Fatalf("expected all 4 steps to survive, got %d: %+v", len(out), out)
	}
	keys := make(map[string]bool, len(out))
	for _, s := range out {
		keys[s.NodeKey] = true
	}
	for _, want := range []string{"root", "child1", "child2", "grandchild"} {
		if !keys[want] {
			t.Errorf("expected %s to survive, got steps: %+v", want, out)
		}
	}
}

// TestDropOrphanedDescendants_CascadesThroughMultipleLevels verifies that
// pruning a mid-tree step (simulating a fanout cap or budget filter dropping
// it after DeduplicateAnchored ran) also drops its descendants transitively,
// rather than leaving a grandchild with a ParentKey pointing at nothing.
func TestDropOrphanedDescendants_CascadesThroughMultipleLevels(t *testing.T) {
	steps := []FlowStep{
		{NodeKey: "root", Name: "root", Label: "Function", Order: 0, Depth: 0},
		{NodeKey: "keptChild", Name: "keptChild", Label: "Function", Order: 1, Depth: 1, ParentKey: "root"},
		// "prunedChild" is intentionally absent from this slice — it was
		// dropped upstream (by the fanout cap or budget filtering), but its
		// own children below still reference it as ParentKey.
		{NodeKey: "orphanGrandchild", Name: "orphanGrandchild", Label: "Function", Order: 2, Depth: 2, ParentKey: "prunedChild"},
		{NodeKey: "orphanGreatGrandchild", Name: "orphanGreatGrandchild", Label: "Function", Order: 3, Depth: 3, ParentKey: "orphanGrandchild"},
		{NodeKey: "keptGrandchild", Name: "keptGrandchild", Label: "Function", Order: 4, Depth: 2, ParentKey: "keptChild"},
	}

	out := dropOrphanedDescendants(steps)

	keys := make(map[string]bool, len(out))
	for _, s := range out {
		keys[s.NodeKey] = true
	}

	if !keys["root"] || !keys["keptChild"] || !keys["keptGrandchild"] {
		t.Errorf("expected root/keptChild/keptGrandchild to survive, got: %+v", out)
	}
	if keys["orphanGrandchild"] {
		t.Errorf("orphanGrandchild's parent was pruned; it must be dropped too, got: %+v", out)
	}
	if keys["orphanGreatGrandchild"] {
		t.Errorf("orphanGreatGrandchild's ancestor chain was pruned; cascade must drop it too, got: %+v", out)
	}
	if len(out) != 3 {
		t.Errorf("expected exactly 3 surviving steps, got %d: %+v", len(out), out)
	}
}

// TestDropOrphanedDescendants_PreservesOrder verifies the output stays
// sorted by Order after the depth-bucketed reconstruction.
func TestDropOrphanedDescendants_PreservesOrder(t *testing.T) {
	steps := []FlowStep{
		{NodeKey: "root", Name: "root", Label: "Function", Order: 0, Depth: 0},
		{NodeKey: "a", Name: "a", Label: "Function", Order: 1, Depth: 1, ParentKey: "root"},
		{NodeKey: "b", Name: "b", Label: "Function", Order: 2, Depth: 1, ParentKey: "root"},
		{NodeKey: "c", Name: "c", Label: "Function", Order: 3, Depth: 2, ParentKey: "a"},
	}

	out := dropOrphanedDescendants(steps)
	for i, s := range out {
		if s.Order != i {
			t.Errorf("step %d: expected Order %d, got %d (%s)", i, i, s.Order, s.NodeKey)
		}
	}
}
