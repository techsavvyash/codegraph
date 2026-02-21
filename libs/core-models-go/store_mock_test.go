package models_test

import (
	"context"
	"errors"
	"testing"
	"time"

	models "github.com/context-maximiser/code-graph/libs/core-models-go"
)

// compile-time assertion: MockGraphStore must satisfy GraphStore.
var _ models.GraphStore = (*models.MockGraphStore)(nil)

func TestMockGraphStore_UpsertNode_GetNode_RoundTrip(t *testing.T) {
	store := models.NewMockGraphStore()
	ctx := context.Background()

	node := &models.Node{
		NodeKey: "fn:MyFunc",
		Labels:  []string{"Function"},
		Scope:   models.ScopeMain,
		ScopeID: models.ScopeMain,
		Props: map[string]any{
			"name": "MyFunc",
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := store.UpsertNode(ctx, node); err != nil {
		t.Fatalf("UpsertNode: %v", err)
	}

	got, err := store.GetNode(ctx, "fn:MyFunc", models.DefaultScope())
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if got == nil {
		t.Fatal("GetNode: expected a node, got nil")
	}
	if got.NodeKey != node.NodeKey {
		t.Errorf("NodeKey: got %q, want %q", got.NodeKey, node.NodeKey)
	}
	if got.Props["name"] != "MyFunc" {
		t.Errorf("Props[name]: got %v, want %q", got.Props["name"], "MyFunc")
	}
}

func TestMockGraphStore_GetNode_NotFound(t *testing.T) {
	store := models.NewMockGraphStore()
	ctx := context.Background()

	got, err := store.GetNode(ctx, "does-not-exist", models.DefaultScope())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

func TestMockGraphStore_UpsertNodes_FindNodes(t *testing.T) {
	store := models.NewMockGraphStore()
	ctx := context.Background()

	nodes := []*models.Node{
		{NodeKey: "fn:FuncA", Labels: []string{"Function"}, Scope: models.ScopeMain, ScopeID: models.ScopeMain},
		{NodeKey: "fn:FuncB", Labels: []string{"Function"}, Scope: models.ScopeMain, ScopeID: models.ScopeMain},
		{NodeKey: "cls:ClassA", Labels: []string{"Class"}, Scope: models.ScopeMain, ScopeID: models.ScopeMain},
	}

	if err := store.UpsertNodes(ctx, nodes); err != nil {
		t.Fatalf("UpsertNodes: %v", err)
	}

	filter := models.NodeFilter{
		ScopeID:   models.ScopeMain,
		NodeTypes: []models.NodeType{models.FunctionNode},
	}
	found, err := store.FindNodes(ctx, filter)
	if err != nil {
		t.Fatalf("FindNodes: %v", err)
	}
	if len(found) != 2 {
		t.Errorf("FindNodes: expected 2 Function nodes, got %d", len(found))
	}
}

func TestMockGraphStore_UpsertRelationship_FindRelationships(t *testing.T) {
	store := models.NewMockGraphStore()
	ctx := context.Background()

	rel := &models.Relationship{
		StartID: "fn:FuncA",
		EndID:   "fn:FuncB",
		Type:    models.CallsRel,
	}

	if err := store.UpsertRelationship(ctx, rel); err != nil {
		t.Fatalf("UpsertRelationship: %v", err)
	}

	filter := models.RelFilter{
		FromNodeKey: "fn:FuncA",
		RelTypes:    []models.RelationshipType{models.CallsRel},
	}
	rels, err := store.FindRelationships(ctx, filter)
	if err != nil {
		t.Fatalf("FindRelationships: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	if rels[0].EndID != "fn:FuncB" {
		t.Errorf("EndID: got %q, want %q", rels[0].EndID, "fn:FuncB")
	}
}

// GetWithOverlay: overlay-scope node wins over main-scope node.
func TestMockGraphStore_GetWithOverlay_OverlayWins(t *testing.T) {
	store := models.NewMockGraphStore()
	ctx := context.Background()

	mainNode := &models.Node{
		NodeKey: "fn:FuncA",
		Labels:  []string{"Function"},
		Scope:   models.ScopeMain,
		ScopeID: models.ScopeMain,
		Props:   map[string]any{"version": "main"},
	}
	overlayNode := &models.Node{
		NodeKey: "fn:FuncA",
		Labels:  []string{"Function"},
		Scope:   models.ScopePR,
		ScopeID: "pr-42",
		Props:   map[string]any{"version": "pr-42"},
	}

	if err := store.UpsertNode(ctx, mainNode); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertNode(ctx, overlayNode); err != nil {
		t.Fatal(err)
	}

	prScope := models.NewPRScope("42")
	got, err := store.GetWithOverlay(ctx, "fn:FuncA", prScope)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected overlay node, got nil")
	}
	if got.ScopeID != "pr-42" {
		t.Errorf("expected overlay ScopeID=pr-42, got %q", got.ScopeID)
	}
	if got.Props["version"] != "pr-42" {
		t.Errorf("expected overlay version=pr-42, got %v", got.Props["version"])
	}
}

// GetWithOverlay: tombstone hides the main-scope node.
func TestMockGraphStore_GetWithOverlay_TombstoneHides(t *testing.T) {
	store := models.NewMockGraphStore()
	ctx := context.Background()

	mainNode := &models.Node{
		NodeKey: "fn:FuncA",
		Labels:  []string{"Function"},
		Scope:   models.ScopeMain,
		ScopeID: models.ScopeMain,
	}
	if err := store.UpsertNode(ctx, mainNode); err != nil {
		t.Fatal(err)
	}

	scopeID := "pr-42"
	tombstone := &models.Tombstone{
		BaseNode: models.BaseNode{
			NodeKey: models.TombstoneNodeKey(scopeID, "fn:FuncA"),
			ScopeID: scopeID,
		},
		TargetNodeKey: "fn:FuncA",
		TargetLabel:   "Function",
		Reason:        models.TombstoneSymbolRemoved,
	}
	if err := store.ApplyTombstone(ctx, tombstone); err != nil {
		t.Fatal(err)
	}

	prScope := models.NewPRScope("42")
	got, err := store.GetWithOverlay(ctx, "fn:FuncA", prScope)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("expected nil (tombstoned), got node with key %q", got.NodeKey)
	}
}

// GetWithOverlay: falls back to main-scope when no overlay and no tombstone.
func TestMockGraphStore_GetWithOverlay_FallsBackToMain(t *testing.T) {
	store := models.NewMockGraphStore()
	ctx := context.Background()

	mainNode := &models.Node{
		NodeKey: "fn:FuncA",
		Labels:  []string{"Function"},
		Scope:   models.ScopeMain,
		ScopeID: models.ScopeMain,
		Props:   map[string]any{"version": "main"},
	}
	if err := store.UpsertNode(ctx, mainNode); err != nil {
		t.Fatal(err)
	}

	// PR scope with no overlay node and no tombstone.
	prScope := models.NewPRScope("99")
	got, err := store.GetWithOverlay(ctx, "fn:FuncA", prScope)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected main-scope fallback, got nil")
	}
	if got.ScopeID != models.ScopeMain {
		t.Errorf("expected ScopeID=main, got %q", got.ScopeID)
	}
}

// GetWithOverlay: returns nil when nothing matches at all.
func TestMockGraphStore_GetWithOverlay_ReturnsNilWhenNotFound(t *testing.T) {
	store := models.NewMockGraphStore()
	ctx := context.Background()

	prScope := models.NewPRScope("1")
	got, err := store.GetWithOverlay(ctx, "does-not-exist", prScope)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

// Error injection via Errors map.
func TestMockGraphStore_ErrorInjection_UpsertNode(t *testing.T) {
	store := models.NewMockGraphStore()
	ctx := context.Background()

	injectedErr := errors.New("injected UpsertNode error")
	store.Errors["UpsertNode"] = injectedErr

	node := &models.Node{NodeKey: "fn:FuncA", ScopeID: models.ScopeMain}
	if err := store.UpsertNode(ctx, node); !errors.Is(err, injectedErr) {
		t.Errorf("expected injected error, got %v", err)
	}
}

func TestMockGraphStore_ErrorInjection_GetNode(t *testing.T) {
	store := models.NewMockGraphStore()
	ctx := context.Background()

	injectedErr := errors.New("injected GetNode error")
	store.Errors["GetNode"] = injectedErr

	_, err := store.GetNode(ctx, "fn:FuncA", models.DefaultScope())
	if !errors.Is(err, injectedErr) {
		t.Errorf("expected injected error, got %v", err)
	}
}

func TestMockGraphStore_ErrorInjection_GetWithOverlay(t *testing.T) {
	store := models.NewMockGraphStore()
	ctx := context.Background()

	injectedErr := errors.New("injected GetWithOverlay error")
	store.Errors["GetWithOverlay"] = injectedErr

	_, err := store.GetWithOverlay(ctx, "fn:FuncA", models.DefaultScope())
	if !errors.Is(err, injectedErr) {
		t.Errorf("expected injected error, got %v", err)
	}
}

func TestMockGraphStore_ErrorInjection_ApplyTombstone(t *testing.T) {
	store := models.NewMockGraphStore()
	ctx := context.Background()

	injectedErr := errors.New("injected ApplyTombstone error")
	store.Errors["ApplyTombstone"] = injectedErr

	tombstone := &models.Tombstone{
		BaseNode: models.BaseNode{NodeKey: "t:key", ScopeID: "pr-1"},
	}
	if err := store.ApplyTombstone(ctx, tombstone); !errors.Is(err, injectedErr) {
		t.Errorf("expected injected error, got %v", err)
	}
}

func TestMockGraphStore_FindNodes_Limit(t *testing.T) {
	store := models.NewMockGraphStore()
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		node := &models.Node{
			NodeKey: "fn:Func" + string(rune('A'+i)),
			Labels:  []string{"Function"},
			Scope:   models.ScopeMain,
			ScopeID: models.ScopeMain,
		}
		if err := store.UpsertNode(ctx, node); err != nil {
			t.Fatal(err)
		}
	}

	filter := models.NodeFilter{Limit: 3}
	found, err := store.FindNodes(ctx, filter)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) > 3 {
		t.Errorf("expected at most 3 nodes (limit), got %d", len(found))
	}
}

func TestMockGraphStore_UpsertRelationship_Idempotent(t *testing.T) {
	store := models.NewMockGraphStore()
	ctx := context.Background()

	rel := &models.Relationship{
		StartID: "fn:A",
		EndID:   "fn:B",
		Type:    models.CallsRel,
		Properties: map[string]any{
			"line": 10,
		},
	}

	// Upsert the same relationship twice.
	if err := store.UpsertRelationship(ctx, rel); err != nil {
		t.Fatal(err)
	}
	rel2 := &models.Relationship{
		StartID:    "fn:A",
		EndID:      "fn:B",
		Type:       models.CallsRel,
		Properties: map[string]any{"line": 20},
	}
	if err := store.UpsertRelationship(ctx, rel2); err != nil {
		t.Fatal(err)
	}

	all := store.AllRelationships()
	if len(all) != 1 {
		t.Errorf("expected exactly 1 relationship after duplicate upsert, got %d", len(all))
	}
	if all[0].Properties["line"] != 20 {
		t.Errorf("expected updated line=20, got %v", all[0].Properties["line"])
	}
}
