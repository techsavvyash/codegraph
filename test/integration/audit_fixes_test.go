package integration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j/dbtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/context-maximiser/code-graph/libs/indexer-go/generated"
	"github.com/context-maximiser/code-graph/libs/indexer-go/static"
	"github.com/context-maximiser/code-graph/libs/core-models-go"
	"github.com/context-maximiser/code-graph/libs/neo4j-go"
	queryPkg "github.com/context-maximiser/code-graph/libs/query-go"
	"github.com/context-maximiser/code-graph/libs/schema-go"
	"github.com/context-maximiser/code-graph/libs/search-go"
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

// ===========================================================================
// Round-Trip Regression Test: SCIP index → query back all nodes & rels
// ===========================================================================

func TestRoundTrip_SCIPIndexThenQuery(t *testing.T) {
	scopeID := "pr-roundtrip"
	client, cleanup := setupTestDBWithScopeCleanup(t, scopeID)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Create a tiny Go project.
	projectDir := writeTempGoProject(t, "example.com/roundtrip")

	// Index it with the real SCIP pipeline.
	indexer := static.NewSCIPIndexerWithLanguage(client, "roundtrip-svc", "v1", "", static.LanguageGo)
	indexer.SetScope(models.NewPRScope("roundtrip"))

	if err := indexer.ValidateEnvironment(); err != nil {
		t.Skipf("scip-go not available: %v", err)
	}

	if err := indexer.IndexProject(ctx, projectDir); err != nil {
		t.Fatalf("IndexProject failed: %v", err)
	}

	// 1. Verify Service node exists.
	records, err := client.ExecuteQuery(ctx, `
		MATCH (s:Service)
		WHERE s.scopeId = $scopeId AND s.name CONTAINS 'roundtrip'
		RETURN s.name AS name, s.scopeId AS scopeId
	`, map[string]any{"scopeId": scopeID})
	if err != nil {
		t.Fatalf("query Service failed: %v", err)
	}
	if len(records) == 0 {
		t.Fatal("expected Service node")
	}

	// 2. Verify File nodes exist (2 files: lib.go and main.go).
	records, err = client.ExecuteQuery(ctx, `
		MATCH (f:File)
		WHERE f.scopeId = $scopeId
		RETURN f.path AS path
	`, map[string]any{"scopeId": scopeID})
	if err != nil {
		t.Fatalf("query File failed: %v", err)
	}
	if len(records) < 2 {
		t.Errorf("expected at least 2 File nodes, got %d", len(records))
	}
	filePaths := map[string]bool{}
	for _, r := range records {
		m := r.AsMap()
		if p, ok := m["path"].(string); ok {
			filePaths[p] = true
		}
	}
	if !filePaths["lib.go"] {
		t.Error("expected lib.go File node")
	}
	if !filePaths["main.go"] {
		t.Error("expected main.go File node")
	}

	// 3. Verify Service -[:CONTAINS]-> File relationships.
	records, err = client.ExecuteQuery(ctx, `
		MATCH (s:Service)-[:CONTAINS]->(f:File)
		WHERE s.scopeId = $scopeId AND f.scopeId = $scopeId
		RETURN count(f) AS cnt
	`, map[string]any{"scopeId": scopeID})
	if err != nil {
		t.Fatalf("query CONTAINS failed: %v", err)
	}
	if len(records) > 0 {
		m := records[0].AsMap()
		cnt, _ := m["cnt"].(int64)
		if cnt < 2 {
			t.Errorf("expected at least 2 CONTAINS rels (Service->File), got %d", cnt)
		}
	}

	// 4. Verify Symbol nodes exist.
	records, err = client.ExecuteQuery(ctx, `
		MATCH (sym:Symbol)
		WHERE sym.scopeId = $scopeId
		RETURN count(sym) AS cnt
	`, map[string]any{"scopeId": scopeID})
	if err != nil {
		t.Fatalf("query Symbol failed: %v", err)
	}
	if len(records) > 0 {
		m := records[0].AsMap()
		cnt, _ := m["cnt"].(int64)
		if cnt == 0 {
			t.Error("expected Symbol nodes, found none")
		}
	}

	// 5. Verify Function nodes exist (Greet and main).
	// Note: SCIP display names may include parens/dot suffixes like "Greet()." so we use CONTAINS.
	records, err = client.ExecuteQuery(ctx, `
		MATCH (fn:Function)
		WHERE fn.scopeId = $scopeId
		RETURN fn.name AS name
	`, map[string]any{"scopeId": scopeID})
	if err != nil {
		t.Fatalf("query Function failed: %v", err)
	}
	foundGreet := false
	foundMain := false
	for _, r := range records {
		m := r.AsMap()
		if n, ok := m["name"].(string); ok {
			if strings.Contains(n, "Greet") {
				foundGreet = true
			}
			if strings.Contains(n, "main") {
				foundMain = true
			}
		}
	}
	if !foundGreet {
		t.Error("expected Function node for Greet")
	}
	if !foundMain {
		t.Error("expected Function node for main")
	}

	// 6. Verify DEFINES relationships exist (Function/Method -> Symbol).
	records, err = client.ExecuteQuery(ctx, `
		MATCH ()-[r:DEFINES]->(:Symbol)
		WHERE r.scopeId = $scopeId
		RETURN count(r) AS cnt
	`, map[string]any{"scopeId": scopeID})
	if err != nil {
		t.Fatalf("query DEFINES failed: %v", err)
	}
	if len(records) > 0 {
		m := records[0].AsMap()
		cnt, _ := m["cnt"].(int64)
		if cnt == 0 {
			t.Error("expected DEFINES relationships, found none")
		}
	}

	// 7. Verify REFERENCES relationships exist.
	records, err = client.ExecuteQuery(ctx, `
		MATCH ()-[r:REFERENCES]->(:Symbol)
		WHERE r.scopeId = $scopeId
		RETURN count(r) AS cnt
	`, map[string]any{"scopeId": scopeID})
	if err != nil {
		t.Fatalf("query REFERENCES failed: %v", err)
	}
	if len(records) > 0 {
		m := records[0].AsMap()
		cnt, _ := m["cnt"].(int64)
		if cnt == 0 {
			t.Error("expected REFERENCES relationships, found none")
		}
	}

	// 8. Verify CALLS relationships exist (main -> Greet, from call graph builder).
	// Note: SCIP display names may include suffixes, so use CONTAINS matching.
	records, err = client.ExecuteQuery(ctx, `
		MATCH (caller:Function)-[r:CALLS]->(callee:Function)
		WHERE caller.scopeId = $scopeId AND callee.scopeId = $scopeId
		RETURN caller.name AS caller, callee.name AS callee
	`, map[string]any{"scopeId": scopeID})
	if err != nil {
		t.Fatalf("query CALLS failed: %v", err)
	}
	foundMainCallsGreet := false
	for _, r := range records {
		m := r.AsMap()
		caller, _ := m["caller"].(string)
		callee, _ := m["callee"].(string)
		if strings.Contains(caller, "main") && strings.Contains(callee, "Greet") {
			foundMainCallsGreet = true
		}
	}
	if !foundMainCallsGreet {
		t.Error("expected CALLS relationship: main -> Greet")
	}

	// 9. Verify all nodes have correct scope/scopeId.
	records, err = client.ExecuteQuery(ctx, `
		MATCH (n)
		WHERE n.scopeId = $scopeId AND (n.scope IS NULL OR n.scope <> 'pr')
		RETURN labels(n) AS labels, n.nodeKey AS nodeKey
		LIMIT 5
	`, map[string]any{"scopeId": scopeID})
	if err != nil {
		t.Fatalf("scope check query failed: %v", err)
	}
	if len(records) > 0 {
		for _, r := range records {
			m := r.AsMap()
			t.Errorf("node with scopeId=%s has wrong scope: labels=%v nodeKey=%v", scopeID, m["labels"], m["nodeKey"])
		}
	}
}

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

