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

	models "github.com/context-maximiser/code-graph/internal/model"
	"github.com/context-maximiser/code-graph/internal/graph"
	"github.com/context-maximiser/code-graph/internal/query"
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
	client        *neo4j.Client
	queryBuilder  *neo4j.QueryBuilder
	workspaceRoot string
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
		client:        client,
		queryBuilder:  neo4j.NewQueryBuilder(client),
		workspaceRoot: workspaceRoot,
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
						"type":    "string",
						"enum":    []string{"json", "text", "mermaid"},
						"default": "json",
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

	ctx := context.Background()
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

// handleRenderTool runs a read-only Cypher query and writes a standalone
// cytoscape.js HTML page containing every node and relationship in the result.
// Same read-only enforcement as handleCypherTool (regex pre-check + read-only
// transaction + caps), but row_limit and timeout caps are higher because
// visualization typically wants more of the graph than a JSON answer.
func (s *CodeGraphMCPServer) handleRenderTool(parentCtx context.Context, args map[string]interface{}) ToolCallResponse {
	query, _ := args["query"].(string)
	query = strings.TrimSpace(query)
	if query == "" {
		return errorResponse("render: query is required")
	}

	stripped := stripCypherComments(query)
	if writeKeywordRegex.MatchString(stripped) {
		return errorResponse("render: write keywords (CREATE/MERGE/DELETE/SET/REMOVE/DROP/FOREACH/LOAD CSV) are not allowed.")
	}

	timeoutMs := 5000
	if t, ok := args["timeout_ms"].(float64); ok {
		timeoutMs = int(t)
	}
	if timeoutMs < 100 {
		timeoutMs = 100
	}
	if timeoutMs > 10000 {
		timeoutMs = 10000
	}

	rowLimit := 1000
	if r, ok := args["row_limit"].(float64); ok {
		rowLimit = int(r)
	}
	if rowLimit < 1 {
		rowLimit = 1000
	}
	if rowLimit > 5000 {
		rowLimit = 5000
	}

	layout, _ := args["layout"].(string)
	if layout == "" {
		layout = "fcose"
	}
	switch layout {
	case "fcose", "cose", "concentric", "circle", "grid", "breadthfirst":
	default:
		return errorResponse(fmt.Sprintf("render: unsupported layout %q", layout))
	}

	title, _ := args["title"].(string)
	if title == "" {
		title = "codegraph render"
	}

	outPath, _ := args["out_path"].(string)
	if outPath == "" {
		outPath = fmt.Sprintf("/tmp/codegraph-render-%d.html", time.Now().Unix())
	}
	if !strings.HasSuffix(outPath, ".html") {
		return errorResponse("render: out_path must end in .html")
	}

	ctx, cancel := context.WithTimeout(parentCtx, time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	type renderNode struct {
		ID    string                 `json:"id"`
		Label string                 `json:"label"`
		Name  string                 `json:"name"`
		Props map[string]interface{} `json:"props"`
	}
	type renderEdge struct {
		ID     string `json:"id"`
		Source string `json:"source"`
		Target string `json:"target"`
		Type   string `json:"type"`
	}
	type renderData struct {
		nodes     map[string]renderNode
		edges     map[string]renderEdge
		truncated bool
	}

	collect := func(v interface{}, data *renderData) {
		switch x := v.(type) {
		case dbtype.Node:
			label := ""
			if len(x.Labels) > 0 {
				label = x.Labels[0]
			}
			name := ""
			if n, ok := x.Props["name"].(string); ok {
				name = n
			} else if p, ok := x.Props["path"].(string); ok {
				name = p
			}
			data.nodes[x.ElementId] = renderNode{
				ID:    x.ElementId,
				Label: label,
				Name:  name,
				Props: x.Props,
			}
		case dbtype.Relationship:
			data.edges[x.ElementId] = renderEdge{
				ID:     x.ElementId,
				Source: x.StartElementId,
				Target: x.EndElementId,
				Type:   x.Type,
			}
		}
	}

	result, err := s.client.ExecuteRead(ctx, func(tx neo4jdriver.ManagedTransaction) (any, error) {
		res, err := tx.Run(ctx, query, map[string]any{})
		if err != nil {
			return nil, err
		}
		keys, _ := res.Keys()
		out := &renderData{nodes: map[string]renderNode{}, edges: map[string]renderEdge{}}
		rows := 0
		for res.Next(ctx) {
			if rows >= rowLimit {
				out.truncated = true
				break
			}
			rows++
			rec := res.Record()
			for _, k := range keys {
				v, _ := rec.Get(k)
				switch vv := v.(type) {
				case []interface{}:
					for _, item := range vv {
						collect(item, out)
					}
				default:
					collect(v, out)
				}
			}
		}
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
			return errorResponse(fmt.Sprintf("render: query timed out after %dms", timeoutMs))
		}
		return errorResponse(fmt.Sprintf("render: %v", err))
	}

	data, _ := result.(*renderData)
	if data == nil {
		return errorResponse("render: internal error (nil result)")
	}

	nodes := make([]renderNode, 0, len(data.nodes))
	for _, n := range data.nodes {
		nodes = append(nodes, n)
	}
	edges := make([]renderEdge, 0, len(data.edges))
	for _, e := range data.edges {
		// Drop dangling edges whose endpoints didn't make it into the node set.
		if _, ok := data.nodes[e.Source]; !ok {
			continue
		}
		if _, ok := data.nodes[e.Target]; !ok {
			continue
		}
		edges = append(edges, e)
	}

	labelCounts := map[string]int{}
	for _, n := range nodes {
		labelCounts[n.Label]++
	}
	typeCounts := map[string]int{}
	for _, e := range edges {
		typeCounts[e.Type]++
	}

	nodesJSON, _ := json.Marshal(nodes)
	edgesJSON, _ := json.Marshal(edges)

	html := buildRenderHTML(title, layout, string(nodesJSON), string(edgesJSON))
	if err := os.WriteFile(outPath, []byte(html), 0644); err != nil {
		return errorResponse(fmt.Sprintf("render: write %s: %v", outPath, err))
	}

	summary := map[string]interface{}{
		"file_path":   outPath,
		"node_count":  len(nodes),
		"edge_count":  len(edges),
		"node_labels": labelCounts,
		"rel_types":   typeCounts,
		"truncated":   data.truncated,
		"layout":      layout,
		"hint":        fmt.Sprintf("open file://%s in a browser", outPath),
	}
	body, _ := json.MarshalIndent(summary, "", "  ")
	return ToolCallResponse{Content: []ToolContent{{Type: "text", Text: string(body)}}}
}

// buildRenderHTML produces a self-contained cytoscape.js page. The JS strings
// are JSON-encoded element arrays inlined into the template.
func buildRenderHTML(title, layout, nodesJSON, edgesJSON string) string {
	const tmpl = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>{{TITLE}}</title>
<style>
  html,body { margin:0; padding:0; height:100%; font-family: -apple-system, system-ui, sans-serif; background:#0f1117; color:#e6e6e6; }
  #app { display:flex; height:100%; }
  #cy { flex:1; background:#0f1117; }
  #side { width:360px; background:#171a23; border-left:1px solid #2a2f3a; padding:14px 16px; overflow:auto; }
  #side h2 { margin:0 0 8px; font-size:13px; font-weight:600; letter-spacing:.02em; text-transform:uppercase; color:#8a93a6; }
  #side .muted { color:#8a93a6; font-size:12px; }
  #legend { margin-top:8px; }
  #legend .row { display:flex; align-items:center; gap:8px; margin:4px 0; font-size:12px; }
  #legend .sw { width:12px; height:12px; border-radius:3px; }
  #info { font-size:12px; }
  #info pre { background:#0f1117; padding:8px; border-radius:4px; overflow:auto; max-height:280px; font-size:11px; line-height:1.4; }
  #controls { margin-top:14px; display:flex; flex-direction:column; gap:6px; }
  #controls button, #controls select { background:#222836; color:#e6e6e6; border:1px solid #2a2f3a; padding:6px 10px; border-radius:4px; font-size:12px; cursor:pointer; }
  #controls button:hover, #controls select:hover { background:#2a2f3a; }
  #header { padding:10px 14px; border-bottom:1px solid #2a2f3a; display:flex; justify-content:space-between; align-items:baseline; }
  #header h1 { margin:0; font-size:14px; font-weight:600; }
  #stats { font-size:11px; color:#8a93a6; }
  .section { margin-bottom:18px; }
</style>
<script src="https://unpkg.com/cytoscape@3.30.1/dist/cytoscape.min.js"></script>
<script src="https://unpkg.com/layout-base/layout-base.js"></script>
<script src="https://unpkg.com/cose-base/cose-base.js"></script>
<script src="https://unpkg.com/cytoscape-fcose/cytoscape-fcose.js"></script>
</head>
<body>
<div id="app">
  <div style="flex:1; display:flex; flex-direction:column;">
    <div id="header"><h1>{{TITLE}}</h1><div id="stats"></div></div>
    <div id="cy"></div>
  </div>
  <div id="side">
    <div class="section">
      <h2>Inspector</h2>
      <div id="info"><div class="muted">Click a node or edge.</div></div>
    </div>
    <div class="section">
      <h2>Controls</h2>
      <div id="controls">
        <select id="layout">
          <option value="fcose">fcose (force) — default</option>
          <option value="cose">cose</option>
          <option value="concentric">concentric (by degree)</option>
          <option value="circle">circle</option>
          <option value="grid">grid</option>
          <option value="breadthfirst">breadthfirst</option>
        </select>
        <button id="fit">Fit to view</button>
        <button id="reset">Reset zoom</button>
        <button id="labels">Toggle edge labels</button>
      </div>
    </div>
    <div class="section">
      <h2>Legend</h2>
      <div id="legend"></div>
    </div>
  </div>
</div>
<script>
  if (typeof cytoscape !== "undefined" && typeof cytoscapeFcose !== "undefined") {
    cytoscape.use(cytoscapeFcose);
  }

  const nodes = {{NODES_JSON}};
  const edges = {{EDGES_JSON}};
  const initialLayout = {{LAYOUT}};

  // Strip the longest common path prefix from all node names so labels stay
  // legible when (e.g.) every Service starts with "codegraph/".
  function commonPrefix(strs) {
    if (strs.length < 2) return "";
    let p = strs[0];
    for (const s of strs) { while (!s.startsWith(p)) p = p.slice(0, -1); if (!p) return ""; }
    const cut = p.lastIndexOf("/");
    return cut >= 0 ? p.slice(0, cut + 1) : "";
  }
  const allNames = nodes.map(n => n.name || "").filter(Boolean);
  const prefix = commonPrefix(allNames);
  function shortName(n) {
    let s = n.name || n.id;
    if (prefix && s.startsWith(prefix)) s = s.slice(prefix.length);
    if (s.length > 32) s = s.slice(0, 30) + "…";
    return s;
  }

  // Choose color axis. Multi-label graphs: color by label. Single-label graphs:
  // color by degree tier (orphan / low / mid / high) so structure isn't lost.
  const labels = [...new Set(nodes.map(n => n.label || "Node"))];
  const palette = ["#4c8bf5","#ef5b9c","#f0b400","#26c6da","#9ccc65","#ab47bc","#ff7043","#26a69a","#7e57c2","#ec407a","#5c6bc0","#d4e157"];
  const labelColors = {};
  labels.forEach((l,i) => labelColors[l] = palette[i % palette.length]);

  // Pre-compute degree per node id for sizing (and the single-label color tier).
  const degree = {};
  for (const n of nodes) degree[n.id] = 0;
  for (const e of edges) { degree[e.source] = (degree[e.source]||0)+1; degree[e.target] = (degree[e.target]||0)+1; }
  const maxDeg = Math.max(1, ...Object.values(degree));

  const singleLabel = labels.length === 1;
  const tierColors = ["#3a4256","#5c6bc0","#26a69a","#f0b400","#ef5b9c"]; // 0 = orphan, 4 = hub
  function tierFor(d) {
    if (d === 0) return 0;
    if (d <= 2) return 1;
    if (d <= 5) return 2;
    if (d <= 10) return 3;
    return 4;
  }
  function colorFor(node) {
    if (singleLabel) return tierColors[tierFor(degree[node.id] || 0)];
    return labelColors[node.label || "Node"];
  }

  const elements = [];
  for (const n of nodes) {
    elements.push({ data: {
      id: n.id, label: n.label || "Node",
      name: n.name || n.id, short: shortName(n),
      degree: degree[n.id] || 0,
      color: colorFor(n),
      props: n.props || {}
    }});
  }
  for (const e of edges) {
    elements.push({ data: { id: e.id, source: e.source, target: e.target, label: e.type } });
  }

  // Size: log-scaled by degree, dramatic enough to read structurally
  function sizeFor(d) { return 22 + Math.round(Math.sqrt(d) * 12); }

  const cy = cytoscape({
    container: document.getElementById("cy"),
    elements,
    wheelSensitivity: 0.2,
    minZoom: 0.1,
    maxZoom: 4,
    style: [
      { selector: "node", style: {
        "background-color": "data(color)",
        "label": "data(short)",
        "font-size": 11,
        "font-weight": 500,
        "color": "#ffffff",
        "text-outline-color": "#0f1117",
        "text-outline-width": 3,
        "text-valign": "center",
        "text-halign": "center",
        "text-wrap": "ellipsis",
        "text-max-width": 160,
        "width":  ele => sizeFor(ele.data("degree")),
        "height": ele => sizeFor(ele.data("degree")),
        "border-color": "#0f1117",
        "border-width": 2
      }},
      { selector: "edge", style: {
        "width": 1.2,
        "line-color": "#3a4256",
        "target-arrow-color": "#5a6378",
        "target-arrow-shape": "triangle",
        "arrow-scale": 0.9,
        "curve-style": "bezier",
        "label": "data(label)",
        "font-size": 8,
        "color": "#6a7388",
        "text-rotation": "autorotate",
        "text-background-color": "#0f1117",
        "text-background-opacity": 0.85,
        "text-background-padding": 2,
        "opacity": 0.65
      }},
      { selector: "node:selected", style: { "border-color": "#fff", "border-width": 3 } },
      { selector: "edge:selected", style: { "line-color": "#fff", "target-arrow-color": "#fff", "opacity": 1, "width": 2 } },
      { selector: ".dim", style: { "opacity": 0.15 } }
    ],
    layout: layoutOpts(initialLayout)
  });

  function layoutOpts(name) {
    const base = { name, animate: true, animationDuration: 500, fit: true, padding: 40 };
    if (name === "fcose") {
      return Object.assign(base, {
        quality: "proof",
        randomize: true,
        nodeRepulsion: 9000,
        idealEdgeLength: 140,
        edgeElasticity: 0.45,
        nestingFactor: 0.1,
        gravity: 0.25,
        gravityRange: 3.0,
        numIter: 2500,
        tile: true,             // pack disconnected components into a grid
        tilingPaddingHorizontal: 30,
        tilingPaddingVertical: 30
      });
    }
    if (name === "cose") {
      return Object.assign(base, { nodeRepulsion: 8000, idealEdgeLength: 120, gravity: 0.25, numIter: 1800, componentSpacing: 80 });
    }
    if (name === "concentric") {
      return Object.assign(base, { concentric: n => n.data("degree"), levelWidth: () => 1, minNodeSpacing: 30 });
    }
    return base;
  }

  // If the requested initial layout is fcose but plugin failed to load, fall back.
  const requested = (typeof cytoscape !== "undefined" && cy.layout({name: initialLayout}).options) ? initialLayout : "cose";
  cy.layout(layoutOpts(requested)).run();

  // Header stats + legend
  document.getElementById("stats").textContent =
    nodes.length + " nodes • " + edges.length + " edges" + (prefix ? " • prefix “" + prefix + "” hidden" : "");
  const legend = document.getElementById("legend");
  if (singleLabel) {
    const tiers = [
      ["orphan (0)",     0],
      ["1-2 edges",      1],
      ["3-5 edges",      2],
      ["6-10 edges",     3],
      ["11+ edges (hub)",4],
    ];
    for (const [name, t] of tiers) {
      const row = document.createElement("div");
      row.className = "row";
      row.innerHTML = '<span class="sw" style="background:'+tierColors[t]+'"></span><span>'+name+'</span>';
      legend.appendChild(row);
    }
  } else {
    for (const lbl of labels) {
      const count = nodes.filter(n => (n.label||"Node") === lbl).length;
      const row = document.createElement("div");
      row.className = "row";
      row.innerHTML = '<span class="sw" style="background:'+labelColors[lbl]+'"></span><span>'+lbl+'</span><span class="muted">('+count+')</span>';
      legend.appendChild(row);
    }
  }

  // Inspector
  cy.on("tap", "node", evt => {
    const d = evt.target.data();
    const props = JSON.stringify(d.props, null, 2);
    document.getElementById("info").innerHTML =
      "<div><strong>" + escapeHTML(d.label) + "</strong></div>" +
      "<div style='margin:4px 0 8px; word-break:break-all;'>" + escapeHTML(d.name) + "</div>" +
      "<div class='muted'>degree " + d.degree + "</div>" +
      "<pre>" + escapeHTML(props) + "</pre>";
    // dim the rest
    cy.elements().addClass("dim");
    const ego = evt.target.closedNeighborhood();
    ego.removeClass("dim");
  });
  cy.on("tap", "edge", evt => {
    const d = evt.target.data();
    document.getElementById("info").innerHTML =
      "<div><strong>" + escapeHTML(d.label) + "</strong></div>" +
      "<div class='muted'>" + escapeHTML(d.source) + " → " + escapeHTML(d.target) + "</div>";
  });
  cy.on("tap", evt => {
    if (evt.target === cy) {
      document.getElementById("info").innerHTML = '<div class="muted">Click a node or edge.</div>';
      cy.elements().removeClass("dim");
    }
  });

  document.getElementById("layout").value = requested;
  document.getElementById("layout").addEventListener("change", e => cy.layout(layoutOpts(e.target.value)).run());
  document.getElementById("fit").addEventListener("click", () => cy.fit(null, 40));
  document.getElementById("reset").addEventListener("click", () => { cy.fit(null, 40); cy.elements().removeClass("dim"); });
  let edgeLabels = true;
  document.getElementById("labels").addEventListener("click", () => {
    edgeLabels = !edgeLabels;
    cy.style().selector("edge").style("label", edgeLabels ? "data(label)" : "").update();
  });

  function escapeHTML(s) { return String(s).replace(/[&<>"']/g, c => ({"&":"&amp;","<":"&lt;",">":"&gt;","\"":"&quot;","'":"&#39;"}[c])); }
</script>
</body>
</html>`
	out := strings.ReplaceAll(tmpl, "{{TITLE}}", htmlEscape(title))
	out = strings.ReplaceAll(out, "{{NODES_JSON}}", nodesJSON)
	out = strings.ReplaceAll(out, "{{EDGES_JSON}}", edgesJSON)
	layoutJSON, _ := json.Marshal(layout)
	out = strings.ReplaceAll(out, "{{LAYOUT}}", string(layoutJSON))
	return out
}

func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;", "'", "&#39;")
	return r.Replace(s)
}
