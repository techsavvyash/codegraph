package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
)

// statusCmd checks the connection to Neo4j
var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Check Neo4j connection status",
	Long:  "Check if the Neo4j database is accessible and return connection information",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := createNeo4jClient()
		if err != nil {
			return fmt.Errorf("failed to create Neo4j client: %w", err)
		}
		defer client.Close(context.Background())

		ctx := context.Background()
		info, err := client.GetDatabaseInfo(ctx)
		if err != nil {
			return fmt.Errorf("failed to get database info: %w", err)
		}

		fmt.Println("Neo4j Connection Status: ✓ Connected")
		fmt.Printf("Database: %s\n", neo4jDB)
		fmt.Printf("URI: %s\n", neo4jURI)
		if name, ok := info["name"]; ok {
			fmt.Printf("Name: %s\n", name)
		}
		if versions, ok := info["versions"]; ok {
			fmt.Printf("Version: %s\n", versions)
		}
		if edition, ok := info["edition"]; ok {
			fmt.Printf("Edition: %s\n", edition)
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(statusCmd)
}
