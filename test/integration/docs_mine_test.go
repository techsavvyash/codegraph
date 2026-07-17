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
	"github.com/context-maximiser/code-graph/internal/ingest/docs/mine"
	models "github.com/context-maximiser/code-graph/internal/model"
)

const (
	mineService      = "itest-docs-mine"
	mineOtherService = "itest-docs-mine-other"
)

func cleanupMineData(t *testing.T, client *neo4j.Client) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err := client.ExecuteQuery(ctx, `
		MATCH (n)
		WHERE n.serviceName IN [$a, $b]
		   OR n.nodeKey IN [$svcA, $svcB]
		   OR n.nodeKey STARTS WITH 'class:itest-mine '
		DETACH DELETE n
	`, map[string]any{
		"a": mineService, "b": mineOtherService,
		"svcA": models.ServiceNodeKey(mineService),
		"svcB": models.ServiceNodeKey(mineOtherService),
	})
	require.NoError(t, err)
}

// seedMineCodeGraph creates the code side: two services, files, functions
// (one name unique per service, one name colliding within the service, one
// name existing only in the other service), a method with a receiver
// signature, and a Class with a package-affine FQN nodeKey.
func seedMineCodeGraph(t *testing.T, client *neo4j.Client) {
	t.Helper()
	ctx := context.Background()

	_, err := client.ExecuteQuery(ctx, `
		MERGE (s:Service {nodeKey: $svcKey, scopeId: 'main'})
		SET s.name = $svc, s.packageName = 'example.com/minetest', s.scope = 'main',
		    s.scopedKey = $svcKey + '|main'
		MERGE (s2:Service {nodeKey: $otherKey, scopeId: 'main'})
		SET s2.name = $other, s2.packageName = 'example.com/other', s2.scope = 'main',
		    s2.scopedKey = $otherKey + '|main'
	`, map[string]any{
		"svcKey": models.ServiceNodeKey(mineService), "svc": mineService,
		"otherKey": models.ServiceNodeKey(mineOtherService), "other": mineOtherService,
	})
	require.NoError(t, err)

	type node struct {
		label string
		props map[string]any
	}
	nodes := []node{
		// Files of the doc's service.
		{"File", map[string]any{"nodeKey": "file:" + mineService + ":internal/engine/engine.go",
			"path": "internal/engine/engine.go", "serviceName": mineService}},
		{"File", map[string]any{"nodeKey": "file:" + mineService + ":internal/util/helper.go",
			"path": "internal/util/helper.go", "serviceName": mineService}},
		// Two files sharing a basename → ambiguous for a short candidate.
		{"File", map[string]any{"nodeKey": "file:" + mineService + ":a/dup/config.go",
			"path": "a/dup/config.go", "serviceName": mineService}},
		{"File", map[string]any{"nodeKey": "file:" + mineService + ":b/dup/config.go",
			"path": "b/dup/config.go", "serviceName": mineService}},
		// A file only in the OTHER service (cross-service D1).
		{"File", map[string]any{"nodeKey": "file:" + mineOtherService + ":pkg/mineremote/clientm77.go",
			"path": "pkg/mineremote/clientm77.go", "serviceName": mineOtherService}},

		// Functions. StartEngine: unique in-service. ParseThing: exists twice
		// in-service (ambiguous). RemoteOnlyM77: only in the other service.
		{"Function", map[string]any{"nodeKey": "func:" + mineService + ":internal/engine/engine.go#StartEngine()",
			"name": "StartEngine", "signature": "func StartEngine() error", "serviceName": mineService}},
		{"Function", map[string]any{"nodeKey": "func:" + mineService + ":internal/engine/engine.go#ParseThing()",
			"name": "ParseThing", "signature": "func ParseThing() error", "serviceName": mineService}},
		{"Function", map[string]any{"nodeKey": "func:" + mineService + ":internal/util/helper.go#ParseThing()",
			"name": "ParseThing", "signature": "func ParseThing() int", "serviceName": mineService}},
		{"Function", map[string]any{"nodeKey": "func:" + mineOtherService + ":pkg/mineremote/clientm77.go#RemoteOnlyM77()",
			"name": "RemoteOnlyM77", "signature": "func RemoteOnlyM77()", "serviceName": mineOtherService}},

		// Method with receiver signature for qualifier corroboration.
		{"Method", map[string]any{"nodeKey": "method:" + mineService + ":internal/engine/engine.go#(*Engine).Reload()",
			"name": "Reload", "signature": "func (e *Engine) Reload() error", "serviceName": mineService}},

		// Class whose FQN nodeKey contains the service packageName (affinity).
		{"Class", map[string]any{"nodeKey": "class:itest-mine example.com/minetest/engine/Engine#",
			"name": "Engine", "fqn": "itest-mine example.com/minetest/engine/Engine#"}},
	}

	for _, n := range nodes {
		props := n.props
		props["scope"] = "main"
		props["scopeId"] = "main"
		_, err := client.MergeNode(ctx, []string{n.label},
			map[string]any{"nodeKey": props["nodeKey"], "scopeId": "main"}, props)
		require.NoError(t, err)
	}
}

