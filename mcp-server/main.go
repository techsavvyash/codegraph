package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/context-maximiser/code-graph/pkg/indexer/documents"
	"github.com/context-maximiser/code-graph/pkg/neo4j"
	"github.com/context-maximiser/code-graph/pkg/search"
	"github.com/joho/godotenv"
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
	client           *neo4j.Client
	queryBuilder     *neo4j.QueryBuilder
	hybridSearch     *search.HybridSearchManager
	vectorSearch     *search.VectorSearchManager
	embeddingService search.EmbeddingService
	docIndexer       *documents.DocumentIndexer
	commentSearch    *search.CommentEmbeddingService
}

func main() {
	// Load environment variables from .env file
	if err := godotenv.Load("../.env"); err != nil {
		log.Printf("Warning: Could not load .env file: %v", err)
		// Continue execution - environment variables might be set via other means
	}

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

	// Initialize embedding service - require GEMINI_API_KEY
	var embeddingService search.EmbeddingService
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("GOOGLE_API_KEY") // fallback
	}

	if apiKey != "" {
		embeddingService = search.NewGeminiEmbeddingService(apiKey, "gemini-embedding-001")
		log.Printf("Using Gemini embedding service")
	} else {
		log.Fatalf("GEMINI_API_KEY environment variable is required for embedding functionality")
	}

	// Initialize search managers
	vectorSearch := search.NewVectorSearchManager(client)
	hybridSearch := search.NewHybridSearchManager(client, embeddingService)

	// Initialize document indexer and comment search
	docIndexer := documents.NewDocumentIndexer(client)
	commentSearch := search.NewCommentEmbeddingService(client, embeddingService)

	server := &CodeGraphMCPServer{
		client:           client,
		queryBuilder:     neo4j.NewQueryBuilder(client),
		hybridSearch:     hybridSearch,
		vectorSearch:     vectorSearch,
		embeddingService: embeddingService,
		docIndexer:       docIndexer,
		commentSearch:    commentSearch,
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
		{
			Name:        "codegraph_index_documents",
			Description: "Index markdown/text documents with embeddings and create relationships to code symbols",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Directory or file path to index (supports .md, .txt, .rst, .adoc files)",
					},
					"generate_embeddings": map[string]interface{}{
						"type":        "boolean",
						"description": "Whether to generate embeddings for documents (default: true)",
						"default":     true,
					},
				},
				"required": []string{"path"},
			},
		},
		{
			Name:        "codegraph_search_docs",
			Description: "Search documents using natural language queries and find related code",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{
						"type":        "string",
						"description": "Natural language search query to find relevant documents",
					},
					"limit": map[string]interface{}{
						"type":        "number",
						"description": "Maximum number of results to return (default: 10)",
						"default":     10,
					},
					"include_code": map[string]interface{}{
						"type":        "boolean",
						"description": "Whether to include related code symbols (default: true)",
						"default":     true,
					},
				},
				"required": []string{"query"},
			},
		},
		{
			Name:        "codegraph_search_by_comment",
			Description: "Search for functions/methods by their docstrings and comments using semantic similarity",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{
						"type":        "string",
						"description": "Natural language description to find functions by their documentation",
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
			Name:        "codegraph_link_docs_to_code",
			Description: "Create explicit relationships between documents and code symbols they reference",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"doc_path": map[string]interface{}{
						"type":        "string",
						"description": "Path to the document file to link to code",
					},
					"auto_detect": map[string]interface{}{
						"type":        "boolean",
						"description": "Automatically detect code references in backticks (default: true)",
						"default":     true,
					},
				},
				"required": []string{"doc_path"},
			},
		},
		{
			Name:        "codegraph_intelligent_link",
			Description: "Create intelligent semantic relationships between documents and code using LLM analysis and call graph traversal",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"doc_path": map[string]interface{}{
						"type":        "string",
						"description": "Path to the document file to analyze and link",
					},
					"confidence_threshold": map[string]interface{}{
						"type":        "number",
						"description": "Minimum confidence threshold for creating links (0.0-1.0, default: 0.2)",
						"default":     0.2,
						"minimum":     0.0,
						"maximum":     1.0,
					},
				},
				"required": []string{"doc_path"},
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
	case "codegraph_index_documents":
		response = s.handleIndexDocumentsTool(ctx, toolCall.Arguments)
	case "codegraph_search_docs":
		response = s.handleSearchDocsTool(ctx, toolCall.Arguments)
	case "codegraph_search_by_comment":
		response = s.handleSearchByCommentTool(ctx, toolCall.Arguments)
	case "codegraph_link_docs_to_code":
		response = s.handleLinkDocsToCodeTool(ctx, toolCall.Arguments)
	case "codegraph_intelligent_link":
		response = s.handleIntelligentLinkTool(ctx, toolCall.Arguments)
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

