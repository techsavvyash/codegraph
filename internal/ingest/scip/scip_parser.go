package static

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	models "github.com/context-maximiser/code-graph/internal/model"
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

// ExtractSymbols extracts all symbol information from the SCIP index.
// projectPath is the root used to resolve document paths when refining
// descriptor-based kinds against each file's parse tree (see
// promoteDeclaratorBoundFunctions); pass "" to skip that refinement.
func (sp *SCIPParser) ExtractSymbols(projectPath string) ([]*models.SymbolDefinition, error) {
	if sp.index == nil {
		return nil, fmt.Errorf("no SCIP index loaded")
	}

	// Use a map for O(1) lookups instead of O(n) linear search
	symbolMap := make(map[string]*models.SymbolDefinition)

	// Identify symbols that are targets of impl relationships — these are
	// interfaces (or interface members) and need refined kind classification.
	interfaceLike := sp.collectInterfaceLikeSymbols()

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

		symbolMap[scipSymbol.String()] = symbolDef
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
				Symbol:       scipSymbol,
				FilePath:     filePath,
				StartLine:    startLine,
				EndLine:      endLine,
				StartColumn:  startColumn,
				EndColumn:    endColumn,
				IsDefinition: occurrence.SymbolRoles&int32(scip.SymbolRole_Definition) != 0,
			}

			// Find or create the symbol definition using map lookup (O(1))
			symbolKey := scipSymbol.String()
			targetSymbolDef, exists := symbolMap[symbolKey]

			if !exists {
				// Create new symbol definition. Only stamp the location
				// (FilePath/StartLine/...) when this occurrence is the
				// actual definition. Reference-only occurrences leave the
				// location empty so the indexer doesn't create a typed
				// definition node (Class/Method/Variable/...) for symbols
				// that are merely *referenced* from this project — e.g.
				// `console`/`console.log` in TypeScript or `fmt.Println`
				// in Go. Those still get a Symbol node + Reference nodes,
				// just no fake "Variable named console" definition.
				info := &models.SymbolInfo{
					Symbol:      scipSymbol,
					Kind:        inferSymbolKindWith(occurrence.Symbol, interfaceLike),
					DisplayName: extractDisplayName(occurrence.Symbol),
					Signature:   occurrence.Symbol, // SCIP symbol string drives unique nodeKey
				}
				if ref.IsDefinition {
					info.FilePath = filePath
					info.StartLine = startLine
					info.EndLine = endLine
					info.StartColumn = startColumn
					info.EndColumn = endColumn
				}
				targetSymbolDef = &models.SymbolDefinition{
					Symbol: scipSymbol,
					Info:   info,
					Refs:   []*models.SymbolReference{},
				}
				symbolMap[symbolKey] = targetSymbolDef
			} else if ref.IsDefinition {
				// Update location to the actual definition site. A symbol
				// can have multiple def-occurrences (e.g. scip-go emits a
				// def at the interface method's declaration AND at every
				// implementation site). To stay deterministic regardless
				// of iteration / output order, we always pick the def
				// with the smallest (filePath, startLine, startColumn).
				cur := targetSymbolDef.Info
				curEmpty := cur.FilePath == ""
				better := !curEmpty && isLocationLess(filePath, startLine, startColumn, cur.FilePath, cur.StartLine, cur.StartColumn)
				if curEmpty || better {
					cur.FilePath = filePath
					cur.StartLine = startLine
					cur.EndLine = endLine
					cur.StartColumn = startColumn
					cur.EndColumn = endColumn
				}
			}

			// Add reference to symbol definition
			targetSymbolDef.AddReference(ref)
		}
	}

	// Convert map to slice
	symbolDefs := make([]*models.SymbolDefinition, 0, len(symbolMap))
	for _, symbolDef := range symbolMap {
		symbolDefs = append(symbolDefs, symbolDef)
	}

	promoteDeclaratorBoundFunctions(symbolDefs, projectPath)

	return symbolDefs, nil
}

// isLocationLess reports whether (fileA, lineA, colA) sorts before
// (fileB, lineB, colB) by lexicographic (file, line, col).
func isLocationLess(fileA string, lineA, colA int, fileB string, lineB, colB int) bool {
	if fileA != fileB {
		return fileA < fileB
	}
	if lineA != lineB {
		return lineA < lineB
	}
	return colA < colB
}

