package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/context-maximiser/code-graph/pkg/neo4j"
	"github.com/context-maximiser/code-graph/pkg/search"
	"github.com/neo4j/neo4j-go-driver/v5/neo4j/dbtype"
)

// MCP Protocol Types
type MCPRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

type MCPResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *MCPError   `json:"error,omitempty"`
}

type MCPError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// MCP Tool Definitions
type MCPTool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
}

type ToolCallRequest struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

type ToolCallResponse struct {
	Content []ToolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

type ToolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// CodeGraph MCP Server
type CodeGraphMCPServer struct {
	client         *neo4j.Client
	queryBuilder   *neo4j.QueryBuilder
	hybridSearch   *search.HybridSearchManager
	vectorSearch   *search.VectorSearchManager
	embeddingService search.EmbeddingService
}

func main() {
	// Initialize Neo4j client
	config := neo4j.Config{
		URI:      getEnvOrDefault("NEO4J_URI", "bolt://localhost:7687"),
		Username: getEnvOrDefault("NEO4J_USER", "neo4j"),
		Password: getEnvOrDefault("NEO4J_PASSWORD", "password123"),
		Database: getEnvOrDefault("NEO4J_DATABASE", "neo4j"),
	}

	client, err := neo4j.NewClient(config)
	if err != nil {
		log.Fatalf("Failed to create Neo4j client: %v", err)
	}
	defer client.Close(context.Background())

	// Initialize embedding service
	var embeddingService search.EmbeddingService
	if apiKey := os.Getenv("GOOGLE_API_KEY"); apiKey != "" {
		embeddingService = search.NewGeminiEmbeddingService(apiKey, "gemini-embedding-001")
		log.Printf("Using Gemini embedding service")
	} else {
		embeddingService = search.NewMockEmbeddingService()
		log.Printf("Warning: GOOGLE_API_KEY not set, using mock embedding service")
	}

	// Initialize search managers
	vectorSearch := search.NewVectorSearchManager(client)
	hybridSearch := search.NewHybridSearchManager(client, embeddingService)

	server := &CodeGraphMCPServer{
		client:           client,
		queryBuilder:     neo4j.NewQueryBuilder(client),
		hybridSearch:     hybridSearch,
		vectorSearch:     vectorSearch,
		embeddingService: embeddingService,
	}

	// Start MCP server
	server.run()
}

func (s *CodeGraphMCPServer) run() {
	scanner := bufio.NewScanner(os.Stdin)
	
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		var request MCPRequest
		if err := json.Unmarshal([]byte(line), &request); err != nil {
			s.sendError(request.ID, -32700, "Parse error")
			continue
		}

		s.handleRequest(request)
	}

	if err := scanner.Err(); err != nil {
		log.Printf("Error reading input: %v", err)
	}
}

func (s *CodeGraphMCPServer) handleRequest(request MCPRequest) {
	switch request.Method {
	case "initialize":
		s.handleInitialize(request)
	case "tools/list":
		s.handleToolsList(request)
	case "tools/call":
		s.handleToolCall(request)
	default:
		s.sendError(request.ID, -32601, "Method not found")
	}
}

func (s *CodeGraphMCPServer) handleInitialize(request MCPRequest) {
	result := map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"capabilities": map[string]interface{}{
			"tools": map[string]interface{}{},
		},
		"serverInfo": map[string]interface{}{
			"name":    "codegraph-mcp-server",
			"version": "1.0.0",
		},
	}

	s.sendResponse(request.ID, result)
}

