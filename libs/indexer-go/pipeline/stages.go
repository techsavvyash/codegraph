package pipeline

import (
	"context"
	"fmt"
	"log"

	"github.com/context-maximiser/code-graph/libs/indexer-go/documents"
	"github.com/context-maximiser/code-graph/libs/indexer-go/generated"
	"github.com/context-maximiser/code-graph/libs/indexer-go/static"
	"github.com/context-maximiser/code-graph/libs/search-go"
)

// --- Stage 1: IngestCode ---

type IngestCodeStage struct{}

func (s *IngestCodeStage) Name() StageName { return StageIngestCode }
func (s *IngestCodeStage) Optional() bool  { return false }

func (s *IngestCodeStage) Run(ctx context.Context, cfg *PipelineConfig) (int, error) {
	indexer := static.NewSCIPIndexer(cfg.Client, cfg.ServiceName, cfg.Version, cfg.RepoURL)
	indexer.SetScope(cfg.ScopeCtx)
	if cfg.EmbeddingService != nil {
		indexer.SetEmbeddingService(cfg.EmbeddingService)
	}
	if cfg.VectorStore != nil {
		indexer.SetVectorStore(cfg.VectorStore)
	}
	if cfg.TextStore != nil {
		indexer.SetTextStore(cfg.TextStore)
	}

	if err := indexer.IndexProjectPolyglot(ctx, cfg.ProjectPath); err != nil {
		return 0, fmt.Errorf("IngestCode: %w", err)
	}
	return 1, nil
}

// --- Stage 2: InferServiceDependencies ---

type InferServiceDepsStage struct{}

func (s *InferServiceDepsStage) Name() StageName { return StageInferServiceDeps }
func (s *InferServiceDepsStage) Optional() bool  { return true }

func (s *InferServiceDepsStage) Run(ctx context.Context, cfg *PipelineConfig) (int, error) {
	// Service dependency inference is handled as part of SCIP symbol analysis.
	// This stage is a placeholder for future cross-service dependency detection.
	log.Printf("[InferServiceDeps] Service dependency inference is included in IngestCode stage")
	return 0, nil
}

// --- Stage 3: GenerateFlowSpines ---

type GenerateFlowSpinesStage struct{}

func (s *GenerateFlowSpinesStage) Name() StageName { return StageGenerateFlowSpines }
func (s *GenerateFlowSpinesStage) Optional() bool  { return true }

func (s *GenerateFlowSpinesStage) Run(ctx context.Context, cfg *PipelineConfig) (int, error) {
	// Flow spine generation detects HTTP handlers, event processors, etc.
	// and expands bounded call graphs from those entrypoints.
	// Full implementation in Phase 5; this stage is a placeholder.
	log.Printf("[GenerateFlowSpines] Flow spine generation (Phase 5 placeholder)")
	return 0, nil
}

// --- Stage 4: IngestDocuments ---

type IngestDocumentsStage struct{}

func (s *IngestDocumentsStage) Name() StageName { return StageIngestDocuments }
func (s *IngestDocumentsStage) Optional() bool  { return true }

func (s *IngestDocumentsStage) Run(ctx context.Context, cfg *PipelineConfig) (int, error) {
	if len(cfg.DocPaths) == 0 {
		return 0, nil
	}

	indexer := documents.NewDocumentIndexer(cfg.Client)
	indexer.SetScope(cfg.ScopeCtx)
	if cfg.TextStore != nil {
		indexer.WithTextStore(cfg.TextStore)
	}
	if cfg.EmbeddingService != nil && cfg.VectorStore != nil {
		indexer.WithVectorStore(cfg.EmbeddingService, cfg.VectorStore)
	}

	total := 0
	for _, docPath := range cfg.DocPaths {
		if err := indexer.IndexDirectory(ctx, docPath); err != nil {
			log.Printf("Warning: failed to index docs at %s: %v", docPath, err)
			continue
		}
		total++
	}
	return total, nil
}

// --- Stage 5: LinkDocumentChunks ---

type LinkDocumentChunksStage struct{}

func (s *LinkDocumentChunksStage) Name() StageName { return StageLinkDocumentChunks }
func (s *LinkDocumentChunksStage) Optional() bool  { return true }

func (s *LinkDocumentChunksStage) Run(ctx context.Context, cfg *PipelineConfig) (int, error) {
	// Run chunk linker for all documents in this scope.
	cl := search.NewChunkLinker(cfg.Client)
	cl.SetScope(cfg.ScopeCtx.ScopeID)

	// Find all documents in this scope and link their chunks.
	cypher := `
		MATCH (d:Document)
		WHERE d.scopeId = $scopeId OR d.scopeId = 'main'
		RETURN d.nodeKey AS nodeKey
	`
	records, err := cfg.Client.ExecuteQuery(ctx, cypher, map[string]any{
		"scopeId": cfg.ScopeCtx.ScopeID,
	})
	if err != nil {
		return 0, fmt.Errorf("LinkDocumentChunks: failed to query documents: %w", err)
	}

	totalLinks := 0
	for _, r := range records {
		m := r.AsMap()
		nk, _ := m["nodeKey"].(string)
		if nk == "" {
			continue
		}
		n, linkErr := cl.LinkChunksForDocument(ctx, nk, cfg.ScopeCtx.ScopeID)
		if linkErr != nil {
			log.Printf("Warning: chunk linking failed for %s: %v", nk, linkErr)
			continue
		}
		totalLinks += n
	}
	return totalLinks, nil
}

// --- Stage 6: GenerateContextDocs ---

type GenerateContextDocsStage struct{}

func (s *GenerateContextDocsStage) Name() StageName { return StageGenerateContextDocs }
func (s *GenerateContextDocsStage) Optional() bool  { return true }

func (s *GenerateContextDocsStage) Run(ctx context.Context, cfg *PipelineConfig) (int, error) {
	ctxGen := generated.NewContextGenerator(cfg.Client)
	ctxGen.SetScope(cfg.ScopeCtx)

	total := 0

	// Docstring suggestions for exported symbols missing docs.
	if n, err := ctxGen.GenerateDocstringSuggestionsForScope(ctx); err != nil {
		log.Printf("Warning: docstring suggestion generation failed: %v", err)
	} else {
		total += n
	}

	// Flow summaries for Flow nodes without summaries.
	if n, err := ctxGen.GenerateFlowSummariesForScope(ctx); err != nil {
		log.Printf("Warning: flow summary generation failed: %v", err)
	} else {
		total += n
	}

	return total, nil
}

// --- Stage 7: RefreshRetrievalIndexes ---

type RefreshRetrievalIndexesStage struct{}

func (s *RefreshRetrievalIndexesStage) Name() StageName { return StageRefreshRetrievalIndexes }
func (s *RefreshRetrievalIndexesStage) Optional() bool  { return true }

func (s *RefreshRetrievalIndexesStage) Run(ctx context.Context, cfg *PipelineConfig) (int, error) {
	// Tri-store refresh is handled inline during IngestCode.
	// This stage exists as a hook for explicit re-indexing when
	// only documents change (no code change).
	if cfg.EmbeddingService == nil || cfg.VectorStore == nil {
		return 0, nil
	}
	log.Printf("[RefreshRetrievalIndexes] Retrieval indexes refreshed during ingest stages")
	return 0, nil
}
