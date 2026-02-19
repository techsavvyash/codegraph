package static

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/context-maximiser/code-graph/pkg/models"
	"github.com/context-maximiser/code-graph/pkg/neo4j"
)

// SCIPIndexer indexes projects using the SCIP protocol
type SCIPIndexer struct {
	client       *neo4j.Client
	serviceName  string
	version      string
	repoURL      string
	language     Language
	langConfig   *LanguageConfig
	scopeCtx     models.ScopeContext
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
	scipFile, err := si.generateSCIPIndex(projectPath)
	if err != nil {
		return fmt.Errorf("failed to generate SCIP index: %w", err)
	}
	// Only clean up for languages that auto-generate (not Java/Scala/Kotlin)
	if si.language != LanguageJava && si.language != LanguageScala && si.language != LanguageKotlin {
		defer os.Remove(scipFile) // Clean up temporary file
	}

	fmt.Printf("Using SCIP index file: %s\n", scipFile)

	// Step 2: Parse the SCIP file
	parser := NewSCIPParser()
	if err := parser.ParseFile(scipFile); err != nil {
		return fmt.Errorf("failed to parse SCIP file: %w", err)
	}

	// Debug: Print SCIP file contents
	if err := parser.DebugPrintSCIPFile(); err != nil {
		fmt.Printf("Warning: failed to debug print SCIP file: %v\n", err)
	}

	// Step 3: Create service node
	serviceID, err := si.createServiceNode(ctx, projectPath)
	if err != nil {
		return fmt.Errorf("failed to create service node: %w", err)
	}

	// Step 4: Index files
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

	fmt.Printf("Created %d file nodes\n", len(fileNodes))

	// Step 5: Index symbols and their relationships
	symbolDefs, err := parser.ExtractSymbols()
	if err != nil {
		return fmt.Errorf("failed to extract symbols: %w", err)
	}

	if err := si.indexSymbols(ctx, symbolDefs, fileNodes); err != nil {
		return fmt.Errorf("failed to index symbols: %w", err)
	}

	fmt.Printf("Successfully indexed %d symbols from SCIP data\n", len(symbolDefs))

	// Step 6: Extract and index imports for cross-package dependencies
	imports, err := parser.ExtractImports(projectPath)
	if err != nil {
		fmt.Printf("Warning: failed to extract imports: %v\n", err)
	} else {
		fmt.Printf("Extracted %d import statements\n", len(imports))
		if err := si.indexPackageDependencies(ctx, imports, serviceID); err != nil {
			fmt.Printf("Warning: failed to index package dependencies: %v\n", err)
		}
	}

	// Step 7: Analyze API patterns and create cross-service relationships
	fmt.Println("Analyzing API patterns and cross-service calls...")
	analyzer := NewAPIAnalyzer(si.client, si.serviceName, si.langConfig.DisplayName, projectPath)
	if err := analyzer.AnalyzeAPIPatterns(ctx, fileNodes); err != nil {
		fmt.Printf("Warning: API pattern analysis failed: %v\n", err)
		// Don't fail the entire indexing, just log warning
	}

	fmt.Println("SCIP indexing completed successfully")
	return nil
}