func (s *CodeGraphMCPServer) handleToolsList(request MCPRequest) {
	tools := []MCPTool{
		{
			Name:        "codegraph_search",
			Description: "Search for functions, methods, classes, and other code entities in the codebase",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{
						"type":        "string",
						"description": "Search term to find code entities (functions, methods, classes, etc.)",
					},
					"limit": map[string]interface{}{
						"type":        "number",
						"description": "Maximum number of results to return (default: 20, 0 for unlimited)",
						"default":     20,
					},
					"types": map[string]interface{}{
						"type":        "array",
						"description": "Filter by entity types (Function, Method, Class, Variable, etc.)",
						"items": map[string]interface{}{
							"type": "string",
						},
					},
				},
				"required": []string{"query"},
			},
		},
		{
			Name:        "codegraph_get_source",
			Description: "Retrieve the exact source code for a specific function or method",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"function_name": map[string]interface{}{
						"type":        "string",
						"description": "Name of the function or method to retrieve source code for",
					},
				},
				"required": []string{"function_name"},
			},
		},
		{
			Name:        "codegraph_find_references",
			Description: "Find all references (usages) of a specific symbol in the codebase",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"symbol": map[string]interface{}{
						"type":        "string",
						"description": "Symbol to find references for",
					},
				},
				"required": []string{"symbol"},
			},
		},
		{
			Name:        "codegraph_analyze_function",
			Description: "Get detailed analysis of a function including callers, callees, and metadata",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"function_name": map[string]interface{}{
						"type":        "string",
						"description": "Name of the function to analyze",
					},
				},
				"required": []string{"function_name"},
			},
		},
		{
			Name:        "codegraph_hybrid_search",
			Description: "Perform hybrid semantic search combining vector similarity, full-text search, and graph queries",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{
						"type":        "string",
						"description": "Search query for semantic understanding",
					},
					"limit": map[string]interface{}{
						"type":        "number",
						"description": "Maximum number of results to return (default: 10)",
						"default":     10,
					},
				},
				"required": []string{"query"},
			},
		},
		{
			Name:        "codegraph_vector_search",
			Description: "Perform pure vector similarity search using embeddings",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{
						"type":        "string",
						"description": "Query text to convert to vector and search",
					},
					"limit": map[string]interface{}{
						"type":        "number",
						"description": "Maximum number of results to return (default: 10)",
						"default":     10,
					},
				},
				"required": []string{"query"},
			},
		},
	}

	result := map[string]interface{}{
		"tools": tools,
	}

	s.sendResponse(request.ID, result)
}

func (s *CodeGraphMCPServer) handleToolCall(request MCPRequest) {
	var toolCall ToolCallRequest
	paramsBytes, _ := json.Marshal(request.Params)
	if err := json.Unmarshal(paramsBytes, &toolCall); err != nil {
		s.sendError(request.ID, -32602, "Invalid params")
		return
	}

	ctx := context.Background()
	var response ToolCallResponse

	switch toolCall.Name {
	case "codegraph_search":
		response = s.handleSearchTool(ctx, toolCall.Arguments)
	case "codegraph_get_source":
		response = s.handleGetSourceTool(ctx, toolCall.Arguments)
	case "codegraph_find_references":
		response = s.handleFindReferencesTool(ctx, toolCall.Arguments)
	case "codegraph_analyze_function":
		response = s.handleAnalyzeFunctionTool(ctx, toolCall.Arguments)
	case "codegraph_hybrid_search":
		response = s.handleHybridSearchTool(ctx, toolCall.Arguments)
	case "codegraph_vector_search":
		response = s.handleVectorSearchTool(ctx, toolCall.Arguments)
	default:
		s.sendError(request.ID, -32601, "Unknown tool")
		return
	}

	s.sendResponse(request.ID, response)
}

