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
	"sync"

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

	// writeMu serializes writes to stdout. Required because requests are now
	// dispatched concurrently (one goroutine per request); without it, two
	// handlers could interleave bytes of their JSON-RPC responses on the pipe.
	writeMu sync.Mutex
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

	// workspaceRoot decides which stored file paths count as "in workspace". It must be the
	// parent directory that holds all indexed service repos (e.g. ~/Workspace/Tazapay), which
	// is NOT necessarily the process cwd — Claude Code spawns MCP servers with the active
	// project dir as cwd. Prefer the explicit env override; fall back to cwd only if unset.
	workspaceRoot := getEnvOrDefault("CODEGRAPH_WORKSPACE_ROOT", "")
	if workspaceRoot == "" {
		var err error
		workspaceRoot, err = os.Getwd()
		if err != nil {
			workspaceRoot = "."
		}
	}
	log.Printf("workspace root: %s", workspaceRoot)

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
	// Lift the default 64KB line cap (bufio.MaxScanTokenSize). A single
	// JSON-RPC request larger than 64KB would otherwise abort Scan(), exit the
	// loop, and shut the server down — surfacing to the client as a disconnect.
	scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024)

	var wg sync.WaitGroup
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		// Dispatch each request in its own goroutine so a slow handler (e.g. a
		// multi-second Neo4j query) does not head-of-line-block subsequent
		// requests. The client issues tool calls in parallel bursts; serializing
		// them here made the queue exceed the client's per-request timeout and
		// caused it to tear down the transport.
		wg.Add(1)
		go func(l string) {
			defer wg.Done()
			s.dispatch(l)
		}(line)
	}

	if err := scanner.Err(); err != nil {
		log.Printf("Error reading input: %v", err)
	}
	wg.Wait()
}

// dispatch parses and handles a single request line, recovering from any panic
// so one bad request cannot crash the whole server (which would kill the pipe
// and disconnect the client from every in-flight request).
func (s *CodeGraphMCPServer) dispatch(line string) {
	var request MCPRequest
	if err := json.Unmarshal([]byte(line), &request); err != nil {
		s.sendError(request.ID, -32700, "Parse error")
		return
	}

	defer func() {
		if r := recover(); r != nil {
			log.Printf("[panic] handling request id=%v method=%s: %v", request.ID, request.Method, r)
			s.sendError(request.ID, -32603, fmt.Sprintf("Internal error: %v", r))
		}
	}()

	s.handleRequest(request)
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
			Description: "Retrieve the exact source code for a specific function or method. If the name exists in multiple services/files, candidates are listed — re-call with the service parameter.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"function_name": map[string]interface{}{
						"type":        "string",
						"description": "Name of the function or method to retrieve source code for",
					},
					"service": map[string]interface{}{
						"type":        "string",
						"description": "Service name to disambiguate when the same function name exists in multiple services",
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
			Name:        "codegraph_cross_service_flow",
			Description: "Trace multi-hop cross-service call flows starting from a service (or function). Performs BFS across CALLS_API→CALLS_SERVICE edges to show how a request propagates through the system.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"start_service": map[string]interface{}{
						"type":        "string",
						"description": "Name of the service to start tracing from",
					},
					"start_function": map[string]interface{}{
						"type":        "string",
						"description": "Optional function name within start_service to narrow the starting point",
					},
					"max_hops": map[string]interface{}{
						"type":        "number",
						"description": "Maximum number of service boundary crossings to trace (1–5, default: 3)",
						"default":     3,
					},
				},
				"required": []string{"start_service"},
			},
		},
		{
			Name:        "codegraph_expand_step",
			Description: "Expand a single step from a codegraph_generate_flows execution trace to show its full details: file path, line range, signature, docstring, and scope annotations (condition, transaction, parallelism). Use this after codegraph_generate_flows to drill into any step shown in the execution trace.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"flow_node_key": map[string]interface{}{
						"type":        "string",
						"description": "The flow_node_key shown in the codegraph_generate_flows output tip",
					},
					"step_name": map[string]interface{}{
						"type":        "string",
						"description": "Function or method name as shown in the execution trace (case-insensitive)",
					},
					"scope_id": map[string]interface{}{
						"type":        "string",
						"description": "Scope ID to query (default: main)",
						"default":     "main",
					},
				},
				"required": []string{"flow_node_key", "step_name"},
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
			Name:        "codegraph_rpc_dependencies",
			Description: "Get all dependencies for a single RPC handler: DB tables touched, downstream gRPC/HTTP services called, and async events published. Requires DBCall nodes (indexed via SCIP pipeline).",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"service": map[string]interface{}{
						"type":        "string",
						"description": "Service name containing the RPC handler",
					},
					"rpc": map[string]interface{}{
						"type":        "string",
						"description": "Handler function name or exposed endpoint path (e.g. 'CreatePayment' or '/v1/payments')",
					},
					"scope": map[string]interface{}{
						"type":        "string",
						"description": "Scope ID to query (default: main)",
						"default":     "main",
					},
				},
				"required": []string{"service", "rpc"},
			},
		},
		{
			Name:        "codegraph_service_dependency_map",
			Description: "Get the full dependency manifest for every exposed RPC in a service: which DB tables each handler touches and which downstream services it depends on. Useful for architecture review and change-impact analysis.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"service": map[string]interface{}{
						"type":        "string",
						"description": "Service name to map dependencies for",
					},
					"scope": map[string]interface{}{
						"type":        "string",
						"description": "Scope ID to query (default: main)",
						"default":     "main",
					},
				},
				"required": []string{"service"},
			},
		},
		{
			Name:        "codegraph_rpc_anatomy",
			Description: "Return the structured anatomy of an RPC handler: all steps in source order with kind (validation/rpc/db/outbox), control-flow context, transaction membership, and parallel group. Requires enriched indexing with call metadata.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"rpc_name": map[string]interface{}{
						"type":        "string",
						"description": "Handler function name (e.g. 'CreatePayout') or fully-qualified name (e.g. 'PayoutServiceServer.CreatePayout')",
					},
					"service": map[string]interface{}{
						"type":        "string",
						"description": "Service name to narrow the search when multiple services are indexed",
					},
					"scope_id": map[string]interface{}{
						"type":        "string",
						"description": "Scope ID to query (default: main)",
						"default":     "main",
					},
				},
				"required": []string{"rpc_name"},
			},
		},
		{
			Name:        "codegraph_event_flow",
			Description: "Trace the full lifecycle of an async event through the system: who emits it, which queue/service receives it, which handler processes it, and where it fans out downstream. Works with Tazapay's SQS/AsyncMessage pattern. Accepts an event name like 'settlement.failed' or just a group like 'settlement'.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"event_name": map[string]interface{}{
						"type":        "string",
						"description": "Event name to trace, e.g. 'settlement.failed', 'payout.created', or just 'settlement' to show all actions in that group",
					},
					"scope_id": map[string]interface{}{
						"type":        "string",
						"description": "Scope ID to query (default: main)",
						"default":     "main",
					},
				},
				"required": []string{"event_name"},
			},
		},
		{
			Name:        "codegraph_api_callers",
			Description: "Answer 'who calls this API?' across service boundaries in one call. Given an RPC handler (name, proto method, or HTTP route), returns every inbound call site in other services (gRPC/HTTP, with file:line and resolution confidence), folds each caller back to the calling service's own API endpoint, and lists async triggers (events routed to this handler plus their producers). Use this instead of codegraph_trace_call_graph upstream when the question crosses services.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"rpc": map[string]interface{}{
						"type":        "string",
						"description": "Handler function name (e.g. 'FundPayout'), proto method name, or HTTP route substring (starts with '/')",
					},
					"service": map[string]interface{}{
						"type":        "string",
						"description": "Service that OWNS the handler — required when the name exists in multiple services",
					},
					"max_depth": map[string]interface{}{
						"type":        "number",
						"description": "How far to walk up the calling service's internal call chain to find its entry API (default: 5)",
						"default":     5,
					},
					"scope_id": map[string]interface{}{
						"type":        "string",
						"description": "Scope ID to query (default: main)",
						"default":     "main",
					},
				},
				"required": []string{"rpc"},
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
	case "codegraph_service_api_calls":
		response = s.handleServiceAPICallsTool(ctx, toolCall.Arguments)
	case "codegraph_cross_service_calls":
		response = s.handleCrossServiceCallsTool(ctx, toolCall.Arguments)
	case "codegraph_cross_service_flow":
		response = s.handleCrossServiceFlowTool(ctx, toolCall.Arguments)
	case "codegraph_service_architecture":
		response = s.handleServiceArchitectureTool(ctx, toolCall.Arguments)
	case "codegraph_get_entry_points":
		response = s.handleGetEntryPointsTool(ctx, toolCall.Arguments)
	case "codegraph_generate_flows":
		response = s.handleGenerateFlowsTool(ctx, toolCall.Arguments)
	case "codegraph_expand_step":
		response = s.handleExpandStepTool(ctx, toolCall.Arguments)
	case "codegraph_trace_call_graph":
		response = s.handleTraceCallGraphTool(ctx, toolCall.Arguments)
	case "codegraph_rpc_dependencies":
		response = s.handleRPCDependenciesTool(ctx, toolCall.Arguments)
	case "codegraph_service_dependency_map":
		response = s.handleServiceDependencyMapTool(ctx, toolCall.Arguments)
	case "codegraph_rpc_anatomy":
		response = s.handleRPCAnatomyTool(ctx, toolCall.Arguments)
	case "codegraph_event_flow":
		response = s.handleEventFlowTool(ctx, toolCall.Arguments)
	case "codegraph_api_callers":
		response = s.handleAPICallersTool(ctx, toolCall.Arguments)
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
				if summary := getStringProp(props, "summary"); summary != "" {
					output.WriteString(fmt.Sprintf("  Does: %s\n", summary))
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

	// Get all candidate matches so a cross-service name collision never silently
	// returns the wrong function's source.
	service := getOptionalStringArg(args, "service")
	serviceFilter := ""
	if service != "" {
		serviceFilter = `AND EXISTS { MATCH (svc:Service)-[:CONTAINS*1..5]->(f) WHERE toLower(svc.name) = toLower($service) }`
	}
	cypher := fmt.Sprintf(`
		MATCH (f)
		WHERE (f:Function OR f:Method)
		  AND f.name = $functionName
		%s
		OPTIONAL MATCH (svc:Service)-[:CONTAINS*1..5]->(f)
		RETURN DISTINCT f.filePath AS filePath, coalesce(f.startLine,0) AS startLine,
		       coalesce(f.endLine,0) AS endLine, coalesce(svc.name,'') AS serviceName,
		       coalesce(f.summary,'') AS summary
		LIMIT 10
	`, serviceFilter)

	result, err := s.client.ExecuteQuery(ctx, cypher, map[string]any{"functionName": functionName, "service": service})
	if err != nil || len(result) == 0 {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error finding function '%s': %v", functionName, err)}},
			IsError: true,
		}
	}

	// Distinct file paths → ambiguous match; list candidates instead of guessing.
	distinctFiles := map[string]bool{}
	for _, r := range result {
		distinctFiles[getStringFromRecord(r.AsMap(), "filePath")] = true
	}
	if len(distinctFiles) > 1 {
		var out strings.Builder
		out.WriteString(fmt.Sprintf("'%s' matches %d locations — re-call with the `service` parameter:\n\n", functionName, len(distinctFiles)))
		for _, r := range result {
			m := r.AsMap()
			out.WriteString(fmt.Sprintf("- **%s** %s:%d-%d", getStringFromRecord(m, "serviceName"),
				getStringFromRecord(m, "filePath"), getIntFromRecord(m, "startLine"), getIntFromRecord(m, "endLine")))
			if summary := getStringFromRecord(m, "summary"); summary != "" {
				out.WriteString(" — " + summary)
			}
			out.WriteString("\n")
		}
		return ToolCallResponse{Content: []ToolContent{{Type: "text", Text: out.String()}}}
	}

	// Detect language from file path
	filePath := getStringFromRecord(result[0].AsMap(), "filePath")
	language := detectLanguageFromPath(filePath)

	sourceCode, err := s.getSourceByLocation(result[0].AsMap(), functionName)
	if err != nil {
		sourceCode, err = s.queryBuilder.GetFunctionSourceCode(ctx, functionName)
	}
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
		MATCH (f)
		WHERE (f:Function OR f:Method) AND f.name = $name
		RETURN f.name as name, f.signature as signature, f.filePath as filePath,
			   f.startLine as startLine, f.endLine as endLine, f.linesOfCode as linesOfCode,
			   f.returnType as returnType, f.isExported as isExported,
			   f.complexity as complexity, f.docstring as docstring,
			   coalesce(f.summary, '') as summary
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
	if summary := getStringFromRecord(record, "summary"); summary != "" {
		output.WriteString(fmt.Sprintf("- **Does**: %s\n", summary))
	}
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
		MATCH (caller)-[:CALLS]->(f)
		WHERE (f:Function OR f:Method) AND f.name = $name
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
		MATCH (f)-[:CALLS]->(callee)
		WHERE (f:Function OR f:Method) AND f.name = $name
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
	s.writeMu.Lock()
	fmt.Println(string(jsonBytes))
	s.writeMu.Unlock()
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
	s.writeMu.Lock()
	fmt.Println(string(jsonBytes))
	s.writeMu.Unlock()
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

