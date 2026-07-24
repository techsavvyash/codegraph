package static

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	goparser "go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	models "github.com/context-maximiser/code-graph/libs/core-models-go"
	"github.com/context-maximiser/code-graph/libs/indexer-go/contracts"
	"github.com/context-maximiser/code-graph/libs/indexer-go/generated"
	"github.com/context-maximiser/code-graph/libs/neo4j-go"
	"github.com/context-maximiser/code-graph/libs/search-go"
	textindex "github.com/context-maximiser/code-graph/libs/text-index-client-go"
)

// PipelineTimer is an optional interface for timing pipeline phases.
// Implementations include benchmarks.PhaseTimer.
type PipelineTimer interface {
	Start(name string)
	Stop(items int, detail string)
}

// SubPhaseRecorder is an optional extension of PipelineTimer for recording
// sub-phase breakdowns within a parent phase.
type SubPhaseRecorder interface {
	AddResult(name string, duration time.Duration, items int, detail string)
}

// SCIPIndexer indexes projects using the SCIP protocol
type SCIPIndexer struct {
	client           *neo4j.Client
	serviceName      string
	version          string
	repoURL          string
	language         Language
	langConfig       *LanguageConfig
	scopeCtx         models.ScopeContext
	timer            PipelineTimer
	fileContentCache map[string][]byte // cache for calculateByteOffsets
	projectPath      string            // absolute project root, set in IndexProject for absoluteFilePath

	// Tri-store support: secondary stores populated after Neo4j indexing.
	embeddingService    search.EmbeddingService
	vectorStore         search.VectorStore
	textStore           textindex.TextIndexStore
	skipSecondaryStores bool // true for sub-indexers in polyglot mode

	// hybrid mode: set by IndexGoService to partition responsibilities between passes.
	skipCallGraph     bool
	skipRPCDetection  bool
	skipAPIAnalysis   bool
	skipSemanticEdges bool

	// verbose enables the deep line-by-line diagnostics; default prints only the
	// phased summary block. report accumulates per-stage timing for that block.
	verbose bool
	report  *indexReport
}

// SetVerbose toggles deep line-by-line indexing diagnostics. When false (the
// default), only the phased summary block is printed.
func (si *SCIPIndexer) SetVerbose(v bool) { si.verbose = v }

// NewSCIPIndexer creates a new SCIP-based indexer
func NewSCIPIndexer(client *neo4j.Client, serviceName, version, repoURL string) *SCIPIndexer {
	// Default to Go for backward compatibility
	return NewSCIPIndexerWithLanguage(client, serviceName, version, repoURL, LanguageGo)
}

// NewSCIPIndexerWithLanguage creates a new SCIP-based indexer for a specific language
func NewSCIPIndexerWithLanguage(client *neo4j.Client, serviceName, version, repoURL string, lang Language) *SCIPIndexer {
	langConfig, err := GetLanguageConfig(lang)
	if err != nil {
		// Fallback to Go if language not found
		langConfig, _ = GetLanguageConfig(LanguageGo)
		lang = LanguageGo
	}

	return &SCIPIndexer{
		client:      client,
		serviceName: serviceName,
		version:     version,
		repoURL:     repoURL,
		language:    lang,
		langConfig:  langConfig,
		scopeCtx:    models.DefaultScope(),
	}
}

