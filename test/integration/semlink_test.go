package integration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	neo4j "github.com/context-maximiser/code-graph/internal/graph"
	"github.com/context-maximiser/code-graph/internal/graph/schema"
	"github.com/context-maximiser/code-graph/internal/ingest/docs"
	"github.com/context-maximiser/code-graph/internal/ingest/semlink"
	"github.com/context-maximiser/code-graph/internal/llm/llmtest"
	models "github.com/context-maximiser/code-graph/internal/model"
)

const semlinkService = "itest-semlink"

// semlinkVectors gives the test controlled geometry: alpha-flavored texts and
// beta-flavored texts sit on orthogonal axes, so alpha chunks match only
// alpha summaries. Everything else falls back to hash vectors (≈ orthogonal).
func semlinkVectors(text string) []float32 {
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "alpha"):
		return []float32{1, 0, 0, 0, 0, 0, 0, 0}
	case strings.Contains(lower, "beta"):
		return []float32{0, 1, 0, 0, 0, 0, 0, 0}
	default:
		return nil // hash fallback
	}
}

// semlinkCompleter fakes both prompt kinds: summaries carry the flavor word
// of the symbol they describe; judge verdicts accept only same-flavor pairs.
func semlinkCompleter() *llmtest.Completer {
	return &llmtest.Completer{Fn: func(system, user string) (string, error) {
		if strings.Contains(system, "judge") {
			sameFlavor := (strings.Contains(user, "alpha") && strings.Contains(user, "AlphaWorker77")) ||
				(strings.Contains(user, "beta") && strings.Contains(user, "BetaWorker77"))
			if sameFlavor {
				return `{"match": true, "confidence": 0.9}`, nil
			}
			return `{"match": false, "confidence": 0.1}`, nil
		}
		// Summary prompts.
		switch {
		case strings.Contains(user, "AlphaWorker77"):
			return "Processes alpha work items.", nil
		case strings.Contains(user, "BetaWorker77"):
			return "Processes beta work items.", nil
		default:
			return "Utility code.", nil
		}
	}}
}

func cleanupSemlinkData(t *testing.T, client *neo4j.Client) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err := client.ExecuteQuery(ctx, `
		MATCH (n) WHERE n.serviceName = $svc OR n.nodeKey = $svcKey
		DETACH DELETE n
	`, map[string]any{"svc": semlinkService, "svcKey": models.ServiceNodeKey(semlinkService)})
	require.NoError(t, err)

	// The 8-dim fake indexes must not linger on the shared dev graph — a
	// later real-model run would otherwise start with a dimension reset.
	require.NoError(t, schema.NewSchemaManager(client).DropVectorIndexes(ctx))
}

func seedSemlinkCode(t *testing.T, client *neo4j.Client) {
	t.Helper()
	ctx := context.Background()

	_, err := client.ExecuteQuery(ctx, `
		MERGE (s:Service {nodeKey: $key, scopeId: 'main'})
		SET s.name = $svc, s.scope = 'main', s.scopedKey = $key + '|main'
	`, map[string]any{"key": models.ServiceNodeKey(semlinkService), "svc": semlinkService})
	require.NoError(t, err)

	for _, fn := range []struct{ name, sig string }{
		{"AlphaWorker77", "func AlphaWorker77(items []Item) error"},
		{"BetaWorker77", "func BetaWorker77(items []Item) error"},
	} {
		nodeKey := fmt.Sprintf("func:%s:worker.go#%s", semlinkService, fn.name)
		_, err := client.MergeNode(ctx, []string{"Function"},
			map[string]any{"nodeKey": nodeKey, "scopeId": "main"},
			map[string]any{
				"nodeKey": nodeKey, "name": fn.name, "signature": fn.sig,
				"serviceName": semlinkService, "isExported": true, "isTestFunction": false,
				"filePath": "worker.go", "scope": "main", "scopeId": "main",
			})
		require.NoError(t, err)
	}
}

