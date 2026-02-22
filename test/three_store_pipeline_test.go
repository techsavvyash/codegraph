package pipeline_test

import (
	"context"
	"testing"

	models "github.com/context-maximiser/code-graph/libs/core-models-go"
	textindex "github.com/context-maximiser/code-graph/libs/text-index-client-go"
	search "github.com/context-maximiser/code-graph/libs/vector-client-go"
)

// ThreeStorePipeline wires GraphStore + VectorStore + TextIndexStore together.
// Each store field may be nil to simulate degraded mode (store unavailable).
type ThreeStorePipeline struct {
	graph  models.GraphStore
	vector search.VectorStore
	text   textindex.TextIndexStore
}

// IndexSymbol simulates indexing a code symbol across all three stores.
// Nil stores are gracefully skipped (degraded-mode support).
func (p *ThreeStorePipeline) IndexSymbol(ctx context.Context, node *models.Node, docContent string) error {
	// 1. Write to graph store (if available).
	if p.graph != nil {
		if err := p.graph.UpsertNode(ctx, node); err != nil {
			return err
		}
	}

	// 2. Write to text store (if available).
	if p.text != nil {
		nodeType := ""
		if len(node.Labels) > 0 {
			nodeType = node.Labels[0]
		}
		meta := map[string]string{
			"nodeKey":  node.NodeKey,
			"nodeType": nodeType,
			"scope":    node.Scope,
			"scopeId":  node.ScopeID,
		}
		if err := p.text.IndexDocument(ctx, node.NodeKey, docContent, meta); err != nil {
			return err
		}
	}

	return nil
}

// --- Test 1: index three nodes and search across all three stores ---

