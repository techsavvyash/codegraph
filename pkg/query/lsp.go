package query

import (
	"context"
	"fmt"

	"github.com/context-maximiser/code-graph/pkg/models"
	"github.com/context-maximiser/code-graph/pkg/neo4j"
)

// LSPService provides Language Server Protocol-like functionality
type LSPService struct {
	queryBuilder *neo4j.QueryBuilder
}

// NewLSPService creates a new LSP service
func NewLSPService(client *neo4j.Client) *LSPService {
	return &LSPService{
		queryBuilder: neo4j.NewQueryBuilder(client),
	}
}

// GoToDefinitionRequest represents a go-to-definition request
type GoToDefinitionRequest struct {
	Symbol   string `json:"symbol"`
	FilePath string `json:"filePath,omitempty"`
	Line     int    `json:"line,omitempty"`
	Column   int    `json:"column,omitempty"`
}

// GoToDefinitionResponse represents the response
type GoToDefinitionResponse struct {
	Symbol     *models.SCIPSymbol `json:"symbol"`
	Definition *models.SymbolInfo `json:"definition"`
	Found      bool               `json:"found"`
}

// FindReferencesRequest represents a find-references request  
type FindReferencesRequest struct {
	Symbol          string `json:"symbol"`
	IncludeDeclaration bool `json:"includeDeclaration"`
}

// FindReferencesResponse represents the response
type FindReferencesResponse struct {
	Symbol     *models.SCIPSymbol      `json:"symbol"`
	References []*models.SymbolReference `json:"references"`
	Count      int                     `json:"count"`
}

// FindImplementationsRequest represents a find-implementations request
type FindImplementationsRequest struct {
	InterfaceSymbol string `json:"interfaceSymbol"`
}

// FindImplementationsResponse represents the response
type FindImplementationsResponse struct {
	Interface       *models.SCIPSymbol `json:"interface"`
	Implementations []*models.Class    `json:"implementations"`
	Count           int                `json:"count"`
}

// GoToDefinition finds the definition of a symbol
func (lsp *LSPService) GoToDefinition(ctx context.Context, req GoToDefinitionRequest) (*GoToDefinitionResponse, error) {
	symbolInfo, err := lsp.queryBuilder.FindSymbolDefinition(ctx, req.Symbol)
	if err != nil {
		return &GoToDefinitionResponse{Found: false}, nil
	}

	return &GoToDefinitionResponse{
		Symbol:     symbolInfo.Symbol,
		Definition: symbolInfo,
		Found:      true,
	}, nil
}

// FindReferences finds all references to a symbol
func (lsp *LSPService) FindReferences(ctx context.Context, req FindReferencesRequest) (*FindReferencesResponse, error) {
	references, err := lsp.queryBuilder.FindAllReferences(ctx, req.Symbol)
	if err != nil {
		return nil, fmt.Errorf("failed to find references: %w", err)
	}

	// Parse the symbol
	scipSymbol, err := models.ParseSCIPSymbol(req.Symbol)
	if err != nil {
		return nil, fmt.Errorf("failed to parse symbol: %w", err)
	}

	// If includeDeclaration is true, add the definition as well
	if req.IncludeDeclaration {
		// TODO: Add the definition to the references list
	}

	return &FindReferencesResponse{
		Symbol:     scipSymbol,
		References: references,
		Count:      len(references),
	}, nil
}

// FindImplementations finds all implementations of an interface
func (lsp *LSPService) FindImplementations(ctx context.Context, req FindImplementationsRequest) (*FindImplementationsResponse, error) {
	implementations, err := lsp.queryBuilder.FindImplementations(ctx, req.InterfaceSymbol)
	if err != nil {
		return nil, fmt.Errorf("failed to find implementations: %w", err)
	}

	scipSymbol, err := models.ParseSCIPSymbol(req.InterfaceSymbol)
	if err != nil {
		return nil, fmt.Errorf("failed to parse interface symbol: %w", err)
	}

	return &FindImplementationsResponse{
		Interface:       scipSymbol,
		Implementations: implementations,
		Count:           len(implementations),
	}, nil
}

// SearchRequest represents a search request
type SearchRequest struct {
	Query     string   `json:"query"`
	NodeTypes []string `json:"nodeTypes,omitempty"`
	Limit     int      `json:"limit,omitempty"`
}

// SearchResult represents a search result item
type SearchResult struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Type        string            `json:"type"`
	FilePath    string            `json:"filePath,omitempty"`
	Signature   string            `json:"signature,omitempty"`
	Description string            `json:"description,omitempty"`
	Properties  map[string]any    `json:"properties,omitempty"`
}