// shouldExcludePath checks if a file path should be excluded from indexing.
// We exclude generated/build/dependency directories and test fixture trees
// because they are not part of the production logic graph users want to
// reason about. Real test files (e.g. *_test.go, src/**/*.test.ts) live
// alongside the code they cover and are kept.
func shouldExcludePath(path string) bool {
	excludedDirs := []string{
		// Dependencies / generated.
		"node_modules/", "vendor/", ".git/",
		".next/", ".nuxt/", ".svelte-kit/",
		// Build output.
		"dist/", "build/",
		"target/", // Maven/Gradle build output.
		// Python.
		"venv/", ".venv/", "__pycache__/",
		// Test fixture data — sample inputs, not source.
		"testdata/", "fixtures/",
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
	// scip-typescript can emit the same RelativePath twice when a source file
	// is referenced via multiple module-resolution paths; without dedup,
	// downstream createFileNode runs twice and CreateRelationship produces a
	// duplicate Service→File CONTAINS edge (visible as Service→File rels=2 in
	// Neo4j). We dedupe at this seam so every callsite below benefits.
	seenPaths := make(map[string]bool)

	for _, doc := range sp.index.Documents {
		if shouldExcludePath(doc.RelativePath) {
			excludedCount++
			continue
		}
		if seenPaths[doc.RelativePath] {
			continue
		}
		seenPaths[doc.RelativePath] = true

		file := &models.File{
			Path:     doc.RelativePath,
			Language: inferLanguage(doc.RelativePath),
		}
		files = append(files, file)
	}

	if excludedCount > 0 {
		fmt.Printf("Filtered out %d/%d files from excluded directories (node_modules, vendor, fixtures, .svelte-kit, etc.)\n", excludedCount, totalDocs)
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

// inferSymbolKind classifies a SCIP symbol from its descriptor alone.
// Calls without an interfaceLike set cannot distinguish:
//   - Interface vs Class (both end "#")
//   - Interface method vs struct field (both end "." with a "#" parent)
//
// Use inferSymbolKindWith when the SCIP relationships are available.
func inferSymbolKind(symbol string) models.SymbolKind {
	return inferSymbolKindWith(symbol, nil)
}

// inferSymbolKindWith refines kind classification using the set of symbols that
// are targets of is_implementation_of relationships (i.e. "interface-like").
// In Go, scip-go does not tag interfaces explicitly — we infer them from the
// fact that something implements them.
func inferSymbolKindWith(symbol string, interfaceLike map[string]bool) models.SymbolKind {
	descriptor := scipDescriptor(symbol)

	switch {
	case strings.HasSuffix(descriptor, "/"):
		return models.PackageSymbol

	case strings.HasSuffix(descriptor, "()."):
		if strings.Contains(descriptor, "#") {
			return models.MethodSymbol
		}
		return models.FunctionSymbol

	case strings.HasSuffix(descriptor, "#"):
		if interfaceLike[symbol] {
			return models.InterfaceSymbol
		}
		return models.TypeSymbol

	case strings.HasSuffix(descriptor, "."):
		// Term descriptor: a "." child is either a struct field or an
		// interface method — decided by whether anything implements the
		// member itself, or failing that whether its PARENT type is a known
		// interface (covers methods of interfaces nothing implements, whose
		// members have no is_implementation_of relationships either).
		if strings.Contains(descriptor, "#") {
			if interfaceLike[symbol] || interfaceLike[parentTypeSymbol(symbol)] {
				return models.MethodSymbol
			}
			return models.FieldSymbol
		}
		return models.VariableSymbol
	}

	return models.VariableSymbol
}

// parentTypeSymbol returns the enclosing type's symbol for a member symbol:
// `…/Storer#Save.` → `…/Storer#`. Empty when the symbol has no '#'.
func parentTypeSymbol(symbol string) string {
	idx := strings.LastIndex(symbol, "#")
	if idx < 0 {
		return ""
	}
	return symbol[:idx+1]
}

// scipDescriptor returns the descriptor portion of a SCIP symbol string.
// SCIP format is: "scheme manager name version descriptor" (5 space-separated
// parts). Falls back to the whole symbol for shorter forms like "local 0".
func scipDescriptor(symbol string) string {
	parts := strings.Split(symbol, " ")
	if len(parts) >= 5 {
		return parts[len(parts)-1]
	}
	return symbol
}

// collectInterfaceLikeSymbols returns the set of SCIP symbols that appear as
// the target of any is_implementation_of relationship anywhere in the index.
// In Go semantics, a type that something implements is by definition an
// interface; an interface method is the corresponding "."-terminated target.
func (sp *SCIPParser) collectInterfaceLikeSymbols() map[string]bool {
	out := make(map[string]bool)
	if sp.index == nil {
		return out
	}
	add := func(infos []*scip.SymbolInformation) {
		for _, info := range infos {
			for _, rel := range info.Relationships {
				if rel.IsImplementation && rel.Symbol != "" {
					out[rel.Symbol] = true
				}
			}
		}
	}
	add(sp.index.ExternalSymbols)
	for _, doc := range sp.index.Documents {
		add(doc.Symbols)
	}

	// Second signal: scip-go's hover documentation embeds the declaration
	// form ("```go\ntype Numeric interface\n```") generated from the AST.
	// Interfaces nothing implements — generic constraint interfaces above
	// all (structural constraint satisfaction is compiler-resolved, never a
	// nominal is_implementation_of) — have no impl relationships and would
	// otherwise fall through to the Class classification.
	addDocDeclared := func(infos []*scip.SymbolInformation) {
		for _, info := range infos {
			if symbolDocsDeclareGoInterface(info.Documentation) {
				out[info.Symbol] = true
			}
		}
	}
	addDocDeclared(sp.index.ExternalSymbols)
	for _, doc := range sp.index.Documents {
		addDocDeclared(doc.Symbols)
	}
	return out
}

// symbolDocsDeclareGoInterface reports whether any hover-documentation entry
// contains a Go interface type declaration. scip-go emits these fenced
// snippets verbatim from the type's AST, so the match is against generated
// text with a fixed shape, not free-form prose.
func symbolDocsDeclareGoInterface(docs []string) bool {
	for _, d := range docs {
		if strings.HasPrefix(d, "```go\ntype ") && strings.Contains(d, " interface") {
			return true
		}
	}
	return false
}

// extractDisplayName returns the human-readable name of a SCIP symbol.
// Examples (with surrounding scheme prefix omitted for brevity):
//
//	`pkg`/Greeter#                  → "Greeter"
//	`pkg`/EnglishGreeter#Greet().   → "Greet"
//	`pkg`/EnglishGreeter#Prefix.    → "Prefix"
//	`pkg`/main().                   → "main"
//	`pkg`/                          → "pkg"  (last segment, backticks stripped)
//	`pkg`/Foo#`<constructor>`().    → "<constructor>"
//	`pkg`/greet().(logger)          → "logger"  (parameter descriptor)
func extractDisplayName(symbol string) string {
	descriptor := scipDescriptor(symbol)
	name := descriptor

	// Parameter descriptor `(<name>)` always ends in ")" but is NOT a method
	// terminator (those end in "()." or bare "()"). Extract the parameter name
	// from between the last `(` and trailing `)`.
	if strings.HasSuffix(name, ")") && !strings.HasSuffix(name, "().") && !strings.HasSuffix(name, "()") {
		if i := strings.LastIndex(name, "("); i >= 0 {
			return strings.Trim(name[i+1:len(name)-1], "`")
		}
	}

	// Strip the terminator so the remaining name is at the trailing edge.
	// SCIP method/function descriptors normally end "()." but some legacy
	// inputs end "()"; handle both. ":" is the meta descriptor terminator
	// (used by scip-typescript for object-literal property keys).
	switch {
	case strings.HasSuffix(name, "()."):
		name = strings.TrimSuffix(name, "().")
	case strings.HasSuffix(name, "()"):
		name = strings.TrimSuffix(name, "()")
	case strings.HasSuffix(name, "/"):
		name = strings.TrimSuffix(name, "/")
	case strings.HasSuffix(name, "#"):
		name = strings.TrimSuffix(name, "#")
	case strings.HasSuffix(name, "."):
		name = strings.TrimSuffix(name, ".")
	case strings.HasSuffix(name, ":"):
		name = strings.TrimSuffix(name, ":")
	}

	// Member-of-type wins: take what follows the last "#" if present.
	// Trim backticks since names like `<constructor>` are wrapped in them.
	if i := strings.LastIndex(name, "#"); i >= 0 {
		return strings.Trim(name[i+1:], "`")
	}
	// SCIP wraps multi-segment package paths in backticks: `a/b/c`. After
	// trimming a trailing terminator we may be left with the bare backtick
	// form — strip surrounds and treat the inside as a path.
	if strings.HasPrefix(name, "`") && strings.HasSuffix(name, "`") {
		name = strings.Trim(name, "`")
	}
	// Take the trailing path segment.
	if i := strings.LastIndex(name, "/"); i >= 0 {
		return strings.Trim(name[i+1:], "`")
	}
	return strings.Trim(name, "`")
}

func extractSignature(symbolInfo *scip.SymbolInformation) string {
	// For now, use the display name as signature
	// In a full implementation, we might extract more detailed signature info
	return symbolInfo.Symbol
}

// convertRange converts a raw SCIP occurrence range into (line, column).
//
// Line convention: SCIP ranges are 0-based on the line axis. This function is
// the single conversion point for the entire graph — it returns 1-based lines
// (the editor/AST convention used everywhere else: Go's token.FileSet,
// findEnclosingCaller's AST body ranges, calculateByteOffsets, etc.). Every
// downstream consumer of Symbol/Function/Method/Variable/Reference
// startLine/endLine relies on this having already happened; nothing past this
// point should add another +1.
//
// Column convention: SCIP columns are 0-based and are passed through
// unchanged — columns are not touched by this conversion.
func convertRange(scipRange []int32, isStart bool) (int, int) {
	// SCIP ranges come in two forms:
	// 3-element: [startLine, startCol, endCol] (single-line span)
	// 4-element: [startLine, startCol, endLine, endCol] (multi-line span)
	if len(scipRange) < 3 {
		return 0, 0
	}

	if isStart {
		return int(scipRange[0]) + 1, int(scipRange[1])
	}

	// End position
	if len(scipRange) == 3 {
		// Single-line: endLine == startLine, endCol is scipRange[2]
		return int(scipRange[0]) + 1, int(scipRange[2])
	}
	return int(scipRange[2]) + 1, int(scipRange[3])
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