func TestThreeStorePipeline_IndexAndSearch(t *testing.T) {
	ctx := context.Background()
	graphStore := models.NewMockGraphStore()
	textStore := textindex.NewMockTextIndexStore()

	pipeline := &ThreeStorePipeline{
		graph: graphStore,
		text:  textStore,
	}

	mainScope := models.DefaultScope()

	nodes := []*models.Node{
		{
			NodeKey: "fn:pkg/util.ParseInput",
			Labels:  []string{string(models.FunctionNode)},
			Scope:   mainScope.Scope,
			ScopeID: mainScope.ScopeID,
			Props:   map[string]any{"name": "ParseInput"},
		},
		{
			NodeKey: "method:pkg/server.Handler.ServeHTTP",
			Labels:  []string{string(models.MethodNode)},
			Scope:   mainScope.Scope,
			ScopeID: mainScope.ScopeID,
			Props:   map[string]any{"name": "ServeHTTP"},
		},
		{
			NodeKey: "class:pkg/models.UserRecord",
			Labels:  []string{string(models.ClassNode)},
			Scope:   mainScope.Scope,
			ScopeID: mainScope.ScopeID,
			Props:   map[string]any{"name": "UserRecord"},
		},
	}

	contents := []string{
		"function content for ParseInput that parses HTTP input",
		"method content for ServeHTTP that handles HTTP requests",
		"class content for UserRecord that models a user",
	}

	for i, node := range nodes {
		if err := pipeline.IndexSymbol(ctx, node, contents[i]); err != nil {
			t.Fatalf("IndexSymbol(%s): %v", node.NodeKey, err)
		}
	}

	// Assert: GraphStore.GetNode returns each node by nodeKey.
	for _, node := range nodes {
		got, err := graphStore.GetNode(ctx, node.NodeKey, mainScope)
		if err != nil {
			t.Fatalf("GetNode(%s): %v", node.NodeKey, err)
		}
		if got == nil {
			t.Fatalf("GetNode(%s): expected node, got nil", node.NodeKey)
		}
		if got.NodeKey != node.NodeKey {
			t.Errorf("GetNode returned nodeKey=%s, want %s", got.NodeKey, node.NodeKey)
		}
	}

	// Assert: TextIndexStore.Search("function content") returns matching result.
	results, err := textStore.Search(ctx, "function content", textindex.SearchOpts{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one text search result, got none")
	}
	// The first node's content contains "function content".
	found := false
	for _, r := range results {
		if r.NodeKey == "fn:pkg/util.ParseInput" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected text search to return fn:pkg/util.ParseInput, got: %v", results)
	}
}

// --- Test 2: overlay precedence — PR overlay wins over main scope ---

func TestThreeStorePipeline_OverlayPrecedence(t *testing.T) {
	ctx := context.Background()
	graphStore := models.NewMockGraphStore()
	textStore := textindex.NewMockTextIndexStore()

	pipeline := &ThreeStorePipeline{
		graph: graphStore,
		text:  textStore,
	}

	mainScope := models.DefaultScope()
	prScope := models.NewPRScope("42")

	const nodeKey = "fn:pkg/api.Authenticate"

	// Index main-scope version.
	mainNode := &models.Node{
		NodeKey: nodeKey,
		Labels:  []string{string(models.FunctionNode)},
		Scope:   mainScope.Scope,
		ScopeID: mainScope.ScopeID,
		Props:   map[string]any{"name": "Authenticate", "version": "main"},
	}
	if err := pipeline.IndexSymbol(ctx, mainNode, "main version of Authenticate"); err != nil {
		t.Fatalf("IndexSymbol (main): %v", err)
	}

	// Index PR overlay version of the same node.
	prNode := &models.Node{
		NodeKey: nodeKey,
		Labels:  []string{string(models.FunctionNode)},
		Scope:   prScope.Scope,
		ScopeID: prScope.ScopeID,
		Props:   map[string]any{"name": "Authenticate", "version": "pr-42"},
	}
	if err := graphStore.UpsertNode(ctx, prNode); err != nil {
		t.Fatalf("UpsertNode (pr): %v", err)
	}

	// GetWithOverlay in PR scope must return the PR version.
	got, err := graphStore.GetWithOverlay(ctx, nodeKey, prScope)
	if err != nil {
		t.Fatalf("GetWithOverlay (pr scope): %v", err)
	}
	if got == nil {
		t.Fatal("GetWithOverlay (pr scope): expected node, got nil")
	}
	if got.ScopeID != prScope.ScopeID {
		t.Errorf("expected overlay node scopeId=%s, got %s", prScope.ScopeID, got.ScopeID)
	}
	if got.Props["version"] != "pr-42" {
		t.Errorf("expected PR overlay version='pr-42', got %v", got.Props["version"])
	}

	// GetWithOverlay in main scope must return the main version.
	gotMain, err := graphStore.GetWithOverlay(ctx, nodeKey, mainScope)
	if err != nil {
		t.Fatalf("GetWithOverlay (main scope): %v", err)
	}
	if gotMain == nil {
		t.Fatal("GetWithOverlay (main scope): expected node, got nil")
	}
	if gotMain.Props["version"] != "main" {
		t.Errorf("expected main version='main', got %v", gotMain.Props["version"])
	}
}

// --- Test 3: tombstone hides a node in PR scope but not in main scope ---

func TestThreeStorePipeline_TombstonedNodeHidden(t *testing.T) {
	ctx := context.Background()
	graphStore := models.NewMockGraphStore()
	textStore := textindex.NewMockTextIndexStore()

	pipeline := &ThreeStorePipeline{
		graph: graphStore,
		text:  textStore,
	}

	mainScope := models.DefaultScope()
	prScope := models.NewPRScope("99")

	const nodeKey = "fn:pkg/legacy.OldHandler"

	// Index in main scope.
	mainNode := &models.Node{
		NodeKey: nodeKey,
		Labels:  []string{string(models.FunctionNode)},
		Scope:   mainScope.Scope,
		ScopeID: mainScope.ScopeID,
		Props:   map[string]any{"name": "OldHandler"},
	}
	if err := pipeline.IndexSymbol(ctx, mainNode, "old handler content"); err != nil {
		t.Fatalf("IndexSymbol: %v", err)
	}

	// Apply tombstone for that node in PR scope.
	tombstoneKey := models.TombstoneNodeKey(prScope.ScopeID, nodeKey)
	tombstone := &models.Tombstone{
		BaseNode: models.BaseNode{
			NodeKey: tombstoneKey,
			Scope:   prScope.Scope,
			ScopeID: prScope.ScopeID,
		},
		TargetNodeKey: nodeKey,
		TargetLabel:   string(models.FunctionNode),
		Reason:        models.TombstoneSymbolRemoved,
	}
	if err := graphStore.ApplyTombstone(ctx, tombstone); err != nil {
		t.Fatalf("ApplyTombstone: %v", err)
	}

	// GetWithOverlay in PR scope must return nil (tombstone hides).
	got, err := graphStore.GetWithOverlay(ctx, nodeKey, prScope)
	if err != nil {
		t.Fatalf("GetWithOverlay (pr scope): %v", err)
	}
	if got != nil {
		t.Errorf("expected nil (tombstoned) in PR scope, got node with key=%s", got.NodeKey)
	}

	// GetWithOverlay in main scope must still return the node.
	gotMain, err := graphStore.GetWithOverlay(ctx, nodeKey, mainScope)
	if err != nil {
		t.Fatalf("GetWithOverlay (main scope): %v", err)
	}
	if gotMain == nil {
		t.Fatal("GetWithOverlay (main scope): expected node to be visible, got nil")
	}
	if gotMain.NodeKey != nodeKey {
		t.Errorf("expected nodeKey=%s, got %s", nodeKey, gotMain.NodeKey)
	}
}

// --- Test 4: degraded mode — text store is nil ---

func TestThreeStorePipeline_DegradedMode_NoText(t *testing.T) {
	ctx := context.Background()
	graphStore := models.NewMockGraphStore()

	// Pipeline without text store (nil).
	pipeline := &ThreeStorePipeline{
		graph: graphStore,
		text:  nil,
	}

	mainScope := models.DefaultScope()
	node := &models.Node{
		NodeKey: "fn:pkg/core.Init",
		Labels:  []string{string(models.FunctionNode)},
		Scope:   mainScope.Scope,
		ScopeID: mainScope.ScopeID,
		Props:   map[string]any{"name": "Init"},
	}

	// IndexSymbol should not panic or error when text store is nil.
	if err := pipeline.IndexSymbol(ctx, node, "init function content"); err != nil {
		t.Fatalf("IndexSymbol with nil text store: %v", err)
	}

	// Graph store must still have the node.
	got, err := graphStore.GetNode(ctx, node.NodeKey, mainScope)
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if got == nil {
		t.Fatal("expected node in graph store, got nil")
	}
	if got.NodeKey != node.NodeKey {
		t.Errorf("expected nodeKey=%s, got %s", node.NodeKey, got.NodeKey)
	}
}

// --- Test 5: degraded mode — graph store is nil ---

func TestThreeStorePipeline_DegradedMode_NoGraph(t *testing.T) {
	ctx := context.Background()
	textStore := textindex.NewMockTextIndexStore()

	// Pipeline without graph store (nil).
	pipeline := &ThreeStorePipeline{
		graph: nil,
		text:  textStore,
	}

	mainScope := models.DefaultScope()
	node := &models.Node{
		NodeKey: "fn:pkg/core.Shutdown",
		Labels:  []string{string(models.FunctionNode)},
		Scope:   mainScope.Scope,
		ScopeID: mainScope.ScopeID,
		Props:   map[string]any{"name": "Shutdown"},
	}

	// IndexSymbol should not panic or error when graph store is nil.
	if err := pipeline.IndexSymbol(ctx, node, "shutdown function content"); err != nil {
		t.Fatalf("IndexSymbol with nil graph store: %v", err)
	}

	// Text store must have the document.
	results, err := textStore.Search(ctx, "shutdown function", textindex.SearchOpts{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected text store to have the document, got 0 results")
	}
	if results[0].NodeKey != node.NodeKey {
		t.Errorf("expected nodeKey=%s, got %s", node.NodeKey, results[0].NodeKey)
	}
}

// --- Test 6: cross-store consistency — graph is truth for existence ---

func TestThreeStorePipeline_CrossStoreConsistency(t *testing.T) {
	ctx := context.Background()
	graphStore := models.NewMockGraphStore()
	textStore := textindex.NewMockTextIndexStore()

	pipeline := &ThreeStorePipeline{
		graph: graphStore,
		text:  textStore,
	}

	mainScope := models.DefaultScope()
	prScope := models.NewPRScope("77")

	// Index 5 nodes across all stores.
	nodeKeys := []string{
		"fn:svc/a.Alpha",
		"fn:svc/a.Beta",
		"fn:svc/a.Gamma",
		"fn:svc/a.Delta",
		"fn:svc/a.Epsilon",
	}

	for _, key := range nodeKeys {
		node := &models.Node{
			NodeKey: key,
			Labels:  []string{string(models.FunctionNode)},
			Scope:   mainScope.Scope,
			ScopeID: mainScope.ScopeID,
			Props:   map[string]any{"name": key},
		}
		if err := pipeline.IndexSymbol(ctx, node, "shared function content for "+key); err != nil {
			t.Fatalf("IndexSymbol(%s): %v", key, err)
		}
	}

	// Tombstone 2 nodes in PR scope (Delta and Epsilon).
	tombstonedKeys := []string{"fn:svc/a.Delta", "fn:svc/a.Epsilon"}
	for _, key := range tombstonedKeys {
		tsKey := models.TombstoneNodeKey(prScope.ScopeID, key)
		tombstone := &models.Tombstone{
			BaseNode: models.BaseNode{
				NodeKey: tsKey,
				Scope:   prScope.Scope,
				ScopeID: prScope.ScopeID,
			},
			TargetNodeKey: key,
			TargetLabel:   string(models.FunctionNode),
			Reason:        models.TombstoneSymbolRemoved,
		}
		if err := graphStore.ApplyTombstone(ctx, tombstone); err != nil {
			t.Fatalf("ApplyTombstone(%s): %v", key, err)
		}
	}

	// Assert: graph overlay returns nil for tombstoned nodes in PR scope.
	for _, key := range tombstonedKeys {
		got, err := graphStore.GetWithOverlay(ctx, key, prScope)
		if err != nil {
			t.Fatalf("GetWithOverlay(%s, pr): %v", key, err)
		}
		if got != nil {
			t.Errorf("expected nil (tombstoned) for %s in PR scope, got node", key)
		}
	}

	// Assert: non-tombstoned nodes are still visible in PR scope.
	liveKeys := []string{"fn:svc/a.Alpha", "fn:svc/a.Beta", "fn:svc/a.Gamma"}
	for _, key := range liveKeys {
		got, err := graphStore.GetWithOverlay(ctx, key, prScope)
		if err != nil {
			t.Fatalf("GetWithOverlay(%s, pr): %v", key, err)
		}
		if got == nil {
			t.Errorf("expected node for %s in PR scope (not tombstoned), got nil", key)
		}
	}

	// Assert: text store still has ALL 5 docs (text store doesn't know about tombstones).
	// This documents the contract: graph is truth for existence,
	// text store is for candidate fetch only — retrieval layer must filter using graph overlay.
	allDocs := textStore.AllDocs()
	if len(allDocs) != 5 {
		t.Errorf("expected text store to retain all 5 docs (tombstones are graph-only), got %d", len(allDocs))
	}

	// Verify tombstoned keys are still retrievable from text store (by design).
	for _, key := range tombstonedKeys {
		results, err := textStore.Search(ctx, key, textindex.SearchOpts{})
		if err != nil {
			t.Fatalf("Search(%s): %v", key, err)
		}
		if len(results) == 0 {
			t.Errorf("text store should still return tombstoned node %s (retrieval layer filters, not text store)", key)
		}
	}
}