func (s *CodeGraphMCPServer) handleSearchTool(ctx context.Context, args map[string]interface{}) ToolCallResponse {
	query, ok := args["query"].(string)
	if !ok {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: "Error: query parameter is required"}},
			IsError: true,
		}
	}

	limit := 20
	if l, ok := args["limit"].(float64); ok {
		limit = int(l)
	}

	var nodeTypes []string
	if types, ok := args["types"].([]interface{}); ok {
		for _, t := range types {
			if typeStr, ok := t.(string); ok {
				nodeTypes = append(nodeTypes, typeStr)
			}
		}
	}

	results, err := s.queryBuilder.SearchNodes(ctx, query, nodeTypes, limit)
	if err != nil {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Search error: %v", err)}},
			IsError: true,
		}
	}

	if len(results) == 0 {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("No results found for query: %s", query)}},
		}
	}

	var output strings.Builder
	output.WriteString(fmt.Sprintf("Found %d result(s) for '%s':\n\n", len(results), query))

	for i, record := range results {
		if i >= 50 { // Limit output to prevent overwhelming
			output.WriteString(fmt.Sprintf("... and %d more results\n", len(results)-i))
			break
		}

		recordMap := record.AsMap()
		if nodeObj, ok := recordMap["n"]; ok {
			if node, ok := nodeObj.(dbtype.Node); ok {
				props := node.Props
				labels := node.Labels

				var nodeType string
				if len(labels) > 0 {
					nodeType = labels[0]
				}

				name := getStringProp(props, "name")
				filePath := getStringProp(props, "filePath")
				signature := getStringProp(props, "signature")

				output.WriteString(fmt.Sprintf("**%s** (%s)\n", name, nodeType))
				if filePath != "" {
					output.WriteString(fmt.Sprintf("  File: %s\n", filePath))
				}
				if signature != "" {
					output.WriteString(fmt.Sprintf("  Signature: %s\n", signature))
				}

				// Add specific info based on node type
				switch nodeType {
				case "Function", "Method":
					if startLine := getIntProp(props, "startLine"); startLine > 0 {
						endLine := getIntProp(props, "endLine")
						output.WriteString(fmt.Sprintf("  Lines: %d-%d\n", startLine, endLine))
					}
					if linesOfCode := getIntProp(props, "linesOfCode"); linesOfCode > 0 {
						output.WriteString(fmt.Sprintf("  Lines of Code: %d\n", linesOfCode))
					}
				case "Class":
					if fqn := getStringProp(props, "fqn"); fqn != "" {
						output.WriteString(fmt.Sprintf("  FQN: %s\n", fqn))
					}
				}

				output.WriteString("\n")
			}
		}
	}

	return ToolCallResponse{
		Content: []ToolContent{{Type: "text", Text: output.String()}},
	}
}

func (s *CodeGraphMCPServer) handleGetSourceTool(ctx context.Context, args map[string]interface{}) ToolCallResponse {
	functionName, ok := args["function_name"].(string)
	if !ok {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: "Error: function_name parameter is required"}},
			IsError: true,
		}
	}

	sourceCode, err := s.queryBuilder.GetFunctionSourceCode(ctx, functionName)
	if err != nil {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error retrieving source for '%s': %v", functionName, err)}},
			IsError: true,
		}
	}

	var output strings.Builder
	output.WriteString(fmt.Sprintf("Source code for function '%s':\n\n", functionName))
	output.WriteString("```go\n")
	output.WriteString(sourceCode)
	output.WriteString("\n```\n")

	return ToolCallResponse{
		Content: []ToolContent{{Type: "text", Text: output.String()}},
	}
}

func (s *CodeGraphMCPServer) handleFindReferencesTool(ctx context.Context, args map[string]interface{}) ToolCallResponse {
	symbol, ok := args["symbol"].(string)
	if !ok {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: "Error: symbol parameter is required"}},
			IsError: true,
		}
	}

	references, err := s.queryBuilder.FindAllReferences(ctx, symbol)
	if err != nil {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error finding references for '%s': %v", symbol, err)}},
			IsError: true,
		}
	}

	if len(references) == 0 {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("No references found for symbol: %s", symbol)}},
		}
	}

	var output strings.Builder
	output.WriteString(fmt.Sprintf("Found %d reference(s) for '%s':\n\n", len(references), symbol))

	for _, ref := range references {
		output.WriteString(fmt.Sprintf("**%s**\n", ref.FilePath))
		output.WriteString(fmt.Sprintf("  Line: %d", ref.StartLine))
		if ref.StartColumn > 0 {
			output.WriteString(fmt.Sprintf(", Column: %d", ref.StartColumn))
		}
		output.WriteString("\n")
		if ref.Context != "" {
			output.WriteString(fmt.Sprintf("  Context: %s\n", ref.Context))
		}
		output.WriteString("\n")
	}

	return ToolCallResponse{
		Content: []ToolContent{{Type: "text", Text: output.String()}},
	}
}

