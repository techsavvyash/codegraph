package main

import (
	"context"
	"fmt"
	"time"

	neo4j "github.com/context-maximiser/code-graph/internal/graph"
	textindex "github.com/context-maximiser/code-graph/internal/search/textindex"
	"github.com/spf13/viper"
)

// createNeo4jClient creates a new Neo4j client using configuration
func createNeo4jClient() (*neo4j.Client, error) {
	config := neo4j.Config{
		URI:      viper.GetString("neo4j.uri"),
		Username: viper.GetString("neo4j.username"),
		Password: viper.GetString("neo4j.password"),
		Database: viper.GetString("neo4j.database"),
	}

	return neo4j.NewClient(config)
}

// createOpenSearchStore attempts to connect to OpenSearch and returns the store if reachable.
// Returns nil, false if the endpoint is unreachable (callers fall back to Neo4j fulltext).
func createOpenSearchStore() (*textindex.OpenSearchStore, bool) {
	url := opensearchURL
	if url == "" {
		url = "http://localhost:9200"
	}
	index := opensearchIndex
	if index == "" {
		index = "codegraph"
	}
	store := textindex.NewOpenSearchStore(textindex.OpenSearchConfig{
		BaseURL:   url,
		IndexName: index,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := store.Ping(ctx); err != nil {
		fmt.Printf("Warning: OpenSearch not reachable at %s (%v) — falling back to Neo4j fulltext\n", url, err)
		return nil, false
	}
	return store, true
}