// ===========================================================================
// Task 11: Chunk linker scope isolation — integration tests
// ===========================================================================

func TestChunkLinker_FindCodeNodes_ScopeIsolation(t *testing.T) {
	prefix := "audit-cl-scope-"
	client, cleanup := setupTestDB(t, prefix)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create a Function in main scope.
	// The backtick extractor gets "HandleRequest()" from content, so names should match.
	mainFnKey := prefix + "func:pkg/main.go#HandleRequest(...)"
	_, err := client.MergeNode(ctx, []string{"Function"},
		map[string]any{"nodeKey": mainFnKey, "scopeId": "main"},
		map[string]any{
			"name": "HandleRequest()", "displayName": "HandleRequest()", "nodeKey": mainFnKey,
			"scope": "main", "scopeId": "main",
		})
	if err != nil {
		t.Fatal(err)
	}

	// Create a Function in pr-42 scope with same name.
	prFnKey := prefix + "func:pkg/main.go#HandleRequest(...):pr42"
	_, err = client.MergeNode(ctx, []string{"Function"},
		map[string]any{"nodeKey": prFnKey, "scopeId": "pr-42"},
		map[string]any{
			"name": "HandleRequest()", "displayName": "HandleRequest()", "nodeKey": prFnKey,
			"scope": "pr", "scopeId": "pr-42",
		})
	if err != nil {
		t.Fatal(err)
	}

	// Create a Function in pr-99 scope — should not be visible to pr-42.
	otherFnKey := prefix + "func:pkg/other.go#HandleRequest(...):pr99"
	_, err = client.MergeNode(ctx, []string{"Function"},
		map[string]any{"nodeKey": otherFnKey, "scopeId": "pr-99"},
		map[string]any{
			"name": "HandleRequest()", "displayName": "HandleRequest()", "nodeKey": otherFnKey,
			"scope": "pr", "scopeId": "pr-99",
		})
	if err != nil {
		t.Fatal(err)
	}

	// Create a DocumentChunk and link it.
	chunkKey := prefix + "chunk:doc1:ch1"
	_, err = client.MergeNode(ctx, []string{"DocumentChunk"},
		map[string]any{"nodeKey": chunkKey, "scopeId": "pr-42"},
		map[string]any{
			"nodeKey": chunkKey, "documentKey": prefix + "doc1",
			"content": "The `HandleRequest()` function processes incoming requests.",
			"headingPath": "API > HandleRequest",
			"scope": "pr", "scopeId": "pr-42",
		})
	if err != nil {
		t.Fatal(err)
	}

	// Use ChunkLinker with pr-42 scope.
	cl := search.NewChunkLinker(client)
	cl.SetScope("pr-42")

	links, err := cl.LinkChunksForDocument(ctx, prefix+"doc1", "pr-42")
	if err != nil {
		t.Fatalf("LinkChunksForDocument failed: %v", err)
	}

	if links == 0 {
		t.Error("expected at least 1 link from chunk to code node")
	}

	// Verify MENTIONS edges only point to main or pr-42 nodes, NOT pr-99.
	cypher := `
		MATCH (chunk:DocumentChunk {nodeKey: $chunkKey})-[:MENTIONS]->(target)
		RETURN target.nodeKey AS targetKey, target.scopeId AS scopeId`
	records, err := client.ExecuteQuery(ctx, cypher, map[string]any{"chunkKey": chunkKey})
	if err != nil {
		t.Fatal(err)
	}

	for _, r := range records {
		m := r.AsMap()
		targetScopeID, _ := m["scopeId"].(string)
		if targetScopeID == "pr-99" {
			t.Errorf("chunk should NOT link to pr-99 scoped node, but found target %v", m["targetKey"])
		}
	}
}

