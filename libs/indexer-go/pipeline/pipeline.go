package pipeline

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/context-maximiser/code-graph/libs/core-models-go"
	"github.com/context-maximiser/code-graph/libs/neo4j-go"
	"github.com/context-maximiser/code-graph/libs/search-go"
	textindex "github.com/context-maximiser/code-graph/libs/text-index-client-go"
)

// StageName identifies a pipeline stage.
type StageName string

const (
	StageIngestCode              StageName = "IngestCode"
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

// PipelineConfig carries all dependencies and parameters through the pipeline.
type PipelineConfig struct {
	Client           *neo4j.Client
	ScopeCtx         models.ScopeContext
	ProjectPath      string
	ServiceName      string
	Version          string
	RepoURL          string
	Language         string // Auto-detected or explicit.
	EmbeddingService search.EmbeddingService
	VectorStore      search.VectorStore
	TextStore        textindex.TextIndexStore
	DocPaths         []string // Paths to local docs (optional).
}

// Pipeline orchestrates the 7-stage enrichment pipeline.
type Pipeline struct {
	stages []Stage
}

// New creates a pipeline with the canonical stage order.
func New(stages ...Stage) *Pipeline {
	return &Pipeline{stages: stages}
}

// DefaultStages returns the 7 stages in canonical order.
func DefaultStages() []Stage {
	return []Stage{
		&IngestCodeStage{},
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
		start := time.Now()
		log.Printf("[pipeline] Running stage: %s", stage.Name())

		items, err := stage.Run(ctx, cfg)
		dur := time.Since(start)

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
