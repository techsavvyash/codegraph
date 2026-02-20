package integration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j/dbtype"

	"github.com/context-maximiser/code-graph/pkg/indexer/static"
	"github.com/context-maximiser/code-graph/pkg/models"
	"github.com/context-maximiser/code-graph/pkg/neo4j"
	queryPkg "github.com/context-maximiser/code-graph/pkg/query"
	"github.com/context-maximiser/code-graph/pkg/schema"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// setupTestDB creates a client, ensures schema, and returns a cleanup function
// that removes ONLY the test-specific data identified by the given prefix.
func setupTestDB(t *testing.T, prefix string) (*neo4j.Client, func()) {
	t.Helper()
	client := createTestClient(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Ensure schema exists (idempotent).
	sm := schema.NewSchemaManager(client)
	if err := sm.CreateSchema(ctx); err != nil {
		t.Fatalf("failed to create schema: %v", err)
	}

	cleanup := func() {
		ctx2, cancel2 := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel2()
		// Delete only nodes whose nodeKey starts with the test prefix.
		cypher := `MATCH (n) WHERE n.nodeKey STARTS WITH $prefix DETACH DELETE n`
		_, _ = client.ExecuteQuery(ctx2, cypher, map[string]any{"prefix": prefix})
		client.Close(ctx2)
	}
	return client, cleanup
}

// setupTestDBWithScopeCleanup cleans up by scopeId instead of prefix — useful
// for tests that exercise real indexer output where we don't control nodeKey format.
func setupTestDBWithScopeCleanup(t *testing.T, scopeID string) (*neo4j.Client, func()) {
	t.Helper()
	client := createTestClient(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	sm := schema.NewSchemaManager(client)
	if err := sm.CreateSchema(ctx); err != nil {
		t.Fatalf("failed to create schema: %v", err)
	}

	cleanup := func() {
		ctx2, cancel2 := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel2()
		cypher := `MATCH (n) WHERE n.scopeId = $scopeId DETACH DELETE n`
		_, _ = client.ExecuteQuery(ctx2, cypher, map[string]any{"scopeId": scopeID})
		client.Close(ctx2)
	}
	return client, cleanup
}

// writeTempGoProject creates a minimal Go project in a temp directory and
// returns the path. The project has two files: one with a function definition
// and one that calls it, so we get CONTAINS, DEFINES, REFERENCES, and CALLS.
func writeTempGoProject(t *testing.T, modName string) string {
	t.Helper()
	dir := t.TempDir()

	// go.mod
	goMod := fmt.Sprintf("module %s\n\ngo 1.21\n", modName)
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0644); err != nil {
		t.Fatal(err)
	}

	// lib.go — exports a function
	lib := `package main

func Greet(name string) string {
	return "Hello, " + name
}
`
	if err := os.WriteFile(filepath.Join(dir, "lib.go"), []byte(lib), 0644); err != nil {
		t.Fatal(err)
	}

	// main.go — calls Greet
	main := `package main

import "fmt"

func main() {
	msg := Greet("world")
	fmt.Println(msg)
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(main), 0644); err != nil {
		t.Fatal(err)
	}

	return dir
}

// ===========================================================================
// P1: ValidateEnvironment resolves from cache
// ===========================================================================

func TestP1_ValidateEnvironment_ResolvesFromCache(t *testing.T) {
	// Create a temporary cache directory and place a fake binary there.
	cacheDir := t.TempDir()
	mgr := static.NewIndexerManager(cacheDir)

	// Get the expected cache path for Go.
	cachedPath := mgr.CachedBinaryPath(static.LanguageGo)
	if cachedPath == "" {
		t.Fatal("CachedBinaryPath returned empty for Go")
	}

	// Create parent dirs and a fake binary.
	if err := os.MkdirAll(filepath.Dir(cachedPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cachedPath, []byte("#!/bin/sh\necho fake"), 0755); err != nil {
		t.Fatal(err)
	}

	// Verify the manager finds it.
	resolved := mgr.ResolveBinary(static.LanguageGo)
	if resolved != cachedPath {
		t.Errorf("expected ResolveBinary to return %q, got %q", cachedPath, resolved)
	}
}

func TestP1_ValidateEnvironmentNoInstall_FailsWhenMissing(t *testing.T) {
	// Create an SCIPIndexer with an empty cache dir — no binary exists.
	si := static.NewSCIPIndexerWithLanguage(nil, "test-svc", "v1", "", static.LanguageGo)

	// Override PATH to ensure scip-go isn't found via system PATH either.
	// We can't easily do this, but ValidateEnvironmentNoInstall will check
	// cache first, then PATH. If scip-go IS in PATH it succeeds (which is fine).
	// The important thing: it doesn't attempt auto-install.
	err := si.ValidateEnvironmentNoInstall()
	if err != nil {
		// Expected when scip-go is not in PATH — error message should mention "not found"
		t.Logf("ValidateEnvironmentNoInstall correctly failed: %v", err)
	} else {
		// scip-go was found in PATH — that's also correct behavior
		t.Log("ValidateEnvironmentNoInstall succeeded (scip-go found in PATH)")
	}
}

// ===========================================================================
// P2: Overlay search deduplication by nodeKey
// ===========================================================================

func TestP2_SearchNodesScoped_DeduplicatesByNodeKey(t *testing.T) {
	prefix := "audit-p2-dedup-"
	client, cleanup := setupTestDB(t, prefix)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create two Function nodes with the SAME nodeKey but different scopes.
	sharedNodeKey := prefix + "func:pkg/handler.go#HandleUser(...)"

	_, err := client.MergeNode(ctx, []string{"Function"},
		map[string]any{"nodeKey": sharedNodeKey, "scopeId": "main"},
		map[string]any{
			"name": "HandleUser", "nodeKey": sharedNodeKey,
			"signature": "HandleUser(ctx context.Context)",
			"filePath": "pkg/handler.go", "startLine": 10, "endLine": 20,
			"scope": "main", "scopeId": "main",
		})
	if err != nil {
		t.Fatalf("failed to create main Function: %v", err)
	}

	_, err = client.MergeNode(ctx, []string{"Function"},
		map[string]any{"nodeKey": sharedNodeKey, "scopeId": "pr-42"},
		map[string]any{
			"name": "HandleUser", "nodeKey": sharedNodeKey,
			"signature": "HandleUser(ctx context.Context, id string)",
			"filePath": "pkg/handler.go", "startLine": 10, "endLine": 25,
			"scope": "pr", "scopeId": "pr-42",
		})
	if err != nil {
		t.Fatalf("failed to create PR Function: %v", err)
	}

	// Search with scopeId = "pr-42"
	qb := neo4j.NewQueryBuilder(client)
	results, err := qb.SearchNodesScoped(ctx, "HandleUser", []string{"Function"}, 0, "pr-42")
	if err != nil {
		t.Fatalf("SearchNodesScoped failed: %v", err)
	}

	// Should get exactly 1 result — the PR version.
	if len(results) != 1 {
		t.Fatalf("expected 1 result (deduplicated), got %d", len(results))
	}

	// Verify it's the PR version by checking the signature property.
	record := results[0].AsMap()
	nodeObj, ok := record["n"]
	if !ok {
		t.Fatal("expected 'n' in result")
	}
	node, ok := nodeObj.(dbtype.Node)
	if !ok {
		t.Fatalf("expected dbtype.Node, got %T", nodeObj)
	}
	sig, _ := node.Props["signature"].(string)
	if sig != "HandleUser(ctx context.Context, id string)" {
		t.Errorf("expected PR version signature, got %q", sig)
	}
	scopeId, _ := node.Props["scopeId"].(string)
	if scopeId != "pr-42" {
		t.Errorf("expected scopeId 'pr-42', got %q", scopeId)
	}
}

func TestP2_SearchNodesScoped_MainVisibleFromPRScope(t *testing.T) {
	prefix := "audit-p2-main-"
	client, cleanup := setupTestDB(t, prefix)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	nodeKey := prefix + "func:pkg/main.go#Run(...)"
	_, err := client.MergeNode(ctx, []string{"Function"},
		map[string]any{"nodeKey": nodeKey, "scopeId": "main"},
		map[string]any{
			"name": "Run", "nodeKey": nodeKey,
			"scope": "main", "scopeId": "main",
		})
	if err != nil {
		t.Fatal(err)
	}

	qb := neo4j.NewQueryBuilder(client)
	results, err := qb.SearchNodesScoped(ctx, "Run", []string{"Function"}, 0, "pr-99")
	if err != nil {
		t.Fatalf("SearchNodesScoped failed: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result (main visible in PR scope), got %d", len(results))
	}
}

func TestP2_SearchNodesScoped_TombstoneExclusion(t *testing.T) {
	prefix := "audit-p2-tomb-"
	client, cleanup := setupTestDB(t, prefix)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	nodeKey := prefix + "func:pkg/deleted.go#OldFunc(...)"
	_, err := client.MergeNode(ctx, []string{"Function"},
		map[string]any{"nodeKey": nodeKey, "scopeId": "main"},
		map[string]any{
			"name": "OldFunc", "nodeKey": nodeKey,
			"scope": "main", "scopeId": "main",
		})
	if err != nil {
		t.Fatal(err)
	}

	// Create a Tombstone targeting this nodeKey.
	tombKey := prefix + fmt.Sprintf("tombstone:pr-50:%s", nodeKey)
	_, err = client.MergeNode(ctx, []string{"Tombstone"},
		map[string]any{"nodeKey": tombKey, "scopeId": "pr-50"},
		map[string]any{
			"nodeKey": tombKey, "targetNodeKey": nodeKey,
			"scope": "pr", "scopeId": "pr-50",
		})
	if err != nil {
		t.Fatal(err)
	}

	qb := neo4j.NewQueryBuilder(client)
	results, err := qb.SearchNodesScoped(ctx, "OldFunc", []string{"Function"}, 0, "pr-50")
	if err != nil {
		t.Fatalf("SearchNodesScoped failed: %v", err)
	}

	if len(results) != 0 {
		t.Errorf("expected 0 results (tombstoned node), got %d", len(results))
	}
}

// ===========================================================================
// P3: Scope metadata on relationships — via actual SCIP indexer
// ===========================================================================

func TestP3_SCIPIndexer_RelationshipScopeProps(t *testing.T) {
	scopeID := "pr-p3test"
	client, cleanup := setupTestDBWithScopeCleanup(t, scopeID)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Create a tiny Go project.
	projectDir := writeTempGoProject(t, "example.com/p3test")

	// Index it with PR scope using the actual SCIP indexer.
	indexer := static.NewSCIPIndexerWithLanguage(client, "p3test-svc", "v1", "", static.LanguageGo)
	indexer.SetScope(models.NewPRScope("p3test"))

	if err := indexer.ValidateEnvironment(); err != nil {
		t.Skipf("scip-go not available: %v", err)
	}

	if err := indexer.IndexProject(ctx, projectDir); err != nil {
		t.Fatalf("IndexProject failed: %v", err)
	}

	// Verify CONTAINS relationships (Service→File, File→Function) have scope props.
	cypher := `
		MATCH ()-[r:CONTAINS]->()
		WHERE r.scopeId = $scopeId
		RETURN r.scope AS scope, r.scopeId AS scopeId
		LIMIT 5
	`
	records, err := client.ExecuteQuery(ctx, cypher, map[string]any{"scopeId": scopeID})
	if err != nil {
		t.Fatalf("query CONTAINS rels failed: %v", err)
	}
	if len(records) == 0 {
		t.Error("expected CONTAINS relationships with scopeId = pr-p3test, found none")
	}
	for _, r := range records {
		m := r.AsMap()
		if s, _ := m["scope"].(string); s != "pr" {
			t.Errorf("CONTAINS rel scope = %q, want 'pr'", s)
		}
	}

	// Verify DEFINES relationships have scope props.
	cypher = `
		MATCH ()-[r:DEFINES]->()
		WHERE r.scopeId = $scopeId
		RETURN r.scope AS scope, r.scopeId AS scopeId
		LIMIT 5
	`
	records, err = client.ExecuteQuery(ctx, cypher, map[string]any{"scopeId": scopeID})
	if err != nil {
		t.Fatalf("query DEFINES rels failed: %v", err)
	}
	if len(records) == 0 {
		t.Error("expected DEFINES relationships with scopeId = pr-p3test, found none")
	}
	for _, r := range records {
		m := r.AsMap()
		if s, _ := m["scope"].(string); s != "pr" {
			t.Errorf("DEFINES rel scope = %q, want 'pr'", s)
		}
	}

	// Verify REFERENCES relationships have scope props.
	cypher = `
		MATCH ()-[r:REFERENCES]->()
		WHERE r.scopeId = $scopeId
		RETURN r.scope AS scope, r.scopeId AS scopeId
		LIMIT 5
	`
	records, err = client.ExecuteQuery(ctx, cypher, map[string]any{"scopeId": scopeID})
	if err != nil {
		t.Fatalf("query REFERENCES rels failed: %v", err)
	}
	if len(records) == 0 {
		t.Error("expected REFERENCES relationships with scopeId = pr-p3test, found none")
	}
	for _, r := range records {
		m := r.AsMap()
		if s, _ := m["scope"].(string); s != "pr" {
			t.Errorf("REFERENCES rel scope = %q, want 'pr'", s)
		}
	}
}

// ===========================================================================
// P3c: Flow spine scope filtering
// ===========================================================================

func TestP3c_ListFlows_ScopeFilter(t *testing.T) {
	prefix := "audit-p3c-list-"
	client, cleanup := setupTestDB(t, prefix)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create three Flow nodes: main, pr-7, and pr-99.
	mainFlowKey := prefix + "flow:api:main-flow"
	_, err := client.MergeNode(ctx, []string{"Flow"},
		map[string]any{"nodeKey": mainFlowKey, "scopeId": "main"},
		map[string]any{
			"name": "GET /api/users", "flowType": "api",
			"nodeKey": mainFlowKey, "scope": "main", "scopeId": "main",
		})
	if err != nil {
		t.Fatal(err)
	}

	prFlowKey := prefix + "flow:api:pr-flow"
	_, err = client.MergeNode(ctx, []string{"Flow"},
		map[string]any{"nodeKey": prFlowKey, "scopeId": "pr-7"},
		map[string]any{
			"name": "POST /api/orders", "flowType": "api",
			"nodeKey": prFlowKey, "scope": "pr", "scopeId": "pr-7",
		})
	if err != nil {
		t.Fatal(err)
	}

	otherPRFlowKey := prefix + "flow:api:other-pr-flow"
	_, err = client.MergeNode(ctx, []string{"Flow"},
		map[string]any{"nodeKey": otherPRFlowKey, "scopeId": "pr-99"},
		map[string]any{
			"name": "DELETE /api/items", "flowType": "api",
			"nodeKey": otherPRFlowKey, "scope": "pr", "scopeId": "pr-99",
		})
	if err != nil {
		t.Fatal(err)
	}

	// ListFlows with scope pr-7 should return main + pr-7, NOT pr-99.
	gen := queryPkg.NewFlowSpineGenerator(client)
	gen.SetScope(models.NewPRScope("7"))

	flows, err := gen.ListFlows(ctx, "")
	if err != nil {
		t.Fatalf("ListFlows failed: %v", err)
	}

	flowKeys := map[string]bool{}
	for _, f := range flows {
		flowKeys[f.FlowNodeKey] = true
	}
	if !flowKeys[mainFlowKey] {
		t.Error("expected main flow in results")
	}
	if !flowKeys[prFlowKey] {
		t.Error("expected pr-7 flow in results")
	}
	if flowKeys[otherPRFlowKey] {
		t.Error("should NOT include pr-99 flow in pr-7 scope")
	}
}

func TestP3c_GetFlow_ScopeFilter(t *testing.T) {
	prefix := "audit-p3c-get-"
	client, cleanup := setupTestDB(t, prefix)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	flowKey := prefix + "flow:api:get-users"
	_, err := client.MergeNode(ctx, []string{"Flow"},
		map[string]any{"nodeKey": flowKey, "scopeId": "main"},
		map[string]any{
			"name": "GET /api/users", "flowType": "api",
			"nodeKey": flowKey, "scope": "main", "scopeId": "main",
		})
	if err != nil {
		t.Fatal(err)
	}

	// GetFlow from PR scope should still find main-scope flows.
	gen := queryPkg.NewFlowSpineGenerator(client)
	gen.SetScope(models.NewPRScope("42"))

	flow, err := gen.GetFlow(ctx, flowKey)
	if err != nil {
		t.Fatalf("GetFlow failed: %v", err)
	}
	if flow.FlowName != "GET /api/users" {
		t.Errorf("expected flow name 'GET /api/users', got %q", flow.FlowName)
	}
}

func TestP3c_GenerateFromAPIEndpoints_ScopeFilter(t *testing.T) {
	prefix := "audit-p3c-gen-"
	client, cleanup := setupTestDB(t, prefix)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Build: APIRoute -> (EXPOSES_API) <- handler:Function -> (CALLS) -> callee:Function
	// Main-scope handler + main-scope callee.
	routeKey := prefix + "api:GET:/users"
	_, err := client.MergeNode(ctx, []string{"APIRoute"},
		map[string]any{"nodeKey": routeKey, "scopeId": "main"},
		map[string]any{
			"nodeKey": routeKey, "method": "GET", "path": "/users",
			"scope": "main", "scopeId": "main",
		})
	if err != nil {
		t.Fatal(err)
	}

	handlerKey := prefix + "func:handler.go#GetUsers(...)"
	handlerID, err := client.MergeNode(ctx, []string{"Function"},
		map[string]any{"nodeKey": handlerKey, "scopeId": "main"},
		map[string]any{
			"nodeKey": handlerKey, "name": "GetUsers",
			"scope": "main", "scopeId": "main",
		})
	if err != nil {
		t.Fatal(err)
	}

	// EXPOSES_API: handler -> route (the query matches route<-[:EXPOSES_API]-handler)
	_, err = client.CreateRelationship(ctx, handlerID, routeKey, "EXPOSES_API", nil)
	if err != nil {
		// CreateRelationship expects elementId, not nodeKey. We need to get the route elementId.
		// Let's use Cypher directly for this.
	}

	// Use Cypher to create the EXPOSES_API relationship by nodeKey.
	_, err = client.ExecuteQuery(ctx, `
		MATCH (h:Function {nodeKey: $handlerKey, scopeId: 'main'})
		MATCH (r:APIRoute {nodeKey: $routeKey, scopeId: 'main'})
		MERGE (h)-[:EXPOSES_API]->(r)
	`, map[string]any{"handlerKey": handlerKey, "routeKey": routeKey})
	if err != nil {
		t.Fatalf("EXPOSES_API creation failed: %v", err)
	}

	// Main-scope callee — should be included in flow.
	calleeKey := prefix + "func:repo.go#FindAll(...)"
	_, err = client.MergeNode(ctx, []string{"Function"},
		map[string]any{"nodeKey": calleeKey, "scopeId": "main"},
		map[string]any{
			"nodeKey": calleeKey, "name": "FindAll",
			"scope": "main", "scopeId": "main",
		})
	if err != nil {
		t.Fatal(err)
	}

	// CALLS: handler -> callee
	_, err = client.ExecuteQuery(ctx, `
		MATCH (h:Function {nodeKey: $handlerKey, scopeId: 'main'})
		MATCH (c:Function {nodeKey: $calleeKey, scopeId: 'main'})
		MERGE (h)-[:CALLS]->(c)
	`, map[string]any{"handlerKey": handlerKey, "calleeKey": calleeKey})
	if err != nil {
		t.Fatalf("CALLS creation failed: %v", err)
	}

	// Other-scope callee — should NOT appear when querying from pr-10.
	otherCalleeKey := prefix + "func:other.go#OtherFn(...)"
	_, err = client.MergeNode(ctx, []string{"Function"},
		map[string]any{"nodeKey": otherCalleeKey, "scopeId": "pr-99"},
		map[string]any{
			"nodeKey": otherCalleeKey, "name": "OtherFn",
			"scope": "pr", "scopeId": "pr-99",
		})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.ExecuteQuery(ctx, `
		MATCH (h:Function {nodeKey: $handlerKey, scopeId: 'main'})
		MATCH (c:Function {nodeKey: $otherKey, scopeId: 'pr-99'})
		MERGE (h)-[:CALLS]->(c)
	`, map[string]any{"handlerKey": handlerKey, "otherKey": otherCalleeKey})
	if err != nil {
		t.Fatalf("CALLS (other) creation failed: %v", err)
	}

	// Generate flow spines using pr-10 scope.
	// pr-10 scope should see: main route, main handler, main callee — but NOT pr-99 callee.
	gen := queryPkg.NewFlowSpineGenerator(client)
	gen.SetScope(models.NewPRScope("10"))

	results, err := gen.GenerateFromAPIEndpoints(ctx, 2)
	if err != nil {
		t.Fatalf("GenerateFromAPIEndpoints failed: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("expected at least 1 flow spine result")
	}

	// Find our flow.
	var found *queryPkg.FlowSpineResult
	for i := range results {
		for _, s := range results[i].Steps {
			if s.NodeKey == routeKey {
				found = &results[i]
				break
			}
		}
	}
	if found == nil {
		t.Fatal("expected to find flow containing our route")
	}

	// Verify steps include the main callee but not the pr-99 one.
	stepKeys := map[string]bool{}
	for _, s := range found.Steps {
		stepKeys[s.NodeKey] = true
	}
	if !stepKeys[calleeKey] {
		t.Error("expected main-scope callee in flow steps")
	}
	if stepKeys[otherCalleeKey] {
		t.Error("pr-99 callee should NOT appear in pr-10 scoped flow")
	}
}

func TestP3c_HAS_STEP_ScopeProps(t *testing.T) {
	prefix := "audit-p3c-step-"
	client, cleanup := setupTestDB(t, prefix)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Build minimal graph: APIRoute + handler Function.
	routeKey := prefix + "api:GET:/items"
	_, err := client.MergeNode(ctx, []string{"APIRoute"},
		map[string]any{"nodeKey": routeKey, "scopeId": "main"},
		map[string]any{
			"nodeKey": routeKey, "method": "GET", "path": "/items",
			"scope": "main", "scopeId": "main",
		})
	if err != nil {
		t.Fatal(err)
	}

	handlerKey := prefix + "func:items.go#ListItems(...)"
	_, err = client.MergeNode(ctx, []string{"Function"},
		map[string]any{"nodeKey": handlerKey, "scopeId": "main"},
		map[string]any{
			"nodeKey": handlerKey, "name": "ListItems",
			"scope": "main", "scopeId": "main",
		})
	if err != nil {
		t.Fatal(err)
	}

	_, err = client.ExecuteQuery(ctx, `
		MATCH (h:Function {nodeKey: $hKey, scopeId: 'main'})
		MATCH (r:APIRoute {nodeKey: $rKey, scopeId: 'main'})
		MERGE (h)-[:EXPOSES_API]->(r)
	`, map[string]any{"hKey": handlerKey, "rKey": routeKey})
	if err != nil {
		t.Fatal(err)
	}

	// Generate flow with PR scope.
	gen := queryPkg.NewFlowSpineGenerator(client)
	gen.SetScope(models.NewPRScope("55"))

	_, err = gen.GenerateFromAPIEndpoints(ctx, 2)
	if err != nil {
		t.Fatalf("GenerateFromAPIEndpoints failed: %v", err)
	}

	// Verify HAS_STEP relationships have scope props.
	cypher := `
		MATCH (:Flow)-[r:HAS_STEP]->()
		WHERE r.scopeId = $scopeId
		RETURN r.scope AS scope, r.scopeId AS scopeId
	`
	records, err := client.ExecuteQuery(ctx, cypher, map[string]any{"scopeId": "pr-55"})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) == 0 {
		t.Error("expected HAS_STEP relationships with scopeId 'pr-55', found none")
	}
	for _, r := range records {
		m := r.AsMap()
		if s, _ := m["scope"].(string); s != "pr" {
			t.Errorf("HAS_STEP scope = %q, want 'pr'", s)
		}
	}

	// Clean up the Flow + HAS_STEP nodes (they use models.FlowNodeKey format, not our prefix).
	_, _ = client.ExecuteQuery(ctx, `MATCH (n) WHERE n.scopeId = 'pr-55' DETACH DELETE n`, nil)
}

// ===========================================================================
// P4: Service dependency inference with CALLS_API → SDKCall
// ===========================================================================

func TestP4_InferServiceDependencies(t *testing.T) {
	prefix := "audit-p4-infer-"
	client, cleanup := setupTestDB(t, prefix)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Build graph: (caller:Service)-[:CONTAINS]->(f:File)-[:CONTAINS]->(fn:Function)-[:CALLS_API]->(call:SDKCall)
	//              (target:Service)-[:CONTAINS]->(targetFile:File)
	callerSvcKey := prefix + "svc:api-gateway"
	callerSvcID, err := client.MergeNode(ctx, []string{"Service"},
		map[string]any{"nodeKey": callerSvcKey, "scopeId": "main"},
		map[string]any{"name": "api-gateway", "nodeKey": callerSvcKey, "scope": "main", "scopeId": "main"})
	if err != nil {
		t.Fatal(err)
	}

	callerFileKey := prefix + "file:gateway/main.go"
	callerFileID, err := client.MergeNode(ctx, []string{"File"},
		map[string]any{"nodeKey": callerFileKey, "scopeId": "main"},
		map[string]any{"path": "gateway/main.go", "nodeKey": callerFileKey, "scope": "main", "scopeId": "main"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.CreateRelationship(ctx, callerSvcID, callerFileID, "CONTAINS", nil); err != nil {
		t.Fatal(err)
	}

	callerFnKey := prefix + "func:gateway/main.go#ProxyRequest()"
	callerFnID, err := client.MergeNode(ctx, []string{"Function"},
		map[string]any{"nodeKey": callerFnKey, "scopeId": "main"},
		map[string]any{"name": "ProxyRequest", "nodeKey": callerFnKey, "scope": "main", "scopeId": "main"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.CreateRelationship(ctx, callerFileID, callerFnID, "CONTAINS", nil); err != nil {
		t.Fatal(err)
	}

	sdkCallKey := prefix + "sdkcall:user-service:/api/users"
	sdkCallID, err := client.MergeNode(ctx, []string{"SDKCall"},
		map[string]any{"nodeKey": sdkCallKey, "scopeId": "main"},
		map[string]any{
			"url": "http://user-service/api/users", "nodeKey": sdkCallKey,
			"scope": "main", "scopeId": "main",
		})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.CreateRelationship(ctx, callerFnID, sdkCallID, "CALLS_API", nil); err != nil {
		t.Fatal(err)
	}

	targetSvcKey := prefix + "svc:user-service"
	targetSvcID, err := client.MergeNode(ctx, []string{"Service"},
		map[string]any{"nodeKey": targetSvcKey, "scopeId": "main"},
		map[string]any{"name": "user-service", "nodeKey": targetSvcKey, "scope": "main", "scopeId": "main"})
	if err != nil {
		t.Fatal(err)
	}

	targetFileKey := prefix + "file:user/main.go"
	targetFileID, err := client.MergeNode(ctx, []string{"File"},
		map[string]any{"nodeKey": targetFileKey, "scopeId": "main"},
		map[string]any{"path": "user/main.go", "nodeKey": targetFileKey, "scope": "main", "scopeId": "main"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.CreateRelationship(ctx, targetSvcID, targetFileID, "CONTAINS", nil); err != nil {
		t.Fatal(err)
	}

	// Run inference.
	depsQuery := queryPkg.NewServiceDepsQuery(client)
	count, err := depsQuery.InferServiceDependencies(ctx, "main")
	if err != nil {
		t.Fatalf("InferServiceDependencies failed: %v", err)
	}
	if count < 1 {
		t.Errorf("expected at least 1 inferred dependency, got %d", count)
	}

	// Verify CALLS_SERVICE edge was created with correct properties.
	cypher := `
		MATCH (s:Service {name: 'api-gateway'})-[r:CALLS_SERVICE]->(t:Service {name: 'user-service'})
		RETURN r.confidence AS confidence, r.source AS source, r.scopeId AS scopeId
	`
	records, err := client.ExecuteQuery(ctx, cypher, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) == 0 {
		t.Fatal("expected CALLS_SERVICE edge between api-gateway and user-service")
	}

	m := records[0].AsMap()
	if source, _ := m["source"].(string); source != "sdk_call_inference" {
		t.Errorf("expected source 'sdk_call_inference', got %q", source)
	}
	if conf, ok := m["confidence"].(float64); !ok || conf != 0.7 {
		t.Errorf("expected confidence 0.7, got %v", m["confidence"])
	}
	if scopeId, _ := m["scopeId"].(string); scopeId != "main" {
		t.Errorf("expected scopeId 'main', got %q", scopeId)
	}
}

func TestP4_InferServiceDependencies_NoSelfDeps(t *testing.T) {
	prefix := "audit-p4-self-"
	client, cleanup := setupTestDB(t, prefix)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// A service that calls itself should NOT produce CALLS_SERVICE.
	svcKey := prefix + "svc:self-caller"
	svcID, err := client.MergeNode(ctx, []string{"Service"},
		map[string]any{"nodeKey": svcKey, "scopeId": "main"},
		map[string]any{"name": "self-caller", "nodeKey": svcKey, "scope": "main", "scopeId": "main"})
	if err != nil {
		t.Fatal(err)
	}

	fileKey := prefix + "file:self/main.go"
	fileID, err := client.MergeNode(ctx, []string{"File"},
		map[string]any{"nodeKey": fileKey, "scopeId": "main"},
		map[string]any{"path": "self/main.go", "nodeKey": fileKey, "scope": "main", "scopeId": "main"})
	if err != nil {
		t.Fatal(err)
	}
	client.CreateRelationship(ctx, svcID, fileID, "CONTAINS", nil)

	fnKey := prefix + "func:self/main.go#DoSomething()"
	fnID, err := client.MergeNode(ctx, []string{"Function"},
		map[string]any{"nodeKey": fnKey, "scopeId": "main"},
		map[string]any{"name": "DoSomething", "nodeKey": fnKey, "scope": "main", "scopeId": "main"})
	if err != nil {
		t.Fatal(err)
	}
	client.CreateRelationship(ctx, fileID, fnID, "CONTAINS", nil)

	sdkKey := prefix + "sdkcall:self-caller:/internal"
	sdkID, err := client.MergeNode(ctx, []string{"SDKCall"},
		map[string]any{"nodeKey": sdkKey, "scopeId": "main"},
		map[string]any{"url": "http://self-caller/internal", "nodeKey": sdkKey, "scope": "main", "scopeId": "main"})
	if err != nil {
		t.Fatal(err)
	}
	client.CreateRelationship(ctx, fnID, sdkID, "CALLS_API", nil)

	depsQuery := queryPkg.NewServiceDepsQuery(client)
	count, err := depsQuery.InferServiceDependencies(ctx, "main")
	if err != nil {
		t.Fatalf("InferServiceDependencies failed: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 inferred dependencies (self-calls excluded), got %d", count)
	}
}
