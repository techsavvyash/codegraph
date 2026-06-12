package static

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/context-maximiser/code-graph/libs/core-models-go"
	"github.com/context-maximiser/code-graph/libs/neo4j-go"
)

// FrameworkPattern describes a known HTTP framework symbol pattern for detection.
type FrameworkPattern struct {
	PackagePattern string // SCIP package substring to match (e.g., "net/http")
	Descriptor     string // Descriptor substring to match (e.g., "HandleFunc")
	HTTPMethod     string // "GET", "POST", "ANY", etc.
	Type           string // "endpoint" or "client"
	Framework      string // "go-http", "gin", "express", etc.
}

// SymbolAnalyzer detects API endpoints and HTTP client calls by querying
// SCIP-indexed Symbol/Reference nodes rather than scanning source with regex.
type SymbolAnalyzer struct {
	client      *neo4j.Client
	serviceName string
	language    string
	projectPath string
	scopeCtx    models.ScopeContext
	patterns    []FrameworkPattern
}

// NewSymbolAnalyzer creates a new SymbolAnalyzer.
func NewSymbolAnalyzer(client *neo4j.Client, serviceName, language, projectPath string) *SymbolAnalyzer {
	sa := &SymbolAnalyzer{
		client:      client,
		serviceName: serviceName,
		language:    language,
		projectPath: projectPath,
		scopeCtx:    models.DefaultScope(),
	}
	sa.patterns = defaultFrameworkPatterns()
	return sa
}

// SetScope sets the scope context for the analyzer.
func (sa *SymbolAnalyzer) SetScope(scope models.ScopeContext) {
	sa.scopeCtx = scope
}

