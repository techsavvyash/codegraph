package neo4j

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/context-maximiser/code-graph/pkg/models"
	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// QueryBuilder helps build Cypher queries programmatically
type QueryBuilder struct {
	client *Client
}

// NewQueryBuilder creates a new query builder
func NewQueryBuilder(client *Client) *QueryBuilder {
	return &QueryBuilder{client: client}
}

// FindNodesByLabel finds all nodes with a specific label
func (qb *QueryBuilder) FindNodesByLabel(ctx context.Context, label string, limit int) ([]*neo4j.Record, error) {
	cypher := fmt.Sprintf("MATCH (n:%s) RETURN n", label)
	if limit > 0 {
		cypher += fmt.Sprintf(" LIMIT %d", limit)
	}

	result, err := qb.client.ExecuteQuery(ctx, cypher, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to find nodes by label %s: %w", label, err)
	}

	return result, nil
}

// FindNodeByProperty finds nodes by a specific property value
func (qb *QueryBuilder) FindNodeByProperty(ctx context.Context, label, property string, value any) ([]*neo4j.Record, error) {
	cypher := fmt.Sprintf("MATCH (n:%s {%s: $value}) RETURN n", label, property)
	params := map[string]any{"value": value}

	result, err := qb.client.ExecuteQuery(ctx, cypher, params)
	if err != nil {
		return nil, fmt.Errorf("failed to find node by property %s=%v: %w", property, value, err)
	}

	return result, nil
}

// FindSymbolDefinition finds the definition of a symbol by its SCIP identifier
func (qb *QueryBuilder) FindSymbolDefinition(ctx context.Context, symbol string) (*models.SymbolInfo, error) {
	cypher := `
		MATCH (s:Symbol {symbol: $symbol})<-[:DEFINES]-(definition)
		RETURN 
			labels(definition) AS nodeType,
			definition.name AS name,
			definition.signature AS signature,
			definition.filePath AS filePath,
			definition.startLine AS startLine,
			definition.endLine AS endLine,
			properties(definition) AS allProperties
	`

	params := map[string]any{"symbol": symbol}
	result, err := qb.client.ExecuteQuery(ctx, cypher, params)
	if err != nil {
		return nil, fmt.Errorf("failed to find symbol definition: %w", err)
	}

	if len(result) == 0 {
		return nil, fmt.Errorf("symbol definition not found: %s", symbol)
	}

	record := result[0]
	recordMap := record.AsMap()

	// Parse the SCIP symbol
	scipSymbol, err := models.ParseSCIPSymbol(symbol)
	if err != nil {
		return nil, fmt.Errorf("failed to parse SCIP symbol: %w", err)
	}

	// Extract symbol info
	symbolInfo := &models.SymbolInfo{
		Symbol:      scipSymbol,
		DisplayName: getString(recordMap, "name"),
		Signature:   getString(recordMap, "signature"),
		FilePath:    getString(recordMap, "filePath"),
		StartLine:   getInt(recordMap, "startLine"),
		EndLine:     getInt(recordMap, "endLine"),
	}

	// Determine symbol kind from node labels
	if labels, ok := recordMap["nodeType"].([]interface{}); ok {
		for _, label := range labels {
			if labelStr, ok := label.(string); ok {
				switch labelStr {
				case "Function":
					symbolInfo.Kind = models.FunctionSymbol
				case "Method":
					symbolInfo.Kind = models.MethodSymbol
				case "Class":
					symbolInfo.Kind = models.TypeSymbol
				case "Interface":
					symbolInfo.Kind = models.InterfaceSymbol
				case "Variable":
					symbolInfo.Kind = models.VariableSymbol
				case "Parameter":
					symbolInfo.Kind = models.ParameterSymbol
				}
			}
		}
	}

	return symbolInfo, nil
}

