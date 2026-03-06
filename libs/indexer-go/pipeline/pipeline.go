package pipeline

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/context-maximiser/code-graph/libs/core-models-go"
	"github.com/context-maximiser/code-graph/libs/indexer-go/generated"
	"github.com/context-maximiser/code-graph/libs/intelligence-go/contracts"
	"github.com/context-maximiser/code-graph/libs/neo4j-go"
	"github.com/context-maximiser/code-graph/libs/search-go"
	textindex "github.com/context-maximiser/code-graph/libs/text-index-client-go"
)

// StageName identifies a pipeline stage.
type StageName string

const (
	StageIngestCode              StageName = "IngestCode"
	StageComputeGraphMetrics     StageName = "ComputeGraphMetrics"
	StageInferServiceDeps        StageName = "InferServiceDependencies"
	StageGenerateFlowSpines      StageName = "GenerateFlowSpines"
	StageIngestDocuments         StageName = "IngestDocuments"
	StageLinkDocumentChunks      StageName = "LinkDocumentChunks"
	StageGenerateContextDocs     StageName = "GenerateContextDocs"
	StageRefreshRetrievalIndexes StageName = "RefreshRetrievalIndexes"
)

// StageResult captures the outcome of a single stage.
type StageResult struct {
	Name     StageName
	Duration time.Duration
	Items    int    // Number of items processed.
	Err      error  // Non-nil if the stage failed.
	Skipped  bool   // True if the stage was skipped (e.g. missing deps).
}

// Stage is the contract every pipeline stage must satisfy.
type Stage interface {
	// Name returns the stage identifier.
	Name() StageName
	// Run executes the stage. It returns the number of items processed.
	Run(ctx context.Context, cfg *PipelineConfig) (int, error)
	// Optional returns true if the stage may be skipped on failure.
	Optional() bool
}

// PipelineTimer records phase timings during a pipeline run.
// Implemented by benchmarks.PhaseTimer.
type PipelineTimer interface {
	Start(name string)
	Stop(items int, detail string)
	AddResult(name string, duration time.Duration, items int, detail string)
}

// PipelineConfig carries all dependencies and parameters through the pipeline.
type PipelineConfig struct {
	Client           *neo4j.Client
	ScopeCtx         models.ScopeContext
	ProjectPath      string
	ServiceName      string
	Version          string
	RepoURL          string
	Language         string // Auto-detected or explicit.
	TenantID         string // Multi-tenant namespace (optional).
	Repo             string // Repository identifier (optional).
	EmbeddingService search.EmbeddingService
	VectorStore      search.VectorStore
	TextStore        textindex.TextIndexStore
	DocPaths         []string // Paths to local docs (optional).
	Timer            PipelineTimer // Optional phase timer for benchmarking.
	Generator        contracts.Generator       // Optional: evidence-backed generation.
	Verifier         contracts.Verifier        // Optional: citation verification.
	Policy           generated.PolicyEvaluator // Optional: persistence policy gate.
}

// Pipeline orchestrates the 7-stage enrichment pipeline.
type Pipeline struct {
	stages []Stage
}

// New creates a pipeline with the canonical stage order.
func New(stages ...Stage) *Pipeline {
	return &Pipeline{stages: stages}
}

// DefaultStages returns the 8 stages in canonical order.
func DefaultStages() []Stage {
	return []Stage{
		&IngestCodeStage{},
		&ComputeGraphMetricsStage{},
		&InferServiceDepsStage{},
		&GenerateFlowSpinesStage{},
		&IngestDocumentsStage{},
		&LinkDocumentChunksStage{},
		&GenerateContextDocsStage{},
		&RefreshRetrievalIndexesStage{},
	}
}

