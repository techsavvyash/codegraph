package static

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/context-maximiser/code-graph/pkg/models"
	"github.com/sourcegraph/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"
)

// SCIPParser parses SCIP index files and extracts code intelligence data
type SCIPParser struct {
	index *scip.Index
}

// NewSCIPParser creates a new SCIP parser
func NewSCIPParser() *SCIPParser {
	return &SCIPParser{}
}

// ParseFile parses a SCIP index file
func (sp *SCIPParser) ParseFile(scipFilePath string) error {
	data, err := os.ReadFile(scipFilePath)
	if err != nil {
		return fmt.Errorf("failed to read SCIP file: %w", err)
	}

	sp.index = &scip.Index{}
	err = proto.Unmarshal(data, sp.index)
	if err != nil {
		return fmt.Errorf("failed to unmarshal SCIP data: %w", err)
	}

	return nil
}

// GetMetadata returns the metadata from the SCIP index
func (sp *SCIPParser) GetMetadata() *scip.Metadata {
	if sp.index == nil {
		return nil
	}
	return sp.index.Metadata
}

// ExtractSymbols extracts all symbol information from the SCIP index
func (sp *SCIPParser) ExtractSymbols() ([]*models.SymbolDefinition, error) {
	if sp.index == nil {
		return nil, fmt.Errorf("no SCIP index loaded")
	}

	var symbolDefs []*models.SymbolDefinition

	// Process external symbols first
	for _, symbolInfo := range sp.index.ExternalSymbols {
		scipSymbol, err := models.ParseSCIPSymbol(symbolInfo.Symbol)
		if err != nil {
			continue // Skip invalid symbols
		}

		symbolDef := &models.SymbolDefinition{
			Symbol: scipSymbol,
			Info: &models.SymbolInfo{
				Symbol:        scipSymbol,
				Kind:          convertSymbolKind(symbolInfo.Kind),
				DisplayName:   extractDisplayName(symbolInfo.Symbol),
				Documentation: strings.Join(symbolInfo.Documentation, " "),
				Signature:     extractSignature(symbolInfo),
			},
			Refs: []*models.SymbolReference{},
		}

		symbolDefs = append(symbolDefs, symbolDef)
	}

	// Process documents and their symbols
	for _, doc := range sp.index.Documents {
		filePath := doc.RelativePath

		// Skip excluded paths like node_modules, vendor, etc.
		if shouldExcludePath(filePath) {
			continue
		}

		// Process occurrences in this document
		for _, occurrence := range doc.Occurrences {
			scipSymbol, err := models.ParseSCIPSymbol(occurrence.Symbol)
			if err != nil {
				continue // Skip invalid symbols
			}

			// Convert SCIP ranges to our format
			startLine, startColumn := convertRange(occurrence.Range, true)
			endLine, endColumn := convertRange(occurrence.Range, false)

			ref := &models.SymbolReference{
				Symbol:      scipSymbol,
				FilePath:    filePath,
				StartLine:   startLine,
				EndLine:     endLine,
				StartColumn: startColumn,
				EndColumn:   endColumn,
				IsDefinition: occurrence.SymbolRoles&int32(scip.SymbolRole_Definition) != 0,
			}

			// Find or create the symbol definition
			var targetSymbolDef *models.SymbolDefinition
			for _, existing := range symbolDefs {
				if existing.Symbol.String() == scipSymbol.String() {
					targetSymbolDef = existing
					break
				}
			}

			if targetSymbolDef == nil {
				// Create new symbol definition
				targetSymbolDef = &models.SymbolDefinition{
					Symbol: scipSymbol,
					Info: &models.SymbolInfo{
						Symbol:      scipSymbol,
						Kind:        inferSymbolKind(occurrence.Symbol),
						DisplayName: extractDisplayName(occurrence.Symbol),
						FilePath:    filePath,
						StartLine:   startLine,
						EndLine:     endLine,
						StartColumn: startColumn,
						EndColumn:   endColumn,
					},
					Refs: []*models.SymbolReference{},
				}
				symbolDefs = append(symbolDefs, targetSymbolDef)
			}

			// Add reference to symbol definition
			targetSymbolDef.AddReference(ref)
		}
	}

	return symbolDefs, nil
}

