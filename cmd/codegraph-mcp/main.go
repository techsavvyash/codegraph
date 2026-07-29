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
	"strconv"
	"strings"
	"sync"
	"time"

	neo4j "github.com/context-maximiser/code-graph/internal/graph"
	"github.com/context-maximiser/code-graph/internal/llm"
	models "github.com/context-maximiser/code-graph/internal/model"
	"github.com/context-maximiser/code-graph/internal/query"
	"github.com/joho/godotenv"
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
	client          *neo4j.Client
	queryBuilder    *neo4j.QueryBuilder
	workspaceRoot   string
	embedder        llm.Embedder // nil unless CODEGRAPH_EMBED_* env is set; gates find's semantic mode
	schemaCacheMu   sync.Mutex
	schemaCache     map[string]any
	schemaCacheTime time.Time
	schemaCacheTTL  time.Duration
	now             func() time.Time
}

// embedderFromEnv builds the semantic-search embedder from environment
// variables (the MCP server is env-configured, unlike the viper-based CLI):
// CODEGRAPH_EMBED_BASE_URL, CODEGRAPH_EMBED_MODEL, CODEGRAPH_EMBED_DIMENSIONS,
// and optionally CODEGRAPH_EMBED_API_KEY. Returns nil when unconfigured —
// semantic find then reports a clear configuration error.
func embedderFromEnv() llm.Embedder {
	baseURL := os.Getenv("CODEGRAPH_EMBED_BASE_URL")
	model := os.Getenv("CODEGRAPH_EMBED_MODEL")
	if baseURL == "" || model == "" {
		return nil
	}
	dims, err := strconv.Atoi(os.Getenv("CODEGRAPH_EMBED_DIMENSIONS"))
	if err != nil || dims <= 0 {
		log.Printf("Warning: CODEGRAPH_EMBED_DIMENSIONS missing/invalid; semantic search disabled")
		return nil
	}
	keyEnv := ""
	if os.Getenv("CODEGRAPH_EMBED_API_KEY") != "" {
		keyEnv = "CODEGRAPH_EMBED_API_KEY"
	}
	_, embedder, err := llm.New(llm.Config{
		Provider: "openai-compat",
		Embedding: llm.EndpointConfig{
			BaseURL: baseURL, Model: model, Dimensions: dims, APIKeyEnv: keyEnv,
		},
	})
	if err != nil {
		log.Printf("Warning: embedding provider misconfigured (%v); semantic search disabled", err)
		return nil
	}
	log.Printf("Semantic search enabled: %s (%d dims)", model, dims)
	return embedder
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

	workspaceRoot, err := os.Getwd()
	if err != nil {
		workspaceRoot = "."
	}

	server := &CodeGraphMCPServer{
		client:         client,
		queryBuilder:   neo4j.NewQueryBuilder(client),
		workspaceRoot:  workspaceRoot,
		embedder:       embedderFromEnv(),
		schemaCache:    make(map[string]any),
		schemaCacheTTL: 300 * time.Second,
		now:            time.Now,
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
			Name:        "codegraph_entry_points",
			Description: "List structurally-detected entry points (4-tier classification: API-exposed, interface impls with no callers, exported topological roots, high-centrality functions). Same algorithm as codegraph_get_entry_points but with format=json|text|mermaid. format=json entries also carry node_id (elementId), label, start_line, out_degree, in_degree. Passing service_name bypasses workspace-cwd filtering (explicit scoping replaces it).",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"tier":         map[string]interface{}{"type": "number", "description": "Restrict to a single tier (1-4); omit for all"},
					"limit":        map[string]interface{}{"type": "number", "description": "Max entries (default 50)", "default": 50},
					"scope_id":     map[string]interface{}{"type": "string", "description": "Scope ID (default: main)", "default": "main"},
					"service_name": map[string]interface{}{"type": "string", "description": "Optional service filter; also bypasses workspace-cwd relevance filtering"},
					"format":       map[string]interface{}{"type": "string", "enum": []string{"json", "text", "mermaid"}, "default": "json"},
				},
			},
		},
		{
			Name:        "codegraph_flows",
			Description: "Generate flow spines. Default: discover entry points via the multi-strategy seed finder and trace each (workspace-filtered, unless service_name is given). With from/from_name: generate one flow anchored at that specific node (name-or-id addressing; ambiguous names return a candidates list). format=json|text|mermaid; format=json steps carry depth, parentKey (spanning-tree parent nodeKey, omitted on the entry step), nodeId (elementId), filePath, startLine. format=json with zero flows returns {\"flow_count\":0,\"flows\":[]}.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"max_depth":    map[string]interface{}{"type": "number", "description": "Traversal depth per flow (default 5)", "default": 5},
					"limit":        map[string]interface{}{"type": "number", "description": "Max flows returned (default 20)", "default": 20},
					"scope_id":     map[string]interface{}{"type": "string", "default": "main"},
					"service_name": map[string]interface{}{"type": "string", "description": "Optional service filter; also bypasses workspace-cwd relevance filtering in discovery mode"},
					"format":       map[string]interface{}{"type": "string", "enum": []string{"json", "text", "mermaid"}, "default": "json"},
					"from":         map[string]interface{}{"type": "string", "description": "Node elementId to anchor a single flow at (from expand/path/find results)"},
					"from_name":    map[string]interface{}{"type": "string", "description": "Function/Method name (or File path) to anchor a single flow at; ambiguous matches return candidates"},
					"from_label":   map[string]interface{}{"type": "string", "description": "Optional label to disambiguate from_name (e.g. Function, Method)"},
					"from_service": map[string]interface{}{"type": "string", "description": "Optional serviceName to disambiguate from_name"},
					"persist":      map[string]interface{}{"type": "boolean", "description": "Persist the generated flow as a Flow/HAS_STEP graph node (default true). Set false for read-only/interactive tracing to avoid polluting the graph.", "default": true},
				},
			},
		},
		{
			Name:        "codegraph_cypher",
			Description: "Advanced/escape-hatch tool: run a read-only Cypher query directly against Neo4j. Prefer find/expand/path/source for common questions. Write operations (CREATE, MERGE, DELETE, SET, REMOVE, DROP, FOREACH, LOAD CSV) are rejected. Queries are EXPLAIN-validated first; plans containing AllNodesScan get a warning. Hard caps: timeout 120000ms, 1000 rows.",
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
						"description": "Query timeout in milliseconds (default 10000, clamped to [100, 120000]; accepted by every codegraph tool)",
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
						"description": "Opaque node_id of the starting node (from find/expand). Alternative: from_name.",
					},
					"to_id": map[string]interface{}{
						"type":        "string",
						"description": "Opaque node_id of the destination node. Alternative: to_name.",
					},
					"from_name": map[string]interface{}{
						"type":        "string",
						"description": "Exact name of the starting node as an alternative to from_id (disambiguate with from_label/from_service)",
					},
					"from_label": map[string]interface{}{
						"type":        "string",
						"description": "Optional label filter for from_name resolution",
					},
					"from_service": map[string]interface{}{
						"type":        "string",
						"description": "Optional service filter for from_name resolution",
					},
					"to_name": map[string]interface{}{
						"type":        "string",
						"description": "Exact name of the destination node as an alternative to to_id (disambiguate with to_label/to_service)",
					},
					"to_label": map[string]interface{}{
						"type":        "string",
						"description": "Optional label filter for to_name resolution",
					},
					"to_service": map[string]interface{}{
						"type":        "string",
						"description": "Optional service filter for to_name resolution",
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
						"type":    "string",
						"enum":    []string{"json", "text", "mermaid"},
						"default": "json",
					},
				},
				"required": []string{"rel_types"},
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
					"format": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"markdown", "json"},
						"description": "Response format: markdown code block (default) or structured json (kind/name/lang/source/...) for UI consumption",
						"default":     "markdown",
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
						"description": "Opaque node_id from a previous find/expand call. Alternative: address by name.",
					},
					"name": map[string]interface{}{
						"type":        "string",
						"description": "Exact node name as an alternative to node_id. Ambiguous matches return a candidates list to pick from.",
					},
					"label": map[string]interface{}{
						"type":        "string",
						"description": "Optional label filter when addressing by name (e.g. Function, Method)",
					},
					"service_name": map[string]interface{}{
						"type":        "string",
						"description": "Optional service filter when addressing by name",
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
						"description": "Max hops (default 3, hard cap 10)",
						"default":     3,
					},
					"max_nodes": map[string]interface{}{
						"type":        "number",
						"description": "Max nodes returned including the start node (default 500, hard cap 2000); sets truncated when cut",
						"default":     500,
					},
					"format": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"json", "text", "mermaid"},
						"description": "Response format (default json)",
						"default":     "json",
					},
				},
				"required": []string{"rel_types"},
			},
		},
		{
			Name:        "codegraph_find",
			Description: "Find nodes in the code graph. With `query`: relevance-ranked fulltext search (RRF fusion across per-label indexes, exact-name matches first). With only `label`: structural listing ordered by name. Both paginate via next_cursor. Each result carries a node_id usable as input to expand/path. Searchable labels: Function, Method, Class, Interface, Symbol, File, Variable, Document, DocumentChunk. Set `semantic: true` to additionally rank by embedding similarity over doc chunks and code summaries (requires an embedding provider on the server).",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{
						"type":        "string",
						"description": "Fulltext search text (name/signature/path; single words prefix-match). Omit for pure structural listing by label.",
					},
					"label": map[string]interface{}{
						"type":        "string",
						"description": "Restrict to one label: Function, Method, Class, Interface, Symbol, File, Variable. Required when query is omitted.",
					},
					"service": map[string]interface{}{
						"type":        "string",
						"description": "Filter to nodes owned by a specific service (e.g. codegraph/web/chat-ui)",
					},
					"scope_id": map[string]interface{}{
						"type":        "string",
						"description": "Scope ID (default: main)",
						"default":     "main",
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
					"semantic": map[string]interface{}{
						"type":        "boolean",
						"description": "Fuse in a vector-similarity ranking over document chunks and code summaries (RFC-011). Errors if the server has no embedding provider configured.",
						"default":     false,
					},
				},
			},
		},
		{
			Name:        "codegraph_schema",
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
		{
			Name:        "codegraph_render",
			Description: "Run a read-only Cypher query and emit a standalone interactive HTML page (cytoscape.js force-directed layout) showing the resulting nodes and edges. Use for whole-graph or large-subgraph visualization (e.g. all services + DEPENDS_ON; the full CALLS graph for one service) where mermaid in expand/path/flows would be too cluttered. Pan/zoom/click in a browser. The query must RETURN nodes and relationships (e.g. `MATCH (a)-[r]->(b) RETURN a, r, b`); scalar columns are ignored.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{
						"type":        "string",
						"description": "Cypher query returning nodes/relationships. Same read-only caps as codegraph_cypher.",
					},
					"out_path": map[string]interface{}{
						"type":        "string",
						"description": "Path to write the HTML file (default: /tmp/codegraph-render-<unix>.html). Must end in .html.",
					},
					"layout": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"fcose", "cose", "concentric", "circle", "grid", "breadthfirst"},
						"description": "Cytoscape layout (default: fcose — packs disconnected components into a grid, much better than cose for orphan detection)",
						"default":     "fcose",
					},
					"row_limit": map[string]interface{}{
						"type":        "number",
						"description": "Max query rows (default 1000, hard cap 5000)",
						"default":     1000,
					},
					"timeout_ms": map[string]interface{}{
						"type":        "number",
						"description": "Query timeout in ms (default 5000, hard cap 10000)",
						"default":     5000,
					},
					"title": map[string]interface{}{
						"type":        "string",
						"description": "Page title shown at the top (default: \"codegraph render\")",
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

	// Parse and clamp timeout_ms from tool arguments
	timeoutMs := parseTimeoutMs(toolCall.Arguments)

	// Wrap context with timeout bound to the handler
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	var response ToolCallResponse

	switch toolCall.Name {
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
	case "codegraph_render":
		response = s.handleRenderTool(ctx, toolCall.Arguments)
	default:
		s.sendError(request.ID, -32601, "Unknown tool")
		return
	}

	// If the per-tool deadline fired, replace whatever the handler surfaced
	// (usually a raw driver context error) with a clean, actionable message.
	if ctx.Err() == context.DeadlineExceeded && response.IsError {
		response = errorResponse(fmt.Sprintf(
			"%s: timed out after %dms — pass a larger timeout_ms (max 120000) or narrow the query",
			toolCall.Name, timeoutMs))
	}

	s.sendResponse(request.ID, response)
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

// parseTimeoutMs extracts and clamps the timeout_ms argument (default 10000, range [100, 120000])
func parseTimeoutMs(args map[string]interface{}) int {
	const defaultTimeoutMs = 10000
	const minTimeoutMs = 100
	const maxTimeoutMs = 120000

	if args == nil {
		return defaultTimeoutMs
	}

	if t, ok := args["timeout_ms"].(float64); ok {
		timeoutMs := int(t)
		if timeoutMs < minTimeoutMs {
			return minTimeoutMs
		}
		if timeoutMs > maxTimeoutMs {
			return maxTimeoutMs
		}
		return timeoutMs
	}
	return defaultTimeoutMs
}

// serviceFilterClause filters by the node's own serviceName property (set at
// creation by the indexer) instead of an EXISTS Service-CONTAINS*1..3
// traversal per row — RFC-006 Phase 2 item 3. Prefix matching covers polyglot
// sub-services ("codegraph" also matches "codegraph/web/chat-ui").
func serviceFilterClause(nodeVar string) string {
	return fmt.Sprintf(`
                  AND (size($serviceNames) = 0
                       OR any(svc IN $serviceNames WHERE %[1]s.serviceName = svc OR %[1]s.serviceName STARTS WITH svc + '/'))`, nodeVar)
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
