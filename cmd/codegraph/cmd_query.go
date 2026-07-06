package main

import (
	"context"
	"fmt"
	"strings"

	neo4j "github.com/context-maximiser/code-graph/internal/graph"
	models "github.com/context-maximiser/code-graph/internal/model"
	"github.com/context-maximiser/code-graph/internal/query"
	inference "github.com/context-maximiser/code-graph/internal/query/inference"
	"github.com/context-maximiser/code-graph/internal/search"
	"github.com/spf13/cobra"
)

// queryCmd handles querying the graph
var queryCmd = &cobra.Command{
	Use:   "query",
	Short: "Query the code graph",
	Long:  "Execute queries against the code graph database",
}

var querySearchCmd = &cobra.Command{
	Use:   "search [term]",
	Short: "Search for code symbols",
	Long:  "Search for functions, classes, variables, and other code symbols",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		searchTerm := args[0]
		limit, _ := cmd.Flags().GetInt("limit")

		client, err := createNeo4jClient()
		if err != nil {
			return fmt.Errorf("failed to create Neo4j client: %w", err)
		}
		defer client.Close(context.Background())

		hybridSearch := search.NewHybridSearchManager(client)

		// Optionally attach OpenSearch for BM25; fall back to Neo4j fulltext.
		searchMode := "graph+fulltext (neo4j)"
		if osStore, ok := createOpenSearchStore(); ok {
			defer osStore.Close()
			hybridSearch.WithTextStore(osStore)
			searchMode = "graph+fulltext (opensearch)"
		}

		// Apply scope if provided.
		if scopeID, _ := cmd.Flags().GetString("scope-id"); scopeID != "" {
			hybridSearch.SetScope(scopeID)
		}

		fmt.Printf("🔍 Search mode: %s\n", searchMode)

		ctx := context.Background()
		response, err := hybridSearch.UnifiedSearch(ctx, searchTerm, limit)
		if err != nil {
			return fmt.Errorf("search failed: %w", err)
		}

		// Display results using RRF-fused rendering.
		fmt.Printf("\nSearch Results (%d total):\n", response.TotalResults)
		fmt.Printf("Search Types: %v\n", response.SearchTypes)
		fmt.Printf("Full-Text Results: %d | Semantic Results: %d\n",
			response.Metadata.FullTextResults,
			response.Metadata.SemanticResults)

		fmt.Println("\nResults:")
		fmt.Println("---------")

		for i, result := range response.Results {
			fmt.Printf("\n%d. ", i+1)

			name := ""
			for _, field := range []string{"name", "title", "displayName", "signature", "symbol", "path"} {
				if v, ok := result.Node[field].(string); ok && v != "" {
					name = v
					break
				}
			}
			if name == "" {
				if v, ok := result.Node["snippet"].(string); ok && v != "" {
					name = v
				} else if v, ok := result.Node["nodeKey"].(string); ok && v != "" {
					name = v
				} else {
					name = "Unknown"
				}
			}
			if len(name) > 80 {
				name = name[:77] + "..."
			}
			fmt.Printf("**%s**", name)

			labels := result.Labels
			if len(labels) == 0 {
				if nt, ok := result.Node["nodeType"].(string); ok && nt != "" {
					labels = []string{nt}
				}
			}
			if len(labels) > 0 {
				fmt.Printf(" (%s)", strings.Join(labels, ", "))
			}
			fmt.Printf("\n   RRF Score: %.5f | Source: %s | Relevance: %s\n",
				result.CombinedScore, result.Source, result.Relevance)

			var scores []string
			if result.FullTextScore > 0 {
				scores = append(scores, fmt.Sprintf("BM25: %.2f", result.FullTextScore))
			}
			if result.SemanticScore > 0 {
				scores = append(scores, fmt.Sprintf("Semantic: %.4f", result.SemanticScore))
			}
			if len(scores) > 0 {
				fmt.Printf("   Raw scores: %s\n", strings.Join(scores, " | "))
			}

			if fp, ok := result.Node["filePath"].(string); ok && fp != "" {
				loc := fp
				if sl, ok := result.Node["startLine"]; ok {
					loc = fmt.Sprintf("%s:%v", fp, sl)
				}
				fmt.Printf("   Location: %s\n", loc)
			}

			if description, ok := result.Node["description"].(string); ok && description != "" {
				if len(description) > 120 {
					description = description[:117] + "..."
				}
				fmt.Printf("   Description: %s\n", description)
			} else if content, ok := result.Node["content"].(string); ok && content != "" {
				if len(content) > 120 {
					content = content[:117] + "..."
				}
				fmt.Printf("   Content: %s\n", content)
			}
		}

		return nil
	},
}

var querySourceCmd = &cobra.Command{
	Use:   "source [function_name]",
	Short: "Get source code for a function",
	Long:  "Retrieve the exact source code for a function or method using stored location metadata",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		functionName := args[0]

		client, err := createNeo4jClient()
		if err != nil {
			return fmt.Errorf("failed to create Neo4j client: %w", err)
		}
		defer client.Close(context.Background())

		queryBuilder := neo4j.NewQueryBuilder(client)

		ctx := context.Background()
		sourceCode, err := queryBuilder.GetFunctionSourceCode(ctx, functionName)
		if err != nil {
			return fmt.Errorf("failed to get source code: %w", err)
		}

		fmt.Printf("Source code for function '%s':\n", functionName)
		fmt.Println("=" + strings.Repeat("=", len(functionName)+25))
		fmt.Println(sourceCode)
		fmt.Println("=" + strings.Repeat("=", len(functionName)+25))

		return nil
	},
}

