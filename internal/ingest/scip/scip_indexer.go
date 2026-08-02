package static

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	neo4j "github.com/context-maximiser/code-graph/internal/graph"
	"github.com/context-maximiser/code-graph/internal/ingest/resolve"
	models "github.com/context-maximiser/code-graph/internal/model"
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
	projectPath      string            // project root path for resolving relative file paths

	// skipDependencyResolution defers the DEPENDS_ON-creation pass so the
	// polyglot orchestrator can run it once at the end, after every sibling
	// service exists in Neo4j (otherwise early sub-indexes resolve against an
	// empty service set and produce zero edges).
	skipDependencyResolution bool
	// pendingImports holds parsed imports captured during IndexProject when
	// skipDependencyResolution is true; the polyglot post-pass drains it.
	pendingImports   []*models.PackageImport
	pendingServiceID string

	// report tracks per-phase counts and failures during indexing.
	report *IndexReport
}

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
	fmt.Printf("Starting SCIP indexing for %s project at %s\n", si.langConfig.DisplayName, projectPath)

	// Initialize fresh report for this indexing run
	si.report = NewIndexReport()

	// Store projectPath as absolute path for consistent relative path resolution
	absPath, err := filepath.Abs(projectPath)
	if err != nil {
		absPath = projectPath // Fall back to original if conversion fails
	}
	si.projectPath = absPath

	// Step 1: Generate SCIP index file
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

	fmt.Printf("Using SCIP index file: %s\n", scipFile)

	// Step 2: Parse the SCIP file
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

	// Debug: Print SCIP file contents (optional, non-critical)
	if err := parser.DebugPrintSCIPFile(); err != nil {
		// Silently ignore debug print failures; these are optional diagnostic output
	}

	// Step 3: Create service node
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

	// Step 3b: Delete this service's previous subgraph (within this scope)
	// before writing anything new, so re-indexing is idempotent instead of
	// accumulating stale nodes/edges alongside fresh ones.
	if si.timer != nil {
		si.timer.Start("Delete previous subgraph")
	}
	deleteCounts, err := si.deletePreviousSubgraph(ctx)
	if err != nil {
		return fmt.Errorf("failed to delete previous subgraph for %s: %w", si.serviceName, err)
	}
	fmt.Printf("Deleted previous subgraph for %s: %s\n", si.serviceName, formatDeleteCounts(deleteCounts))
	if si.timer != nil {
		total := 0
		for _, n := range deleteCounts {
			total += n
		}
		si.timer.Stop(total, "")
	}

	// Step 4: Index files
	if si.timer != nil {
		si.timer.Start("Index files")
	}
	files, err := parser.ExtractDocuments()
	if err != nil {
		return fmt.Errorf("failed to extract documents: %w", err)
	}

	fileNodes := make(map[string]string) // filePath -> nodeID mapping
	for _, file := range files {
		fileID, err := si.createFileNode(ctx, file, serviceID)
		if err != nil {
			si.report.IncrementFailed("Index files", 1)
			return fmt.Errorf("failed to create file node for %s: %w", file.Path, err)
		}
		fileNodes[file.Path] = fileID
	}
	if si.timer != nil {
		si.timer.Stop(len(fileNodes), fmt.Sprintf("%d files", len(fileNodes)))
	}

	fmt.Printf("Created %d file nodes\n", len(fileNodes))

	// Step 5: Extract symbols
	if si.timer != nil {
		si.timer.Start("Extract symbols")
	}
	symbolDefs, err := parser.ExtractSymbols(si.projectPath)
	if err != nil {
		return fmt.Errorf("failed to extract symbols: %w", err)
	}
	if si.timer != nil {
		si.timer.Stop(len(symbolDefs), fmt.Sprintf("%d symbols", len(symbolDefs)))
	}

	// Step 6 & 7: Index symbols (defs then refs) — instrumented inside indexSymbols
	symIdx, err := si.indexSymbols(ctx, symbolDefs, fileNodes)
	if err != nil {
		return fmt.Errorf("failed to index symbols: %w", err)
	}

	fmt.Printf("Successfully indexed %d symbols from SCIP data\n", len(symbolDefs))

	// Step 6b: Extract and index IMPLEMENTS relationships from SCIP
	if si.timer != nil {
		si.timer.Start("IMPLEMENTS relationships")
	}
	scipRels, err := parser.ExtractRelationships()
	if err != nil {
		// Skip, not failure: SCIP relationships are optional; index can proceed without them
		si.report.IncrementSkipped("IMPLEMENTS relationships", 1)
	} else {
		// RFC-001 Layer 3: scip-go emits sparse is_implementation
		// relationships for Go's structural interfaces (see rfc/001).
		// Supplement whatever SCIP gave us with an authoritative go/types
		// structural-satisfaction pass. Best-effort by design: any failure
		// (no go.mod, non-Go project, packages.Load error) is a warning,
		// never an indexing failure — the SCIP-native relationships above
		// already indexed fine without this.
		knownSymbols := make([]string, 0, len(symIdx.symbolIDs))
		for sym := range symIdx.symbolIDs {
			knownSymbols = append(knownSymbols, sym)
		}

		if si.language == LanguageGo {
			resolverRels, resolveStats, resolveErr := resolve.ResolveImplementations(si.projectPath, knownSymbols)
			if resolveErr != nil {
				fmt.Printf("WARNING: Go structural type resolver skipped: %v\n", resolveErr)
				si.report.AddWarning(fmt.Sprintf("go structural type resolver skipped: %v", resolveErr))
			} else {
				// Skip pairs scip-go already emitted: MERGE would dedupe the
				// edge anyway, but the later SET would overwrite its "scip"
				// provenance with "go-types-resolver" — the edge should keep
				// crediting the indexer that found it first.
				scipPairs := make(map[[2]string]bool, len(scipRels))
				for _, r := range scipRels {
					if r.IsImplementation {
						scipPairs[[2]string{r.FromSymbol, r.ToSymbol}] = true
					}
				}
				added := 0
				for _, r := range resolverRels {
					if scipPairs[[2]string{r.FromSymbol, r.ToSymbol}] {
						continue
					}
					scipRels = append(scipRels, SCIPRelationship{
						FromSymbol:       r.FromSymbol,
						ToSymbol:         r.ToSymbol,
						IsImplementation: r.IsImplementation,
						IsReference:      r.IsReference,
						IsTypeDefinition: r.IsTypeDefinition,
						DetectionSource:  "go-types-resolver",
					})
					added++
				}
				fmt.Printf(
					"Go structural type resolver: %d structural implementations found (%d type-level, %d method-level), %d dropped (symbol not in index), %d new beyond scip-go\n",
					resolveStats.TypeLevelEmitted+resolveStats.MethodLevelEmitted,
					resolveStats.TypeLevelEmitted,
					resolveStats.MethodLevelEmitted,
					resolveStats.DroppedMissingSymbol,
					added,
				)
			}

			// RFC-001 follow-up: dispatch through graph-invisible interfaces
			// (function-local named, anonymous literal, anonymous generic
			// constraint) is expressible by neither scip-go (which gives such
			// interfaces `local N` symbols the indexer creates no Reference
			// node for) nor the IMPLEMENTS machinery (which only enumerates
			// package-scope named types). Synthesize DIRECT dispatch edges at
			// those call sites so the callees don't read as dead code. These
			// merge as CALLS/USES_VALUE (not IMPLEMENTS) below, before the call
			// graph builder computes degrees. Best-effort, same as above.
			if err := si.indexLocalInterfaceCalls(ctx, knownSymbols, symIdx); err != nil {
				// Only merge failures surface here (resolver-run failures are
				// warned about inside) — data-affecting, so fatal like the
				// IMPLEMENTS merge above.
				return err
			}
		} else if si.language == LanguageTypeScript || si.language == LanguageJavaScript {
			// RFC-001 Layer 3 (TS/JS side): scip-typescript only emits
			// is_implementation for explicit `implements`/heritage clauses
			// (FileIndexer.ts's forEachAncestor walk) — a class that
			// structurally satisfies a local interface WITHOUT an
			// `implements` clause gets no relationship at all. Supplement
			// with tools/ts-resolver/resolve.mjs, which uses the TypeScript
			// compiler's own checker.isTypeAssignableTo. Best-effort by
			// design, mirroring the Go branch above: a missing/unusable
			// Node.js or `typescript` environment is a warning, never an
			// indexing failure.
			scipRels = si.runTSStructuralResolver(scipRels, knownSymbols)
		}

		implCount := 0
		for _, r := range scipRels {
			if r.IsImplementation {
				implCount++
			}
		}
		fmt.Printf("Extracted %d SCIP relationships (%d implementation)\n", len(scipRels), implCount)

		if implCount > 0 {
			batch := buildImplementsBatch(scipRels, symIdx.symbolIDs, symIdx.defIDs, symIdx.symbolToDefKey, si.scopeCtx)
			if len(batch) > 0 {
				if err := si.client.MergeRelsBatch(ctx, string(models.ImplementsRel), batch, batchSize); err != nil {
					// Data-affecting failure — a graph missing these is silently wrong: IMPLEMENTS are structural graph edges
					si.report.IncrementFailed("IMPLEMENTS relationships", 1)
					return fmt.Errorf("failed to merge IMPLEMENTS relationships: %w", err)
				} else {
					fmt.Printf("Merged %d IMPLEMENTS relationships\n", len(batch))
					si.report.IncrementWritten("IMPLEMENTS relationships", len(batch))
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

	// Step 8: Package dependencies
	if si.timer != nil {
		si.timer.Start("Package dependencies")
	}
	imports, err := parser.ExtractImports(projectPath)
	if err != nil {
		// Skip, not failure: imports are optional if extraction fails
		si.report.IncrementSkipped("Package dependencies", 1)
	} else {
		fmt.Printf("Extracted %d import statements\n", len(imports))
		if si.skipDependencyResolution {
			si.pendingImports = imports
			si.pendingServiceID = serviceID
			fmt.Println("Deferring DEPENDS_ON resolution to polyglot post-pass")
		} else if err := si.indexPackageDependencies(ctx, imports, serviceID); err != nil {
			// Enrichment only — record and continue: DEPENDS_ON edges enhance but don't corrupt the graph
			si.report.AddWarning(fmt.Sprintf("failed to index package dependencies: %v", err))
			si.report.IncrementFailed("Package dependencies", 1)
		}
	}
	if si.timer != nil {
		importCount := 0
		if imports != nil {
			importCount = len(imports)
		}
		si.timer.Stop(importCount, "")
	}

	// Step 9: Symbol-based API analysis
	// Step 10: Build call graph (Go only, requires AST for function body ranges).
	// Must run before API analysis so that Function/Method nodes have correct
	// startLine/endLine body ranges for findContainingFunction lookups.
	if si.timer != nil {
		si.timer.Start("Call graph")
	}
	if si.language == LanguageGo {
		fmt.Println("Building call graph from SCIP references + Go AST...")
		cgBuilder := NewSCIPCallGraphBuilder(si.client, projectPath)
		cgBuilder.SetScope(si.scopeCtx)
		cgBuilder.SetServiceName(si.serviceName)
		if err := cgBuilder.BuildCallGraph(ctx); err != nil {
			// Enrichment only — record and continue: call graph enhances but doesn't corrupt
			si.report.AddWarning(fmt.Sprintf("call graph construction failed: %v", err))
			si.report.IncrementFailed("Call graph", 1)
		}
	} else {
		fmt.Println("Building call graph from SCIP references (language-agnostic)...")
		cgBuilder := NewGenericCallGraphBuilder(si.client)
		cgBuilder.SetScope(si.scopeCtx)
		cgBuilder.SetServiceName(si.serviceName)
		cgBuilder.SetProjectPath(projectPath)
		// Use the NPM package name or service name for target filtering.
		pkgName := si.extractNPMPackageName(projectPath)
		if pkgName == "" {
			pkgName = si.serviceName
		}
		cgBuilder.SetPackageName(pkgName)
		if err := cgBuilder.BuildCallGraph(ctx); err != nil {
			// Enrichment only — record and continue: call graph enhances but doesn't corrupt
			si.report.AddWarning(fmt.Sprintf("call graph construction failed: %v", err))
			si.report.IncrementFailed("Call graph", 1)
		}
	}
	if si.timer != nil {
		si.timer.Stop(0, "")
	}

	// Step 10b: API analysis (depends on body ranges from call graph step above)
	if si.timer != nil {
		si.timer.Start("API analysis")
	}
	if si.language == LanguageGo {
		// Structural API surface detection — zero framework catalogs.
		fmt.Println("Detecting API surface via graph-structural signals...")
		modulePath := readModulePath(projectPath)
		apiDetector := NewAPISurfaceDetector(si.client, modulePath)
		apiDetector.SetScope(si.scopeCtx)
		apiDetector.SetServiceName(si.serviceName)
		if err := apiDetector.Detect(ctx); err != nil {
			// Enrichment only — record and continue: API surface enhances but doesn't corrupt
			si.report.AddWarning(fmt.Sprintf("structural API surface detection failed: %v", err))
			si.report.IncrementFailed("API analysis", 1)
		}
	} else {
		// Fallback: framework-pattern-based detection for non-Go languages.
		fmt.Println("Analyzing API patterns via SCIP symbol matching...")
		symAnalyzer := NewSymbolAnalyzer(si.client, si.serviceName, si.langConfig.DisplayName, projectPath)
		symAnalyzer.SetScope(si.scopeCtx)
		if err := symAnalyzer.AnalyzeBySymbols(ctx); err != nil {
			// Enrichment only — record and continue: symbol-based API enhances but doesn't corrupt
			si.report.AddWarning(fmt.Sprintf("symbol-based API analysis failed: %v", err))
			si.report.IncrementFailed("API analysis", 1)
		}

		// Decorator-routed handlers (NestJS @Get/@Post/etc.): the tree-sitter
		// structure pass stamped fn.decorators/classDecorators during call
		// graph construction, and decorators are the ONLY signal for these —
		// the framework invokes handlers via reflection, so no CALLS-based
		// strategy can see them. Only the decorator strategy runs here; the
		// full structural detector's other strategies are Go-oriented.
		apiDetector := NewAPISurfaceDetector(si.client, "")
		apiDetector.SetScope(si.scopeCtx)
		apiDetector.SetServiceName(si.serviceName)
		if _, err := apiDetector.detectDecoratorRoutes(ctx); err != nil {
			si.report.AddWarning(fmt.Sprintf("decorator route detection failed: %v", err))
			si.report.IncrementFailed("API analysis", 1)
		}
	}
	if si.timer != nil {
		si.timer.Stop(0, "")
	}

	// Step 10a: Detect semantic edges (message consumers, scheduled functions)
	fmt.Println("Detecting semantic edges...")
	sed := NewSemanticEdgeDetector(si.client)
	sed.SetScope(si.scopeCtx)
	if err := sed.DetectSemanticEdges(ctx); err != nil {
		// Enrichment only — record and continue: semantic edges enhance but don't corrupt
		si.report.AddWarning(fmt.Sprintf("semantic edge detection failed: %v", err))
		si.report.IncrementFailed("Semantic edges", 1)
	}

	// Step 11: Create PullRequest node for PR overlays.
	if si.scopeCtx.Scope == models.ScopePR && si.client != nil {
		// Extract PR ID from scopeId (format: "pr-{id}")
		prID := strings.TrimPrefix(si.scopeCtx.ScopeID, "pr-")

		if _, err := si.createPullRequestNode(ctx, prID,
			fmt.Sprintf("PR %s: %s indexing", prID, si.serviceName)); err != nil {
			// Enrichment only — record and continue: PullRequest node is auxiliary
			si.report.AddWarning(fmt.Sprintf("failed to create PullRequest node: %v", err))
			si.report.IncrementFailed("PullRequest node", 1)
		}
	}

	fmt.Println("SCIP indexing completed successfully")
	return nil
}

// tsResolverTimeout bounds how long the external Node.js structural
// resolver is allowed to run. Best-effort by design: a slow/hung resolver
// must never hold up the rest of indexing indefinitely.
const tsResolverTimeout = 120 * time.Second

// locateTSResolverScript finds tools/ts-resolver/resolve.mjs, checked in
// this order:
//  1. CODEGRAPH_TS_RESOLVER env var, if set (explicit override).
//  2. <dir of the running executable>/../tools/ts-resolver/resolve.mjs
//     (the layout produced when the repo's tools/ directory is shipped
//     alongside the built binary).
//
// Returns "" (never an error) when neither resolves, so callers can treat
// "resolver not found" as a warn-and-skip condition identical to any other
// best-effort environment gap.
func locateTSResolverScript() string {
	if envPath := os.Getenv("CODEGRAPH_TS_RESOLVER"); envPath != "" {
		if _, err := os.Stat(envPath); err == nil {
			return envPath
		}
		return ""
	}

	exePath, err := os.Executable()
	if err != nil {
		return ""
	}
	candidate := filepath.Join(filepath.Dir(exePath), "..", "tools", "ts-resolver", "resolve.mjs")
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}
	return ""
}

// runTSStructuralResolver runs the TypeScript/JavaScript structural type
// resolver (tools/ts-resolver/resolve.mjs) against si.projectPath and
// returns scipRels with any newly-discovered structural implementation
// relationships appended (DetectionSource: "ts-types-resolver"), deduped
// against whatever scip-typescript already emitted natively — mirroring the
// Go branch in IndexProject exactly (see the `si.language == LanguageGo`
// block above).
//
// knownSymbols is every SCIP symbol string scip-typescript emitted for this
// project (the same set the Go branch builds from symIdx.symbolIDs) — it is
// what JoinTSRelationships joins the resolver's file/type/method-name
// output onto.
//
// Every failure mode here — no `node` in PATH, script not found, resolver
// exit codes 2/3, timeout, malformed output — is best-effort: it is
// recorded as a warning and scipRels is returned unchanged. This function
// never returns an error and never fails the index run.
func (si *SCIPIndexer) runTSStructuralResolver(scipRels []SCIPRelationship, knownSymbols []string) []SCIPRelationship {
	if _, err := exec.LookPath("node"); err != nil {
		msg := "ts structural type resolver skipped: 'node' not found in PATH"
		fmt.Printf("WARNING: %s\n", msg)
		si.report.AddWarning(msg)
		return scipRels
	}

	scriptPath := locateTSResolverScript()
	if scriptPath == "" {
		msg := "ts structural type resolver skipped: resolve.mjs not found (set CODEGRAPH_TS_RESOLVER or ship tools/ts-resolver alongside the binary)"
		fmt.Printf("WARNING: %s\n", msg)
		si.report.AddWarning(msg)
		return scipRels
	}

	parsed, err := resolve.RunTSResolver(context.Background(), scriptPath, si.projectPath, tsResolverTimeout)
	if err != nil {
		fmt.Printf("WARNING: TS structural type resolver skipped: %v\n", err)
		si.report.AddWarning(fmt.Sprintf("ts structural type resolver skipped: %v", err))
		return scipRels
	}

	resolverRels, joinStats := resolve.JoinTSRelationships(parsed, knownSymbols)

	scipPairs := make(map[[2]string]bool, len(scipRels))
	for _, r := range scipRels {
		if r.IsImplementation {
			scipPairs[[2]string{r.FromSymbol, r.ToSymbol}] = true
		}
	}
	added := 0
	for _, r := range resolverRels {
		if scipPairs[[2]string{r.FromSymbol, r.ToSymbol}] {
			continue
		}
		scipRels = append(scipRels, SCIPRelationship{
			FromSymbol:       r.FromSymbol,
			ToSymbol:         r.ToSymbol,
			IsImplementation: r.IsImplementation,
			IsReference:      r.IsReference,
			IsTypeDefinition: r.IsTypeDefinition,
			DetectionSource:  "ts-types-resolver",
		})
		added++
	}

	fmt.Printf(
		"TS structural type resolver: %d structural implementations found (%d type-level, %d method-level), %d dropped (symbol not in index), %d new beyond scip-typescript\n",
		joinStats.TypeLevelEmitted+joinStats.MethodLevelEmitted,
		joinStats.TypeLevelEmitted,
		joinStats.MethodLevelEmitted,
		joinStats.DroppedMissingSymbol,
		added,
	)

	return scipRels
}

// indexLocalInterfaceCalls runs the Go local/anonymous-interface dispatch
// resolver (internal/ingest/resolve.ResolveLocalInterfaceCallsJoined) and
// merges the synthesized dispatch edges into the graph as CALLS/USES_VALUE
// relationships tagged detectionSource "local-interface".
//
// It runs inside Step 6b, BEFORE the call-graph builder's
// ComputeDegreeProperties (Step 10), so the synthesized CALLS edges are
// counted in in/out degrees — degree is a CALLS-only computation, so
// USES_VALUE edges (method values) correctly do not affect it, matching the
// call-graph builder's own treatment.
//
// Idempotency: MergeRelsBatch MERGEs one edge per (from, to) per rel type and
// SET-overwrites props, so re-indexing unchanged source produces the same
// edges, not duplicates. On re-index, deletePreviousSubgraph DETACH DELETEs
// this service's Function/Method definition nodes; since both endpoints of a
// local-interface edge are such nodes, the synthesized edges die with their
// endpoints — no orphan leak.
//
// Best-effort by design: a resolver failure (no go.mod, packages.Load error)
// is warned about here and swallowed; a merge failure IS returned (and the
// caller propagates it as fatal), mirroring the IMPLEMENTS merge, because a
// graph silently missing structural edges is wrong in a way that must not
// pass silently.
func (si *SCIPIndexer) indexLocalInterfaceCalls(ctx context.Context, knownSymbols []string, symIdx *symbolIndex) error {
	rels, stats, joinStats, err := resolve.ResolveLocalInterfaceCallsJoined(si.projectPath, knownSymbols)
	if err != nil {
		// Resolver-run failure (no go.mod, packages.Load error, non-Go root):
		// best-effort by design, warn and index on — mirroring the
		// ResolveImplementations block. Merge failures below stay fatal.
		fmt.Printf("WARNING: local-interface dispatch resolver skipped: %v\n", err)
		si.report.AddWarning(fmt.Sprintf("local-interface dispatch resolver skipped: %v", err))
		return nil
	}

	fmt.Printf(
		"Local-interface dispatch resolver: %d sites (%d handled, %d package-named skipped, %d module-scope skipped), %d relations found, %d edges joined, %d dropped (symbol not in index)\n",
		stats.SitesSeen, stats.HandledSites, stats.PackageNamedSkipped, stats.ModuleScopeSkipped,
		stats.RelationsFound, joinStats.Emitted, joinStats.DroppedMissingSymbol,
	)

	callsBatch := buildLocalCallBatch(rels, resolve.LocalCallInvoke, symIdx.symbolIDs, symIdx.defIDs, symIdx.symbolToDefKey, si.scopeCtx)
	valueBatch := buildLocalCallBatch(rels, resolve.LocalCallValue, symIdx.symbolIDs, symIdx.defIDs, symIdx.symbolToDefKey, si.scopeCtx)

	if len(callsBatch) > 0 {
		if err := si.client.MergeRelsBatch(ctx, string(models.CallsRel), callsBatch, batchSize); err != nil {
			si.report.IncrementFailed("Local-interface CALLS", 1)
			return fmt.Errorf("merge local-interface CALLS: %w", err)
		}
		fmt.Printf("Merged %d local-interface CALLS relationships\n", len(callsBatch))
		si.report.IncrementWritten("Local-interface CALLS", len(callsBatch))
	}

	if len(valueBatch) > 0 {
		if err := si.client.MergeRelsBatch(ctx, string(models.UsesValueRel), valueBatch, batchSize); err != nil {
			si.report.IncrementFailed("Local-interface USES_VALUE", 1)
			return fmt.Errorf("merge local-interface USES_VALUE: %w", err)
		}
		fmt.Printf("Merged %d local-interface USES_VALUE relationships\n", len(valueBatch))
		si.report.IncrementWritten("Local-interface USES_VALUE", len(valueBatch))
	}

	return nil
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
	var indexedSubs []indexedSub
	var errs []error
	usedServiceNames := make(map[string]bool)
	for _, r := range roots {
		if _, didFail := failed[r.Language]; didFail {
			fmt.Printf("Skipping %s (%s) — indexer unavailable\n", r.Language, relLabel(r.Path))
			errs = append(errs, fmt.Errorf("indexer unavailable for %s", r.Language))
			continue
		}

		cfg, _ := GetLanguageConfig(r.Language)
		rel := relLabel(r.Path)
		fmt.Printf("\n=== Indexing %s at %s ===\n", cfg.DisplayName, rel)

		// Derive a unique service name: serviceName for the project root,
		// serviceName/rel-path for every sub-module. Uniqueness must hold per
		// (path, language), not just per path — two language roots in the same
		// directory (e.g. go.mod and tsconfig.json both at the repo root)
		// would otherwise share a (serviceName, scopeId) identity, and each
		// sub-run's delete-before-write pass would wipe the other's subgraph.
		subServiceName := si.serviceName
		if rel != "." {
			subServiceName = si.serviceName + "/" + filepath.ToSlash(rel)
		}
		if usedServiceNames[subServiceName] {
			subServiceName = subServiceName + "#" + string(r.Language)
		}
		usedServiceNames[subServiceName] = true

		sub := NewSCIPIndexerWithLanguage(si.client, subServiceName, si.version, si.repoURL, r.Language)
		sub.SetScope(si.scopeCtx)
		if si.timer != nil {
			sub.SetBenchmarkTimer(si.timer)
		}
		// Defer DEPENDS_ON resolution so all sibling services exist before
		// any of them try to resolve imports against the service catalog.
		sub.skipDependencyResolution = true

		if err := sub.IndexProject(ctx, r.Path); err != nil {
			fmt.Printf("Warning: %s indexing at %s failed: %v\n", cfg.DisplayName, rel, err)
			errs = append(errs, fmt.Errorf("%s@%s: %w", r.Language, rel, err))
			continue
		}
		indexedSubs = append(indexedSubs, indexedSub{indexer: sub})
	}

	// Resolve DEPENDS_ON edges after every root is indexed so cross-service
	// references can find their targets.
	si.resolveDeferredDependencies(ctx, indexedSubs)

	if len(errs) == len(roots) {
		return fmt.Errorf("all language roots failed to index: %v", errs)
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
	removeInferredTsconfig := false
	switch si.language {
	case LanguageGo:
		// scip-go --module-name <name> --module-version <version> --output <file>
		cmd = exec.Command(si.langConfig.SCIPBinary,
			"--module-name", si.serviceName,
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

		// If no tsconfig.json at root, use --infer-tsconfig. scip-typescript
		// MATERIALIZES the inferred config into the project directory, so
		// remember to remove it afterward — indexing must never leave
		// artifacts in repos it only reads (a stray inferred tsconfig.json at
		// a repo root also makes the whole repo detect as a TypeScript root
		// on the next polyglot run).
		if _, err := os.Stat(filepath.Join(absPath, "tsconfig.json")); os.IsNotExist(err) {
			args = append(args, "--infer-tsconfig")
			fmt.Println("No root tsconfig.json found, using --infer-tsconfig")
			removeInferredTsconfig = true
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
	fmt.Printf("Running: %s in %s\n", cmd.String(), absPath)
	output, err := cmd.CombinedOutput()
	if removeInferredTsconfig {
		if rmErr := os.Remove(filepath.Join(absPath, "tsconfig.json")); rmErr != nil && !os.IsNotExist(rmErr) {
			fmt.Printf("Warning: could not remove inferred tsconfig.json: %v\n", rmErr)
		}
	}
	if err != nil {
		return "", fmt.Errorf("%s command failed: %w\nOutput: %s", si.langConfig.SCIPBinary, err, string(output))
	}

	fmt.Printf("%s output: %s\n", si.langConfig.SCIPBinary, string(output))

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
	// packageName must hold the canonical identifier that imports of this service
	// resolve to (Go module path, NPM package name, Python distribution name).
	// indexPackageDependencies matches imp.TargetPackage against Service.packageName,
	// so a wrong value here breaks the entire DEPENDS_ON graph.
	actualPackageName := si.detectPackageName(projectPath)

	// rootPath is the absolute, symlink-cleaned indexed directory. It lets
	// MCP source-resolution (codegraph_source, query source) find files on
	// disk regardless of the server process's cwd — see RFC-012 R2.
	rootPath := si.projectPath
	if rootPath == "" {
		rootPath = projectPath
	}
	if abs, err := filepath.Abs(rootPath); err == nil {
		rootPath = abs
	}
	if resolved, err := filepath.EvalSymlinks(rootPath); err == nil {
		rootPath = resolved
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
		"rootPath":      rootPath,
	}

	return si.client.MergeNode(ctx, []string{"Service"},
		map[string]any{"nodeKey": nodeKey, "scopeId": si.scopeCtx.ScopeID}, serviceProps)
}

// detectPackageName returns the canonical import-resolution path for the service,
// per language manifest. Falls back to serviceName when no manifest is found.
func (si *SCIPIndexer) detectPackageName(projectPath string) string {
	switch si.language {
	case LanguageGo:
		if mod := extractGoModulePath(projectPath); mod != "" {
			fmt.Printf("Detected Go module path: %s\n", mod)
			return mod
		}
	case LanguageTypeScript, LanguageJavaScript:
		if npm := si.extractNPMPackageName(projectPath); npm != "" {
			fmt.Printf("Detected NPM package name: %s\n", npm)
			return npm
		}
	case LanguagePython:
		if py := extractPythonPackageName(projectPath); py != "" {
			fmt.Printf("Detected Python package name: %s\n", py)
			return py
		}
	}
	return si.serviceName
}

// extractGoModulePath reads go.mod and returns the value of the `module` directive.
// Returns "" if go.mod is missing or unparseable.
func extractGoModulePath(projectPath string) string {
	data, err := os.ReadFile(filepath.Join(projectPath, "go.mod"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "module ") {
			continue
		}
		mod := strings.TrimSpace(strings.TrimPrefix(line, "module"))
		// Strip optional inline comment and surrounding quotes.
		if i := strings.Index(mod, "//"); i >= 0 {
			mod = strings.TrimSpace(mod[:i])
		}
		mod = strings.Trim(mod, `"`)
		return mod
	}
	return ""
}

// extractPythonPackageName reads pyproject.toml and returns the [project].name value.
// Falls back to top-level `name = "..."` outside any [project] table for setup-style files.
// Returns "" if no name is found.
func extractPythonPackageName(projectPath string) string {
	data, err := os.ReadFile(filepath.Join(projectPath, "pyproject.toml"))
	if err != nil {
		return ""
	}
	var inProject bool
	var nameRegex = []byte(`name`)
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			inProject = trimmed == "[project]"
			continue
		}
		if !inProject {
			continue
		}
		// Match `name = "..."` (TOML basic string).
		if !strings.HasPrefix(trimmed, string(nameRegex)) {
			continue
		}
		eq := strings.Index(trimmed, "=")
		if eq < 0 {
			continue
		}
		val := strings.TrimSpace(trimmed[eq+1:])
		val = strings.Trim(val, `"'`)
		if val != "" {
			return val
		}
	}
	return ""
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

// computeFileHash returns the SHA-256 hex digest of file content, used to
// detect content changes across re-indexing runs. Returns "" for empty
// content.
func computeFileHash(content []byte) string {
	if len(content) == 0 {
		return ""
	}
	h := sha256.Sum256(content)
	return hex.EncodeToString(h[:])
}

// createFileNode creates a file node in Neo4j
func (si *SCIPIndexer) createFileNode(ctx context.Context, file *models.File, serviceID string) (string, error) {
	nodeKey := models.FileNodeKey(si.serviceName, file.Path)

	var content []byte

	// Resolve the file path once (consistent key for all uses)
	resolvedPath := si.resolvePath(file.Path)

	// Try to get content from cache first
	if si.fileContentCache != nil {
		if cached, ok := si.fileContentCache[resolvedPath]; ok {
			content = cached
		}
	}

	// If not in cache, try to read from disk
	if content == nil {
		var err error
		content, err = os.ReadFile(resolvedPath)
		if err != nil {
			return "", fmt.Errorf("failed to read file for hash computation %s: %w", file.Path, err)
		}
		// Cache the content for later use by calculateByteOffsets
		if si.fileContentCache == nil {
			si.fileContentCache = make(map[string][]byte)
		}
		si.fileContentCache[resolvedPath] = content
	}

	hash := computeFileHash(content)

	fileProps := map[string]any{
		"path":         file.Path,
		"nodeKey":      nodeKey,
		"absolutePath": file.Path,
		"language":     file.Language,
		"hash":         hash,
		"lineCount":    0,
		"scope":        si.scopeCtx.Scope,
		"scopeId":      si.scopeCtx.ScopeID,
		"serviceName":  si.serviceName,
	}

	fileID, err := si.client.MergeNode(ctx, []string{"File"},
		map[string]any{"nodeKey": nodeKey, "scopeId": si.scopeCtx.ScopeID}, fileProps)
	if err != nil {
		return "", err
	}

	// Link file to service. MERGE (not CREATE): re-indexing the same service
	// must not accumulate parallel Service-CONTAINS->File edges.
	_, err = si.client.MergeRelationship(ctx, serviceID, fileID, "CONTAINS", nil,
		map[string]any{"scope": si.scopeCtx.Scope, "scopeId": si.scopeCtx.ScopeID})
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
		nodeKey = models.ClassNodeKey(symbolInfo.Symbol.String(), si.serviceName, symbolInfo.FilePath, symbolInfo.DisplayName)
	case "Interface":
		nodeKey = models.InterfaceNodeKey(symbolInfo.Symbol.String(), si.serviceName, symbolInfo.FilePath, symbolInfo.DisplayName)
	case "Variable":
		nodeKey = models.VariableNodeKey(si.serviceName, symbolInfo.FilePath, symbolInfo.DisplayName, symbolInfo.StartLine)
	case "Parameter":
		nodeKey = models.ParameterNodeKey(si.serviceName, symbolInfo.FilePath, symbolInfo.Signature, symbolInfo.DisplayName, 0)
	case "Module":
		nodeKey = models.ModuleNodeKey(symbolInfo.Symbol.String())
	default:
		nodeKey = models.VariableNodeKey(si.serviceName, symbolInfo.FilePath, symbolInfo.DisplayName, symbolInfo.StartLine)
	}

	props = map[string]any{
		"name":        symbolInfo.DisplayName,
		"nodeKey":     nodeKey,
		"signature":   symbolInfo.Signature,
		"filePath":    symbolInfo.FilePath,
		"startLine":   symbolInfo.StartLine,
		"endLine":     symbolInfo.EndLine,
		"startColumn": symbolInfo.StartColumn,
		"endColumn":   symbolInfo.EndColumn,
		"scope":       si.scopeCtx.Scope,
		"scopeId":     si.scopeCtx.ScopeID,
	}

	// Per-service identity nodes carry serviceName so scoped queries can hit
	// (n {serviceName: $svc}) directly instead of traversing the
	// CONTAINS chain from the Service node. Class/Interface/Module are
	// intentionally omitted: their nodeKeys derive from globally-unique SCIP
	// FQNs and the same node is MERGEd from multiple services — a single
	// serviceName property on a shared node would be incoherent (last-writer
	// wins). Cross-service joins for those types go through SymbolNodeKey.
	switch label {
	case "Function", "Method", "Variable", "Parameter":
		props["serviceName"] = si.serviceName
	}

	if label == "Function" || label == "Method" {
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
		props["isExported"] = si.computeIsExported(symbolInfo.DisplayName)
		props["docstring"] = symbolInfo.Documentation
		props["isTestFunction"] = isTestFunction(symbolInfo.DisplayName, symbolInfo.FilePath)
		if symbolInfo.KindSource != "" {
			props["kindSource"] = symbolInfo.KindSource
		}
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

// computeIsExported returns whether a symbol's display name marks it as
// exported in its source language. For Go we use the leading-uppercase
// convention (ast.IsExported equivalent). For other languages SCIP doesn't
// give us a visibility modifier directly, so we conservatively return true
// (matches the prior hardcoded behavior for those languages).
func (si *SCIPIndexer) computeIsExported(displayName string) bool {
	if displayName == "" {
		return false
	}
	if si.language == LanguageGo {
		return unicode.IsUpper(rune(displayName[0]))
	}
	return true
}

const batchSize = 500

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

		if info.FilePath != "" {
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
		// Data-affecting failure — a graph missing these is silently wrong: Symbol nodes are core to the graph
		si.report.IncrementFailed("Index symbols (defs)", 1)
		return nil, fmt.Errorf("batch merge Symbol nodes failed: %w", err)
	}
	fmt.Printf("Merged %d Symbol nodes\n", len(symbolIDs))
	si.report.IncrementWritten("Index symbols (defs)", len(symbolIDs))

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
		batch = dedupeByNodeKey(batch)
		ids, err := si.client.MergeNodesBatch(ctx, lbl, batch, batchSize)
		if err != nil {
			// Data-affecting failure — a graph missing these is silently wrong: Definition nodes (Function/Method/Class/etc) are core
			si.report.IncrementFailed("Index symbols (defs)", 1)
			return nil, fmt.Errorf("batch merge %s nodes failed: %w", lbl, err)
		}
		for nk, id := range ids {
			defIDs[nk] = id
		}
		si.report.IncrementWritten("Index symbols (defs)", len(ids))
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
	if err := si.client.MergeRelsBatch(ctx, "DEFINES", definesRels, batchSize); err != nil {
		// Data-affecting failure — a graph missing these is silently wrong: DEFINES edges are core relationships
		si.report.IncrementFailed("Index symbols (defs)", 1)
		return nil, fmt.Errorf("batch merge DEFINES rels failed: %w", err)
	}
	if len(definesRels) > 0 {
		si.report.IncrementWritten("Index symbols (defs)", len(definesRels))
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
	if err := si.client.MergeRelsBatch(ctx, "CONTAINS", containsRels, batchSize); err != nil {
		// Data-affecting failure — a graph missing these is silently wrong: CONTAINS edges (File -> Definition) are core structural
		si.report.IncrementFailed("Index symbols (defs)", 1)
		return nil, fmt.Errorf("batch merge def CONTAINS rels failed: %w", err)
	}
	if len(containsRels) > 0 {
		si.report.IncrementWritten("Index symbols (defs)", len(containsRels))
	}
	tRelContains := time.Since(t)

	if si.timer != nil {
		si.timer.Stop(len(symbolDefs), fmt.Sprintf("%d symbols", len(symbolDefs)))
		if recorder, ok := si.timer.(SubPhaseRecorder); ok {
			recorder.AddResult("  MergeNodesBatch(Symbol)", tMergeSymbol, len(symbolIDs), "")
			recorder.AddResult("  MergeNodesBatch(Definition)", tMergeDefinition, len(defIDs), "")
			recorder.AddResult("  MergeRelsBatch(DEFINES)", tRelDefines, len(definesRels), "")
			recorder.AddResult("  MergeRelsBatch(CONTAINS)", tRelContains, len(containsRels), "")
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
					"serviceName": si.serviceName,
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

	t = time.Now()
	refIDs, err := si.client.MergeNodesBatch(ctx, "Reference", refBatch, batchSize)
	tMergeRef := time.Since(t)
	if err != nil {
		// Data-affecting failure — a graph missing these is silently wrong: Reference nodes are core to the graph
		si.report.IncrementFailed("Index symbols (refs)", 1)
		return nil, fmt.Errorf("batch merge Reference nodes failed: %w", err)
	}
	fmt.Printf("Merged %d Reference nodes\n", len(refIDs))
	si.report.IncrementWritten("Index symbols (refs)", len(refIDs))

	// 2. Batch create REFERENCES rels (refID → symbolID)
	var referencesRels []map[string]any
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

	t = time.Now()
	if err := si.client.MergeRelsBatch(ctx, "REFERENCES", referencesRels, batchSize); err != nil {
		// Data-affecting failure — a graph missing these is silently wrong: REFERENCES edges are core relationships
		si.report.IncrementFailed("Index symbols (refs)", 1)
		return nil, fmt.Errorf("batch merge REFERENCES rels failed: %w", err)
	}
	if len(referencesRels) > 0 {
		si.report.IncrementWritten("Index symbols (refs)", len(referencesRels))
	}
	tRelReferences := time.Since(t)

	// 3. Batch create CONTAINS rels (fileID → refID).
	// refItems contains one entry per (symbol, reference) pair; the same
	// Reference node can appear under multiple symbols (e.g. an identifier
	// occurrence that satisfies both a definition and an import). Without
	// deduping by (fileID, refID), CreateRelsBatch produces N parallel CONTAINS
	// edges between the same File and Reference (visible as File→Reference
	// rels=2..23 in Neo4j). We dedupe before the batch since CONTAINS is a
	// boolean structural fact — there is no per-edge data to preserve.
	type fileRefKey struct{ fromID, toID string }
	seenContains := make(map[fileRefKey]bool, len(refItems))
	var refContainsRels []map[string]any
	for _, ri := range refItems {
		refID, ok1 := refIDs[ri.nodeKey]
		fileID, ok2 := fileNodes[ri.filePath]
		if !ok1 || !ok2 {
			continue
		}
		key := fileRefKey{fromID: fileID, toID: refID}
		if seenContains[key] {
			continue
		}
		seenContains[key] = true
		refContainsRels = append(refContainsRels, map[string]any{
			"fromId": fileID,
			"toId":   refID,
			"props":  map[string]any{"scope": si.scopeCtx.Scope, "scopeId": si.scopeCtx.ScopeID},
		})
	}

	t = time.Now()
	if err := si.client.MergeRelsBatch(ctx, "CONTAINS", refContainsRels, batchSize); err != nil {
		// Data-affecting failure — a graph missing these is silently wrong: CONTAINS edges (File -> Reference) are core structural
		si.report.IncrementFailed("Index symbols (refs)", 1)
		return nil, fmt.Errorf("batch merge ref CONTAINS rels failed: %w", err)
	}
	if len(refContainsRels) > 0 {
		si.report.IncrementWritten("Index symbols (refs)", len(refContainsRels))
	}
	tRelRefContains := time.Since(t)

	if si.timer != nil {
		si.timer.Stop(len(refItems), fmt.Sprintf("%d refs", len(refItems)))
		if recorder, ok := si.timer.(SubPhaseRecorder); ok {
			recorder.AddResult("  MergeNodesBatch(Reference)", tMergeRef, len(refIDs), "")
			recorder.AddResult("  MergeRelsBatch(REFERENCES)", tRelReferences, len(referencesRels), "")
			recorder.AddResult("  MergeRelsBatch(CONTAINS)", tRelRefContains, len(refContainsRels), "")
		}
	}

	fmt.Printf("Completed indexing symbols (created %d reference relationships)\n", len(refItems))
	return &symbolIndex{
		symbolIDs:      symbolIDs,
		defIDs:         defIDs,
		symbolToDefKey: symToDefKey,
	}, nil
}

// indexedSub holds a polyglot sub-indexer whose dependency resolution has been
// deferred to the post-pass. The sub-indexer itself carries the captured
// imports and service ID.
type indexedSub struct {
	indexer *SCIPIndexer
}

// resolveDeferredDependencies runs the DEPENDS_ON resolver for each polyglot
// sub-indexer after all sibling services have been written to Neo4j. We need
// the post-pass because indexPackageDependencies matches imports against the
// service catalog at query time — running it eagerly per-root produces zero
// edges for the first root (catalog is empty) and incomplete edges for the
// middle roots.
func (si *SCIPIndexer) resolveDeferredDependencies(ctx context.Context, subs []indexedSub) {
	if len(subs) == 0 {
		return
	}
	fmt.Println("\n=== Resolving cross-service DEPENDS_ON edges ===")
	for _, s := range subs {
		if s.indexer.pendingServiceID == "" {
			// Indexing failed before serviceNode was created; nothing to resolve.
			continue
		}
		if err := s.indexer.indexPackageDependencies(ctx,
			s.indexer.pendingImports, s.indexer.pendingServiceID); err != nil {
			// Enrichment only — record and continue: DEPENDS_ON edges enhance but don't corrupt
			s.indexer.report.AddWarning(fmt.Sprintf("deferred dep resolution failed for %s: %v", s.indexer.serviceName, err))
			s.indexer.report.IncrementFailed("Package dependencies", 1)
		}
	}
}

// indexPackageDependencies creates DEPENDS_ON relationships between services based on imports
func (si *SCIPIndexer) indexPackageDependencies(ctx context.Context, imports []*models.PackageImport, serviceID string) error {
	if len(imports) == 0 {
		return nil
	}

	fmt.Printf("Processing %d imports for dependency relationships...\n", len(imports))

	// Group imports by target package
	packageMap := make(map[string]int)
	for _, imp := range imports {
		if imp.IsExternal {
			packageMap[imp.TargetPackage]++
		}
	}

	// Match strategy (longest match wins):
	//   1. Exact match on s.packageName (canonical module path).
	//   2. Subpackage import: $packageName starts with s.packageName + '/'
	//      (e.g. importing ".../libs/core-models-go/foo" hits the
	//      core-models-go service whose packageName is the parent module).
	//
	// The previous bidirectional CONTAINS branches are intentionally removed:
	//   - "$packageName CONTAINS s.packageName" matched any service whose
	//     packageName was a substring anywhere in the import (false positives
	//     like 'go' matching '.../go-something').
	//   - "s.packageName CONTAINS $packageName" matched the root-module service
	//     for every sub-module import.
	// Both produced silent mis-attribution of dependencies.
	const targetServiceQuery = `
		MATCH (s:Service)
		WHERE s.scopeId = $scopeId
		  AND s.packageName IS NOT NULL
		  AND s.packageName <> ''
		  AND (s.packageName = $packageName
		       OR $packageName STARTS WITH (s.packageName + '/'))
		RETURN elementId(s) AS id, s.name AS name, s.packageName AS packageName
		ORDER BY
			CASE WHEN s.packageName = $packageName THEN 0 ELSE 1 END,
			size(s.packageName) DESC
		LIMIT 1
	`

	createdCount := 0
	// Multiple distinct import paths can resolve to the same target service
	// (e.g. .../libs/llm-go/foo and .../libs/llm-go/bar both hit llm-go's
	// subpackage match). Without this dedup we emit one DEPENDS_ON per
	// import path, producing 4× duplicates in the graph.
	linkedTargets := make(map[string]bool, len(packageMap))
	for packageName, count := range packageMap {
		result, err := si.client.ExecuteQuery(ctx, targetServiceQuery,
			map[string]any{
				"packageName": packageName,
				"scopeId":     si.scopeCtx.ScopeID,
			})

		if err != nil || len(result) == 0 {
			// Target service not indexed yet, log and skip
			fmt.Printf("  No service found for package: %s\n", packageName)
			continue
		}

		targetServiceID := result[0].AsMap()["id"].(string)
		targetServiceName := result[0].AsMap()["name"].(string)

		// Avoid self-dependencies
		if targetServiceID == serviceID {
			continue
		}

		// Skip if we've already linked this target from this source.
		if linkedTargets[targetServiceID] {
			continue
		}
		linkedTargets[targetServiceID] = true

		// Create DEPENDS_ON relationship
		relProps := map[string]any{
			"packageName":  packageName,
			"isDirect":     true,
			"importCount":  count,
			"detectedFrom": "import-analysis",
			"scope":        si.scopeCtx.Scope,
			"scopeId":      si.scopeCtx.ScopeID,
		}

		_, err = si.client.MergeRelationship(ctx, serviceID, targetServiceID,
			string(models.DependsOnRel), nil, relProps)

		if err != nil {
			// Enrichment only — record and continue: individual DEPENDS_ON edge failure doesn't corrupt graph
			// Continue trying to create other dependency edges
			si.report.AddWarning(fmt.Sprintf("failed to merge DEPENDS_ON relationship to %s: %v", targetServiceName, err))
			si.report.IncrementFailed("Package dependencies", 1)
		} else {
			fmt.Printf("Created DEPENDS_ON: %s -> %s (%d imports)\n", si.serviceName, targetServiceName, count)
			si.report.IncrementWritten("Package dependencies", 1)
			createdCount++
		}
	}

	fmt.Printf("Created %d DEPENDS_ON relationships\n", createdCount)
	return nil
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

// Report returns the IndexReport from the most recent IndexProject run.
// Returns nil if IndexProject has not been called or completed.
func (si *SCIPIndexer) Report() *IndexReport {
	return si.report
}

// createPullRequestNode creates a PullRequest node in the graph for PR-scope
// overlay tracking. This is a plain graph write with no generation/LLM
// involvement.
func (si *SCIPIndexer) createPullRequestNode(ctx context.Context, prID, title string) (string, error) {
	nodeKey := models.PullRequestNodeKey(prID)
	props := map[string]any{
		"prId":    prID,
		"title":   title,
		"status":  "open",
		"nodeKey": nodeKey,
		"scope":   si.scopeCtx.Scope,
		"scopeId": si.scopeCtx.ScopeID,
	}

	id, err := si.client.MergeNode(ctx, []string{"PullRequest"},
		map[string]any{"nodeKey": nodeKey, "scopeId": si.scopeCtx.ScopeID}, props)
	if err != nil {
		return "", fmt.Errorf("failed to create PullRequest node: %w", err)
	}
	return id, nil
}

// GetLanguage returns the language this indexer is configured for
func (si *SCIPIndexer) GetLanguage() Language {
	return si.language
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

// resolvePath resolves a relative file path against projectPath to create an absolute path.
// If the path is already absolute, it is returned unchanged.
func (si *SCIPIndexer) resolvePath(filePath string) string {
	if filepath.IsAbs(filePath) || si.projectPath == "" {
		return filePath
	}
	return filepath.Join(si.projectPath, filePath)
}

// calculateByteOffsets calculates the start and end byte positions for a code
// location. startLine/endLine are 1-based (the graph-wide convention — see
// convertRange's doc comment); startColumn/endColumn are 0-based, matching
// SCIP. Returns (-1, -1) if the file can't be read or the line range is out
// of bounds.
func (si *SCIPIndexer) calculateByteOffsets(filePath string, startLine, startColumn, endLine, endColumn int) (int, int) {
	// Resolve relative paths using projectPath
	resolvedPath := si.resolvePath(filePath)

	// Use cache to avoid re-reading the same file for every symbol
	// Keep cache keys consistent: use the resolved absolute path
	content, ok := si.fileContentCache[resolvedPath]
	if !ok {
		var err error
		content, err = os.ReadFile(resolvedPath)
		if err != nil {
			return -1, -1
		}
		if si.fileContentCache == nil {
			si.fileContentCache = make(map[string][]byte)
		}
		si.fileContentCache[resolvedPath] = content
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