// IndexProject indexes a project using SCIP
func (si *SCIPIndexer) IndexProject(ctx context.Context, projectPath string) error {
	setIndexVerbose(si.verbose)
	if si.report == nil {
		si.report = newIndexReport(si.serviceName)
	}
	vprintf("Starting SCIP indexing for %s project at %s\n", si.langConfig.DisplayName, projectPath)

	// Resolve to absolute so absoluteFilePath can join relative SCIP paths.
	if absPath, err := filepath.Abs(projectPath); err == nil {
		si.projectPath = absPath
	} else {
		si.projectPath = projectPath
	}

	// Step 1-4: Generate + parse the SCIP index, create the service node, index files.
	si.report.begin("SCIP")
	if si.timer != nil {
		si.timer.Start("SCIP generation")
	}
	scipFile, err := si.generateSCIPIndex(projectPath)
	if err != nil {
		return fmt.Errorf("failed to generate SCIP index: %w", err)
	}
	if si.timer != nil {
		si.timer.Stop(0, "")
	}
	// Only clean up for languages that auto-generate (not Java/Scala/Kotlin)
	if si.language != LanguageJava && si.language != LanguageScala && si.language != LanguageKotlin {
		defer os.Remove(scipFile) // Clean up temporary file
	}

	vprintf("Using SCIP index file: %s\n", scipFile)

	if si.timer != nil {
		si.timer.Start("Parse SCIP file")
	}
	parser := NewSCIPParser()
	if err := parser.ParseFile(scipFile); err != nil {
		return fmt.Errorf("failed to parse SCIP file: %w", err)
	}
	if si.timer != nil {
		si.timer.Stop(0, "")
	}

	// Debug: Print SCIP file contents (verbose only — DebugPrintSCIPFile self-gates).
	if err := parser.DebugPrintSCIPFile(); err != nil {
		si.report.warn("failed to debug print SCIP file: %v", err)
	}

	if si.timer != nil {
		si.timer.Start("Create service node")
	}
	serviceID, err := si.createServiceNode(ctx, projectPath)
	if err != nil {
		return fmt.Errorf("failed to create service node: %w", err)
	}
	if si.timer != nil {
		si.timer.Stop(1, "")
	}

	if si.timer != nil {
		si.timer.Start("Index files")
	}
	files, err := parser.ExtractDocuments()
	if err != nil {
		return fmt.Errorf("failed to extract documents: %w", err)
	}

	fileNodes := make(map[string]string) // filePath -> nodeID mapping
	for _, file := range files {
		if isNoisyFilePath(file.Path) {
			continue
		}
		fileID, err := si.createFileNode(ctx, file, serviceID)
		if err != nil {
			si.report.warn("failed to create file node for %s: %v", file.Path, err)
			continue
		}
		fileNodes[file.Path] = fileID
	}
	if si.timer != nil {
		si.timer.Stop(len(fileNodes), fmt.Sprintf("%d files", len(fileNodes)))
	}
	si.report.end(fmt.Sprintf("%d docs → %d files", len(files), len(fileNodes)))

	// Step 5-7: Extract + index symbols (defs then refs) and IMPLEMENTS relationships.
	si.report.begin("Symbols")
	if si.timer != nil {
		si.timer.Start("Extract symbols")
	}
	symbolDefs, err := parser.ExtractSymbols()
	if err != nil {
		return fmt.Errorf("failed to extract symbols: %w", err)
	}
	// Filter out symbols defined in test files and strip test-file references.
	symbolDefs = filterTestSymbols(symbolDefs)
	if si.timer != nil {
		si.timer.Stop(len(symbolDefs), fmt.Sprintf("%d symbols", len(symbolDefs)))
	}

	symIdx, err := si.indexSymbols(ctx, symbolDefs, fileNodes)
	if err != nil {
		return fmt.Errorf("failed to index symbols: %w", err)
	}
	vprintf("Successfully indexed %d symbols from SCIP data\n", len(symbolDefs))

	if si.timer != nil {
		si.timer.Start("IMPLEMENTS relationships")
	}
	implCount := 0
	scipRels, err := parser.ExtractRelationships()
	if err != nil {
		si.report.warn("failed to extract SCIP relationships: %v", err)
	} else {
		for _, r := range scipRels {
			if r.IsImplementation {
				implCount++
			}
		}
		vprintf("Extracted %d SCIP relationships (%d implementation)\n", len(scipRels), implCount)

		if implCount > 0 && !isNoiseRelType(models.ImplementsRel) {
			batch := buildImplementsBatch(scipRels, symIdx.symbolIDs, symIdx.defIDs, symIdx.symbolToDefKey)
			if len(batch) > 0 {
				if err := si.client.CreateRelsBatch(ctx, string(models.ImplementsRel), batch, batchSize); err != nil {
					si.report.warn("failed to create IMPLEMENTS relationships: %v", err)
				} else {
					vprintf("Created %d IMPLEMENTS relationships\n", len(batch))
				}
			}
		}
	}
	if si.timer != nil {
		relCount := 0
		if scipRels != nil {
			relCount = len(scipRels)
		}
		si.timer.Stop(relCount, "")
	}
	si.report.end(fmt.Sprintf("%d syms · %d defs · %d impl", len(symbolDefs), len(symIdx.defIDs), implCount))

	// Step 8: Package dependencies
	si.report.begin("Deps")
	if si.timer != nil {
		si.timer.Start("Package dependencies")
	}
	depsCreated, depsSkipped := 0, 0
	imports, err := parser.ExtractImports(projectPath)
	if err != nil {
		si.report.warn("failed to extract imports: %v", err)
	} else {
		vprintf("Extracted %d import statements\n", len(imports))
		if depsCreated, depsSkipped, err = si.indexPackageDependencies(ctx, imports, serviceID); err != nil {
			si.report.warn("failed to index package dependencies: %v", err)
		}
	}
	if si.timer != nil {
		importCount := 0
		if imports != nil {
			importCount = len(imports)
		}
		si.timer.Stop(importCount, "")
	}
	si.report.end(fmt.Sprintf("%d internal · %d ext/stdlib", depsCreated, depsSkipped))

	// Step 9: Symbol-based API analysis
	// Step 10: Build call graph (Go only, requires AST for function body ranges).
	// Must run before API analysis so that Function/Method nodes have correct
	// startLine/endLine body ranges for findContainingFunction lookups.
	if !si.skipCallGraph {
		si.report.begin("Calls")
		if si.timer != nil {
			si.timer.Start("Call graph")
		}
		if si.language == LanguageGo {
			vprintln("Building call graph from SCIP references + Go AST...")
			cgBuilder := NewSCIPCallGraphBuilder(si.client, projectPath)
			cgBuilder.SetScope(si.scopeCtx)
			cgBuilder.SetServiceName(si.serviceName)
			// Feed SCIP's precise occurrence resolution so callees bind to the exact
			// symbol rather than an arbitrary bare-name match.
			cgBuilder.LoadSCIPResolution(ctx, symbolDefs, symIdx)
			if err := cgBuilder.BuildCallGraph(ctx); err != nil {
				si.report.warn("call graph construction failed: %v", err)
			}
			si.report.end(fmt.Sprintf("%d CALLS · %d precise/%d bare",
				cgBuilder.totalCalls, cgBuilder.preciseHits, cgBuilder.fallbackHits))
		} else {
			vprintln("Building call graph from SCIP references (language-agnostic)...")
			cgBuilder := NewGenericCallGraphBuilder(si.client)
			cgBuilder.SetScope(si.scopeCtx)
			cgBuilder.SetServiceName(si.serviceName)
			// Use the NPM package name or service name for target filtering.
			pkgName := si.extractNPMPackageName(projectPath)
			if pkgName == "" {
				pkgName = si.serviceName
			}
			cgBuilder.SetPackageName(pkgName)
			if err := cgBuilder.BuildCallGraph(ctx); err != nil {
				si.report.warn("call graph construction failed: %v", err)
			}
			si.report.end("built")
		}
		if si.timer != nil {
			si.timer.Stop(0, "")
		}
	}

	// Step 9b: Detect cross-service RPC call sites + outbound DB/HTTP/event/cache.
	// For Go: use AST-based detection (fast, no Reference nodes needed).
	// For other languages: fall back to SCIP graph traversal (needs Reference nodes).
	if !si.skipRPCDetection {
		si.report.begin("Detectors")
		// Capture the run watermark BEFORE the detectors write: every node they
		// (re-)emit gets updatedAt >= detectStart, so anything older afterwards is
		// a leftover from a previous run's (possibly wrong) detector output.
		detectStart := time.Now().UTC().Unix()
		detectOK := true
		if si.language == LanguageGo {
			vprintln("Detecting cross-service RPC call sites via Go AST...")
			stats, err := si.runASTRPCDetection(ctx, projectPath)
			if err != nil {
				si.report.warn("AST RPC detection failed: %v", err)
				detectOK = false
			}
			si.report.end(detectorDetail(stats))
			si.reportCoverageWarnings(stats)
		} else {
			vprintln("Detecting cross-service RPC call sites via SCIP symbols...")
			rpcDetector := NewSCIPRPCDetector(si.client, si.serviceName, si.scopeCtx)
			if svcIdx, idxErr := loadServiceIndex(ctx, si.client, si.scopeCtx.ScopeID); idxErr == nil {
				rpcDetector.SetServiceIndex(svcIdx)
			} else {
				si.report.warn("failed to preload service index for SCIP detector: %v", idxErr)
			}
			if err := rpcDetector.DetectRPCCalls(ctx); err != nil {
				si.report.warn("SCIP RPC detection failed: %v", err)
				detectOK = false
			}
			si.report.end("SCIP symbol traversal")
		}
		// Only sweep after a SUCCESSFUL detection pass — sweeping after a failed
		// one would delete the service's entire semantic layer.
		if detectOK {
			si.report.begin("Sweep")
			if n, err := SweepStaleCallNodes(ctx, si.client, si.serviceName, si.scopeCtx.ScopeID, detectStart); err != nil {
				si.report.warn("stale call-node sweep failed: %v", err)
				si.report.end("failed")
			} else {
				si.report.end(fmt.Sprintf("%d stale call nodes removed", n))
			}
		}
	}

	// Step 9c: Proto contract nodes (.proto → ProtoContract/ProtoMethod). Runs only
	// when the project ships proto sources (the shared contract repo). ProtoMethod
	// nodes are the cross-service rendezvous that activates IMPLEMENTED_BY linking
	// (API-surface Strategy 3b) on every subsequent service index — without this
	// step the ProtoIndexer was never invoked anywhere and 3b was permanently dead.
	if hasProtoSources(projectPath) {
		si.report.begin("Contracts")
		protoIdx := contracts.NewProtoIndexer(si.client, si.serviceName, si.scopeCtx)
		if err := protoIdx.IndexProtoFiles(ctx, projectPath); err != nil {
			si.report.warn("proto contract indexing failed: %v", err)
		}
		si.report.end("proto contracts")
	}

	// Step 10b: API analysis (depends on body ranges from call graph step above)
	if !si.skipAPIAnalysis {
		si.report.begin("API")
		if si.timer != nil {
			si.timer.Start("API analysis")
		}
		if si.language == LanguageGo {
			// Structural API surface detection — zero framework catalogs.
			vprintln("Detecting API surface via graph-structural signals...")
			modulePath := readModulePath(projectPath)
			apiDetector := NewAPISurfaceDetector(si.client, modulePath)
			apiDetector.SetScope(si.scopeCtx)
			if err := apiDetector.Detect(ctx); err != nil {
				si.report.warn("structural API surface detection failed: %v", err)
			}
			si.report.end("structural surface")
		} else {
			// Fallback: framework-pattern-based detection for non-Go languages.
			vprintln("Analyzing API patterns via SCIP symbol matching...")
			symAnalyzer := NewSymbolAnalyzer(si.client, si.serviceName, si.langConfig.DisplayName, projectPath)
			symAnalyzer.SetScope(si.scopeCtx)
			if err := symAnalyzer.AnalyzeBySymbols(ctx); err != nil {
				si.report.warn("symbol-based API analysis failed: %v", err)
			}
			si.report.end("symbol matching")
		}
		if si.timer != nil {
			si.timer.Stop(0, "")
		}
	}

	// Step 10a: Detect semantic edges (message consumers, scheduled functions)
	if !si.skipSemanticEdges {
		si.report.begin("Semantic")
		vprintln("Detecting semantic edges...")
		sed := NewSemanticEdgeDetector(si.client)
		sed.SetScope(si.scopeCtx)
		if err := sed.DetectSemanticEdges(ctx); err != nil {
			si.report.warn("semantic edge detection failed: %v", err)
		}
		si.report.end("consumers + cron")
	}

	// Step 10b: Populate secondary stores (Qdrant + OpenSearch)
	if !si.skipSecondaryStores && si.embeddingService != nil && si.vectorStore != nil {
		si.report.begin("Stores")
		if si.timer != nil {
			si.timer.Start("Secondary stores")
		}
		vprintln("Populating secondary stores (Qdrant + OpenSearch)...")
		si.ensureSecondaryStoreIndexes(ctx)
		si.populateSecondaryStores(ctx)
		if si.timer != nil {
			si.timer.Stop(0, "")
		}
		si.report.end("Qdrant + OpenSearch")
	}

	// Step 11: Create PullRequest node for PR overlays (generated-doc creation
	// is handled exclusively by pipeline Stage 6 — GenerateContextDocs).
	if si.scopeCtx.Scope == models.ScopePR && si.client != nil {
		ctxGen := generated.NewContextGenerator(si.client)
		ctxGen.SetScope(si.scopeCtx)

		// Extract PR ID from scopeId (format: "pr-{id}")
		prID := strings.TrimPrefix(si.scopeCtx.ScopeID, "pr-")

		if _, err := ctxGen.CreatePullRequestNode(ctx, prID,
			fmt.Sprintf("PR %s: %s indexing", prID, si.serviceName),
			"", "", "", ""); err != nil {
			si.report.warn("failed to create PullRequest node: %v", err)
		}
	}

	si.report.finish()
	return nil
}

// detectorDetail renders the per-detector node-count summary line for the block
// (DB/Cache/Event/External/GRPC/HTTP/Outbox), from a run's IndexStats.
func detectorDetail(stats *IndexStats) string {
	if stats == nil {
		return "no stats"
	}
	w := stats.written
	return fmt.Sprintf("DB=%d Cache=%d Evt=%d Ext=%d GRPC=%d HTTP=%d Outbox=%d",
		w["DBCall"], w["CacheCall"], w["EventType"], w["ExternalCall"],
		w["GRPCCall"], w["HTTPCall"], w["OutboxCall"])
}

// reportCoverageWarnings routes the P3-1 "a whole wrapper class is invisible"
// alarms into the report footer so they survive the non-verbose summary.
func (si *SCIPIndexer) reportCoverageWarnings(stats *IndexStats) {
	if stats == nil || si.report == nil {
		return
	}
	for _, w := range stats.coverageWarnings() {
		si.report.warn("%s", w)
	}
}