// SearchResponse represents the search response
type SearchResponse struct {
	Query   string          `json:"query"`
	Results []*SearchResult `json:"results"`
	Count   int             `json:"count"`
	Limit   int             `json:"limit"`
}

// Search performs a search across code symbols
func (lsp *LSPService) Search(ctx context.Context, req SearchRequest) (*SearchResponse, error) {
	limit := req.Limit
	if limit == 0 {
		limit = 50
	}

	nodeTypes := req.NodeTypes
	if len(nodeTypes) == 0 {
		nodeTypes = []string{"Function", "Method", "Class", "Interface", "Variable"}
	}

	records, err := lsp.queryBuilder.SearchNodes(ctx, req.Query, nodeTypes, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to search nodes: %w", err)
	}

	var results []*SearchResult
	for _, record := range records {
		recordMap := record.AsMap()
		
		if node, ok := recordMap["n"]; ok {
			if nodeMap, ok := node.(map[string]any); ok {
				result := &SearchResult{
					Properties: nodeMap,
				}

				// Extract common properties
				if name, ok := nodeMap["name"].(string); ok {
					result.Name = name
				}
				if filePath, ok := nodeMap["filePath"].(string); ok {
					result.FilePath = filePath
				}
				if signature, ok := nodeMap["signature"].(string); ok {
					result.Signature = signature
				}
				if description, ok := nodeMap["description"].(string); ok {
					result.Description = description
				}

				// Extract node type from labels
				if labels, ok := recordMap["nodeLabels"].([]interface{}); ok && len(labels) > 0 {
					if label, ok := labels[0].(string); ok {
						result.Type = label
					}
				}

				results = append(results, result)
			}
		}
	}

	return &SearchResponse{
		Query:   req.Query,
		Results: results,
		Count:   len(results),
		Limit:   limit,
	}, nil
}

// CompletionRequest represents a code completion request
type CompletionRequest struct {
	FilePath string `json:"filePath"`
	Line     int    `json:"line"`
	Column   int    `json:"column"`
	Prefix   string `json:"prefix"`
}

// CompletionItem represents a completion item
type CompletionItem struct {
	Label         string `json:"label"`
	Kind          string `json:"kind"`
	Detail        string `json:"detail,omitempty"`
	Documentation string `json:"documentation,omitempty"`
	InsertText    string `json:"insertText,omitempty"`
}

// CompletionResponse represents the completion response
type CompletionResponse struct {
	Items []CompletionItem `json:"items"`
	Count int              `json:"count"`
}

// GetCompletion provides code completion suggestions
func (lsp *LSPService) GetCompletion(ctx context.Context, req CompletionRequest) (*CompletionResponse, error) {
	// For now, implement a simple completion based on searching for symbols
	// that start with the prefix
	searchReq := SearchRequest{
		Query:     req.Prefix,
		NodeTypes: []string{"Function", "Method", "Variable", "Class"},
		Limit:     20,
	}

	searchResp, err := lsp.Search(ctx, searchReq)
	if err != nil {
		return nil, fmt.Errorf("failed to get completions: %w", err)
	}

	var items []CompletionItem
	for _, result := range searchResp.Results {
		item := CompletionItem{
			Label:      result.Name,
			Kind:       result.Type,
			Detail:     result.Signature,
			InsertText: result.Name,
		}

		// Set documentation if available
		if docstring, ok := result.Properties["docstring"].(string); ok && docstring != "" {
			item.Documentation = docstring
		}

		items = append(items, item)
	}

	return &CompletionResponse{
		Items: items,
		Count: len(items),
	}, nil
}

// HoverRequest represents a hover request
type HoverRequest struct {
	FilePath string `json:"filePath"`
	Line     int    `json:"line"`
	Column   int    `json:"column"`
}

// HoverResponse represents hover information
type HoverResponse struct {
	Content   string `json:"content"`
	Range     *Range `json:"range,omitempty"`
	Found     bool   `json:"found"`
}

// Range represents a text range
type Range struct {
	StartLine   int `json:"startLine"`
	StartColumn int `json:"startColumn"`
	EndLine     int `json:"endLine"`
	EndColumn   int `json:"endColumn"`
}

// GetHover provides hover information for a symbol at a position
func (lsp *LSPService) GetHover(ctx context.Context, req HoverRequest) (*HoverResponse, error) {
	// This is a simplified implementation
	// In a full implementation, we would need to map file positions to symbols
	// For now, return a placeholder response
	return &HoverResponse{
		Content: "Hover information not yet implemented",
		Found:   false,
	}, nil
}