// queryDepsCmd queries service-level dependencies
var queryDepsCmd = &cobra.Command{
	Use:   "deps",
	Short: "Query service dependencies",
	Long:  "Show inter-service dependencies (CALLS_SERVICE relationships)",
	RunE: func(cmd *cobra.Command, args []string) error {
		serviceName, _ := cmd.Flags().GetString("service")
		scopeID, _ := cmd.Flags().GetString("scope-id")

		if serviceName == "" {
			return fmt.Errorf("--service is required")
		}

		client, err := createNeo4jClient()
		if err != nil {
			return fmt.Errorf("failed to create Neo4j client: %w", err)
		}
		defer client.Close(context.Background())

		depsQuery := query.NewServiceDepsQuery(client)
		ctx := context.Background()

		deps, err := depsQuery.GetDependencies(ctx, serviceName, scopeID)
		if err != nil {
			return fmt.Errorf("failed to query dependencies: %w", err)
		}

		if len(deps) == 0 {
			fmt.Printf("No dependencies found for service '%s'\n", serviceName)
			return nil
		}

		fmt.Printf("Dependencies for service '%s':\n", serviceName)
		for _, dep := range deps {
			direction := "→"
			peer := dep.ToService
			if dep.ToService == serviceName {
				direction = "←"
				peer = dep.FromService
			}
			fmt.Printf("  %s %s %s (confidence: %.2f, source: %s)\n",
				serviceName, direction, peer, dep.Confidence, dep.Source)
			for _, ev := range dep.Evidence {
				fmt.Printf("    evidence: %s\n", ev)
			}
		}

		return nil
	},
}

// queryFlowsCmd lists or generates flow spines
var queryFlowsCmd = &cobra.Command{
	Use:   "flows",
	Short: "List or generate flow spines",
	Long:  "List existing flow spines or generate new ones from API endpoints",
	RunE: func(cmd *cobra.Command, args []string) error {
		generate, _ := cmd.Flags().GetBool("generate")
		maxDepth, _ := cmd.Flags().GetInt("max-depth")
		flowType, _ := cmd.Flags().GetString("type")
		scopeID, _ := cmd.Flags().GetString("scope-id")

		client, err := createNeo4jClient()
		if err != nil {
			return fmt.Errorf("failed to create Neo4j client: %w", err)
		}
		defer client.Close(context.Background())

		gen := query.NewFlowSpineGenerator(client)
		if scopeID != "" {
			gen.SetScope(models.NewPRScope(models.NormalizePRID(scopeID)))
		}
		if maxDepth > 0 {
			budget := inference.DefaultTraversalBudget
			budget.MaxDepth = maxDepth
			gen.SetBudget(budget)
		}
		ctx := context.Background()

		if generate {
			results, err := gen.GenerateFromAPIEndpoints(ctx, maxDepth)
			if err != nil {
				return fmt.Errorf("failed to generate flows: %w", err)
			}
			fmt.Printf("Generated %d flow spines\n", len(results))
			for _, r := range results {
				fmt.Printf("  %s (%s) — %d steps\n", r.FlowName, r.FlowType, len(r.Steps))
				for _, s := range r.Steps {
					fmt.Printf("    [%d] %s (%s)\n", s.Order, s.Name, s.Label)
				}
			}
			return nil
		}

		// List existing flows.
		flows, err := gen.ListFlows(ctx, flowType)
		if err != nil {
			return fmt.Errorf("failed to list flows: %w", err)
		}

		if len(flows) == 0 {
			fmt.Println("No flow spines found. Use --generate to create them from API endpoints.")
			return nil
		}

		fmt.Printf("Found %d flow spines:\n", len(flows))
		for _, f := range flows {
			fmt.Printf("  %s [%s] (%s)\n", f.FlowName, f.FlowType, f.FlowNodeKey)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(queryCmd)

	queryCmd.AddCommand(querySearchCmd)
	queryCmd.AddCommand(querySourceCmd)
	queryCmd.AddCommand(queryDepsCmd)
	queryCmd.AddCommand(queryFlowsCmd)

	// Query flags
	querySearchCmd.Flags().IntP("limit", "l", 10, "Limit search results")
	querySearchCmd.Flags().String("scope-id", "", "Optional scope ID for overlay-aware search (e.g., pr-42)")
	queryDepsCmd.Flags().String("service", "", "Service name to query dependencies for")
	queryDepsCmd.Flags().String("scope-id", "", "Optional scope ID for overlay-aware query")

	// Flow flags
	queryFlowsCmd.Flags().Bool("generate", false, "Generate flow spines from API endpoints")
	queryFlowsCmd.Flags().Int("max-depth", 2, "Maximum call graph traversal depth")
	queryFlowsCmd.Flags().String("type", "", "Filter by flow type (api, consumer, cron)")
	queryFlowsCmd.Flags().String("scope-id", "", "Optional scope ID for overlay-aware flows")
}
