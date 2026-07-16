package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	neo4j "github.com/context-maximiser/code-graph/internal/graph"
	"github.com/context-maximiser/code-graph/internal/ingest/docs"
	models "github.com/context-maximiser/code-graph/internal/model"
)

const docsIngestService = "itest-docs-ingest"

// cleanupDocsIngestData removes everything the docs ingest test wrote. All
// Document/DocumentChunk nodes carry serviceName; the Service anchor is keyed
// by nodeKey.
func cleanupDocsIngestData(t *testing.T, client *neo4j.Client) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err := client.ExecuteQuery(ctx, `
		MATCH (n)
		WHERE (n:Document OR n:DocumentChunk) AND n.serviceName = $svc
		DETACH DELETE n
	`, map[string]any{"svc": docsIngestService})
	require.NoError(t, err)

	_, err = client.ExecuteQuery(ctx, `
		MATCH (s:Service {nodeKey: $key}) DETACH DELETE s
	`, map[string]any{"key": models.ServiceNodeKey(docsIngestService)})
	require.NoError(t, err)
}

func writeDoc(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

// queryDocState returns (contentHash, chunkCount) for a document.
func queryDocState(t *testing.T, client *neo4j.Client, docKey string) (string, int) {
	t.Helper()
	recs, err := client.ExecuteQuery(context.Background(), `
		MATCH (d:Document {nodeKey: $key, scopeId: 'main'})
		RETURN d.contentHash AS hash, d.chunkCount AS count
	`, map[string]any{"key": docKey})
	require.NoError(t, err)
	require.Len(t, recs, 1, "document %s should exist exactly once", docKey)
	m := recs[0].AsMap()
	hash, _ := m["hash"].(string)
	count, _ := m["count"].(int64)
	return hash, int(count)
}

// queryChunks returns chunkIndex → (textHash, elementId) for a document.
func queryChunks(t *testing.T, client *neo4j.Client, docKey string) map[int][2]string {
	t.Helper()
	recs, err := client.ExecuteQuery(context.Background(), `
		MATCH (c:DocumentChunk {documentKey: $key, scopeId: 'main'})
		RETURN c.chunkIndex AS idx, c.textHash AS hash, elementId(c) AS id
		ORDER BY c.chunkIndex
	`, map[string]any{"key": docKey})
	require.NoError(t, err)
	out := make(map[int][2]string, len(recs))
	for _, rec := range recs {
		m := rec.AsMap()
		idx, _ := m["idx"].(int64)
		hash, _ := m["hash"].(string)
		id, _ := m["id"].(string)
		out[int(idx)] = [2]string{hash, id}
	}
	return out
}

// TestDocsIngestSyncMatrix drives the RFC-011 §4.3 hash-diff behavior
// end-to-end: initial ingest, no-op re-ingest, section edit (only the edited
// chunk rewritten, its MENTIONS cleared, others untouched), doc deletion, and
// chunk-count shrink.
func TestDocsIngestSyncMatrix(t *testing.T) {
	client := createTestClient(t)
	defer client.Close(context.Background())

	cleanupDocsIngestData(t, client)
	defer cleanupDocsIngestData(t, client)

	root := t.TempDir()
	guide := "# Guide\n\nIntro paragraph for the guide.\n\n## Setup\n\nSetup body text.\n\n## Usage\n\nUsage body text."
	writeDoc(t, root, "docs/guide.md", guide)
	writeDoc(t, root, "README.md", "# Readme\n\nReadme body.")

	ing := docs.NewIngestor(client, docsIngestService, models.DefaultScope())
	src := &docs.RepoMarkdownSource{Root: root}
	ctx := context.Background()

	// --- Initial ingest -------------------------------------------------
	report, err := ing.Run(ctx, src)
	require.NoError(t, err)
	require.Equal(t, 2, report.DocsNew)
	require.Zero(t, report.DocsChanged)
	require.Zero(t, report.DocsUnchanged)
	require.NotEmpty(t, report.Changed, "initial ingest must emit chunks for mining")

	guideKey := models.DocumentNodeKey(docsIngestService + "/docs/guide.md")
	readmeKey := models.DocumentNodeKey(docsIngestService + "/README.md")

	_, guideCount := queryDocState(t, client, guideKey)
	require.Equal(t, 3, guideCount, "guide has three H1/H2 sections")
	chunksBefore := queryChunks(t, client, guideKey)
	require.Len(t, chunksBefore, 3)

	// Service -CONTAINS-> Document and Document -HAS_CHUNK-> chunk edges exist.
	recs, err := client.ExecuteQuery(ctx, `
		MATCH (s:Service {nodeKey: $svcKey})-[:CONTAINS]->(d:Document {nodeKey: $docKey})-[:HAS_CHUNK]->(c:DocumentChunk)
		RETURN count(c) AS n
	`, map[string]any{"svcKey": models.ServiceNodeKey(docsIngestService), "docKey": guideKey})
	require.NoError(t, err)
	require.EqualValues(t, 3, recs[0].AsMap()["n"])

	// Title extraction: first H1.
	recs, err = client.ExecuteQuery(ctx,
		`MATCH (d:Document {nodeKey: $key}) RETURN d.title AS title`,
		map[string]any{"key": readmeKey})
	require.NoError(t, err)
	require.Equal(t, "Readme", recs[0].AsMap()["title"])

	// --- Idempotence: unchanged re-run writes nothing --------------------
	report, err = ing.Run(ctx, src)
	require.NoError(t, err)
	require.Zero(t, report.DocsNew)
	require.Zero(t, report.DocsChanged)
	require.Equal(t, 2, report.DocsUnchanged)
	require.Zero(t, report.ChunksWritten)
	require.Empty(t, report.Changed)

	// --- Section edit: only the edited chunk is rewritten ----------------
	// Seed a MENTIONS edge from the Usage chunk (index 2) to prove the clear,
	// and one from the untouched Setup chunk (index 1) to prove preservation.
	seedMention := func(chunkID string) {
		_, err := client.ExecuteQuery(ctx, `
			MATCH (c:DocumentChunk) WHERE elementId(c) = $id
			MERGE (f:File {nodeKey: 'file-docs-ingest-target', scopeId: 'main'})
			SET f.serviceName = $svc, f.scopedKey = 'file-docs-ingest-target|main'
			MERGE (c)-[m:MENTIONS]->(f)
			SET m.strategy = 'docmine/test-seed', m.confidence = 0.9
		`, map[string]any{"id": chunkID, "svc": docsIngestService})
		require.NoError(t, err)
	}
	seedMention(chunksBefore[1][1])
	seedMention(chunksBefore[2][1])

	edited := "# Guide\n\nIntro paragraph for the guide.\n\n## Setup\n\nSetup body text.\n\n## Usage\n\nUsage body text, now edited."
	writeDoc(t, root, "docs/guide.md", edited)

	report, err = ing.Run(ctx, src)
	require.NoError(t, err)
	require.Equal(t, 1, report.DocsChanged)
	require.Equal(t, 1, report.DocsUnchanged)
	require.Equal(t, 1, report.ChunksWritten, "only the edited Usage chunk is rewritten")
	require.Equal(t, 2, report.ChunksUnchanged)
	require.Len(t, report.Changed, 1)
	require.Contains(t, report.Changed[0].Content, "now edited")
	require.Equal(t, "docs/guide.md", report.Changed[0].FilePath)

	chunksAfter := queryChunks(t, client, guideKey)
	require.Len(t, chunksAfter, 3)
	require.Equal(t, chunksBefore[0][0], chunksAfter[0][0], "intro chunk hash unchanged")
	require.Equal(t, chunksBefore[1][0], chunksAfter[1][0], "setup chunk hash unchanged")
	require.NotEqual(t, chunksBefore[2][0], chunksAfter[2][0], "usage chunk hash changed")

	countMentions := func(chunkIdx int) int {
		recs, err := client.ExecuteQuery(ctx, `
			MATCH (c:DocumentChunk {documentKey: $key, chunkIndex: $idx, scopeId: 'main'})-[m:MENTIONS]->()
			RETURN count(m) AS n
		`, map[string]any{"key": guideKey, "idx": chunkIdx})
		require.NoError(t, err)
		n, _ := recs[0].AsMap()["n"].(int64)
		return int(n)
	}
	require.Equal(t, 1, countMentions(1), "untouched chunk keeps its MENTIONS")
	require.Equal(t, 0, countMentions(2), "rewritten chunk's MENTIONS are cleared")

	// --- Shrink: dropping a section removes the trailing chunk -----------
	shrunk := "# Guide\n\nIntro paragraph for the guide.\n\n## Setup\n\nSetup body text."
	writeDoc(t, root, "docs/guide.md", shrunk)
	report, err = ing.Run(ctx, src)
	require.NoError(t, err)
	require.Equal(t, 1, report.ChunksRemoved)
	_, guideCount = queryDocState(t, client, guideKey)
	require.Equal(t, 2, guideCount)
	require.Len(t, queryChunks(t, client, guideKey), 2)

	// --- Doc deletion: document and chunks disappear ----------------------
	require.NoError(t, os.Remove(filepath.Join(root, "README.md")))
	report, err = ing.Run(ctx, src)
	require.NoError(t, err)
	require.Equal(t, 1, report.DocsRemoved)

	recs, err = client.ExecuteQuery(ctx, `
		MATCH (n) WHERE (n:Document OR n:DocumentChunk) AND n.nodeKey STARTS WITH $prefix
		RETURN count(n) AS n
	`, map[string]any{"prefix": "doc:" + docsIngestService + "/README.md"})
	require.NoError(t, err)
	require.EqualValues(t, 0, recs[0].AsMap()["n"])

	// Cleanup the seeded File target.
	_, err = client.ExecuteQuery(ctx,
		`MATCH (f:File {nodeKey: 'file-docs-ingest-target'}) DETACH DELETE f`, nil)
	require.NoError(t, err)
}