// Deprecated: defaultFrameworkPatterns is superseded by APISurfaceDetector which
// uses purely graph-structural signals. Kept as fallback for non-Go languages
// where AST param extraction isn't yet implemented.
func defaultFrameworkPatterns() []FrameworkPattern {
	return []FrameworkPattern{
		// ── Go standard library ──
		{PackagePattern: "net/http", Descriptor: "HandleFunc", HTTPMethod: "ANY", Type: "endpoint", Framework: "go-http"},
		{PackagePattern: "net/http", Descriptor: "Handle", HTTPMethod: "ANY", Type: "endpoint", Framework: "go-http"},
		{PackagePattern: "net/http", Descriptor: "ServeMux", HTTPMethod: "ANY", Type: "endpoint", Framework: "go-http"},
		{PackagePattern: "net/http", Descriptor: "ListenAndServe", HTTPMethod: "ANY", Type: "endpoint", Framework: "go-http"},
		// Go HTTP client
		{PackagePattern: "net/http", Descriptor: "NewRequest", HTTPMethod: "ANY", Type: "client", Framework: "go-http-client"},
		{PackagePattern: "net/http", Descriptor: "NewRequestWithContext", HTTPMethod: "ANY", Type: "client", Framework: "go-http-client"},
		{PackagePattern: "net/http", Descriptor: "Get", HTTPMethod: "GET", Type: "client", Framework: "go-http-client"},
		{PackagePattern: "net/http", Descriptor: "Post", HTTPMethod: "POST", Type: "client", Framework: "go-http-client"},
		{PackagePattern: "net/http", Descriptor: "Do", HTTPMethod: "ANY", Type: "client", Framework: "go-http-client"},

		// ── Gin ──
		{PackagePattern: "github.com/gin-gonic/gin", Descriptor: "GET", HTTPMethod: "GET", Type: "endpoint", Framework: "gin"},
		{PackagePattern: "github.com/gin-gonic/gin", Descriptor: "POST", HTTPMethod: "POST", Type: "endpoint", Framework: "gin"},
		{PackagePattern: "github.com/gin-gonic/gin", Descriptor: "PUT", HTTPMethod: "PUT", Type: "endpoint", Framework: "gin"},
		{PackagePattern: "github.com/gin-gonic/gin", Descriptor: "DELETE", HTTPMethod: "DELETE", Type: "endpoint", Framework: "gin"},
		{PackagePattern: "github.com/gin-gonic/gin", Descriptor: "PATCH", HTTPMethod: "PATCH", Type: "endpoint", Framework: "gin"},
		{PackagePattern: "github.com/gin-gonic/gin", Descriptor: "Handle", HTTPMethod: "ANY", Type: "endpoint", Framework: "gin"},

		// ── Echo ──
		{PackagePattern: "github.com/labstack/echo", Descriptor: "GET", HTTPMethod: "GET", Type: "endpoint", Framework: "echo"},
		{PackagePattern: "github.com/labstack/echo", Descriptor: "POST", HTTPMethod: "POST", Type: "endpoint", Framework: "echo"},
		{PackagePattern: "github.com/labstack/echo", Descriptor: "PUT", HTTPMethod: "PUT", Type: "endpoint", Framework: "echo"},
		{PackagePattern: "github.com/labstack/echo", Descriptor: "DELETE", HTTPMethod: "DELETE", Type: "endpoint", Framework: "echo"},

		// ── Gorilla Mux ──
		{PackagePattern: "github.com/gorilla/mux", Descriptor: "HandleFunc", HTTPMethod: "ANY", Type: "endpoint", Framework: "gorilla-mux"},
		{PackagePattern: "github.com/gorilla/mux", Descriptor: "Handle", HTTPMethod: "ANY", Type: "endpoint", Framework: "gorilla-mux"},

		// ── Express (TypeScript/JavaScript) ──
		{PackagePattern: "express", Descriptor: "get", HTTPMethod: "GET", Type: "endpoint", Framework: "express"},
		{PackagePattern: "express", Descriptor: "post", HTTPMethod: "POST", Type: "endpoint", Framework: "express"},
		{PackagePattern: "express", Descriptor: "put", HTTPMethod: "PUT", Type: "endpoint", Framework: "express"},
		{PackagePattern: "express", Descriptor: "delete", HTTPMethod: "DELETE", Type: "endpoint", Framework: "express"},
		{PackagePattern: "express", Descriptor: "patch", HTTPMethod: "PATCH", Type: "endpoint", Framework: "express"},

		// ── Elysia (TypeScript) ──
		{PackagePattern: "elysia", Descriptor: "get", HTTPMethod: "GET", Type: "endpoint", Framework: "elysia"},
		{PackagePattern: "elysia", Descriptor: "post", HTTPMethod: "POST", Type: "endpoint", Framework: "elysia"},
		{PackagePattern: "elysia", Descriptor: "put", HTTPMethod: "PUT", Type: "endpoint", Framework: "elysia"},
		{PackagePattern: "elysia", Descriptor: "delete", HTTPMethod: "DELETE", Type: "endpoint", Framework: "elysia"},
		{PackagePattern: "elysia", Descriptor: "patch", HTTPMethod: "PATCH", Type: "endpoint", Framework: "elysia"},

		// ── Hono (TypeScript) ──
		{PackagePattern: "hono", Descriptor: "get", HTTPMethod: "GET", Type: "endpoint", Framework: "hono"},
		{PackagePattern: "hono", Descriptor: "post", HTTPMethod: "POST", Type: "endpoint", Framework: "hono"},
		{PackagePattern: "hono", Descriptor: "put", HTTPMethod: "PUT", Type: "endpoint", Framework: "hono"},
		{PackagePattern: "hono", Descriptor: "delete", HTTPMethod: "DELETE", Type: "endpoint", Framework: "hono"},

		// ── Flask (Python) ──
		{PackagePattern: "flask", Descriptor: "route", HTTPMethod: "ANY", Type: "endpoint", Framework: "flask"},
		{PackagePattern: "flask", Descriptor: "add_url_rule", HTTPMethod: "ANY", Type: "endpoint", Framework: "flask"},

		// ── FastAPI (Python) ──
		{PackagePattern: "fastapi", Descriptor: "get", HTTPMethod: "GET", Type: "endpoint", Framework: "fastapi"},
		{PackagePattern: "fastapi", Descriptor: "post", HTTPMethod: "POST", Type: "endpoint", Framework: "fastapi"},
		{PackagePattern: "fastapi", Descriptor: "put", HTTPMethod: "PUT", Type: "endpoint", Framework: "fastapi"},
		{PackagePattern: "fastapi", Descriptor: "delete", HTTPMethod: "DELETE", Type: "endpoint", Framework: "fastapi"},

		// ── HTTP client libraries ──
		{PackagePattern: "axios", Descriptor: "get", HTTPMethod: "GET", Type: "client", Framework: "axios"},
		{PackagePattern: "axios", Descriptor: "post", HTTPMethod: "POST", Type: "client", Framework: "axios"},
		{PackagePattern: "axios", Descriptor: "put", HTTPMethod: "PUT", Type: "client", Framework: "axios"},
		{PackagePattern: "axios", Descriptor: "delete", HTTPMethod: "DELETE", Type: "client", Framework: "axios"},
		{PackagePattern: "axios", Descriptor: "patch", HTTPMethod: "PATCH", Type: "client", Framework: "axios"},

		{PackagePattern: "node-fetch", Descriptor: "default", HTTPMethod: "ANY", Type: "client", Framework: "node-fetch"},
		{PackagePattern: "requests", Descriptor: "get", HTTPMethod: "GET", Type: "client", Framework: "python-requests"},
		{PackagePattern: "requests", Descriptor: "post", HTTPMethod: "POST", Type: "client", Framework: "python-requests"},
	}
}