// shouldExcludePath checks if a file path should be excluded from indexing
func shouldExcludePath(path string) bool {
	excludedDirs := []string{
		"node_modules/",
		"vendor/",
		".git/",
		".next/",
		".nuxt/",
		"dist/",
		"build/",
		"target/", // Maven/Gradle build output
		"venv/",   // Python virtual env
		".venv/",
		"__pycache__/",
	}

	for _, dir := range excludedDirs {
		if strings.Contains(path, dir) {
			return true
		}
	}
	return false
}

// ExtractDocuments extracts file information from the SCIP index
func (sp *SCIPParser) ExtractDocuments() ([]*models.File, error) {
	if sp.index == nil {
		return nil, fmt.Errorf("no SCIP index loaded")
	}

	var files []*models.File
	totalDocs := len(sp.index.Documents)
	excludedCount := 0

	for _, doc := range sp.index.Documents {
		// Skip excluded paths like node_modules, vendor, etc.
		if shouldExcludePath(doc.RelativePath) {
			excludedCount++
			continue
		}

		file := &models.File{
			Path:     doc.RelativePath,
			Language: inferLanguage(doc.RelativePath),
			// Note: SCIP doesn't provide file size, line count, or hash
			// These would need to be computed separately if needed
		}

		files = append(files, file)
	}

	if excludedCount > 0 {
		fmt.Printf("Filtered out %d/%d files from excluded directories (node_modules, vendor, etc.)\n", excludedCount, totalDocs)
	}

	return files, nil
}

// GetServiceInfo extracts service information from SCIP metadata
func (sp *SCIPParser) GetServiceInfo() (*models.Service, error) {
	metadata := sp.GetMetadata()
	if metadata == nil {
		return nil, fmt.Errorf("no metadata found")
	}

	// Try to infer language from tool info
	language := "unknown"
	if metadata.ToolInfo != nil {
		toolName := strings.ToLower(metadata.ToolInfo.Name)
		if strings.Contains(toolName, "scip-go") {
			language = "Go"
		} else if strings.Contains(toolName, "scip-typescript") || strings.Contains(toolName, "typescript") {
			language = "TypeScript"
		} else if strings.Contains(toolName, "scip-python") || strings.Contains(toolName, "python") {
			language = "Python"
		} else if strings.Contains(toolName, "scip-java") || strings.Contains(toolName, "java") {
			language = "Java"
		}
	}

	service := &models.Service{
		Name:     metadata.ProjectRoot,
		Language: language,
		Version:  "1.0.0", // Default version since metadata.Version is a ProtocolVersion
	}

	return service, nil
}

// Helper functions

func convertSymbolKind(scipKind scip.SymbolInformation_Kind) models.SymbolKind {
	switch scipKind {
	case scip.SymbolInformation_UnspecifiedKind:
		return models.VariableSymbol
	case scip.SymbolInformation_Namespace:
		return models.PackageSymbol
	case scip.SymbolInformation_Type:
		return models.TypeSymbol
	case scip.SymbolInformation_Class:
		return models.TypeSymbol
	case scip.SymbolInformation_Interface:
		return models.InterfaceSymbol
	case scip.SymbolInformation_Function:
		return models.FunctionSymbol
	case scip.SymbolInformation_Method:
		return models.MethodSymbol
	case scip.SymbolInformation_Field:
		return models.FieldSymbol
	case scip.SymbolInformation_Variable:
		return models.VariableSymbol
	case scip.SymbolInformation_Constant:
		return models.ConstantSymbol
	case scip.SymbolInformation_Parameter:
		return models.ParameterSymbol
	default:
		return models.VariableSymbol
	}
}

func inferSymbolKind(symbol string) models.SymbolKind {
	// Simple heuristic to infer symbol kind from SCIP symbol string
	if strings.Contains(symbol, "#") && strings.Contains(symbol, "().") {
		return models.MethodSymbol
	} else if strings.Contains(symbol, "().") {
		return models.FunctionSymbol
	} else if strings.Contains(symbol, "#") {
		return models.TypeSymbol
	} else if strings.Contains(symbol, "/") {
		return models.PackageSymbol
	} else {
		return models.VariableSymbol
	}
}

