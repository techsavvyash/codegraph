package main

import (
	"context"
	"fmt"

	"github.com/context-maximiser/code-graph/internal/search"
	textindex "github.com/context-maximiser/code-graph/internal/search/textindex"
	"github.com/spf13/cobra"
)

// searchCmd manages advanced search capabilities
var searchCmd = &cobra.Command{
	Use:   "search",
	Short: "Advanced search management",
	Long:  "Manage full-text search (BM25) and hybrid search capabilities",
}

var searchInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize search indexes",
	Long:  "Create full-text indexes required for advanced search",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := createNeo4jClient()
		if err != nil {
			return fmt.Errorf("failed to create Neo4j client: %w", err)
		}
		defer client.Close(context.Background())

		ctx := context.Background()

		// Always create full-text indexes in Neo4j.
		fullTextSearch := search.NewFullTextSearchManager(client)
		fmt.Println("🚀 Initializing full-text search indexes...")
		if err := fullTextSearch.CreateFullTextIndexes(ctx); err != nil {
			fmt.Printf("Warning: failed to create full-text indexes: %v\n", err)
		}

		// OpenSearch: create index and bulk-sync existing nodes.
		if osStore, ok := createOpenSearchStore(); ok {
			defer osStore.Close()
			fmt.Println("🚀 Initializing OpenSearch index...")
			if err := osStore.EnsureIndex(ctx); err != nil {
				fmt.Printf("Warning: failed to ensure OpenSearch index: %v\n", err)
			} else {
				totalSynced := 0
				batchSize := 500

				// Bulk-sync Function/Method nodes.
				fnQuery := `MATCH (n) WHERE n:Function OR n:Method
RETURN n.nodeKey AS nodeKey, coalesce(n.name,'') + ' ' + coalesce(n.signature,'') + ' ' + coalesce(n.docstring,'') AS content, labels(n)[0] AS nodeType`
				if rows, err := client.ExecuteQuery(ctx, fnQuery, nil); err == nil {
					var batch []textindex.IndexDoc
					for _, r := range rows {
						m := r.AsMap()
						nk, _ := m["nodeKey"].(string)
						ct, _ := m["content"].(string)
						nt, _ := m["nodeType"].(string)
						if nk != "" {
							batch = append(batch, textindex.IndexDoc{NodeKey: nk, Content: ct, Metadata: map[string]string{"nodeType": nt}})
							if len(batch) >= batchSize {
								if e := osStore.IndexDocuments(ctx, batch); e == nil {
									totalSynced += len(batch)
								}
								batch = batch[:0]
							}
						}
					}
					if len(batch) > 0 {
						if e := osStore.IndexDocuments(ctx, batch); e == nil {
							totalSynced += len(batch)
						}
					}
				}

				// Bulk-sync Symbol nodes.
				symQuery := `MATCH (n:Symbol) RETURN n.nodeKey AS nodeKey, coalesce(n.displayName,'') + ' ' + coalesce(n.documentation,'') AS content`
				if rows, err := client.ExecuteQuery(ctx, symQuery, nil); err == nil {
					var batch []textindex.IndexDoc
					for _, r := range rows {
						m := r.AsMap()
						nk, _ := m["nodeKey"].(string)
						ct, _ := m["content"].(string)
						if nk != "" {
							batch = append(batch, textindex.IndexDoc{NodeKey: nk, Content: ct, Metadata: map[string]string{"nodeType": "Symbol"}})
							if len(batch) >= batchSize {
								if e := osStore.IndexDocuments(ctx, batch); e == nil {
									totalSynced += len(batch)
								}
								batch = batch[:0]
							}
						}
					}
					if len(batch) > 0 {
						if e := osStore.IndexDocuments(ctx, batch); e == nil {
							totalSynced += len(batch)
						}
					}
				}

				// Bulk-sync DocumentChunk nodes.
				chunkQuery := `MATCH (n:DocumentChunk) RETURN n.nodeKey AS nodeKey, coalesce(n.content,'') AS content, coalesce(n.documentKey,'') AS documentKey`
				if rows, err := client.ExecuteQuery(ctx, chunkQuery, nil); err == nil {
					var batch []textindex.IndexDoc
					for _, r := range rows {
						m := r.AsMap()
						nk, _ := m["nodeKey"].(string)
						ct, _ := m["content"].(string)
						dk, _ := m["documentKey"].(string)
						if nk != "" {
							batch = append(batch, textindex.IndexDoc{NodeKey: nk, Content: ct, Metadata: map[string]string{"nodeType": "DocumentChunk", "documentKey": dk}})
							if len(batch) >= batchSize {
								if e := osStore.IndexDocuments(ctx, batch); e == nil {
									totalSynced += len(batch)
								}
								batch = batch[:0]
							}
						}
					}
					if len(batch) > 0 {
						if e := osStore.IndexDocuments(ctx, batch); e == nil {
							totalSynced += len(batch)
						}
					}
				}

				// Bulk-sync Document nodes.
				docQuery := `MATCH (n:Document) RETURN n.nodeKey AS nodeKey, coalesce(n.title,'') AS content`
				if rows, err := client.ExecuteQuery(ctx, docQuery, nil); err == nil {
					var batch []textindex.IndexDoc
					for _, r := range rows {
						m := r.AsMap()
						nk, _ := m["nodeKey"].(string)
						ct, _ := m["content"].(string)
						if nk != "" {
							batch = append(batch, textindex.IndexDoc{NodeKey: nk, Content: ct, Metadata: map[string]string{"nodeType": "Document"}})
							if len(batch) >= batchSize {
								if e := osStore.IndexDocuments(ctx, batch); e == nil {
									totalSynced += len(batch)
								}
								batch = batch[:0]
							}
						}
					}
					if len(batch) > 0 {
						if e := osStore.IndexDocuments(ctx, batch); e == nil {
							totalSynced += len(batch)
						}
					}
				}

				// Bulk-sync Feature nodes.
				featQuery := `MATCH (n:Feature) RETURN n.nodeKey AS nodeKey, coalesce(n.name,'') + ' ' + coalesce(n.description,'') AS content`
				if rows, err := client.ExecuteQuery(ctx, featQuery, nil); err == nil {
					var batch []textindex.IndexDoc
					for _, r := range rows {
						m := r.AsMap()
						nk, _ := m["nodeKey"].(string)
						ct, _ := m["content"].(string)
						if nk != "" {
							batch = append(batch, textindex.IndexDoc{NodeKey: nk, Content: ct, Metadata: map[string]string{"nodeType": "Feature"}})
							if len(batch) >= batchSize {
								if e := osStore.IndexDocuments(ctx, batch); e == nil {
									totalSynced += len(batch)
								}
								batch = batch[:0]
							}
						}
					}
					if len(batch) > 0 {
						if e := osStore.IndexDocuments(ctx, batch); e == nil {
							totalSynced += len(batch)
						}
					}
				}

				fmt.Printf("✓ OpenSearch index '%s' ready (%d docs synced)\n", opensearchIndex, totalSynced)
			}
		}

		fmt.Println("✅ Advanced search indexes initialized successfully")
		return nil
	},
}

