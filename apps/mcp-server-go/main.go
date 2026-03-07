package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	models "github.com/context-maximiser/code-graph/libs/core-models-go"
	"github.com/context-maximiser/code-graph/libs/indexer-go/documents"
	"github.com/context-maximiser/code-graph/libs/neo4j-go"
	"github.com/context-maximiser/code-graph/libs/query-go"
	"github.com/context-maximiser/code-graph/libs/search-go"
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
	vectorStore      search.VectorStore
	embeddingService search.EmbeddingService
	docIndexer       *documents.DocumentIndexer
	commentSearch    *search.CommentEmbeddingService
	workspaceRoot    string
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

	// Initialize embedding service - optional, only required for embedding-based tools
	var embeddingService search.EmbeddingService
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("GOOGLE_API_KEY") // fallback
	}

	if apiKey != "" {
		embeddingService = search.NewGeminiEmbeddingService(apiKey, "gemini-embedding-001")
		log.Printf("Using Gemini embedding service")
	} else {
		log.Printf("Warning: GEMINI_API_KEY not set - embedding-based tools will not be available")
	}

	// Initialize Qdrant vector store
	qdrantURL := os.Getenv("QDRANT_URL")
	if qdrantURL == "" {
		qdrantURL = "localhost:6334"
	}
	vectorStore, err := search.NewQdrantVectorStore(qdrantURL)
	if err != nil {
		log.Printf("Warning: failed to connect to Qdrant at %s: %v", qdrantURL, err)
	}

	// Initialize search managers
	hybridSearch := search.NewHybridSearchManager(client, embeddingService, vectorStore)

	// Initialize document indexer and comment search
	docIndexer := documents.NewDocumentIndexer(client)
	commentSearch := search.NewCommentEmbeddingService(client, embeddingService)

	workspaceRoot, err := os.Getwd()
	if err != nil {
		workspaceRoot = "."
	}

	server := &CodeGraphMCPServer{
		client:           client,
		queryBuilder:     neo4j.NewQueryBuilder(client),
		hybridSearch:     hybridSearch,
		vectorStore:      vectorStore,
		embeddingService: embeddingService,
		docIndexer:       docIndexer,
		commentSearch:    commentSearch,
		workspaceRoot:    workspaceRoot,
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
		{
			Name:        "codegraph_list_services",
			Description: "List all services in the codebase with their metadata (language, package name, version)",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name_filter": map[string]interface{}{
						"type":        "string",
						"description": "Optional filter to match service names (case-insensitive substring match)",
					},
				},
			},
		},
		{
			Name:        "codegraph_service_dependencies",
			Description: "Get all dependencies (DEPENDS_ON relationships) of a service, showing which packages/services it imports",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"service_name": map[string]interface{}{
						"type":        "string",
						"description": "Name of the service to get dependencies for",
					},
				},
				"required": []string{"service_name"},
			},
		},
		{
			Name:        "codegraph_service_api_endpoints",
			Description: "Get all API endpoints (EXPOSES_API) exposed by a service, including HTTP method, path, and framework",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"service_name": map[string]interface{}{
						"type":        "string",
						"description": "Name of the service to get API endpoints for",
					},
					"limit": map[string]interface{}{
						"type":        "number",
						"description": "Maximum number of endpoints to return (default: 50)",
						"default":     50,
					},
				},
				"required": []string{"service_name"},
			},
		},
		{
			Name:        "codegraph_service_api_calls",
			Description: "Get all API calls (CALLS_API) made by a service to other services or external APIs",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"service_name": map[string]interface{}{
						"type":        "string",
						"description": "Name of the service to get API calls for",
					},
					"limit": map[string]interface{}{
						"type":        "number",
						"description": "Maximum number of calls to return (default: 50)",
						"default":     50,
					},
				},
				"required": []string{"service_name"},
			},
		},
		{
			Name:        "codegraph_cross_service_calls",
			Description: "Get cross-service call chains showing how services communicate through API calls and SDK usage",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"from_service": map[string]interface{}{
						"type":        "string",
						"description": "Source service name (optional, shows all if not specified)",
					},
					"to_service": map[string]interface{}{
						"type":        "string",
						"description": "Target service name (optional, shows all if not specified)",
					},
					"limit": map[string]interface{}{
						"type":        "number",
						"description": "Maximum number of call chains to return (default: 20)",
						"default":     20,
					},
				},
			},
		},
		{
			Name:        "codegraph_service_architecture",
			Description: "Get comprehensive architecture overview showing all services and their relationships (dependencies, API calls, SDK usage)",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			Name:        "codegraph_get_entry_points",
			Description: "List structurally-detected entry points (API handlers, interface implementations, topological roots, high-centrality functions) across all 4 tiers. Use this to discover the important starting points in a codebase.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"tier": map[string]interface{}{
						"type":        "number",
						"description": "Filter to a specific tier (1=API-exposed, 2=interface implementations, 3=topological roots, 4=high centrality). Omit for all tiers.",
					},
					"limit": map[string]interface{}{
						"type":        "number",
						"description": "Maximum number of results to return (default: 50)",
						"default":     50,
					},
					"scope_id": map[string]interface{}{
						"type":        "string",
						"description": "Scope ID to query (default: main)",
						"default":     "main",
					},
					"service_name": map[string]interface{}{
						"type":        "string",
						"description": "Optional service name filter to constrain entry point discovery",
					},
				},
			},
		},
		{
			Name:        "codegraph_generate_flows",
			Description: "Generate flow spines from entry points — call chain documentation showing how a request flows through the codebase. Each flow traces from an entry point through its call graph.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"max_depth": map[string]interface{}{
						"type":        "number",
						"description": "How deep to trace call chains (default: 5)",
						"default":     5,
					},
					"limit": map[string]interface{}{
						"type":        "number",
						"description": "Maximum number of flows to generate (default: 20)",
						"default":     20,
					},
					"scope_id": map[string]interface{}{
						"type":        "string",
						"description": "Scope ID to query (default: main)",
						"default":     "main",
					},
					"service_name": map[string]interface{}{
						"type":        "string",
						"description": "Optional service name filter to constrain flow generation",
					},
				},
			},
		},
		{
			Name:        "codegraph_trace_call_graph",
			Description: "Traverse the call graph from a specific function, showing what it calls (downstream) or what calls it (upstream). Returns a tree-formatted call chain with file locations.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"function_name": map[string]interface{}{
						"type":        "string",
						"description": "Name of the function to trace from",
					},
					"direction": map[string]interface{}{
						"type":        "string",
						"description": "Traversal direction: 'downstream' (callees), 'upstream' (callers), or 'both' (default: 'downstream')",
						"default":     "downstream",
					},
					"max_depth": map[string]interface{}{
						"type":        "number",
						"description": "Maximum traversal depth (default: 3)",
						"default":     3,
					},
					"scope_id": map[string]interface{}{
						"type":        "string",
						"description": "Scope ID to query (default: main)",
						"default":     "main",
					},
					"service_name": map[string]interface{}{
						"type":        "string",
						"description": "Optional service name filter to constrain traversal",
					},
				},
				"required": []string{"function_name"},
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
	case "codegraph_list_services":
		response = s.handleListServicesTool(ctx, toolCall.Arguments)
	case "codegraph_service_dependencies":
		response = s.handleServiceDependenciesTool(ctx, toolCall.Arguments)
	case "codegraph_service_api_endpoints":
		response = s.handleServiceAPIEndpointsTool(ctx, toolCall.Arguments)
	case "codegraph_service_api_calls":
		response = s.handleServiceAPICallsTool(ctx, toolCall.Arguments)
	case "codegraph_cross_service_calls":
		response = s.handleCrossServiceCallsTool(ctx, toolCall.Arguments)
	case "codegraph_service_architecture":
		response = s.handleServiceArchitectureTool(ctx, toolCall.Arguments)
	case "codegraph_get_entry_points":
		response = s.handleGetEntryPointsTool(ctx, toolCall.Arguments)
	case "codegraph_generate_flows":
		response = s.handleGenerateFlowsTool(ctx, toolCall.Arguments)
	case "codegraph_trace_call_graph":
		response = s.handleTraceCallGraphTool(ctx, toolCall.Arguments)
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

	// Get function metadata including file path
	cypher := `
		MATCH (f)
		WHERE (f:Function OR f:Method) AND f.name = $functionName
		RETURN f.filePath AS filePath
		LIMIT 1
	`

	result, err := s.client.ExecuteQuery(ctx, cypher, map[string]any{"functionName": functionName})
	if err != nil || len(result) == 0 {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error finding function '%s': %v", functionName, err)}},
			IsError: true,
		}
	}

	// Detect language from file path
	filePath := getStringFromRecord(result[0].AsMap(), "filePath")
	language := detectLanguageFromPath(filePath)

	sourceCode, err := s.queryBuilder.GetFunctionSourceCode(ctx, functionName)
	if err != nil {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error retrieving source for '%s': %v", functionName, err)}},
			IsError: true,
		}
	}

	var output strings.Builder
	output.WriteString(fmt.Sprintf("Source code for function '%s':\n\n", functionName))
	output.WriteString(fmt.Sprintf("```%s\n", language))
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

	if s.embeddingService == nil {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: "Error: Hybrid search requires GEMINI_API_KEY environment variable to be set for embedding generation"}},
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

	if s.embeddingService == nil {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: "Error: Vector search requires GEMINI_API_KEY environment variable to be set for embedding generation"}},
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

	// Perform vector search via Qdrant
	if s.vectorStore == nil {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: "Error: Vector store not available"}},
			IsError: true,
		}
	}

	results, err := s.vectorStore.Query(ctx, search.VectorQuery{
		Vector: embedding,
		Limit:  limit,
	})
	if err != nil {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Vector search error: %v", err)}},
			IsError: true,
		}
	}

	if len(results) == 0 {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("No vector results found for query: %s", query)}},
		}
	}

	var output strings.Builder
	output.WriteString(fmt.Sprintf("## Vector Search Results for '%s'\n\n", query))
	output.WriteString(fmt.Sprintf("**Found %d result(s)**\n", len(results)))
	output.WriteString(fmt.Sprintf("**Embedding Dimensions**: %d\n\n", len(embedding)))

	for i, result := range results {
		if i >= 20 {
			output.WriteString(fmt.Sprintf("... and %d more results\n", len(results)-i))
			break
		}

		output.WriteString(fmt.Sprintf("### Result %d (Similarity: %.4f)\n", i+1, result.Score))

		name, _ := result.Metadata["name"].(string)
		signature, _ := result.Metadata["signature"].(string)
		nodeLabel, _ := result.Metadata["nodeLabel"].(string)

		output.WriteString(fmt.Sprintf("**%s** (%s)\n", name, nodeLabel))

		if signature != "" {
			output.WriteString(fmt.Sprintf("- **Signature**: %s\n", signature))
		}
		if description, _ := result.Metadata["description"].(string); description != "" {
			output.WriteString(fmt.Sprintf("- **Description**: %s\n", description))
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

// detectLanguageFromPath detects the programming language from a file path
func detectLanguageFromPath(filePath string) string {
	ext := filepath.Ext(filePath)
	switch ext {
	case ".go":
		return "go"
	case ".ts":
		return "typescript"
	case ".tsx":
		return "tsx"
	case ".js":
		return "javascript"
	case ".jsx":
		return "jsx"
	case ".py":
		return "python"
	case ".java":
		return "java"
	case ".kt", ".kts":
		return "kotlin"
	case ".scala":
		return "scala"
	case ".rb":
		return "ruby"
	case ".php":
		return "php"
	case ".rs":
		return "rust"
	case ".c":
		return "c"
	case ".cpp", ".cc", ".cxx":
		return "cpp"
	case ".h", ".hpp":
		return "cpp"
	case ".cs":
		return "csharp"
	case ".swift":
		return "swift"
	default:
		return "text"
	}
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

		// Create/update vector collections in Qdrant
		if s.vectorStore != nil {
			collections := []string{"function_embeddings_768", "document_embeddings_768", "class_embeddings_768"}
			for _, col := range collections {
				if err := s.vectorStore.CreateIndex(ctx, col, 768, "cosine"); err != nil {
					output.WriteString(fmt.Sprintf("Warning: failed to create collection %s: %v\n", col, err))
				}
			}
			output.WriteString("✓ Qdrant vector collections updated\n")
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

	if s.embeddingService == nil {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: "Error: Comment search requires GEMINI_API_KEY environment variable to be set for embedding generation"}},
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

	intelligentLinker := search.NewIntelligentDocumentLinker(s.client, s.embeddingService, s.vectorStore)

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

// handleListServicesTool lists all services with their metadata
func (s *CodeGraphMCPServer) handleListServicesTool(ctx context.Context, args map[string]interface{}) ToolCallResponse {
	query := `
		MATCH (s:Service)
		OPTIONAL MATCH (s)-[:CONTAINS]->(f:File)
		WITH s, count(DISTINCT f) AS fileCount
		OPTIONAL MATCH (s)-[:DEPENDS_ON]->(dep:Service)
		WITH s, fileCount, collect(DISTINCT dep.name) AS dependencies
		RETURN s.name AS name, s.packageName AS packageName, s.version AS version,
		       s.language AS language, fileCount, dependencies
		ORDER BY s.name
	`

	result, err := s.client.ExecuteQuery(ctx, query, nil)
	if err != nil {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error: %v", err)}},
			IsError: true,
		}
	}

	var output strings.Builder
	output.WriteString("# Services\n\n")

	if len(result) == 0 {
		output.WriteString("No services found.\n")
	} else {
		for _, record := range result {
			recordMap := record.AsMap()
			name := getStringFromRecord(recordMap, "name")
			packageName := getStringFromRecord(recordMap, "packageName")
			version := getStringFromRecord(recordMap, "version")
			language := getStringFromRecord(recordMap, "language")
			fileCount := getIntFromRecord(recordMap, "fileCount")
			dependencies := []interface{}{}
			if deps, ok := recordMap["dependencies"].([]interface{}); ok {
				dependencies = deps
			}

			output.WriteString(fmt.Sprintf("## %s\n\n", name))
			if packageName != "" {
				output.WriteString(fmt.Sprintf("- **Package**: %s\n", packageName))
			}
			if version != "" {
				output.WriteString(fmt.Sprintf("- **Version**: %s\n", version))
			}
			if language != "" {
				output.WriteString(fmt.Sprintf("- **Language**: %s\n", language))
			}
			output.WriteString(fmt.Sprintf("- **Files**: %d\n", fileCount))
			if len(dependencies) > 0 {
				output.WriteString("- **Dependencies**: ")
				depNames := make([]string, len(dependencies))
				for i, dep := range dependencies {
					depNames[i] = dep.(string)
				}
				output.WriteString(strings.Join(depNames, ", "))
				output.WriteString("\n")
			}
			output.WriteString("\n")
		}
	}

	return ToolCallResponse{
		Content: []ToolContent{{Type: "text", Text: output.String()}},
	}
}

// handleServiceDependenciesTool gets dependencies of a service
func (s *CodeGraphMCPServer) handleServiceDependenciesTool(ctx context.Context, args map[string]interface{}) ToolCallResponse {
	serviceName, ok := args["service_name"].(string)
	if !ok {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: "Error: service_name is required"}},
			IsError: true,
		}
	}

	query := `
		MATCH (s:Service {name: $serviceName})-[d:DEPENDS_ON]->(target:Service)
		RETURN target.name AS targetService, target.packageName AS packageName,
		       d.importCount AS importCount, d.packageName AS importedPackage
		ORDER BY importCount DESC
	`

	params := map[string]interface{}{
		"serviceName": serviceName,
	}

	result, err := s.client.ExecuteQuery(ctx, query, params)
	if err != nil {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error: %v", err)}},
			IsError: true,
		}
	}

	var output strings.Builder
	output.WriteString(fmt.Sprintf("# Dependencies for %s\n\n", serviceName))

	if len(result) == 0 {
		output.WriteString("No dependencies found.\n")
	} else {
		output.WriteString("| Target Service | Package Name | Import Count | Imported Package |\n")
		output.WriteString("|----------------|--------------|--------------|------------------|\n")
		for _, record := range result {
			recordMap := record.AsMap()
			targetService := getStringFromRecord(recordMap, "targetService")
			packageName := getStringFromRecord(recordMap, "packageName")
			importCount := getIntFromRecord(recordMap, "importCount")
			importedPackage := getStringFromRecord(recordMap, "importedPackage")

			output.WriteString(fmt.Sprintf("| %s | %s | %d | %s |\n",
				targetService, packageName, importCount, importedPackage))
		}
	}

	return ToolCallResponse{
		Content: []ToolContent{{Type: "text", Text: output.String()}},
	}
}

