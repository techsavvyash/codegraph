package semlink

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/context-maximiser/code-graph/internal/graph/schema"
	models "github.com/context-maximiser/code-graph/internal/model"
	"github.com/context-maximiser/code-graph/internal/model/provenance"
	"golang.org/x/sync/errgroup"
)

const judgeSystemPrompt = "You judge whether a documentation excerpt describes or is implemented by a piece of code. " +
	`Reply with ONLY a JSON object: {"match": true|false, "confidence": 0.0-1.0}.`

// semantic confidence mapping (RFC-011 §5.2/§6): judge-confirmed edges land
// in [0.30, 0.60]; similarity-only edges cap at 0.55. Both stay strictly
// below Layer D's 0.70 floor.
const (
	judgeConfBase   = 0.30
	judgeConfSlope  = 0.30
	simOnlyScale    = 0.60
	simOnlyCap      = 0.55
	maxJudgeExcerpt = 1500
)

// matchCandidate is one kNN hit for a chunk.
type matchCandidate struct {
	elementID string
	nodeKey   string
	label     string
	name      string
	summary   string
	cosine    float64
}

// matchChunks runs kNN + judge for each chunk and writes semlink MENTIONS.
// stampAllowed=false (budget-clipped summary corpus) suppresses the
// semlink-done markers so every chunk re-matches once the corpus completes.
func (r *Runner) matchChunks(ctx context.Context, chunkIDs []string, stampAllowed bool, report *Report) error {
	if len(chunkIDs) == 0 {
		return nil
	}

	model := r.embedder.Model()
	strategy := "semlink/" + model
	if !r.opts.judgeEnabled() {
		strategy = "semlink-sim/" + model
	}
	createdAt := time.Now().UTC().Format(time.RFC3339)

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(r.opts.Concurrency)
	for _, chunkID := range chunkIDs {
		g.Go(func() error {
			return r.matchOneChunk(gctx, chunkID, model, strategy, createdAt, stampAllowed, report)
		})
	}
	return g.Wait()
}

// matchOneChunk runs kNN + judge for a single chunk and writes its semlink
// MENTIONS. Safe to run concurrently: chunks are independent, and budget /
// report mutations go through the runner lock.
func (r *Runner) matchOneChunk(ctx context.Context, chunkID, model, strategy, createdAt string, stampAllowed bool, report *Report) error {
	chunk, err := r.loadChunkForMatch(ctx, chunkID)
	if err != nil {
		return err
	}
	if chunk == nil {
		return nil
	}

	cands, err := r.knnCandidates(ctx, chunk.vector)
	if err != nil {
		return err
	}

	// Drop targets this chunk already mentions (Layer D subsumes S; a
	// prior semlink edge just re-merges, skipping saves judge calls).
	linked, err := r.linkedTargets(ctx, chunkID)
	if err != nil {
		return err
	}

	var edges []map[string]any
	budgetHit := false
	for _, cand := range cands {
		if linked[cand.elementID] {
			continue
		}

		confidence := 0.0
		reasons := []string{"semantic-similarity"}
		if r.opts.judgeEnabled() {
			if !r.spendBudget() {
				r.tally(func() { report.SkippedBudget++ })
				budgetHit = true
				continue
			}
			verdict, jconf, err := r.judge(ctx, chunk.content, cand)
			if err != nil {
				return err
			}
			if !verdict {
				r.tally(func() { report.JudgeRejected++ })
				continue
			}
			r.tally(func() { report.JudgeAccepted++ })
			reasons = append(reasons, "llm-judge-confirmed")
			confidence = judgeConfBase + judgeConfSlope*clamp01(jconf)
		} else {
			confidence = cand.cosine * simOnlyScale
			if confidence > simOnlyCap {
				confidence = simOnlyCap
			}
			if confidence <= 0 {
				continue
			}
		}

		props, err := provenance.BuildMentionEdgeProps(confidence, reasons, strategy, createdAt,
			r.scope.ScopeID, []string{
				fmt.Sprintf("cos:%.3f", cand.cosine),
				"sumhash:" + hashString(cand.summary)[:12],
			})
		if err != nil {
			return fmt.Errorf("semlink provenance invalid for %s: %w", cand.nodeKey, err)
		}
		edges = append(edges, map[string]any{"fromId": chunkID, "toId": cand.elementID, "props": props})
	}

	if len(edges) > 0 {
		if err := r.client.MergeRelsBatch(ctx, string(models.MentionsRel), edges, 100); err != nil {
			return err
		}
		r.tally(func() { report.EdgesWritten += len(edges) })
	}

	// Mark the chunk matched only when its full candidate set was processed
	// against a complete summary corpus — a budget-interrupted chunk (or any
	// chunk of a summary-clipped run) re-matches next run.
	if !budgetHit && stampAllowed {
		if _, err := r.client.ExecuteQuery(ctx, `
			MATCH (c) WHERE elementId(c) = $id
			SET c.semlinkModel = $model, c.semlinkAt = $now, c.semlinkThreshold = $threshold
		`, map[string]any{"id": chunkID, "model": model, "now": createdAt,
			"threshold": r.opts.SimilarityThreshold}); err != nil {
			return err
		}
		r.tally(func() { report.ChunksMatched++ })
	}
	return nil
}