// Run executes all stages in order. Non-optional stage failures abort the pipeline.
// Optional stage failures are logged but do not stop execution.
func (p *Pipeline) Run(ctx context.Context, cfg *PipelineConfig) []StageResult {
	results := make([]StageResult, 0, len(p.stages))

	for _, stage := range p.stages {
		if cfg.Timer != nil {
			cfg.Timer.Start(string(stage.Name()))
		}
		start := time.Now()
		log.Printf("[pipeline] Running stage: %s", stage.Name())

		items, err := stage.Run(ctx, cfg)
		dur := time.Since(start)

		if cfg.Timer != nil {
			cfg.Timer.Stop(items, "")
		}

		result := StageResult{
			Name:     stage.Name(),
			Duration: dur,
			Items:    items,
			Err:      err,
		}

		if err != nil {
			if stage.Optional() {
				log.Printf("[pipeline] Optional stage %s failed (%.1fs): %v", stage.Name(), dur.Seconds(), err)
				result.Skipped = true
			} else {
				log.Printf("[pipeline] Stage %s failed (%.1fs): %v", stage.Name(), dur.Seconds(), err)
				results = append(results, result)
				return results // Abort on non-optional failure.
			}
		} else {
			log.Printf("[pipeline] Stage %s completed (%.1fs): %d items", stage.Name(), dur.Seconds(), items)
		}

		results = append(results, result)
	}

	return results
}

// StageTier groups stages that can run concurrently within a tier.
// Tiers execute sequentially; stages within a tier execute in parallel.
type StageTier struct {
	Stages []Stage
}

// DefaultTiers returns the default parallel execution tiers.
// Tier 0: IngestCode (required, must run first)
// Tier 1: ComputeGraphMetrics (depends on ingested call graph)
// Tier 2: InferServiceDeps, GenerateFlowSpines, IngestDocuments (independent)
// Tier 3: LinkDocumentChunks, GenerateContextDocs (depend on tier 2)
// Tier 4: RefreshRetrievalIndexes (final)
func DefaultTiers() []StageTier {
	return []StageTier{
		{Stages: []Stage{&IngestCodeStage{}}},
		{Stages: []Stage{&ComputeGraphMetricsStage{}}},
		{Stages: []Stage{&InferServiceDepsStage{}, &GenerateFlowSpinesStage{}, &IngestDocumentsStage{}}},
		{Stages: []Stage{&LinkDocumentChunksStage{}, &GenerateContextDocsStage{}}},
		{Stages: []Stage{&RefreshRetrievalIndexesStage{}}},
	}
}

// RunParallel executes stages in tiered parallel groups.
// Stages within a tier run concurrently; tiers run sequentially.
// A non-optional failure in any tier aborts the pipeline.
func (p *Pipeline) RunParallel(ctx context.Context, cfg *PipelineConfig, tiers []StageTier) []StageResult {
	var allResults []StageResult

	for tierIdx, tier := range tiers {
		log.Printf("[pipeline] Starting tier %d with %d stage(s)", tierIdx, len(tier.Stages))

		tierResults := make([]StageResult, len(tier.Stages))
		var wg sync.WaitGroup

		for i, stage := range tier.Stages {
			wg.Add(1)
			go func(idx int, s Stage) {
				defer wg.Done()
				start := time.Now()
				log.Printf("[pipeline] Running stage: %s (tier %d)", s.Name(), tierIdx)

				items, err := s.Run(ctx, cfg)
				dur := time.Since(start)

				result := StageResult{
					Name:     s.Name(),
					Duration: dur,
					Items:    items,
					Err:      err,
				}

				if err != nil {
					if s.Optional() {
						log.Printf("[pipeline] Optional stage %s failed (%.1fs): %v", s.Name(), dur.Seconds(), err)
						result.Skipped = true
					} else {
						log.Printf("[pipeline] Stage %s failed (%.1fs): %v", s.Name(), dur.Seconds(), err)
					}
				} else {
					log.Printf("[pipeline] Stage %s completed (%.1fs): %d items", s.Name(), dur.Seconds(), items)
				}

				tierResults[idx] = result
			}(i, stage)
		}

		wg.Wait()

		// Record tier results into the timer and check for failures.
		for _, result := range tierResults {
			if cfg.Timer != nil {
				cfg.Timer.AddResult(string(result.Name), result.Duration, result.Items, "")
			}
			allResults = append(allResults, result)
			if result.Err != nil && !result.Skipped {
				return allResults // Abort on non-optional failure.
			}
		}
	}

	return allResults
}

// Summary returns a human-readable summary of the pipeline run.
func Summary(results []StageResult) string {
	var total time.Duration
	failed := 0
	for _, r := range results {
		total += r.Duration
		if r.Err != nil && !r.Skipped {
			failed++
		}
	}
	return fmt.Sprintf("Pipeline: %d stages, %d failed, total %.1fs",
		len(results), failed, total.Seconds())
}