func extractDisplayName(symbol string) string {
	// Extract the last component as display name
	parts := strings.Split(symbol, " ")
	if len(parts) < 5 {
		return symbol
	}
	
	descriptor := parts[4] // SCIP format: scheme manager name version descriptor
	
	// Extract the actual name from the descriptor
	if strings.Contains(descriptor, "#") {
		// Type or method
		parts := strings.Split(descriptor, "#")
		if len(parts) > 1 {
			return strings.TrimSuffix(parts[len(parts)-1], "()")
		}
	} else if strings.Contains(descriptor, "/") {
		// Package
		parts := strings.Split(descriptor, "/")
		return parts[len(parts)-1]
	}
	
	return descriptor
}

func extractSignature(symbolInfo *scip.SymbolInformation) string {
	// For now, use the display name as signature
	// In a full implementation, we might extract more detailed signature info
	return symbolInfo.Symbol
}

func convertRange(scipRange []int32, isStart bool) (int, int) {
	if len(scipRange) < 4 {
		return 0, 0
	}
	
	if isStart {
		return int(scipRange[0]), int(scipRange[1])
	} else {
		return int(scipRange[2]), int(scipRange[3])
	}
}

func inferLanguage(filePath string) string {
	ext := strings.ToLower(filepath.Ext(filePath))

	switch ext {
	case ".go":
		return "Go"
	case ".ts", ".tsx":
		return "TypeScript"
	case ".js", ".jsx":
		return "JavaScript"
	case ".py":
		return "Python"
	case ".java":
		return "Java"
	case ".scala":
		return "Scala"
	case ".kt", ".kts":
		return "Kotlin"
	case ".rs":
		return "Rust"
	case ".rb":
		return "Ruby"
	case ".php":
		return "PHP"
	case ".c", ".h":
		return "C"
	case ".cpp", ".cc", ".cxx", ".hpp":
		return "C++"
	case ".cs":
		return "C#"
	default:
		return "unknown"
	}
}

// DebugPrintSCIPFile prints a human-readable representation of the SCIP file
func (sp *SCIPParser) DebugPrintSCIPFile() error {
	if sp.index == nil {
		return fmt.Errorf("no SCIP index loaded")
	}

	fmt.Println("=== SCIP Index Debug Output ===")
	
	// Print metadata
	if metadata := sp.index.Metadata; metadata != nil {
		fmt.Printf("Project Root: %s\n", metadata.ProjectRoot)
		fmt.Printf("Version: %s\n", metadata.Version)
		fmt.Printf("Tool Info: %s %s\n", metadata.ToolInfo.Name, metadata.ToolInfo.Version)
	}

	// Print external symbols
	fmt.Printf("\nExternal Symbols (%d):\n", len(sp.index.ExternalSymbols))
	for i, symbol := range sp.index.ExternalSymbols {
		if i < 10 { // Limit output
			fmt.Printf("  %s (Kind: %s)\n", symbol.Symbol, symbol.Kind.String())
		}
	}
	if len(sp.index.ExternalSymbols) > 10 {
		fmt.Printf("  ... and %d more\n", len(sp.index.ExternalSymbols)-10)
	}

	// Print documents
	fmt.Printf("\nDocuments (%d):\n", len(sp.index.Documents))
	for i, doc := range sp.index.Documents {
		if i < 5 { // Limit output
			fmt.Printf("  %s (%d occurrences)\n", doc.RelativePath, len(doc.Occurrences))
			
			// Print first few occurrences
			for j, occ := range doc.Occurrences {
				if j < 3 {
					fmt.Printf("    %s [%v] (Roles: %d)\n", occ.Symbol, occ.Range, occ.SymbolRoles)
				}
			}
			if len(doc.Occurrences) > 3 {
				fmt.Printf("    ... and %d more occurrences\n", len(doc.Occurrences)-3)
			}
		}
	}
	if len(sp.index.Documents) > 5 {
		fmt.Printf("  ... and %d more documents\n", len(sp.index.Documents)-5)
	}

	return nil
}

