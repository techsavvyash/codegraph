package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	models "github.com/context-maximiser/code-graph/libs/core-models-go"
	"github.com/context-maximiser/code-graph/libs/indexer-go/documents"
	"github.com/context-maximiser/code-graph/libs/neo4j-go"
	"github.com/context-maximiser/code-graph/libs/query-go"
	"github.com/context-maximiser/code-graph/libs/search-go"
	"github.com/joho/godotenv"
	neo4jdriver "github.com/neo4j/neo4j-go-driver/v5/neo4j"
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
	// Per JSON-RPC 2.0, a notification has no id and the server MUST NOT
	// respond. Detect that before dispatch — if we send an error response
	// to a notification (with id=null), strict MCP clients (Claude Code's
	// Zod parser) reject the message and drop the connection.
	isNotification := request.ID == nil

	switch request.Method {
	case "initialize":
		s.handleInitialize(request)
	case "tools/list":
		s.handleToolsList(request)
	case "tools/call":
		s.handleToolCall(request)
	case "notifications/initialized", "notifications/cancelled", "notifications/progress":
		// Known notifications — silently acknowledged.
	case "ping":
		s.sendResponse(request.ID, map[string]interface{}{})
	default:
		if !isNotification {
			s.sendError(request.ID, -32601, "Method not found")
		}
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
		{
			Name:        "codegraph_entry_points",
			Description: "List structurally-detected entry points (4-tier classification: API-exposed, interface impls with no callers, exported topological roots, high-centrality functions). Same algorithm as codegraph_get_entry_points but with format=json|text|mermaid.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"tier":         map[string]interface{}{"type": "number", "description": "Restrict to a single tier (1-4); omit for all"},
					"limit":        map[string]interface{}{"type": "number", "description": "Max entries (default 50)", "default": 50},
					"scope_id":     map[string]interface{}{"type": "string", "description": "Scope ID (default: main)", "default": "main"},
					"service_name": map[string]interface{}{"type": "string", "description": "Optional service filter"},
					"format":       map[string]interface{}{"type": "string", "enum": []string{"json", "text", "mermaid"}, "default": "json"},
				},
			},
		},
		{
			Name:        "codegraph_flows",
			Description: "Generate flow spines from entry points using the multi-strategy seed finder + traversal budget. Same algorithm as codegraph_generate_flows but with format=json|text|mermaid (each flow renders as a graph LR chain).",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"max_depth":    map[string]interface{}{"type": "number", "description": "Traversal depth per flow (default 5)", "default": 5},
					"limit":        map[string]interface{}{"type": "number", "description": "Max flows returned (default 20)", "default": 20},
					"scope_id":     map[string]interface{}{"type": "string", "default": "main"},
					"service_name": map[string]interface{}{"type": "string", "description": "Optional service filter"},
					"format":       map[string]interface{}{"type": "string", "enum": []string{"json", "text", "mermaid"}, "default": "json"},
				},
			},
		},
		{
			Name:        "codegraph_cypher",
			Description: "Advanced/escape-hatch tool: run a read-only Cypher query directly against Neo4j. Prefer find/expand/path/source for common questions. Write operations (CREATE, MERGE, DELETE, SET, REMOVE, DROP, FOREACH, LOAD CSV) are rejected. Hard caps: timeout 5000ms, 1000 rows.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{
						"type":        "string",
						"description": "Cypher query (read-only). Use codegraph_schema to learn labels and rel types.",
					},
					"params": map[string]interface{}{
						"type":        "object",
						"description": "Optional parameter map for the query (Cypher $-prefixed parameters)",
					},
					"timeout_ms": map[string]interface{}{
						"type":        "number",
						"description": "Query timeout in milliseconds (default 3000, hard cap 5000)",
						"default":     3000,
					},
					"row_limit": map[string]interface{}{
						"type":        "number",
						"description": "Maximum rows returned (default 100, hard cap 1000)",
						"default":     100,
					},
				},
				"required": []string{"query"},
			},
		},
		{
			Name:        "codegraph_path",
			Description: "Find path(s) between two nodes following the given relationship types. Use shortest=true (default) for canonical reachability questions; shortest=false returns all paths up to max_hops (capped at 25 paths). Supports text/json/mermaid output.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"from_id": map[string]interface{}{
						"type":        "string",
						"description": "Opaque node_id of the starting node (from find/expand)",
					},
					"to_id": map[string]interface{}{
						"type":        "string",
						"description": "Opaque node_id of the destination node",
					},
					"rel_types": map[string]interface{}{
						"type":        "array",
						"items":       map[string]interface{}{"type": "string"},
						"description": "Relationship types the path may traverse (e.g. [\"CALLS\"], [\"CALLS\",\"CALLS_API\"])",
					},
					"max_hops": map[string]interface{}{
						"type":        "number",
						"description": "Maximum path length (default 6, hard cap 20)",
						"default":     6,
					},
					"shortest": map[string]interface{}{
						"type":        "boolean",
						"description": "If true, return all shortest paths only; if false, return up to 25 paths up to max_hops (default: true)",
						"default":     true,
					},
					"direction": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"out", "in", "both"},
						"description": "Edge direction relative to from→to (default: out)",
						"default":     "out",
					},
					"format": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"json", "text", "mermaid"},
						"default":     "json",
					},
				},
				"required": []string{"from_id", "to_id", "rel_types"},
			},
		},
		{
			Name:        "codegraph_source",
			Description: "Fetch source code for a function or method, addressed by node_id (preferred, from find/expand) or symbol_name. Returns the code with file path and line range.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"node_id": map[string]interface{}{
						"type":        "string",
						"description": "Opaque node_id from a previous find/expand call (preferred — unambiguous)",
					},
					"symbol_name": map[string]interface{}{
						"type":        "string",
						"description": "Name of a function or method (used when node_id is not available; ambiguous if multiple matches exist)",
					},
				},
			},
		},
		{
			Name:        "codegraph_expand",
			Description: "Traverse edges from a starting node along given relationship types. The most-used primitive: replaces find_callers/callees, find_references, service_dependencies, etc. by composing rel_types and direction. Supports text/json/mermaid output for inline rendering in chat clients.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"node_id": map[string]interface{}{
						"type":        "string",
						"description": "Opaque node_id from a previous find/expand call",
					},
					"rel_types": map[string]interface{}{
						"type":        "array",
						"items":       map[string]interface{}{"type": "string"},
						"description": "Relationship types to follow (e.g. [\"CALLS\"], [\"IMPLEMENTS\"], [\"DEPENDS_ON\"]). See codegraph_schema for the catalog.",
					},
					"direction": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"in", "out", "both"},
						"description": "Edge direction relative to the start node (default: out)",
						"default":     "out",
					},
					"depth": map[string]interface{}{
						"type":        "number",
						"description": "Max hops (default 1, hard cap 10)",
						"default":     1,
					},
					"limit": map[string]interface{}{
						"type":        "number",
						"description": "Max nodes returned (default 50, hard cap 500)",
						"default":     50,
					},
					"format": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"json", "text", "mermaid"},
						"description": "Response format (default json)",
						"default":     "json",
					},
				},
				"required": []string{"node_id", "rel_types"},
			},
		},
		{
			Name:        "codegraph_find",
			Description: "List/filter nodes in the code graph by label, name pattern, and/or service. Returns paginated results, each including a node_id usable as input to expand/path. Pair with codegraph_schema to discover available labels and properties.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"label": map[string]interface{}{
						"type":        "string",
						"description": "Restrict to a node label (Function, Method, Class, Interface, Service, Module, File, Variable, APIRoute, etc.)",
					},
					"name_pattern": map[string]interface{}{
						"type":        "string",
						"description": "Case-insensitive substring match against the node's name (or path for File nodes)",
					},
					"service": map[string]interface{}{
						"type":        "string",
						"description": "Filter to nodes owned by a specific service (e.g. codegraph/apps/cli)",
					},
					"limit": map[string]interface{}{
						"type":        "number",
						"description": "Max results per page (default 25, hard cap 200)",
						"default":     25,
					},
					"cursor": map[string]interface{}{
						"type":        "string",
						"description": "Opaque pagination cursor returned by a previous call's next_cursor",
					},
				},
			},
		},
		{
			Name: "codegraph_schema",
			Description: "Describe the code graph contract: node labels with their properties, relationship types with valid (from-label, to-label) endpoint pairs, and counts per category. Call this once before composing other tools — it is the discoverability primitive that lets `find`, `expand`, and `path` be used correctly without guessing relationship type names.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"include_examples": map[string]interface{}{
						"type":        "boolean",
						"description": "Include canonical example tool invocations alongside the schema (default: true)",
						"default":     true,
					},
				},
			},
		},
	}

	visible := tools[:0]
	for _, t := range tools {
		if retiredTools[t.Name] {
			continue
		}
		visible = append(visible, t)
	}

	result := map[string]interface{}{
		"tools": visible,
	}

	s.sendResponse(request.ID, result)
}