// Document-related tool handlers

func (s *CodeGraphMCPServer) handleIndexDocumentsTool(ctx context.Context, args map[string]interface{}) ToolCallResponse {
	path, ok := args["path"].(string)
	if !ok {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: "Error: path parameter is required"}},
			IsError: true,
		}
	}

	generateEmbeddings := true
	if gen, ok := args["generate_embeddings"].(bool); ok {
		generateEmbeddings = gen
	}

	var output strings.Builder
	output.WriteString(fmt.Sprintf("## Indexing Documents at '%s'\n\n", path))

	// Check if path exists
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error: path does not exist: %s", path)}},
			IsError: true,
		}
	}

	// Determine if it's a file or directory
	fileInfo, err := os.Stat(path)
	if err != nil {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error accessing path: %v", err)}},
			IsError: true,
		}
	}

	if fileInfo.IsDir() {
		// Index directory
		err = s.docIndexer.IndexDirectory(ctx, path)
		if err != nil {
			return ToolCallResponse{
				Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error indexing directory: %v", err)}},
				IsError: true,
			}
		}
		output.WriteString("✓ Successfully indexed all documents in directory\n")
	} else {
		// Index single file
		err = s.docIndexer.IndexDocument(ctx, path)
		if err != nil {
			return ToolCallResponse{
				Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error indexing document: %v", err)}},
				IsError: true,
			}
		}
		output.WriteString("✓ Successfully indexed document\n")
	}

	// Get document stats
	stats, err := s.docIndexer.GetDocumentStats(ctx)
	if err != nil {
		output.WriteString(fmt.Sprintf("Warning: failed to get document stats: %v\n", err))
	} else {
		output.WriteString("\n### Document Index Statistics\n")
		if docCount, ok := stats["documentCount"].(int64); ok {
			output.WriteString(fmt.Sprintf("- **Total Documents**: %d\n", docCount))
		}
		if featureCount, ok := stats["featureCount"].(int64); ok {
			output.WriteString(fmt.Sprintf("- **Features Extracted**: %d\n", featureCount))
		}
		if symbolCount, ok := stats["mentionedSymbolCount"].(int64); ok {
			output.WriteString(fmt.Sprintf("- **Code Symbols Linked**: %d\n", symbolCount))
		}
	}

	// If embeddings are enabled, update vector indexes
	if generateEmbeddings && s.embeddingService != nil {
		output.WriteString("\n### Embedding Generation\n")

		// Create/update vector indexes
		if err := s.vectorSearch.CreateVectorIndexes(ctx); err != nil {
			output.WriteString(fmt.Sprintf("Warning: failed to create vector indexes: %v\n", err))
		} else {
			output.WriteString("✓ Vector indexes updated\n")
		}

		output.WriteString("✓ Document embeddings generated and indexed\n")
	}

	return ToolCallResponse{
		Content: []ToolContent{{Type: "text", Text: output.String()}},
	}
}