// handleServiceDependenciesTool gets dependencies of a service (6d: augmented with runtime CALLS_SERVICE evidence)
func (s *CodeGraphMCPServer) handleServiceDependenciesTool(ctx context.Context, args map[string]interface{}) ToolCallResponse {
	serviceName, ok := args["service_name"].(string)
	if !ok {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: "Error: service_name is required"}},
			IsError: true,
		}
	}

	params := map[string]interface{}{"serviceName": serviceName}

	var output strings.Builder
	output.WriteString(fmt.Sprintf("# Dependencies for %s\n\n", serviceName))

	// Static import-based dependencies (DEPENDS_ON)
	staticQuery := `
		MATCH (s:Service {name: $serviceName})-[d:DEPENDS_ON]->(target:Service)
		RETURN target.name AS targetService, target.packageName AS packageName,
		       d.importCount AS importCount, d.packageName AS importedPackage
		ORDER BY importCount DESC
	`
	staticResult, err := s.client.ExecuteQuery(ctx, staticQuery, params)
	if err != nil {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error: %v", err)}},
			IsError: true,
		}
	}

	if len(staticResult) > 0 {
		output.WriteString("## Static Import Dependencies\n\n")
		output.WriteString("| Target Service | Package Name | Import Count | Imported Package |\n")
		output.WriteString("|----------------|--------------|--------------|------------------|\n")
		for _, record := range staticResult {
			m := record.AsMap()
			output.WriteString(fmt.Sprintf("| %s | %s | %d | %s |\n",
				getStringFromRecord(m, "targetService"),
				getStringFromRecord(m, "packageName"),
				getIntFromRecord(m, "importCount"),
				getStringFromRecord(m, "importedPackage")))
		}
		output.WriteString("\n")
	}

	// Runtime call-based dependencies (CALLS_SERVICE via CALLS_API edges)
	runtimeQuery := `
		MATCH (s:Service {name: $serviceName})-[:CONTAINS]->(file:File)-[:CONTAINS]->(fn:Function)-[:CALLS_API]->(call)-[:CALLS_SERVICE]->(target:Service)
		WHERE s <> target
		RETURN target.name AS targetService,
		       count(DISTINCT call) AS callCount,
		       collect(DISTINCT CASE WHEN call:GRPCCall THEN 'gRPC'
		                             WHEN call:HTTPCall THEN 'HTTP'
		                             WHEN call:OutboxCall THEN 'Outbox'
		                             ELSE 'Unknown' END) AS protocols
		ORDER BY callCount DESC
	`
	runtimeResult, err := s.client.ExecuteQuery(ctx, runtimeQuery, params)
	if err == nil && len(runtimeResult) > 0 {
		output.WriteString("## Runtime Call Dependencies (indexed RPC/HTTP/Outbox calls)\n\n")
		output.WriteString("| Target Service | Call Count | Protocols |\n")
		output.WriteString("|----------------|------------|------------|\n")
		for _, record := range runtimeResult {
			m := record.AsMap()
			protocols := []string{}
			if ps, ok := m["protocols"].([]interface{}); ok {
				for _, p := range ps {
					if pStr, ok := p.(string); ok {
						protocols = append(protocols, pStr)
					}
				}
			}
			output.WriteString(fmt.Sprintf("| %s | %d | %s |\n",
				getStringFromRecord(m, "targetService"),
				getIntFromRecord(m, "callCount"),
				strings.Join(protocols, ", ")))
		}
		output.WriteString("\n")
	}

	if len(staticResult) == 0 && (err != nil || len(runtimeResult) == 0) {
		output.WriteString("No dependencies found.\n")
	}

	return ToolCallResponse{
		Content: []ToolContent{{Type: "text", Text: output.String()}},
	}
}

// handleServiceAPICallsTool gets API calls made by a service (6a: fixed node types, 2-hop traversal)
func (s *CodeGraphMCPServer) handleServiceAPICallsTool(ctx context.Context, args map[string]interface{}) ToolCallResponse {
	serviceName, ok := args["service_name"].(string)
	if !ok {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: "Error: service_name is required"}},
			IsError: true,
		}
	}

	limit := 50
	if l, ok := args["limit"].(float64); ok && l > 0 {
		limit = int(l)
	}

	// 2-hop traversal (Service→File→Function) — avoids expensive CONTAINS*
	// Matches GRPCCall, HTTPCall, OutboxCall — SDKCall does not exist in the schema
	query := `
		MATCH (s:Service {name: $serviceName})-[:CONTAINS]->(file:File)-[:CONTAINS]->(fn:Function)-[:CALLS_API]->(call)
		WHERE call:GRPCCall OR call:HTTPCall OR call:OutboxCall
		OPTIONAL MATCH (call)-[:CALLS_SERVICE]->(svcTarget:Service)
		RETURN fn.name AS functionName,
		       CASE WHEN call:GRPCCall THEN 'gRPC'
		            WHEN call:HTTPCall THEN 'HTTP'
		            ELSE 'Outbox' END AS callType,
		       CASE WHEN call:GRPCCall THEN coalesce(call.targetMethod, '')
		            WHEN call:HTTPCall THEN coalesce(call.url, '')
		            ELSE coalesce(call.eventType, '') END AS callTarget,
		       CASE WHEN call:GRPCCall THEN coalesce(call.protoPackage, '')
		            WHEN call:HTTPCall THEN coalesce(call.method, 'GET')
		            ELSE coalesce(call.transport, '') END AS method,
		       coalesce(svcTarget.name, '') AS targetService,
		       coalesce(fn.filePath, '') AS filePath
		ORDER BY callType, callTarget
		LIMIT $limit
	`

	params := map[string]interface{}{
		"serviceName": serviceName,
		"limit":       limit,
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
		output.WriteString("No API calls found. (Run `codegraph index scip` to populate cross-service call nodes.)\n")
	} else {
		output.WriteString("| Type | Target | Method | Target Service | Function | File |\n")
		output.WriteString("|------|--------|--------|----------------|----------|------|\n")
		for _, record := range result {
			m := record.AsMap()
			output.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %s | %s |\n",
				getStringFromRecord(m, "callType"),
				getStringFromRecord(m, "callTarget"),
				getStringFromRecord(m, "method"),
				getStringFromRecord(m, "targetService"),
				getStringFromRecord(m, "functionName"),
				getStringFromRecord(m, "filePath")))
		}
	}

	return ToolCallResponse{
		Content: []ToolContent{{Type: "text", Text: output.String()}},
	}
}

