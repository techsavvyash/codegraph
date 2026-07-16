package integration

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	neo4j "github.com/context-maximiser/code-graph/internal/graph"
	"github.com/context-maximiser/code-graph/internal/graph/schema"
	"github.com/context-maximiser/code-graph/internal/llm"
	"github.com/context-maximiser/code-graph/internal/search"
)

const semSearchService = "itest-search-semantic"

func cleanupSemSearchData(t *testing.T, client *neo4j.Client) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := client.ExecuteQuery(ctx,
		`MATCH (n) WHERE n.serviceName = $svc DETACH DELETE n`,
		map[string]any{"svc": semSearchService})
	require.NoError(t, err)
	require.NoError(t, schema.NewSchemaManager(client).DropVectorIndexes(ctx))
}

// TestSearchSemanticFusion seeds a chunk whose text shares no keywords with
// the query, embeds both to the same vector, and verifies the semantic list
// surfaces it through RRF fusion — and that semantic without an embedder is a
// loud error, not a silent downgrade.
func TestSearchSemanticFusion(t *testing.T) {
	client := createTestClient(t)
	defer client.Close(context.Background())

	cleanupSemSearchData(t, client)
	defer cleanupSemSearchData(t, client)

	ctx := context.Background()
	require.NoError(t, schema.NewSchemaManager(client).CreateVectorIndexes(ctx, 8))
	_, err := client.ExecuteQuery(ctx, "CALL db.awaitIndexes(300)", nil)
	require.NoError(t, err)

	// The chunk's TEXT contains none of the query's words — fulltext can
	// never find it; only the vector space can.
	_, err = client.ExecuteQuery(ctx, `
		CREATE (c:DocumentChunk {nodeKey: 'chunk:semsearch#0', scopeId: 'main',
			documentKey: 'doc:semsearch', chunkIndex: 0,
			headingPath: 'Nightly draining procedure',
			content: 'The nightly job drains pending work items in batches.',
			serviceName: $svc, scopedKey: 'chunk:semsearch#0|main'})
		WITH c
		CALL db.create.setNodeVectorProperty(c, 'embedding', [1.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0])
		SET c.embeddingModel = 'fake-embedder'
	`, map[string]any{"svc": semSearchService})
	require.NoError(t, err)

	embedder := llm.NewFakeEmbedder(8)
	embedder.Fn = func(text string) []float32 {
		if text == "how does batch processing work" {
			return []float32{1, 0, 0, 0, 0, 0, 0, 0}
		}
		return nil
	}

	searcher := search.NewSearcher(client)

	// Without an embedder: explicit error.
	_, err = searcher.Search(ctx, "how does batch processing work",
		search.Options{Semantic: true, Service: semSearchService})
	require.Error(t, err, "semantic without embedder must fail loudly")
	require.Contains(t, err.Error(), "embedding provider")

	// With the embedder: the keyword-less chunk is found semantically.
	searcher.SetEmbedder(embedder)
	resp, err := searcher.Search(ctx, "how does batch processing work",
		search.Options{Semantic: true, Service: semSearchService})
	require.NoError(t, err)
	require.NotEmpty(t, resp.Results, "semantic list must surface the chunk")
	require.Equal(t, "DocumentChunk", resp.Results[0].Label)
	require.Equal(t, "chunk:semsearch#0", resp.Results[0].NodeKey)
	require.Equal(t, "Nightly draining procedure", resp.Results[0].Name)

	// Sanity: the same query WITHOUT semantic finds nothing (keyword-less).
	resp, err = searcher.Search(ctx, "how does batch processing work",
		search.Options{Service: semSearchService})
	require.NoError(t, err)
	require.Empty(t, resp.Results, "fulltext alone must not match — proves the semantic list did the work")
}