func (s *CodeGraphMCPServer) handleSearchDocsTool(ctx context.Context, args map[string]interface{}) ToolCallResponse {
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

	includeCode := true
	if include, ok := args["include_code"].(bool); ok {
		includeCode = include
	}

	var output strings.Builder
	output.WriteString(fmt.Sprintf("## Document Search Results for '%s'\n\n", query))

	// Use hybrid search to find documents
	results, err := s.hybridSearch.UnifiedSearch(ctx, query, limit)
	if err != nil {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Document search error: %v", err)}},
			IsError: true,
		}
	}

	if len(results.Results) == 0 {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("No documents found for query: %s", query)}},
		}
	}

	// Filter for document results and group related code
	documentResults := []search.HybridSearchResult{}
	codeResults := []search.HybridSearchResult{}

	for _, result := range results.Results {
		// Check if this is a document
		isDocument := false
		for _, label := range result.Labels {
			if label == "Document" {
				isDocument = true
				break
			}
		}

		if isDocument {
			documentResults = append(documentResults, result)
		} else {
			codeResults = append(codeResults, result)
		}
	}

	// Display document results
	if len(documentResults) > 0 {
		output.WriteString("### 📄 Documents\n\n")
		for i, result := range documentResults {
			title := getStringFromInterface(result.Node, "title")
			if title == "" {
				title = getStringFromInterface(result.Node, "name")
			}
			sourceUrl := getStringFromInterface(result.Node, "sourceUrl")
			content := getStringFromInterface(result.Node, "content")

			output.WriteString(fmt.Sprintf("**%d. %s** (Score: %.3f)\n", i+1, title, result.CombinedScore))
			if sourceUrl != "" {
				output.WriteString(fmt.Sprintf("   - **Source**: %s\n", sourceUrl))
			}
			if content != "" {
				// Show first 200 characters of content
				preview := content
				if len(preview) > 200 {
					preview = preview[:200] + "..."
				}
				output.WriteString(fmt.Sprintf("   - **Preview**: %s\n", preview))
			}
			output.WriteString(fmt.Sprintf("   - **Match Source**: %s | **Relevance**: %s\n\n", result.Source, result.Relevance))

			// Find related code for this document if includeCode is true
			if includeCode {
				relatedCode, err := s.findRelatedCodeForDocument(ctx, result.Node, 3)
				if err == nil && len(relatedCode) > 0 {
					output.WriteString("   **Related Code**:\n")
					for _, code := range relatedCode {
						codeName := getStringFromInterface(code, "name")
						codeType := getStringFromInterface(code, "nodeType")
						if codeType == "" {
							codeType = "Symbol"
						}
						filePath := getStringFromInterface(code, "filePath")
						output.WriteString(fmt.Sprintf("   - %s (%s)", codeName, codeType))
						if filePath != "" {
							output.WriteString(fmt.Sprintf(" in %s", filePath))
						}
						output.WriteString("\n")
					}
					output.WriteString("\n")
				}
			}
		}
	}

	// Display related code results if includeCode is true
	if includeCode && len(codeResults) > 0 {
		output.WriteString("### 💻 Related Code\n\n")
		for i, result := range codeResults {
			if i >= 5 { // Limit code results
				output.WriteString(fmt.Sprintf("... and %d more code results\n", len(codeResults)-i))
				break
			}

			name := getStringFromInterface(result.Node, "name")
			nodeType := "Code"
			for _, label := range result.Labels {
				if label != "Node" {
					nodeType = label
					break
				}
			}
			filePath := getStringFromInterface(result.Node, "filePath")
			signature := getStringFromInterface(result.Node, "signature")

			output.WriteString(fmt.Sprintf("**%d. %s** (%s, Score: %.3f)\n", i+1, name, nodeType, result.CombinedScore))
			if filePath != "" {
				output.WriteString(fmt.Sprintf("   - **File**: %s\n", filePath))
			}
			if signature != "" {
				output.WriteString(fmt.Sprintf("   - **Signature**: %s\n", signature))
			}
			output.WriteString("\n")
		}
	}

	output.WriteString(fmt.Sprintf("\n**Total Results**: %d documents", len(documentResults)))
	if includeCode {
		output.WriteString(fmt.Sprintf(", %d code entities", len(codeResults)))
	}
	output.WriteString(fmt.Sprintf(" | **Search Methods**: %s\n", strings.Join(results.SearchTypes, ", ")))

	return ToolCallResponse{
		Content: []ToolContent{{Type: "text", Text: output.String()}},
	}
}

