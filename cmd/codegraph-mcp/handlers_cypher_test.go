//go:build integration

package main

import (
	"context"
	"os"
	"testing"

	"github.com/context-maximiser/code-graph/internal/graph"
	"github.com/stretchr/testify/require"
)

// setupMCPServer creates a test CodeGraphMCPServer with a real Neo4j client.
// It returns the server and a cleanup function that closes the client.
func setupMCPServer(t *testing.T) (*CodeGraphMCPServer, func()) {
	client, err := neo4j.NewClient(neo4j.Config{
		URI:      getEnvOrDefault("NEO4J_URI", "bolt://localhost:7687"),
		Username: getEnvOrDefault("NEO4J_USERNAME", "neo4j"),
		Password: getEnvOrDefault("NEO4J_PASSWORD", "password123"),
		Database: getEnvOrDefault("NEO4J_DATABASE", "neo4j"),
	})
	require.NoError(t, err, "failed to create Neo4j client")

	workspaceRoot, err := os.Getwd()
	if err != nil {
		workspaceRoot = "."
	}

	server := &CodeGraphMCPServer{
		client:        client,
		queryBuilder:  neo4j.NewQueryBuilder(client),
		workspaceRoot: workspaceRoot,
	}

	cleanup := func() {
		client.Close(context.Background())
	}

	return server, cleanup
}

// TestExplainGuardRejectsInvalidQuery verifies that EXPLAIN validation catches
// syntax/semantic errors without executing the query.
func TestExplainGuardRejectsInvalidQuery(t *testing.T) {
	server, cleanup := setupMCPServer(t)
	defer cleanup()

	ctx := context.Background()

	// Try to execute an invalid Cypher query
	// Invalid syntax: INVALID_PATTERN is not valid Cypher
	invalidQuery := `
		INVALID_PATTERN foo bar RETURN foo
	`

	response := server.handleCypherTool(ctx, map[string]interface{}{
		"query": invalidQuery,
	})

	// Should be an error response due to validation failure
	require.True(t, response.IsError, "expected error response for invalid query")
	require.NotEmpty(t, response.Content)
	// The error can be either a syntax error or validation error
	require.Contains(t, response.Content[0].Text, "cypher:", "error should be a cypher error")
}

// TestExplainGuardWarnOnAllNodesScan verifies that an unlabeled MATCH — which
// the planner can only satisfy with AllNodesScan — triggers the warning.
func TestExplainGuardWarnOnAllNodesScan(t *testing.T) {
	server, cleanup := setupMCPServer(t)
	defer cleanup()

	ctx := context.Background()

	// An unlabeled node pattern returning the node itself has no index or
	// count-store shortcut: the plan always contains AllNodesScan.
	unlabeledQuery := `
		MATCH (n)
		RETURN n LIMIT 1
	`

	response := server.handleCypherTool(ctx, map[string]interface{}{
		"query": unlabeledQuery,
	})

	require.False(t, response.IsError, "query should not error")
	require.NotEmpty(t, response.Content)
	responseText := response.Content[0].Text

	require.Contains(t, responseText, "AllNodesScan",
		"unlabeled MATCH must include the AllNodesScan warning")
	require.Contains(t, responseText, "add a label qualifier",
		"warning must carry the remediation hint")
}

// TestExplainGuardNoWarnOnLabeledQuery verifies that properly labeled queries
// don't trigger the AllNodesScan warning.
func TestExplainGuardNoWarnOnLabeledQuery(t *testing.T) {
	server, cleanup := setupMCPServer(t)
	defer cleanup()

	ctx := context.Background()

	// Labeled query — should not trigger AllNodesScan
	labeledQuery := `
		MATCH (f:Function)
		WHERE f.name = 'processPayment'
		RETURN f.name AS name LIMIT 1
	`

	response := server.handleCypherTool(ctx, map[string]interface{}{
		"query": labeledQuery,
	})

	require.False(t, response.IsError, "labeled query should not error")
	require.NotEmpty(t, response.Content)
	responseText := response.Content[0].Text

	// Response should not include the AllNodesScan warning
	require.NotContains(t, responseText, "AllNodesScan", "labeled query should not warn about AllNodesScan")
}