// symbolMatch holds a matched reference alongside its framework pattern.
type symbolMatch struct {
	Pattern     FrameworkPattern
	FilePath    string
	StartLine   int
	StartColumn int
	FunctionID  string // element ID of the containing Function/Method (if found)
}

// Deprecated: AnalyzeBySymbols is superseded by APISurfaceDetector.Detect() for
// Go projects. Kept as fallback for non-Go languages where AST param extraction
// isn't yet implemented.
//
// AnalyzeBySymbols queries the graph for references to known framework symbols,
// extracts route paths from source lines, and creates SDKCall nodes.
func (sa *SymbolAnalyzer) AnalyzeBySymbols(ctx context.Context) error {
	fmt.Println("Starting symbol-based API analysis...")

	// Collect unique package patterns to query
	packageSet := map[string]bool{}
	for _, p := range sa.patterns {
		packageSet[p.PackagePattern] = true
	}

	var allMatches []symbolMatch

	for pkg := range packageSet {
		matches, err := sa.queryReferencesForPackage(ctx, pkg)
		if err != nil {
			fmt.Printf("Warning: failed to query references for %s: %v\n", pkg, err)
			continue
		}
		allMatches = append(allMatches, matches...)
	}

	if len(allMatches) == 0 {
		fmt.Println("No framework symbol references found in graph.")
		return nil
	}

	fmt.Printf("Found %d framework symbol references, creating API nodes...\n", len(allMatches))

	endpointCount := 0
	clientCount := 0

	for _, m := range allMatches {
		routePath, sourceMethod := sa.extractRouteInfoFromSource(m.FilePath, m.StartLine)

		// If the source line contains an explicit method (Go 1.22+ ServeMux),
		// use it to override the pattern's generic method.
		if sourceMethod != "" && (m.Pattern.HTTPMethod == "" || m.Pattern.HTTPMethod == "ANY") {
			m.Pattern.HTTPMethod = sourceMethod
		}

		switch m.Pattern.Type {
		case "endpoint":
			// APIRoute creation suppressed — noise removed.
		case "client":
			if err := sa.createClientCallNode(ctx, m, routePath); err != nil {
				fmt.Printf("Warning: failed to create client call node: %v\n", err)
			} else {
				clientCount++
			}
		}
	}

	fmt.Printf("Symbol-based API analysis complete: %d endpoints, %d client calls\n", endpointCount, clientCount)
	return nil
}

// queryReferencesForPackage finds all Reference nodes that point at Symbols
// whose symbol string contains the given package substring, then matches
// against the descriptor patterns for that package.
func (sa *SymbolAnalyzer) queryReferencesForPackage(ctx context.Context, pkg string) ([]symbolMatch, error) {
	query := `
		MATCH (ref:Reference)-[:REFERENCES]->(sym:Symbol)
		WHERE sym.symbol CONTAINS $package
		  AND ref.scopeId = $scopeId
		RETURN ref.filePath AS filePath,
		       ref.startLine AS startLine,
		       ref.startColumn AS startColumn,
		       sym.symbol AS symbol
	`

	results, err := sa.client.ExecuteQuery(ctx, query, map[string]any{
		"package": pkg,
		"scopeId": sa.scopeCtx.ScopeID,
	})
	if err != nil {
		return nil, err
	}

	var matches []symbolMatch
	for _, rec := range results {
		rm := rec.AsMap()
		symStr := getStringFromMap(rm, "symbol")
		filePath := getStringFromMap(rm, "filePath")
		startLine := int(getInt64FromMap(rm, "startLine"))
		startCol := int(getInt64FromMap(rm, "startColumn"))

		for _, pat := range sa.patterns {
			if pat.PackagePattern != pkg {
				continue
			}
			if !strings.Contains(symStr, pat.Descriptor) {
				continue
			}
			matches = append(matches, symbolMatch{
				Pattern:     pat,
				FilePath:    filePath,
				StartLine:   startLine,
				StartColumn: startCol,
			})
			break // one pattern match per reference is enough
		}
	}

	return matches, nil
}