// handleServiceAPIEndpointsTool gets API endpoints exposed by a service
func (s *CodeGraphMCPServer) handleServiceAPIEndpointsTool(ctx context.Context, args map[string]interface{}) ToolCallResponse {
	serviceName, ok := args["service_name"].(string)
	if !ok {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: "Error: service_name is required"}},
			IsError: true,
		}
	}

	query := `
		MATCH (s:Service {name: $serviceName})-[:CONTAINS*]->(f:Function)-[e:EXPOSES_API]->(api:APIRoute)
		RETURN f.name AS functionName, api.method AS method, api.endpoint AS endpoint,
		       api.line AS line, f.file AS filePath
		ORDER BY api.endpoint, api.method
	`

	params := map[string]interface{}{
		"serviceName": serviceName,
	}

	result, err := s.client.ExecuteQuery(ctx, query, params)
	if err != nil {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error: %v", err)}},
			IsError: true,
		}
	}

	var output strings.Builder
	output.WriteString(fmt.Sprintf("# API Endpoints for %s\n\n", serviceName))

	if len(result) == 0 {
		output.WriteString("No API endpoints found.\n")
	} else {
		output.WriteString("| Method | Endpoint | Function | File |\n")
		output.WriteString("|--------|----------|----------|------|\n")
		for _, record := range result {
			recordMap := record.AsMap()
			method := getStringFromRecord(recordMap, "method")
			endpoint := getStringFromRecord(recordMap, "endpoint")
			functionName := getStringFromRecord(recordMap, "functionName")
			filePath := getStringFromRecord(recordMap, "filePath")

			output.WriteString(fmt.Sprintf("| %s | %s | %s | %s |\n",
				method, endpoint, functionName, filePath))
		}
	}

	return ToolCallResponse{
		Content: []ToolContent{{Type: "text", Text: output.String()}},
	}
}