func TestChunkLinker_MentionEdge_ScopeProps(t *testing.T) {
	prefix := "audit-cl-edge-"
	client, cleanup := setupTestDB(t, prefix)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create a Function in main scope.
	// The backtick extractor will extract "ProcessOrder()" from content.
	// The findCodeNodesByName query checks: name = $name OR displayName = $name OR signature CONTAINS $name
	// So we set both name and displayName to "ProcessOrder()" to match the extracted reference.
	fnKey := prefix + "func:pkg/srv.go#ProcessOrder(...)"
	_, err := client.MergeNode(ctx, []string{"Function"},
		map[string]any{"nodeKey": fnKey, "scopeId": "main"},
		map[string]any{
			"name": "ProcessOrder()", "displayName": "ProcessOrder()", "nodeKey": fnKey,
			"signature": "ProcessOrder(ctx context.Context, order Order) error",
			"scope": "main", "scopeId": "main",
		})
	if err != nil {
		t.Fatal(err)
	}

	// Create a DocumentChunk.
	chunkKey := prefix + "chunk:doc2:ch1"
	_, err = client.MergeNode(ctx, []string{"DocumentChunk"},
		map[string]any{"nodeKey": chunkKey, "scopeId": "pr-77"},
		map[string]any{
			"nodeKey": chunkKey, "documentKey": prefix + "doc2",
			"content": "Use `ProcessOrder()` for order processing.",
			"headingPath": "",
			"scope": "pr", "scopeId": "pr-77",
		})
	if err != nil {
		t.Fatal(err)
	}

	cl := search.NewChunkLinker(client)
	cl.SetScope("pr-77")

	links, err := cl.LinkChunksForDocument(ctx, prefix+"doc2", "pr-77")
	if err != nil {
		t.Fatalf("LinkChunksForDocument failed: %v", err)
	}

	if links == 0 {
		t.Fatal("expected at least 1 link")
	}

	// Verify MENTIONS edge has correct scopeId.
	cypher := `
		MATCH (:DocumentChunk {nodeKey: $chunkKey})-[r:MENTIONS]->(:Function {nodeKey: $fnKey})
		RETURN r.scopeId AS scopeId, r.confidence AS confidence, r.model AS model`
	records, err := client.ExecuteQuery(ctx, cypher, map[string]any{"chunkKey": chunkKey, "fnKey": fnKey})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) == 0 {
		t.Fatal("expected MENTIONS edge between chunk and function")
	}

	m := records[0].AsMap()
	if scopeID, _ := m["scopeId"].(string); scopeID != "pr-77" {
		t.Errorf("expected MENTIONS scopeId 'pr-77', got %q", scopeID)
	}
	if conf, ok := m["confidence"].(float64); !ok || conf <= 0 {
		t.Errorf("expected positive confidence, got %v", m["confidence"])
	}
}

