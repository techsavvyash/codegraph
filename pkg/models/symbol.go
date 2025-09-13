package models

import (
	"fmt"
	"strings"
)

// SCIPSymbol represents a SCIP (Source Code Intelligence Protocol) symbol
// Format: <scheme> <manager> <name> <version> <descriptor>
// Example: scip-go go github.com/context-maximiser/code-graph v1.0.0 pkg/models/Symbol#
type SCIPSymbol struct {
	Scheme     string `json:"scheme"`     // scip-go, scip-java, scip-python, etc.
	Manager    string `json:"manager"`    // go, maven, npm, pip, etc.
	Name       string `json:"name"`       // package/repository name
	Version    string `json:"version"`    // version string
	Descriptor string `json:"descriptor"` // path to symbol within package
}

// String returns the SCIP symbol as a formatted string
func (s *SCIPSymbol) String() string {
	return fmt.Sprintf("%s %s %s %s %s", s.Scheme, s.Manager, s.Name, s.Version, s.Descriptor)
}

// ParseSCIPSymbol parses a SCIP symbol string into components
func ParseSCIPSymbol(symbol string) (*SCIPSymbol, error) {
	parts := strings.SplitN(symbol, " ", 5)
	if len(parts) != 5 {
		return nil, fmt.Errorf("invalid SCIP symbol format: %s", symbol)
	}

	return &SCIPSymbol{
		Scheme:     parts[0],
		Manager:    parts[1],
		Name:       parts[2],
		Version:    parts[3],
		Descriptor: parts[4],
	}, nil
}

// NewGoSCIPSymbol creates a SCIP symbol for Go code
func NewGoSCIPSymbol(packageName, version, descriptor string) *SCIPSymbol {
	return &SCIPSymbol{
		Scheme:     "scip-go",
		Manager:    "go",
		Name:       packageName,
		Version:    version,
		Descriptor: descriptor,
	}
}

// GoDescriptor represents different Go symbol descriptors
type GoDescriptor struct {
	Package   string
	Type      string
	Method    string
	Field     string
	Local     string
	Parameter string
}

// String formats the Go descriptor according to SCIP conventions
func (d *GoDescriptor) String() string {
	var parts []string

	if d.Package != "" {
		parts = append(parts, d.Package)
	}

	if d.Type != "" {
		parts = append(parts, d.Type+"#")
	}

	if d.Method != "" {
		parts = append(parts, d.Method+"().")
	}

	if d.Field != "" {
		parts = append(parts, d.Field+".")
	}

	if d.Local != "" {
		parts = append(parts, "local "+d.Local)
	}

	if d.Parameter != "" {
		parts = append(parts, "param "+d.Parameter)
	}

	return strings.Join(parts, "")
}

// SymbolKind represents different kinds of symbols
type SymbolKind string

const (
	PackageSymbol   SymbolKind = "Package"
	TypeSymbol      SymbolKind = "Type"
	MethodSymbol    SymbolKind = "Method"
	FunctionSymbol  SymbolKind = "Function"
	FieldSymbol     SymbolKind = "Field"
	VariableSymbol  SymbolKind = "Variable"
	ConstantSymbol  SymbolKind = "Constant"
	InterfaceSymbol SymbolKind = "Interface"
	ParameterSymbol SymbolKind = "Parameter"
	LocalSymbol     SymbolKind = "Local"
)

// SymbolScope represents the scope/visibility of a symbol
type SymbolScope string

const (
	PublicScope    SymbolScope = "public"
	PrivateScope   SymbolScope = "private"
	ProtectedScope SymbolScope = "protected"
	PackageScope   SymbolScope = "package"
	LocalScope     SymbolScope = "local"
)

// SymbolInfo represents metadata about a code symbol
type SymbolInfo struct {
	Symbol         *SCIPSymbol `json:"symbol"`
	Kind           SymbolKind  `json:"kind"`
	Scope          SymbolScope `json:"scope"`
	DisplayName    string      `json:"displayName"`
	Documentation  string      `json:"documentation"`
	Signature      string      `json:"signature"`
	ReturnType     string      `json:"returnType,omitempty"`
	Parameters     []Parameter `json:"parameters,omitempty"`
	FilePath       string      `json:"filePath"`
	StartLine      int         `json:"startLine"`
	EndLine        int         `json:"endLine"`
	StartColumn    int         `json:"startColumn"`
	EndColumn      int         `json:"endColumn"`
}

// IsExported returns true if the symbol is exported (public)
func (si *SymbolInfo) IsExported() bool {
	return si.Scope == PublicScope
}

// GenerateSymbolID generates a unique ID for the symbol (for use as Neo4j node ID)
func (si *SymbolInfo) GenerateSymbolID() string {
	if si.Symbol != nil {
		return si.Symbol.String()
	}
	// Fallback: use file path and position
	return fmt.Sprintf("%s:%d:%d", si.FilePath, si.StartLine, si.StartColumn)
}

// SymbolReference represents a reference to a symbol in code
type SymbolReference struct {
	Symbol      *SCIPSymbol `json:"symbol"`
	FilePath    string      `json:"filePath"`
	StartLine   int         `json:"startLine"`
	EndLine     int         `json:"endLine"`
	StartColumn int         `json:"startColumn"`
	EndColumn   int         `json:"endColumn"`
	IsDefinition bool       `json:"isDefinition"`
	Context     string      `json:"context"` // surrounding code context
}

// SymbolDefinition represents a symbol definition
type SymbolDefinition struct {
	Symbol *SCIPSymbol  `json:"symbol"`
	Info   *SymbolInfo  `json:"info"`
	Refs   []*SymbolReference `json:"references"`
}

// AddReference adds a reference to this symbol definition
func (sd *SymbolDefinition) AddReference(ref *SymbolReference) {
	sd.Refs = append(sd.Refs, ref)
}

// GetDefinitionReference returns the definition reference (where IsDefinition is true)
func (sd *SymbolDefinition) GetDefinitionReference() *SymbolReference {
	for _, ref := range sd.Refs {
		if ref.IsDefinition {
			return ref
		}
	}
	return nil
}

// GetUsageReferences returns all usage references (where IsDefinition is false)
func (sd *SymbolDefinition) GetUsageReferences() []*SymbolReference {
	var usages []*SymbolReference
	for _, ref := range sd.Refs {
		if !ref.IsDefinition {
			usages = append(usages, ref)
		}
	}
	return usages
}