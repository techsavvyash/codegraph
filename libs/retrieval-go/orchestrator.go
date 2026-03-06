package retrieval

import (
	"context"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	models "github.com/context-maximiser/code-graph/libs/core-models-go"
	"github.com/context-maximiser/code-graph/libs/intelligence-go/contracts"
)

// Source name constants used in RetrievalCandidate.Source.
const (
	SourceGraph  = "graph"
	SourceVector = "vector"
	SourceText   = "text"
	SourceHybrid = "hybrid"
)

// Orchestrator runs multiple retrieval adapters in parallel, merges results
// using Reciprocal Rank Fusion (RRF), enforces overlay scope precedence,
// and captures retrieval diagnostics.
type Orchestrator struct {
	graphAdapter  *GraphAdapter
	vectorAdapter *VectorAdapter
	textAdapter   *TextAdapter
	overlay       OverlayFilter
	rrfK          float64 // RRF constant (default 60)
}

// OverlayFilter resolves overlay scope precedence for retrieved candidates.
// It hides tombstoned main-scope nodes and lets overlay nodes take precedence.
type OverlayFilter interface {
	// FilterCandidates applies overlay precedence: overlay wins, tombstoned main nodes hidden.
	FilterCandidates(ctx context.Context, candidates []contracts.RetrievalCandidate, scope models.ScopeContext) ([]contracts.RetrievalCandidate, error)
}

// OrchestratorOption configures an Orchestrator.
type OrchestratorOption func(*Orchestrator)

// WithGraphAdapter adds a graph retrieval adapter.
func WithGraphAdapter(a *GraphAdapter) OrchestratorOption {
	return func(o *Orchestrator) { o.graphAdapter = a }
}

// WithVectorAdapter adds a vector retrieval adapter.
func WithVectorAdapter(a *VectorAdapter) OrchestratorOption {
	return func(o *Orchestrator) { o.vectorAdapter = a }
}

// WithTextAdapter adds a text retrieval adapter.
func WithTextAdapter(a *TextAdapter) OrchestratorOption {
	return func(o *Orchestrator) { o.textAdapter = a }
}

// WithOverlayFilter adds an overlay scope filter.
func WithOverlayFilter(f OverlayFilter) OrchestratorOption {
	return func(o *Orchestrator) { o.overlay = f }
}

// WithRRFK sets the RRF constant (default 60).
func WithRRFK(k float64) OrchestratorOption {
	return func(o *Orchestrator) { o.rrfK = k }
}

// NewOrchestrator creates a retrieval orchestrator with the given adapters.
func NewOrchestrator(opts ...OrchestratorOption) *Orchestrator {
	o := &Orchestrator{rrfK: 60}
	for _, opt := range opts {
		opt(o)
	}
	return o
}

// Retrieve runs all configured adapters in parallel, merges via RRF,
// applies overlay filtering, and returns diagnostics.
func (o *Orchestrator) Retrieve(ctx context.Context, query string, scope models.ScopeContext, limit int) ([]contracts.RetrievalCandidate, *Diagnostics, error) {
	if limit <= 0 {
		limit = 20
	}

	diag := &Diagnostics{
		Query:   query,
		Scope:   scope,
		StartAt: time.Now(),
		Sources: make(map[string]*SourceDiagnostic),
	}

	var wg sync.WaitGroup
	results := make(chan adapterResult, 3)

	// Launch adapters in parallel
	if o.graphAdapter != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			start := time.Now()
			candidates, err := o.graphAdapter.Retrieve(ctx, query, scope, limit)
			results <- adapterResult{
				source:     SourceGraph,
				candidates: candidates,
				err:        err,
				duration:   time.Since(start),
			}
		}()
	}

	if o.vectorAdapter != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			start := time.Now()
			candidates, err := o.vectorAdapter.Retrieve(ctx, query, scope, limit)
			results <- adapterResult{
				source:     SourceVector,
				candidates: candidates,
				err:        err,
				duration:   time.Since(start),
			}
		}()
	}

	if o.textAdapter != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			start := time.Now()
			candidates, err := o.textAdapter.Retrieve(ctx, query, scope, limit)
			results <- adapterResult{
				source:     SourceText,
				candidates: candidates,
				err:        err,
				duration:   time.Since(start),
			}
		}()
	}

	// Close channel when all adapters finish
	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect all results and build diagnostics
	var allCandidates []adapterResult
	for r := range results {
		sd := &SourceDiagnostic{
			Source:   r.source,
			Latency: r.duration,
		}
		if r.err != nil {
			sd.Error = r.err.Error()
			sd.Fallback = true
		} else {
			sd.Count = len(r.candidates)
		}
		diag.Sources[r.source] = sd
		allCandidates = append(allCandidates, r)
	}

	// Merge using RRF
	merged := o.mergeRRF(allCandidates)

	// Apply overlay filter if configured and scope is PR
	if o.overlay != nil && scope.Scope == models.ScopePR {
		var err error
		merged, err = o.overlay.FilterCandidates(ctx, merged, scope)
		if err != nil {
			return nil, diag, err
		}
		diag.OverlayApplied = true
	}

	// Limit results
	if len(merged) > limit {
		merged = merged[:limit]
	}

	diag.TotalCandidates = len(merged)
	diag.Duration = time.Since(diag.StartAt)
	diag.computeMergeBehavior(allCandidates)

	return merged, diag, nil
}