// handleCrossServiceCallsTool gets cross-service call chains (6b: directed CALLS_API→CALLS_SERVICE traversal)
func (s *CodeGraphMCPServer) handleCrossServiceCallsTool(ctx context.Context, args map[string]interface{}) ToolCallResponse {
	// Accept both from_service/to_service (tool schema) and source_service/target_service (legacy)
	fromService := getOptionalStringArg(args, "from_service")
	if fromService == "" {
		fromService = getOptionalStringArg(args, "source_service")
	}
	toService := getOptionalStringArg(args, "to_service")
	if toService == "" {
		toService = getOptionalStringArg(args, "target_service")
	}

	limit := 20
	if l, ok := args["limit"].(float64); ok && l > 0 {
		limit = int(l)
	}

	// Directed traversal: Service→File→Function→CALLS_API→call→CALLS_SERVICE→Service
	// Both from/to are optional filters — omitting shows all cross-service calls
	query := `
		MATCH (callerSvc:Service)-[:CONTAINS]->(file:File)-[:CONTAINS]->(fn:Function)-[:CALLS_API]->(call)-[:CALLS_SERVICE]->(targetSvc:Service)
		WHERE callerSvc <> targetSvc
		  AND ($fromService = '' OR callerSvc.name = $fromService)
		  AND ($toService = '' OR targetSvc.name = $toService)
		RETURN callerSvc.name AS callerService,
		       fn.name AS functionName,
		       coalesce(fn.filePath, '') AS filePath,
		       CASE WHEN call:GRPCCall THEN 'gRPC'
		            WHEN call:HTTPCall THEN 'HTTP'
		            WHEN call:OutboxCall THEN 'Outbox'
		            ELSE 'Unknown' END AS callType,
		       CASE WHEN call:GRPCCall THEN coalesce(call.targetMethod, '')
		            WHEN call:HTTPCall THEN coalesce(call.url, '')
		            ELSE coalesce(call.eventType, '') END AS callTarget,
		       targetSvc.name AS targetService
		ORDER BY callerService, targetService, callType
		LIMIT $limit
	`

	params := map[string]interface{}{
		"fromService": fromService,
		"toService":   toService,
		"limit":       limit,
	}

	result, err := s.client.ExecuteQuery(ctx, query, params)
	if err != nil {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error: %v", err)}},
			IsError: true,
		}
	}

	var output strings.Builder
	title := "# Cross-Service Calls"
	if fromService != "" && toService != "" {
		title = fmt.Sprintf("# Cross-Service Calls: %s → %s", fromService, toService)
	} else if fromService != "" {
		title = fmt.Sprintf("# Cross-Service Calls from %s", fromService)
	} else if toService != "" {
		title = fmt.Sprintf("# Cross-Service Calls to %s", toService)
	}
	output.WriteString(title + "\n\n")

	if len(result) == 0 {
		output.WriteString("No cross-service calls found. (Run `codegraph index scip` to populate CALLS_SERVICE edges.)\n")
	} else {
		output.WriteString("| Caller Service | Function | Type | Call Target | Target Service | File |\n")
		output.WriteString("|----------------|----------|------|-------------|----------------|------|\n")
		for _, record := range result {
			m := record.AsMap()
			output.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %s | %s |\n",
				getStringFromRecord(m, "callerService"),
				getStringFromRecord(m, "functionName"),
				getStringFromRecord(m, "callType"),
				getStringFromRecord(m, "callTarget"),
				getStringFromRecord(m, "targetService"),
				getStringFromRecord(m, "filePath")))
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

		// Get API calls (2-hop traversal; GRPCCall/HTTPCall/OutboxCall)
		OPTIONAL MATCH (s)-[:CONTAINS]->(file2:File)-[:CONTAINS]->(f2:Function)-[:CALLS_API]->(call)
		WHERE call:GRPCCall OR call:HTTPCall OR call:OutboxCall
		WITH s, dependencies, count(DISTINCT call) AS callCount

		RETURN s.name AS name, s.packageName AS packageName,
		       dependencies, callCount
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
			callCount := getIntFromRecord(recordMap, "callCount")

			output.WriteString(fmt.Sprintf("## %s\n\n", name))
			if packageName != "" {
				output.WriteString(fmt.Sprintf("- **Package**: %s\n", packageName))
			}
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

	// Tier 1: Interface implementations with no callers
	if tierFilter == 0 || tierFilter == 1 {
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
					Tier: 1, TierLabel: "Interface impl", DetectionSource: "implements " + getStringFromRecord(m, "ifaceName"),
				})
			}
		}
	}

	// Tier 2: Topological roots (exported, no callers, has callees)
	if tierFilter == 0 || tierFilter == 2 {
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
					Tier: 2, TierLabel: "Topological root", DetectionSource: fmt.Sprintf("exported, %d callees", calleeCount),
				})
			}
		}
	}

	// Tier 3: High centrality (functions with many callers AND callees)
	if tierFilter == 0 || tierFilter == 3 {
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
					Tier: 3, TierLabel: "High centrality", DetectionSource: fmt.Sprintf("%d callers, %d callees", inDeg, outDeg),
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

	summaryGen := query.NewBehavioralSummaryGenerator(s.client)
	summaryGen.SetScope(scopeCtx)

	var output strings.Builder
	output.WriteString(fmt.Sprintf("## Generated Flows (%d)\n\n", len(flows)))
	if len(serviceNames) > 0 {
		output.WriteString(fmt.Sprintf("_Workspace service filter:_ `%s`\n\n", strings.Join(serviceNames, "`, `")))
	}

	for i, flow := range flows {
		output.WriteString(fmt.Sprintf("### %d. %s\n", i+1, flow.FlowName))
		output.WriteString(fmt.Sprintf("- **Type**: %s  **Steps**: %d\n\n", flow.FlowType, len(flow.Steps)))

		summary, err := summaryGen.GetOrComputeSummary(ctx, flow.FlowNodeKey)
		if err == nil && summary != "" {
			output.WriteString("**Execution trace:**\n```\n")
			output.WriteString(summary)
			output.WriteString("\n```\n")
			output.WriteString(fmt.Sprintf("_Use `codegraph_expand_step` with `flow_node_key: %q` and `step_name` for file location and full signature._\n\n", flow.FlowNodeKey))
		} else {
			// Fallback: raw step list when no enrichment data is available.
			output.WriteString("**Steps:**\n")
			for _, step := range flow.Steps {
				output.WriteString(fmt.Sprintf("  %d. `%s` (%s)\n", step.Order+1, step.Name, step.Label))
			}
			output.WriteString("\n")
		}
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
		Summary   string
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
                       coalesce(root.summary, '') AS summary,
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
			Summary:  getStringFromRecord(m, "summary"),
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
	if root.Summary != "" {
		output.WriteString(fmt.Sprintf("_Does_: %s\n\n", root.Summary))
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
			RETURN DISTINCT nodeKey, callee.name AS name, filePath, depth,
			       coalesce(callee.summary, '') AS summary
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
				line := fmt.Sprintf("%s→ `%s`", indent, name)
				if file != "" {
					line += fmt.Sprintf(" (%s)", file)
				}
				if summary := getStringFromRecord(m, "summary"); summary != "" {
					line += " — " + summary
				}
				output.WriteString(line + "\n")
				count++
			}
			if count == 0 {
				output.WriteString("No downstream calls found.\n")
			}
			output.WriteString("\n")
		}
	}

	// Cross-service hops: what services does this function (or any callee) reach via CALLS_API?
	if direction == "downstream" || direction == "both" {
		output.WriteString("### Cross-Service Calls (outbound)\n\n")
		crossCypher := fmt.Sprintf(`
			MATCH (root {nodeKey: $rootKey})
			OPTIONAL MATCH (root)-[:CALLS*0..%d]->(fn)
			WHERE fn:Function OR fn:Method
			WITH collect(DISTINCT fn) + [root] AS reachable
			UNWIND reachable AS caller
			MATCH (caller)-[:CALLS_API]->(call)-[:CALLS_SERVICE]->(targetSvc:Service)
			RETURN caller.name AS callerName,
			       coalesce(caller.filePath, '') AS callerFile,
			       coalesce(call.line, 0) AS callLine,
			       coalesce(call.protoService, '') AS protoService,
			       coalesce(call.protoMethod, '') AS protoMethod,
			       CASE WHEN call:GRPCCall THEN 'gRPC'
			            WHEN call:HTTPCall THEN 'HTTP'
			            WHEN call:OutboxCall THEN 'Outbox'
			            ELSE 'Unknown' END AS callType,
			       CASE WHEN call:GRPCCall THEN coalesce(call.targetMethod, '')
			            WHEN call:HTTPCall THEN coalesce(call.url, '')
			            ELSE coalesce(call.eventType, '') END AS callTarget,
			       targetSvc.name AS targetService
			ORDER BY callerName, callType
			LIMIT 50`, maxDepth)

		crossRecords, crossErr := s.client.ExecuteQuery(ctx, crossCypher, map[string]any{
			"rootKey": root.NodeKey,
		})
		if crossErr != nil {
			output.WriteString(fmt.Sprintf("Error: %v\n\n", crossErr))
		} else if len(crossRecords) == 0 {
			output.WriteString("No cross-service calls found from this call chain.\n\n")
		} else {
			for _, r := range crossRecords {
				m := r.AsMap()
				callerName := getStringFromRecord(m, "callerName")
				callType := getStringFromRecord(m, "callType")
				callTarget := getStringFromRecord(m, "callTarget")
				targetSvc := getStringFromRecord(m, "targetService")
				protoService := getStringFromRecord(m, "protoService")
				protoMethod := getStringFromRecord(m, "protoMethod")
				line := getIntFromRecord(m, "callLine")
				entry := fmt.Sprintf("  `%s` →[%s]→ `%s` @ **%s**", callerName, callType, callTarget, targetSvc)
				if protoService != "" || protoMethod != "" {
					entry += fmt.Sprintf(" (proto: %s.%s)", protoService, protoMethod)
				}
				if line > 0 {
					entry += fmt.Sprintf(" line %d", line)
				}
				output.WriteString(entry + "\n")
			}
			output.WriteString("\n")
		}
	}

	// Upstream: what calls this function?
	if direction == "upstream" || direction == "both" {
		output.WriteString("### Upstream (callers)\n\n_Upstream traversal stays within one service. For cross-service inbound callers of an RPC, use codegraph_api_callers._\n\n")
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
			RETURN DISTINCT nodeKey, caller.name AS name, filePath, depth,
			       coalesce(caller.summary, '') AS summary,
			       coalesce(caller.isRPCHandler, false) AS isHandler
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
				line := fmt.Sprintf("%s← `%s`", indent, name)
				if getBoolFromRecord(m, "isHandler") {
					line += " (API endpoint)"
				}
				if file != "" {
					line += fmt.Sprintf(" (%s)", file)
				}
				if summary := getStringFromRecord(m, "summary"); summary != "" {
					line += " — " + summary
				}
				output.WriteString(line + "\n")
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

// handleCrossServiceFlowTool traces multi-hop cross-service call flows via BFS (Phase 6e)
func (s *CodeGraphMCPServer) handleCrossServiceFlowTool(ctx context.Context, args map[string]interface{}) ToolCallResponse {
	startService, ok := args["start_service"].(string)
	if !ok || startService == "" {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: "Error: start_service is required"}},
			IsError: true,
		}
	}
	startFunction := getOptionalStringArg(args, "start_function")
	maxHops := 3
	if h, ok := args["max_hops"].(float64); ok && h >= 1 && h <= 5 {
		maxHops = int(h)
	}

	type crossCall struct {
		CallerService  string
		FunctionName   string
		CallType       string
		CallTarget     string
		TargetService  string
		HandlerName    string // resolved via RESOLVES_TO (may be empty)
		HandlerFile    string
		ResolutionConf float64
	}

	var output strings.Builder
	output.WriteString(fmt.Sprintf("# Cross-Service Flow from **%s**\n\n", startService))
	if startFunction != "" {
		output.WriteString(fmt.Sprintf("_Starting function filter: `%s`_\n\n", startFunction))
	}
	output.WriteString(fmt.Sprintf("_Max service hops: %d_\n\n", maxHops))

	visited := map[string]bool{startService: true}
	frontier := []string{startService}

	for hop := 1; hop <= maxHops && len(frontier) > 0; hop++ {
		output.WriteString(fmt.Sprintf("## Hop %d\n\n", hop))

		var cypher string
		var params map[string]interface{}

		if hop == 1 && startFunction != "" {
			cypher = `
				MATCH (callerSvc:Service {name: $startService})-[:CONTAINS]->(file:File)-[:CONTAINS]->(fn:Function)-[:CALLS_API]->(call)-[:CALLS_SERVICE]->(targetSvc:Service)
				WHERE callerSvc <> targetSvc
				  AND toLower(fn.name) CONTAINS toLower($startFunction)
				OPTIONAL MATCH (call)-[:RESOLVES_TO]->(handler:Function)
				RETURN callerSvc.name AS callerService, fn.name AS functionName,
				       CASE WHEN call:GRPCCall THEN 'gRPC' WHEN call:HTTPCall THEN 'HTTP' WHEN call:OutboxCall THEN 'Outbox' ELSE 'Unknown' END AS callType,
				       CASE WHEN call:GRPCCall THEN coalesce(call.targetMethod,'')
				            WHEN call:HTTPCall THEN coalesce(call.url,'')
				            ELSE coalesce(call.eventType,'') END AS callTarget,
				       targetSvc.name AS targetService,
				       coalesce(handler.name, '') AS handlerName,
				       coalesce(handler.filePath, '') AS handlerFile,
				       coalesce(handler.confidence, 0.0) AS resolutionConf
				ORDER BY callerService, callType LIMIT 50`
			params = map[string]interface{}{"startService": startService, "startFunction": startFunction}
		} else {
			cypher = `
				MATCH (callerSvc:Service)-[:CONTAINS]->(file:File)-[:CONTAINS]->(fn:Function)-[:CALLS_API]->(call)-[:CALLS_SERVICE]->(targetSvc:Service)
				WHERE callerSvc.name IN $frontier AND callerSvc <> targetSvc
				OPTIONAL MATCH (call)-[:RESOLVES_TO]->(handler:Function)
				RETURN callerSvc.name AS callerService, fn.name AS functionName,
				       CASE WHEN call:GRPCCall THEN 'gRPC' WHEN call:HTTPCall THEN 'HTTP' WHEN call:OutboxCall THEN 'Outbox' ELSE 'Unknown' END AS callType,
				       CASE WHEN call:GRPCCall THEN coalesce(call.targetMethod,'')
				            WHEN call:HTTPCall THEN coalesce(call.url,'')
				            ELSE coalesce(call.eventType,'') END AS callTarget,
				       targetSvc.name AS targetService,
				       coalesce(handler.name, '') AS handlerName,
				       coalesce(handler.filePath, '') AS handlerFile,
				       coalesce(handler.confidence, 0.0) AS resolutionConf
				ORDER BY callerService, callType LIMIT 50`
			params = map[string]interface{}{"frontier": frontier}
		}

		records, err := s.client.ExecuteQuery(ctx, cypher, params)
		if err != nil {
			output.WriteString(fmt.Sprintf("Error querying hop %d: %v\n\n", hop, err))
			break
		}

		nextFrontier := []string{}
		var calls []crossCall
		for _, r := range records {
			m := r.AsMap()
			conf := 0.0
			if v, ok := m["resolutionConf"].(float64); ok {
				conf = v
			}
			c := crossCall{
				CallerService:  getStringFromRecord(m, "callerService"),
				FunctionName:   getStringFromRecord(m, "functionName"),
				CallType:       getStringFromRecord(m, "callType"),
				CallTarget:     getStringFromRecord(m, "callTarget"),
				TargetService:  getStringFromRecord(m, "targetService"),
				HandlerName:    getStringFromRecord(m, "handlerName"),
				HandlerFile:    getStringFromRecord(m, "handlerFile"),
				ResolutionConf: conf,
			}
			calls = append(calls, c)
			if !visited[c.TargetService] {
				visited[c.TargetService] = true
				nextFrontier = append(nextFrontier, c.TargetService)
			}
		}

		if len(calls) == 0 {
			output.WriteString("_No outbound cross-service calls found._\n\n")
			break
		}

		output.WriteString("| Caller | Function | Type | Call Target | Target Service | Handler |\n")
		output.WriteString("|--------|----------|------|-------------|----------------|---------|\n")
		for _, c := range calls {
			handler := "_unresolved_"
			if c.HandlerName != "" {
				handler = fmt.Sprintf("`%s`", c.HandlerName)
				if c.ResolutionConf > 0 {
					handler += fmt.Sprintf(" (%.0f%%)", c.ResolutionConf*100)
				}
			}
			output.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %s | %s |\n",
				c.CallerService, c.FunctionName, c.CallType, c.CallTarget, c.TargetService, handler))
		}
		output.WriteString("\n")

		frontier = nextFrontier
	}

	if len(visited) > 1 {
		svcList := make([]string, 0, len(visited))
		for svc := range visited {
			svcList = append(svcList, svc)
		}
		sort.Strings(svcList)
		output.WriteString(fmt.Sprintf("**Services reached**: %s\n", strings.Join(svcList, " → ")))
	}

	return ToolCallResponse{
		Content: []ToolContent{{Type: "text", Text: output.String()}},
	}
}