func (s *CodeGraphMCPServer) handleAnalyzeFunctionTool(ctx context.Context, args map[string]interface{}) ToolCallResponse {
	functionName, ok := args["function_name"].(string)
	if !ok {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: "Error: function_name parameter is required"}},
			IsError: true,
		}
	}

	// Get function metadata
	cypher := `
		MATCH (f:Function {name: $name})
		RETURN f.name as name, f.signature as signature, f.filePath as filePath,
			   f.startLine as startLine, f.endLine as endLine, f.linesOfCode as linesOfCode,
			   f.returnType as returnType, f.isExported as isExported,
			   f.complexity as complexity, f.docstring as docstring
		LIMIT 1
	`

	result, err := s.client.ExecuteQuery(ctx, cypher, map[string]any{"name": functionName})
	if err != nil {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error analyzing function '%s': %v", functionName, err)}},
			IsError: true,
		}
	}

	if len(result) == 0 {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Function not found: %s", functionName)}},
			IsError: true,
		}
	}

	record := result[0].AsMap()
	var output strings.Builder

	output.WriteString(fmt.Sprintf("## Analysis for function '%s'\n\n", functionName))

	// Basic info
	output.WriteString("### Basic Information\n")
	if signature := getStringFromRecord(record, "signature"); signature != "" {
		output.WriteString(fmt.Sprintf("- **Signature**: %s\n", signature))
	}
	if filePath := getStringFromRecord(record, "filePath"); filePath != "" {
		output.WriteString(fmt.Sprintf("- **File**: %s\n", filePath))
	}
	if startLine := getIntFromRecord(record, "startLine"); startLine > 0 {
		endLine := getIntFromRecord(record, "endLine")
		output.WriteString(fmt.Sprintf("- **Location**: Lines %d-%d\n", startLine, endLine))
	}
	if linesOfCode := getIntFromRecord(record, "linesOfCode"); linesOfCode > 0 {
		output.WriteString(fmt.Sprintf("- **Lines of Code**: %d\n", linesOfCode))
	}
	if returnType := getStringFromRecord(record, "returnType"); returnType != "" {
		output.WriteString(fmt.Sprintf("- **Return Type**: %s\n", returnType))
	}
	if isExported := getBoolFromRecord(record, "isExported"); isExported {
		output.WriteString("- **Exported**: Yes\n")
	} else {
		output.WriteString("- **Exported**: No\n")
	}

	output.WriteString("\n")

	// Find callers (functions that call this function)
	callersQuery := `
		MATCH (caller)-[:CALLS]->(f:Function {name: $name})
		RETURN caller.name as callerName, caller.filePath as callerFile
		LIMIT 10
	`
	callers, _ := s.client.ExecuteQuery(ctx, callersQuery, map[string]any{"name": functionName})

	output.WriteString("### Called By\n")
	if len(callers) > 0 {
		for _, caller := range callers {
			callerMap := caller.AsMap()
			callerName := getStringFromRecord(callerMap, "callerName")
			callerFile := getStringFromRecord(callerMap, "callerFile")
			output.WriteString(fmt.Sprintf("- **%s** (%s)\n", callerName, callerFile))
		}
	} else {
		output.WriteString("- No callers found\n")
	}

	output.WriteString("\n")

	// Find callees (functions this function calls)
	calleesQuery := `
		MATCH (f:Function {name: $name})-[:CALLS]->(callee)
		RETURN callee.name as calleeName, callee.filePath as calleeFile
		LIMIT 10
	`
	callees, _ := s.client.ExecuteQuery(ctx, calleesQuery, map[string]any{"name": functionName})

	output.WriteString("### Calls\n")
	if len(callees) > 0 {
		for _, callee := range callees {
			calleeMap := callee.AsMap()
			calleeName := getStringFromRecord(calleeMap, "calleeName")
			calleeFile := getStringFromRecord(calleeMap, "calleeFile")
			output.WriteString(fmt.Sprintf("- **%s** (%s)\n", calleeName, calleeFile))
		}
	} else {
		output.WriteString("- No function calls found\n")
	}

	return ToolCallResponse{
		Content: []ToolContent{{Type: "text", Text: output.String()}},
	}
}