// handleServiceAPICallsTool gets API calls made by a service
func (s *CodeGraphMCPServer) handleServiceAPICallsTool(ctx context.Context, args map[string]interface{}) ToolCallResponse {
	serviceName, ok := args["service_name"].(string)
	if !ok {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: "Error: service_name is required"}},
			IsError: true,
		}
	}

	query := `
		MATCH (s:Service {name: $serviceName})-[:CONTAINS*]->(f:Function)-[c:CALLS_API]->(call)
		WHERE call:HTTPCall OR call:SDKCall
		OPTIONAL MATCH (call)-[:TARGETS_SERVICE]->(target:Service)
		RETURN f.name AS functionName,
		       CASE WHEN call:HTTPCall THEN 'HTTP' ELSE 'SDK' END AS callType,
		       call.url AS url, call.method AS method, call.target AS target,
		       target.name AS targetService,
		       f.file AS filePath
		ORDER BY callType, target
	`

	params := map[string]interface{}{
		"serviceName": serviceName,
	}

	result, err := s.client.ExecuteQuery(ctx, query, params)
	if err != nil {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error: %v", err)}},
			IsError: true,
		}
	}

	var output strings.Builder
	output.WriteString(fmt.Sprintf("# API Calls from %s\n\n", serviceName))

	if len(result) == 0 {
		output.WriteString("No API calls found.\n")
	} else {
		output.WriteString("| Type | Target | Method/SDK | URL/Call | Target Service | Function |\n")
		output.WriteString("|------|--------|------------|----------|----------------|----------|\n")
		for _, record := range result {
			recordMap := record.AsMap()
			callType := getStringFromRecord(recordMap, "callType")
			target := getStringFromRecord(recordMap, "target")
			method := getStringFromRecord(recordMap, "method")
			url := getStringFromRecord(recordMap, "url")
			targetService := getStringFromRecord(recordMap, "targetService")
			functionName := getStringFromRecord(recordMap, "functionName")

			output.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %s | %s |\n",
				callType, target, method, url, targetService, functionName))
		}
	}

	return ToolCallResponse{
		Content: []ToolContent{{Type: "text", Text: output.String()}},
	}
}

