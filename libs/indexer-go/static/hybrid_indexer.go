package static

import (
	"context"
	"fmt"

	models "github.com/context-maximiser/code-graph/libs/core-models-go"
	neo4j "github.com/context-maximiser/code-graph/libs/neo4j-go"
	search "github.com/context-maximiser/code-graph/libs/search-go"
	textindex "github.com/context-maximiser/code-graph/libs/text-index-client-go"
)

// IndexConfig holds all configuration needed for both the SCIP and AST passes.
type IndexConfig struct {
	Client      *neo4j.Client
	ServiceName string
	Version     string
	RepoURL     string
	ProjectPath string
	ScopeCtx    models.ScopeContext
	Timer       PipelineTimer

	// Language specifies the indexing language. Defaults to LanguageGo for
	// IndexGoService. Required for IndexViaSCIP when not Go.
	Language Language

	// Optional secondary stores (Qdrant + OpenSearch).
	EmbeddingService search.EmbeddingService
	VectorStore      search.VectorStore
	TextStore        textindex.TextIndexStore
}

// IndexGoService is the Go-specific hybrid SCIP+AST indexing entry point.
//
// Stage 1 (SCIP pass) indexes type-oracle data: symbol definitions, IMPLEMENTS
// edges, DEFINES edges, and filtered REFERENCES (function-call occurrences only).
// Call graph construction, RPC/DB/event detection are suppressed in this pass.
//
// Stage 2 (AST pass) builds the behavioral graph: CALLS edges via
// SCIPCallGraphBuilder, RPC/DB/event call-site nodes and edges via
// runASTRPCDetection, API surface via APISurfaceDetector, and semantic edges.
//
// Both passes write to the same Neo4j instance using MERGE with compatible
// nodeKeys — deduplication of Function/Method nodes is implicit.
func IndexGoService(ctx context.Context, cfg IndexConfig) error {
	fmt.Println("[hybrid] Stage 1: SCIP pass (symbols, IMPLEMENTS, filtered REFERENCES)...")
	if err := runSCIPPass(ctx, cfg); err != nil {
		return fmt.Errorf("SCIP pass failed: %w", err)
	}

	fmt.Println("[hybrid] Stage 2: AST pass (call graph, RPC/DB/event detection)...")
	if err := runASTPass(ctx, cfg); err != nil {
		return fmt.Errorf("AST pass failed: %w", err)
	}

	fmt.Println("[hybrid] Stage 3: merge (secondary stores)...")
	return mergeResults(ctx, cfg)
}

// runSCIPPass runs the symbol-indexing portion of the SCIP pipeline.
// It skips call graph, RPC/DB/event detection, API analysis, semantic edges,
// and secondary store population — those are the AST pass's responsibility.
func runSCIPPass(ctx context.Context, cfg IndexConfig) error {
	si := NewSCIPIndexerWithLanguage(cfg.Client, cfg.ServiceName, cfg.Version, cfg.RepoURL, LanguageGo)
	si.SetScope(cfg.ScopeCtx)
	if cfg.Timer != nil {
		si.SetBenchmarkTimer(cfg.Timer)
	}
	// Suppress all behavioral-graph work — SCIP pass owns type oracle only.
	si.skipCallGraph = true
	si.skipRPCDetection = true
	si.skipAPIAnalysis = true
	si.skipSemanticEdges = true
	si.skipSecondaryStores = true
	return si.IndexProject(ctx, cfg.ProjectPath)
}