// retiredTools are superseded by RFC-004 primitives. Handler code is retained
// for now; tools are hidden from tools/list and rejected in tools/call so
// stale clients can't invoke them by name. Code deletion deferred.
var retiredTools = map[string]bool{
	"codegraph_search":                true, // → find
	"codegraph_get_source":            true, // renamed → source
	"codegraph_find_references":       true, // → expand(_, [REFERENCES], in)
	"codegraph_analyze_function":      true, // → find + expand([CALLS]) + source
	"codegraph_hybrid_search":         true, // doc-linking out of scope
	"codegraph_vector_search":         true, // doc-linking out of scope
	"codegraph_index_documents":       true, // doc-linking out of scope
	"codegraph_search_docs":           true, // doc-linking out of scope
	"codegraph_search_by_comment":     true, // doc-linking out of scope
	"codegraph_link_docs_to_code":     true, // doc-linking out of scope
	"codegraph_intelligent_link":      true, // doc-linking out of scope
	"codegraph_list_services":         true, // → find(label=Service)
	"codegraph_service_dependencies":  true, // → expand
	"codegraph_service_api_endpoints": true, // → expand
	"codegraph_service_api_calls":     true, // → expand
	"codegraph_cross_service_calls":   true, // → path
	"codegraph_service_architecture":  true, // → composition of find + expand
	"codegraph_get_entry_points":      true, // renamed → entry_points
	"codegraph_generate_flows":        true, // renamed → flows
	"codegraph_trace_call_graph":      true, // → expand([CALLS], depth)
}

func (s *CodeGraphMCPServer) handleToolCall(request MCPRequest) {
	var toolCall ToolCallRequest
	paramsBytes, _ := json.Marshal(request.Params)
	if err := json.Unmarshal(paramsBytes, &toolCall); err != nil {
		s.sendError(request.ID, -32602, "Invalid params")
		return
	}

	if retiredTools[toolCall.Name] {
		s.sendError(request.ID, -32601, fmt.Sprintf("Tool %q is retired (RFC-004). Use codegraph_schema to discover the replacement primitive.", toolCall.Name))
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
	case "codegraph_schema":
		response = s.handleSchemaTool(ctx, toolCall.Arguments)
	case "codegraph_find":
		response = s.handleFindTool(ctx, toolCall.Arguments)
	case "codegraph_expand":
		response = s.handleExpandTool(ctx, toolCall.Arguments)
	case "codegraph_source":
		response = s.handleSourceToolV2(ctx, toolCall.Arguments)
	case "codegraph_path":
		response = s.handlePathTool(ctx, toolCall.Arguments)
	case "codegraph_cypher":
		response = s.handleCypherTool(ctx, toolCall.Arguments)
	case "codegraph_entry_points":
		response = s.handleEntryPointsToolV2(ctx, toolCall.Arguments)
	case "codegraph_flows":
		response = s.handleFlowsToolV2(ctx, toolCall.Arguments)
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

// handleSchemaTool returns the graph contract: node labels with their
// properties, relationship types with valid (from-label, to-label) endpoint
// pairs, and counts per category. This is the discoverability primitive — it
// lets agents compose `find`, `expand`, and `path` correctly without guessing
// relationship type names.
func (s *CodeGraphMCPServer) handleSchemaTool(ctx context.Context, args map[string]interface{}) ToolCallResponse {
	includeExamples := true
	if v, ok := args["include_examples"].(bool); ok {
		includeExamples = v
	}

	type propInfo struct {
		Name string `json:"name"`
		Type string `json:"type,omitempty"`
	}
	type labelInfo struct {
		Label      string     `json:"label"`
		Count      int        `json:"count"`
		Properties []propInfo `json:"properties,omitempty"`
	}
	type endpoint struct {
		From  string `json:"from"`
		To    string `json:"to"`
		Count int    `json:"count"`
	}
	type relInfo struct {
		Type      string     `json:"type"`
		Count     int        `json:"count"`
		Endpoints []endpoint `json:"endpoints"`
	}

	// Labels with counts. UNWIND handles multi-label nodes correctly.
	labelRecords, err := s.client.ExecuteQuery(ctx,
		`MATCH (n) WITH labels(n) AS lbls
		 UNWIND lbls AS label
		 RETURN label, count(*) AS count
		 ORDER BY label`, nil)
	if err != nil {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("schema: failed to enumerate labels: %v", err)}},
			IsError: true,
		}
	}

	labels := []labelInfo{}
	labelByName := map[string]int{}
	for _, rec := range labelRecords {
		m := rec.AsMap()
		name := getStringFromRecord(m, "label")
		if name == "" {
			continue
		}
		count := getIntFromRecord(m, "count")
		labelByName[name] = len(labels)
		labels = append(labels, labelInfo{Label: name, Count: count})
	}

	// Properties per label via Neo4j's built-in introspection (no APOC).
	propRecords, _ := s.client.ExecuteQuery(ctx,
		`CALL db.schema.nodeTypeProperties() YIELD nodeType, propertyName, propertyTypes
		 RETURN nodeType, propertyName, propertyTypes`, nil)
	for _, rec := range propRecords {
		m := rec.AsMap()
		nt := getStringFromRecord(m, "nodeType")
		propName := getStringFromRecord(m, "propertyName")
		if propName == "" {
			continue
		}
		propType := ""
		if pts, ok := m["propertyTypes"].([]interface{}); ok && len(pts) > 0 {
			if first, ok := pts[0].(string); ok {
				propType = first
			}
		}
		for _, lbl := range parseSchemaNodeType(nt) {
			idx, ok := labelByName[lbl]
			if !ok {
				continue
			}
			labels[idx].Properties = append(labels[idx].Properties, propInfo{Name: propName, Type: propType})
		}
	}
	for i := range labels {
		sort.Slice(labels[i].Properties, func(a, b int) bool {
			return labels[i].Properties[a].Name < labels[i].Properties[b].Name
		})
	}

	// Relationship endpoints with counts. Groups by (from-label, rel-type, to-label).
	relMap := map[string]*relInfo{}
	edgeRecords, err := s.client.ExecuteQuery(ctx,
		`MATCH (a)-[r]->(b)
		 RETURN labels(a)[0] AS fromLabel, type(r) AS relType, labels(b)[0] AS toLabel, count(*) AS c`, nil)
	if err != nil {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("schema: failed to enumerate relationships: %v", err)}},
			IsError: true,
		}
	}
	for _, rec := range edgeRecords {
		m := rec.AsMap()
		from := getStringFromRecord(m, "fromLabel")
		to := getStringFromRecord(m, "toLabel")
		rt := getStringFromRecord(m, "relType")
		if rt == "" || from == "" || to == "" {
			continue
		}
		c := getIntFromRecord(m, "c")
		ri, ok := relMap[rt]
		if !ok {
			ri = &relInfo{Type: rt}
			relMap[rt] = ri
		}
		ri.Count += c
		ri.Endpoints = append(ri.Endpoints, endpoint{From: from, To: to, Count: c})
	}
	relTypes := make([]*relInfo, 0, len(relMap))
	for _, ri := range relMap {
		sort.Slice(ri.Endpoints, func(a, b int) bool {
			if ri.Endpoints[a].From != ri.Endpoints[b].From {
				return ri.Endpoints[a].From < ri.Endpoints[b].From
			}
			return ri.Endpoints[a].To < ri.Endpoints[b].To
		})
		relTypes = append(relTypes, ri)
	}
	sort.Slice(relTypes, func(a, b int) bool { return relTypes[a].Type < relTypes[b].Type })

	out := map[string]interface{}{
		"nodes":         labels,
		"relationships": relTypes,
	}
	if includeExamples {
		out["examples"] = schemaExamples()
		out["notes"] = "Examples in `examples` reference the RFC-004 primitives (find, expand, path, source, cypher, entry_points, flows). Each example shows the recommended composition for a common code-intelligence question."
	}

	body, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("schema: failed to encode: %v", err)}},
			IsError: true,
		}
	}
	return ToolCallResponse{
		Content: []ToolContent{{Type: "text", Text: string(body)}},
	}
}

