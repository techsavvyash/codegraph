package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/context-maximiser/code-graph/internal/graph/schema"
	"github.com/spf13/cobra"
)

// schemaCmd manages Neo4j schema (constraints and indexes)
var schemaCmd = &cobra.Command{
	Use:   "schema",
	Short: "Manage Neo4j schema",
	Long:  "Create, drop, or inspect the Neo4j schema (constraints and indexes)",
}

var schemaCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create Neo4j schema",
	Long:  "Create all required constraints and indexes in the Neo4j database",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := createNeo4jClient()
		if err != nil {
			return fmt.Errorf("failed to create Neo4j client: %w", err)
		}
		defer client.Close(context.Background())

		schemaManager := schema.NewSchemaManager(client)

		fmt.Println("Creating Neo4j schema...")
		ctx := context.Background()
		if err := schemaManager.CreateSchema(ctx); err != nil {
			return fmt.Errorf("failed to create schema: %w", err)
		}

		fmt.Println("✓ Schema created successfully")
		return nil
	},
}

var schemaDropCmd = &cobra.Command{
	Use:   "drop",
	Short: "Drop Neo4j schema",
	Long:  "Drop all constraints and indexes from the Neo4j database",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := createNeo4jClient()
		if err != nil {
			return fmt.Errorf("failed to create Neo4j client: %w", err)
		}
		defer client.Close(context.Background())

		schemaManager := schema.NewSchemaManager(client)

		fmt.Println("Dropping Neo4j schema...")
		ctx := context.Background()
		if err := schemaManager.DropSchema(ctx); err != nil {
			return fmt.Errorf("failed to drop schema: %w", err)
		}

		fmt.Println("✓ Schema dropped successfully")
		return nil
	},
}

var schemaInfoCmd = &cobra.Command{
	Use:   "info",
	Short: "Show schema information",
	Long:  "Display information about current constraints and indexes",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := createNeo4jClient()
		if err != nil {
			return fmt.Errorf("failed to create Neo4j client: %w", err)
		}
		defer client.Close(context.Background())

		schemaManager := schema.NewSchemaManager(client)

		ctx := context.Background()
		info, err := schemaManager.GetSchemaInfo(ctx)
		if err != nil {
			return fmt.Errorf("failed to get schema info: %w", err)
		}

		fmt.Println("Schema Information:")
		fmt.Println("==================")

		if constraints, ok := info["constraints"].([]map[string]any); ok {
			fmt.Printf("\nConstraints (%d):\n", len(constraints))
			for _, constraint := range constraints {
				if name, ok := constraint["name"]; ok {
					fmt.Printf("  - %s\n", name)
				}
			}
		}

		if indexes, ok := info["indexes"].([]map[string]any); ok {
			fmt.Printf("\nIndexes (%d):\n", len(indexes))
			for _, index := range indexes {
				if name, ok := index["name"]; ok {
					fmt.Printf("  - %s\n", name)
				}
			}
		}

		return nil
	},
}

var schemaMigrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Migrate schema to scopedKey identity constraints",
	Long:  "Backfill scopedKey for existing nodes and create per-label UNIQUE constraints. Fails if duplicates are detected.",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := createNeo4jClient()
		if err != nil {
			return fmt.Errorf("failed to create Neo4j client: %w", err)
		}
		defer client.Close(context.Background())

		schemaManager := schema.NewSchemaManager(client)

		fmt.Println("Migrating schema to scopedKey constraints...")
		ctx := context.Background()
		results, err := schemaManager.Migrate(ctx)

		// Print summary table
		fmt.Println("\nMigration Summary:")
		fmt.Println("==================")
		fmt.Printf("%-20s %-15s %-15s %-15s\n", "Label", "Backfilled", "Duplicates", "Constraint")
		fmt.Println(strings.Repeat("-", 65))
		for _, r := range results {
			status := "✓"
			if r.Error != "" {
				status = "✗"
			} else if !r.ConstraintOK {
				status = "⚠"
			}
			fmt.Printf("%-20s %-15d %-15d %s %s\n", r.Label, r.BackfilledCount, r.DuplicatesFound, status, r.Error)

			// Print duplicate keys if found
			for _, key := range r.DuplicateKeys {
				fmt.Printf("  • %s\n", key)
			}
			if r.DuplicatesFound > len(r.DuplicateKeys) {
				fmt.Printf("  + %d more\n", r.DuplicatesFound-len(r.DuplicateKeys))
			}
		}

		if err != nil {
			fmt.Printf("\n✗ Migration failed: %v\n", err)
			return err
		}

		fmt.Println("\n✓ Schema migration completed successfully")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(schemaCmd)

	schemaCmd.AddCommand(schemaCreateCmd)
	schemaCmd.AddCommand(schemaDropCmd)
	schemaCmd.AddCommand(schemaInfoCmd)
	schemaCmd.AddCommand(schemaMigrateCmd)
}