// FindAllReferences finds all references to a symbol
func (qb *QueryBuilder) FindAllReferences(ctx context.Context, symbol string) ([]*models.SymbolReference, error) {
	cypher := `
		MATCH (s:Symbol {symbol: $symbol})<-[:REFERENCES]-(usage)
		MATCH (usage)<-[:CONTAINS*]-(file:File)
		RETURN 
			usage.name AS usageName,
			usage.startLine AS startLine,
			usage.endLine AS endLine,
			usage.startColumn AS startColumn,
			usage.endColumn AS endColumn,
			file.path AS filePath
		ORDER BY file.path, startLine
	`

	params := map[string]any{"symbol": symbol}
	result, err := qb.client.ExecuteQuery(ctx, cypher, params)
	if err != nil {
		return nil, fmt.Errorf("failed to find symbol references: %w", err)
	}

	scipSymbol, err := models.ParseSCIPSymbol(symbol)
	if err != nil {
		return nil, fmt.Errorf("failed to parse SCIP symbol: %w", err)
	}

	var references []*models.SymbolReference
	for _, record := range result {
		recordMap := record.AsMap()
		
		ref := &models.SymbolReference{
			Symbol:      scipSymbol,
			FilePath:    getString(recordMap, "filePath"),
			StartLine:   getInt(recordMap, "startLine"),
			EndLine:     getInt(recordMap, "endLine"),
			StartColumn: getInt(recordMap, "startColumn"),
			EndColumn:   getInt(recordMap, "endColumn"),
			IsDefinition: false, // These are usage references
		}
		references = append(references, ref)
	}

	return references, nil
}

// FindImplementations finds all classes that implement an interface
func (qb *QueryBuilder) FindImplementations(ctx context.Context, interfaceSymbol string) ([]*models.Class, error) {
	cypher := `
		MATCH (interfaceSymbol:Symbol {symbol: $interfaceSymbol})
		MATCH (interfaceSymbol)<-[:DEFINES]-(interfaceNode:Interface)
		MATCH (interfaceNode)<-[:IMPLEMENTS]-(classNode:Class)
		RETURN 
			classNode.name AS className,
			classNode.fqn AS fullyQualifiedName,
			classNode.filePath AS filePath,
			classNode.startLine AS startLine,
			classNode.endLine AS endLine
	`

	params := map[string]any{"interfaceSymbol": interfaceSymbol}
	result, err := qb.client.ExecuteQuery(ctx, cypher, params)
	if err != nil {
		return nil, fmt.Errorf("failed to find implementations: %w", err)
	}

	var classes []*models.Class
	for _, record := range result {
		recordMap := record.AsMap()
		
		class := &models.Class{
			Name:      getString(recordMap, "className"),
			FQN:       getString(recordMap, "fullyQualifiedName"),
			FilePath:  getString(recordMap, "filePath"),
			StartLine: getInt(recordMap, "startLine"),
			EndLine:   getInt(recordMap, "endLine"),
		}
		classes = append(classes, class)
	}

	return classes, nil
}

// FindAPIEndpointsAffectedByFunction performs impact analysis
func (qb *QueryBuilder) FindAPIEndpointsAffectedByFunction(ctx context.Context, functionSymbol string) ([]*models.APIRoute, error) {
	cypher := `
		MATCH (startFunc)-[:DEFINES]->(:Symbol {symbol: $functionSymbol})
		WHERE startFunc:Function OR startFunc:Method
		
		// Find all functions and methods called by startFunc, up to 10 levels deep
		MATCH (startFunc)-[:CALLS*1..10]->(downstream)
		WHERE downstream:Function OR downstream:Method
		
		// From the set of downstream functions, find any that directly handle an API route
		MATCH (downstream)-[:EXPOSES_API]->(route:APIRoute)
		
		RETURN DISTINCT
			route.protocol AS protocol,
			route.method AS httpMethod,
			route.path AS apiPath,
			route.description AS description
	`

	params := map[string]any{"functionSymbol": functionSymbol}
	result, err := qb.client.ExecuteQuery(ctx, cypher, params)
	if err != nil {
		return nil, fmt.Errorf("failed to find affected API endpoints: %w", err)
	}

	var routes []*models.APIRoute
	for _, record := range result {
		recordMap := record.AsMap()
		
		route := &models.APIRoute{
			Protocol:    getString(recordMap, "protocol"),
			Method:      getString(recordMap, "httpMethod"),
			Path:        getString(recordMap, "apiPath"),
			Description: getString(recordMap, "description"),
		}
		routes = append(routes, route)
	}

	return routes, nil
}

