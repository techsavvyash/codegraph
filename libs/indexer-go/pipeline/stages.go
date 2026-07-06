package pipeline

import (
	"context"
	"fmt"
	"log"

	gds "github.com/context-maximiser/code-graph/libs/gds-go"
	"github.com/context-maximiser/code-graph/libs/indexer-go/static"
	"github.com/context-maximiser/code-graph/libs/query-go"
)

// --- Stage 1: IngestCode ---

type IngestCodeStage struct{}

func (s *IngestCodeStage) Name() StageName { return StageIngestCode }
func (s *IngestCodeStage) Optional() bool  { return false }

func (s *IngestCodeStage) Run(ctx context.Context, cfg *PipelineConfig) (int, error) {
	indexer := static.NewSCIPIndexer(cfg.Client, cfg.ServiceName, cfg.Version, cfg.RepoURL)
	// Propagate tenant/repo into scope context.
	scopeCtx := cfg.ScopeCtx
	if cfg.TenantID != "" {
		scopeCtx.TenantID = cfg.TenantID
	}
	if cfg.Repo != "" {
		scopeCtx.Repo = cfg.Repo
	}
	indexer.SetScope(scopeCtx)
	// Propagate benchmark timer so SCIP sub-phases are recorded.
	if cfg.Timer != nil {
		indexer.SetBenchmarkTimer(cfg.Timer)
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
	gen := query.NewFlowSpineGenerator(cfg.Client)
	gen.SetScope(cfg.ScopeCtx)
	if cfg.ServiceName != "" {
		// Polyglot indexing creates sub-services as "{service}/{subpath}".
		// Prefix filtering keeps flow generation confined to the currently indexed project.
		gen.SetServicePrefix(cfg.ServiceName)
	}

	results, err := gen.GenerateFlows(ctx, 3)
	if err != nil {
		return 0, fmt.Errorf("GenerateFlowSpines: %w", err)
	}
	return len(results), nil
}

// --- Stage: ComputeGraphMetrics ---

type ComputeGraphMetricsStage struct{}

func (s *ComputeGraphMetricsStage) Name() StageName { return StageComputeGraphMetrics }
func (s *ComputeGraphMetricsStage) Optional() bool  { return true }

func (s *ComputeGraphMetricsStage) Run(ctx context.Context, cfg *PipelineConfig) (int, error) {
	gdsClient := gds.NewGDSClient(cfg.Client)

	// Check if GDS plugin is available; skip gracefully if not.
	if !gdsClient.IsGDSAvailable(ctx) {
		log.Printf("[ComputeGraphMetrics] GDS plugin not available, skipping")
		return 0, nil
	}

	graphName := "codegraph_calls_" + cfg.ScopeCtx.ScopeID
	nodeLabels := []string{"Function", "Method"}
	relTypes := []string{"CALLS"}

	// Project the call graph subgraph.
	if err := gdsClient.ProjectGraph(ctx, graphName, nodeLabels, relTypes, cfg.ScopeCtx.ScopeID); err != nil {
		return 0, fmt.Errorf("ComputeGraphMetrics: projection failed: %w", err)
	}
	defer gdsClient.DropGraph(ctx, graphName)

	totalWritten := 0

	// Run PageRank.
	if n, err := gdsClient.RunPageRank(ctx, graphName, gds.DefaultPageRankOpts()); err != nil {
		log.Printf("[ComputeGraphMetrics] PageRank failed: %v", err)
	} else {
		totalWritten += n
		log.Printf("[ComputeGraphMetrics] PageRank wrote %d properties", n)
	}

	// Run Betweenness Centrality.
	if n, err := gdsClient.RunBetweennessCentrality(ctx, graphName, gds.DefaultBetweennessOpts()); err != nil {
		log.Printf("[ComputeGraphMetrics] Betweenness failed: %v", err)
	} else {
		totalWritten += n
		log.Printf("[ComputeGraphMetrics] Betweenness wrote %d properties", n)
	}

	// Run Weakly Connected Components.
	if n, err := gdsClient.RunWCC(ctx, graphName); err != nil {
		log.Printf("[ComputeGraphMetrics] WCC failed: %v", err)
	} else {
		totalWritten += n
		log.Printf("[ComputeGraphMetrics] WCC wrote %d properties", n)
	}

	// Run Louvain Community Detection.
	if n, err := gdsClient.RunLouvain(ctx, graphName); err != nil {
		log.Printf("[ComputeGraphMetrics] Louvain failed: %v", err)
	} else {
		totalWritten += n
		log.Printf("[ComputeGraphMetrics] Louvain wrote %d properties", n)
	}

	return totalWritten, nil
}