// IndexProjectPolyglot detects all languages present under projectPath, installs
// any missing SCIP indexers, and then indexes each language root in sequence.
// It propagates the service name, version, repo URL, and scope from the receiver.
//
// Partial failures (a single language failing while others succeed) are printed
// as warnings but do not abort the run. An error is returned only when every
// detected language root fails to index.
func (si *SCIPIndexer) IndexProjectPolyglot(ctx context.Context, projectPath string) error {
	// Resolve to absolute path so filepath.Rel works regardless of whether
	// the caller passed "." or a full absolute path.
	absProjectRoot, err := filepath.Abs(projectPath)
	if err != nil {
		return fmt.Errorf("failed to resolve project path: %w", err)
	}

	roots, err := DetectAllLanguages(absProjectRoot)
	if err != nil {
		return fmt.Errorf("language detection failed: %w", err)
	}
	if len(roots) == 0 {
		return fmt.Errorf("no supported languages detected in %s", absProjectRoot)
	}

	// relLabel returns a human-readable relative path label for a root.
	relLabel := func(absPath string) string {
		rel, err := filepath.Rel(absProjectRoot, absPath)
		if err != nil || rel == "" {
			return "."
		}
		return rel
	}

	fmt.Printf("Detected %d language root(s):\n", len(roots))
	for _, r := range roots {
		cfg, _ := GetLanguageConfig(r.Language)
		fmt.Printf("  %-12s %s\n", cfg.DisplayName, relLabel(r.Path))
	}

	// Collect unique languages and auto-install missing indexers.
	langSet := make(map[Language]bool)
	for _, r := range roots {
		langSet[r.Language] = true
	}
	langs := make([]Language, 0, len(langSet))
	for lang := range langSet {
		langs = append(langs, lang)
	}

	mgr := NewIndexerManager("")
	installed, failed := mgr.InstallAll(langs)
	if len(failed) > 0 {
		for lang, ferr := range failed {
			fmt.Printf("Warning: could not install indexer for %s: %v\n", lang, ferr)
		}
	}
	_ = installed

	// Index each root.
	var errs []error
	for _, r := range roots {
		if _, didFail := failed[r.Language]; didFail {
			fmt.Printf("Skipping %s (%s) — indexer unavailable\n", r.Language, relLabel(r.Path))
			errs = append(errs, fmt.Errorf("indexer unavailable for %s", r.Language))
			continue
		}

		cfg, _ := GetLanguageConfig(r.Language)
		rel := relLabel(r.Path)
		vprintf("\n=== Indexing %s at %s ===\n", cfg.DisplayName, rel)

		// Derive a unique service name: serviceName for the project root,
		// serviceName/rel-path for every sub-module.
		subServiceName := si.serviceName
		if rel != "." {
			subServiceName = si.serviceName + "/" + filepath.ToSlash(rel)
		}

		sub := NewSCIPIndexerWithLanguage(si.client, subServiceName, si.version, si.repoURL, r.Language)
		sub.SetScope(si.scopeCtx)
		sub.SetVerbose(si.verbose)
		if si.timer != nil {
			sub.SetBenchmarkTimer(si.timer)
		}
		// Propagate tri-store clients but skip per-root population;
		// we populate once after all roots are indexed.
		sub.SetEmbeddingService(si.embeddingService)
		sub.SetVectorStore(si.vectorStore)
		sub.SetTextStore(si.textStore)
		sub.skipSecondaryStores = true

		if err := sub.IndexProject(ctx, r.Path); err != nil {
			fmt.Printf("Warning: %s indexing at %s failed: %v\n", cfg.DisplayName, rel, err)
			// For Go, fall back to the AST indexer when scip-go can't resolve module
			// dependencies (e.g. private deps not in the local module cache).
			if r.Language == LanguageGo {
				fmt.Printf("Falling back to AST indexer for Go at %s...\n", rel)
				astIdx := NewStaticIndexer(si.client, subServiceName, si.version, si.repoURL)
				if astErr := astIdx.IndexProject(ctx, r.Path); astErr != nil {
					fmt.Printf("Warning: AST fallback also failed at %s: %v\n", rel, astErr)
					errs = append(errs, fmt.Errorf("%s@%s: scip: %w; ast: %v", r.Language, rel, err, astErr))
				} else {
					fmt.Printf("AST fallback succeeded for Go at %s\n", rel)
					// Don't count this root as failed — AST indexing worked.
				}
			} else {
				errs = append(errs, fmt.Errorf("%s@%s: %w", r.Language, rel, err))
			}
		}
	}

	// Populate secondary stores once after all roots are indexed.
	if si.embeddingService != nil && si.vectorStore != nil {
		t := time.Now()
		vprintln("\nPopulating secondary stores (Qdrant + OpenSearch)...")
		si.ensureSecondaryStoreIndexes(ctx)
		si.populateSecondaryStores(ctx)
		fmt.Printf(" ▸ %-16s %s\n", "Secondary stores", fmtDur(time.Since(t)))
	}

	if len(errs) == len(roots) {
		return fmt.Errorf("all language roots failed to index: %v", errs)
	}

	// Generate and store one-line summaries for all Function/Method nodes that
	// don't have one yet.  Two round-trips regardless of service size.
	t := time.Now()
	if n, err := GenerateAndStoreNodeSummaries(ctx, si.client, si.scopeCtx, si.serviceName); err != nil {
		fmt.Printf(" ▸ %-16s failed: %v\n", "Node summaries", err)
	} else {
		fmt.Printf(" ▸ %-16s %d nodes  %s\n", "Node summaries", n, fmtDur(time.Since(t)))
	}

	return nil
}

// generateSCIPIndex runs the appropriate SCIP indexer to generate a SCIP index file
func (si *SCIPIndexer) generateSCIPIndex(projectPath string) (string, error) {
	// Resolve the SCIP binary: check IndexerManager cache first, then system PATH.
	mgr := NewIndexerManager("")
	resolvedBinary := mgr.ResolveBinary(si.language)
	if resolvedBinary != "" {
		si.langConfig.SCIPBinary = resolvedBinary
	} else if _, err := exec.LookPath(si.langConfig.SCIPBinary); err != nil {
		return "", fmt.Errorf("%s not found in PATH or cache.\nInstall with: codegraph indexers install --language %s\nOr manually: %s\nSee: %s",
			si.langConfig.SCIPBinary,
			si.language,
			si.langConfig.InstallCommand,
			si.langConfig.InstallDocs)
	}

	// Get absolute path for project
	absPath, err := filepath.Abs(projectPath)
	if err != nil {
		return "", fmt.Errorf("failed to get absolute path: %w", err)
	}

	// Create temporary output file with absolute path
	outputFile := filepath.Join(absPath, "index.scip")

	// Prepare language-specific command
	var cmd *exec.Cmd
	switch si.language {
	case LanguageGo:
		// scip-go --module-name <name> --module-version <version> --output <file>
		// Use the actual module path from go.mod so scip-go can resolve versions correctly.
		moduleName := readGoModuleName(absPath)
		if moduleName == "" {
			moduleName = si.serviceName
		}
		// Warm the module cache so scip-go can resolve all dependency versions.
		// Ignore errors: if a private dep can't be downloaded the cache may be
		// partially warm enough, and scip-go will emit the real error if not.
		dlCmd := exec.Command("go", "mod", "download")
		dlCmd.Dir = absPath
		if out, err := dlCmd.CombinedOutput(); err != nil {
			fmt.Printf("Warning: go mod download failed (continuing): %v\n%s\n", err, strings.TrimSpace(string(out)))
		}
		cmd = exec.Command(si.langConfig.SCIPBinary,
			"--module-name", moduleName,
			"--module-version", si.version,
			"--output", outputFile,
		)
	case LanguageTypeScript, LanguageJavaScript:
		// scip-typescript index --output <file>
		args := append(si.langConfig.IndexFlags, "--output", outputFile)

		// Detect workspace type and add appropriate flags
		workspaceType := si.detectWorkspaceType(absPath)
		switch workspaceType {
		case "pnpm":
			args = append(args, "--pnpm-workspaces")
			fmt.Println("Detected pnpm workspace, using --pnpm-workspaces")
		case "yarn":
			args = append(args, "--yarn-workspaces")
			fmt.Println("Detected yarn workspace, using --yarn-workspaces")
		}

		// If no tsconfig.json at root, use --infer-tsconfig
		if _, err := os.Stat(filepath.Join(absPath, "tsconfig.json")); os.IsNotExist(err) {
			args = append(args, "--infer-tsconfig")
			fmt.Println("No root tsconfig.json found, using --infer-tsconfig")
		}

		cmd = exec.Command(si.langConfig.SCIPBinary, args...)
	case LanguagePython:
		// scip-python index --project-name <name> --output <file>
		args := append(si.langConfig.IndexFlags,
			"--project-name", si.serviceName,
			"--output", outputFile,
		)
		cmd = exec.Command(si.langConfig.SCIPBinary, args...)
		// If a venv exists, prepend its bin/ to PATH so scip-python can find pip/python.
		if venvPath := detectPythonVenv(absPath); venvPath != "" {
			fmt.Printf("Detected Python venv at %s\n", venvPath)
			venvBin := filepath.Join(venvPath, "bin")
			env := os.Environ()
			for i, e := range env {
				if strings.HasPrefix(e, "PATH=") {
					env[i] = "PATH=" + venvBin + ":" + e[5:]
					break
				}
			}
			cmd.Env = env
		}
	case LanguagePHP:
		// scip-php generates index.scip in current directory
		// We need to specify the source directory
		cmd = exec.Command(si.langConfig.SCIPBinary, "src", "--output", outputFile)
	case LanguageJava, LanguageScala, LanguageKotlin:
		// scip-java index
		// scip-java runs the build tool (Maven/Gradle/sbt) and generates index.scip
		// in the current directory
		// Use sh -c to handle shell scripts
		cmd = exec.Command("sh", "-c", si.langConfig.SCIPBinary+" index")
	default:
		return "", fmt.Errorf("unsupported language for SCIP indexing: %s", si.language)
	}

	// Set working directory to absolute path
	cmd.Dir = absPath

	// Run the command
	vprintf("Running: %s in %s\n", cmd.String(), absPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s command failed: %w\nOutput: %s", si.langConfig.SCIPBinary, err, string(output))
	}

	vprintf("%s output: %s\n", si.langConfig.SCIPBinary, string(output))

	// Verify the output file exists
	if _, err := os.Stat(outputFile); os.IsNotExist(err) {
		return "", fmt.Errorf("SCIP index file was not generated: %s", outputFile)
	}

	return outputFile, nil
}

// detectPythonVenv returns the path to a Python virtual environment inside
// projectPath if one exists. It checks the common venv directory names and
// confirms by looking for a pyvenv.cfg file or python/pip binaries.
func detectPythonVenv(projectPath string) string {
	candidates := []string{".venv", "venv", ".env", "env"}
	for _, dir := range candidates {
		venvPath := filepath.Join(projectPath, dir)
		info, err := os.Stat(venvPath)
		if err != nil || !info.IsDir() {
			continue
		}
		// pyvenv.cfg is the most reliable marker for a Python venv.
		if _, err := os.Stat(filepath.Join(venvPath, "pyvenv.cfg")); err == nil {
			return venvPath
		}
		// Fallback: check for any python or pip binary.
		for _, bin := range []string{"bin/python", "bin/python3", "bin/pip", "bin/pip3"} {
			if _, err := os.Stat(filepath.Join(venvPath, bin)); err == nil {
				return venvPath
			}
		}
	}
	return ""
}