// handleExpandStepTool returns the full details for a named step within a flow.
// This is the companion to codegraph_generate_flows: the main flow view shows only
// the phase-numbered execution trace; call this to drill into file location, signature,
// and scope annotations for a specific step.
func (s *CodeGraphMCPServer) handleExpandStepTool(ctx context.Context, args map[string]interface{}) ToolCallResponse {
	flowNodeKey, ok := args["flow_node_key"].(string)
	if !ok || flowNodeKey == "" {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: "Error: flow_node_key is required (shown in the codegraph_generate_flows output)"}},
			IsError: true,
		}
	}
	stepName, ok := args["step_name"].(string)
	if !ok || stepName == "" {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: "Error: step_name is required (function/method name as shown in the execution trace)"}},
			IsError: true,
		}
	}

	scopeCtx := parseScopeContextArg(args)
	summaryGen := query.NewBehavioralSummaryGenerator(s.client)
	summaryGen.SetScope(scopeCtx)

	details, err := summaryGen.GetStepDetails(ctx, flowNodeKey, stepName)
	if err != nil {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error querying step details: %v", err)}},
			IsError: true,
		}
	}
	if len(details) == 0 {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("No step named %q found in flow %q. Check the spelling matches the execution trace.", stepName, flowNodeKey)}},
		}
	}

	var output strings.Builder
	output.WriteString(fmt.Sprintf("## Step: `%s`\n\n", stepName))
	for i, d := range details {
		if len(details) > 1 {
			output.WriteString(fmt.Sprintf("### Occurrence %d\n", i+1))
		}
		if d.FilePath != "" {
			output.WriteString(fmt.Sprintf("- **File**: `%s`\n", d.FilePath))
			if d.StartLine > 0 {
				output.WriteString(fmt.Sprintf("- **Lines**: %d–%d\n", d.StartLine, d.EndLine))
			}
		}
		if d.Signature != "" {
			output.WriteString(fmt.Sprintf("- **Signature**: `%s`\n", d.Signature))
		}
		if d.Condition != "" {
			output.WriteString(fmt.Sprintf("- **Under condition**: `%s`\n", d.Condition))
		}
		if d.InTx {
			output.WriteString("- **In transaction**: yes\n")
		}
		if d.ParallelGroup != "" {
			output.WriteString("- **Parallel group**: yes (concurrent with siblings sharing the same fork)\n")
		}
		if d.Docstring != "" {
			output.WriteString(fmt.Sprintf("\n**Docstring**: %s\n", d.Docstring))
		}
		output.WriteString("\n")
	}
	return ToolCallResponse{
		Content: []ToolContent{{Type: "text", Text: output.String()}},
	}
}