// parseSchemaNodeType splits Neo4j's nodeType strings (e.g. ":`Function`" or
// ":`Function`:`Symbol`") into a list of label names.
func parseSchemaNodeType(nt string) []string {
	nt = strings.TrimPrefix(nt, ":")
	parts := strings.Split(nt, ":")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.Trim(p, "`")
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func schemaExamples() []map[string]interface{} {
	return []map[string]interface{}{
		{
			"description": "List every Service in the graph",
			"steps": []map[string]interface{}{
				{"tool": "find", "args": map[string]interface{}{"label": "Service"}},
			},
		},
		{
			"description": "Find direct callers of a function: first locate it by name, then expand inbound CALLS one hop",
			"steps": []map[string]interface{}{
				{"tool": "find", "args": map[string]interface{}{"label": "Function", "name_pattern": "IndexProject"}},
				{"tool": "expand", "args": map[string]interface{}{"node_id": "<id from previous step>", "rel_types": []string{"CALLS"}, "direction": "in", "depth": 1}},
			},
		},
		{
			"description": "Compute the blast radius of a method (transitive callers up to depth 4)",
			"steps": []map[string]interface{}{
				{"tool": "find", "args": map[string]interface{}{"label": "Method", "name_pattern": "ExecuteQuery"}},
				{"tool": "expand", "args": map[string]interface{}{"node_id": "<id>", "rel_types": []string{"CALLS"}, "direction": "in", "depth": 4, "format": "mermaid"}},
			},
		},
		{
			"description": "Find all implementations of a Go interface",
			"steps": []map[string]interface{}{
				{"tool": "find", "args": map[string]interface{}{"label": "Interface", "name_pattern": "Client"}},
				{"tool": "expand", "args": map[string]interface{}{"node_id": "<id>", "rel_types": []string{"IMPLEMENTS"}, "direction": "in"}},
			},
		},
		{
			"description": "Shortest call chain between two functions",
			"steps": []map[string]interface{}{
				{"tool": "path", "args": map[string]interface{}{"from_id": "<source id>", "to_id": "<target id>", "rel_types": []string{"CALLS"}, "max_hops": 8, "shortest": true, "format": "mermaid"}},
			},
		},
		{
			"description": "Service-level dependency graph (which services this one imports from)",
			"steps": []map[string]interface{}{
				{"tool": "find", "args": map[string]interface{}{"label": "Service", "name_pattern": "codegraph/apps/cli"}},
				{"tool": "expand", "args": map[string]interface{}{"node_id": "<id>", "rel_types": []string{"DEPENDS_ON"}, "direction": "out", "depth": 2}},
			},
		},
	}
}

// handleFindTool is the L1 node-listing primitive. It supports filtering by
// label, name substring, and serviceName, with simple offset-based pagination
// via an opaque cursor. Each result carries an opaque node_id usable as input
// to expand/path.
func (s *CodeGraphMCPServer) handleFindTool(ctx context.Context, args map[string]interface{}) ToolCallResponse {
	label, _ := args["label"].(string)
	namePattern, _ := args["name_pattern"].(string)
	service, _ := args["service"].(string)

	limit := 25
	if l, ok := args["limit"].(float64); ok {
		limit = int(l)
	}
	if limit < 1 {
		limit = 25
	}
	if limit > 200 {
		limit = 200
	}

	skip := 0
	if cursor, ok := args["cursor"].(string); ok && cursor != "" {
		if v, err := strconv.Atoi(cursor); err == nil && v >= 0 {
			skip = v
		}
	}

	conditions := []string{}
	params := map[string]any{
		"skip":  skip,
		"limit": limit + 1, // overfetch by 1 to detect more pages
	}
	if label != "" {
		conditions = append(conditions, "$label IN labels(n)")
		params["label"] = label
	}
	if namePattern != "" {
		conditions = append(conditions, "(toLower(coalesce(n.name,'')) CONTAINS toLower($namePattern) OR toLower(coalesce(n.path,'')) CONTAINS toLower($namePattern))")
		params["namePattern"] = namePattern
	}
	if service != "" {
		conditions = append(conditions, "n.serviceName = $service")
		params["service"] = service
	}
	where := ""
	if len(conditions) > 0 {
		where = "WHERE " + strings.Join(conditions, " AND ")
	}

	cypher := fmt.Sprintf(`
MATCH (n)
%s
RETURN elementId(n) AS node_id,
       labels(n)[0] AS label,
       coalesce(n.name, n.path, '') AS name,
       n.fqn AS qualified_name,
       n.filePath AS file_path,
       n.startLine AS start_line,
       n.signature AS signature,
       n.serviceName AS service
ORDER BY label, name
SKIP $skip LIMIT $limit
`, where)

	records, err := s.client.ExecuteQuery(ctx, cypher, params)
	if err != nil {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("find: query failed: %v", err)}},
			IsError: true,
		}
	}

	type result struct {
		NodeID        string `json:"node_id"`
		Label         string `json:"label"`
		Name          string `json:"name,omitempty"`
		QualifiedName string `json:"qualified_name,omitempty"`
		FilePath      string `json:"file_path,omitempty"`
		StartLine     int    `json:"start_line,omitempty"`
		Signature     string `json:"signature,omitempty"`
		Service       string `json:"service,omitempty"`
	}

	results := make([]result, 0, len(records))
	for _, rec := range records {
		m := rec.AsMap()
		results = append(results, result{
			NodeID:        getStringFromRecord(m, "node_id"),
			Label:         getStringFromRecord(m, "label"),
			Name:          getStringFromRecord(m, "name"),
			QualifiedName: getStringFromRecord(m, "qualified_name"),
			FilePath:      getStringFromRecord(m, "file_path"),
			StartLine:     getIntFromRecord(m, "start_line"),
			Signature:     getStringFromRecord(m, "signature"),
			Service:       getStringFromRecord(m, "service"),
		})
	}

	truncated := len(results) > limit
	if truncated {
		results = results[:limit]
	}

	out := map[string]interface{}{
		"results":   results,
		"truncated": truncated,
		"count":     len(results),
	}
	if truncated {
		out["next_cursor"] = strconv.Itoa(skip + limit)
	}

	body, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("find: encode failed: %v", err)}},
			IsError: true,
		}
	}
	return ToolCallResponse{
		Content: []ToolContent{{Type: "text", Text: string(body)}},
	}
}

type expandNode struct {
	NodeID        string `json:"node_id"`
	Label         string `json:"label"`
	Name          string `json:"name,omitempty"`
	QualifiedName string `json:"qualified_name,omitempty"`
	FilePath      string `json:"file_path,omitempty"`
	StartLine     int    `json:"start_line,omitempty"`
	Signature     string `json:"signature,omitempty"`
	Service       string `json:"service,omitempty"`
	Distance      int    `json:"distance"`
}

type expandEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Type string `json:"type"`
}