// mentionEdge is one MENTIONS row for golden comparison.
type mentionEdge struct {
	TargetKey  string
	Strategy   string
	Confidence float64
}

func queryMentions(t *testing.T, client *neo4j.Client) map[string][]mentionEdge {
	t.Helper()
	recs, err := client.ExecuteQuery(context.Background(), `
		MATCH (c:DocumentChunk {serviceName: $svc, scopeId: 'main'})-[m:MENTIONS]->(target)
		RETURN c.nodeKey AS chunk, target.nodeKey AS target,
		       m.strategy AS strategy, m.confidence AS confidence,
		       m.reasons AS reasons, m.evidenceRefs AS evidence,
		       m.scopeId AS scopeId, m.createdAt AS createdAt
		ORDER BY chunk, target
	`, map[string]any{"svc": mineService})
	require.NoError(t, err)

	out := map[string][]mentionEdge{}
	for _, rec := range recs {
		m := rec.AsMap()
		chunk, _ := m["chunk"].(string)
		target, _ := m["target"].(string)
		strategy, _ := m["strategy"].(string)
		conf, _ := m["confidence"].(float64)

		// I4: every edge must carry full provenance.
		require.NotEmpty(t, m["reasons"], "edge %s->%s missing reasons", chunk, target)
		require.NotEmpty(t, m["evidence"], "edge %s->%s missing evidenceRefs", chunk, target)
		require.Equal(t, "main", m["scopeId"], "edge %s->%s missing scopeId", chunk, target)
		require.NotEmpty(t, m["createdAt"], "edge %s->%s missing createdAt", chunk, target)

		out[chunk] = append(out[chunk], mentionEdge{TargetKey: target, Strategy: strategy, Confidence: conf})
	}
	return out
}