// handleRPCDependenciesTool returns all dependencies for a single named RPC handler.
func (s *CodeGraphMCPServer) handleRPCDependenciesTool(ctx context.Context, args map[string]interface{}) ToolCallResponse {
	service, ok := args["service"].(string)
	if !ok || service == "" {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: "Error: service is required"}},
			IsError: true,
		}
	}
	rpc, ok := args["rpc"].(string)
	if !ok || rpc == "" {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: "Error: rpc is required"}},
			IsError: true,
		}
	}
	scope := "main"
	if s, ok := args["scope"].(string); ok && s != "" {
		scope = s
	}

	// Locate handler, then fan out to DB/gRPC/HTTP/event edges with depth cap.
	// Split the optional matches per edge type to avoid cross-join explosion.
	cypher := `
		MATCH (svc:Service {name: $service})
		WHERE svc.scopeId = $scope OR svc.scopeId = 'main'
		MATCH (svc)-[:CONTAINS*1..4]->(handler)
		WHERE (handler:Function OR handler:Method)
		  AND (handler.name = $rpc OR handler.exposedAs = $rpc)
		WITH handler ORDER BY coalesce(handler.isRPCHandler,false) DESC LIMIT 1

		OPTIONAL MATCH (handler)-[:CALLS*0..6]->(c1)-[:CALLS_DB]->(db:DBCall)
		OPTIONAL MATCH (handler)-[rg:REACHES_CALL]->(grpc:GRPCCall)
		OPTIONAL MATCH (handler)-[rh:REACHES_CALL]->(http:HTTPCall)
		OPTIONAL MATCH (handler)-[:CALLS*0..6]->(c4)-[:CALLS_API]->(event:OutboxCall)

		RETURN
		  handler.name AS handlerName,
		  handler.filePath AS handlerFile,
		  COLLECT(DISTINCT CASE WHEN db IS NOT NULL THEN {table: db.table, op: db.operation, line: db.line} ELSE null END) AS dbCalls,
		  COLLECT(DISTINCT CASE WHEN grpc IS NOT NULL THEN {service: grpc.targetService, method: grpc.targetMethod, hops: rg.hops, via: rg.viaFunction} ELSE null END) AS grpcCalls,
		  COLLECT(DISTINCT CASE WHEN http IS NOT NULL THEN {service: http.targetService, url: http.url, hops: rh.hops, via: rh.viaFunction} ELSE null END) AS httpCalls,
		  COLLECT(DISTINCT CASE WHEN event IS NOT NULL THEN {event: event.eventType, transport: event.transport} ELSE null END) AS events
		LIMIT 1
	`

	result, err := s.client.ExecuteQuery(ctx, cypher, map[string]any{
		"service": service,
		"rpc":     rpc,
		"scope":   scope,
	})
	if err != nil {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error: %v", err)}},
			IsError: true,
		}
	}
	if len(result) == 0 {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("No handler found for rpc=%q in service=%q", rpc, service)}},
		}
	}

	m := result[0].AsMap()
	handlerName := getStringFromRecord(m, "handlerName")
	handlerFile := getStringFromRecord(m, "handlerFile")

	var out strings.Builder
	out.WriteString(fmt.Sprintf("# RPC Dependencies: `%s` in **%s**\n\n", rpc, service))
	out.WriteString(fmt.Sprintf("**Handler**: `%s`", handlerName))
	if handlerFile != "" {
		out.WriteString(fmt.Sprintf(" — `%s`", handlerFile))
	}
	out.WriteString("\n\n")

	writeListSection := func(title string, items []interface{}, render func(map[string]interface{}) string) {
		out.WriteString(fmt.Sprintf("## %s\n\n", title))
		written := 0
		for _, raw := range items {
			if raw == nil {
				continue
			}
			row, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}
			line := render(row)
			if line == "" {
				continue
			}
			out.WriteString(fmt.Sprintf("- %s\n", line))
			written++
		}
		if written == 0 {
			out.WriteString("_None detected_\n")
		}
		out.WriteString("\n")
	}

	dbCalls, _ := m["dbCalls"].([]interface{})
	writeListSection("DB Tables", dbCalls, func(r map[string]interface{}) string {
		table := getStringFromRecord(r, "table")
		if table == "" {
			return ""
		}
		op := getStringFromRecord(r, "op")
		line := getIntFromRecord(r, "line")
		if op != "" && line > 0 {
			return fmt.Sprintf("`%s` — %s (line %d)", table, op, line)
		} else if op != "" {
			return fmt.Sprintf("`%s` — %s", table, op)
		}
		return fmt.Sprintf("`%s`", table)
	})

	// reachSuffix annotates a cross-service call with how deep in the call chain
	// it sits, so a dependency reached 8 hops down through business logic is
	// distinguishable from one the handler calls directly.
	reachSuffix := func(r map[string]interface{}) string {
		hops := getIntFromRecord(r, "hops")
		via := getStringFromRecord(r, "via")
		if hops <= 0 {
			return ""
		}
		if via != "" {
			return fmt.Sprintf(" _(%d hops deep, via `%s`)_", hops, via)
		}
		return fmt.Sprintf(" _(%d hops deep)_", hops)
	}

	grpcCalls, _ := m["grpcCalls"].([]interface{})
	writeListSection("gRPC Calls", grpcCalls, func(r map[string]interface{}) string {
		svcName := getStringFromRecord(r, "service")
		if svcName == "" {
			return ""
		}
		method := getStringFromRecord(r, "method")
		if method != "" {
			return fmt.Sprintf("**%s** — `%s`%s", svcName, method, reachSuffix(r))
		}
		return fmt.Sprintf("**%s**%s", svcName, reachSuffix(r))
	})

	httpCalls, _ := m["httpCalls"].([]interface{})
	writeListSection("HTTP Calls", httpCalls, func(r map[string]interface{}) string {
		svcName := getStringFromRecord(r, "service")
		if svcName == "" {
			return ""
		}
		url := getStringFromRecord(r, "url")
		if url != "" {
			return fmt.Sprintf("**%s** — `%s`%s", svcName, url, reachSuffix(r))
		}
		return fmt.Sprintf("**%s**%s", svcName, reachSuffix(r))
	})

	events, _ := m["events"].([]interface{})
	writeListSection("Events Published", events, func(r map[string]interface{}) string {
		eventType := getStringFromRecord(r, "event")
		if eventType == "" {
			return ""
		}
		transport := getStringFromRecord(r, "transport")
		if transport != "" {
			return fmt.Sprintf("`%s` via %s", eventType, transport)
		}
		return fmt.Sprintf("`%s`", eventType)
	})

	return ToolCallResponse{
		Content: []ToolContent{{Type: "text", Text: out.String()}},
	}
}

// handleServiceDependencyMapTool returns the full dependency manifest for every exposed RPC in a service.
func (s *CodeGraphMCPServer) handleServiceDependencyMapTool(ctx context.Context, args map[string]interface{}) ToolCallResponse {
	service, ok := args["service"].(string)
	if !ok || service == "" {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: "Error: service is required"}},
			IsError: true,
		}
	}
	scope := "main"
	if sv, ok := args["scope"].(string); ok && sv != "" {
		scope = sv
	}

	// Two separate optional matches to avoid cross-product between DB and service edges.
	cypher := `
		MATCH (svc:Service {name: $service})
		WHERE svc.scopeId = $scope OR svc.scopeId = 'main'
		MATCH (svc)-[:CONTAINS*1..4]->(handler:Function)
		WHERE handler.exposedAs IS NOT NULL

		OPTIONAL MATCH (handler)-[:CALLS*0..3]->(:Function)-[:CALLS_DB]->(db:DBCall)
		OPTIONAL MATCH (handler)-[:CALLS*0..3]->(:Function)-[:CALLS_API]->(call)-[:CALLS_SERVICE]->(dep:Service)
		WHERE call:GRPCCall OR call:HTTPCall

		RETURN handler.name AS rpc,
		       handler.exposedAs AS endpoint,
		       COLLECT(DISTINCT db.table) AS tables,
		       COLLECT(DISTINCT dep.name) AS dependsOn
		ORDER BY rpc
	`

	result, err := s.client.ExecuteQuery(ctx, cypher, map[string]any{
		"service": service,
		"scope":   scope,
	})
	if err != nil {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error: %v", err)}},
			IsError: true,
		}
	}

	var out strings.Builder
	out.WriteString(fmt.Sprintf("# Dependency Map for **%s**\n\n", service))

	if len(result) == 0 {
		out.WriteString("No exposed RPC handlers found. Ensure the service has been indexed and handlers have `exposedAs` set.\n")
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: out.String()}},
		}
	}

	out.WriteString(fmt.Sprintf("_Showing %d exposed RPC handler(s)_\n\n", len(result)))
	out.WriteString("| RPC / Handler | Endpoint | DB Tables | Downstream Services |\n")
	out.WriteString("|---------------|----------|-----------|---------------------|\n")

	for _, record := range result {
		m := record.AsMap()
		rpc := getStringFromRecord(m, "rpc")
		endpoint := getStringFromRecord(m, "endpoint")

		var tables []string
		if raw, ok := m["tables"].([]interface{}); ok {
			for _, t := range raw {
				if ts, ok := t.(string); ok && ts != "" {
					tables = append(tables, ts)
				}
			}
		}

		var deps []string
		if raw, ok := m["dependsOn"].([]interface{}); ok {
			for _, d := range raw {
				if ds, ok := d.(string); ok && ds != "" {
					deps = append(deps, ds)
				}
			}
		}

		tablesStr := "_none_"
		if len(tables) > 0 {
			sort.Strings(tables)
			quoted := make([]string, len(tables))
			for i, t := range tables {
				quoted[i] = "`" + t + "`"
			}
			tablesStr = strings.Join(quoted, ", ")
		}

		depsStr := "_none_"
		if len(deps) > 0 {
			sort.Strings(deps)
			depsStr = strings.Join(deps, ", ")
		}

		out.WriteString(fmt.Sprintf("| `%s` | `%s` | %s | %s |\n", rpc, endpoint, tablesStr, depsStr))
	}

	return ToolCallResponse{
		Content: []ToolContent{{Type: "text", Text: out.String()}},
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

	// Try both the configured workspace root AND its parent. In a multi-repo layout
	// (…/Tazapay/settlement, …/Tazapay/account, …), the MCP server is often spawned with
	// cwd = one repo (…/Tazapay/codegraph) rather than the shared parent. Stored paths are
	// relative to each service repo, so a sibling service resolves only against the parent.
	roots := []string{s.workspaceRoot}
	if parent := filepath.Dir(s.workspaceRoot); parent != "" && parent != s.workspaceRoot {
		roots = append(roots, parent)
	}
	for _, root := range roots {
		if fileUnderRoot(root, clean) {
			return true
		}
	}
	return false
}

// resolveWorkspaceFile locates filePath on disk and returns its absolute path.
// It mirrors fileInWorkspace's search — the configured workspace root AND its
// parent (multi-repo layout) — so any file the MCP considers "in workspace" can
// also be read back, including sibling service repos. Returns ("", false) if the
// file cannot be located.
func (s *CodeGraphMCPServer) resolveWorkspaceFile(filePath string) (string, bool) {
	if filePath == "" {
		return "", false
	}
	clean := filepath.Clean(filePath)
	roots := []string{s.workspaceRoot}
	if parent := filepath.Dir(s.workspaceRoot); parent != "" && parent != s.workspaceRoot {
		roots = append(roots, parent)
	}
	for _, root := range roots {
		if p, ok := resolveUnderRoot(root, clean); ok {
			return p, true
		}
	}
	return "", false
}