func (s *CodeGraphMCPServer) handleHybridSearchTool(ctx context.Context, args map[string]interface{}) ToolCallResponse {
	query, ok := args["query"].(string)
	if !ok {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: "Error: query parameter is required"}},
			IsError: true,
		}
	}

	limit := 10
	if l, ok := args["limit"].(float64); ok {
		limit = int(l)
	}

	results, err := s.hybridSearch.SmartSearch(ctx, query, limit)
	if err != nil {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Hybrid search error: %v", err)}},
			IsError: true,
		}
	}

	if len(results.Results) == 0 {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("No results found for query: %s", query)}},
		}
	}

	var output strings.Builder
	output.WriteString(fmt.Sprintf("## Hybrid Search Results for '%s'\n\n", query))
	output.WriteString(fmt.Sprintf("**Found %d result(s) using %s**\n\n", results.TotalResults, strings.Join(results.SearchTypes, ", ")))

	for i, result := range results.Results {
		if i >= 20 { // Limit output
			output.WriteString(fmt.Sprintf("... and %d more results\n", len(results.Results)-i))
			break
		}

		output.WriteString(fmt.Sprintf("### Result %d (Score: %.3f)\n", i+1, result.CombinedScore))

		// Handle different result types
		if result.Node != nil {
			name := getStringFromInterface(result.Node, "name")
			nodeType := getStringFromInterface(result.Node, "nodeType")
			if nodeType == "" {
				nodeType = "Unknown"
			}

			output.WriteString(fmt.Sprintf("**%s** (%s)\n", name, nodeType))

			if filePath := getStringFromInterface(result.Node, "filePath"); filePath != "" {
				output.WriteString(fmt.Sprintf("- **File**: %s\n", filePath))
			}
			if signature := getStringFromInterface(result.Node, "signature"); signature != "" {
				output.WriteString(fmt.Sprintf("- **Signature**: %s\n", signature))
			}
			if startLine := getIntFromInterface(result.Node, "startLine"); startLine > 0 {
				endLine := getIntFromInterface(result.Node, "endLine")
				if endLine > startLine {
					output.WriteString(fmt.Sprintf("- **Lines**: %d-%d\n", startLine, endLine))
				} else {
					output.WriteString(fmt.Sprintf("- **Line**: %d\n", startLine))
				}
			}
			if docstring := getStringFromInterface(result.Node, "docstring"); docstring != "" {
				output.WriteString(fmt.Sprintf("- **Description**: %s\n", docstring))
			}
		}

		if result.Source != "" {
			output.WriteString(fmt.Sprintf("- **Match Source**: %s\n", result.Source))
		}
		if result.Relevance != "" {
			output.WriteString(fmt.Sprintf("- **Relevance**: %s\n", result.Relevance))
		}

		output.WriteString("\n")
	}

	return ToolCallResponse{
		Content: []ToolContent{{Type: "text", Text: output.String()}},
	}
}

