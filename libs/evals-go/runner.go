package evals

import (
	"context"
	"fmt"
	"time"

	"github.com/context-maximiser/code-graph/libs/benchmarks-go"
	"github.com/context-maximiser/code-graph/libs/search-go"
)

// SearchMode controls which search backends are active during evaluation.
type SearchMode string

const (
	ModeHybrid      SearchMode = "hybrid"
	ModeVectorOnly  SearchMode = "vector-only"
	ModeBM25Only    SearchMode = "bm25-only"
	ModeSemanticOnly SearchMode = "semantic-only"
)

// RunConfig holds parameters for an evaluation run.
type RunConfig struct {
	Dataset *EvalDataset
	Mode    SearchMode
	Limit   int
	Warmup  int
	Verbose bool
}

// QueryMetrics holds per-query evaluation results.
type QueryMetrics struct {
	ID            string        `json:"id"`
	Query         string        `json:"query"`
	Category      string        `json:"category,omitempty"`
	RecallAtK     float64       `json:"recallAtK"`
	NDCG          float64       `json:"ndcg"`
	MRR           float64       `json:"mrr"`
	PrecisionAtK  float64       `json:"precisionAtK"`
	Found         int           `json:"found"`
	TotalExpected int           `json:"totalExpected"`
	Latency       time.Duration `json:"latency"`
	SourceContrib SourceContrib `json:"sourceContrib"`
}

// AggregateMetrics holds mean metrics across all queries.
type AggregateMetrics struct {
	MeanRecallAtK    float64       `json:"meanRecallAtK"`
	MeanNDCG         float64       `json:"meanNDCG"`
	MeanMRR          float64       `json:"meanMRR"`
	MeanPrecisionAtK float64       `json:"meanPrecisionAtK"`
	TotalContrib     SourceContrib `json:"totalSourceContrib"`
}

// EvalRun holds the complete results of an evaluation run.
type EvalRun struct {
	Dataset   string           `json:"dataset"`
	Mode      SearchMode       `json:"mode"`
	Weights   search.Weights   `json:"weights"`
	K         int              `json:"k"`
	Timestamp time.Time        `json:"timestamp"`
	Results   []QueryMetrics   `json:"results"`
	Aggregate AggregateMetrics `json:"aggregate"`
	Latency   LatencyStats     `json:"latency"`
	Phases    []benchmarks.PhaseResult `json:"phases,omitempty"`
}

// EvalRunner orchestrates evaluation queries against a HybridSearchManager.
type EvalRunner struct {
	searchMgr *search.HybridSearchManager
	timer     *benchmarks.PhaseTimer
}

// NewEvalRunner creates an EvalRunner with the given search manager.
func NewEvalRunner(searchMgr *search.HybridSearchManager) *EvalRunner {
	return &EvalRunner{
		searchMgr: searchMgr,
		timer:     benchmarks.NewPhaseTimer(),
	}
}

// weightsForMode returns search weights that enable only the specified search mode.
func weightsForMode(mode SearchMode) search.Weights {
	switch mode {
	case ModeVectorOnly:
		return search.Weights{Vector: 1, FullText: 0, Semantic: 0}
	case ModeBM25Only:
		return search.Weights{Vector: 0, FullText: 1, Semantic: 0}
	case ModeSemanticOnly:
		return search.Weights{Vector: 0, FullText: 0, Semantic: 1}
	default:
		return search.DefaultWeights
	}
}