// mergeRRF combines results from multiple adapters using Reciprocal Rank Fusion.
// For each candidate (keyed by nodeKey), it accumulates 1/(k+rank) across sources.
func (o *Orchestrator) mergeRRF(results []adapterResult) []contracts.RetrievalCandidate {
	type entry struct {
		candidate contracts.RetrievalCandidate
		rrf       float64
		sources   map[string]bool
	}

	byKey := make(map[string]*entry)

	for _, r := range results {
		if r.err != nil {
			continue
		}
		for rank, c := range r.candidates {
			key := normalizeNodeKey(c.NodeKey)
			e, ok := byKey[key]
			if !ok {
				e = &entry{
					candidate: c,
					sources:   make(map[string]bool),
				}
				byKey[key] = e
			}
			e.rrf += 1.0 / (o.rrfK + float64(rank+1))
			e.sources[r.source] = true

			// Merge metadata: prefer richer metadata
			if len(c.Metadata) > len(e.candidate.Metadata) {
				e.candidate.Metadata = c.Metadata
			}
		}
	}

	// Build sorted result
	merged := make([]contracts.RetrievalCandidate, 0, len(byKey))
	for _, e := range byKey {
		e.candidate.Score = e.rrf
		if len(e.sources) > 1 {
			e.candidate.Source = SourceHybrid
		}
		merged = append(merged, e.candidate)
	}

	sort.Slice(merged, func(i, j int) bool {
		return merged[i].Score > merged[j].Score
	})

	return merged
}

// normalizeNodeKey strips any scopeId:: prefix for dedup purposes.
func normalizeNodeKey(key string) string {
	if idx := strings.Index(key, "::"); idx >= 0 {
		return key[idx+2:]
	}
	return key
}

// Diagnostics captures retrieval execution metrics for observability.
type Diagnostics struct {
	Query           string                       `json:"query"`
	Scope           models.ScopeContext           `json:"scope"`
	StartAt         time.Time                     `json:"startAt"`
	Duration        time.Duration                 `json:"duration"`
	TotalCandidates int                           `json:"totalCandidates"`
	Sources         map[string]*SourceDiagnostic  `json:"sources"`
	OverlayApplied  bool                          `json:"overlayApplied"`
	MergeBehavior   MergeBehavior                 `json:"mergeBehavior"`
}

// SourceDiagnostic captures per-source retrieval metrics.
type SourceDiagnostic struct {
	Source   string        `json:"source"`
	Count    int           `json:"count"`
	Latency  time.Duration `json:"latency"`
	Error    string        `json:"error,omitempty"`
	Fallback bool          `json:"fallback"`
}

// MergeBehavior describes how results were merged across sources.
type MergeBehavior struct {
	GraphOnly      int `json:"graphOnly"`
	VectorOnly     int `json:"vectorOnly"`
	TextOnly       int `json:"textOnly"`
	MultiSource    int `json:"multiSource"`
	TotalPreMerge  int `json:"totalPreMerge"`
	TotalPostMerge int `json:"totalPostMerge"`
	DeduplicatedN  int `json:"deduplicated"`
}

// computeMergeBehavior counts how candidates were distributed across sources.
func (d *Diagnostics) computeMergeBehavior(results []adapterResult) {
	type entry struct {
		sources map[string]bool
	}
	byKey := make(map[string]*entry)
	totalPre := 0

	for _, r := range results {
		if r.err != nil {
			continue
		}
		totalPre += len(r.candidates)
		for _, c := range r.candidates {
			key := normalizeNodeKey(c.NodeKey)
			e, ok := byKey[key]
			if !ok {
				e = &entry{sources: make(map[string]bool)}
				byKey[key] = e
			}
			e.sources[r.source] = true
		}
	}

	mb := MergeBehavior{
		TotalPreMerge:  totalPre,
		TotalPostMerge: len(byKey),
		DeduplicatedN:  int(math.Max(0, float64(totalPre-len(byKey)))),
	}

	for _, e := range byKey {
		switch {
		case len(e.sources) > 1:
			mb.MultiSource++
		case e.sources[SourceGraph]:
			mb.GraphOnly++
		case e.sources[SourceVector]:
			mb.VectorOnly++
		case e.sources[SourceText]:
			mb.TextOnly++
		}
	}

	d.MergeBehavior = mb
}

type adapterResult struct {
	source     string
	candidates []contracts.RetrievalCandidate
	err        error
	duration   time.Duration
}