// generateSCIPIndex runs the appropriate SCIP indexer to generate a SCIP index file
func (si *SCIPIndexer) generateSCIPIndex(projectPath string) (string, error) {
	// Check if the language-specific SCIP binary is available
	if _, err := exec.LookPath(si.langConfig.SCIPBinary); err != nil {
		return "", fmt.Errorf("%s not found in PATH.\nInstall with: %s\nSee: %s",
			si.langConfig.SCIPBinary,
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
	_, err = si.client.CreateRelationship(ctx, serviceID, fileID, "CONTAINS", nil)
	return fileID, err
}

// indexSymbols indexes all symbols and their relationships
func (si *SCIPIndexer) indexSymbols(ctx context.Context, symbolDefs []*models.SymbolDefinition, fileNodes map[string]string) error {
	fmt.Printf("Indexing %d symbols...\n", len(symbolDefs))

	symbolNodes := make(map[string]string) // symbol -> nodeID mapping

	// First pass: Create all symbol nodes
	for i, symbolDef := range symbolDefs {
		if i%100 == 0 {
			fmt.Printf("Processing symbol %d/%d\n", i, len(symbolDefs))
		}

		symbolID, err := si.createSymbolNode(ctx, symbolDef.Info)
		if err != nil {
			fmt.Printf("Warning: failed to create symbol node for %s: %v\n", 
				symbolDef.Symbol.String(), err)
			continue
		}

		symbolNodes[symbolDef.Symbol.String()] = symbolID

		// Create definition node if we have location info
		if symbolDef.Info.FilePath != "" {
			definitionID, err := si.createDefinitionNode(ctx, symbolDef.Info)
			if err != nil {
				fmt.Printf("Warning: failed to create definition node: %v\n", err)
				continue
			}

			// Link definition to symbol
			_, err = si.client.CreateRelationship(ctx, definitionID, symbolID, "DEFINES", 
				map[string]any{"isExported": true}) // Assume exported for now
			if err != nil {
				fmt.Printf("Warning: failed to link definition to symbol: %v\n", err)
			}

			// Link definition to file if file exists
			if fileID, exists := fileNodes[symbolDef.Info.FilePath]; exists {
				_, err = si.client.CreateRelationship(ctx, fileID, definitionID, "CONTAINS", nil)
				if err != nil {
					fmt.Printf("Warning: failed to link definition to file: %v\n", err)
				}
			}
		}
	}

	// Second pass: Create reference relationships
	fmt.Printf("\nCreating reference relationships...\n")
	processedRefs := 0
	for i, symbolDef := range symbolDefs {
		if i%1000 == 0 {
			fmt.Printf("Processing references for symbol %d/%d (created %d references)\n", i, len(symbolDefs), processedRefs)
		}

		symbolID, exists := symbolNodes[symbolDef.Symbol.String()]
		if !exists {
			continue
		}

		for _, ref := range symbolDef.Refs {
			if !ref.IsDefinition { // Skip definitions, we already handled those
				err := si.createReferenceRelationship(ctx, ref, symbolID, fileNodes)
				if err != nil {
					fmt.Printf("Warning: failed to create reference relationship: %v\n", err)
				} else {
					processedRefs++
				}
			}
		}
	}

	fmt.Printf("Completed indexing symbols (created %d reference relationships)\n", processedRefs)
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

// createSymbolNode creates a Symbol node in Neo4j
func (si *SCIPIndexer) createSymbolNode(ctx context.Context, symbolInfo *models.SymbolInfo) (string, error) {
	nodeKey := models.SymbolNodeKey(symbolInfo.Symbol.String())
	symbolProps := map[string]any{
		"symbol":        symbolInfo.Symbol.String(),
		"nodeKey":       nodeKey,
		"kind":          string(symbolInfo.Kind),
		"displayName":   symbolInfo.DisplayName,
		"documentation": symbolInfo.Documentation,
		"scope":         si.scopeCtx.Scope,
		"scopeId":       si.scopeCtx.ScopeID,
	}

	return si.client.MergeNode(ctx, []string{"Symbol"},
		map[string]any{"nodeKey": nodeKey, "scopeId": si.scopeCtx.ScopeID}, symbolProps)
}

// createDefinitionNode creates a definition node (Function, Class, etc.) in Neo4j
func (si *SCIPIndexer) createDefinitionNode(ctx context.Context, symbolInfo *models.SymbolInfo) (string, error) {
	var nodeLabel string
	switch symbolInfo.Kind {
	case models.FunctionSymbol:
		nodeLabel = "Function"
	case models.MethodSymbol:
		nodeLabel = "Method"
	case models.TypeSymbol:
		nodeLabel = "Class"
	case models.InterfaceSymbol:
		nodeLabel = "Interface"
	case models.VariableSymbol:
		nodeLabel = "Variable"
	case models.ConstantSymbol:
		nodeLabel = "Variable"
	case models.ParameterSymbol:
		nodeLabel = "Parameter"
	case models.FieldSymbol:
		nodeLabel = "Variable"
	case models.PackageSymbol:
		nodeLabel = "Module"
	default:
		nodeLabel = "Variable"
	}

	// Derive nodeKey based on node type
	var nodeKey string
	switch nodeLabel {
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

	props := map[string]any{
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

	// Calculate additional metadata for Functions and Methods
	if nodeLabel == "Function" || nodeLabel == "Method" {
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

	// Add type-specific properties
	switch nodeLabel {
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

	return si.client.MergeNode(ctx, []string{nodeLabel},
		map[string]any{"nodeKey": nodeKey, "scopeId": si.scopeCtx.ScopeID}, props)
}

// createReferenceRelationship creates reference relationships
func (si *SCIPIndexer) createReferenceRelationship(ctx context.Context, ref *models.SymbolReference, symbolID string, fileNodes map[string]string) error {
	nodeKey := models.ReferenceNodeKey(ref.FilePath, ref.StartLine, ref.StartColumn)
	refProps := map[string]any{
		"filePath":    ref.FilePath,
		"nodeKey":     nodeKey,
		"startLine":   ref.StartLine,
		"endLine":     ref.EndLine,
		"startColumn": ref.StartColumn,
		"endColumn":   ref.EndColumn,
		"context":     ref.Context,
		"scope":       si.scopeCtx.Scope,
		"scopeId":     si.scopeCtx.ScopeID,
	}

	refID, err := si.client.MergeNode(ctx, []string{"Reference"},
		map[string]any{"nodeKey": nodeKey, "scopeId": si.scopeCtx.ScopeID}, refProps)
	if err != nil {
		return err
	}

	// Link reference to symbol
	_, err = si.client.CreateRelationship(ctx, refID, symbolID, "REFERENCES",
		map[string]any{
			"isDefinition": ref.IsDefinition,
			"line":         ref.StartLine,
			"column":       ref.StartColumn,
		})
	if err != nil {
		return err
	}

	// Link reference to file if file exists
	if fileID, exists := fileNodes[ref.FilePath]; exists {
		_, err = si.client.CreateRelationship(ctx, fileID, refID, "CONTAINS", nil)
		if err != nil {
			return err
		}
	}

	return nil
}

// SetSCIPBinary sets the path to the SCIP binary (for testing or custom installations)
func (si *SCIPIndexer) SetSCIPBinary(binary string) {
	si.langConfig.SCIPBinary = binary
}

// ValidateEnvironment checks if the required tools are available
func (si *SCIPIndexer) ValidateEnvironment() error {
	if _, err := exec.LookPath(si.langConfig.SCIPBinary); err != nil {
		return fmt.Errorf("%s not found in PATH.\nInstall with: %s\nSee: %s",
			si.langConfig.SCIPBinary,
			si.langConfig.InstallCommand,
			si.langConfig.InstallDocs)
	}
	return nil
}

// SetScope sets the scope context for the indexer.
func (si *SCIPIndexer) SetScope(scope models.ScopeContext) {
	si.scopeCtx = scope
}

// GetLanguage returns the language this indexer is configured for
func (si *SCIPIndexer) GetLanguage() Language {
	return si.language
}

// calculateByteOffsets calculates the start and end byte positions for a code location
func (si *SCIPIndexer) calculateByteOffsets(filePath string, startLine, startColumn, endLine, endColumn int) (int, int) {
	// Read the file content
	content, err := os.ReadFile(filePath)
	if err != nil {
		return -1, -1
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