// TraceDataFlow traces the flow of data from a parameter to function calls
func (qb *QueryBuilder) TraceDataFlow(ctx context.Context, paramSymbol string) ([]*models.SymbolReference, error) {
	cypher := `
		MATCH (param:Parameter)-[:DEFINES]->(:Symbol {symbol: $paramSymbol})
		
		// Follow the data flow path through intermediate variables
		MATCH path = (param)-[:FLOWS_TO*1..15]->(usage)
		
		// Identify if the final usage is a parameter in another function call
		MATCH (usage:Parameter)<-[:CONTAINS]-(call:Method)
		WHERE usage:Parameter
		
		RETURN 
			call.name AS receivingMethod,
			call.signature AS receivingMethodSignature,
			nodes(path) AS dataFlowPath
	`

	params := map[string]any{"paramSymbol": paramSymbol}
	result, err := qb.client.ExecuteQuery(ctx, cypher, params)
	if err != nil {
		return nil, fmt.Errorf("failed to trace data flow: %w", err)
	}

	scipSymbol, err := models.ParseSCIPSymbol(paramSymbol)
	if err != nil {
		return nil, fmt.Errorf("failed to parse SCIP symbol: %w", err)
	}

	var references []*models.SymbolReference
	for _, record := range result {
		recordMap := record.AsMap()
		
		ref := &models.SymbolReference{
			Symbol:  scipSymbol,
			Context: getString(recordMap, "receivingMethod"),
		}
		references = append(references, ref)
	}

	return references, nil
}

// DiscoverServiceDependencies finds all external service dependencies
func (qb *QueryBuilder) DiscoverServiceDependencies(ctx context.Context, serviceName string) ([]map[string]any, error) {
	cypher := `
		MATCH (s:Service {name: $serviceName})
		
		// Find all functions/methods defined within this service
		MATCH (s)-[:CONTAINS*]->(caller)
		WHERE caller:Function OR caller:Method
		
		// Find all calls originating from these functions to symbols in other services
		MATCH (caller)-[:CALLS]->()-[:DEFINES]->(symbol:Symbol)
		// SCIP symbols for external packages contain the package name
		// We filter out internal calls by checking that the symbol does not contain the current service's name
		WHERE symbol.symbol CONTAINS " " AND NOT symbol.symbol CONTAINS $serviceName
		
		// Extract the foreign service name from the SCIP symbol string
		WITH caller, split(symbol.symbol, ' ') AS symbolParts, symbol
		RETURN DISTINCT
			symbolParts[2] AS foreignServiceName,
			caller.name AS callingFunction,
			symbol.symbol AS targetSymbol
		ORDER BY foreignServiceName, callingFunction
	`

	params := map[string]any{"serviceName": serviceName}
	result, err := qb.client.ExecuteQuery(ctx, cypher, params)
	if err != nil {
		return nil, fmt.Errorf("failed to discover service dependencies: %w", err)
	}

	var dependencies []map[string]any
	for _, record := range result {
		dependencies = append(dependencies, record.AsMap())
	}

	return dependencies, nil
}

