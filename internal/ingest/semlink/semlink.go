// Package semlink implements RFC-011 Layer S: LLM code summaries embedded in
// Neo4j native vector indexes, doc chunks in the same space, and thresholded,
// judge-validated MENTIONS edges. Confidences are capped at 0.60 — strictly
// below every deterministic (Layer D) band, so ranking by confidence never
// prefers a semantic guess over an explicit reference.
package semlink

import (
	"context"
	"fmt"
	"sync"

	graph "github.com/context-maximiser/code-graph/internal/graph"
	"github.com/context-maximiser/code-graph/internal/llm"
	models "github.com/context-maximiser/code-graph/internal/model"
)

// Options tunes the semantic layer. Zero values take the RFC defaults.
type Options struct {
	// SimilarityThreshold is the minimum raw cosine similarity (-1..1) for a
	// kNN candidate. Note Neo4j reports normalized scores ((cos+1)/2); the
	// matcher converts back before comparing.
	SimilarityThreshold float64
	// TopK caps candidates per chunk after merging the per-label indexes.
	TopK int
	// Judge enables the per-candidate LLM validation pass. nil means the
	// RFC default (enabled) — a plain bool would make Options{} silently
	// select the weaker similarity-only mode.
	Judge *bool
	// MaxLLMCalls bounds Complete() calls (summaries + judgments) per run.
	// Embedding calls are not counted — they are orders of magnitude cheaper.
	// Hitting the budget stops cleanly; hash caches make re-runs resume.
	MaxLLMCalls int
	// Concurrency bounds in-flight LLM calls during summarization and
	// judging (the run's wall-clock is dominated by sequential completion
	// latency otherwise). Embedding is already batched. Set 1 for strictly
	// ordered runs.
	Concurrency int
}

func (o Options) withDefaults() Options {
	if o.SimilarityThreshold == 0 {
		o.SimilarityThreshold = 0.78
	}
	if o.TopK == 0 {
		o.TopK = 10
	}
	if o.MaxLLMCalls == 0 {
		o.MaxLLMCalls = 2000
	}
	if o.Concurrency == 0 {
		o.Concurrency = 8
	}
	if o.Judge == nil {
		on := true
		o.Judge = &on
	}
	return o
}

// judgeEnabled reports the resolved judge setting (defaults applied).
func (o Options) judgeEnabled() bool { return o.Judge != nil && *o.Judge }

// Report summarizes one Layer S run. Budget exhaustion is counted, not fatal.
type Report struct {
	SummariesWritten   int
	SummariesUpToDate  int
	EmbeddingsWritten  int
	ChunksMatched      int
	EdgesWritten       int
	JudgeAccepted      int
	JudgeRejected      int
	LLMCalls           int
	SkippedBudget      int
	VectorIndexesReset bool // true when a dimension change forced recreation
}

// Runner executes the semantic pipeline for one service.
type Runner struct {
	client      *graph.Client
	serviceName string
	scope       models.ScopeContext
	completer   llm.Completer
	embedder    llm.Embedder
	projectRoot string // for reading function bodies; "" = signatures/docstrings only
	opts        Options

	mu         sync.Mutex // guards budgetUsed and Report mutation across workers
	budgetUsed int
}

// tally applies a Report mutation under the runner lock — every counter
// update in a parallel phase must go through this.
func (r *Runner) tally(f func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	f()
}

// NewRunner wires a semantic linking run. completer may be nil only when
// the judge is disabled and summaries already exist; embedder is required.
func NewRunner(client *graph.Client, serviceName string, scope models.ScopeContext,
	completer llm.Completer, embedder llm.Embedder, projectRoot string, opts Options) (*Runner, error) {
	if embedder == nil {
		return nil, fmt.Errorf("semlink requires an embedder (configure llm.embedding)")
	}
	o := opts.withDefaults()
	if o.judgeEnabled() && completer == nil {
		return nil, fmt.Errorf("semlink judge pass requires a completer (configure llm.completion or set semlink.judge: false)")
	}
	return &Runner{
		client:      client,
		serviceName: serviceName,
		scope:       scope,
		completer:   completer,
		embedder:    embedder,
		projectRoot: projectRoot,
		opts:        o,
	}, nil
}

// Run executes: ensure vector indexes → summarize (symbols, files, service) →
// embed code summaries → embed chunks → match + judge → write edges.
func (r *Runner) Run(ctx context.Context) (*Report, error) {
	report := &Report{}

	reset, err := r.ensureVectorIndexes(ctx)
	if err != nil {
		return nil, err
	}
	report.VectorIndexesReset = reset

	if r.completer != nil {
		if err := r.summarizeAll(ctx, report); err != nil {
			return nil, err
		}
	}

	if err := r.embedCodeSummaries(ctx, report); err != nil {
		return nil, err
	}

	chunkIDs, err := r.embedChunks(ctx, report)
	if err != nil {
		return nil, err
	}

	// A budget-clipped summary corpus means incomplete kNN candidates:
	// matching still runs (edges found now are kept), but chunks must not be
	// stamped as matched, or the missing summaries would be invisible to
	// every future run.
	stampAllowed := report.SkippedBudget == 0
	if err := r.matchChunks(ctx, chunkIDs, stampAllowed, report); err != nil {
		return nil, err
	}

	report.LLMCalls = r.budgetUsed
	return report, nil
}

// spendBudget reserves one completion call. Returns false when exhausted.
// Reservation happens before the call, so concurrent workers can never
// overshoot MaxLLMCalls.
func (r *Runner) spendBudget() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.budgetUsed >= r.opts.MaxLLMCalls {
		return false
	}
	r.budgetUsed++
	return true
}