// fileUnderRoot reports whether clean (absolute, or relative to root or one of root's
// immediate/second-level subdirectories) resolves to an existing file.
func fileUnderRoot(root, clean string) bool {
	_, ok := resolveUnderRoot(root, clean)
	return ok
}

// resolveUnderRoot resolves clean against root, trying: the path as-is under root,
// then each of root's immediate and second-level subdirectories (a service repo, or
// "libs/query-go" when a submodule was indexed in isolation). Returns the resolved
// absolute path when it names an existing file.
func resolveUnderRoot(root, clean string) (string, bool) {
	if filepath.IsAbs(clean) {
		rel, err := filepath.Rel(root, clean)
		if err != nil || strings.HasPrefix(rel, "..") {
			return "", false
		}
		if _, err := os.Stat(clean); err == nil {
			return clean, true
		}
		return "", false
	}

	// 1) Path already rooted at repo root.
	if p := filepath.Join(root, clean); statIsFile(p) {
		return p, true
	}

	// 2) Path rooted at module directories (e.g. a service repo, or "libs/query-go"
	// when a submodule was indexed in isolation).
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", false
	}
	for _, e1 := range entries {
		if !e1.IsDir() {
			continue
		}
		l1 := filepath.Join(root, e1.Name())
		if p := filepath.Join(l1, clean); statIsFile(p) {
			return p, true
		}
		subEntries, err := os.ReadDir(l1)
		if err != nil {
			continue
		}
		for _, e2 := range subEntries {
			if !e2.IsDir() {
				continue
			}
			if p := filepath.Join(l1, e2.Name(), clean); statIsFile(p) {
				return p, true
			}
		}
	}
	return "", false
}

// statIsFile reports whether p exists as a regular (non-directory) file.
func statIsFile(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
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

func (s *CodeGraphMCPServer) handleRPCAnatomyTool(ctx context.Context, args map[string]interface{}) ToolCallResponse {
	rpcName, ok := args["rpc_name"].(string)
	if !ok || rpcName == "" {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: "Error: rpc_name is required"}},
			IsError: true,
		}
	}
	service := getOptionalStringArg(args, "service")
	scopeCtx := parseScopeContextArg(args)

	// Locate the handler function/method. Names carry a SCIP "()." suffix, so normalize
	// before comparing; RPC handlers are usually receiver methods (:Method).
	serviceFilter := ""
	if service != "" {
		serviceFilter = `AND EXISTS { MATCH (svc:Service {name: $service})-[:CONTAINS*1..5]->(f) }`
	}
	locateCypher := `
		MATCH (f)
		WHERE (f:Function OR f:Method)
		  AND (f.scopeId = $scopeId OR f.scopeId = 'main')
		  AND (toLower(f.name) = toLower($rpcName)
		       OR toLower(f.name) ENDS WITH ('.' + toLower($rpcName)))
		` + serviceFilter + `
		RETURN elementId(f) AS fId, f.name AS fName, f.filePath AS fFile,
		       coalesce(f.startLine, 0) AS startLine
		ORDER BY coalesce(f.isRPCHandler,false) DESC, startLine
		LIMIT 1
	`
	params := map[string]any{
		"scopeId": scopeCtx.ScopeID,
		"rpcName": rpcName,
		"service": service,
	}
	roots, err := s.client.ExecuteQuery(ctx, locateCypher, params)
	if err != nil || len(roots) == 0 {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("No handler found for %q", rpcName)}},
		}
	}

	rm := roots[0].AsMap()
	fId := getStringFromRecord(rm, "fId")
	fName := getStringFromRecord(rm, "fName")
	fFile := getStringFromRecord(rm, "fFile")

	// Gather all call sites reachable within 6 hops (deep handlers like PayoutAction
	// reach their DB/gRPC sites via performX → processY → repo chains ~5 hops down).
	anatomyCypher := `
		MATCH (root) WHERE elementId(root) = $rootId

		// DB calls
		OPTIONAL MATCH (root)-[:CALLS*0..6]->(fn)-[:CALLS_DB]->(db:DBCall)
		// gRPC calls — cross-service, any depth, via precomputed reachability closure.
		// Target read from the resolved targetService property (not a CALLS_SERVICE
		// edge) so calls to un-indexed downstream services still surface.
		OPTIONAL MATCH (root)-[rg:REACHES_CALL]->(grpc:GRPCCall)
		OPTIONAL MATCH (grpc)-[:RESOLVES_TO]->(handler)
		// HTTP calls — cross-service, any depth
		OPTIONAL MATCH (root)-[rh:REACHES_CALL]->(http:HTTPCall)
		// Events
		OPTIONAL MATCH (root)-[:CALLS*0..6]->(fn4)-[:CALLS_API]->(event:OutboxCall)

		RETURN
		  COLLECT(DISTINCT CASE WHEN db IS NOT NULL THEN {
		    kind: 'db', table: db.table, op: db.operation, line: db.line,
		    file: db.filePath, viaFn: fn.name
		  } END) AS dbSteps,
		  COLLECT(DISTINCT CASE WHEN grpc IS NOT NULL THEN {
		    kind: 'grpc', target: grpc.targetMethod, protoService: coalesce(grpc.protoService,''),
		    protoMethod: coalesce(grpc.protoMethod,''), targetSvc: coalesce(grpc.targetService,''),
		    handler: coalesce(handler.name,''), handlerFile: coalesce(handler.filePath,''),
		    line: grpc.line, file: grpc.filePath, viaFn: rg.viaFunction, hops: rg.hops
		  } END) AS grpcSteps,
		  COLLECT(DISTINCT CASE WHEN http IS NOT NULL THEN {
		    kind: 'http', url: http.url, method: http.method,
		    targetSvc: coalesce(http.targetService,''), line: http.line,
		    file: http.filePath, viaFn: rh.viaFunction, hops: rh.hops
		  } END) AS httpSteps,
		  COLLECT(DISTINCT CASE WHEN event IS NOT NULL THEN {
		    kind: 'outbox', event: event.eventType, transport: event.transport,
		    line: event.line, file: event.filePath, viaFn: fn4.name
		  } END) AS eventSteps
		LIMIT 1
	`

	result, err := s.client.ExecuteQuery(ctx, anatomyCypher, map[string]any{"rootId": fId})
	if err != nil {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error: %v", err)}},
			IsError: true,
		}
	}

	var output strings.Builder
	output.WriteString(fmt.Sprintf("## RPC Anatomy: `%s`\n\n", fName))
	if fFile != "" {
		output.WriteString(fmt.Sprintf("**Handler**: `%s` in `%s`\n\n", fName, fFile))
	}

	if len(result) > 0 {
		m := result[0].AsMap()

		type step struct {
			Line int
			Text string
		}
		var steps []step

		collectSteps := func(key string, formatter func(map[string]any) (int, string)) {
			if items, ok := m[key].([]interface{}); ok {
				for _, item := range items {
					if item == nil {
						continue
					}
					if itemMap, ok := item.(map[string]any); ok {
						line, text := formatter(itemMap)
						if text != "" {
							steps = append(steps, step{Line: line, Text: text})
						}
					}
				}
			}
		}

		collectSteps("dbSteps", func(m map[string]any) (int, string) {
			table, _ := m["table"].(string)
			op, _ := m["op"].(string)
			line, _ := m["line"].(int64)
			viaFn, _ := m["viaFn"].(string)
			if table == "" {
				return 0, ""
			}
			text := fmt.Sprintf("**DB** `%s` %s", op, table)
			if viaFn != "" {
				text += fmt.Sprintf(" (via `%s`)", viaFn)
			}
			return int(line), text
		})

		collectSteps("grpcSteps", func(m map[string]any) (int, string) {
			target, _ := m["target"].(string)
			targetSvc, _ := m["targetSvc"].(string)
			handler, _ := m["handler"].(string)
			line, _ := m["line"].(int64)
			viaFn, _ := m["viaFn"].(string)
			if target == "" {
				return 0, ""
			}
			text := fmt.Sprintf("**gRPC** → `%s`", target)
			if targetSvc != "" {
				text += fmt.Sprintf(" @ **%s**", targetSvc)
			}
			if handler != "" {
				text += fmt.Sprintf(" → handler `%s`", handler)
			}
			if viaFn != "" {
				text += fmt.Sprintf(" (via `%s`)", viaFn)
			}
			return int(line), text
		})

		collectSteps("httpSteps", func(m map[string]any) (int, string) {
			url, _ := m["url"].(string)
			method, _ := m["method"].(string)
			targetSvc, _ := m["targetSvc"].(string)
			line, _ := m["line"].(int64)
			viaFn, _ := m["viaFn"].(string)
			if url == "" {
				return 0, ""
			}
			text := fmt.Sprintf("**HTTP** %s `%s`", method, url)
			if targetSvc != "" {
				text += fmt.Sprintf(" @ **%s**", targetSvc)
			}
			if viaFn != "" {
				text += fmt.Sprintf(" (via `%s`)", viaFn)
			}
			return int(line), text
		})

		collectSteps("eventSteps", func(m map[string]any) (int, string) {
			event, _ := m["event"].(string)
			transport, _ := m["transport"].(string)
			line, _ := m["line"].(int64)
			viaFn, _ := m["viaFn"].(string)
			if event == "" {
				return 0, ""
			}
			text := fmt.Sprintf("**Event** `%s` via %s", event, transport)
			if viaFn != "" {
				text += fmt.Sprintf(" (via `%s`)", viaFn)
			}
			return int(line), text
		})

		// Sort by line number (insertion sort).
		for i := 1; i < len(steps); i++ {
			for j := i; j > 0 && steps[j].Line < steps[j-1].Line; j-- {
				steps[j], steps[j-1] = steps[j-1], steps[j]
			}
		}

		if len(steps) == 0 {
			output.WriteString("_No DB/RPC/event call sites found within 3 hops. Ensure the service is indexed with SCIP._\n")
		} else {
			output.WriteString("### Call Sites (source order)\n\n")
			for i, st := range steps {
				if st.Line > 0 {
					output.WriteString(fmt.Sprintf("%d. (line %d) %s\n", i+1, st.Line, st.Text))
				} else {
					output.WriteString(fmt.Sprintf("%d. %s\n", i+1, st.Text))
				}
			}
		}
	}

	return ToolCallResponse{
		Content: []ToolContent{{Type: "text", Text: output.String()}},
	}
}