var searchInfoCmd = &cobra.Command{
	Use:   "info",
	Short: "Show search capabilities and index status",
	Long:  "Display information about available search methods and index status",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := createNeo4jClient()
		if err != nil {
			return fmt.Errorf("failed to create Neo4j client: %w", err)
		}
		defer client.Close(context.Background())

		hybridSearch := search.NewHybridSearchManager(client)
		if osStore, ok := createOpenSearchStore(); ok {
			defer osStore.Close()
			hybridSearch.WithTextStore(osStore)
			fmt.Println("📋 BM25 backend: OpenSearch")
		} else {
			fmt.Println("📋 BM25 backend: Neo4j fulltext (OpenSearch not reachable)")
		}

		fmt.Println("🔍 CodeGraph Search Capabilities")
		fmt.Println("=================================")

		ctx := context.Background()
		capabilities, err := hybridSearch.GetSearchCapabilities(ctx)
		if err != nil {
			return fmt.Errorf("failed to get search capabilities: %w", err)
		}

		// Display full-text search info
		if fullTextInfo, ok := capabilities["fullTextSearch"].(map[string]interface{}); ok {
			fmt.Println("\n📝 Full-Text Search (BM25):")
			if indexes, ok := fullTextInfo["fullTextIndexes"].([]map[string]interface{}); ok {
				fmt.Printf("   Indexes: %d\n", len(indexes))
				for _, index := range indexes {
					if name, ok := index["name"].(string); ok {
						fmt.Printf("   - %s", name)
						if state, ok := index["state"].(string); ok {
							fmt.Printf(" (%s)", state)
						}
						fmt.Println()
					}
				}
			}
		}

		// Display hybrid search info
		if hybridInfo, ok := capabilities["hybridSearch"].(map[string]interface{}); ok {
			fmt.Println("\n🔬 Hybrid Search:")
			if methods, ok := hybridInfo["supportedMethods"].([]string); ok {
				fmt.Printf("   Methods: %v\n", methods)
			}
			if weights, ok := hybridInfo["defaultWeights"]; ok {
				fmt.Printf("   Default Weights: %+v\n", weights)
			}
			if smartSearch, ok := hybridInfo["smartSearch"].(bool); ok {
				fmt.Printf("   Smart Search: %t\n", smartSearch)
			}
		}

		fmt.Println("\n✨ Available Commands:")
		fmt.Println("   codegraph search init          # Initialize search indexes")
		fmt.Println("   codegraph search info          # Show this information")
		fmt.Println("   codegraph query search 'query' # Run a search")

		return nil
	},
}

func init() {
	rootCmd.AddCommand(searchCmd)

	searchCmd.AddCommand(searchInitCmd)
	searchCmd.AddCommand(searchInfoCmd)
}