// detectWorkspaceType detects if the project uses a workspace manager (pnpm, yarn, npm)
func (si *SCIPIndexer) detectWorkspaceType(projectPath string) string {
	// Check for pnpm-workspace.yaml
	if _, err := os.Stat(filepath.Join(projectPath, "pnpm-workspace.yaml")); err == nil {
		return "pnpm"
	}

	// Check for yarn workspaces in package.json
	packageJSONPath := filepath.Join(projectPath, "package.json")
	if data, err := os.ReadFile(packageJSONPath); err == nil {
		var packageData struct {
			Workspaces interface{} `json:"workspaces"`
		}
		if err := json.Unmarshal(data, &packageData); err == nil {
			if packageData.Workspaces != nil {
				// Check for yarn.lock to distinguish yarn from npm
				if _, err := os.Stat(filepath.Join(projectPath, "yarn.lock")); err == nil {
					return "yarn"
				}
			}
		}
	}

	return ""
}

// createServiceNode creates the service node in Neo4j
func (si *SCIPIndexer) createServiceNode(ctx context.Context, projectPath string) (string, error) {
	// Try to extract actual package name from package.json for TypeScript/JavaScript
	actualPackageName := si.serviceName
	if si.language == LanguageTypeScript || si.language == LanguageJavaScript {
		if npmName := si.extractNPMPackageName(projectPath); npmName != "" {
			actualPackageName = npmName
			fmt.Printf("Detected NPM package name: %s\n", npmName)
		}
	}

	nodeKey := models.ServiceNodeKey(si.serviceName)
	serviceProps := map[string]any{
		"name":          si.serviceName,
		"nodeKey":       nodeKey,
		"packageName":   actualPackageName,
		"language":      si.langConfig.DisplayName,
		"version":       si.version,
		"repositoryUrl": si.repoURL,
		"scope":         si.scopeCtx.Scope,
		"scopeId":       si.scopeCtx.ScopeID,
	}

	return si.client.MergeNode(ctx, []string{"Service"},
		map[string]any{"nodeKey": nodeKey, "scopeId": si.scopeCtx.ScopeID}, serviceProps)
}

// extractNPMPackageName reads package.json and extracts the package name
func (si *SCIPIndexer) extractNPMPackageName(projectPath string) string {
	packageJSONPath := filepath.Join(projectPath, "package.json")
	data, err := os.ReadFile(packageJSONPath)
	if err != nil {
		return ""
	}

	var packageData struct {
		Name string `json:"name"`
	}

	if err := json.Unmarshal(data, &packageData); err != nil {
		return ""
	}

	return packageData.Name
}

// createFileNode creates a file node in Neo4j
func (si *SCIPIndexer) createFileNode(ctx context.Context, file *models.File, serviceID string) (string, error) {
	nodeKey := models.FileNodeKey(si.serviceName, file.Path)
	fileProps := map[string]any{
		"path":         file.Path,
		"nodeKey":      nodeKey,
		"absolutePath": file.Path,
		"language":     file.Language,
		"hash":         "",
		"lineCount":    0,
		"scope":        si.scopeCtx.Scope,
		"scopeId":      si.scopeCtx.ScopeID,
	}

	fileID, err := si.client.MergeNode(ctx, []string{"File"},
		map[string]any{"nodeKey": nodeKey, "scopeId": si.scopeCtx.ScopeID}, fileProps)
	if err != nil {
		return "", err
	}

	// Link file to service. MergeRelationship (not CreateRelationship) so a reindex
	// over a live DB reuses the existing CONTAINS edge instead of stacking a duplicate.
	_, err = si.client.MergeRelationship(ctx, serviceID, fileID, "CONTAINS",
		nil, map[string]any{"scope": si.scopeCtx.Scope, "scopeId": si.scopeCtx.ScopeID})
	return fileID, err
}

// computeDefinitionProps computes the label, nodeKey, and properties for a definition node
// without touching Neo4j. This is the pure-computation half of createDefinitionNode.
func (si *SCIPIndexer) computeDefinitionProps(symbolInfo *models.SymbolInfo) (label string, nodeKey string, props map[string]any) {
	switch symbolInfo.Kind {
	case models.FunctionSymbol:
		label = "Function"
	case models.MethodSymbol:
		label = "Method"
	case models.TypeSymbol:
		label = "Class"
	case models.InterfaceSymbol:
		label = "Interface"
	case models.VariableSymbol:
		label = "Variable"
	case models.ConstantSymbol:
		label = "Variable"
	case models.ParameterSymbol:
		label = "Parameter"
	case models.FieldSymbol:
		label = "Variable"
	case models.PackageSymbol:
		label = "Module"
	default:
		label = "Variable"
	}

	switch label {
	case "Function":
		nodeKey = models.FunctionNodeKey(si.serviceName, symbolInfo.FilePath, symbolInfo.Signature)
	case "Method":
		nodeKey = models.MethodNodeKey(si.serviceName, symbolInfo.FilePath, symbolInfo.Signature)
	case "Class":
		nodeKey = models.ClassNodeKey(si.serviceName, symbolInfo.Symbol.String(), symbolInfo.FilePath, symbolInfo.DisplayName)
	case "Interface":
		nodeKey = models.InterfaceNodeKey(si.serviceName, symbolInfo.Symbol.String(), symbolInfo.FilePath, symbolInfo.DisplayName)
	case "Variable":
		nodeKey = models.VariableNodeKey(si.serviceName, symbolInfo.FilePath, symbolInfo.DisplayName, symbolInfo.StartLine)
	case "Parameter":
		nodeKey = models.ParameterNodeKey(si.serviceName, symbolInfo.FilePath, symbolInfo.Signature, symbolInfo.DisplayName, 0)
	case "Module":
		nodeKey = models.ModuleNodeKey(symbolInfo.Symbol.String())
	default:
		nodeKey = models.VariableNodeKey(si.serviceName, symbolInfo.FilePath, symbolInfo.DisplayName, symbolInfo.StartLine)
	}

	// Canonicalize the name at ingest. SCIP display names for functions and
	// methods carry a descriptor suffix (e.g. "EditSettlement()."). Normalizing
	// it once here makes `name` the single canonical identifier, so exact-match
	// queries (analyze_function, rpc_anatomy, IMPLEMENTED_BY linking) resolve
	// without a per-query replace(name,'().','') workaround. normalizeIdent is a
	// no-op on already-clean names, so it is safe for non-function labels too.
	nodeName := symbolInfo.DisplayName
	if label == "Function" || label == "Method" {
		nodeName = normalizeIdent(symbolInfo.DisplayName)
	}

	props = map[string]any{
		"name":             nodeName,
		"nodeKey":          nodeKey,
		"signature":        symbolInfo.Signature,
		"filePath":         symbolInfo.FilePath,
		"absoluteFilePath": si.absoluteFilePath(symbolInfo.FilePath),
		"startLine":        symbolInfo.StartLine,
		"endLine":          symbolInfo.EndLine,
		"startColumn":      symbolInfo.StartColumn,
		"endColumn":        symbolInfo.EndColumn,
		"scope":            si.scopeCtx.Scope,
		"scopeId":          si.scopeCtx.ScopeID,
	}

	if label == "Function" || label == "Method" {
		if symbolInfo.EndLine > symbolInfo.StartLine {
			props["linesOfCode"] = symbolInfo.EndLine - symbolInfo.StartLine + 1
		} else {
			props["linesOfCode"] = 1
		}
		if symbolInfo.FilePath != "" {
			startByte, endByte := si.calculateByteOffsets(symbolInfo.FilePath,
				symbolInfo.StartLine, symbolInfo.StartColumn,
				symbolInfo.EndLine, symbolInfo.EndColumn)
			if startByte >= 0 && endByte >= 0 {
				props["startByte"] = startByte
				props["endByte"] = endByte
			}
		}
	}

	switch label {
	case "Function", "Method":
		props["returnType"] = ""
		props["isExported"] = true
		props["complexity"] = 1
		props["docstring"] = symbolInfo.Documentation
		props["goSignature"] = symbolInfo.GoSignature
		props["isTestFunction"] = isTestFunction(symbolInfo.DisplayName, symbolInfo.FilePath)
	case "Class":
		props["fqn"] = symbolInfo.Symbol.String()
		props["accessModifier"] = "public"
		props["isAbstract"] = false
		props["docstring"] = symbolInfo.Documentation
	case "Variable":
		props["type"] = ""
		props["isConstant"] = symbolInfo.Kind == models.ConstantSymbol
	}

	return label, nodeKey, props
}

const batchSize = 500

// noiseNodeLabels lists node labels that must never be written by the
// call-graph indexing pipeline. These types belong to other subsystems
// (document indexer, generated-context) and inflate write volume without
// contributing to any RPC context query.
//
// TEMPORARILY suppressed during testing to reduce graph noise:
//   - "Reference": creates one node per call-site occurrence, inflating the
//     graph with ~50K+ nodes that add no signal to cross-service RPC queries.
//   - "Class": Go struct/type definitions are not needed for RPC traversal;
//     suppressed until we decide how to surface them cleanly.
var noiseNodeLabels = map[string]bool{
	"Document":             true,
	"DocumentChunk":        true,
	"Feature":              true,
	"PullRequest":          true,
	"GeneratedDoc":         true,
	"GenerationDiagnostic": true,
	"Reference":            true, // suppressed: per-call-site nodes inflate graph during testing
	"Class":                true, // suppressed: Go type nodes not needed for RPC traversal during testing
}

// noiseRelTypes lists relationship types that must never be written by the
// call-graph indexing pipeline. INHERITS_FROM and SCHEDULED_BY are not in any
// RPC traversal path and contribute no signal to cross-service context.
// NOTE: ReferencesRel is intentionally NOT suppressed here — REFERENCES edges
// are required by SCIPCallGraphBuilder and SCIPRPCDetector to traverse from
// Reference nodes to their target Symbol nodes. The ~50K volume concern is
// mitigated by the isFunctionReferenceKind filter applied before this point,
// which restricts refs to function/method call occurrences only.
var noiseRelTypes = map[models.RelationshipType]bool{
	models.InheritsFromRel: true,
	models.ScheduledByRel:  true,
}

// isNoiseRelType returns true if rt must not be written by the call-graph pipeline.
func isNoiseRelType(rt models.RelationshipType) bool {
	return noiseRelTypes[rt]
}