// TestSemlinkEndToEnd drives the full Layer S pipeline on the fake provider:
// summaries (hash-cached), embeddings, vector indexes, kNN + judge, edge
// provenance, chunk markers, budget resumability, and idempotence.
func TestSemlinkEndToEnd(t *testing.T) {
	client := createTestClient(t)
	defer client.Close(context.Background())

	cleanupSemlinkData(t, client)
	defer cleanupSemlinkData(t, client)
	seedSemlinkCode(t, client)

	// Two doc sections: one alpha-flavored, one plain (no semantic partner).
	doc := "# Workers\n\n## Alpha pipeline\n\nThe alpha pipeline drains alpha work items nightly.\n\n## Colophon\n\nThis page uses standard formatting conventions."
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "workers.md"), []byte(doc), 0o644))

	ctx := context.Background()
	ing := docs.NewIngestor(client, semlinkService, models.DefaultScope())
	ingReport, err := ing.Run(ctx, &docs.RepoMarkdownSource{Root: root})
	require.NoError(t, err)
	require.Equal(t, 3, len(ingReport.Changed), "title + two sections")

	embedder := llmtest.NewEmbedder(8)
	embedder.Fn = semlinkVectors
	completer := semlinkCompleter()

	runner, err := semlink.NewRunner(client, semlinkService, models.DefaultScope(),
		completer, embedder, "", semlink.Options{})
	require.NoError(t, err)

	report, err := runner.Run(ctx)
	require.NoError(t, err)

	// Summaries: 2 functions + 0 files (no File nodes seeded) + 0 service
	// (service has no CONTAINS->File with summaries).
	require.Equal(t, 2, report.SummariesWritten)
	// Embeddings: 2 function summaries + 3 chunks.
	require.Equal(t, 5, report.EmbeddingsWritten)
	require.Equal(t, 3, report.ChunksMatched)
	require.Zero(t, report.SkippedBudget)

	// Exactly one semantic edge: alpha chunk → AlphaWorker77. The judge saw
	// (alpha chunk, BetaWorker77)? No — beta summary is orthogonal to the
	// alpha chunk, killed by threshold before the judge.
	alphaFn := fmt.Sprintf("func:%s:worker.go#AlphaWorker77", semlinkService)
	recs, err := client.ExecuteQuery(ctx, `
		MATCH (c:DocumentChunk {serviceName: $svc})-[m:MENTIONS]->(t)
		RETURN c.headingPath AS heading, t.nodeKey AS target, m.strategy AS strategy,
		       m.confidence AS confidence, m.reasons AS reasons, m.evidenceRefs AS evidence
	`, map[string]any{"svc": semlinkService})
	require.NoError(t, err)
	require.Len(t, recs, 1, "exactly one semantic edge expected")

	edge := recs[0].AsMap()
	require.Equal(t, alphaFn, edge["target"])
	require.Equal(t, "semlink/fake-embedder", edge["strategy"])
	require.InDelta(t, 0.30+0.30*0.9, edge["confidence"].(float64), 1e-9)
	require.Contains(t, edge["heading"], "Alpha pipeline")
	require.NotEmpty(t, edge["reasons"])
	require.NotEmpty(t, edge["evidence"])
	require.Equal(t, 1, report.EdgesWritten)
	require.GreaterOrEqual(t, report.JudgeAccepted, 1)

	// --- Idempotence: second run costs nothing and changes nothing. --------
	callsBefore := completer.Calls()
	report2, err := runner.Run(ctx)
	require.NoError(t, err)
	require.Zero(t, report2.SummariesWritten, "hash cache must skip unchanged summaries")
	require.Equal(t, 2, report2.SummariesUpToDate)
	require.Zero(t, report2.EmbeddingsWritten)
	require.Zero(t, report2.EdgesWritten)
	require.Zero(t, report2.ChunksMatched, "semlinkModel marker must skip matched chunks")
	require.Equal(t, callsBefore, completer.Calls(), "idempotent re-run must not call the LLM")

	// --- Threshold change re-opens matching (real-vendor calibration found
	// stamps keyed on model only, forcing manual clears). Same model, lower
	// threshold: every chunk re-matches; the existing edge just re-merges.
	runner3, err := semlink.NewRunner(client, semlinkService, models.DefaultScope(),
		completer, embedder, "", semlink.Options{SimilarityThreshold: 0.40})
	require.NoError(t, err)
	report3, err := runner3.Run(ctx)
	require.NoError(t, err)
	require.Equal(t, 3, report3.ChunksMatched, "threshold change must re-open all chunks")
	recs, err = client.ExecuteQuery(ctx, `
		MATCH (c:DocumentChunk {serviceName: $svc})-[m:MENTIONS]->(t {nodeKey: $target})
		RETURN count(m) AS n
	`, map[string]any{"svc": semlinkService, "target": alphaFn})
	require.NoError(t, err)
	n, _ := recs[0].AsMap()["n"].(int64)
	require.EqualValues(t, 1, n, "re-match must MERGE, not duplicate, the alpha edge")
}

// TestSemlinkBudgetResumes verifies budget exhaustion stops cleanly and a
// re-run resumes from the hash caches without redoing paid work.
func TestSemlinkBudgetResumes(t *testing.T) {
	client := createTestClient(t)
	defer client.Close(context.Background())

	cleanupSemlinkData(t, client)
	defer cleanupSemlinkData(t, client)
	seedSemlinkCode(t, client)

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "w.md"),
		[]byte("# W\n\nAlpha work is drained by the alpha pipeline."), 0o644))

	ctx := context.Background()
	ing := docs.NewIngestor(client, semlinkService, models.DefaultScope())
	_, err := ing.Run(ctx, &docs.RepoMarkdownSource{Root: root})
	require.NoError(t, err)

	embedder := llmtest.NewEmbedder(8)
	embedder.Fn = semlinkVectors
	completer := semlinkCompleter()

	// Budget of 1: only the first function summary fits.
	runner, err := semlink.NewRunner(client, semlinkService, models.DefaultScope(),
		completer, embedder, "", semlink.Options{MaxLLMCalls: 1})
	require.NoError(t, err)
	report, err := runner.Run(ctx)
	require.NoError(t, err, "budget exhaustion must not be an error")
	require.Equal(t, 1, report.SummariesWritten)
	require.GreaterOrEqual(t, report.SkippedBudget, 1)
	// With concurrency, either symbol may win the single budget slot; a
	// summary-clipped run must therefore never stamp chunks as matched, or a
	// chunk matched against the incomplete corpus would be skipped forever.
	require.Zero(t, report.ChunksMatched, "summary-clipped run must not stamp chunks")

	// Re-run with headroom: the paid summary is cached, the rest completes.
	runner2, err := semlink.NewRunner(client, semlinkService, models.DefaultScope(),
		completer, embedder, "", semlink.Options{})
	require.NoError(t, err)
	report2, err := runner2.Run(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, report2.SummariesWritten, "only the skipped summary is generated")
	require.Equal(t, 1, report2.SummariesUpToDate, "the budget-run summary is cache-hit")
	require.Zero(t, report2.SkippedBudget)
	require.Equal(t, 1, report2.EdgesWritten, "alpha chunk links after resume")
}