// handleExpandTool traverses edges from a known starting node along the
// requested relationship types and direction, up to a depth bound. Returns
// reachable nodes (with shortest-path distance) and the connecting edges.
// Supports json/text/mermaid output formats.
func (s *CodeGraphMCPServer) handleExpandTool(ctx context.Context, args map[string]interface{}) ToolCallResponse {
	nodeID, ok := args["node_id"].(string)
	if !ok || nodeID == "" {
		return errorResponse("expand: node_id is required (use codegraph_find to obtain one)")
	}

	relTypesRaw, _ := args["rel_types"].([]interface{})
	relTypes := []string{}
	for _, rt := range relTypesRaw {
		if str, ok := rt.(string); ok && str != "" {
			relTypes = append(relTypes, str)
		}
	}
	if len(relTypes) == 0 {
		return errorResponse("expand: rel_types must contain at least one type (call codegraph_schema to list valid types)")
	}

	direction := "out"
	if d, ok := args["direction"].(string); ok && d != "" {
		direction = d
	}
	if direction != "in" && direction != "out" && direction != "both" {
		return errorResponse("expand: direction must be 'in', 'out', or 'both'")
	}

	depth := 1
	if d, ok := args["depth"].(float64); ok {
		depth = int(d)
	}
	if depth < 1 {
		depth = 1
	}
	if depth > 10 {
		depth = 10
	}

	limit := 50
	if l, ok := args["limit"].(float64); ok {
		limit = int(l)
	}
	if limit < 1 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}

	format := "json"
	if f, ok := args["format"].(string); ok && f != "" {
		format = f
	}

	// Cypher does not allow parameterized direction or rel-type list inside
	// a variable-length pattern, so build the pattern fragment carefully.
	// Backticks around each rel type defend against names with special chars.
	relTypeFilter := ":`" + strings.Join(relTypes, "`|`") + "`"
	var leftArrow, rightArrow string
	switch direction {
	case "out":
		leftArrow, rightArrow = "-", "->"
	case "in":
		leftArrow, rightArrow = "<-", "-"
	case "both":
		leftArrow, rightArrow = "-", "-"
	}

	// Fetch the start node first so we can include it in output regardless
	// of whether any traversal succeeds.
	startRecords, err := s.client.ExecuteQuery(ctx,
		`MATCH (n) WHERE elementId(n) = $startId
		 RETURN elementId(n) AS node_id, labels(n)[0] AS label,
		        coalesce(n.name, n.path, '') AS name,
		        n.fqn AS qualified_name,
		        n.filePath AS file_path,
		        n.startLine AS start_line,
		        n.signature AS signature,
		        n.serviceName AS service`,
		map[string]any{"startId": nodeID})
	if err != nil {
		return errorResponse(fmt.Sprintf("expand: lookup of start node failed: %v", err))
	}
	if len(startRecords) == 0 {
		return errorResponse(fmt.Sprintf("expand: starting node not found (node_id=%s)", nodeID))
	}
	startMap := startRecords[0].AsMap()
	startNode := expandNode{
		NodeID:        getStringFromRecord(startMap, "node_id"),
		Label:         getStringFromRecord(startMap, "label"),
		Name:          getStringFromRecord(startMap, "name"),
		QualifiedName: getStringFromRecord(startMap, "qualified_name"),
		FilePath:      getStringFromRecord(startMap, "file_path"),
		StartLine:     getIntFromRecord(startMap, "start_line"),
		Signature:     getStringFromRecord(startMap, "signature"),
		Service:       getStringFromRecord(startMap, "service"),
		Distance:      0,
	}

	cypher := fmt.Sprintf(`
MATCH (start) WHERE elementId(start) = $startId
MATCH path = (start)%s[r%s*1..%d]%s(end)
WITH end, min(length(path)) AS distance
RETURN elementId(end) AS node_id,
       labels(end)[0] AS label,
       coalesce(end.name, end.path, '') AS name,
       end.fqn AS qualified_name,
       end.filePath AS file_path,
       end.startLine AS start_line,
       end.signature AS signature,
       end.serviceName AS service,
       distance
ORDER BY distance, label, name
LIMIT %d
`, leftArrow, relTypeFilter, depth, rightArrow, limit+1)

	records, err := s.client.ExecuteQuery(ctx, cypher, map[string]any{"startId": nodeID})
	if err != nil {
		return errorResponse(fmt.Sprintf("expand: traversal failed: %v", err))
	}

	nodes := []expandNode{startNode}
	seen := map[string]bool{startNode.NodeID: true}
	for _, rec := range records {
		m := rec.AsMap()
		nid := getStringFromRecord(m, "node_id")
		if nid == "" || seen[nid] {
			continue
		}
		seen[nid] = true
		nodes = append(nodes, expandNode{
			NodeID:        nid,
			Label:         getStringFromRecord(m, "label"),
			Name:          getStringFromRecord(m, "name"),
			QualifiedName: getStringFromRecord(m, "qualified_name"),
			FilePath:      getStringFromRecord(m, "file_path"),
			StartLine:     getIntFromRecord(m, "start_line"),
			Signature:     getStringFromRecord(m, "signature"),
			Service:       getStringFromRecord(m, "service"),
			Distance:      getIntFromRecord(m, "distance"),
		})
	}

	truncated := len(nodes)-1 > limit
	if truncated {
		nodes = nodes[:limit+1]
	}

	// Edges among the returned node set, restricted to requested rel types.
	edges := []expandEdge{}
	if len(nodes) > 1 {
		ids := make([]string, len(nodes))
		for i, n := range nodes {
			ids[i] = n.NodeID
		}
		edgeRecords, err := s.client.ExecuteQuery(ctx,
			`MATCH (a)-[r]->(b)
			 WHERE elementId(a) IN $ids AND elementId(b) IN $ids
			   AND type(r) IN $relTypes
			 RETURN elementId(a) AS from, elementId(b) AS to, type(r) AS type`,
			map[string]any{"ids": ids, "relTypes": relTypes})
		if err == nil {
			for _, rec := range edgeRecords {
				m := rec.AsMap()
				edges = append(edges, expandEdge{
					From: getStringFromRecord(m, "from"),
					To:   getStringFromRecord(m, "to"),
					Type: getStringFromRecord(m, "type"),
				})
			}
		}
	}

	switch format {
	case "mermaid":
		return ToolCallResponse{Content: []ToolContent{{Type: "text", Text: renderExpandMermaid(nodes, edges)}}}
	case "text":
		return ToolCallResponse{Content: []ToolContent{{Type: "text", Text: renderExpandText(nodes, edges, truncated)}}}
	default:
		out := map[string]interface{}{
			"start":      startNode,
			"nodes":      nodes,
			"edges":      edges,
			"node_count": len(nodes),
			"edge_count": len(edges),
			"truncated":  truncated,
		}
		body, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			return errorResponse(fmt.Sprintf("expand: encode failed: %v", err))
		}
		return ToolCallResponse{Content: []ToolContent{{Type: "text", Text: string(body)}}}
	}
}

func renderExpandMermaid(nodes []expandNode, edges []expandEdge) string {
	var b strings.Builder
	b.WriteString("```mermaid\ngraph TD\n")
	idMap := make(map[string]string, len(nodes))
	for i, n := range nodes {
		shortID := fmt.Sprintf("N%d", i)
		idMap[n.NodeID] = shortID
		label := n.Name
		if label == "" {
			label = n.Label
		}
		// Escape characters that break Mermaid node text
		label = strings.ReplaceAll(label, "\"", "'")
		label = strings.ReplaceAll(label, "\n", " ")
		fmt.Fprintf(&b, "  %s[\"%s: %s\"]\n", shortID, n.Label, label)
	}
	for _, e := range edges {
		from, ok1 := idMap[e.From]
		to, ok2 := idMap[e.To]
		if !ok1 || !ok2 {
			continue
		}
		fmt.Fprintf(&b, "  %s -->|%s| %s\n", from, e.Type, to)
	}
	b.WriteString("```\n")
	return b.String()
}