func (s *CodeGraphMCPServer) handleSearchByCommentTool(ctx context.Context, args map[string]interface{}) ToolCallResponse {
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

	var output strings.Builder
	output.WriteString(fmt.Sprintf("## Function Search by Comment/Docstring for '%s'\n\n", query))

	// Use comment search service to find functions by their docstrings
	results, err := s.commentSearch.SearchFunctionsByComment(ctx, query, limit)
	if err != nil {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Comment search error: %v", err)}},
			IsError: true,
		}
	}

	if len(results.Results) == 0 {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("No functions found with comments matching: %s", query)}},
		}
	}

	output.WriteString(fmt.Sprintf("**Found %d function(s) with matching documentation**\n\n", len(results.Results)))

	for i, result := range results.Results {
		// Extract function details from parent node
		functionName := getStringFromInterface(result.ParentNode, "name")
		functionType := getStringFromInterface(result.CommentNode, "parentType")
		if functionType == "" {
			functionType = "Function"
		}
		filePath := getStringFromInterface(result.ParentNode, "filePath")
		signature := getStringFromInterface(result.ParentNode, "signature")
		startLine := getIntFromInterface(result.ParentNode, "startLine")
		endLine := getIntFromInterface(result.ParentNode, "endLine")

		// Extract comment details
		commentText := getStringFromInterface(result.CommentNode, "text")

		output.WriteString(fmt.Sprintf("### %d. %s (%s) - Similarity: %.4f\n\n", i+1, functionName, functionType, result.Score))

		if filePath != "" {
			output.WriteString(fmt.Sprintf("**File**: %s", filePath))
			if startLine > 0 {
				if endLine > startLine {
					output.WriteString(fmt.Sprintf(" (lines %d-%d)", startLine, endLine))
				} else {
					output.WriteString(fmt.Sprintf(" (line %d)", startLine))
				}
			}
			output.WriteString("\n\n")
		}

		if signature != "" {
			output.WriteString(fmt.Sprintf("**Signature**: `%s`\n\n", signature))
		}

		if commentText != "" {
			output.WriteString("**Documentation**:\n")
			output.WriteString("```\n")
			output.WriteString(commentText)
			output.WriteString("\n```\n\n")
		}
	}

	return ToolCallResponse{
		Content: []ToolContent{{Type: "text", Text: output.String()}},
	}
}

func (s *CodeGraphMCPServer) handleLinkDocsToCodeTool(ctx context.Context, args map[string]interface{}) ToolCallResponse {
	docPath, ok := args["doc_path"].(string)
	if !ok {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: "Error: doc_path parameter is required"}},
			IsError: true,
		}
	}

	autoDetect := true
	if auto, ok := args["auto_detect"].(bool); ok {
		autoDetect = auto
	}

	var output strings.Builder
	output.WriteString(fmt.Sprintf("## Linking Document to Code: '%s'\n\n", docPath))

	// Check if document exists
	if _, err := os.Stat(docPath); os.IsNotExist(err) {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error: document file does not exist: %s", docPath)}},
			IsError: true,
		}
	}

	if autoDetect {
		// Re-index this specific document to create/update relationships
		err := s.docIndexer.IndexDocument(ctx, docPath)
		if err != nil {
			return ToolCallResponse{
				Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error re-indexing document: %v", err)}},
				IsError: true,
			}
		}
		output.WriteString("✓ Document re-indexed with automatic code symbol detection\n\n")
	}

	// Query for relationships from this document
	docFileName := filepath.Base(docPath)
	relationshipQuery := `
		MATCH (d:Document)-[r:MENTIONS]->(s)
		WHERE d.sourceUrl CONTAINS $docFileName OR d.title CONTAINS $docFileName
		RETURN d.title as docTitle, s.name as symbolName, labels(s) as symbolLabels, s.filePath as symbolPath, count(r) as mentionCount
		ORDER BY mentionCount DESC
		LIMIT 20
	`

	results, err := s.client.ExecuteQuery(ctx, relationshipQuery, map[string]any{
		"docFileName": docFileName,
	})
	if err != nil {
		output.WriteString(fmt.Sprintf("Warning: could not query relationships: %v\n", err))
	} else if len(results) > 0 {
		output.WriteString("### 🔗 Code Symbols Linked to Document\n\n")
		for i, record := range results {
			recordMap := record.AsMap()
			symbolName := getStringFromRecord(recordMap, "symbolName")
			symbolPath := getStringFromRecord(recordMap, "symbolPath")
			mentionCount := getIntFromRecord(recordMap, "mentionCount")

			var symbolType string
			if labels, ok := recordMap["symbolLabels"].([]interface{}); ok && len(labels) > 0 {
				if label, ok := labels[0].(string); ok {
					symbolType = label
				}
			}
			if symbolType == "" {
				symbolType = "Symbol"
			}

			output.WriteString(fmt.Sprintf("**%d. %s** (%s)\n", i+1, symbolName, symbolType))
			if symbolPath != "" {
				output.WriteString(fmt.Sprintf("   - **File**: %s\n", symbolPath))
			}
			output.WriteString(fmt.Sprintf("   - **Mentions**: %d\n\n", mentionCount))
		}
	} else {
		output.WriteString("### ℹ️  No Code Symbol Links Found\n\n")
		output.WriteString("This document doesn't appear to reference any indexed code symbols.\n")
		output.WriteString("Make sure:\n")
		output.WriteString("- Code symbols are referenced in backticks (e.g., `functionName`)\n")
		output.WriteString("- The referenced code has been indexed\n")
		output.WriteString("- Symbol names match exactly\n\n")
	}

	return ToolCallResponse{
		Content: []ToolContent{{Type: "text", Text: output.String()}},
	}
}