func (s *CodeGraphMCPServer) handleVectorSearchTool(ctx context.Context, args map[string]interface{}) ToolCallResponse {
	query, ok := args["query"].(string)
	if !ok {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: "Error: query parameter is required"}},
			IsError: true,
		}
	}

	limit := 10
	if l, ok := args["limit"].(float64); ok {
		limit = int(l)
	}

	// Generate embedding for the query
	embedding, err := s.embeddingService.GenerateEmbedding(ctx, query)
	if err != nil {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error generating embedding: %v", err)}},
			IsError: true,
		}
	}

	// Perform vector search
	results, err := s.vectorSearch.HybridVectorSearch(ctx, embedding, limit)
	if err != nil {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Vector search error: %v", err)}},
			IsError: true,
		}
	}

	if len(results.Results) == 0 {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("No vector results found for query: %s", query)}},
		}
	}

	var output strings.Builder
	output.WriteString(fmt.Sprintf("## Vector Search Results for '%s'\n\n", query))
	output.WriteString(fmt.Sprintf("**Found %d result(s) using %s index**\n", results.Count, results.IndexUsed))
	output.WriteString(fmt.Sprintf("**Embedding Dimensions**: %d\n\n", len(results.QueryVector)))

	for i, result := range results.Results {
		if i >= 20 { // Limit output
			output.WriteString(fmt.Sprintf("... and %d more results\n", len(results.Results)-i))
			break
		}

		output.WriteString(fmt.Sprintf("### Result %d (Similarity: %.4f)\n", i+1, result.Score))

		name := getStringFromInterface(result.Node, "name")
		filePath := getStringFromInterface(result.Node, "filePath")
		signature := getStringFromInterface(result.Node, "signature")
		nodeType := "Unknown"

		// Try to determine node type from properties
		if labels, ok := result.Node["labels"].([]interface{}); ok && len(labels) > 0 {
			if label, ok := labels[0].(string); ok {
				nodeType = label
			}
		}

		output.WriteString(fmt.Sprintf("**%s** (%s)\n", name, nodeType))

		if filePath != "" {
			output.WriteString(fmt.Sprintf("- **File**: %s\n", filePath))
		}
		if signature != "" {
			output.WriteString(fmt.Sprintf("- **Signature**: %s\n", signature))
		}
		if startLine := getIntFromInterface(result.Node, "startLine"); startLine > 0 {
			endLine := getIntFromInterface(result.Node, "endLine")
			if endLine > startLine {
				output.WriteString(fmt.Sprintf("- **Lines**: %d-%d\n", startLine, endLine))
			} else {
				output.WriteString(fmt.Sprintf("- **Line**: %d\n", startLine))
			}
		}
		if docstring := getStringFromInterface(result.Node, "docstring"); docstring != "" {
			output.WriteString(fmt.Sprintf("- **Description**: %s\n", docstring))
		}

		output.WriteString("\n")
	}

	return ToolCallResponse{
		Content: []ToolContent{{Type: "text", Text: output.String()}},
	}
}

func (s *CodeGraphMCPServer) sendResponse(id interface{}, result interface{}) {
	response := MCPResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}

	jsonBytes, _ := json.Marshal(response)
	fmt.Println(string(jsonBytes))
}

func (s *CodeGraphMCPServer) sendError(id interface{}, code int, message string) {
	response := MCPResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &MCPError{
			Code:    code,
			Message: message,
		},
	}

	jsonBytes, _ := json.Marshal(response)
	fmt.Println(string(jsonBytes))
}

// Helper functions
func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getStringProp(props map[string]interface{}, key string) string {
	if val, ok := props[key]; ok {
		if str, ok := val.(string); ok {
			return str
		}
	}
	return ""
}

func getIntProp(props map[string]interface{}, key string) int {
	if val, ok := props[key]; ok {
		switch v := val.(type) {
		case int64:
			return int(v)
		case int:
			return v
		case float64:
			return int(v)
		}
	}
	return 0
}

func getBoolProp(props map[string]interface{}, key string) bool {
	if val, ok := props[key]; ok {
		if b, ok := val.(bool); ok {
			return b
		}
	}
	return false
}

func getStringFromRecord(record map[string]interface{}, key string) string {
	if val, ok := record[key]; ok {
		if str, ok := val.(string); ok {
			return str
		}
	}
	return ""
}

func getIntFromRecord(record map[string]interface{}, key string) int {
	if val, ok := record[key]; ok {
		switch v := val.(type) {
		case int64:
			return int(v)
		case int:
			return v
		case float64:
			return int(v)
		}
	}
	return 0
}

func getBoolFromRecord(record map[string]interface{}, key string) bool {
	if val, ok := record[key]; ok {
		if b, ok := val.(bool); ok {
			return b
		}
	}
	return false
}

func getStringFromInterface(data map[string]interface{}, key string) string {
	if val, ok := data[key]; ok {
		if str, ok := val.(string); ok {
			return str
		}
	}
	return ""
}

func getIntFromInterface(data map[string]interface{}, key string) int {
	if val, ok := data[key]; ok {
		switch v := val.(type) {
		case int64:
			return int(v)
		case int:
			return v
		case float64:
			return int(v)
		}
	}
	return 0
}