func renderExpandText(nodes []expandNode, edges []expandEdge, truncated bool) string {
	var b strings.Builder
	if len(nodes) > 0 {
		fmt.Fprintf(&b, "Start: %s %s\n", nodes[0].Label, nodes[0].Name)
		if nodes[0].FilePath != "" {
			fmt.Fprintf(&b, "  %s", nodes[0].FilePath)
			if nodes[0].StartLine > 0 {
				fmt.Fprintf(&b, ":%d", nodes[0].StartLine)
			}
			b.WriteString("\n")
		}
	}
	fmt.Fprintf(&b, "\nReached %d node(s) via %d edge(s)", len(nodes)-1, len(edges))
	if truncated {
		b.WriteString(" (truncated)")
	}
	b.WriteString(":\n\n")

	byDistance := map[int][]expandNode{}
	maxD := 0
	for _, n := range nodes[1:] {
		byDistance[n.Distance] = append(byDistance[n.Distance], n)
		if n.Distance > maxD {
			maxD = n.Distance
		}
	}
	for d := 1; d <= maxD; d++ {
		group := byDistance[d]
		if len(group) == 0 {
			continue
		}
		fmt.Fprintf(&b, "Depth %d (%d):\n", d, len(group))
		for _, n := range group {
			fmt.Fprintf(&b, "  - %s %s", n.Label, n.Name)
			if n.FilePath != "" {
				fmt.Fprintf(&b, " (%s", n.FilePath)
				if n.StartLine > 0 {
					fmt.Fprintf(&b, ":%d", n.StartLine)
				}
				b.WriteString(")")
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}

func errorResponse(msg string) ToolCallResponse {
	return ToolCallResponse{
		Content: []ToolContent{{Type: "text", Text: msg}},
		IsError: true,
	}
}

// handleFlowsToolV2 is the RFC-004 flows primitive. Wraps the existing
// FlowSpineGenerator logic with format=json|text|mermaid output.
func (s *CodeGraphMCPServer) handleFlowsToolV2(ctx context.Context, args map[string]interface{}) ToolCallResponse {
	maxDepth := 5
	if d, ok := args["max_depth"].(float64); ok && d > 0 {
		maxDepth = int(d)
	}
	limit := 20
	if l, ok := args["limit"].(float64); ok && l > 0 {
		limit = int(l)
	}
	format := "json"
	if f, ok := args["format"].(string); ok && f != "" {
		format = f
	}

	scopeCtx := parseScopeContextArg(args)
	serviceNames := s.resolveWorkspaceServices(ctx, scopeCtx.ScopeID, getOptionalStringArg(args, "service_name"))

	gen := query.NewFlowSpineGenerator(s.client)
	gen.SetScope(scopeCtx)
	gen.SetServiceFilter(serviceNames)
	flows, err := gen.GenerateFlows(ctx, maxDepth)
	if err != nil {
		return errorResponse(fmt.Sprintf("flows: generation failed: %v", err))
	}
	flows = s.filterFlowsToWorkspace(ctx, scopeCtx.ScopeID, flows)
	if len(flows) > limit {
		flows = flows[:limit]
	}
	if len(flows) == 0 {
		return ToolCallResponse{Content: []ToolContent{{Type: "text", Text: "No flows generated for this scope. Re-index with `index pipeline` if call graph data is missing."}}}
	}

	switch format {
	case "mermaid":
		return ToolCallResponse{Content: []ToolContent{{Type: "text", Text: renderFlowsMermaid(flows)}}}
	case "text":
		return ToolCallResponse{Content: []ToolContent{{Type: "text", Text: renderFlowsText(flows)}}}
	default:
		body, err := json.MarshalIndent(map[string]interface{}{
			"flow_count": len(flows),
			"flows":      flows,
		}, "", "  ")
		if err != nil {
			return errorResponse(fmt.Sprintf("flows: encode failed: %v", err))
		}
		return ToolCallResponse{Content: []ToolContent{{Type: "text", Text: string(body)}}}
	}
}

func renderFlowsMermaid(flows []query.FlowSpineResult) string {
	var b strings.Builder
	b.WriteString("```mermaid\ngraph LR\n")
	for fi, f := range flows {
		// Subgraph per flow keeps multi-flow output readable.
		fmt.Fprintf(&b, "  subgraph F%d[\"%s\"]\n", fi, escapeMermaidLabel(f.FlowName))
		for si, step := range f.Steps {
			fmt.Fprintf(&b, "    F%dS%d[\"%s: %s\"]\n", fi, si, step.Label, escapeMermaidLabel(step.Name))
		}
		for si := 1; si < len(f.Steps); si++ {
			fmt.Fprintf(&b, "    F%dS%d --> F%dS%d\n", fi, si-1, fi, si)
		}
		b.WriteString("  end\n")
	}
	b.WriteString("```\n")
	return b.String()
}

func renderFlowsText(flows []query.FlowSpineResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d flow(s):\n\n", len(flows))
	for i, f := range flows {
		fmt.Fprintf(&b, "%d. %s [%s]\n", i+1, f.FlowName, f.FlowType)
		for _, step := range f.Steps {
			fmt.Fprintf(&b, "%s  %d. %s (%s)\n", strings.Repeat("  ", step.Order), step.Order+1, step.Name, step.Label)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func escapeMermaidLabel(s string) string {
	s = strings.ReplaceAll(s, "\"", "'")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "[", "(")
	s = strings.ReplaceAll(s, "]", ")")
	return s
}

// handleEntryPointsToolV2 implements the same 4-tier classification as
// handleGetEntryPointsTool but accepts a format parameter (json|text|mermaid).
// Code is intentionally similar to the legacy handler — Phase 4 retires the
// old one and the duplication goes away.
func (s *CodeGraphMCPServer) handleEntryPointsToolV2(ctx context.Context, args map[string]interface{}) ToolCallResponse {
	limit := 50
	if l, ok := args["limit"].(float64); ok && l > 0 {
		limit = int(l)
	}
	format := "json"
	if f, ok := args["format"].(string); ok && f != "" {
		format = f
	}
	scopeCtx := parseScopeContextArg(args)
	serviceNames := s.resolveWorkspaceServices(ctx, scopeCtx.ScopeID, getOptionalStringArg(args, "service_name"))
	tierFilter := 0
	if t, ok := args["tier"].(float64); ok && t >= 1 && t <= 4 {
		tierFilter = int(t)
	}

	type entryOut struct {
		NodeKey     string `json:"node_key"`
		Name        string `json:"name"`
		FilePath    string `json:"file_path,omitempty"`
		Tier        int    `json:"tier"`
		TierLabel   string `json:"tier_label"`
		Source      string `json:"detection_source,omitempty"`
		ServiceName string `json:"service,omitempty"`
	}

	params := map[string]any{"scopeId": scopeCtx.ScopeID, "serviceNames": serviceNames}
	seen := make(map[string]bool)
	entries := []entryOut{}

	addEntry := func(e entryOut) {
		if e.NodeKey == "" || seen[e.NodeKey] {
			return
		}
		if e.FilePath != "" && !s.fileInWorkspace(e.FilePath) {
			return
		}
		seen[e.NodeKey] = true
		entries = append(entries, e)
	}

	runTier := func(tier int, label, cypher string, fillSource func(map[string]interface{}) string) {
		if tierFilter != 0 && tierFilter != tier {
			return
		}
		records, err := s.client.ExecuteQuery(ctx, cypher, params)
		if err != nil {
			return
		}
		for _, r := range records {
			m := r.AsMap()
			addEntry(entryOut{
				NodeKey:     getStringFromRecord(m, "nodeKey"),
				Name:        getStringFromRecord(m, "name"),
				FilePath:    getStringFromRecord(m, "filePath"),
				Tier:        tier,
				TierLabel:   label,
				Source:      fillSource(m),
				ServiceName: getStringFromRecord(m, "serviceName"),
			})
		}
	}

	// Tier 1: API-exposed.
	runTier(1, "API-exposed", fmt.Sprintf(`
		MATCH (fn)-[:EXPOSES_API]->(r:APIRoute)
		WHERE (fn:Function OR fn:Method)
		  AND (fn.scopeId = $scopeId OR fn.scopeId = 'main')
		  AND (r.scopeId = $scopeId OR r.scopeId = 'main')
		  AND coalesce(fn.isTestFunction, false) = false
		  AND (fn.filePath IS NULL OR NOT fn.filePath ENDS WITH '_test.go')
		  %s
		RETURN DISTINCT fn.nodeKey AS nodeKey, fn.name AS name,
		       coalesce(fn.filePath, '') AS filePath,
		       fn.serviceName AS serviceName,
		       coalesce(r.detectionSource, r.protocol) AS source
		ORDER BY fn.name`, serviceFilterClause("fn")),
		func(m map[string]interface{}) string { return getStringFromRecord(m, "source") })

	// Tier 2: Interface implementations with no callers.
	runTier(2, "Interface impl", fmt.Sprintf(`
		MATCH (fn)-[:IMPLEMENTS]->(iface:Interface)
		WHERE (fn:Function OR fn:Method)
		  AND (fn.scopeId = $scopeId OR fn.scopeId = 'main')
		  AND coalesce(fn.isTestFunction, false) = false
		  AND (fn.filePath IS NULL OR NOT fn.filePath ENDS WITH '_test.go')
		  %s
		OPTIONAL MATCH (caller)-[:CALLS]->(fn)
		WHERE caller:Function OR caller:Method
		WITH fn, iface, count(caller) AS callerCount
		WHERE callerCount = 0
		RETURN DISTINCT fn.nodeKey AS nodeKey, fn.name AS name,
		       coalesce(fn.filePath, '') AS filePath,
		       fn.serviceName AS serviceName,
		       iface.name AS source
		ORDER BY fn.name`, serviceFilterClause("fn")),
		func(m map[string]interface{}) string { return "implements " + getStringFromRecord(m, "source") })

	// Tier 3: Exported topological roots (no callers, has callees).
	runTier(3, "Topological root", fmt.Sprintf(`
		MATCH (fn) WHERE (fn:Function OR fn:Method)
		  AND (fn.scopeId = $scopeId OR fn.scopeId = 'main')
		  AND coalesce(fn.isExported, true) = true
		  AND coalesce(fn.isTestFunction, false) = false
		  AND (fn.filePath IS NULL OR NOT fn.filePath ENDS WITH '_test.go')
		  %s
		OPTIONAL MATCH (caller)-[:CALLS]->(fn)
		WITH fn, count(caller) AS callerCount
		WHERE callerCount = 0
		MATCH (fn)-[:CALLS]->(callee)
		WITH fn, count(DISTINCT callee) AS calleeCount
		WHERE calleeCount > 0
		RETURN DISTINCT fn.nodeKey AS nodeKey, fn.name AS name,
		       coalesce(fn.filePath, '') AS filePath,
		       fn.serviceName AS serviceName,
		       toString(calleeCount) AS source
		ORDER BY calleeCount DESC, fn.name
		LIMIT 50`, serviceFilterClause("fn")),
		func(m map[string]interface{}) string { return getStringFromRecord(m, "source") + " callees" })

	// Tier 4: High centrality (top callees count).
	runTier(4, "High centrality", fmt.Sprintf(`
		MATCH (fn) WHERE (fn:Function OR fn:Method)
		  AND (fn.scopeId = $scopeId OR fn.scopeId = 'main')
		  AND coalesce(fn.isTestFunction, false) = false
		  AND (fn.filePath IS NULL OR NOT fn.filePath ENDS WITH '_test.go')
		  %s
		MATCH (fn)-[:CALLS]->(callee)
		WITH fn, count(DISTINCT callee) AS calleeCount
		WHERE calleeCount >= 5
		RETURN fn.nodeKey AS nodeKey, fn.name AS name,
		       coalesce(fn.filePath, '') AS filePath,
		       fn.serviceName AS serviceName,
		       toString(calleeCount) AS source
		ORDER BY calleeCount DESC LIMIT 30`, serviceFilterClause("fn")),
		func(m map[string]interface{}) string { return getStringFromRecord(m, "source") + " callees" })

	if len(entries) > limit {
		entries = entries[:limit]
	}

	switch format {
	case "text":
		var b strings.Builder
		fmt.Fprintf(&b, "%d entry point(s):\n\n", len(entries))
		byTier := map[int][]entryOut{}
		for _, e := range entries {
			byTier[e.Tier] = append(byTier[e.Tier], e)
		}
		for t := 1; t <= 4; t++ {
			es := byTier[t]
			if len(es) == 0 {
				continue
			}
			fmt.Fprintf(&b, "Tier %d — %s (%d):\n", t, es[0].TierLabel, len(es))
			for _, e := range es {
				fmt.Fprintf(&b, "  - %s", e.Name)
				if e.FilePath != "" {
					fmt.Fprintf(&b, " (%s)", e.FilePath)
				}
				if e.Source != "" {
					fmt.Fprintf(&b, " [%s]", e.Source)
				}
				b.WriteString("\n")
			}
			b.WriteString("\n")
		}
		return ToolCallResponse{Content: []ToolContent{{Type: "text", Text: b.String()}}}
	case "mermaid":
		// Entry points are a list, not a graph. Render as a tier-grouped
		// flowchart with each entry as a leaf under its tier subgraph.
		var b strings.Builder
		b.WriteString("```mermaid\ngraph TD\n")
		byTier := map[int][]entryOut{}
		for _, e := range entries {
			byTier[e.Tier] = append(byTier[e.Tier], e)
		}
		for t := 1; t <= 4; t++ {
			es := byTier[t]
			if len(es) == 0 {
				continue
			}
			fmt.Fprintf(&b, "  subgraph T%d[\"Tier %d: %s\"]\n", t, t, es[0].TierLabel)
			for i, e := range es {
				fmt.Fprintf(&b, "    T%dE%d[\"%s\"]\n", t, i, escapeMermaidLabel(e.Name))
			}
			b.WriteString("  end\n")
		}
		b.WriteString("```\n")
		return ToolCallResponse{Content: []ToolContent{{Type: "text", Text: b.String()}}}
	default:
		body, err := json.MarshalIndent(map[string]interface{}{
			"count":   len(entries),
			"entries": entries,
		}, "", "  ")
		if err != nil {
			return errorResponse(fmt.Sprintf("entry_points: encode failed: %v", err))
		}
		return ToolCallResponse{Content: []ToolContent{{Type: "text", Text: string(body)}}}
	}
}

// writeKeywordRegex matches Cypher write/DDL keywords as whole words. Defense
// in depth — ExecuteRead also rejects writes at the driver level. Keyword
// matches inside string literals will produce false positives; users hitting
// those should rephrase. Comments are stripped before matching.
var writeKeywordRegex = regexp.MustCompile(`(?i)\b(CREATE|MERGE|DELETE|SET|REMOVE|DROP|FOREACH|LOAD\s+CSV|CALL\s+\{[^}]*\b(?:CREATE|MERGE|DELETE|SET|REMOVE|DROP)\b)`)

// stripCypherComments removes // line and /* block */ comments from a query
// to reduce false negatives in keyword detection.
func stripCypherComments(q string) string {
	// Remove block comments first.
	for {
		i := strings.Index(q, "/*")
		if i < 0 {
			break
		}
		j := strings.Index(q[i:], "*/")
		if j < 0 {
			q = q[:i]
			break
		}
		q = q[:i] + q[i+j+2:]
	}
	// Remove line comments.
	var b strings.Builder
	for _, line := range strings.Split(q, "\n") {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// handleCypherTool runs a read-only Cypher query directly. Defense in depth:
// (1) regex keyword pre-check after stripping comments, (2) read-only Neo4j
// transaction (driver-level enforcement), (3) bounded timeout via context,
// (4) row cap enforced during result iteration.
func (s *CodeGraphMCPServer) handleCypherTool(parentCtx context.Context, args map[string]interface{}) ToolCallResponse {
	query, _ := args["query"].(string)
	query = strings.TrimSpace(query)
	if query == "" {
		return errorResponse("cypher: query is required")
	}

	// Layer 1: keyword check on de-commented query.
	stripped := stripCypherComments(query)
	if writeKeywordRegex.MatchString(stripped) {
		return errorResponse("cypher: write keywords (CREATE/MERGE/DELETE/SET/REMOVE/DROP/FOREACH/LOAD CSV) are not allowed in this tool. Use the indexer for graph mutations.")
	}

	timeoutMs := 3000
	if t, ok := args["timeout_ms"].(float64); ok {
		timeoutMs = int(t)
	}
	if timeoutMs < 100 {
		timeoutMs = 100
	}
	if timeoutMs > 5000 {
		timeoutMs = 5000
	}

	rowLimit := 100
	if r, ok := args["row_limit"].(float64); ok {
		rowLimit = int(r)
	}
	if rowLimit < 1 {
		rowLimit = 100
	}
	if rowLimit > 1000 {
		rowLimit = 1000
	}

	params := map[string]any{}
	if rawParams, ok := args["params"].(map[string]interface{}); ok {
		for k, v := range rawParams {
			params[k] = v
		}
	}

	ctx, cancel := context.WithTimeout(parentCtx, time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	type cypherResult struct {
		rows      []map[string]interface{}
		keys      []string
		truncated bool
	}

	result, err := s.client.ExecuteRead(ctx, func(tx neo4jdriver.ManagedTransaction) (any, error) {
		// Layer 2: driver-level read-only enforcement (any write here would
		// fail with a transaction-mode error).
		res, err := tx.Run(ctx, query, params)
		if err != nil {
			return nil, err
		}
		out := &cypherResult{}
		keys, kerr := res.Keys()
		if kerr == nil {
			out.keys = keys
		}
		// Layer 4: row cap during iteration.
		for res.Next(ctx) {
			if len(out.rows) >= rowLimit {
				out.truncated = true
				break
			}
			rec := res.Record()
			row := make(map[string]interface{}, len(out.keys))
			for _, k := range out.keys {
				v, _ := rec.Get(k)
				row[k] = sanitizeCypherValue(v)
			}
			out.rows = append(out.rows, row)
		}
		// If we broke out of iteration due to cap, drain res.Next so the
		// driver doesn't complain. Else surface streaming errors.
		if out.truncated {
			for res.Next(ctx) {
			}
		}
		if rerr := res.Err(); rerr != nil {
			return out, rerr
		}
		return out, nil
	})

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return errorResponse(fmt.Sprintf("cypher: query timed out after %dms", timeoutMs))
		}
		return errorResponse(fmt.Sprintf("cypher: %v", err))
	}

	cr, _ := result.(*cypherResult)
	if cr == nil {
		return errorResponse("cypher: internal error (nil result)")
	}

	out := map[string]interface{}{
		"columns":   cr.keys,
		"rows":      cr.rows,
		"row_count": len(cr.rows),
		"truncated": cr.truncated,
	}
	body, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return errorResponse(fmt.Sprintf("cypher: encode failed: %v", err))
	}
	return ToolCallResponse{Content: []ToolContent{{Type: "text", Text: string(body)}}}
}

// sanitizeCypherValue converts Neo4j driver types into JSON-friendly values
// without losing useful identifying information (elementIds, labels, props).
func sanitizeCypherValue(v interface{}) interface{} {
	switch x := v.(type) {
	case dbtype.Node:
		return map[string]interface{}{
			"_type":   "node",
			"_id":     x.ElementId,
			"_labels": x.Labels,
			"props":   x.Props,
		}
	case dbtype.Relationship:
		return map[string]interface{}{
			"_type":  "relationship",
			"_id":    x.ElementId,
			"_rtype": x.Type,
			"_start": x.StartElementId,
			"_end":   x.EndElementId,
			"props":  x.Props,
		}
	case []interface{}:
		out := make([]interface{}, len(x))
		for i, item := range x {
			out[i] = sanitizeCypherValue(item)
		}
		return out
	case map[string]interface{}:
		out := make(map[string]interface{}, len(x))
		for k, item := range x {
			out[k] = sanitizeCypherValue(item)
		}
		return out
	default:
		return v
	}
}

type pathNode struct {
	NodeID string `json:"node_id"`
	Label  string `json:"label"`
	Name   string `json:"name,omitempty"`
}

type pathEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Type string `json:"type"`
}

type pathResult struct {
	Hops  int        `json:"hops"`
	Nodes []pathNode `json:"nodes"`
	Edges []pathEdge `json:"edges"`
}

// handlePathTool finds paths between two nodes filtered by relationship types
// and direction. Defaults to all shortest paths; pass shortest=false to get up
// to 25 paths up to max_hops.
func (s *CodeGraphMCPServer) handlePathTool(ctx context.Context, args map[string]interface{}) ToolCallResponse {
	fromID, _ := args["from_id"].(string)
	toID, _ := args["to_id"].(string)
	if fromID == "" || toID == "" {
		return errorResponse("path: from_id and to_id are required")
	}

	relTypesRaw, _ := args["rel_types"].([]interface{})
	relTypes := []string{}
	for _, rt := range relTypesRaw {
		if str, ok := rt.(string); ok && str != "" {
			relTypes = append(relTypes, str)
		}
	}
	if len(relTypes) == 0 {
		return errorResponse("path: rel_types must contain at least one type")
	}

	maxHops := 6
	if h, ok := args["max_hops"].(float64); ok {
		maxHops = int(h)
	}
	if maxHops < 1 {
		maxHops = 1
	}
	if maxHops > 20 {
		maxHops = 20
	}

	shortest := true
	if v, ok := args["shortest"].(bool); ok {
		shortest = v
	}

	direction := "out"
	if d, ok := args["direction"].(string); ok && d != "" {
		direction = d
	}
	if direction != "in" && direction != "out" && direction != "both" {
		return errorResponse("path: direction must be 'in', 'out', or 'both'")
	}

	relTypeFilter := ":`" + strings.Join(relTypes, "`|`") + "`"
	var leftArrow, rightArrow string
	switch direction {
	case "out":
		leftArrow, rightArrow = "-", "->"
	case "in":
		leftArrow, rightArrow = "<-", "-"
	case "both":
		leftArrow, rightArrow = "-", "-"
	}

	pathExpr := fmt.Sprintf("(a)%s[%s*1..%d]%s(b)", leftArrow, relTypeFilter, maxHops, rightArrow)
	var matchClause string
	if shortest {
		matchClause = fmt.Sprintf("MATCH p = allShortestPaths(%s)", pathExpr)
	} else {
		matchClause = fmt.Sprintf("MATCH p = %s", pathExpr)
	}

	cypher := fmt.Sprintf(`
MATCH (a) WHERE elementId(a) = $fromId
MATCH (b) WHERE elementId(b) = $toId
%s
RETURN [n IN nodes(p) | {node_id: elementId(n), label: labels(n)[0], name: coalesce(n.name, n.path, '')}] AS pathNodes,
       [r IN relationships(p) | {from: elementId(startNode(r)), to: elementId(endNode(r)), type: type(r)}] AS pathEdges,
       length(p) AS hops
ORDER BY hops
LIMIT 25
`, matchClause)

	records, err := s.client.ExecuteQuery(ctx, cypher, map[string]any{
		"fromId": fromID,
		"toId":   toID,
	})
	if err != nil {
		return errorResponse(fmt.Sprintf("path: query failed: %v", err))
	}

	paths := []pathResult{}
	for _, rec := range records {
		m := rec.AsMap()
		hops := getIntFromRecord(m, "hops")
		nodesRaw, _ := m["pathNodes"].([]interface{})
		edgesRaw, _ := m["pathEdges"].([]interface{})

		pn := make([]pathNode, 0, len(nodesRaw))
		for _, x := range nodesRaw {
			if mm, ok := x.(map[string]interface{}); ok {
				pn = append(pn, pathNode{
					NodeID: getStringFromRecord(mm, "node_id"),
					Label:  getStringFromRecord(mm, "label"),
					Name:   getStringFromRecord(mm, "name"),
				})
			}
		}
		pe := make([]pathEdge, 0, len(edgesRaw))
		for _, x := range edgesRaw {
			if mm, ok := x.(map[string]interface{}); ok {
				pe = append(pe, pathEdge{
					From: getStringFromRecord(mm, "from"),
					To:   getStringFromRecord(mm, "to"),
					Type: getStringFromRecord(mm, "type"),
				})
			}
		}
		paths = append(paths, pathResult{Hops: hops, Nodes: pn, Edges: pe})
	}

	if len(paths) == 0 {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("No path found from %s to %s within %d hops via %v (direction=%s)", fromID, toID, maxHops, relTypes, direction)}},
		}
	}

	format := "json"
	if f, ok := args["format"].(string); ok && f != "" {
		format = f
	}

	switch format {
	case "mermaid":
		return ToolCallResponse{Content: []ToolContent{{Type: "text", Text: renderPathMermaid(paths)}}}
	case "text":
		return ToolCallResponse{Content: []ToolContent{{Type: "text", Text: renderPathText(paths)}}}
	default:
		out := map[string]interface{}{
			"paths":      paths,
			"path_count": len(paths),
		}
		body, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			return errorResponse(fmt.Sprintf("path: encode failed: %v", err))
		}
		return ToolCallResponse{Content: []ToolContent{{Type: "text", Text: string(body)}}}
	}
}