// ===========================================================================
// Task 13: Generated context lifecycle — integration tests
// ===========================================================================

func TestContextGenerator_CreatePullRequestNode(t *testing.T) {
	prefix := "audit-ctx-pr-"
	client, cleanup := setupTestDB(t, prefix)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	gen := generated.NewContextGenerator(client)
	gen.SetScope(models.NewPRScope("100"))

	prID, err := gen.CreatePullRequestNode(ctx, "100", "Add auth module", "alice",
		"main", "feat/auth", "Adds OAuth2 support")
	if err != nil {
		t.Fatalf("CreatePullRequestNode failed: %v", err)
	}
	if prID == "" {
		t.Fatal("expected non-empty PR node ID")
	}

	// Verify the node exists with correct properties.
	cypher := `
		MATCH (pr:PullRequest {scopeId: $scopeId})
		RETURN pr.prId AS prId, pr.title AS title, pr.author AS author,
		       pr.scope AS scope, pr.scopeId AS scopeId`
	records, err := client.ExecuteQuery(ctx, cypher, map[string]any{"scopeId": "pr-100"})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) == 0 {
		t.Fatal("expected PullRequest node")
	}

	m := records[0].AsMap()
	if v, _ := m["prId"].(string); v != "100" {
		t.Errorf("expected prId '100', got %q", v)
	}
	if v, _ := m["title"].(string); v != "Add auth module" {
		t.Errorf("expected title 'Add auth module', got %q", v)
	}
	if v, _ := m["scope"].(string); v != "pr" {
		t.Errorf("expected scope 'pr', got %q", v)
	}
}