// dedupeByNodeKey keeps only the last item for each nodeKey, preventing
// UNWIND MERGE race conditions when duplicate keys appear in the same batch.
func dedupeByNodeKey(items []map[string]any) []map[string]any {
	seen := make(map[string]int, len(items))
	for i, item := range items {
		nk, _ := item["nodeKey"].(string)
		seen[nk] = i // last-write wins
	}
	if len(seen) == len(items) {
		return items // no duplicates
	}
	deduped := make([]map[string]any, 0, len(seen))
	for _, idx := range seen {
		deduped = append(deduped, items[idx])
	}
	return deduped
}

// symbolIndex holds the Neo4j node ID maps produced by indexSymbols,
// needed by subsequent pipeline stages (e.g. IMPLEMENTS edge creation).
type symbolIndex struct {
	symbolIDs      map[string]string // symbolNodeKey → elementId
	defIDs         map[string]string // defNodeKey → elementId
	symbolToDefKey map[string]string // SCIP symbol string → defNodeKey
}

// indexSymbols indexes all symbols and their relationships using UNWIND batches.
func (si *SCIPIndexer) indexSymbols(ctx context.Context, symbolDefs []*models.SymbolDefinition, fileNodes map[string]string) (*symbolIndex, error) {
	fmt.Printf("Indexing %d symbols...\n", len(symbolDefs))

	// Initialize file content cache for byte offset calculations
	si.fileContentCache = make(map[string][]byte)
	defer func() { si.fileContentCache = nil }()

	// ── Phase 6: definitions ──────────────────────────────────────────
	if si.timer != nil {
		si.timer.Start("Index symbols (defs)")
	}

	// Pre-compute all items
	type defItem struct {
		symbolNodeKey string
		symbolProps   map[string]any
		defLabel      string
		defNodeKey    string
		defProps      map[string]any
		filePath      string
	}
	items := make([]defItem, 0, len(symbolDefs))

	for _, sd := range symbolDefs {
		info := sd.Info
		symNodeKey := models.SymbolNodeKey(info.Symbol.String())
		symProps := map[string]any{
			"symbol":        info.Symbol.String(),
			"nodeKey":       symNodeKey,
			"kind":          string(info.Kind),
			"displayName":   info.DisplayName,
			"documentation": info.Documentation,
			"scope":         si.scopeCtx.Scope,
			"scopeId":       si.scopeCtx.ScopeID,
		}

		di := defItem{
			symbolNodeKey: symNodeKey,
			symbolProps:   symProps,
		}

		if info.FilePath != "" && sd.GetDefinitionReference() != nil {
			lbl, nk, props := si.computeDefinitionProps(info)
			di.defLabel = lbl
			di.defNodeKey = nk
			di.defProps = props
			di.filePath = info.FilePath
		}

		items = append(items, di)
	}

	// Build symbolToDefKey map for IMPLEMENTS edge resolution
	symToDefKey := make(map[string]string, len(items))
	for _, it := range items {
		if it.defNodeKey != "" {
			// symbolNodeKey == SCIP symbol string (via SymbolNodeKey)
			symToDefKey[it.symbolNodeKey] = it.defNodeKey
		}
	}

	// 1. Batch merge all Symbol nodes (deduplicated)
	symbolBatch := make([]map[string]any, len(items))
	for i, it := range items {
		symbolBatch[i] = map[string]any{
			"nodeKey": it.symbolNodeKey,
			"scopeId": si.scopeCtx.ScopeID,
			"props":   it.symbolProps,
		}
	}
	symbolBatch = dedupeByNodeKey(symbolBatch)

	t := time.Now()
	symbolIDs, err := si.client.MergeNodesBatch(ctx, "Symbol", symbolBatch, batchSize)
	tMergeSymbol := time.Since(t)
	if err != nil {
		fmt.Printf("Warning: batch merge Symbol nodes failed: %v\n", err)
		symbolIDs = make(map[string]string)
	}
	fmt.Printf("Merged %d Symbol nodes\n", len(symbolIDs))

	// 2. Group definitions by label, deduplicate, batch merge each group
	labelGroups := make(map[string][]map[string]any)
	for _, it := range items {
		if it.defLabel == "" {
			continue
		}
		labelGroups[it.defLabel] = append(labelGroups[it.defLabel], map[string]any{
			"nodeKey": it.defNodeKey,
			"scopeId": si.scopeCtx.ScopeID,
			"props":   it.defProps,
		})
	}

	defIDs := make(map[string]string) // defNodeKey → elementId
	t = time.Now()
	for lbl, batch := range labelGroups {
		if noiseNodeLabels[lbl] {
			continue
		}
		batch = dedupeByNodeKey(batch)
		ids, err := si.client.MergeNodesBatch(ctx, lbl, batch, batchSize)
		if err != nil {
			fmt.Printf("Warning: batch merge %s nodes failed: %v\n", lbl, err)
			continue
		}
		for nk, id := range ids {
			defIDs[nk] = id
		}
	}
	tMergeDefinition := time.Since(t)
	fmt.Printf("Merged %d definition nodes across %d labels\n", len(defIDs), len(labelGroups))

	// 3. Batch create DEFINES rels (defID → symbolID)
	var definesRels []map[string]any
	for _, it := range items {
		if it.defLabel == "" {
			continue
		}
		fromID, ok1 := defIDs[it.defNodeKey]
		toID, ok2 := symbolIDs[it.symbolNodeKey]
		if ok1 && ok2 {
			definesRels = append(definesRels, map[string]any{
				"fromId": fromID,
				"toId":   toID,
				"props":  map[string]any{"isExported": true, "scope": si.scopeCtx.Scope, "scopeId": si.scopeCtx.ScopeID},
			})
		}
	}

	t = time.Now()
	if err := si.client.CreateRelsBatch(ctx, "DEFINES", definesRels, batchSize); err != nil {
		fmt.Printf("Warning: batch create DEFINES rels failed: %v\n", err)
	}
	tRelDefines := time.Since(t)

	// 4. Batch create CONTAINS rels (fileID → defID)
	var containsRels []map[string]any
	for _, it := range items {
		if it.defLabel == "" {
			continue
		}
		defID, ok1 := defIDs[it.defNodeKey]
		fileID, ok2 := fileNodes[it.filePath]
		if ok1 && ok2 {
			containsRels = append(containsRels, map[string]any{
				"fromId": fileID,
				"toId":   defID,
				"props":  map[string]any{"scope": si.scopeCtx.Scope, "scopeId": si.scopeCtx.ScopeID},
			})
		}
	}

	t = time.Now()
	if err := si.client.CreateRelsBatch(ctx, "CONTAINS", containsRels, batchSize); err != nil {
		fmt.Printf("Warning: batch create def CONTAINS rels failed: %v\n", err)
	}
	tRelContains := time.Since(t)

	if si.timer != nil {
		si.timer.Stop(len(symbolDefs), fmt.Sprintf("%d symbols", len(symbolDefs)))
		if recorder, ok := si.timer.(SubPhaseRecorder); ok {
			recorder.AddResult("  MergeNodesBatch(Symbol)", tMergeSymbol, len(symbolIDs), "")
			recorder.AddResult("  MergeNodesBatch(Definition)", tMergeDefinition, len(defIDs), "")
			recorder.AddResult("  CreateRelsBatch(DEFINES)", tRelDefines, len(definesRels), "")
			recorder.AddResult("  CreateRelsBatch(CONTAINS)", tRelContains, len(containsRels), "")
		}
	}

	// ── Phase 7: references ──────────────────────────────────────────
	if si.timer != nil {
		si.timer.Start("Index symbols (refs)")
	}
	fmt.Printf("\nCreating reference relationships...\n")

	// Pre-compute all reference items
	type refItem struct {
		nodeKey       string
		props         map[string]any
		symbolNodeKey string
		filePath      string
		refRelProps   map[string]any
	}
	var refItems []refItem

	for _, sd := range symbolDefs {
		if sd.Info == nil || !isFunctionReferenceKind(sd.Info.Kind) {
			continue
		}
		symNK := models.SymbolNodeKey(sd.Info.Symbol.String())
		if _, exists := symbolIDs[symNK]; !exists {
			continue
		}
		for _, ref := range sd.Refs {
			if ref.IsDefinition {
				continue
			}
			nk := models.ReferenceNodeKey(si.serviceName, ref.FilePath, ref.StartLine, ref.StartColumn)
			refItems = append(refItems, refItem{
				nodeKey: nk,
				props: map[string]any{
					"filePath":    ref.FilePath,
					"nodeKey":     nk,
					"startLine":   ref.StartLine,
					"endLine":     ref.EndLine,
					"startColumn": ref.StartColumn,
					"endColumn":   ref.EndColumn,
					"context":     ref.Context,
					"scope":       si.scopeCtx.Scope,
					"scopeId":     si.scopeCtx.ScopeID,
				},
				symbolNodeKey: symNK,
				filePath:      ref.FilePath,
				refRelProps: map[string]any{
					"isDefinition": ref.IsDefinition,
					"line":         ref.StartLine,
					"column":       ref.StartColumn,
					"scope":        si.scopeCtx.Scope,
					"scopeId":      si.scopeCtx.ScopeID,
				},
			})
		}
	}
	fmt.Printf("  Built %d reference items from %d symbols\n", len(refItems), len(symbolDefs))

	// Reference node creation is suppressed while "Reference" is in noiseNodeLabels
	// (reduces graph noise during testing — re-enable by removing "Reference" from that map).
	var (
		refIDs          map[string]string
		referencesRels  []map[string]any
		refContainsRels []map[string]any
		tMergeRef       time.Duration
		tRelReferences  time.Duration
		tRelRefContains time.Duration
	)
	if !noiseNodeLabels["Reference"] {
		// 1. Batch merge all Reference nodes (deduplicated)
		refBatch := make([]map[string]any, len(refItems))
		for i, ri := range refItems {
			refBatch[i] = map[string]any{
				"nodeKey": ri.nodeKey,
				"scopeId": si.scopeCtx.ScopeID,
				"props":   ri.props,
			}
		}
		refBatch = dedupeByNodeKey(refBatch)
		fmt.Printf("  After dedup: %d unique Reference nodes to merge (batchSize=%d, batches=%d)\n",
			len(refBatch), batchSize, (len(refBatch)+batchSize-1)/batchSize)

		t = time.Now()
		refIDs, err = si.client.MergeNodesBatch(ctx, "Reference", refBatch, batchSize)
		tMergeRef = time.Since(t)
		if err != nil {
			fmt.Printf("Warning: batch merge Reference nodes failed: %v\n", err)
			refIDs = make(map[string]string)
		}
		fmt.Printf("  Merged %d Reference nodes in %s\n", len(refIDs), tMergeRef)

		// 2. Batch create REFERENCES rels (refID → symbolID)
		for _, ri := range refItems {
			fromID, ok1 := refIDs[ri.nodeKey]
			toID, ok2 := symbolIDs[ri.symbolNodeKey]
			if ok1 && ok2 {
				referencesRels = append(referencesRels, map[string]any{
					"fromId": fromID,
					"toId":   toID,
					"props":  ri.refRelProps,
				})
			}
		}
		if !isNoiseRelType(models.ReferencesRel) {
			fmt.Printf("  Creating %d REFERENCES rels (%d batches)...\n",
				len(referencesRels), (len(referencesRels)+batchSize-1)/batchSize)
			t = time.Now()
			if err := si.client.CreateRelsBatch(ctx, "REFERENCES", referencesRels, batchSize); err != nil {
				fmt.Printf("Warning: batch create REFERENCES rels failed: %v\n", err)
			}
			tRelReferences = time.Since(t)
			fmt.Printf("  Created REFERENCES rels in %s\n", tRelReferences)
		}

		// 3. Batch create CONTAINS rels (fileID → refID)
		for _, ri := range refItems {
			refID, ok1 := refIDs[ri.nodeKey]
			fileID, ok2 := fileNodes[ri.filePath]
			if ok1 && ok2 {
				refContainsRels = append(refContainsRels, map[string]any{
					"fromId": fileID,
					"toId":   refID,
					"props":  map[string]any{"scope": si.scopeCtx.Scope, "scopeId": si.scopeCtx.ScopeID},
				})
			}
		}
		fmt.Printf("  Creating %d CONTAINS rels for refs (%d batches)...\n",
			len(refContainsRels), (len(refContainsRels)+batchSize-1)/batchSize)

		t = time.Now()
		if err := si.client.CreateRelsBatch(ctx, "CONTAINS", refContainsRels, batchSize); err != nil {
			fmt.Printf("Warning: batch create ref CONTAINS rels failed: %v\n", err)
		}
		tRelRefContains = time.Since(t)
		fmt.Printf("  Created CONTAINS rels for refs in %s\n", tRelRefContains)
	} else {
		fmt.Printf("  Skipping Reference node creation (suppressed via noiseNodeLabels — %d items not written)\n", len(refItems))
		refIDs = make(map[string]string)
	}

	if si.timer != nil {
		si.timer.Stop(len(refItems), fmt.Sprintf("%d refs", len(refItems)))
		if recorder, ok := si.timer.(SubPhaseRecorder); ok {
			recorder.AddResult("  MergeNodesBatch(Reference)", tMergeRef, len(refIDs), "")
			recorder.AddResult("  CreateRelsBatch(REFERENCES)", tRelReferences, len(referencesRels), "")
			recorder.AddResult("  CreateRelsBatch(CONTAINS)", tRelRefContains, len(refContainsRels), "")
		}
	}

	fmt.Printf("Completed indexing symbols (created %d reference relationships)\n", len(refItems))
	return &symbolIndex{
		symbolIDs:      symbolIDs,
		defIDs:         defIDs,
		symbolToDefKey: symToDefKey,
	}, nil
}

