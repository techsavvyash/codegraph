package static

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/context-maximiser/code-graph/libs/indexer-go/generated"
	"github.com/context-maximiser/code-graph/libs/core-models-go"
	"github.com/context-maximiser/code-graph/libs/neo4j-go"
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

	// Debug: Print SCIP file contents
	if err := parser.DebugPrintSCIPFile(); err != nil {
		fmt.Printf("Warning: failed to debug print SCIP file: %v\n", err)
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
			fmt.Printf("Warning: failed to create file node for %s: %v\n", file.Path, err)
			continue
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
	symbolDefs, err := parser.ExtractSymbols()
	if err != nil {
		return fmt.Errorf("failed to extract symbols: %w", err)
	}
	if si.timer != nil {
		si.timer.Stop(len(symbolDefs), fmt.Sprintf("%d symbols", len(symbolDefs)))
	}

	// Step 6 & 7: Index symbols (defs then refs) — instrumented inside indexSymbols
	if err := si.indexSymbols(ctx, symbolDefs, fileNodes); err != nil {
		return fmt.Errorf("failed to index symbols: %w", err)
	}

	fmt.Printf("Successfully indexed %d symbols from SCIP data\n", len(symbolDefs))

	// Step 8: Package dependencies
	if si.timer != nil {
		si.timer.Start("Package dependencies")
	}
	imports, err := parser.ExtractImports(projectPath)
	if err != nil {
		fmt.Printf("Warning: failed to extract imports: %v\n", err)
	} else {
		fmt.Printf("Extracted %d import statements\n", len(imports))
		if err := si.indexPackageDependencies(ctx, imports, serviceID); err != nil {
			fmt.Printf("Warning: failed to index package dependencies: %v\n", err)
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
	if si.timer != nil {
		si.timer.Start("API analysis")
	}
	fmt.Println("Analyzing API patterns via SCIP symbol matching...")
	symAnalyzer := NewSymbolAnalyzer(si.client, si.serviceName, si.langConfig.DisplayName, projectPath)
	symAnalyzer.SetScope(si.scopeCtx)
	if err := symAnalyzer.AnalyzeBySymbols(ctx); err != nil {
		fmt.Printf("Warning: symbol-based API analysis failed: %v\n", err)
	}
	if si.timer != nil {
		si.timer.Stop(0, "")
	}

	// Step 10: Build call graph (Go only, requires AST for function body ranges)
	if si.timer != nil {
		si.timer.Start("Call graph")
	}
	if si.language == LanguageGo {
		fmt.Println("Building call graph from SCIP references + Go AST...")
		cgBuilder := NewSCIPCallGraphBuilder(si.client, projectPath)
		cgBuilder.SetScope(si.scopeCtx)
		if err := cgBuilder.BuildCallGraph(ctx); err != nil {
			fmt.Printf("Warning: call graph construction failed: %v\n", err)
		}
	}
	if si.timer != nil {
		si.timer.Stop(0, "")
	}

	// Step 11: Generate context for PR overlays (creates PullRequest node + PR summary)
	if si.scopeCtx.Scope == models.ScopePR && si.client != nil {
		ctxGen := generated.NewContextGenerator(si.client)
		ctxGen.SetScope(si.scopeCtx)

		// Extract PR ID from scopeId (format: "pr-{id}")
		prID := strings.TrimPrefix(si.scopeCtx.ScopeID, "pr-")

		if _, err := ctxGen.CreatePullRequestNode(ctx, prID,
			fmt.Sprintf("PR %s: %s indexing", prID, si.serviceName),
			"", "", "", ""); err != nil {
			fmt.Printf("Warning: failed to create PullRequest node: %v\n", err)
		} else {
			// Store a basic PR summary with indexed file list
			fileKeys := make([]string, 0)
			for _, f := range fileNodes {
				fileKeys = append(fileKeys, f)
			}
			summary := fmt.Sprintf("Indexed %d files and %d symbols for service %s",
				len(fileNodes), len(symbolDefs), si.serviceName)
			if _, err := ctxGen.StorePRSummary(ctx, prID,
				fmt.Sprintf("Indexing summary for %s", si.serviceName),
				summary, "scip-indexer", fileKeys); err != nil {
				fmt.Printf("Warning: failed to store PR summary: %v\n", err)
			}
		}
	}

	fmt.Println("SCIP indexing completed successfully")
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
	var errs []error
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
		// serviceName/rel-path for every sub-module.
		subServiceName := si.serviceName
		if rel != "." {
			subServiceName = si.serviceName + "/" + filepath.ToSlash(rel)
		}

		sub := NewSCIPIndexerWithLanguage(si.client, subServiceName, si.version, si.repoURL, r.Language)
		sub.SetScope(si.scopeCtx)
		if si.timer != nil {
			sub.SetBenchmarkTimer(si.timer)
		}

		if err := sub.IndexProject(ctx, r.Path); err != nil {
			fmt.Printf("Warning: %s indexing at %s failed: %v\n", cfg.DisplayName, rel, err)
			errs = append(errs, fmt.Errorf("%s@%s: %w", r.Language, rel, err))
		}
	}

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
		// If a virtual environment exists, point scip-python at it so it can
		// enumerate installed packages for type resolution.
		if venvPath := detectPythonVenv(absPath); venvPath != "" {
			args = append(args, "--environment-path", venvPath)
			fmt.Printf("Detected Python venv at %s\n", venvPath)
		}
		cmd = exec.Command(si.langConfig.SCIPBinary, args...)
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
// projectPath if one exists and contains a usable pip or python binary.
// It checks the common venv directory names: .venv, venv, .env, env.
func detectPythonVenv(projectPath string) string {
	candidates := []string{".venv", "venv", ".env", "env"}
	for _, dir := range candidates {
		venvPath := filepath.Join(projectPath, dir)
		info, err := os.Stat(venvPath)
		if err != nil || !info.IsDir() {
			continue
		}
		// Require at least a pip or python binary to confirm it's a real venv.
		for _, bin := range []string{"bin/pip", "bin/pip3", "bin/python", "bin/python3"} {
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
	nodeKey := models.FileNodeKey(file.Path)
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

	// Link file to service
	_, err = si.client.CreateRelationship(ctx, serviceID, fileID, "CONTAINS",
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
		nodeKey = models.FunctionNodeKey(symbolInfo.FilePath, symbolInfo.Signature)
	case "Method":
		nodeKey = models.MethodNodeKey(symbolInfo.FilePath, symbolInfo.Signature)
	case "Class":
		nodeKey = models.ClassNodeKey(symbolInfo.Symbol.String(), symbolInfo.FilePath, symbolInfo.DisplayName)
	case "Interface":
		nodeKey = models.InterfaceNodeKey(symbolInfo.Symbol.String(), symbolInfo.FilePath, symbolInfo.DisplayName)
	case "Variable":
		nodeKey = models.VariableNodeKey(symbolInfo.FilePath, symbolInfo.DisplayName, symbolInfo.StartLine)
	case "Parameter":
		nodeKey = models.ParameterNodeKey(symbolInfo.FilePath, symbolInfo.Signature, symbolInfo.DisplayName, 0)
	case "Module":
		nodeKey = models.ModuleNodeKey(symbolInfo.Symbol.String())
	default:
		nodeKey = models.VariableNodeKey(symbolInfo.FilePath, symbolInfo.DisplayName, symbolInfo.StartLine)
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

// indexSymbols indexes all symbols and their relationships using UNWIND batches.
func (si *SCIPIndexer) indexSymbols(ctx context.Context, symbolDefs []*models.SymbolDefinition, fileNodes map[string]string) error {
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
		symNK := models.SymbolNodeKey(sd.Info.Symbol.String())
		if _, exists := symbolIDs[symNK]; !exists {
			continue
		}
		for _, ref := range sd.Refs {
			if ref.IsDefinition {
				continue
			}
			nk := models.ReferenceNodeKey(ref.FilePath, ref.StartLine, ref.StartColumn)
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
		fmt.Printf("Warning: batch merge Reference nodes failed: %v\n", err)
		refIDs = make(map[string]string)
	}
	fmt.Printf("Merged %d Reference nodes\n", len(refIDs))

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
	if err := si.client.CreateRelsBatch(ctx, "REFERENCES", referencesRels, batchSize); err != nil {
		fmt.Printf("Warning: batch create REFERENCES rels failed: %v\n", err)
	}
	tRelReferences := time.Since(t)

	// 3. Batch create CONTAINS rels (fileID → refID)
	var refContainsRels []map[string]any
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

	t = time.Now()
	if err := si.client.CreateRelsBatch(ctx, "CONTAINS", refContainsRels, batchSize); err != nil {
		fmt.Printf("Warning: batch create ref CONTAINS rels failed: %v\n", err)
	}
	tRelRefContains := time.Since(t)

	if si.timer != nil {
		si.timer.Stop(len(refItems), fmt.Sprintf("%d refs", len(refItems)))
		if recorder, ok := si.timer.(SubPhaseRecorder); ok {
			recorder.AddResult("  MergeNodesBatch(Reference)", tMergeRef, len(refIDs), "")
			recorder.AddResult("  CreateRelsBatch(REFERENCES)", tRelReferences, len(referencesRels), "")
			recorder.AddResult("  CreateRelsBatch(CONTAINS)", tRelRefContains, len(refContainsRels), "")
		}
	}

	fmt.Printf("Completed indexing symbols (created %d reference relationships)\n", len(refItems))
	return nil
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

	createdCount := 0

	// Create DEPENDS_ON relationships for each external package
	for packageName, count := range packageMap {
		// Try to find the target service by package name or service name
		// Multiple matching strategies:
		// 1. Exact packageName match
		// 2. Service name in package (e.g., "@try-veil/veil-gateway" matches "veil-gateway")
		// 3. Package contains service name
		targetServiceQuery := `
			MATCH (s:Service)
			WHERE s.packageName = $packageName
			   OR s.name = $packageName
			   OR $packageName CONTAINS s.packageName
			   OR s.packageName CONTAINS $packageName
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

		// Create DEPENDS_ON relationship
		relProps := map[string]any{
			"packageName":  packageName,
			"isDirect":     true,
			"importCount":  count,
			"detectedFrom": "import-analysis",
			"scope":        si.scopeCtx.Scope,
			"scopeId":      si.scopeCtx.ScopeID,
		}

		_, err = si.client.CreateRelationship(ctx, serviceID, targetServiceID,
			string(models.DependsOnRel), relProps)

		if err != nil {
			fmt.Printf("Warning: failed to create DEPENDS_ON relationship to %s: %v\n", targetServiceName, err)
		} else {
			fmt.Printf("Created DEPENDS_ON: %s -> %s (%d imports)\n", si.serviceName, targetServiceName, count)
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

// GetLanguage returns the language this indexer is configured for
func (si *SCIPIndexer) GetLanguage() Language {
	return si.language
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