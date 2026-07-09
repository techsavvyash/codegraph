package main

import (
	"context"
	"fmt"

	"github.com/context-maximiser/code-graph/internal/search"
	"github.com/spf13/cobra"
)

// searchCmd handles search operations
var searchCmd = &cobra.Command{
	Use:   "search",
	Short: "Search for code symbols",
	Long:  "Search for functions, classes, variables, and other code symbols using fulltext indexes",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		searchTerm := args[0]
		limit, _ := cmd.Flags().GetInt("limit")
		scopeID, _ := cmd.Flags().GetString("scope-id")
		service, _ := cmd.Flags().GetString("service")
		cursor, _ := cmd.Flags().GetString("cursor")

		client, err := createNeo4jClient()
		if err != nil {
			return fmt.Errorf("failed to create Neo4j client: %w", err)
		}
		defer client.Close(context.Background())

		searcher := search.NewSearcher(client)

		ctx := context.Background()
		response, err := searcher.Search(ctx, searchTerm, search.Options{
			ScopeID: scopeID,
			Service: service,
			Limit:   limit,
			Cursor:  cursor,
		})
		if err != nil {
			return fmt.Errorf("search failed: %w", err)
		}

		// Display results
		fmt.Printf("🔍 Search Results for '%s':\n", searchTerm)
		fmt.Printf("Found %d results\n\n", len(response.Results))

		if len(response.Results) == 0 {
			fmt.Println("No results found.")
			return nil
		}

		// Print each result
		for i, result := range response.Results {
			fmt.Printf("%d. %s (%s)\n", i+1, result.Name, result.Label)
			if result.Signature != "" {
				fmt.Printf("   Signature: %s\n", result.Signature)
			}
			if result.FilePath != "" {
				fmt.Printf("   File: %s\n", result.FilePath)
			}
			if result.Service != "" {
				fmt.Printf("   Service: %s\n", result.Service)
			}
			fmt.Printf("   Score: %.4f\n", result.Score)
			fmt.Println()
		}

		// Print pagination info if available
		if response.NextCursor != "" {
			fmt.Printf("More results available. Use --cursor '%s' to continue.\n", response.NextCursor)
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(searchCmd)
	searchCmd.Flags().IntP("limit", "l", 20, "Maximum number of results")
	searchCmd.Flags().String("scope-id", "", "Scope ID for overlay-aware search (e.g., 'main', 'pr-42')")
	searchCmd.Flags().String("service", "", "Filter by service name")
	searchCmd.Flags().String("cursor", "", "Keyset pagination cursor for next page")
}