// handleCrossServiceCallsTool gets cross-service call chains
func (s *CodeGraphMCPServer) handleCrossServiceCallsTool(ctx context.Context, args map[string]interface{}) ToolCallResponse {
	sourceService, sourceOk := args["source_service"].(string)
	targetService, targetOk := args["target_service"].(string)

	if !sourceOk || !targetOk {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: "Error: source_service and target_service are required"}},
			IsError: true,
		}
	}

	query := `
		MATCH (source:Service {name: $sourceService})
		MATCH (target:Service {name: $targetService})
		MATCH path = shortestPath((source)-[*..10]-(target))
		RETURN [node in nodes(path) |
		          CASE
		            WHEN 'Service' IN labels(node) THEN node.name
		            WHEN 'Function' IN labels(node) THEN node.name
		            WHEN 'HTTPCall' IN labels(node) THEN node.url
		            WHEN 'SDKCall' IN labels(node) THEN node.target
		            ELSE ''
		          END
		       ] AS nodePath,
		       [rel in relationships(path) | type(rel)] AS relPath,
		       length(path) AS pathLength
		LIMIT 10
	`

	params := map[string]interface{}{
		"sourceService": sourceService,
		"targetService": targetService,
	}

	result, err := s.client.ExecuteQuery(ctx, query, params)
	if err != nil {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error: %v", err)}},
			IsError: true,
		}
	}

	var output strings.Builder
	output.WriteString(fmt.Sprintf("# Call Chains: %s → %s\n\n", sourceService, targetService))

	if len(result) == 0 {
		output.WriteString("No paths found between these services.\n")
	} else {
		for i, record := range result {
			recordMap := record.AsMap()
			pathLength := getIntFromRecord(recordMap, "pathLength")
			nodePath := []interface{}{}
			if np, ok := recordMap["nodePath"].([]interface{}); ok {
				nodePath = np
			}
			relPath := []interface{}{}
			if rp, ok := recordMap["relPath"].([]interface{}); ok {
				relPath = rp
			}

			output.WriteString(fmt.Sprintf("## Path %d (length: %d)\n\n", i+1, pathLength))

			for j := 0; j < len(nodePath); j++ {
				node := nodePath[j].(string)
				output.WriteString(fmt.Sprintf("%s", node))
				if j < len(relPath) {
					rel := relPath[j].(string)
					output.WriteString(fmt.Sprintf(" -[%s]→ ", rel))
				}
			}
			output.WriteString("\n\n")
		}
	}

	return ToolCallResponse{
		Content: []ToolContent{{Type: "text", Text: output.String()}},
	}
}

// handleServiceArchitectureTool provides a complete architecture overview
func (s *CodeGraphMCPServer) handleServiceArchitectureTool(ctx context.Context, args map[string]interface{}) ToolCallResponse {
	query := `
		// Get all services
		MATCH (s:Service)
		OPTIONAL MATCH (s)-[:DEPENDS_ON]->(dep:Service)
		WITH s, collect(DISTINCT dep.name) AS dependencies

		// Get API endpoints
		OPTIONAL MATCH (s)-[:CONTAINS*]->(f:Function)-[:EXPOSES_API]->(api:APIRoute)
		WITH s, dependencies, count(DISTINCT api) AS apiCount

		// Get API calls
		OPTIONAL MATCH (s)-[:CONTAINS*]->(f2:Function)-[:CALLS_API]->(call)
		WHERE call:HTTPCall OR call:SDKCall
		WITH s, dependencies, apiCount, count(DISTINCT call) AS callCount

		RETURN s.name AS name, s.packageName AS packageName,
		       dependencies, apiCount, callCount
		ORDER BY s.name
	`

	result, err := s.client.ExecuteQuery(ctx, query, nil)
	if err != nil {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error: %v", err)}},
			IsError: true,
		}
	}

	var output strings.Builder
	output.WriteString("# Service Architecture Overview\n\n")

	if len(result) == 0 {
		output.WriteString("No services found.\n")
	} else {
		// Summary
		output.WriteString(fmt.Sprintf("**Total Services**: %d\n\n", len(result)))

		// Service details
		for _, record := range result {
			recordMap := record.AsMap()
			name := getStringFromRecord(recordMap, "name")
			packageName := getStringFromRecord(recordMap, "packageName")
			dependencies := []interface{}{}
			if deps, ok := recordMap["dependencies"].([]interface{}); ok {
				dependencies = deps
			}
			apiCount := getIntFromRecord(recordMap, "apiCount")
			callCount := getIntFromRecord(recordMap, "callCount")

			output.WriteString(fmt.Sprintf("## %s\n\n", name))
			if packageName != "" {
				output.WriteString(fmt.Sprintf("- **Package**: %s\n", packageName))
			}
			output.WriteString(fmt.Sprintf("- **API Endpoints**: %d\n", apiCount))
			output.WriteString(fmt.Sprintf("- **API Calls**: %d\n", callCount))

			if len(dependencies) > 0 {
				output.WriteString("- **Dependencies**:\n")
				for _, dep := range dependencies {
					output.WriteString(fmt.Sprintf("  - %s\n", dep.(string)))
				}
			}
			output.WriteString("\n")
		}

		// Relationship graph
		output.WriteString("## Dependency Graph\n\n```mermaid\ngraph LR\n")
		for _, record := range result {
			recordMap := record.AsMap()
			name := getStringFromRecord(recordMap, "name")
			dependencies := []interface{}{}
			if deps, ok := recordMap["dependencies"].([]interface{}); ok {
				dependencies = deps
			}

			for _, dep := range dependencies {
				depName := dep.(string)
				output.WriteString(fmt.Sprintf("    %s --> %s\n", name, depName))
			}
		}
		output.WriteString("```\n")
	}

	return ToolCallResponse{
		Content: []ToolContent{{Type: "text", Text: output.String()}},
	}
}