// handleEventFlowTool traces the full lifecycle of an async event:
// producers → EventType hub → consuming service listener/handler → downstream fan-out.
func (s *CodeGraphMCPServer) handleEventFlowTool(ctx context.Context, args map[string]interface{}) ToolCallResponse {
	eventName, ok := args["event_name"].(string)
	if !ok || eventName == "" {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: "Error: event_name is required"}},
			IsError: true,
		}
	}

	var output strings.Builder
	output.WriteString(fmt.Sprintf("# Async Event Flow: `%s`\n\n", eventName))

	// Determine whether the input is "group.action" or just a group name.
	isGroup := !strings.Contains(eventName, ".")
	var etFilter string
	if isGroup {
		etFilter = `et.group = $eventName`
	} else {
		etFilter = `et.eventType = $eventName`
	}

	// --- Producers ---
	producerQuery := fmt.Sprintf(`
		MATCH (oc:OutboxCall)-[:EMITS_EVENT]->(et:EventType)
		WHERE %s
		OPTIONAL MATCH (fn)-[:CALLS_API]->(oc)
		RETURN et.eventType AS event, oc.callerService AS callerSvc,
		       coalesce(fn.name,'') AS fnName, coalesce(fn.filePath,'') AS filePath,
		       oc.destService AS destSvc, coalesce(oc.destQueue,'') AS destQueue,
		       coalesce(oc.line,0) AS emitLine
		ORDER BY et.eventType, oc.callerService
		LIMIT 60
	`, etFilter)
	producerRows, err := s.client.ExecuteQuery(ctx, producerQuery, map[string]interface{}{"eventName": eventName})
	if err != nil {
		output.WriteString(fmt.Sprintf("_Error querying producers: %v_\n\n", err))
	} else if len(producerRows) == 0 {
		output.WriteString("_No producers found. The event may not have been indexed yet._\n\n")
	} else {
		output.WriteString("## Producers\n\n")
		output.WriteString("| Event | Emitting Service | Function | Line | → Destination | Queue |\n")
		output.WriteString("|-------|-----------------|----------|------|--------------|-------|\n")
		for _, r := range producerRows {
			m := r.AsMap()
			emitLine := int64(0)
			if v, ok2 := m["emitLine"].(int64); ok2 {
				emitLine = v
			}
			lineStr := ""
			if emitLine > 0 {
				lineStr = fmt.Sprintf("%d", emitLine)
			}
			output.WriteString(fmt.Sprintf("| `%s` | %s | `%s` | %s | **%s** | `%s` |\n",
				getStringFromRecord(m, "event"),
				getStringFromRecord(m, "callerSvc"),
				getStringFromRecord(m, "fnName"),
				lineStr,
				getStringFromRecord(m, "destSvc"),
				getStringFromRecord(m, "destQueue"),
			))
		}
		output.WriteString("\n")
	}

	// --- Consumers (ROUTED_TO targets on the EventType hub) ---
	consumerQuery := fmt.Sprintf(`
		MATCH (et:EventType)
		WHERE %s
		MATCH (et)-[rr:ROUTED_TO]->(f)
		RETURN et.eventType AS event, rr.service AS consumerSvc, rr.tier AS tier,
		       coalesce(rr.confidence, 0.0) AS confidence,
		       f.name AS fnName, coalesce(f.filePath,'') AS filePath
		ORDER BY et.eventType, rr.tier DESC, rr.confidence DESC
		LIMIT 60
	`, etFilter)
	consumerRows, err := s.client.ExecuteQuery(ctx, consumerQuery, map[string]interface{}{"eventName": eventName})
	if err == nil && len(consumerRows) > 0 {
		output.WriteString("## Consumers\n\n")
		output.WriteString("| Event | Consuming Service | Tier | Confidence | Handler Function | File |\n")
		output.WriteString("|-------|------------------|------|------------|-----------------|------|\n")
		for _, r := range consumerRows {
			m := r.AsMap()
			conf := 0.0
			if v, ok2 := m["confidence"].(float64); ok2 {
				conf = v
			}
			output.WriteString(fmt.Sprintf("| `%s` | **%s** | %s | %.1f | `%s` | %s |\n",
				getStringFromRecord(m, "event"),
				getStringFromRecord(m, "consumerSvc"),
				getStringFromRecord(m, "tier"),
				conf,
				getStringFromRecord(m, "fnName"),
				getStringFromRecord(m, "filePath"),
			))
		}
		output.WriteString("\n")
	}

	// --- Fan-Out: handlers in the consuming service that re-emit downstream ---
	fanoutQuery := fmt.Sprintf(`
		MATCH (et:EventType)
		WHERE %s
		MATCH (et)-[:ROUTED_TO]->(handler)
		MATCH (handler)<-[:CONTAINS*1..5]-(relaySvc:Service)
		MATCH (handler)-[:CALLS_API]->(oc2:OutboxCall)-[:EMITS_EVENT]->(et2:EventType)
		RETURN et.eventType AS inboundEvent, relaySvc.name AS relayService,
		       handler.name AS relayFn, et2.eventType AS outboundEvent,
		       coalesce(oc2.destService,'') AS downstreamSvc
		ORDER BY et.eventType, relaySvc.name, et2.eventType
		LIMIT 40
	`, etFilter)
	fanoutRows, err := s.client.ExecuteQuery(ctx, fanoutQuery, map[string]interface{}{"eventName": eventName})
	if err == nil && len(fanoutRows) > 0 {
		output.WriteString("## Fan-Out (Downstream Re-Broadcasts)\n\n")
		output.WriteString("| Inbound Event | Relay Service | Relay Function | Outbound Event | Downstream Service |\n")
		output.WriteString("|---------------|--------------|----------------|----------------|-----------------|\n")
		for _, r := range fanoutRows {
			m := r.AsMap()
			output.WriteString(fmt.Sprintf("| `%s` | %s | `%s` | `%s` | **%s** |\n",
				getStringFromRecord(m, "inboundEvent"),
				getStringFromRecord(m, "relayService"),
				getStringFromRecord(m, "relayFn"),
				getStringFromRecord(m, "outboundEvent"),
				getStringFromRecord(m, "downstreamSvc"),
			))
		}
		output.WriteString("\n")
	}

	return ToolCallResponse{
		Content: []ToolContent{{Type: "text", Text: output.String()}},
	}
}

