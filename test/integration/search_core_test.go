package integration

import (
	"context"
	"fmt"
	"testing"
	"time"

	graph "github.com/context-maximiser/code-graph/internal/graph"
	"github.com/context-maximiser/code-graph/internal/graph/schema"
	"github.com/context-maximiser/code-graph/internal/search"
)

// TestSearchCore performs end-to-end search tests with real Neo4j.
func TestSearchCore(t *testing.T) {
	ctx := context.Background()

	// Connect to Neo4j (expects bolt://localhost:7687, neo4j/password123)
	client, err := graph.NewClient(graph.Config{
		URI:      "bolt://localhost:7687",
		Username: "neo4j",
		Password: "password123",
		Database: "neo4j",
	})
	if err != nil {
		t.Fatalf("Failed to create Neo4j client: %v", err)
	}
	defer client.Close(ctx)

	scopeID := "itest-search-core"

	// Cleanup after test
	t.Cleanup(func() {
		// Delete all nodes created under this scope
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		freshClient, err := graph.NewClient(graph.Config{
			URI:      "bolt://localhost:7687",
			Username: "neo4j",
			Password: "password123",
			Database: "neo4j",
		})
		if err != nil {
			t.Logf("cleanup: failed to create fresh client: %v", err)
			return
		}
		defer freshClient.Close(cleanupCtx)

		deleteQuery := fmt.Sprintf(`
MATCH (n {scopeId: "%s"})
WITH n LIMIT 1000
DETACH DELETE n
`, scopeID)
		if _, err := freshClient.ExecuteQuery(cleanupCtx, deleteQuery, nil); err != nil {
			t.Logf("cleanup: failed to delete nodes with scopeId %s: %v", scopeID, err)
		}
	})

	// Create test nodes under the itest scope
	testNodes := []map[string]interface{}{
		{
			"labels": []string{"Function"},
			"props": map[string]interface{}{
				"nodeKey":     "SearchCoreAlphaHandler",
				"name":        "SearchCoreAlphaHandler",
				"scopeId":     scopeID,
				"serviceName": "search-test-service",
			},
		},
		{
			"labels": []string{"Function"},
			"props": map[string]interface{}{
				"nodeKey":     "SearchCoreExact",
				"name":        "SearchCoreExact",
				"scopeId":     scopeID,
				"serviceName": "search-test-service",
			},
		},
		{
			"labels": []string{"Function"},
			"props": map[string]interface{}{
				"nodeKey":     "SearchCoreBeta",
				"name":        "SearchCoreBeta",
				"scopeId":     scopeID,
				"serviceName": "search-test-service",
			},
		},
		{
			"labels": []string{"Class"},
			"props": map[string]interface{}{
				"nodeKey":     "SearchCoreAlphaClass",
				"name":        "SearchCoreAlphaClass",
				"scopeId":     scopeID,
				"serviceName": "search-test-service",
			},
		},
		{
			"labels": []string{"File"},
			"props": map[string]interface{}{
				"nodeKey":     "search_core_helper.go",
				"path":        "/src/search_core_helper.go",
				"scopeId":     scopeID,
				"serviceName": "search-test-service",
			},
		},
		{
			"labels": []string{"File"},
			"props": map[string]interface{}{
				"nodeKey":     "search_core_alpha.go",
				"path":        "/src/search_core_alpha.go",
				"scopeId":     scopeID,
				"serviceName": "search-test-service",
			},
		},
	}

	// Create nodes via MergeNode (merge on nodeKey+scopeId, set all props)
	for _, nodeData := range testNodes {
		labels := nodeData["labels"].([]string)
		props := nodeData["props"].(map[string]interface{})
		mergeProps := map[string]any{"nodeKey": props["nodeKey"], "scopeId": props["scopeId"]}
		if _, err := client.MergeNode(ctx, labels, mergeProps, props); err != nil {
			t.Fatalf("failed to create test node %v: %v", props["nodeKey"], err)
		}
	}

	// Create schema (FULLTEXT indexes, etc.)
	schemaManager := schema.NewSchemaManager(client)
	if err := schemaManager.CreateSchema(ctx); err != nil {
		t.Fatalf("failed to create schema: %v", err)
	}

	// Give Neo4j a moment to index
	// (in real scenarios, indexes are online immediately, but we add a small wait for safety)

	// Create searcher
	searcher := search.NewSearcher(client)

	t.Run("SearchFindsAlphaNode", func(t *testing.T) {
		response, err := searcher.Search(ctx, "SearchCoreAlpha", search.Options{
			ScopeID: scopeID,
			Limit:   10,
		})
		if err != nil {
			t.Fatalf("search failed: %v", err)
		}

		if len(response.Results) == 0 {
			t.Fatal("search returned no results; expected to find SearchCoreAlpha nodes")
		}

		// Verify at least one result contains "SearchCoreAlpha" in name
		found := false
		for _, r := range response.Results {
			if r.Name == "SearchCoreAlphaHandler" || r.Name == "SearchCoreAlphaClass" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected to find SearchCoreAlpha* in results, got: %v", response.Results)
		}
	})

	t.Run("ExactMatchRanksFirst", func(t *testing.T) {
		response, err := searcher.Search(ctx, "SearchCoreExact", search.Options{
			ScopeID: scopeID,
			Limit:   10,
		})
		if err != nil {
			t.Fatalf("search failed: %v", err)
		}

		if len(response.Results) == 0 {
			t.Fatal("search returned no results")
		}

		// First result should be the exact match
		if response.Results[0].Name != "SearchCoreExact" {
			t.Errorf("exact match should rank first; got %q instead", response.Results[0].Name)
		}
	})

	t.Run("KeysetPaginationWalksAllResults", func(t *testing.T) {
		// Fetch with Limit=2, walk through all pages
		const pageSize = 2

		allResults := make(map[string]bool)
		cursor := ""
		pageNum := 0

		for {
			pageNum++
			response, err := searcher.Search(ctx, "SearchCore", search.Options{
				ScopeID: scopeID,
				Limit:   pageSize,
				Cursor:  cursor,
			})
			if err != nil {
				t.Fatalf("search on page %d failed: %v", pageNum, err)
			}

			if len(response.Results) == 0 {
				if pageNum == 1 {
					t.Fatal("first page returned no results")
				}
				break // No more results
			}

			for _, r := range response.Results {
				if allResults[r.NodeID] {
					t.Errorf("duplicate result on page %d: %q", pageNum, r.NodeID)
				}
				allResults[r.NodeID] = true
			}

			if response.NextCursor == "" {
				break // Last page
			}
			cursor = response.NextCursor

			if pageNum > 100 {
				t.Fatal("infinite pagination loop detected")
			}
		}

		// Exactly the four name-indexed SearchCore* nodes match: Lucene's
		// standard analyzer keeps "search_core_helper" as a single token
		// (underscore joins words), so the two File paths are correctly NOT
		// prefix-matched by "SearchCore*". With pageSize=2 that's two full
		// pages — pagination must actually have paged.
		if len(allResults) != 4 {
			t.Errorf("pagination collected %d unique results, expected exactly 4", len(allResults))
		}
		if pageNum < 2 {
			t.Errorf("expected at least 2 pages with pageSize=2 over 4 results, got %d", pageNum)
		}

		// Verify collected results are deterministic by doing another full walk
		allResultsSecond := make(map[string]bool)
		cursor = ""
		for {
			response, err := searcher.Search(ctx, "SearchCore", search.Options{
				ScopeID: scopeID,
				Limit:   pageSize,
				Cursor:  cursor,
			})
			if err != nil {
				t.Fatalf("second pagination walk failed: %v", err)
			}

			if len(response.Results) == 0 {
				break
			}

			for _, r := range response.Results {
				allResultsSecond[r.NodeID] = true
			}

			if response.NextCursor == "" {
				break
			}
			cursor = response.NextCursor
		}

		// Sets should be equal
		if len(allResults) != len(allResultsSecond) {
			t.Errorf("pagination non-deterministic: first walk %d results, second walk %d",
				len(allResults), len(allResultsSecond))
		}
		for nodeID := range allResults {
			if !allResultsSecond[nodeID] {
				t.Errorf("nodeID %q missing from second pagination walk", nodeID)
			}
		}
	})

	t.Run("LuceneMetacharacterQuerySucceeds", func(t *testing.T) {
		// Query with Lucene special chars: should not error, just escape and search
		queryWithSpecials := "search+handler-core*(test)&&[async]"
		response, err := searcher.Search(ctx, queryWithSpecials, search.Options{
			ScopeID: scopeID,
			Limit:   10,
		})
		if err != nil {
			t.Fatalf("search with Lucene metacharacters failed: %v", err)
		}

		// Should return results (or empty if nothing matches the escaped query), but no error
		_ = response // Just verify it didn't error
		t.Logf("search with metacharacters returned %d results", len(response.Results))
	})

	t.Run("ServiceFilterWorks", func(t *testing.T) {
		// Search with service filter
		response, err := searcher.Search(ctx, "SearchCore", search.Options{
			ScopeID: scopeID,
			Service: "search-test-service",
			Limit:   10,
		})
		if err != nil {
			t.Fatalf("scoped search failed: %v", err)
		}

		// All results should have the matching service
		for _, r := range response.Results {
			if r.Service != "search-test-service" {
				t.Errorf("result %q has service %q, expected search-test-service", r.NodeID, r.Service)
			}
		}
	})
}

// TODO: import time if running the cleanup