// Helper function to find code related to a document
func (s *CodeGraphMCPServer) findRelatedCodeForDocument(ctx context.Context, docNode map[string]interface{}, limit int) ([]map[string]interface{}, error) {
	// Try to get document ID or use title/sourceUrl for matching
	var query string
	var params map[string]any

	if docId, ok := docNode["elementId"].(string); ok {
		query = `
			MATCH (d)-[:MENTIONS]->(s)
			WHERE elementId(d) = $docId
			RETURN s as symbol, labels(s) as nodeLabels
			LIMIT $limit
		`
		params = map[string]any{"docId": docId, "limit": limit}
	} else {
		// Fallback to matching by title or sourceUrl
		title := getStringFromInterface(docNode, "title")
		sourceUrl := getStringFromInterface(docNode, "sourceUrl")

		query = `
			MATCH (d:Document)-[:MENTIONS]->(s)
			WHERE d.title = $title OR d.sourceUrl = $sourceUrl
			RETURN s as symbol, labels(s) as nodeLabels
			LIMIT $limit
		`
		params = map[string]any{"title": title, "sourceUrl": sourceUrl, "limit": limit}
	}

	results, err := s.client.ExecuteQuery(ctx, query, params)
	if err != nil {
		return nil, err
	}

	var relatedCode []map[string]interface{}
	for _, record := range results {
		recordMap := record.AsMap()
		if symbolObj, ok := recordMap["symbol"]; ok {
			if symbol, ok := symbolObj.(dbtype.Node); ok {
				symbolData := symbol.Props
				// Add node type from labels
				if labels, ok := recordMap["nodeLabels"].([]interface{}); ok && len(labels) > 0 {
					if label, ok := labels[0].(string); ok {
						symbolData["nodeType"] = label
					}
				}
				relatedCode = append(relatedCode, symbolData)
			}
		}
	}

	return relatedCode, nil
}

