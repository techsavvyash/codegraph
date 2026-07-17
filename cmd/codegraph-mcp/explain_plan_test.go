//go:build integration

package main

import (
	"context"
	"testing"

	neo4j "github.com/context-maximiser/code-graph/internal/graph"
	"github.com/stretchr/testify/require"
)

// TestExecuteQueryWithSummaryExposesPlan is a regression test for the EXPLAIN
// guard's plumbing: EXPLAIN produces zero records, so the plan MUST be read
// from the ResultSummary — an implementation that looks for the plan in the
// records silently disables the AllNodesScan warning.
func TestExecuteQueryWithSummaryExposesPlan(t *testing.T) {
	client, err := neo4j.NewClient(neo4j.Config{
		URI:      getEnvOrDefault("NEO4J_URI", "bolt://localhost:7687"),
		Username: getEnvOrDefault("NEO4J_USERNAME", "neo4j"),
		Password: getEnvOrDefault("NEO4J_PASSWORD", "password123"),
		Database: getEnvOrDefault("NEO4J_DATABASE", "neo4j"),
	})
	require.NoError(t, err)
	defer client.Close(context.Background())

	records, summary, err := client.ExecuteQueryWithSummary(context.Background(),
		"EXPLAIN MATCH (n) RETURN n LIMIT 1", nil)
	require.NoError(t, err)
	require.Empty(t, records, "EXPLAIN must produce no records")
	require.NotNil(t, summary, "summary must be present")
	require.NotNil(t, summary.Plan(), "EXPLAIN summary must carry a plan")
	require.True(t, planHasOperator(summary.Plan(), "AllNodesScan"),
		"unlabeled MATCH plan must contain AllNodesScan")

	// And an index-backed query must not trip the detector. (A bare labeled
	// MATCH can still plan as AllNodesScan+Filter on an empty database where
	// the label token has no statistics, so use the name index the schema
	// guarantees.)
	_, labeledSummary, err := client.ExecuteQueryWithSummary(context.Background(),
		"EXPLAIN MATCH (n:Function) WHERE n.name = 'x' RETURN n LIMIT 1", nil)
	require.NoError(t, err)
	require.NotNil(t, labeledSummary.Plan())
	require.False(t, planHasOperator(labeledSummary.Plan(), "AllNodesScan"),
		"index-backed labeled MATCH plan must not contain AllNodesScan")
}