// handleGetEntryPointsTool lists structurally-detected entry points across 4 tiers.
func (s *CodeGraphMCPServer) handleGetEntryPointsTool(ctx context.Context, args map[string]interface{}) ToolCallResponse {
	limit := 50
	if l, ok := args["limit"].(float64); ok && l > 0 {
		limit = int(l)
	}

	scopeCtx := parseScopeContextArg(args)
	serviceNames := s.resolveWorkspaceServices(ctx, scopeCtx.ScopeID, getOptionalStringArg(args, "service_name"))

	tierFilter := 0
	if t, ok := args["tier"].(float64); ok && t >= 1 && t <= 4 {
		tierFilter = int(t)
	}

	type entryPoint struct {
		NodeKey         string
		Name            string
		FilePath        string
		Tier            int
		TierLabel       string
		DetectionSource string
	}

	params := map[string]any{"scopeId": scopeCtx.ScopeID, "serviceNames": serviceNames}
	seen := make(map[string]bool)
	var entries []entryPoint

	// Tier 1: API-exposed functions
	if tierFilter == 0 || tierFilter == 1 {
		cypher := fmt.Sprintf(`
			MATCH (fn)-[:EXPOSES_API]->(r:APIRoute)
			WHERE (fn:Function OR fn:Method)
			  AND (fn.scopeId = $scopeId OR fn.scopeId = 'main')
			  AND (r.scopeId = $scopeId OR r.scopeId = 'main')
			  AND coalesce(fn.isTestFunction, false) = false
			  AND (fn.filePath IS NULL OR NOT fn.filePath ENDS WITH '_test.go')
			  %s
			RETURN DISTINCT fn.nodeKey AS nodeKey, fn.name AS name, coalesce(fn.filePath, '') AS filePath,
			       r.detectionSource AS detectionSource, r.protocol AS protocol
			ORDER BY fn.name`, serviceFilterClause("fn"))
		records, err := s.client.ExecuteQuery(ctx, cypher, params)
		if err == nil {
			for _, r := range records {
				m := r.AsMap()
				nodeKey := getStringFromRecord(m, "nodeKey")
				name := getStringFromRecord(m, "name")
				filePath := getStringFromRecord(m, "filePath")
				if nodeKey == "" || name == "" || seen[nodeKey] {
					continue
				}
				if filePath != "" && !s.fileInWorkspace(filePath) {
					continue
				}
				seen[nodeKey] = true
				source := getStringFromRecord(m, "detectionSource")
				if source == "" {
					source = getStringFromRecord(m, "protocol")
				}
				entries = append(entries, entryPoint{
					NodeKey: nodeKey, Name: name, FilePath: filePath,
					Tier: 1, TierLabel: "API-exposed", DetectionSource: source,
				})
			}
		}
	}

	// Tier 2: Interface implementations with no callers
	if tierFilter == 0 || tierFilter == 2 {
		cypher := fmt.Sprintf(`
			MATCH (fn)-[:IMPLEMENTS]->(iface:Interface)
			WHERE (fn:Function OR fn:Method)
			  AND (fn.scopeId = $scopeId OR fn.scopeId = 'main')
			  AND coalesce(fn.isTestFunction, false) = false
			  AND (fn.filePath IS NULL OR NOT fn.filePath ENDS WITH '_test.go')
			  %s
			OPTIONAL MATCH (caller)-[:CALLS]->(fn)
			WHERE caller:Function OR caller:Method
			  AND (caller.scopeId = $scopeId OR caller.scopeId = 'main')
			WITH fn, iface, count(caller) AS callerCount
			WHERE callerCount = 0
			RETURN DISTINCT fn.nodeKey AS nodeKey, fn.name AS name, coalesce(fn.filePath, '') AS filePath,
			       iface.name AS ifaceName
			ORDER BY fn.name`, serviceFilterClause("fn"))
		records, err := s.client.ExecuteQuery(ctx, cypher, params)
		if err == nil {
			for _, r := range records {
				m := r.AsMap()
				nodeKey := getStringFromRecord(m, "nodeKey")
				name := getStringFromRecord(m, "name")
				filePath := getStringFromRecord(m, "filePath")
				if nodeKey == "" || name == "" || seen[nodeKey] {
					continue
				}
				if filePath != "" && !s.fileInWorkspace(filePath) {
					continue
				}
				seen[nodeKey] = true
				entries = append(entries, entryPoint{
					NodeKey: nodeKey, Name: name, FilePath: filePath,
					Tier: 2, TierLabel: "Interface impl", DetectionSource: "implements " + getStringFromRecord(m, "ifaceName"),
				})
			}
		}
	}

	// Tier 3: Topological roots (exported, no callers, has callees)
	if tierFilter == 0 || tierFilter == 3 {
		cypher := fmt.Sprintf(`
			MATCH (fn)
			WHERE (fn:Function OR fn:Method)
			  AND (fn.scopeId = $scopeId OR fn.scopeId = 'main')
			  AND coalesce(fn.isExported, false) = true
			  AND coalesce(fn.isTestFunction, false) = false
			  AND (fn.filePath IS NULL OR NOT fn.filePath ENDS WITH '_test.go')
			  %s
			OPTIONAL MATCH (caller)-[:CALLS]->(fn)
			WHERE caller:Function OR caller:Method
			  AND (caller.scopeId = $scopeId OR caller.scopeId = 'main')
			WITH fn, count(caller) AS callerCount
			WHERE callerCount = 0
			OPTIONAL MATCH (fn)-[:CALLS]->(callee)
			WHERE callee:Function OR callee:Method
			  AND (callee.scopeId = $scopeId OR callee.scopeId = 'main')
			WITH fn, count(callee) AS calleeCount
			WHERE calleeCount > 0
			RETURN DISTINCT fn.nodeKey AS nodeKey, fn.name AS name, coalesce(fn.filePath, '') AS filePath,
			       calleeCount
			ORDER BY calleeCount DESC`, serviceFilterClause("fn"))
		records, err := s.client.ExecuteQuery(ctx, cypher, params)
		if err == nil {
			for _, r := range records {
				m := r.AsMap()
				nodeKey := getStringFromRecord(m, "nodeKey")
				name := getStringFromRecord(m, "name")
				filePath := getStringFromRecord(m, "filePath")
				if nodeKey == "" || name == "" || seen[nodeKey] {
					continue
				}
				if filePath != "" && !s.fileInWorkspace(filePath) {
					continue
				}
				seen[nodeKey] = true
				calleeCount := getIntFromRecord(m, "calleeCount")
				entries = append(entries, entryPoint{
					NodeKey: nodeKey, Name: name, FilePath: filePath,
					Tier: 3, TierLabel: "Topological root", DetectionSource: fmt.Sprintf("exported, %d callees", calleeCount),
				})
			}
		}
	}

	// Tier 4: High centrality (functions with many callers AND callees)
	if tierFilter == 0 || tierFilter == 4 {
		cypher := fmt.Sprintf(`
			MATCH (fn)
			WHERE (fn:Function OR fn:Method)
			  AND (fn.scopeId = $scopeId OR fn.scopeId = 'main')
			  AND coalesce(fn.isTestFunction, false) = false
			  AND (fn.filePath IS NULL OR NOT fn.filePath ENDS WITH '_test.go')
			  %s
			OPTIONAL MATCH (caller)-[:CALLS]->(fn)
			WHERE caller:Function OR caller:Method
			  AND (caller.scopeId = $scopeId OR caller.scopeId = 'main')
			WITH fn, count(DISTINCT caller) AS inDeg
			WHERE inDeg >= 3
			OPTIONAL MATCH (fn)-[:CALLS]->(callee)
			WHERE callee:Function OR callee:Method
			  AND (callee.scopeId = $scopeId OR callee.scopeId = 'main')
			WITH fn, inDeg, count(DISTINCT callee) AS outDeg
			WHERE outDeg >= 2
			RETURN DISTINCT fn.nodeKey AS nodeKey, fn.name AS name, coalesce(fn.filePath, '') AS filePath,
			       inDeg, outDeg, inDeg + outDeg AS centrality
			ORDER BY centrality DESC`, serviceFilterClause("fn"))
		records, err := s.client.ExecuteQuery(ctx, cypher, params)
		if err == nil {
			for _, r := range records {
				m := r.AsMap()
				nodeKey := getStringFromRecord(m, "nodeKey")
				name := getStringFromRecord(m, "name")
				filePath := getStringFromRecord(m, "filePath")
				if nodeKey == "" || name == "" || seen[nodeKey] {
					continue
				}
				if filePath != "" && !s.fileInWorkspace(filePath) {
					continue
				}
				seen[nodeKey] = true
				inDeg := getIntFromRecord(m, "inDeg")
				outDeg := getIntFromRecord(m, "outDeg")
				entries = append(entries, entryPoint{
					NodeKey: nodeKey, Name: name, FilePath: filePath,
					Tier: 4, TierLabel: "High centrality", DetectionSource: fmt.Sprintf("%d callers, %d callees", inDeg, outDeg),
				})
			}
		}
	}

	if len(entries) == 0 {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: "No entry points found in the graph."}},
		}
	}

	if len(entries) > limit {
		entries = entries[:limit]
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Tier != entries[j].Tier {
			return entries[i].Tier < entries[j].Tier
		}
		if entries[i].Name != entries[j].Name {
			return entries[i].Name < entries[j].Name
		}
		return entries[i].FilePath < entries[j].FilePath
	})

	var output strings.Builder
	output.WriteString(fmt.Sprintf("## Entry Points (%d found)\n\n", len(entries)))
	if len(serviceNames) > 0 {
		output.WriteString(fmt.Sprintf("_Workspace service filter:_ `%s`\n\n", strings.Join(serviceNames, "`, `")))
	}
	output.WriteString("| Name | File | Tier | Detection Source |\n")
	output.WriteString("|------|------|------|------------------|\n")
	for _, e := range entries {
		file := e.FilePath
		output.WriteString(fmt.Sprintf("| %s | %s | T%d: %s | %s |\n",
			e.Name, file, e.Tier, e.TierLabel, e.DetectionSource))
	}

	return ToolCallResponse{
		Content: []ToolContent{{Type: "text", Text: output.String()}},
	}
}