func renderPathMermaid(paths []pathResult) string {
	var b strings.Builder
	b.WriteString("```mermaid\ngraph LR\n")
	idMap := map[string]string{}
	next := 0
	emitNode := func(n pathNode) string {
		if id, ok := idMap[n.NodeID]; ok {
			return id
		}
		id := fmt.Sprintf("N%d", next)
		next++
		idMap[n.NodeID] = id
		label := n.Name
		if label == "" {
			label = n.Label
		}
		label = strings.ReplaceAll(label, "\"", "'")
		fmt.Fprintf(&b, "  %s[\"%s: %s\"]\n", id, n.Label, label)
		return id
	}
	for _, p := range paths {
		for _, n := range p.Nodes {
			emitNode(n)
		}
		for _, e := range p.Edges {
			from, ok1 := idMap[e.From]
			to, ok2 := idMap[e.To]
			if !ok1 || !ok2 {
				continue
			}
			fmt.Fprintf(&b, "  %s -->|%s| %s\n", from, e.Type, to)
		}
	}
	b.WriteString("```\n")
	return b.String()
}

func renderPathText(paths []pathResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Found %d path(s):\n\n", len(paths))
	for i, p := range paths {
		fmt.Fprintf(&b, "Path %d (%d hops):\n", i+1, p.Hops)
		for j, n := range p.Nodes {
			prefix := "  "
			if j > 0 {
				prefix = "    → "
			}
			fmt.Fprintf(&b, "%s%s %s\n", prefix, n.Label, n.Name)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// readSourceFile resolves the on-disk location of a source file given the
// graph's filePath (which is relative to its service's package root) and the
// owning service. Tries: absolute path → workspaceRoot/filePath →
// workspaceRoot/<service-without-org-prefix>/filePath.
func readSourceFile(filePath, service, workspaceRoot string) ([]byte, error) {
	if filePath == "" {
		return nil, fmt.Errorf("empty filePath")
	}
	if filepath.IsAbs(filePath) {
		return os.ReadFile(filePath)
	}
	candidates := []string{}
	if workspaceRoot != "" {
		candidates = append(candidates, filepath.Join(workspaceRoot, filePath))
		if service != "" {
			// Strip a leading org/project segment from service to derive the
			// package directory. Examples: "codegraph/libs/foo" → "libs/foo".
			parts := strings.SplitN(service, "/", 2)
			if len(parts) == 2 {
				candidates = append(candidates, filepath.Join(workspaceRoot, parts[1], filePath))
			}
		}
	}
	candidates = append(candidates, filePath)
	var lastErr error
	for _, c := range candidates {
		data, err := os.ReadFile(c)
		if err == nil {
			return data, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// handleSourceToolV2 is the RFC-004 source primitive. Accepts either node_id
// (preferred — unambiguous) or symbol_name (looked up by name, errors on
// multiple matches). Returns a markdown code block with file location.
func (s *CodeGraphMCPServer) handleSourceToolV2(ctx context.Context, args map[string]interface{}) ToolCallResponse {
	nodeID, _ := args["node_id"].(string)
	symbolName, _ := args["symbol_name"].(string)
	if nodeID == "" && symbolName == "" {
		return errorResponse("source: must provide node_id or symbol_name")
	}

	var cypher string
	params := map[string]any{}
	if nodeID != "" {
		cypher = `MATCH (f) WHERE elementId(f) = $id AND (f:Function OR f:Method)
		          RETURN f.name AS name, f.filePath AS filePath,
		                 f.startLine AS startLine, f.endLine AS endLine,
		                 f.signature AS signature, f.serviceName AS service
		          LIMIT 1`
		params["id"] = nodeID
	} else {
		cypher = `MATCH (f) WHERE (f:Function OR f:Method) AND f.name = $name
		          RETURN f.name AS name, f.filePath AS filePath,
		                 f.startLine AS startLine, f.endLine AS endLine,
		                 f.signature AS signature, f.serviceName AS service
		          ORDER BY f.filePath
		          LIMIT 5`
		params["name"] = symbolName
	}

	records, err := s.client.ExecuteQuery(ctx, cypher, params)
	if err != nil {
		return errorResponse(fmt.Sprintf("source: lookup failed: %v", err))
	}
	if len(records) == 0 {
		ident := nodeID
		if ident == "" {
			ident = symbolName
		}
		return errorResponse(fmt.Sprintf("source: no Function/Method found for %q", ident))
	}
	if nodeID == "" && len(records) > 1 {
		var b strings.Builder
		fmt.Fprintf(&b, "source: %q is ambiguous (%d matches). Pass node_id from one of:\n", symbolName, len(records))
		for _, rec := range records {
			m := rec.AsMap()
			fmt.Fprintf(&b, "  - %s (%s:%d) service=%s\n",
				getStringFromRecord(m, "name"),
				getStringFromRecord(m, "filePath"),
				getIntFromRecord(m, "startLine"),
				getStringFromRecord(m, "service"))
		}
		return errorResponse(b.String())
	}

	m := records[0].AsMap()
	name := getStringFromRecord(m, "name")
	filePath := getStringFromRecord(m, "filePath")
	startLine := getIntFromRecord(m, "startLine")
	endLine := getIntFromRecord(m, "endLine")
	signature := getStringFromRecord(m, "signature")
	service := getStringFromRecord(m, "service")

	if filePath == "" {
		return errorResponse(fmt.Sprintf("source: %s has no filePath in graph", name))
	}

	data, readErr := readSourceFile(filePath, service, s.workspaceRoot)
	if readErr != nil {
		return errorResponse(fmt.Sprintf("source: failed to read %s: %v", filePath, readErr))
	}

	lines := strings.Split(string(data), "\n")
	if startLine < 1 {
		startLine = 1
	}
	if endLine < startLine || endLine > len(lines) {
		endLine = len(lines)
	}
	src := strings.Join(lines[startLine-1:endLine], "\n")

	lang := "text"
	switch strings.ToLower(filepath.Ext(filePath)) {
	case ".go":
		lang = "go"
	case ".ts", ".tsx":
		lang = "typescript"
	case ".js", ".jsx":
		lang = "javascript"
	case ".py":
		lang = "python"
	case ".java":
		lang = "java"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "**%s**", name)
	if signature != "" {
		fmt.Fprintf(&b, "  \n`%s`", signature)
	}
	if service != "" {
		fmt.Fprintf(&b, "  \nservice: `%s`", service)
	}
	fmt.Fprintf(&b, "  \n%s:%d-%d\n\n```%s\n%s\n```\n",
		filePath, startLine, endLine, lang, src)

	return ToolCallResponse{Content: []ToolContent{{Type: "text", Text: b.String()}}}
}