// indexPackageDependencies creates DEPENDS_ON relationships between services based
// on imports. It returns (created, skipped): created DEPENDS_ON edges to other
// indexed services, and external/stdlib packages that resolved to no service.
func (si *SCIPIndexer) indexPackageDependencies(ctx context.Context, imports []*models.PackageImport, serviceID string) (created, skipped int, err error) {
	if len(imports) == 0 {
		return 0, 0, nil
	}

	vprintf("Processing %d imports for dependency relationships...\n", len(imports))

	// Group imports by target package
	packageMap := make(map[string]int)
	for _, imp := range imports {
		if imp.IsExternal {
			packageMap[imp.TargetPackage]++
		}
	}

	createdCount := 0
	skippedCount := 0

	// Create DEPENDS_ON relationships for each external package
	for packageName, count := range packageMap {
		// Try to find the target service by package name or service name
		// Multiple matching strategies:
		// 1. Exact packageName match
		// 2. Service name in package (e.g., "@try-veil/veil-gateway" matches "veil-gateway")
		// 3. Package contains service name
		// A shared proto-contract repo (owns .proto contracts, implements no handler)
		// is excluded as a dependency target (F1): importing a service's proto package
		// is a dependency on that SERVICE, not on the contract repo that declares it.
		// Without this guard every proto import resolves DEPENDS_ON → "proto".
		targetServiceQuery := `
			MATCH (s:Service)
			WHERE (s.packageName = $packageName
			   OR s.name = $packageName
			   OR $packageName CONTAINS s.packageName
			   OR s.packageName CONTAINS $packageName)
			  AND NOT (
			    EXISTS { (:ProtoContract)-[:BELONGS_TO]->(s) }
			    AND NOT EXISTS {
			      MATCH (s)-[:CONTAINS*1..8]->(h)
			      WHERE (h:Function OR h:Method) AND h.receiverType ENDS WITH 'Server'
			    }
			  )
			RETURN elementId(s) AS id, s.name AS name, s.packageName AS packageName
			ORDER BY
				CASE
					WHEN s.packageName = $packageName THEN 0
					WHEN s.name = $packageName THEN 1
					ELSE 2
				END
			LIMIT 1
		`

		result, err := si.client.ExecuteQuery(ctx, targetServiceQuery,
			map[string]any{"packageName": packageName})

		if err != nil || len(result) == 0 {
			// Target service not indexed yet (external dep or stdlib) — skip.
			vprintf("  No service found for package: %s\n", packageName)
			skippedCount++
			continue
		}

		targetServiceID := result[0].AsMap()["id"].(string)
		targetServiceName := result[0].AsMap()["name"].(string)

		// Avoid self-dependencies
		if targetServiceID == serviceID {
			continue
		}

		// Create DEPENDS_ON relationship
		relProps := map[string]any{
			"packageName":  packageName,
			"isDirect":     true,
			"importCount":  count,
			"detectedFrom": "import-analysis",
			"scope":        si.scopeCtx.Scope,
			"scopeId":      si.scopeCtx.ScopeID,
		}

		// MergeRelationship (not CreateRelationship): a reindex over a live DB must
		// reuse the existing DEPENDS_ON edge, not stack a duplicate per run.
		_, err = si.client.MergeRelationship(ctx, serviceID, targetServiceID,
			string(models.DependsOnRel), nil, relProps)

		if err != nil {
			si.report.warn("failed to create DEPENDS_ON relationship to %s: %v", targetServiceName, err)
		} else {
			vprintf("Created DEPENDS_ON: %s -> %s (%d imports)\n", si.serviceName, targetServiceName, count)
			createdCount++
		}
	}

	vprintf("Created %d DEPENDS_ON relationships\n", createdCount)
	return createdCount, skippedCount, nil
}

// SetSCIPBinary sets the path to the SCIP binary (for testing or custom installations)
func (si *SCIPIndexer) SetSCIPBinary(binary string) {
	si.langConfig.SCIPBinary = binary
}

// ValidateEnvironment checks if the required tools are available.
// It first tries to resolve the binary from cache or PATH, then attempts
// auto-install if not found. Set noAutoInstall to skip the install attempt.
func (si *SCIPIndexer) ValidateEnvironment() error {
	return si.validateEnvironment(false)
}

// ValidateEnvironmentNoInstall checks if the required tools are available
// without attempting auto-install.
func (si *SCIPIndexer) ValidateEnvironmentNoInstall() error {
	return si.validateEnvironment(true)
}

func (si *SCIPIndexer) validateEnvironment(noAutoInstall bool) error {
	mgr := NewIndexerManager("")
	if resolved := mgr.ResolveBinary(si.language); resolved != "" {
		si.langConfig.SCIPBinary = resolved
		return nil
	}

	if noAutoInstall {
		return fmt.Errorf("%s not found in PATH or cache.\nInstall with: %s\nSee: %s",
			si.langConfig.SCIPBinary,
			si.langConfig.InstallCommand,
			si.langConfig.InstallDocs)
	}

	// Auto-install attempt
	fmt.Printf("SCIP indexer %q not found, attempting auto-install...\n", si.langConfig.SCIPBinary)
	if err := mgr.Install(si.language); err != nil {
		return fmt.Errorf("auto-install failed: %w\nManual install: %s", err, si.langConfig.InstallCommand)
	}
	if resolved := mgr.ResolveBinary(si.language); resolved != "" {
		si.langConfig.SCIPBinary = resolved
		return nil
	}
	return fmt.Errorf("%s not available after install attempt", si.langConfig.SCIPBinary)
}

// SetScope sets the scope context for the indexer.
func (si *SCIPIndexer) SetScope(scope models.ScopeContext) {
	si.scopeCtx = scope
}

// SetBenchmarkTimer sets an optional phase timer for benchmarking.
// When set, each phase of IndexProject() will be timed.
func (si *SCIPIndexer) SetBenchmarkTimer(timer PipelineTimer) {
	si.timer = timer
}

// SetEmbeddingService sets the embedding service for vector generation.
func (si *SCIPIndexer) SetEmbeddingService(svc search.EmbeddingService) {
	si.embeddingService = svc
}

// SetVectorStore sets the vector store for embedding storage.
func (si *SCIPIndexer) SetVectorStore(store search.VectorStore) {
	si.vectorStore = store
}

// SetTextStore sets the text index store for BM25 search.
func (si *SCIPIndexer) SetTextStore(store textindex.TextIndexStore) {
	si.textStore = store
}