// handleGenerateFlowsTool generates flow spines from entry points.
func (s *CodeGraphMCPServer) handleGenerateFlowsTool(ctx context.Context, args map[string]interface{}) ToolCallResponse {
	maxDepth := 5
	if d, ok := args["max_depth"].(float64); ok && d > 0 {
		maxDepth = int(d)
	}

	limit := 20
	if l, ok := args["limit"].(float64); ok && l > 0 {
		limit = int(l)
	}

	scopeCtx := parseScopeContextArg(args)
	serviceNames := s.resolveWorkspaceServices(ctx, scopeCtx.ScopeID, getOptionalStringArg(args, "service_name"))

	gen := query.NewFlowSpineGenerator(s.client)
	gen.SetScope(scopeCtx)
	gen.SetServiceFilter(serviceNames)
	flows, err := gen.GenerateFlows(ctx, maxDepth)
	if err != nil {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error generating flows: %v", err)}},
			IsError: true,
		}
	}

	if len(flows) == 0 {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: "No flows generated. Ensure the codebase has been indexed with call graph data."}},
		}
	}

	flows = s.filterFlowsToWorkspace(ctx, scopeCtx.ScopeID, flows)
	if len(flows) == 0 {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: "No workspace-scoped flows generated. Try indexing this repository again with `index pipeline`."}},
		}
	}

	if len(flows) > limit {
		flows = flows[:limit]
	}

	var output strings.Builder
	output.WriteString(fmt.Sprintf("## Generated Flows (%d)\n\n", len(flows)))
	if len(serviceNames) > 0 {
		output.WriteString(fmt.Sprintf("_Workspace service filter:_ `%s`\n\n", strings.Join(serviceNames, "`, `")))
	}

	for i, flow := range flows {
		output.WriteString(fmt.Sprintf("### %d. %s\n", i+1, flow.FlowName))
		output.WriteString(fmt.Sprintf("- **Type**: %s\n", flow.FlowType))
		output.WriteString(fmt.Sprintf("- **Steps** (%d):\n", len(flow.Steps)))
		for _, step := range flow.Steps {
			indent := strings.Repeat("  ", step.Order)
			output.WriteString(fmt.Sprintf("%s%d. `%s` (%s)\n", indent, step.Order+1, step.Name, step.Label))
		}
		output.WriteString("\n")
	}

	return ToolCallResponse{
		Content: []ToolContent{{Type: "text", Text: output.String()}},
	}
}