// Run executes the full evaluation: warmup queries, then scored eval queries.
func (er *EvalRunner) Run(ctx context.Context, cfg RunConfig) (*EvalRun, error) {
	weights := weightsForMode(cfg.Mode)
	k := cfg.Limit
	if k <= 0 {
		k = cfg.Dataset.DefaultK
	}

	// Warmup phase
	if cfg.Warmup > 0 {
		er.timer.Start("warmup")
		for i := 0; i < cfg.Warmup && i < len(cfg.Dataset.Queries); i++ {
			_, _ = er.searchMgr.UnifiedSearch(ctx, cfg.Dataset.Queries[i].Query, k, weights)
		}
		er.timer.Stop(cfg.Warmup, "warmup queries")
	}

	// Evaluation phase
	er.timer.Start("evaluation")
	var (
		results   []QueryMetrics
		latencies []time.Duration
	)

	for _, q := range cfg.Dataset.Queries {
		qLimit := k
		if q.Limit > 0 {
			qLimit = q.Limit
		}

		start := time.Now()
		resp, err := er.searchMgr.UnifiedSearch(ctx, q.Query, qLimit, weights)
		elapsed := time.Since(start)
		latencies = append(latencies, elapsed)

		if err != nil {
			if cfg.Verbose {
				fmt.Printf("  [%s] ERROR: %v\n", q.ID, err)
			}
			results = append(results, QueryMetrics{
				ID:            q.ID,
				Query:         q.Query,
				Category:      q.Category,
				TotalExpected: len(q.Expected),
				Latency:       elapsed,
			})
			continue
		}

		relevanceMap := q.RelevanceMap()
		retrieved := extractNodeKeys(resp.Results)

		recall := ComputeRecallAtK(retrieved, relevanceMap, qLimit)
		ndcg := ComputeNDCG(retrieved, relevanceMap, qLimit)
		mrr := ComputeMRR(retrieved, relevanceMap)
		precision := ComputePrecisionAtK(retrieved, relevanceMap, qLimit)
		contrib := AnalyzeSourceContribution(resp.Results, relevanceMap)

		found := countFound(retrieved, relevanceMap, qLimit)
		totalRelevant := countRelevant(relevanceMap)

		qm := QueryMetrics{
			ID:            q.ID,
			Query:         q.Query,
			Category:      q.Category,
			RecallAtK:     recall,
			NDCG:          ndcg,
			MRR:           mrr,
			PrecisionAtK:  precision,
			Found:         found,
			TotalExpected: totalRelevant,
			Latency:       elapsed,
			SourceContrib: contrib,
		}
		results = append(results, qm)

		if cfg.Verbose {
			fmt.Printf("  [%s] Recall=%.3f nDCG=%.3f MRR=%.3f Prec=%.3f (%d/%d) %v\n",
				q.ID, recall, ndcg, mrr, precision, found, totalRelevant, elapsed)
		}
	}
	er.timer.Stop(len(cfg.Dataset.Queries), "eval queries")

	aggregate := computeAggregate(results)
	latencyStats := ComputeLatencyStats(latencies)

	return &EvalRun{
		Dataset:   cfg.Dataset.Name,
		Mode:      cfg.Mode,
		Weights:   weights,
		K:         k,
		Timestamp: time.Now(),
		Results:   results,
		Aggregate: aggregate,
		Latency:   latencyStats,
		Phases:    er.timer.Results(),
	}, nil
}

// extractNodeKeys pulls the nodeKey from each search result.
func extractNodeKeys(results []search.HybridSearchResult) []string {
	keys := make([]string, 0, len(results))
	for _, r := range results {
		if key, ok := r.Node["nodeKey"].(string); ok {
			keys = append(keys, key)
		}
	}
	return keys
}

// countFound counts how many relevant items appear in the top-k retrieved results.
func countFound(retrieved []string, relevant map[string]RelevanceGrade, k int) int {
	if k > len(retrieved) {
		k = len(retrieved)
	}
	found := 0
	for i := 0; i < k; i++ {
		if grade, ok := relevant[retrieved[i]]; ok && grade > 0 {
			found++
		}
	}
	return found
}

// countRelevant counts the number of relevant items (grade > 0) in the map.
func countRelevant(relevant map[string]RelevanceGrade) int {
	n := 0
	for _, g := range relevant {
		if g > 0 {
			n++
		}
	}
	return n
}

// computeAggregate computes mean metrics across all query results.
func computeAggregate(results []QueryMetrics) AggregateMetrics {
	if len(results) == 0 {
		return AggregateMetrics{}
	}
	var agg AggregateMetrics
	for _, r := range results {
		agg.MeanRecallAtK += r.RecallAtK
		agg.MeanNDCG += r.NDCG
		agg.MeanMRR += r.MRR
		agg.MeanPrecisionAtK += r.PrecisionAtK
		agg.TotalContrib.AddContrib(r.SourceContrib)
	}
	n := float64(len(results))
	agg.MeanRecallAtK /= n
	agg.MeanNDCG /= n
	agg.MeanMRR /= n
	agg.MeanPrecisionAtK /= n
	return agg
}