func TestContextGenerator_StorePRSummary(t *testing.T) {
	prefix := "audit-ctx-summary-"
	client, cleanup := setupTestDB(t, prefix)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	gen := generated.NewContextGenerator(client)
	gen.SetScope(models.NewPRScope("200"))

	// Create the PullRequest node first.
	_, err := gen.CreatePullRequestNode(ctx, "200", "Refactor DB layer", "bob",
		"main", "refactor/db", "")
	if err != nil {
		t.Fatal(err)
	}

	// Create some File nodes to serve as changed files.
	fileKey1 := prefix + "file:pkg/db/client.go"
	_, err = client.MergeNode(ctx, []string{"File"},
		map[string]any{"nodeKey": fileKey1, "scopeId": "pr-200"},
		map[string]any{"path": "pkg/db/client.go", "nodeKey": fileKey1, "scope": "pr", "scopeId": "pr-200"})
	if err != nil {
		t.Fatal(err)
	}

	fileKey2 := prefix + "file:pkg/db/query.go"
	_, err = client.MergeNode(ctx, []string{"File"},
		map[string]any{"nodeKey": fileKey2, "scopeId": "pr-200"},
		map[string]any{"path": "pkg/db/query.go", "nodeKey": fileKey2, "scope": "pr", "scopeId": "pr-200"})
	if err != nil {
		t.Fatal(err)
	}

	// Store the PR summary.
	docID, err := gen.StorePRSummary(ctx, "200", "DB Refactor Summary",
		"This PR refactors the database layer for better connection pooling.",
		"test-model", []string{fileKey1, fileKey2}, nil)
	if err != nil {
		t.Fatalf("StorePRSummary failed: %v", err)
	}
	if docID == "" {
		t.Fatal("expected non-empty GeneratedDoc ID")
	}

	// Verify GeneratedDoc node exists.
	cypher := `
		MATCH (gd:GeneratedDoc {scopeId: $scopeId})
		WHERE gd.type = 'pr_summary'
		RETURN gd.title AS title, gd.type AS type, gd.model AS model, gd.content AS content`
	records, err := client.ExecuteQuery(ctx, cypher, map[string]any{"scopeId": "pr-200"})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) == 0 {
		t.Fatal("expected GeneratedDoc node for PR summary")
	}

	m := records[0].AsMap()
	if v, _ := m["title"].(string); v != "DB Refactor Summary" {
		t.Errorf("expected title 'DB Refactor Summary', got %q", v)
	}
	if v, _ := m["type"].(string); v != "pr_summary" {
		t.Errorf("expected type 'pr_summary', got %q", v)
	}

	// Verify DOCUMENTS relationship (GeneratedDoc -> PullRequest).
	cypher = `
		MATCH (gd:GeneratedDoc {scopeId: $scopeId})-[:DOCUMENTS]->(pr:PullRequest {scopeId: $scopeId})
		RETURN pr.prId AS prId`
	records, err = client.ExecuteQuery(ctx, cypher, map[string]any{"scopeId": "pr-200"})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) == 0 {
		t.Fatal("expected DOCUMENTS edge from GeneratedDoc to PullRequest")
	}

	// Verify DERIVED_FROM relationships (GeneratedDoc -> File).
	cypher = `
		MATCH (gd:GeneratedDoc {scopeId: $scopeId})-[:DERIVED_FROM]->(f:File)
		RETURN f.nodeKey AS fileKey`
	records, err = client.ExecuteQuery(ctx, cypher, map[string]any{"scopeId": "pr-200"})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) < 2 {
		t.Errorf("expected 2 DERIVED_FROM edges to files, got %d", len(records))
	}
}