// handleTraceCallGraphTool traverses the call graph from a specific function.
func (s *CodeGraphMCPServer) handleTraceCallGraphTool(ctx context.Context, args map[string]interface{}) ToolCallResponse {
	functionName, ok := args["function_name"].(string)
	if !ok || functionName == "" {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: "Error: function_name parameter is required"}},
			IsError: true,
		}
	}

	direction := "downstream"
	if d, ok := args["direction"].(string); ok && (d == "upstream" || d == "both") {
		direction = d
	}

	maxDepth := 3
	if d, ok := args["max_depth"].(float64); ok && d > 0 {
		maxDepth = int(d)
	}
	if maxDepth > 10 {
		maxDepth = 10
	}

	scopeCtx := parseScopeContextArg(args)
	serviceNames := s.resolveWorkspaceServices(ctx, scopeCtx.ScopeID, getOptionalStringArg(args, "service_name"))

	type functionCandidate struct {
		NodeKey   string
		Name      string
		FilePath  string
		Exact     bool
		Workspace bool
	}

	candidateCypher := fmt.Sprintf(`
                MATCH (root)
                WHERE (root:Function OR root:Method)
                  AND (root.scopeId = $scopeId OR root.scopeId = 'main')
                  AND coalesce(root.isTestFunction, false) = false
                  AND (root.filePath IS NULL OR NOT root.filePath ENDS WITH '_test.go')
                  AND toLower(root.name) CONTAINS toLower($name)
                  %s
                RETURN DISTINCT root.nodeKey AS nodeKey, root.name AS name,
                       coalesce(root.filePath, '') AS filePath,
                       CASE WHEN toLower(root.name) = toLower($name) THEN true ELSE false END AS exact
                ORDER BY exact DESC, root.name ASC
                LIMIT 50`, serviceFilterClause("root"))

	records, err := s.client.ExecuteQuery(ctx, candidateCypher, map[string]any{
		"scopeId":      scopeCtx.ScopeID,
		"name":         functionName,
		"serviceNames": serviceNames,
	})
	if err != nil {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error tracing call graph: %v", err)}},
			IsError: true,
		}
	}

	var candidates []functionCandidate
	for _, r := range records {
		m := r.AsMap()
		c := functionCandidate{
			NodeKey:  getStringFromRecord(m, "nodeKey"),
			Name:     getStringFromRecord(m, "name"),
			FilePath: getStringFromRecord(m, "filePath"),
			Exact:    getBoolFromRecord(m, "exact"),
		}
		if c.NodeKey == "" || c.Name == "" {
			continue
		}
		if c.FilePath != "" && !s.fileInWorkspace(c.FilePath) {
			continue
		}
		c.Workspace = c.FilePath == "" || s.fileInWorkspace(c.FilePath)
		candidates = append(candidates, c)
	}

	if len(candidates) == 0 {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("No function found matching '%s' in the current workspace scope.", functionName)}},
			IsError: true,
		}
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Exact != candidates[j].Exact {
			return candidates[i].Exact
		}
		if candidates[i].Workspace != candidates[j].Workspace {
			return candidates[i].Workspace
		}
		iDepth := strings.Count(candidates[i].FilePath, "/")
		jDepth := strings.Count(candidates[j].FilePath, "/")
		if iDepth != jDepth {
			return iDepth > jDepth
		}
		if candidates[i].FilePath != candidates[j].FilePath {
			return candidates[i].FilePath < candidates[j].FilePath
		}
		return candidates[i].Name < candidates[j].Name
	})

	root := candidates[0]

	var output strings.Builder
	output.WriteString(fmt.Sprintf("## Call Graph for `%s`\n\n", functionName))
	if root.FilePath != "" {
		output.WriteString(fmt.Sprintf("Selected root: `%s` (%s)\n\n", root.Name, root.FilePath))
	}
	if len(serviceNames) > 0 {
		output.WriteString(fmt.Sprintf("_Workspace service filter:_ `%s`\n\n", strings.Join(serviceNames, "`, `")))
	}

	// Downstream: what does this function call?
	if direction == "downstream" || direction == "both" {
		output.WriteString("### Downstream (callees)\n\n")
		cypher := fmt.Sprintf(`
			MATCH (root {nodeKey: $rootKey})
			MATCH path = (root)-[:CALLS*1..%d]->(callee)
			WHERE (callee:Function OR callee:Method)
			  AND (callee.scopeId = $scopeId OR callee.scopeId = 'main')
			  AND coalesce(callee.isTestFunction, false) = false
			  AND (callee.filePath IS NULL OR NOT callee.filePath ENDS WITH '_test.go')
			  %s
			WITH root, callee, length(path) AS depth,
			     callee.nodeKey AS nodeKey,
			     callee.filePath AS filePath
			RETURN DISTINCT nodeKey, callee.name AS name, filePath, depth
			ORDER BY depth, callee.name
			LIMIT 100`, maxDepth, serviceFilterClause("callee"))

		records, err := s.client.ExecuteQuery(ctx, cypher, map[string]any{
			"rootKey":      root.NodeKey,
			"scopeId":      scopeCtx.ScopeID,
			"serviceNames": serviceNames,
		})
		if err != nil {
			output.WriteString(fmt.Sprintf("Error: %v\n\n", err))
		} else if len(records) == 0 {
			output.WriteString("No downstream calls found.\n\n")
		} else {
			count := 0
			for _, r := range records {
				m := r.AsMap()
				name := getStringFromRecord(m, "name")
				file := getStringFromRecord(m, "filePath")
				depth := getIntFromRecord(m, "depth")
				if file != "" && !s.fileInWorkspace(file) {
					continue
				}

				indent := strings.Repeat("  ", depth)
				if file != "" {
					output.WriteString(fmt.Sprintf("%s→ `%s` (%s)\n", indent, name, file))
				} else {
					output.WriteString(fmt.Sprintf("%s→ `%s`\n", indent, name))
				}
				count++
			}
			if count == 0 {
				output.WriteString("No downstream calls found.\n")
			}
			output.WriteString("\n")
		}
	}

	// Upstream: what calls this function?
	if direction == "upstream" || direction == "both" {
		output.WriteString("### Upstream (callers)\n\n")
		cypher := fmt.Sprintf(`
			MATCH (target {nodeKey: $rootKey})
			MATCH path = (caller)-[:CALLS*1..%d]->(target)
			WHERE (caller:Function OR caller:Method)
			  AND (caller.scopeId = $scopeId OR caller.scopeId = 'main')
			  AND coalesce(caller.isTestFunction, false) = false
			  AND (caller.filePath IS NULL OR NOT caller.filePath ENDS WITH '_test.go')
			  %s
			WITH target, caller, length(path) AS depth,
			     caller.nodeKey AS nodeKey,
			     caller.filePath AS filePath
			RETURN DISTINCT nodeKey, caller.name AS name, filePath, depth
			ORDER BY depth, caller.name
			LIMIT 100`, maxDepth, serviceFilterClause("caller"))

		records, err := s.client.ExecuteQuery(ctx, cypher, map[string]any{
			"rootKey":      root.NodeKey,
			"scopeId":      scopeCtx.ScopeID,
			"serviceNames": serviceNames,
		})
		if err != nil {
			output.WriteString(fmt.Sprintf("Error: %v\n\n", err))
		} else if len(records) == 0 {
			output.WriteString("No upstream callers found.\n\n")
		} else {
			count := 0
			for _, r := range records {
				m := r.AsMap()
				name := getStringFromRecord(m, "name")
				file := getStringFromRecord(m, "filePath")
				depth := getIntFromRecord(m, "depth")
				if file != "" && !s.fileInWorkspace(file) {
					continue
				}

				indent := strings.Repeat("  ", depth)
				if file != "" {
					output.WriteString(fmt.Sprintf("%s← `%s` (%s)\n", indent, name, file))
				} else {
					output.WriteString(fmt.Sprintf("%s← `%s`\n", indent, name))
				}
				count++
			}
			if count == 0 {
				output.WriteString("No upstream callers found.\n")
			}
			output.WriteString("\n")
		}
	}

	return ToolCallResponse{
		Content: []ToolContent{{Type: "text", Text: output.String()}},
	}
}