// GetLanguage returns the language this indexer is configured for
func (si *SCIPIndexer) GetLanguage() Language {
	return si.language
}

// runASTRPCDetection walks all Go source files in projectPath, parses each
// function's AST, and runs the AST-based RPC detector. This requires Function
// nodes to already exist in Neo4j (created during symbol indexing) but does NOT
// require Reference nodes — making it fast and timeout-proof.
// runASTRPCDetection walks the Go AST detecting outbound RPC/DB/HTTP/event/cache
// call sites and returns the run's coverage IndexStats (may be non-nil even on
// error, e.g. a flush failure, so callers can still surface counts).
func (si *SCIPIndexer) runASTRPCDetection(ctx context.Context, projectPath string) (*IndexStats, error) {
	// Batch-load all Function/Method node IDs for this service into memory.
	funcIDs, err := si.loadFunctionIDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load function IDs: %w", err)
	}
	vprintf("  Loaded %d function/method IDs from Neo4j\n", len(funcIDs))

	svcIdx, idxErr := loadServiceIndex(ctx, si.client, si.scopeCtx.ScopeID)
	if idxErr != nil {
		si.report.warn("failed to preload service index for AST detector: %v", idxErr)
	}

	callBuffer := newCallNodeBuffer(si.scopeCtx.ScopeID)
	rpcDet := NewRPCCallDetector(si.client, si.serviceName, si.scopeCtx)
	rpcDet.SetCallNodeBuffer(callBuffer)
	rpcDet.SetServiceIndex(svcIdx)

	// P0-5 — authoritative getter→service table from the caller's own grpcclient
	// package. Overrides fuzzy getter-name derivation (GetPaymentService → payin).
	getterMap, getterErr := ScanGRPCClientGetters(projectPath)
	if getterErr != nil {
		si.report.warn("grpcclient getter scan failed: %v", getterErr)
		getterMap = GetterServiceMap{}
	}
	vprintf("  grpcclient getter pre-scan: %d getters mapped\n", len(getterMap))
	rpcDet.SetGetterServiceMap(getterMap)

	eventDet := NewEventCallDetector(si.client, si.serviceName, si.scopeCtx)
	eventDet.SetCallNodeBuffer(callBuffer)

	extDet := NewExternalCallDetector(si.serviceName, si.scopeCtx)
	extDet.SetCallNodeBuffer(callBuffer)

	cacheDet := NewCacheCallDetector(si.serviceName, si.scopeCtx)
	cacheDet.SetCallNodeBuffer(callBuffer)

	// Repo-level constant + event-emission pre-passes power semantic async event indexing:
	// they turn "EventGroupSettlement + CharDot + EventActionFailed" into "settlement.failed"
	// and bridge the switch-relay split between trigger functions and their SQS-send helpers.
	constRes := newConstResolver(projectPath)
	eventDet.SetConstResolver(constRes)
	eventDet.SetEmissionResolver(NewEventEmissionResolver(projectPath, constRes))

	apiDet := NewAPIClientCallDetector(si.serviceName, si.scopeCtx)
	apiDet.SetCallNodeBuffer(callBuffer)
	apiDet.SetConstResolver(constRes)
	apiDet.SetServiceIndex(svcIdx)

	repoSQLMap, sqlScanErr := ScanRepositorySQL(projectPath)
	if sqlScanErr != nil {
		si.report.warn("repository SQL scan failed: %v", sqlScanErr)
		repoSQLMap = RepoSQLMap{}
	}
	vprintf("  Repository SQL pre-scan: %d methods resolved\n", len(repoSQLMap))

	dbDet := NewDBCallDetector(si.client, si.serviceName, si.scopeCtx, repoSQLMap)
	dbDet.SetCallNodeBuffer(callBuffer)
	// P0-6: repo-wide PgxRepo struct-field set so w.Repo.<Repo>.<Method> chains
	// resolve when the struct is declared in a different file from its methods.
	dbDet.SetPgxRepoFields(ScanPgxRepoFields(projectPath))

	stats := newIndexStats(si.serviceName) // P3-1 coverage telemetry.
	detected, skipped := 0, 0
	err = filepath.Walk(projectPath, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || info.IsDir() {
			return walkErr
		}
		if !strings.HasSuffix(path, ".go") || isNoisyFilePath(path) {
			return nil
		}

		// SCIP stores filePaths as relative paths (doc.RelativePath); convert the
		// absolute walk path to relative so the funcIDs lookup key matches.
		relPath, relErr := filepath.Rel(projectPath, path)
		if relErr != nil {
			relPath = path
		}

		fset := token.NewFileSet()
		astFile, parseErr := goparser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return nil
		}

		importMap := buildImportMap(astFile)
		stats.recordFileImports(importMap) // P3-1: tally wrapper imports per file.
		apiDet.BeginFile(astFile, importMap)
		dbDet.BeginFile(astFile) // P2-6: resolve the file's mongo collection once.

		for _, decl := range astFile.Decls {
			funcDecl, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			if isObservabilityFunction(funcDecl.Name.Name, path) {
				continue
			}
			funcID := funcIDs[relPath+":"+funcDecl.Name.Name]
			if funcID == "" {
				skipped++
				continue
			}
			_ = rpcDet.DetectInFunction(ctx, funcDecl, funcID, relPath, fset)
			_ = eventDet.DetectInFunction(ctx, funcDecl, funcID, relPath, fset)
			_ = dbDet.DetectInFunction(ctx, funcDecl, funcID, relPath, fset)
			extDet.DetectInFunction(funcDecl, funcID, importMap, relPath, fset)
			cacheDet.DetectInFunction(funcDecl, funcID, importMap, relPath, fset)
			apiDet.DetectInFunction(funcDecl, funcID, importMap, relPath, fset)
			detected++
		}
		return nil
	})

	vprintf("  AST RPC scan complete: %d functions processed, %d skipped (no Neo4j ID)\n", detected, skipped)

	// P3-1: snapshot coverage counters BEFORE flush (flush resets the buffer), then report.
	stats.functionsScanned = detected
	stats.functionsSkipped = skipped
	stats.captureWritten(callBuffer)
	stats.dbCandidatesSeen, stats.dbGenericWritten, stats.dbRejectedUntrackedRecv, stats.dbRejectedUnresolvedSQL = dbDet.Stats()
	stats.constAmbiguous = constRes.AmbiguousCount()
	stats.Report() // full telemetry block — verbose only (self-gates)

	if err != nil {
		return stats, err
	}
	if flushErr := callBuffer.flush(ctx, si.client); flushErr != nil {
		return stats, fmt.Errorf("failed to flush call-node buffer: %w", flushErr)
	}
	if resolveErr := si.resolveDBCallMethods(ctx); resolveErr != nil {
		si.report.warn("DBCall→Method resolution failed: %v", resolveErr)
	}
	return stats, nil
}

// loadFunctionIDs returns a map of "filePath:funcName" → Neo4j elementId for all
// Function and Method nodes belonging to this service scope.
func (si *SCIPIndexer) loadFunctionIDs(ctx context.Context) (map[string]string, error) {
	query := `
		MATCH (fn)
		WHERE (fn:Function OR fn:Method)
		  AND fn.scopeId = $scopeId
		RETURN fn.filePath AS filePath, fn.name AS name, elementId(fn) AS id
	`
	results, err := si.client.ExecuteQuery(ctx, query, map[string]any{
		"scopeId": si.scopeCtx.ScopeID,
	})
	if err != nil {
		return nil, err
	}
	m := make(map[string]string, len(results))
	for _, rec := range results {
		data := rec.AsMap()
		fp, _ := data["filePath"].(string)
		name, _ := data["name"].(string)
		id, _ := data["id"].(string)
		// SCIP display names carry trailing SCIP descriptor punctuation, e.g.
		// "GetByID()." — strip to get the bare Go identifier that the AST produces.
		name = strings.TrimSuffix(name, ".")
		name = strings.TrimSuffix(name, "()")
		if fp != "" && name != "" && id != "" {
			m[fp+":"+name] = id
		}
	}
	return m, nil
}

// resolveDBCallMethods links DBCall nodes to the Method nodes they resolve to:
//   - Interface methods in repository/intf/ directories (confidence 0.9)
//   - Concrete pgx implementations in pgx/ directories (confidence 0.85 with file match, 0.75 fallback)
//
// This runs after all nodes and CALLS_DB edges have been flushed so both sides exist.
func (si *SCIPIndexer) resolveDBCallMethods(ctx context.Context) error {
	// Pass A: interface method match via repository/intf/ path + table-name substring.
	// SCIP stores method names with descriptor suffix (e.g. "Upsert()."), so match
	// with both exact name and STARTS WITH name + "(" to cover both forms.
	passA := `
		MATCH (db:DBCall)
		WHERE db.repositoryInterface IS NOT NULL
		  AND db.repositoryMethod IS NOT NULL
		  AND db.scopeId = $scopeId
		WITH db
		MATCH (m:Method)
		WHERE m.scopeId = $scopeId
		  AND (m.name = db.repositoryMethod OR m.name STARTS WITH (db.repositoryMethod + '('))
		  AND m.filePath CONTAINS 'repository/intf/'
		  AND m.filePath CONTAINS db.table
		MERGE (db)-[r:RESOLVES_TO]->(m)
		ON CREATE SET
		  r.confidence       = 0.9,
		  r.resolutionMethod = 'interface_method_match'
		RETURN count(r) AS linked
	`
	// Pass B: pgx concrete implementation match.
	// Uses repositoryFilePath for an exact file match when available; falls back to
	// table-name substring in the pgx/ directory. Path check uses 'pgx/' (no leading
	// slash) to match relative paths like "pgx/lookup_payout_error.go".
	passB := `
		MATCH (db:DBCall)
		WHERE db.repositoryInterface IS NOT NULL
		  AND db.repositoryMethod IS NOT NULL
		  AND db.scopeId = $scopeId
		WITH db
		MATCH (m:Method)
		WHERE m.scopeId = $scopeId
		  AND (m.name = db.repositoryMethod OR m.name STARTS WITH (db.repositoryMethod + '('))
		  AND m.filePath CONTAINS 'pgx/'
		  AND (
		    (db.repositoryFilePath IS NOT NULL AND (m.filePath = db.repositoryFilePath OR m.filePath ENDS WITH ('/' + db.repositoryFilePath)))
		    OR
		    (db.repositoryFilePath IS NULL AND m.filePath CONTAINS db.table)
		  )
		MERGE (db)-[r:RESOLVES_TO]->(m)
		ON CREATE SET
		  r.confidence       = CASE WHEN db.repositoryFilePath IS NOT NULL THEN 0.85 ELSE 0.75 END,
		  r.resolutionMethod = CASE WHEN db.repositoryFilePath IS NOT NULL
		                            THEN 'pgx_file_match'
		                            ELSE 'pgx_naming_convention_match' END
		RETURN count(r) AS linked
	`

	params := map[string]any{"scopeId": si.scopeCtx.ScopeID}
	totalLinked := int64(0)
	for _, q := range []string{passA, passB} {
		results, err := si.client.ExecuteQuery(ctx, q, params)
		if err != nil {
			return err
		}
		if len(results) > 0 {
			if n, ok := results[0].AsMap()["linked"].(int64); ok {
				totalLinked += n
			}
		}
	}
	fmt.Printf("  DBCall→Method resolution: %d RESOLVES_TO edges created\n", totalLinked)
	return nil
}

