package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/spf13/cobra"
)

// serviceCmd manages Service nodes in the graph.
var serviceCmd = &cobra.Command{
	Use:   "service",
	Short: "Manage Service nodes",
	Long:  "Inspect and backfill metadata on Service nodes in the graph",
}

// serviceSetRootCmd backfills Service.rootPath for services indexed before
// RFC-012 R2 (or indexed from a path that has since moved), so MCP source
// resolution (codegraph_source, query source) can find files on disk
// regardless of the server process's cwd.
var serviceSetRootCmd = &cobra.Command{
	Use:   "set-root <service-name> <abs-path>",
	Short: "Backfill the on-disk root path for a Service node",
	Long: `Sets rootPath on every Service node with the given name (across all scopes)
to the given directory. Used to backfill services indexed before Service.rootPath
was stamped automatically at index time (codegraph index scip), or to repoint a
service whose checkout moved.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		serviceName := args[0]
		rawPath := args[1]

		absPath, err := resolveRootPath(rawPath)
		if err != nil {
			return err
		}

		client, err := createNeo4jClient()
		if err != nil {
			return fmt.Errorf("failed to create Neo4j client: %w", err)
		}
		defer client.Close(context.Background())

		ctx := context.Background()

		existing, err := client.ExecuteQuery(ctx,
			`MATCH (s:Service {name: $name}) RETURN elementId(s) AS id, s.scopeId AS scopeId`,
			map[string]any{"name": serviceName})
		if err != nil {
			return fmt.Errorf("failed to look up service %q: %w", serviceName, err)
		}
		if len(existing) == 0 {
			names, listErr := client.ExecuteQuery(ctx, `MATCH (s:Service) RETURN DISTINCT s.name AS name`, nil)
			if listErr != nil {
				return fmt.Errorf("no Service node named %q found", serviceName)
			}
			available := make([]string, 0, len(names))
			for _, rec := range names {
				if n, ok := rec.AsMap()["name"].(string); ok && n != "" {
					available = append(available, n)
				}
			}
			sort.Strings(available)
			return fmt.Errorf("no Service node named %q found; available services: %v", serviceName, available)
		}

		if _, err := client.ExecuteQuery(ctx,
			`MATCH (s:Service {name: $name}) SET s.rootPath = $path`,
			map[string]any{"name": serviceName, "path": absPath}); err != nil {
			return fmt.Errorf("failed to set rootPath: %w", err)
		}

		fmt.Printf("✓ Set rootPath=%s on %d Service node(s) named %q\n", absPath, len(existing), serviceName)
		return nil
	},
}

// resolveRootPath validates that rawPath exists and is a directory, then
// returns its absolute, symlink-cleaned form. Extracted from
// serviceSetRootCmd.RunE so the validation logic is testable without a Neo4j
// connection.
func resolveRootPath(rawPath string) (string, error) {
	info, err := os.Stat(rawPath)
	if err != nil {
		return "", fmt.Errorf("path %q: %w", rawPath, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("path %q is not a directory", rawPath)
	}

	absPath, err := filepath.Abs(rawPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve absolute path for %q: %w", rawPath, err)
	}
	if resolved, err := filepath.EvalSymlinks(absPath); err == nil {
		absPath = resolved
	}
	return absPath, nil
}

func init() {
	rootCmd.AddCommand(serviceCmd)
	serviceCmd.AddCommand(serviceSetRootCmd)
}
