package static

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/context-maximiser/code-graph/pkg/models"
	"github.com/context-maximiser/code-graph/pkg/neo4j"
)

// SCIPIndexer indexes Go projects using the SCIP protocol
type SCIPIndexer struct {
	client      *neo4j.Client
	serviceName string
	version     string
	repoURL     string
	scipBinary  string
}

// NewSCIPIndexer creates a new SCIP-based indexer
func NewSCIPIndexer(client *neo4j.Client, serviceName, version, repoURL string) *SCIPIndexer {
	return &SCIPIndexer{
		client:      client,
		serviceName: serviceName,
		version:     version,
		repoURL:     repoURL,
		scipBinary:  "scip-go", // Assume scip-go is in PATH
	}
}

// IndexProject indexes a Go project using SCIP
func (si *SCIPIndexer) IndexProject(ctx context.Context, projectPath string) error {
	fmt.Printf("Starting SCIP indexing for project at %s\n", projectPath)

	// Step 1: Generate SCIP index file
	scipFile, err := si.generateSCIPIndex(projectPath)
	if err != nil {
		return fmt.Errorf("failed to generate SCIP index: %w", err)
	}
	defer os.Remove(scipFile) // Clean up temporary file

	fmt.Printf("Generated SCIP index file: %s\n", scipFile)

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
	serviceID, err := si.createServiceNode(ctx)
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
	return nil
}

// generateSCIPIndex runs scip-go to generate a SCIP index file
func (si *SCIPIndexer) generateSCIPIndex(projectPath string) (string, error) {
	// Check if scip-go is available
	if _, err := exec.LookPath(si.scipBinary); err != nil {
		return "", fmt.Errorf("scip-go not found in PATH. Install with: go install github.com/sourcegraph/scip-go/cmd/scip-go@latest")
	}

	// Create temporary output file
	outputFile := filepath.Join(projectPath, "index.scip")

	// Prepare scip-go command
	cmd := exec.Command(si.scipBinary,
		"--module-name", si.serviceName,
		"--module-version", si.version,
		"--output", outputFile,
	)

	// Set working directory
	cmd.Dir = projectPath

	// Run the command
	fmt.Printf("Running: %s in %s\n", cmd.String(), projectPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("scip-go command failed: %w\nOutput: %s", err, string(output))
	}

	fmt.Printf("scip-go output: %s\n", string(output))

	// Verify the output file exists
	if _, err := os.Stat(outputFile); os.IsNotExist(err) {
		return "", fmt.Errorf("SCIP index file was not generated: %s", outputFile)
	}

	return outputFile, nil
}

// createServiceNode creates the service node in Neo4j
func (si *SCIPIndexer) createServiceNode(ctx context.Context) (string, error) {
	serviceProps := map[string]any{
		"name":          si.serviceName,
		"language":      "Go",
		"version":       si.version,
		"repositoryUrl": si.repoURL,
	}

	return si.client.MergeNode(ctx, []string{"Service"}, 
		map[string]any{"name": si.serviceName}, serviceProps)
}

// createFileNode creates a file node in Neo4j
func (si *SCIPIndexer) createFileNode(ctx context.Context, file *models.File, serviceID string) (string, error) {
	fileProps := map[string]any{
		"path":         file.Path,
		"absolutePath": file.Path, // SCIP only provides relative paths
		"language":     file.Language,
		"hash":         "", // Not available from SCIP
		"lineCount":    0,  // Not available from SCIP
	}

	fileID, err := si.client.MergeNode(ctx, []string{"File"}, 
		map[string]any{"path": file.Path}, fileProps)
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
	for _, symbolDef := range symbolDefs {
		symbolID, exists := symbolNodes[symbolDef.Symbol.String()]
		if !exists {
			continue
		}

		for _, ref := range symbolDef.Refs {
			if !ref.IsDefinition { // Skip definitions, we already handled those
				err := si.createReferenceRelationship(ctx, ref, symbolID, fileNodes)
				if err != nil {
					fmt.Printf("Warning: failed to create reference relationship: %v\n", err)
				}
			}
		}
	}

	fmt.Printf("Completed indexing symbols\n")
	return nil
}

// createSymbolNode creates a Symbol node in Neo4j
func (si *SCIPIndexer) createSymbolNode(ctx context.Context, symbolInfo *models.SymbolInfo) (string, error) {
	symbolProps := map[string]any{
		"symbol":        symbolInfo.Symbol.String(),
		"kind":          string(symbolInfo.Kind),
		"displayName":   symbolInfo.DisplayName,
		"documentation": symbolInfo.Documentation,
	}

	return si.client.MergeNode(ctx, []string{"Symbol"}, 
		map[string]any{"symbol": symbolInfo.Symbol.String()}, symbolProps)
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

	props := map[string]any{
		"name":        symbolInfo.DisplayName,
		"signature":   symbolInfo.Signature,
		"filePath":    symbolInfo.FilePath,
		"startLine":   symbolInfo.StartLine,
		"endLine":     symbolInfo.EndLine,
		"startColumn": symbolInfo.StartColumn,
		"endColumn":   symbolInfo.EndColumn,
	}

	// Calculate additional metadata for Functions and Methods
	if nodeLabel == "Function" || nodeLabel == "Method" {
		// Calculate lines of code
		if symbolInfo.EndLine > symbolInfo.StartLine {
			props["linesOfCode"] = symbolInfo.EndLine - symbolInfo.StartLine + 1
		} else {
			props["linesOfCode"] = 1
		}

		// Calculate byte offsets if we have the file content
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
		props["scope"] = "unknown"
		props["isConstant"] = symbolInfo.Kind == models.ConstantSymbol
	}

	return si.client.MergeNode(ctx, []string{nodeLabel}, 
		map[string]any{"signature": symbolInfo.Signature, "filePath": symbolInfo.FilePath}, props)
}

// createReferenceRelationship creates reference relationships
func (si *SCIPIndexer) createReferenceRelationship(ctx context.Context, ref *models.SymbolReference, symbolID string, fileNodes map[string]string) error {
	// For now, we'll create a simple reference node and link it to the symbol
	// In a full implementation, we might want to find the exact AST node that contains the reference
	
	refProps := map[string]any{
		"filePath":    ref.FilePath,
		"startLine":   ref.StartLine,
		"endLine":     ref.EndLine,
		"startColumn": ref.StartColumn,
		"endColumn":   ref.EndColumn,
		"context":     ref.Context,
	}

	refID, err := si.client.CreateNode(ctx, []string{"Reference"}, refProps)
	if err != nil {
		return err
	}

	// Link reference to symbol
	_, err = si.client.CreateRelationship(ctx, refID, symbolID, "REFERENCES", 
		map[string]any{
			"isDefinition": ref.IsDefinition,
			"line": ref.StartLine,
			"column": ref.StartColumn,
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
	si.scipBinary = binary
}

// ValidateEnvironment checks if the required tools are available
func (si *SCIPIndexer) ValidateEnvironment() error {
	if _, err := exec.LookPath(si.scipBinary); err != nil {
		return fmt.Errorf("scip-go not found in PATH. Install with: go install github.com/sourcegraph/scip-go/cmd/scip-go@latest")
	}
	return nil
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