// ValidateSCIPFile checks if a file is a valid SCIP file
func ValidateSCIPFile(filePath string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("cannot open file: %w", err)
	}
	defer file.Close()

	// Check if it's a binary protobuf file by trying to read first few bytes
	scanner := bufio.NewScanner(file)
	if scanner.Scan() {
		line := scanner.Text()
		// SCIP files are binary, so text files are definitely not SCIP
		if len(line) > 0 && line[0] < 32 {
			// Looks like binary data, could be SCIP
			return nil
		}
	}

	return fmt.Errorf("file does not appear to be a valid SCIP file")
}

// ExtractImports analyzes source files to extract import statements
func (sp *SCIPParser) ExtractImports(projectPath string) ([]*models.PackageImport, error) {
	if sp.index == nil {
		return nil, fmt.Errorf("no SCIP index loaded")
	}

	var imports []*models.PackageImport
	importMap := make(map[string]bool) // Deduplicate imports

	for _, doc := range sp.index.Documents {
		// Build full file path
		fullPath := filepath.Join(projectPath, doc.RelativePath)

		// Read file content
		content, err := os.ReadFile(fullPath)
		if err != nil {
			fmt.Printf("Warning: failed to read file %s: %v\n", fullPath, err)
			continue
		}

		sourceCode := string(content)
		language := inferLanguage(doc.RelativePath)

		// Extract imports based on language
		var fileImports []*models.PackageImport
		switch language {
		case "TypeScript", "JavaScript":
			fileImports = extractTypeScriptImports(doc.RelativePath, sourceCode)
		case "Go":
			fileImports = extractGoImports(doc.RelativePath, sourceCode)
		case "Python":
			fileImports = extractPythonImports(doc.RelativePath, sourceCode)
		case "Java", "Kotlin", "Scala":
			fileImports = extractJavaImports(doc.RelativePath, sourceCode)
		}

		// Deduplicate and add to result
		for _, imp := range fileImports {
			key := fmt.Sprintf("%s -> %s", imp.SourceFile, imp.TargetPackage)
			if !importMap[key] {
				importMap[key] = true
				imports = append(imports, imp)
			}
		}
	}

	return imports, nil
}

// extractTypeScriptImports parses TypeScript/JavaScript import statements
func extractTypeScriptImports(filePath, sourceCode string) []*models.PackageImport {
	var imports []*models.PackageImport

	// Pattern: import { X, Y } from 'package'
	namedImportRegex := regexp.MustCompile(`import\s+{([^}]+)}\s+from\s+['"]([^'"]+)['"]`)
	matches := namedImportRegex.FindAllStringSubmatch(sourceCode, -1)
	for _, match := range matches {
		if len(match) > 2 {
			importedNames := strings.Split(match[1], ",")
			for i, name := range importedNames {
				importedNames[i] = strings.TrimSpace(name)
			}

			packageName := match[2]
			isExternal := !strings.HasPrefix(packageName, ".") && !strings.HasPrefix(packageName, "/")

			imports = append(imports, &models.PackageImport{
				SourceFile:    filePath,
				TargetPackage: packageName,
				ImportedNames: importedNames,
				IsExternal:    isExternal,
			})
		}
	}

	// Pattern: import * as X from 'package'
	namespaceImportRegex := regexp.MustCompile(`import\s+\*\s+as\s+(\w+)\s+from\s+['"]([^'"]+)['"]`)
	matches = namespaceImportRegex.FindAllStringSubmatch(sourceCode, -1)
	for _, match := range matches {
		if len(match) > 2 {
			packageName := match[2]
			isExternal := !strings.HasPrefix(packageName, ".") && !strings.HasPrefix(packageName, "/")

			imports = append(imports, &models.PackageImport{
				SourceFile:    filePath,
				TargetPackage: packageName,
				ImportedNames: []string{match[1]},
				IsExternal:    isExternal,
			})
		}
	}

	// Pattern: import X from 'package' (default import)
	defaultImportRegex := regexp.MustCompile(`import\s+(\w+)\s+from\s+['"]([^'"]+)['"]`)
	matches = defaultImportRegex.FindAllStringSubmatch(sourceCode, -1)
	for _, match := range matches {
		if len(match) > 2 {
			packageName := match[2]
			isExternal := !strings.HasPrefix(packageName, ".") && !strings.HasPrefix(packageName, "/")

			imports = append(imports, &models.PackageImport{
				SourceFile:    filePath,
				TargetPackage: packageName,
				ImportedNames: []string{match[1]},
				IsExternal:    isExternal,
			})
		}
	}

	return imports
}