// routePathRegex extracts the first route path from a string literal in a function call.
// Handles both "/path" and "METHOD /path" (Go 1.22+ ServeMux) patterns.
// Group 1 = HTTP method (optional), Group 2 = path.
var routePathRegex = regexp.MustCompile(`[(\s,]["'` + "`" + `]((?:GET|POST|PUT|DELETE|PATCH|HEAD|OPTIONS)\s+)?(/[^"'` + "`" + `]*)["'` + "`" + `]`)

// extractRouteInfoFromSource reads the source line at startLine and tries to
// extract a route path and optional HTTP method from a string literal.
// Returns (path, method) where method may be empty.
func (sa *SymbolAnalyzer) extractRouteInfoFromSource(filePath string, startLine int) (string, string) {
	fullPath := filePath
	if !filepath.IsAbs(filePath) {
		fullPath = filepath.Join(sa.projectPath, filePath)
	}

	content, err := os.ReadFile(fullPath)
	if err != nil {
		return "", ""
	}

	lines := strings.Split(string(content), "\n")
	if startLine <= 0 || startLine > len(lines) {
		return "", ""
	}

	line := lines[startLine-1]
	match := routePathRegex.FindStringSubmatch(line)
	if len(match) > 2 {
		method := strings.TrimSpace(match[1])
		return match[2], method
	}
	return "", ""
}

// extractRoutePathFromSource is a convenience wrapper that returns just the path.
func (sa *SymbolAnalyzer) extractRoutePathFromSource(filePath string, startLine int) string {
	path, _ := sa.extractRouteInfoFromSource(filePath, startLine)
	return path
}

// createClientCallNode creates an SDKCall node and CALLS_API relationship.
func (sa *SymbolAnalyzer) createClientCallNode(ctx context.Context, m symbolMatch, routePath string) error {
	target := fmt.Sprintf("%s.%s", m.Pattern.Framework, m.Pattern.Descriptor)
	nodeKey := models.SDKCallNodeKey(target)
	callProps := map[string]any{
		"target":    target,
		"nodeKey":   nodeKey,
		"framework": m.Pattern.Framework,
		"type":      "http_client",
		"scope":     sa.scopeCtx.Scope,
		"scopeId":   sa.scopeCtx.ScopeID,
	}
	if routePath != "" {
		callProps["url"] = routePath
	}

	callID, err := sa.client.MergeNode(ctx, []string{"SDKCall"},
		map[string]any{"nodeKey": nodeKey, "scopeId": sa.scopeCtx.ScopeID}, callProps)
	if err != nil {
		return fmt.Errorf("failed to create SDKCall node: %w", err)
	}

	funcID, err := sa.findContainingFunction(ctx, m.FilePath, m.StartLine)
	if err != nil || funcID == "" {
		return nil
	}

	_, err = sa.client.MergeRelationship(ctx, funcID, callID, string(models.CallsAPIRel),
		map[string]any{"line": m.StartLine},
		map[string]any{"framework": m.Pattern.Framework})
	return err
}

// findContainingFunction returns the element ID of the Function or Method
// whose line range contains the given line number.
func (sa *SymbolAnalyzer) findContainingFunction(ctx context.Context, filePath string, line int) (string, error) {
	query := `
		MATCH (f)
		WHERE (f:Function OR f:Method)
		  AND f.filePath = $filePath
		  AND f.startLine <= $line
		  AND f.endLine >= $line
		  AND f.scopeId = $scopeId
		RETURN elementId(f) AS id
		ORDER BY (f.endLine - f.startLine) ASC
		LIMIT 1
	`

	results, err := sa.client.ExecuteQuery(ctx, query, map[string]any{
		"filePath": filePath,
		"line":     line,
		"scopeId":  sa.scopeCtx.ScopeID,
	})
	if err != nil || len(results) == 0 {
		return "", err
	}

	return results[0].AsMap()["id"].(string), nil
}