// Helper functions to safely extract values from record maps
func getString(m map[string]any, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func getInt(m map[string]any, key string) int {
	if v, ok := m[key]; ok {
		if i, ok := v.(int64); ok {
			return int(i)
		}
		if i, ok := v.(int); ok {
			return i
		}
	}
	return 0
}

// SearchNodes performs a full-text search across nodes
func (qb *QueryBuilder) SearchNodes(ctx context.Context, searchTerm string, nodeTypes []string, limit int) ([]*neo4j.Record, error) {
	// Build the label filter
	var labelFilters []string
	for _, nodeType := range nodeTypes {
		labelFilters = append(labelFilters, fmt.Sprintf("n:%s", nodeType))
	}
	
	var cypher string
	if len(labelFilters) > 0 {
		labelFilter := strings.Join(labelFilters, " OR ")
		cypher = fmt.Sprintf(`
			MATCH (n)
			WHERE (%s) AND (
				toLower(n.name) CONTAINS toLower($searchTerm) OR
				toLower(n.displayName) CONTAINS toLower($searchTerm) OR
				toLower(n.signature) CONTAINS toLower($searchTerm) OR
				toLower(n.symbol) CONTAINS toLower($searchTerm) OR
				toLower(n.path) CONTAINS toLower($searchTerm)
			)
			RETURN n, labels(n) AS nodeLabels
			ORDER BY 
				CASE 
					WHEN n:Function OR n:Method THEN 1
					WHEN n:Class OR n:Interface THEN 2
					WHEN n:Variable OR n:Parameter THEN 3
					WHEN n:File OR n:Feature OR n:Document THEN 4
					WHEN n:Symbol THEN 5
					ELSE 6
				END,
				n.name
		`, labelFilter)
	} else {
		cypher = `
			MATCH (n)
			WHERE 
				toLower(n.name) CONTAINS toLower($searchTerm) OR
				toLower(n.displayName) CONTAINS toLower($searchTerm) OR
				toLower(n.signature) CONTAINS toLower($searchTerm) OR
				toLower(n.symbol) CONTAINS toLower($searchTerm) OR
				toLower(n.path) CONTAINS toLower($searchTerm)
			RETURN n, labels(n) AS nodeLabels
			ORDER BY 
				CASE 
					WHEN n:Function OR n:Method THEN 1
					WHEN n:Class OR n:Interface THEN 2
					WHEN n:Variable OR n:Parameter THEN 3
					WHEN n:File OR n:Feature OR n:Document THEN 4
					WHEN n:Symbol THEN 5
					ELSE 6
				END,
				n.name
		`
	}
	
	// Only apply limit if it's greater than 0
	if limit > 0 {
		cypher += fmt.Sprintf(" LIMIT %d", limit)
	}

	params := map[string]any{"searchTerm": searchTerm}
	result, err := qb.client.ExecuteQuery(ctx, cypher, params)
	if err != nil {
		return nil, fmt.Errorf("failed to search nodes: %w", err)
	}

	return result, nil
}

// GetFunctionSourceCode retrieves the exact source code for a function or method
func (qb *QueryBuilder) GetFunctionSourceCode(ctx context.Context, functionName string) (string, error) {
	// Find the function/method node with location metadata
	cypher := `
		MATCH (f)
		WHERE (f:Function OR f:Method) AND f.name = $functionName
		RETURN f.filePath AS filePath, f.startByte AS startByte, f.endByte AS endByte,
			   f.startLine AS startLine, f.endLine AS endLine,
			   f.name AS name, f.signature AS signature
		LIMIT 1
	`
	
	params := map[string]any{"functionName": functionName}
	result, err := qb.client.ExecuteQuery(ctx, cypher, params)
	if err != nil {
		return "", fmt.Errorf("failed to find function: %w", err)
	}
	
	if len(result) == 0 {
		return "", fmt.Errorf("function not found: %s", functionName)
	}
	
	record := result[0].AsMap()
	filePath := getString(record, "filePath")
	startByte := getInt(record, "startByte")
	endByte := getInt(record, "endByte")
	startLine := getInt(record, "startLine")
	endLine := getInt(record, "endLine")
	
	if filePath == "" {
		return "", fmt.Errorf("no file path found for function: %s", functionName)
	}
	
	// Read the file content - handle both absolute and relative paths
	content, err := os.ReadFile(filePath)
	if err != nil {
		// If relative path fails, try from project root
		// This handles the case where tests run from different directories
		if !filepath.IsAbs(filePath) {
			// Try from current working directory
			if pwd, pwdErr := os.Getwd(); pwdErr == nil {
				// Go up to project root if we're in test directory
				projectRoot := pwd
				if strings.HasSuffix(pwd, "/test/integration") {
					projectRoot = filepath.Dir(filepath.Dir(pwd))
				}
				absolutePath := filepath.Join(projectRoot, filePath)
				if content, err = os.ReadFile(absolutePath); err == nil {
					// Success with absolute path
				} else {
					return "", fmt.Errorf("failed to read file %s: %w", filePath, err)
				}
			} else {
				return "", fmt.Errorf("failed to read file %s: %w", filePath, err)
			}
		} else {
			return "", fmt.Errorf("failed to read file %s: %w", filePath, err)
		}
	}
	
	// If we have byte offsets, use them for precise extraction
	if startByte >= 0 && endByte >= 0 && startByte < len(content) && endByte <= len(content) {
		sourceCode := string(content[startByte:endByte])
		return sourceCode, nil
	}
	
	// Fallback to line-based extraction
	if startLine > 0 && endLine > 0 {
		lines := strings.Split(string(content), "\n")
		if startLine <= len(lines) && endLine <= len(lines) {
			functionLines := lines[startLine-1:endLine]
			sourceCode := strings.Join(functionLines, "\n")
			return sourceCode, nil
		}
	}
	
	return "", fmt.Errorf("unable to extract source code for function: %s", functionName)
}

// GetFunctionSourceCodeBySignature retrieves source code using the function signature for disambiguation
func (qb *QueryBuilder) GetFunctionSourceCodeBySignature(ctx context.Context, signature string) (string, error) {
	// Find the function/method node with location metadata using signature
	cypher := `
		MATCH (f)
		WHERE (f:Function OR f:Method) AND f.signature = $signature
		RETURN f.filePath AS filePath, f.startByte AS startByte, f.endByte AS endByte,
			   f.startLine AS startLine, f.endLine AS endLine,
			   f.name AS name, f.signature AS signature
		LIMIT 1
	`
	
	params := map[string]any{"signature": signature}
	result, err := qb.client.ExecuteQuery(ctx, cypher, params)
	if err != nil {
		return "", fmt.Errorf("failed to find function: %w", err)
	}
	
	if len(result) == 0 {
		return "", fmt.Errorf("function not found with signature: %s", signature)
	}
	
	record := result[0].AsMap()
	filePath := getString(record, "filePath")
	startByte := getInt(record, "startByte")
	endByte := getInt(record, "endByte")
	startLine := getInt(record, "startLine")
	endLine := getInt(record, "endLine")
	
	if filePath == "" {
		return "", fmt.Errorf("no file path found for function with signature: %s", signature)
	}
	
	// Read the file content - handle both absolute and relative paths
	content, err := os.ReadFile(filePath)
	if err != nil {
		// If relative path fails, try from project root
		// This handles the case where tests run from different directories
		if !filepath.IsAbs(filePath) {
			// Try from current working directory
			if pwd, pwdErr := os.Getwd(); pwdErr == nil {
				// Go up to project root if we're in test directory
				projectRoot := pwd
				if strings.HasSuffix(pwd, "/test/integration") {
					projectRoot = filepath.Dir(filepath.Dir(pwd))
				}
				absolutePath := filepath.Join(projectRoot, filePath)
				if content, err = os.ReadFile(absolutePath); err == nil {
					// Success with absolute path
				} else {
					return "", fmt.Errorf("failed to read file %s: %w", filePath, err)
				}
			} else {
				return "", fmt.Errorf("failed to read file %s: %w", filePath, err)
			}
		} else {
			return "", fmt.Errorf("failed to read file %s: %w", filePath, err)
		}
	}
	
	// If we have byte offsets, use them for precise extraction
	if startByte >= 0 && endByte >= 0 && startByte < len(content) && endByte <= len(content) {
		sourceCode := string(content[startByte:endByte])
		return sourceCode, nil
	}
	
	// Fallback to line-based extraction
	if startLine > 0 && endLine > 0 {
		lines := strings.Split(string(content), "\n")
		if startLine <= len(lines) && endLine <= len(lines) {
			functionLines := lines[startLine-1:endLine]
			sourceCode := strings.Join(functionLines, "\n")
			return sourceCode, nil
		}
	}
	
	return "", fmt.Errorf("unable to extract source code for function with signature: %s", signature)
}