// extractGoImports parses Go import statements
func extractGoImports(filePath, sourceCode string) []*models.PackageImport {
	var imports []*models.PackageImport

	// Single import: import "package"
	singleImportRegex := regexp.MustCompile(`import\s+"([^"]+)"`)
	matches := singleImportRegex.FindAllStringSubmatch(sourceCode, -1)
	for _, match := range matches {
		if len(match) > 1 {
			imports = append(imports, &models.PackageImport{
				SourceFile:    filePath,
				TargetPackage: match[1],
				IsExternal:    !strings.Contains(match[1], "/"),
			})
		}
	}

	// Multi-line import block
	importBlockRegex := regexp.MustCompile(`import\s*\(\s*([^)]+)\s*\)`)
	blocks := importBlockRegex.FindAllStringSubmatch(sourceCode, -1)
	for _, block := range blocks {
		if len(block) > 1 {
			// Extract individual imports from block
			lines := strings.Split(block[1], "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				// Remove quotes and alias
				packageRegex := regexp.MustCompile(`"([^"]+)"`)
				pkgMatch := packageRegex.FindStringSubmatch(line)
				if len(pkgMatch) > 1 {
					imports = append(imports, &models.PackageImport{
						SourceFile:    filePath,
						TargetPackage: pkgMatch[1],
						IsExternal:    strings.Contains(pkgMatch[1], "/"),
					})
				}
			}
		}
	}

	return imports
}

// extractPythonImports parses Python import statements
func extractPythonImports(filePath, sourceCode string) []*models.PackageImport {
	var imports []*models.PackageImport

	// Pattern: import package
	simpleImportRegex := regexp.MustCompile(`^\s*import\s+(\w+(?:\.\w+)*)`)
	// Pattern: from package import X, Y
	fromImportRegex := regexp.MustCompile(`^\s*from\s+(\w+(?:\.\w+)*)\s+import\s+(.+)`)

	lines := strings.Split(sourceCode, "\n")
	for _, line := range lines {
		// Try from...import first
		if match := fromImportRegex.FindStringSubmatch(line); len(match) > 2 {
			importedNames := strings.Split(match[2], ",")
			for i, name := range importedNames {
				importedNames[i] = strings.TrimSpace(name)
			}

			imports = append(imports, &models.PackageImport{
				SourceFile:    filePath,
				TargetPackage: match[1],
				ImportedNames: importedNames,
				IsExternal:    !strings.HasPrefix(match[1], "."),
			})
		} else if match := simpleImportRegex.FindStringSubmatch(line); len(match) > 1 {
			imports = append(imports, &models.PackageImport{
				SourceFile:    filePath,
				TargetPackage: match[1],
				IsExternal:    !strings.HasPrefix(match[1], "."),
			})
		}
	}

	return imports
}

// extractJavaImports parses Java/Kotlin/Scala import statements
func extractJavaImports(filePath, sourceCode string) []*models.PackageImport {
	var imports []*models.PackageImport

	// Pattern: import package.Class;
	importRegex := regexp.MustCompile(`import\s+([\w.]+);`)
	matches := importRegex.FindAllStringSubmatch(sourceCode, -1)

	for _, match := range matches {
		if len(match) > 1 {
			imports = append(imports, &models.PackageImport{
				SourceFile:    filePath,
				TargetPackage: match[1],
				IsExternal:    true, // Most imports in Java are external
			})
		}
	}

	return imports
}