// TestDocsMineGolden ingests a fixture document against a seeded code graph
// and pins the exact validated edge set: what links, at which strategy and
// confidence — and what must NOT link (ambiguous, stoplisted, cross-service
// fence tokens).
func TestDocsMineGolden(t *testing.T) {
	client := createTestClient(t)
	defer client.Close(context.Background())

	cleanupMineData(t, client)
	defer cleanupMineData(t, client)
	seedMineCodeGraph(t, client)

	doc := `# Engine Guide

The engine lives in ` + "`internal/engine/engine.go`" + ` and boots via ` + "`StartEngine`" + `.
Cross-service reads use ` + "`RemoteOnlyM77`" + ` from the remote client.
Reload it with ` + "`Engine.Reload`" + ` when the ` + "`Engine`" + ` class changes.
The ` + "`ParseThing`" + ` helper is ambiguous and the word ` + "`get`" + ` is stoplisted.
Shared basenames like ` + "`dup/config.go`" + ` must not guess.
Remote code lives at pkg/mineremote/clientm77.go too.

## Example

` + "```go" + `
func main() {
    StartEngine()
    RemoteOnlyM77()
}
` + "```" + `
`

	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "docs"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "docs", "engine.md"), []byte(doc), 0o644))

	ctx := context.Background()
	ing := docs.NewIngestor(client, mineService, models.DefaultScope())
	report, err := ing.Run(ctx, &docs.RepoMarkdownSource{Root: root})
	require.NoError(t, err)
	require.Equal(t, 1, report.DocsNew)
	require.Len(t, report.Changed, 2, "H1 + H2 sections")

	miner := mine.NewMiner(client, mineService, models.DefaultScope())
	mineReport, err := miner.MineChunks(ctx, report.Changed)
	require.NoError(t, err)

	docKey := models.DocumentNodeKey(mineService + "/docs/engine.md")
	chunk0 := models.DocumentChunkNodeKey(docKey, 0)
	chunk1 := models.DocumentChunkNodeKey(docKey, 1)

	edges := queryMentions(t, client)

	// --- Chunk 0 (prose section) ---
	want0 := map[string]mentionEdge{
		"file:" + mineService + ":internal/engine/engine.go": {
			Strategy: "docmine/filepath", Confidence: 0.95},
		"file:" + mineOtherService + ":pkg/mineremote/clientm77.go": {
			Strategy: "docmine/filepath", Confidence: 0.95},
		"func:" + mineService + ":internal/engine/engine.go#StartEngine()": {
			Strategy: "docmine/codespan", Confidence: 0.90},
		"func:" + mineOtherService + ":pkg/mineremote/clientm77.go#RemoteOnlyM77()": {
			Strategy: "docmine/codespan", Confidence: 0.85},
		"method:" + mineService + ":internal/engine/engine.go#(*Engine).Reload()": {
			Strategy: "docmine/codespan", Confidence: 0.90},
		"class:itest-mine example.com/minetest/engine/Engine#": {
			Strategy: "docmine/codespan", Confidence: 0.90},
	}
	got0 := map[string]mentionEdge{}
	for _, e := range edges[chunk0] {
		got0[e.TargetKey] = e
	}
	for key, want := range want0 {
		got, ok := got0[key]
		require.True(t, ok, "chunk0 missing edge to %s (got %v)", key, edges[chunk0])
		require.Equal(t, want.Strategy, got.Strategy, "strategy for %s", key)
		require.InDelta(t, want.Confidence, got.Confidence, 1e-9, "confidence for %s", key)
	}
	// Negative space: ambiguous/stoplisted candidates produced NO edge.
	for target := range got0 {
		if _, ok := want0[target]; !ok {
			t.Errorf("chunk0 has unexpected edge to %s", target)
		}
	}
	require.GreaterOrEqual(t, mineReport.KilledAmbiguous, 2, "ParseThing + dup/config.go must be killed as ambiguous")

	// --- Chunk 1 (fence section): StartEngine links at 0.70; RemoteOnlyM77 is
	// cross-service and fence tokens never cross services.
	got1 := map[string]mentionEdge{}
	for _, e := range edges[chunk1] {
		got1[e.TargetKey] = e
	}
	fnKey := "func:" + mineService + ":internal/engine/engine.go#StartEngine()"
	require.Contains(t, got1, fnKey, "fence StartEngine must link (got %v)", edges[chunk1])
	require.Equal(t, "docmine/fence", got1[fnKey].Strategy)
	require.InDelta(t, 0.70, got1[fnKey].Confidence, 1e-9)
	for target := range got1 {
		if target != fnKey {
			t.Errorf("chunk1 has unexpected edge to %s", target)
		}
	}

	// --- Idempotence: re-mining the same chunks does not duplicate edges. ---
	before := countAllMentions(t, client)
	_, err = miner.MineChunks(ctx, report.Changed)
	require.NoError(t, err)
	require.Equal(t, before, countAllMentions(t, client), "re-mining must not duplicate edges")

	// --- Resumability: a completed pass stamps minedAt, so nothing is left
	// for the failure-recovery path to pick up.
	unmined, err := mine.UnminedChunks(ctx, client, mineService, models.DefaultScope())
	require.NoError(t, err)
	require.Empty(t, unmined, "mined chunks must carry minedAt")

	// Clearing the marker resurfaces the chunk for recovery mining.
	_, err = client.ExecuteQuery(ctx, `
		MATCH (c:DocumentChunk {nodeKey: $key, scopeId: 'main'}) REMOVE c.minedAt
	`, map[string]any{"key": chunk0})
	require.NoError(t, err)
	unmined, err = mine.UnminedChunks(ctx, client, mineService, models.DefaultScope())
	require.NoError(t, err)
	require.Len(t, unmined, 1)
	require.Equal(t, chunk0, unmined[0].NodeKey)
	require.Equal(t, "docs/engine.md", unmined[0].FilePath, "FilePath recovered from the owning Document")
}

func countAllMentions(t *testing.T, client *neo4j.Client) int {
	t.Helper()
	recs, err := client.ExecuteQuery(context.Background(), `
		MATCH (c:DocumentChunk {serviceName: $svc})-[m:MENTIONS]->() RETURN count(m) AS n
	`, map[string]any{"svc": mineService})
	require.NoError(t, err)
	n, _ := recs[0].AsMap()["n"].(int64)
	return int(n)
}