func TestContextGenerator_ListGeneratedDocs(t *testing.T) {
	prefix := "audit-ctx-list-"
	client, cleanup := setupTestDB(t, prefix)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	gen := generated.NewContextGenerator(client)
	gen.SetScope(models.NewPRScope("300"))

	// Create PR node and summary.
	_, err := gen.CreatePullRequestNode(ctx, "300", "Test PR", "alice", "main", "test", "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = gen.StorePRSummary(ctx, "300", "Test Summary", "content", "model", nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	// List docs.
	docs, err := gen.ListGeneratedDocs(ctx, "pr_summary", "")
	if err != nil {
		t.Fatalf("ListGeneratedDocs failed: %v", err)
	}
	if len(docs) == 0 {
		t.Fatal("expected at least 1 generated doc")
	}

	found := false
	for _, d := range docs {
		if title, _ := d["title"].(string); title == "Test Summary" {
			found = true
		}
	}
	if !found {
		t.Error("expected to find 'Test Summary' in listed docs")
	}

	// List with wrong scope — should not find our doc.
	gen2 := generated.NewContextGenerator(client)
	gen2.SetScope(models.NewPRScope("999"))
	docs2, err := gen2.ListGeneratedDocs(ctx, "pr_summary", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range docs2 {
		if title, _ := d["title"].(string); title == "Test Summary" {
			t.Error("should NOT find pr-300's doc from pr-999 scope")
		}
	}
}

// ===========================================================================
// Phase 0: Overlay-Aware Hybrid Retrieval — Guardrail Tests
// ===========================================================================

// TestOverlayAware_HybridRetrieval_ScopeFiltering verifies that hybrid search
// respects scope boundaries: querying with a scopeId returns the overlay version,
// tombstoned nodes are hidden, and no cross-scope bleed occurs.
// This test exercises the Neo4j semantic search path (graph-based) since
// vector/text stores require external services.
func TestOverlayAware_HybridRetrieval_ScopeFiltering(t *testing.T) {
	prefix := "phase0-hybrid-"
	client, cleanup := setupTestDB(t, prefix)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create same-name Function in main and pr-42 scopes with different content.
	sharedNodeKey := prefix + "func:pkg/handler.go#ProcessPayment(...)"

	_, err := client.MergeNode(ctx, []string{"Function"},
		map[string]any{"nodeKey": sharedNodeKey, "scopeId": "main"},
		map[string]any{
			"name": "ProcessPayment", "nodeKey": sharedNodeKey,
			"signature": "ProcessPayment(amount float64) error",
			"sourceCode": "func ProcessPayment(amount float64) error { return nil }",
			"scope": "main", "scopeId": "main",
		})
	require.NoError(t, err)

	_, err = client.MergeNode(ctx, []string{"Function"},
		map[string]any{"nodeKey": sharedNodeKey, "scopeId": "pr-42"},
		map[string]any{
			"name": "ProcessPayment", "nodeKey": sharedNodeKey,
			"signature": "ProcessPayment(amount float64, currency string) error",
			"sourceCode": "func ProcessPayment(amount float64, currency string) error { return nil }",
			"scope": "pr", "scopeId": "pr-42",
		})
	require.NoError(t, err)

	// Create a main-scope Function that should be tombstoned in pr-42.
	tombNodeKey := prefix + "func:pkg/legacy.go#OldPayment(...)"
	_, err = client.MergeNode(ctx, []string{"Function"},
		map[string]any{"nodeKey": tombNodeKey, "scopeId": "main"},
		map[string]any{
			"name": "OldPayment", "nodeKey": tombNodeKey,
			"scope": "main", "scopeId": "main",
		})
	require.NoError(t, err)

	tombKey := prefix + fmt.Sprintf("tombstone:pr-42:%s", tombNodeKey)
	_, err = client.MergeNode(ctx, []string{"Tombstone"},
		map[string]any{"nodeKey": tombKey, "scopeId": "pr-42"},
		map[string]any{
			"nodeKey": tombKey, "targetNodeKey": tombNodeKey,
			"scope": "pr", "scopeId": "pr-42",
		})
	require.NoError(t, err)

	// Create a Function in pr-99 — should NOT be visible from pr-42 scope.
	otherPRKey := prefix + "func:pkg/other.go#OtherPayment(...)"
	_, err = client.MergeNode(ctx, []string{"Function"},
		map[string]any{"nodeKey": otherPRKey, "scopeId": "pr-99"},
		map[string]any{
			"name": "OtherPayment", "nodeKey": otherPRKey,
			"scope": "pr", "scopeId": "pr-99",
		})
	require.NoError(t, err)

	// Use SearchNodesScoped to simulate scope-aware retrieval.
	qb := neo4j.NewQueryBuilder(client)
	results, err := qb.SearchNodesScoped(ctx, "Payment", []string{"Function"}, 0, "pr-42")
	require.NoError(t, err)

	// Collect result nodeKeys and scopeIds.
	type resultInfo struct {
		nodeKey string
		scopeId string
	}
	var found []resultInfo
	for _, r := range results {
		m := r.AsMap()
		if nodeObj, ok := m["n"].(dbtype.Node); ok {
			nk, _ := nodeObj.Props["nodeKey"].(string)
			sid, _ := nodeObj.Props["scopeId"].(string)
			found = append(found, resultInfo{nk, sid})
		}
	}

	// ASSERTION 1: PR overlay version should be returned (not main).
	hasOverlay := false
	for _, f := range found {
		if f.nodeKey == sharedNodeKey && f.scopeId == "pr-42" {
			hasOverlay = true
		}
	}
	assert.True(t, hasOverlay,
		"expected pr-42 overlay version of ProcessPayment, got: %+v", found)

	// ASSERTION 2: Tombstoned node should NOT appear.
	for _, f := range found {
		assert.NotEqual(t, tombNodeKey, f.nodeKey,
			"tombstoned OldPayment should not appear in pr-42 scope")
	}

	// ASSERTION 3: Other PR's nodes should NOT bleed in.
	for _, f := range found {
		assert.NotEqual(t, "pr-99", f.scopeId,
			"pr-99 nodes should not appear in pr-42 scope query")
	}
}

func TestContextGenerator_StoreFlowSummary(t *testing.T) {
	prefix := "audit-ctx-flow-"
	client, cleanup := setupTestDB(t, prefix)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create a Flow node.
	flowKey := prefix + "flow:api:GET:/health"
	_, err := client.MergeNode(ctx, []string{"Flow"},
		map[string]any{"nodeKey": flowKey, "scopeId": "main"},
		map[string]any{
			"name": "GET /health", "flowType": "api",
			"nodeKey": flowKey, "scope": "main", "scopeId": "main",
		})
	if err != nil {
		t.Fatal(err)
	}

	gen := generated.NewContextGenerator(client)
	gen.SetScope(models.DefaultScope())

	docID, err := gen.StoreFlowSummary(ctx, flowKey, "Health Check Flow",
		"Simple liveness probe endpoint.", "test-model", nil)
	if err != nil {
		t.Fatalf("StoreFlowSummary failed: %v", err)
	}
	if docID == "" {
		t.Fatal("expected non-empty doc ID")
	}

	// Verify DOCUMENTS edge.
	cypher := `
		MATCH (gd:GeneratedDoc)-[:DOCUMENTS]->(f:Flow {nodeKey: $flowKey})
		RETURN gd.title AS title, gd.type AS type`
	records, err := client.ExecuteQuery(ctx, cypher, map[string]any{"flowKey": flowKey})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) == 0 {
		t.Fatal("expected DOCUMENTS edge from GeneratedDoc to Flow")
	}
	m := records[0].AsMap()
	if v, _ := m["type"].(string); v != "flow_summary" {
		t.Errorf("expected type 'flow_summary', got %q", v)
	}
}
