package static

import (
	"bufio"
	"fmt"
	"os"
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

// ExtractDocuments extracts file information from the SCIP index
func (sp *SCIPParser) ExtractDocuments() ([]*models.File, error) {
	if sp.index == nil {
		return nil, fmt.Errorf("no SCIP index loaded")
	}

	var files []*models.File

	for _, doc := range sp.index.Documents {
		file := &models.File{
			Path:     doc.RelativePath,
			Language: inferLanguage(doc.RelativePath),
			// Note: SCIP doesn't provide file size, line count, or hash
			// These would need to be computed separately if needed
		}

		files = append(files, file)
	}

	return files, nil
}

// GetServiceInfo extracts service information from SCIP metadata
func (sp *SCIPParser) GetServiceInfo() (*models.Service, error) {
	metadata := sp.GetMetadata()
	if metadata == nil {
		return nil, fmt.Errorf("no metadata found")
	}

	service := &models.Service{
		Name:     metadata.ProjectRoot,
		Language: "Go", // We assume Go for scip-go
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
	if strings.HasSuffix(filePath, ".go") {
		return "Go"
	} else if strings.HasSuffix(filePath, ".java") {
		return "Java"
	} else if strings.HasSuffix(filePath, ".py") {
		return "Python"
	} else if strings.HasSuffix(filePath, ".ts") || strings.HasSuffix(filePath, ".js") {
		return "TypeScript"
	}
	return "unknown"
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