// runASTPass builds the behavioral graph for a Go project.
// Prerequisites: Function/Method nodes must already exist in Neo4j (from SCIP pass).
func runASTPass(ctx context.Context, cfg IndexConfig) error {
	// CALLS edges via SCIP reference data + Go AST body ranges.
	cgBuilder := NewSCIPCallGraphBuilder(cfg.Client, cfg.ProjectPath)
	cgBuilder.SetScope(cfg.ScopeCtx)
	cgBuilder.SetServiceName(cfg.ServiceName)
	if err := cgBuilder.BuildCallGraph(ctx); err != nil {
		fmt.Printf("Warning: call graph construction failed: %v\n", err)
	}

	// RPC / event / DB call-site detection via Go AST.
	// Reuse SCIPIndexer as a thin host for runASTRPCDetection.
	astHost := &SCIPIndexer{
		client:      cfg.Client,
		serviceName: cfg.ServiceName,
		version:     cfg.Version,
		repoURL:     cfg.RepoURL,
		language:    LanguageGo,
		scopeCtx:    cfg.ScopeCtx,
	}
	if err := astHost.runASTRPCDetection(ctx, cfg.ProjectPath); err != nil {
		fmt.Printf("Warning: AST RPC/DB/event detection failed: %v\n", err)
	}

	// API surface detection.
	modulePath := readModulePath(cfg.ProjectPath)
	apiDetector := NewAPISurfaceDetector(cfg.Client, modulePath)
	apiDetector.SetScope(cfg.ScopeCtx)
	if err := apiDetector.Detect(ctx); err != nil {
		fmt.Printf("Warning: structural API surface detection failed: %v\n", err)
	}

	// Semantic edges (message consumers, scheduled functions).
	sed := NewSemanticEdgeDetector(cfg.Client)
	sed.SetScope(cfg.ScopeCtx)
	if err := sed.DetectSemanticEdges(ctx); err != nil {
		fmt.Printf("Warning: semantic edge detection failed: %v\n", err)
	}

	return nil
}

// mergeResults performs any post-pass work after both SCIP and AST passes complete.
// Neo4j MERGE operations with compatible nodeKeys handle node deduplication
// automatically during each pass. This function populates secondary stores if
// configured, and is the single place both passes' output converges.
func mergeResults(ctx context.Context, cfg IndexConfig) error {
	if cfg.EmbeddingService == nil || cfg.VectorStore == nil {
		return nil
	}
	si := &SCIPIndexer{
		client:           cfg.Client,
		serviceName:      cfg.ServiceName,
		version:          cfg.Version,
		repoURL:          cfg.RepoURL,
		language:         LanguageGo,
		scopeCtx:         cfg.ScopeCtx,
		embeddingService: cfg.EmbeddingService,
		vectorStore:      cfg.VectorStore,
		textStore:        cfg.TextStore,
	}
	si.ensureSecondaryStoreIndexes(ctx)
	si.populateSecondaryStores(ctx)
	return nil
}

// IndexViaSCIP runs the full SCIP-only pipeline (no AST behavioral pass).
// Used for non-Go languages (TypeScript, Python, Java, etc.) where AST-level
// call graph and RPC detection are not yet implemented.
func IndexViaSCIP(ctx context.Context, cfg IndexConfig) error {
	lang := cfg.Language
	if lang == "" {
		lang = LanguageGo
	}
	si := NewSCIPIndexerWithLanguage(cfg.Client, cfg.ServiceName, cfg.Version, cfg.RepoURL, lang)
	si.SetScope(cfg.ScopeCtx)
	if cfg.Timer != nil {
		si.SetBenchmarkTimer(cfg.Timer)
	}
	if cfg.EmbeddingService != nil {
		si.SetEmbeddingService(cfg.EmbeddingService)
	}
	if cfg.VectorStore != nil {
		si.SetVectorStore(cfg.VectorStore)
	}
	if cfg.TextStore != nil {
		si.SetTextStore(cfg.TextStore)
	}
	return si.IndexProject(ctx, cfg.ProjectPath)
}

// IndexWithLanguageRouter dispatches to the correct indexing strategy based on language.
// Go projects use the hybrid SCIP+AST path (IndexGoService).
// All other languages use the SCIP-only path (IndexViaSCIP).
func IndexWithLanguageRouter(ctx context.Context, cfg IndexConfig) error {
	if cfg.Language == LanguageGo || cfg.Language == "" {
		return IndexGoService(ctx, cfg)
	}
	return IndexViaSCIP(ctx, cfg)
}