// isTestFilePath returns true if the file path belongs to a test file across
// Go, Python, and TypeScript/JavaScript conventions.
func isTestFilePath(path string) bool {
	p := strings.ToLower(path)
	return strings.HasSuffix(p, "_test.go") ||
		strings.HasSuffix(p, "_test.py") ||
		strings.HasSuffix(p, "test_.py") ||
		strings.HasSuffix(p, ".test.ts") ||
		strings.HasSuffix(p, ".spec.ts") ||
		strings.HasSuffix(p, ".test.js") ||
		strings.HasSuffix(p, ".spec.js") ||
		strings.HasSuffix(p, ".test.tsx") ||
		strings.HasSuffix(p, ".spec.tsx") ||
		strings.Contains(p, "/tests/") ||
		strings.Contains(p, "/__tests__/")
}

// isGeneratedFilePath returns true for machine-generated source files that
// should be excluded from Reference indexing (protobuf, gRPC stubs, etc.).
func isGeneratedFilePath(path string) bool {
	p := strings.ToLower(path)
	return strings.HasSuffix(p, ".pb.go") ||
		// Tazapay convention: grpcclient/grpc_ut.go holds unit-test injection scaffolding
		// (GetUTObject + ServiceClient-typed structs). It is NOT a _test.go file, so it
		// was being indexed as real code — 5,120+ GetUTObject references plus struct
		// fields (Account, Fx, RuleEngine, ...) that feed noise into gRPC heuristics.
		// The one production caller (settlement/service/http/v3/beneficiary.go's
		// `grpcclient.GetUTObject()` guard) only reads a test-mock holder, so excluding
		// the file orphans no meaningful edge. Basename match: grpc_ut.go anywhere.
		strings.HasSuffix(p, "/grpc_ut.go") ||
		p == "grpc_ut.go" ||
		strings.Contains(p, "/gen/") ||
		strings.HasPrefix(p, "gen/") ||
		strings.Contains(p, "/generated/") ||
		strings.HasPrefix(p, "generated/") ||
		strings.Contains(p, "/vendor/") ||
		strings.HasPrefix(p, "vendor/") ||
		// Tazapay service-specific noise directories
		strings.Contains(p, "/mocks/") ||
		strings.HasPrefix(p, "mocks/") ||
		strings.Contains(p, "/testdata/") ||
		strings.HasPrefix(p, "testdata/") ||
		strings.Contains(p, "/docs/") ||
		strings.HasPrefix(p, "docs/") ||
		strings.Contains(p, "/cdk/") ||
		strings.HasPrefix(p, "cdk/") ||
		strings.Contains(p, "/env/") ||
		strings.HasPrefix(p, "env/") ||
		strings.Contains(p, "/migration/") ||
		strings.HasPrefix(p, "migration/")
}

// isNoisyFilePath returns true for files that should be skipped during indexing
// (test files and generated/vendor files).
func isNoisyFilePath(path string) bool {
	return isTestFilePath(path) || isGeneratedFilePath(path)
}

func isFunctionReferenceKind(kind models.SymbolKind) bool {
	switch kind {
	case models.FunctionSymbol, models.MethodSymbol:
		return true
	default:
		return false
	}
}

// isObservabilityFunction returns true for metric/telemetry helper functions that
// add noise to the call graph without representing real business logic or RPC calls.
// Patterns: increment*/decrement*/observe*/record*/emit* + Metric suffix,
// plus common telemetry file names.
func isObservabilityFunction(funcName, filePath string) bool {
	n := strings.ToLower(funcName)
	// Tazapay: incrementXxx… functions (Prometheus counter helpers)
	if strings.HasPrefix(n, "increment") {
		return true
	}
	// Tazapay: trackXxxMetrics functions (OTel span/gauge helpers)
	if strings.HasPrefix(n, "track") && strings.HasSuffix(n, "metrics") {
		return true
	}
	// Name-based: metric counter/gauge/histogram helpers
	if strings.HasSuffix(n, "metric") || strings.HasSuffix(n, "metrics") {
		return true
	}
	// Telemetry span/trace helpers
	if strings.HasSuffix(n, "span") || strings.HasSuffix(n, "trace") {
		return true
	}
	// Logging helpers (logError, logRequest etc.) — distinct from business log calls
	if (strings.HasPrefix(n, "log") || strings.HasPrefix(n, "emit")) &&
		len(funcName) > 3 {
		return true
	}
	// File-based: entire metrics/telemetry files
	base := strings.ToLower(filepath.Base(filePath))
	return base == "metrics.go" || base == "metric.go" ||
		base == "telemetry.go" || base == "instrumentation.go" ||
		base == "observability.go" || strings.Contains(base, "metrics_")
}

// filterTestSymbols removes symbols whose definition lives in a noisy file
// (test or generated) or is an observability helper, and strips noisy-file
// references from surviving symbols.
func filterTestSymbols(defs []*models.SymbolDefinition) []*models.SymbolDefinition {
	out := defs[:0]
	for _, sd := range defs {
		if sd.Info == nil {
			continue
		}
		if isNoisyFilePath(sd.Info.FilePath) {
			continue
		}
		if (sd.Info.Kind == models.FunctionSymbol || sd.Info.Kind == models.MethodSymbol) &&
			isObservabilityFunction(sd.Info.DisplayName, sd.Info.FilePath) {
			continue
		}
		// Strip references that point into noisy files.
		kept := sd.Refs[:0]
		for _, ref := range sd.Refs {
			if !isNoisyFilePath(ref.FilePath) {
				kept = append(kept, ref)
			}
		}
		sd.Refs = kept
		out = append(out, sd)
	}
	return out
}

// isTestFunction determines if a function is a test function based on its name
// and file path. Supports Go, Python, TypeScript/JavaScript test conventions.
func isTestFunction(name, filePath string) bool {
	nameLower := strings.ToLower(name)
	filePathLower := strings.ToLower(filePath)

	// Name-based detection
	if strings.HasPrefix(nameLower, "test") || strings.HasPrefix(nameLower, "bench") {
		return true
	}

	// File-based detection: Go
	if strings.HasSuffix(filePathLower, "_test.go") {
		return true
	}
	// File-based detection: Python
	if strings.HasSuffix(filePathLower, "_test.py") || strings.HasSuffix(filePathLower, "test_.py") ||
		strings.Contains(filePathLower, "/tests/") || strings.Contains(filePathLower, "/test/") {
		if strings.HasPrefix(nameLower, "test") {
			return true
		}
	}
	// File-based detection: TypeScript/JavaScript
	if strings.HasSuffix(filePathLower, ".test.ts") || strings.HasSuffix(filePathLower, ".spec.ts") ||
		strings.HasSuffix(filePathLower, ".test.js") || strings.HasSuffix(filePathLower, ".spec.js") ||
		strings.HasSuffix(filePathLower, ".test.tsx") || strings.HasSuffix(filePathLower, ".spec.tsx") {
		return true
	}

	return false
}

// absoluteFilePath returns the absolute path by joining si.projectPath with the
// SCIP-relative relPath. Returns relPath unchanged when projectPath is unset or
// relPath is already absolute.
func (si *SCIPIndexer) absoluteFilePath(relPath string) string {
	if si.projectPath == "" || filepath.IsAbs(relPath) {
		return relPath
	}
	return filepath.Join(si.projectPath, relPath)
}

// calculateByteOffsets calculates the start and end byte positions for a code location
func (si *SCIPIndexer) calculateByteOffsets(filePath string, startLine, startColumn, endLine, endColumn int) (int, int) {
	// Use cache to avoid re-reading the same file for every symbol
	content, ok := si.fileContentCache[filePath]
	if !ok {
		var err error
		content, err = os.ReadFile(filePath)
		if err != nil {
			return -1, -1
		}
		if si.fileContentCache == nil {
			si.fileContentCache = make(map[string][]byte)
		}
		si.fileContentCache[filePath] = content
	}

	lines := strings.Split(string(content), "\n")
	if startLine <= 0 || endLine <= 0 || startLine > len(lines) || endLine > len(lines) {
		return -1, -1
	}

	// Calculate start byte offset
	startByte := 0
	for i := 0; i < startLine-1; i++ {
		startByte += len(lines[i]) + 1 // +1 for newline character
	}
	startByte += startColumn

	// Calculate end byte offset
	endByte := 0
	for i := 0; i < endLine-1; i++ {
		endByte += len(lines[i]) + 1 // +1 for newline character
	}
	endByte += endColumn

	return startByte, endByte
}

// readGoModuleName reads the module path from the go.mod file in dir.
// Returns empty string if go.mod is absent or the module line cannot be parsed.
func readGoModuleName(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return ""
	}
	for _, line := range strings.SplitN(string(data), "\n", 20) {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module "))
		}
	}
	return ""
}