type chunkForMatch struct {
	content string
	vector  []float64 // []float64: what the driver returns and accepts
}

func (r *Runner) loadChunkForMatch(ctx context.Context, chunkID string) (*chunkForMatch, error) {
	records, err := r.client.ExecuteQuery(ctx, `
		MATCH (c) WHERE elementId(c) = $id
		RETURN c.content AS content, c.embedding AS embedding
	`, map[string]any{"id": chunkID})
	if err != nil || len(records) == 0 {
		return nil, err
	}
	m := records[0].AsMap()
	content := str(m, "content")
	raw, _ := m["embedding"].([]any)
	if len(raw) == 0 {
		return nil, nil
	}
	vec := make([]float64, len(raw))
	for i, v := range raw {
		switch f := v.(type) {
		case float64:
			vec[i] = f
		case float32:
			vec[i] = float64(f)
		}
	}
	return &chunkForMatch{content: content, vector: vec}, nil
}

// knnCandidates queries every code-summary vector index, converts Neo4j's
// normalized cosine score ((cos+1)/2) back to raw cosine, applies the
// threshold, and returns the global top-K.
func (r *Runner) knnCandidates(ctx context.Context, vector []float64) ([]matchCandidate, error) {
	var all []matchCandidate

	for _, idx := range schema.GetVectorIndexes() {
		if idx.NodeLabel == "DocumentChunk" {
			continue // chunks match against code, not other chunks
		}
		records, err := r.client.ExecuteQuery(ctx, `
			CALL db.index.vector.queryNodes($index, $k, $vector)
			YIELD node, score
			RETURN elementId(node) AS id, node.nodeKey AS nodeKey,
			       labels(node)[0] AS label, coalesce(node.name, node.path, '') AS name,
			       coalesce(node.summary, '') AS summary, score
		`, map[string]any{"index": idx.Name, "k": r.opts.TopK, "vector": vector})
		if err != nil {
			return nil, fmt.Errorf("vector query on %s failed: %w", idx.Name, err)
		}
		for _, rec := range records {
			m := rec.AsMap()
			score, _ := m["score"].(float64)
			cosine := 2*score - 1
			if cosine < r.opts.SimilarityThreshold {
				continue
			}
			all = append(all, matchCandidate{
				elementID: str(m, "id"),
				nodeKey:   str(m, "nodeKey"),
				label:     str(m, "label"),
				name:      str(m, "name"),
				summary:   str(m, "summary"),
				cosine:    cosine,
			})
		}
	}

	sort.Slice(all, func(i, j int) bool {
		if all[i].cosine != all[j].cosine {
			return all[i].cosine > all[j].cosine
		}
		return all[i].nodeKey < all[j].nodeKey
	})
	if len(all) > r.opts.TopK {
		all = all[:r.opts.TopK]
	}
	return all, nil
}

func (r *Runner) linkedTargets(ctx context.Context, chunkID string) (map[string]bool, error) {
	records, err := r.client.ExecuteQuery(ctx, `
		MATCH (c)-[:MENTIONS]->(t) WHERE elementId(c) = $id
		RETURN elementId(t) AS id
	`, map[string]any{"id": chunkID})
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, rec := range records {
		out[str(rec.AsMap(), "id")] = true
	}
	return out, nil
}

// judge asks the completer whether the chunk describes the candidate code.
func (r *Runner) judge(ctx context.Context, chunkContent string, cand matchCandidate) (bool, float64, error) {
	excerpt := chunkContent
	if len(excerpt) > maxJudgeExcerpt {
		excerpt = excerpt[:maxJudgeExcerpt]
	}
	user := fmt.Sprintf("Documentation excerpt:\n%s\n\nCode: %s %s\nSummary: %s\n\nDoes the excerpt describe or document this code?",
		excerpt, cand.label, cand.name, cand.summary)

	raw, err := r.completer.Complete(ctx, judgeSystemPrompt, user)
	if err != nil {
		return false, 0, fmt.Errorf("judge call failed for %s: %w", cand.nodeKey, err)
	}
	match, conf, ok := parseJudgeVerdict(raw)
	if !ok {
		// An unparseable verdict is a rejection, not a guess (I4: no edge
		// without a validated basis).
		return false, 0, nil
	}
	return match, conf, nil
}

// parseJudgeVerdict extracts the first JSON object from the reply.
func parseJudgeVerdict(raw string) (match bool, confidence float64, ok bool) {
	start := strings.IndexByte(raw, '{')
	end := strings.LastIndexByte(raw, '}')
	if start < 0 || end <= start {
		return false, 0, false
	}
	var v struct {
		Match      bool    `json:"match"`
		Confidence float64 `json:"confidence"`
	}
	if err := json.Unmarshal([]byte(raw[start:end+1]), &v); err != nil {
		return false, 0, false
	}
	return v.Match, v.Confidence, true
}

func clamp01(f float64) float64 {
	if f < 0 {
		return 0
	}
	if f > 1 {
		return 1
	}
	return f
}