// handleAPICallersTool answers "who calls this API?" across service boundaries in one call:
// inbound gRPC/HTTP call sites (RESOLVES_TO reverse + proto-name fallback), each folded back
// to the calling service's own entry API, plus async triggers (ROUTED_TO events + producers).
func (s *CodeGraphMCPServer) handleAPICallersTool(ctx context.Context, args map[string]interface{}) ToolCallResponse {
	rpc, ok := args["rpc"].(string)
	if !ok || rpc == "" {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: "Error: rpc parameter is required"}},
			IsError: true,
		}
	}
	service := getOptionalStringArg(args, "service")
	scopeCtx := parseScopeContextArg(args)

	maxDepth := 5
	if d, ok := args["max_depth"].(float64); ok && d > 0 {
		maxDepth = int(d)
	}
	if maxDepth > 10 {
		maxDepth = 10
	}

	// --- 1. Resolve the handler node (exact name first, service-disambiguated) ---
	serviceFilter := ""
	if service != "" {
		serviceFilter = `AND EXISTS { MATCH (svc:Service)-[:CONTAINS*1..5]->(h) WHERE toLower(svc.name) = toLower($service) }`
	}
	isRoute := strings.HasPrefix(rpc, "/")
	// SCIP-indexed names carry a "()." suffix (e.g. "FundPayout()."); normalize before comparing.
	nameClause := `(toLower(h.name) = toLower($rpc) OR toLower(h.name) ENDS WITH ('.' + toLower($rpc)))`
	if isRoute {
		// Route form: find handlers reached by a RESOLVES_TO from an HTTPCall whose URL contains the route.
		nameClause = `EXISTS { MATCH (rc:HTTPCall)-[:RESOLVES_TO]->(h) WHERE toLower(rc.url) CONTAINS toLower($rpc) }`
	}
	locateCypher := fmt.Sprintf(`
		MATCH (h)
		WHERE (h:Function OR h:Method)
		  AND (h.scopeId = $scopeId OR h.scopeId = 'main')
		  AND coalesce(h.isTestFunction, false) = false
		  AND %s
		  %s
		OPTIONAL MATCH (svc:Service)-[:CONTAINS*1..5]->(h)
		RETURN h.nodeKey AS nodeKey, h.name AS name, coalesce(h.filePath,'') AS filePath,
		       coalesce(h.startLine, 0) AS startLine, coalesce(h.summary,'') AS summary,
		       coalesce(h.isRPCHandler, false) AS isHandler, coalesce(svc.name,'') AS serviceName
		ORDER BY isHandler DESC, name ASC
		LIMIT 10`, nameClause, serviceFilter)

	rows, err := s.client.ExecuteQuery(ctx, locateCypher, map[string]any{
		"scopeId": scopeCtx.ScopeID,
		"rpc":     rpc,
		"service": service,
	})
	if err != nil {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error locating handler: %v", err)}},
			IsError: true,
		}
	}
	if len(rows) == 0 {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("No handler found matching %q. Try codegraph_search to find the exact handler name.", rpc)}},
		}
	}

	type handlerInfo struct {
		NodeKey, Name, FilePath, Summary, Service string
		StartLine                                 int
	}
	var handlers []handlerInfo
	seenSvc := map[string]bool{}
	for _, r := range rows {
		m := r.AsMap()
		h := handlerInfo{
			NodeKey:   getStringFromRecord(m, "nodeKey"),
			Name:      getStringFromRecord(m, "name"),
			FilePath:  getStringFromRecord(m, "filePath"),
			Summary:   getStringFromRecord(m, "summary"),
			Service:   getStringFromRecord(m, "serviceName"),
			StartLine: getIntFromRecord(m, "startLine"),
		}
		if h.NodeKey == "" {
			continue
		}
		if h.FilePath != "" && !s.fileInWorkspace(h.FilePath) {
			continue
		}
		handlers = append(handlers, h)
		seenSvc[h.Service] = true
	}
	if len(handlers) == 0 {
		return ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("No workspace handler found matching %q.", rpc)}},
		}
	}
	// Ambiguity across services with no service filter: list candidates instead of guessing.
	if service == "" && len(seenSvc) > 1 {
		var out strings.Builder
		out.WriteString(fmt.Sprintf("Multiple services define %q — re-call with the `service` parameter:\n\n", rpc))
		for _, h := range handlers {
			out.WriteString(fmt.Sprintf("- `%s` in **%s** (%s:%d)", h.Name, h.Service, h.FilePath, h.StartLine))
			if h.Summary != "" {
				out.WriteString(" — " + h.Summary)
			}
			out.WriteString("\n")
		}
		return ToolCallResponse{Content: []ToolContent{{Type: "text", Text: out.String()}}}
	}

	target := handlers[0]

	var output strings.Builder
	output.WriteString(fmt.Sprintf("## Who calls `%s`", target.Name))
	if target.Service != "" {
		output.WriteString(fmt.Sprintf(" (%s)", target.Service))
	}
	output.WriteString("\n\n")
	output.WriteString(fmt.Sprintf("**Handler**: `%s` — %s:%d", target.Name, target.FilePath, target.StartLine))
	if target.Summary != "" {
		output.WriteString("\n**Does**: " + target.Summary)
	}
	output.WriteString("\n\n")

	// --- 2. Inbound sync call sites: RESOLVES_TO reverse, plus proto-name fallback for
	// call sites the resolution stage didn't link (e.g. partial index). Each caller is
	// folded back to its own service's entry API via an upstream CALLS walk.
	inboundCypher := fmt.Sprintf(`
		MATCH (h {nodeKey: $handlerKey})
		CALL {
			WITH h
			MATCH (call)-[r:RESOLVES_TO]->(h)
			WHERE call:GRPCCall OR call:HTTPCall
			RETURN call, coalesce(r.confidence, 0.0) AS confidence,
			       coalesce(r.resolutionMethod, '') AS how
			UNION
			WITH h
			MATCH (call:GRPCCall)
			WHERE toLower(coalesce(call.protoMethod, call.targetMethod)) = toLower(h.name)
			  AND NOT (call)-[:RESOLVES_TO]->()
			RETURN call, 0.5 AS confidence, 'proto_name_fallback' AS how
		}
		OPTIONAL MATCH (fn)-[:CALLS_API]->(call) WHERE fn:Function OR fn:Method
		OPTIONAL MATCH p = (entry)-[:CALLS*1..%d]->(fn)
		WHERE (entry:Function OR entry:Method)
		  AND coalesce(entry.isRPCHandler, false) = true
		  AND coalesce(entry.isTestFunction, false) = false
		WITH call, confidence, how, fn, entry, length(p) AS hops
		ORDER BY hops ASC
		WITH call, confidence, how, fn,
		     collect(DISTINCT {name: entry.name, file: entry.filePath,
		                       line: entry.startLine, summary: coalesce(entry.summary,''), hops: hops})[0..3] AS entries
		RETURN coalesce(call.callerService,'') AS callerService,
		       CASE WHEN call:GRPCCall THEN 'gRPC' ELSE 'HTTP' END AS protocol,
		       coalesce(call.filePath,'') AS callFile, coalesce(call.line,0) AS callLine,
		       confidence, how,
		       coalesce(fn.name,'') AS fnName, coalesce(fn.summary,'') AS fnSummary,
		       coalesce(fn.isRPCHandler, false) AS fnIsHandler,
		       coalesce(fn.filePath,'') AS fnFile, coalesce(fn.startLine,0) AS fnLine,
		       entries
		ORDER BY callerService, callFile, callLine
		LIMIT 60`, maxDepth)

	inRows, err := s.client.ExecuteQuery(ctx, inboundCypher, map[string]any{"handlerKey": target.NodeKey})
	if err != nil {
		output.WriteString(fmt.Sprintf("_Error querying inbound calls: %v_\n\n", err))
	} else if len(inRows) == 0 {
		output.WriteString("### Inbound API calls\n\n_None found. Either nothing calls this RPC, or the calling services are not indexed (check codegraph_list_services)._\n\n")
	} else {
		output.WriteString("### Inbound API calls\n\n")
		for _, r := range inRows {
			m := r.AsMap()
			callerSvc := getStringFromRecord(m, "callerService")
			protocol := getStringFromRecord(m, "protocol")
			callFile := getStringFromRecord(m, "callFile")
			callLine := getIntFromRecord(m, "callLine")
			fnName := getStringFromRecord(m, "fnName")
			fnSummary := getStringFromRecord(m, "fnSummary")
			fnIsHandler := getBoolFromRecord(m, "fnIsHandler")
			fnFile := getStringFromRecord(m, "fnFile")
			fnLine := getIntFromRecord(m, "fnLine")
			conf := 0.0
			if v, ok := m["confidence"].(float64); ok {
				conf = v
			}
			how := getStringFromRecord(m, "how")

			output.WriteString(fmt.Sprintf("- **%s** [%s, %.1f %s] call at %s:%d\n", callerSvc, protocol, conf, how, callFile, callLine))
			if fnName != "" {
				marker := ""
				if fnIsHandler {
					marker = " (API endpoint)"
				}
				output.WriteString(fmt.Sprintf("  - in `%s`%s — %s:%d", fnName, marker, fnFile, fnLine))
				if fnSummary != "" {
					output.WriteString(" — " + fnSummary)
				}
				output.WriteString("\n")
			}
			if entries, ok := m["entries"].([]interface{}); ok && !fnIsHandler {
				for _, e := range entries {
					em, isMap := e.(map[string]any)
					if !isMap || em["name"] == nil {
						continue
					}
					name, _ := em["name"].(string)
					file, _ := em["file"].(string)
					line, _ := em["line"].(int64)
					summ, _ := em["summary"].(string)
					hops, _ := em["hops"].(int64)
					if name == "" {
						continue
					}
					output.WriteString(fmt.Sprintf("  - entry API: `%s` — %s:%d (%d hops up)", name, file, line, hops))
					if summ != "" {
						output.WriteString(" — " + summ)
					}
					output.WriteString("\n")
				}
			}
		}
		if len(inRows) == 60 {
			output.WriteString("\n_Truncated at 60 inbound call sites._\n")
		}
		output.WriteString("\n")
	}

	// --- 3. Async triggers: events routed to this handler, and who produces them ---
	asyncCypher := `
		MATCH (et:EventType)-[rt:ROUTED_TO]->(h {nodeKey: $handlerKey})
		OPTIONAL MATCH (oc:OutboxCall)-[:EMITS_EVENT]->(et)
		OPTIONAL MATCH (pfn)-[:CALLS_API]->(oc) WHERE pfn:Function OR pfn:Method
		RETURN et.eventType AS event, rt.tier AS tier, coalesce(rt.confidence,0.0) AS confidence,
		       coalesce(oc.callerService,'') AS producerSvc, coalesce(oc.filePath,'') AS producerFile,
		       coalesce(oc.line,0) AS producerLine, coalesce(pfn.name,'') AS producerFn
		ORDER BY event, producerSvc
		LIMIT 40`
	asyncRows, err := s.client.ExecuteQuery(ctx, asyncCypher, map[string]any{"handlerKey": target.NodeKey})
	if err == nil && len(asyncRows) > 0 {
		output.WriteString("### Async triggers (events routed to this handler)\n\n")
		for _, r := range asyncRows {
			m := r.AsMap()
			conf := 0.0
			if v, ok := m["confidence"].(float64); ok {
				conf = v
			}
			event := getStringFromRecord(m, "event")
			producerSvc := getStringFromRecord(m, "producerSvc")
			producerFn := getStringFromRecord(m, "producerFn")
			producerFile := getStringFromRecord(m, "producerFile")
			producerLine := getIntFromRecord(m, "producerLine")
			entry := fmt.Sprintf("- `%s` [%s, %.1f]", event, getStringFromRecord(m, "tier"), conf)
			if producerSvc != "" {
				entry += fmt.Sprintf(" ← produced by **%s**", producerSvc)
				if producerFn != "" {
					entry += fmt.Sprintf(" `%s`", producerFn)
				}
				if producerFile != "" {
					entry += fmt.Sprintf(" (%s:%d)", producerFile, producerLine)
				}
			}
			output.WriteString(entry + "\n")
		}
		if len(asyncRows) == 40 {
			output.WriteString("\n_Truncated at 40 async trigger rows._\n")
		}
		output.WriteString("\n")
	}

	return ToolCallResponse{
		Content: []ToolContent{{Type: "text", Text: output.String()}},
	}
}

// getSourceByLocation extracts a function body by the exact filePath + line range of the
// already-disambiguated graph record, so a name collision can never return the wrong body.
func (s *CodeGraphMCPServer) getSourceByLocation(record map[string]interface{}, functionName string) (string, error) {
	filePath := getStringFromRecord(record, "filePath")
	startLine := getIntFromRecord(record, "startLine")
	endLine := getIntFromRecord(record, "endLine")
	if filePath == "" || startLine <= 0 || endLine < startLine {
		return "", fmt.Errorf("no precise location for %s", functionName)
	}
	// Resolve against every workspace root (incl. sibling service repos) so source
	// from a repo other than the MCP's cwd — e.g. settlement while cwd=codegraph —
	// can still be read back.
	resolved, ok := s.resolveWorkspaceFile(filePath)
	if !ok {
		return "", fmt.Errorf("failed to locate %s in workspace", filePath)
	}
	content, err := os.ReadFile(resolved)
	if err != nil {
		return "", fmt.Errorf("failed to read %s: %w", resolved, err)
	}
	lines := strings.Split(string(content), "\n")
	if endLine > len(lines) {
		endLine = len(lines)
	}
	return strings.Join(lines[startLine-1:endLine], "\n"), nil
}