func (s *CodeGraphMCPServer) handleIntelligentLinkTool(ctx context.Context, args map[string]interface{}) ToolCallResponse {
	docPath, ok := args["doc_path"].(string)
	if !ok {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: "Error: doc_path parameter is required"}},
			IsError: true,
		}
	}

	// Get confidence threshold
	confidenceThreshold := 0.2
	if ct, ok := args["confidence_threshold"].(float64); ok {
		confidenceThreshold = ct
	}

	var output strings.Builder

	// Check if file exists
	if _, err := os.Stat(docPath); os.IsNotExist(err) {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error: Document not found at path: %s", docPath)}},
			IsError: true,
		}
	}

	// Read document content
	content, err := os.ReadFile(docPath)
	if err != nil {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error reading document: %v", err)}},
			IsError: true,
		}
	}

	// Create intelligent linker if we have embedding service
	if s.embeddingService == nil {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: "Error: Embedding service not available for intelligent linking"}},
			IsError: true,
		}
	}

	intelligentLinker := search.NewIntelligentDocumentLinker(s.client, s.embeddingService)

	// First, create or find the document node
	docIndexer := documents.NewDocumentIndexer(s.client)
	err = docIndexer.IndexDocument(ctx, docPath)
	if err != nil {
		output.WriteString(fmt.Sprintf("Warning: Failed to index document: %v\n", err))
	}

	// Find the document ID
	cypher := `
		MATCH (d:Document)
		WHERE d.sourceUrl = $docPath
		RETURN d.id AS id
		LIMIT 1
	`

	results, err := s.client.ExecuteQuery(ctx, cypher, map[string]any{
		"docPath": docPath,
	})
	if err != nil {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error finding document in database: %v", err)}},
			IsError: true,
		}
	}

	if len(results) == 0 {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: "Error: Document not found in database after indexing"}},
			IsError: true,
		}
	}

	documentID := results[0].AsMap()["id"].(string)

	// Perform intelligent linking
	output.WriteString(fmt.Sprintf("## Intelligent Linking Analysis for '%s'\n\n", docPath))

	linkingResult, err := intelligentLinker.LinkDocumentToCode(ctx, documentID, string(content))
	if err != nil {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error during intelligent linking: %v", err)}},
			IsError: true,
		}
	}

	// Report results
	output.WriteString(fmt.Sprintf("✓ Intelligent linking completed with confidence threshold %.2f\n\n", confidenceThreshold))

	// Direct matches
	if len(linkingResult.DirectMatches) > 0 {
		output.WriteString("### 🎯 Direct Matches\n\n")
		for _, match := range linkingResult.DirectMatches {
			output.WriteString(fmt.Sprintf("**%s** (%s, Score: %.3f)\n", match.Name, match.NodeType, match.Confidence))
			if match.FilePath != "" {
				output.WriteString(fmt.Sprintf("   - **File**: %s\n", match.FilePath))
			}
			if match.Signature != "" {
				output.WriteString(fmt.Sprintf("   - **Signature**: %s\n", match.Signature))
			}
			output.WriteString(fmt.Sprintf("   - **Reasons**: %s\n\n", strings.Join(match.MatchReasons, ", ")))
		}
	}

	// Semantic matches
	if len(linkingResult.SemanticMatches) > 0 {
		output.WriteString("### 🧠 Semantic Matches\n\n")
		for _, match := range linkingResult.SemanticMatches {
			if match.Confidence >= confidenceThreshold {
				output.WriteString(fmt.Sprintf("**%s** (%s, Score: %.3f)\n", match.Name, match.NodeType, match.Confidence))
				if match.FilePath != "" {
					output.WriteString(fmt.Sprintf("   - **File**: %s\n", match.FilePath))
				}
				output.WriteString(fmt.Sprintf("   - **Reasons**: %s\n\n", strings.Join(match.MatchReasons, ", ")))
			}
		}
	}

	// Call graph matches
	if len(linkingResult.CallGraphMatches) > 0 {
		output.WriteString("### 🔗 Call Graph Matches\n\n")
		for _, match := range linkingResult.CallGraphMatches {
			if match.Confidence >= confidenceThreshold {
				output.WriteString(fmt.Sprintf("**%s** (%s, Score: %.3f, Depth: %d)\n",
					match.Name, match.NodeType, match.Confidence, match.CallGraphDepth))
				if match.FilePath != "" {
					output.WriteString(fmt.Sprintf("   - **File**: %s\n", match.FilePath))
				}
				output.WriteString(fmt.Sprintf("   - **Reasons**: %s\n\n", strings.Join(match.MatchReasons, ", ")))
			}
		}
	}

	// Summary
	output.WriteString("### 📊 Summary\n\n")
	output.WriteString(fmt.Sprintf("- **Direct matches**: %d\n", len(linkingResult.DirectMatches)))
	output.WriteString(fmt.Sprintf("- **Semantic matches**: %d\n", len(linkingResult.SemanticMatches)))
	output.WriteString(fmt.Sprintf("- **Call graph matches**: %d\n", len(linkingResult.CallGraphMatches)))
	output.WriteString(fmt.Sprintf("- **Total relationships created**: %d\n", linkingResult.CreatedLinks))

	return ToolCallResponse{
		Content: []ToolContent{{Type: "text", Text: output.String()}},
	}
}