func parseScopeContextArg(args map[string]interface{}) models.ScopeContext {
	rawScopeID := getOptionalStringArg(args, "scope_id")
	if rawScopeID == "" {
		return models.DefaultScope()
	}

	if strings.HasPrefix(rawScopeID, "pr-") {
		return models.ScopeContext{Scope: models.ScopePR, ScopeID: rawScopeID}
	}

	return models.ScopeContext{Scope: models.ScopeMain, ScopeID: rawScopeID}
}

func getOptionalStringArg(args map[string]interface{}, key string) string {
	if args == nil {
		return ""
	}
	if v, ok := args[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func serviceFilterClause(nodeVar string) string {
	return fmt.Sprintf(`
                  AND (size($serviceNames) = 0 OR EXISTS {
                        MATCH (svc:Service)-[:CONTAINS*1..3]->(%s)
                        WHERE (svc.scopeId = $scopeId OR svc.scopeId = 'main')
                          AND svc.name IN $serviceNames
                  })`, nodeVar)
}

func (s *CodeGraphMCPServer) resolveWorkspaceServices(ctx context.Context, scopeID, explicitService string) []string {
	if explicitService != "" {
		return []string{explicitService}
	}

	cypher := `
                MATCH (svc:Service)-[:CONTAINS]->(f:File)
                WHERE (svc.scopeId = $scopeId OR svc.scopeId = 'main')
                  AND (f.scopeId = $scopeId OR f.scopeId = 'main')
                RETURN svc.name AS serviceName, collect(DISTINCT f.filePath)[0..25] AS filePaths`

	records, err := s.client.ExecuteQuery(ctx, cypher, map[string]any{"scopeId": scopeID})
	if err != nil {
		return nil
	}

	services := make([]string, 0)
	for _, r := range records {
		m := r.AsMap()
		serviceName := getStringFromRecord(m, "serviceName")
		if serviceName == "" {
			continue
		}

		paths, ok := m["filePaths"].([]any)
		if !ok {
			continue
		}

		for _, p := range paths {
			fp, _ := p.(string)
			if s.fileInWorkspace(fp) {
				services = append(services, serviceName)
				break
			}
		}
	}

	sort.Strings(services)
	uniq := services[:0]
	for i, name := range services {
		if i == 0 || services[i-1] != name {
			uniq = append(uniq, name)
		}
	}
	return uniq
}

func (s *CodeGraphMCPServer) fileInWorkspace(filePath string) bool {
	if filePath == "" {
		return false
	}

	clean := filepath.Clean(filePath)
	if filepath.IsAbs(clean) {
		rel, err := filepath.Rel(s.workspaceRoot, clean)
		if err != nil || strings.HasPrefix(rel, "..") {
			return false
		}
		_, err = os.Stat(clean)
		return err == nil
	}

	// 1) Path already rooted at repo root.
	abs := filepath.Join(s.workspaceRoot, clean)
	if _, err := os.Stat(abs); err == nil {
		return true
	}

	// 2) Path rooted at module directories (e.g. "flow_spine.go" under
	// "libs/query-go" when a submodule was indexed in isolation).
	entries, err := os.ReadDir(s.workspaceRoot)
	if err != nil {
		return false
	}

	for _, e1 := range entries {
		if !e1.IsDir() {
			continue
		}

		cand := filepath.Join(s.workspaceRoot, e1.Name(), clean)
		if _, err := os.Stat(cand); err == nil {
			return true
		}

		l1 := filepath.Join(s.workspaceRoot, e1.Name())
		subEntries, err := os.ReadDir(l1)
		if err != nil {
			continue
		}
		for _, e2 := range subEntries {
			if !e2.IsDir() {
				continue
			}
			cand2 := filepath.Join(l1, e2.Name(), clean)
			if _, err := os.Stat(cand2); err == nil {
				return true
			}
		}
	}

	return false
}

func (s *CodeGraphMCPServer) filterFlowsToWorkspace(ctx context.Context, scopeID string, flows []query.FlowSpineResult) []query.FlowSpineResult {
	if len(flows) == 0 {
		return flows
	}

	nodeKeys := make([]string, 0)
	seen := make(map[string]bool)
	for _, flow := range flows {
		for _, step := range flow.Steps {
			if step.Label != "Function" && step.Label != "Method" {
				continue
			}
			if step.NodeKey == "" || seen[step.NodeKey] {
				continue
			}
			seen[step.NodeKey] = true
			nodeKeys = append(nodeKeys, step.NodeKey)
		}
	}

	if len(nodeKeys) == 0 {
		return nil
	}

	cypher := `
                UNWIND $nodeKeys AS nk
                MATCH (n {nodeKey: nk})
                WHERE (n:Function OR n:Method)
                  AND (n.scopeId = $scopeId OR n.scopeId = 'main')
                RETURN nk AS nodeKey, coalesce(n.filePath, '') AS filePath`

	records, err := s.client.ExecuteQuery(ctx, cypher, map[string]any{"nodeKeys": nodeKeys, "scopeId": scopeID})
	if err != nil {
		return flows
	}

	workspaceNode := make(map[string]bool)
	for _, r := range records {
		m := r.AsMap()
		nk := getStringFromRecord(m, "nodeKey")
		if nk == "" {
			continue
		}
		fp := getStringFromRecord(m, "filePath")
		if fp != "" && s.fileInWorkspace(fp) {
			workspaceNode[nk] = true
		}
	}

	filtered := make([]query.FlowSpineResult, 0, len(flows))
	for _, flow := range flows {
		belongs := false
		for _, step := range flow.Steps {
			if step.Label != "Function" && step.Label != "Method" {
				continue
			}
			if workspaceNode[step.NodeKey] {
				belongs = true
				break
			}
		}
		if belongs {
			filtered = append(filtered, flow)
		}
	}

	return filtered
}
