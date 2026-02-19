package static

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/context-maximiser/code-graph/pkg/models"
	"github.com/context-maximiser/code-graph/pkg/neo4j"
)

// APIAnalyzer scans indexed code for API patterns and creates cross-service relationships
type APIAnalyzer struct {
	client      *neo4j.Client
	serviceName string
	language    string
	projectPath string // Base path for resolving relative file paths
}

// NewAPIAnalyzer creates a new API analyzer
func NewAPIAnalyzer(client *neo4j.Client, serviceName, language, projectPath string) *APIAnalyzer {
	return &APIAnalyzer{
		client:      client,
		serviceName: serviceName,
		language:    language,
		projectPath: projectPath,
	}
}

// APIPattern represents a detected API pattern
type APIPattern struct {
	Type       string // "endpoint" or "call"
	Method     string // HTTP method
	Path       string // API path
	Framework  string // "express", "elysia", "axios", "fetch", etc.
	Line       int
	SourceCode string
}

// AnalyzeAPIPatterns scans all functions/methods for API patterns
func (aa *APIAnalyzer) AnalyzeAPIPatterns(ctx context.Context, fileNodes map[string]string) error {
	fmt.Println("Starting API pattern analysis...")

	// New approach: analyze entire files instead of individual functions
	// This works better when byte offsets aren't available (TypeScript/SCIP)
	return aa.analyzeFilesByPattern(ctx, fileNodes)
}

// analyzeFilesByPattern scans entire files for API patterns
func (aa *APIAnalyzer) analyzeFilesByPattern(ctx context.Context, fileNodes map[string]string) error {
	// Get all unique file paths from the service
	query := `
		MATCH (s:Service {name: $serviceName})-[:CONTAINS*]->(file:File)
		RETURN DISTINCT file.path AS filePath, elementId(file) AS fileID
	`

	results, err := aa.client.ExecuteQuery(ctx, query, map[string]any{"serviceName": aa.serviceName})
	if err != nil {
		return fmt.Errorf("failed to query files: %w", err)
	}

	fmt.Printf("Analyzing %d files for API patterns...\n", len(results))

	endpointCount := 0
	callCount := 0

	for i, record := range results {
		if i%10 == 0 && i > 0 {
			fmt.Printf("Processed %d/%d files...\n", i, len(results))
		}

		recMap := record.AsMap()
		filePath := getStringFromMap(recMap, "filePath")
		fileID := getStringFromMap(recMap, "fileID")

		// Resolve full path (SCIP stores relative paths)
		fullPath := filePath
		if !filepath.IsAbs(filePath) {
			fullPath = filepath.Join(aa.projectPath, filePath)
		}

		// Read entire file
		sourceCode, err := aa.readEntireFile(fullPath)
		if err != nil {
			fmt.Printf("Warning: failed to read file %s: %v\n", filePath, err)
			continue
		}

		// Detect API endpoints in file
		endpoints := aa.detectAPIEndpoints(sourceCode)
		for _, endpoint := range endpoints {
			// Find function containing this pattern by line number
			functionID, err := aa.findFunctionAtLine(ctx, filePath, endpoint.Line)
			if err != nil || functionID == "" {
				// No specific function found, link to file instead
				functionID = fileID
			}

			if err := aa.createAPIEndpoint(ctx, functionID, endpoint); err != nil {
				fmt.Printf("Warning: failed to create API endpoint: %v\n", err)
			} else {
				endpointCount++
			}
		}

		// Detect HTTP calls in file
		calls := aa.detectHTTPCalls(sourceCode)
		for _, call := range calls {
			// Find function containing this pattern by line number
			functionID, err := aa.findFunctionAtLine(ctx, filePath, call.Line)
			if err != nil || functionID == "" {
				functionID = fileID
			}

			if err := aa.createAPICall(ctx, functionID, call); err != nil {
				fmt.Printf("Warning: failed to create API call: %v\n", err)
			} else {
				callCount++
			}
		}
	}

	fmt.Printf("API pattern analysis complete: %d endpoints, %d calls detected\n", endpointCount, callCount)
	return nil
}

// findFunctionAtLine finds the function/method that contains a specific line number
func (aa *APIAnalyzer) findFunctionAtLine(ctx context.Context, filePath string, lineNum int) (string, error) {
	query := `
		MATCH (f)
		WHERE (f:Function OR f:Method)
		  AND f.filePath = $filePath
		  AND f.startLine <= $line
		  AND f.endLine >= $line
		RETURN elementId(f) AS id
		ORDER BY (f.endLine - f.startLine) ASC
		LIMIT 1
	`

	results, err := aa.client.ExecuteQuery(ctx, query, map[string]any{
		"filePath": filePath,
		"line":     lineNum,
	})

	if err != nil || len(results) == 0 {
		return "", err
	}

	return results[0].AsMap()["id"].(string), nil
}

// detectAPIEndpoints finds API route definitions in source code
func (aa *APIAnalyzer) detectAPIEndpoints(sourceCode string) []*APIPattern {
	var endpoints []*APIPattern

	patterns := []struct {
		regex     *regexp.Regexp
		framework string
		method    string
	}{
		// Elysia patterns (matches .get('/path'), .get("/path"), or .get(`/path`))
		{regexp.MustCompile(`\.get\(['"` + "`" + `]([^'"` + "`" + `]+)['"` + "`" + `]`), "elysia", "GET"},
		{regexp.MustCompile(`\.post\(['"` + "`" + `]([^'"` + "`" + `]+)['"` + "`" + `]`), "elysia", "POST"},
		{regexp.MustCompile(`\.put\(['"` + "`" + `]([^'"` + "`" + `]+)['"` + "`" + `]`), "elysia", "PUT"},
		{regexp.MustCompile(`\.delete\(['"` + "`" + `]([^'"` + "`" + `]+)['"` + "`" + `]`), "elysia", "DELETE"},
		{regexp.MustCompile(`\.patch\(['"` + "`" + `]([^'"` + "`" + `]+)['"` + "`" + `]`), "elysia", "PATCH"},

		// Express patterns
		{regexp.MustCompile(`app\.get\(['"` + "`" + `]([^'"` + "`" + `]+)['"` + "`" + `]`), "express", "GET"},
		{regexp.MustCompile(`app\.post\(['"` + "`" + `]([^'"` + "`" + `]+)['"` + "`" + `]`), "express", "POST"},
		{regexp.MustCompile(`app\.put\(['"` + "`" + `]([^'"` + "`" + `]+)['"` + "`" + `]`), "express", "PUT"},
		{regexp.MustCompile(`app\.delete\(['"` + "`" + `]([^'"` + "`" + `]+)['"` + "`" + `]`), "express", "DELETE"},
		{regexp.MustCompile(`router\.get\(['"` + "`" + `]([^'"` + "`" + `]+)['"` + "`" + `]`), "express", "GET"},
		{regexp.MustCompile(`router\.post\(['"` + "`" + `]([^'"` + "`" + `]+)['"` + "`" + `]`), "express", "POST"},

		// Go http patterns
		{regexp.MustCompile(`HandleFunc\(['"` + "`" + `]([^'"` + "`" + `]+)['"` + "`" + `]`), "go-http", "ANY"},
		{regexp.MustCompile(`Handle\(['"` + "`" + `]([^'"` + "`" + `]+)['"` + "`" + `]`), "go-http", "ANY"},
	}

	// Parse line by line to capture line numbers
	lines := strings.Split(sourceCode, "\n")
	for lineNum, line := range lines {
		for _, pattern := range patterns {
			matches := pattern.regex.FindAllStringSubmatch(line, -1)
			for _, match := range matches {
				if len(match) > 1 {
					endpoints = append(endpoints, &APIPattern{
						Type:       "endpoint",
						Method:     pattern.method,
						Path:       match[1],
						Framework:  pattern.framework,
						Line:       lineNum + 1, // Line numbers are 1-indexed
						SourceCode: match[0],
					})
				}
			}
		}
	}

	return endpoints
}

// detectHTTPCalls finds HTTP client invocations in source code
func (aa *APIAnalyzer) detectHTTPCalls(sourceCode string) []*APIPattern {
	var calls []*APIPattern

	patterns := []struct {
		regex     *regexp.Regexp
		framework string
	}{
		// Axios patterns
		{regexp.MustCompile(`axios\.get\(['"` + "`" + `]([^'"` + "`" + `]+)['"` + "`" + `]`), "axios"},
		{regexp.MustCompile(`axios\.post\(['"` + "`" + `]([^'"` + "`" + `]+)['"` + "`" + `]`), "axios"},
		{regexp.MustCompile(`axios\.put\(['"` + "`" + `]([^'"` + "`" + `]+)['"` + "`" + `]`), "axios"},
		{regexp.MustCompile(`axios\.delete\(['"` + "`" + `]([^'"` + "`" + `]+)['"` + "`" + `]`), "axios"},
		{regexp.MustCompile(`axios\.patch\(['"` + "`" + `]([^'"` + "`" + `]+)['"` + "`" + `]`), "axios"},

		// Fetch patterns
		{regexp.MustCompile(`fetch\(['"` + "`" + `]([^'"` + "`" + `]+)['"` + "`" + `]`), "fetch"},

		// HTTP client patterns (generic)
		{regexp.MustCompile(`http\.Get\(['"` + "`" + `]([^'"` + "`" + `]+)['"` + "`" + `]`), "go-http-client"},
		{regexp.MustCompile(`http\.Post\(['"` + "`" + `]([^'"` + "`" + `]+)['"` + "`" + `]`), "go-http-client"},
	}

	// Parse line by line to capture line numbers
	lines := strings.Split(sourceCode, "\n")

	for lineNum, line := range lines {
		for _, pattern := range patterns {
			matches := pattern.regex.FindAllStringSubmatch(line, -1)
			for _, match := range matches {
				if len(match) > 1 {
					url := match[1]
					method := aa.extractMethodFromRegex(pattern.regex.String())

					calls = append(calls, &APIPattern{
						Type:       "call",
						Method:     method,
						Path:       url,
						Framework:  pattern.framework,
						Line:       lineNum + 1,
						SourceCode: match[0],
					})
				}
			}
		}

		// Detect SDK method calls (VeilClient, CaddyClient, etc.)
		sdkPattern := regexp.MustCompile(`(\w+Client)\.(\w+)\(`)
		sdkMatches := sdkPattern.FindAllStringSubmatch(line, -1)
		for _, match := range sdkMatches {
			if len(match) > 2 {
				clientName := match[1]
				methodName := match[2]

				calls = append(calls, &APIPattern{
					Type:       "call",
					Method:     "SDK",
					Path:       fmt.Sprintf("%s.%s", clientName, methodName),
					Framework:  "sdk",
					Line:       lineNum + 1,
					SourceCode: match[0],
				})
			}
		}
	}

	return calls
}

// createAPIEndpoint creates an APIRoute node and EXPOSES_API relationship
func (aa *APIAnalyzer) createAPIEndpoint(ctx context.Context, functionID string, endpoint *APIPattern) error {
	nodeKey := models.APIRouteNodeKey(endpoint.Method, endpoint.Path)
	routeProps := map[string]any{
		"path":        endpoint.Path,
		"method":      endpoint.Method,
		"nodeKey":     nodeKey,
		"protocol":    "HTTP",
		"description": fmt.Sprintf("%s endpoint exposed by %s", endpoint.Method, aa.serviceName),
		"framework":   endpoint.Framework,
	}

	routeID, err := aa.client.MergeNode(ctx, []string{"APIRoute"},
		map[string]any{"nodeKey": nodeKey},
		routeProps)
	if err != nil {
		return fmt.Errorf("failed to create APIRoute node: %w", err)
	}

	// Create EXPOSES_API relationship
	_, err = aa.client.CreateRelationship(ctx, functionID, routeID, string(models.ExposesAPIRel), nil)
	if err != nil {
		return fmt.Errorf("failed to create EXPOSES_API relationship: %w", err)
	}

	return nil
}

// createAPICall creates a CALLS_API relationship
func (aa *APIAnalyzer) createAPICall(ctx context.Context, functionID string, call *APIPattern) error {
	// Try to find matching APIRoute node
	var routeID string
	var err error

	if call.Framework == "sdk" {
		// For SDK calls, try to link to the SDK method AND the target service
		// Extract client and method name
		parts := strings.Split(call.Path, ".")
		var clientName, methodName string
		if len(parts) == 2 {
			clientName = parts[0] // e.g., "caddyClient", "veilClient"
			methodName = parts[1]  // e.g., "onboardAPI"

			// Find the method in the SDK package
			query := `
				MATCH (m:Method {name: $methodName})
				RETURN elementId(m) AS id
				LIMIT 1
			`
			results, err := aa.client.ExecuteQuery(ctx, query, map[string]any{"methodName": methodName})
			if err == nil && len(results) > 0 {
				routeID = results[0].AsMap()["id"].(string)
			}
		}

		// If no direct method found, create a reference node and try to link to target service
		if routeID == "" {
			refProps := map[string]any{
				"target":    call.Path,
				"framework": call.Framework,
				"type":      "sdk_call",
			}
			routeID, err = aa.client.CreateNode(ctx, []string{"SDKCall"}, refProps)
			if err != nil {
				return fmt.Errorf("failed to create SDK call node: %w", err)
			}

			// Try to link SDKCall to target service based on client name
			// e.g., "caddyClient" -> "veil-caddy" or "caddy"
			if clientName != "" {
				aa.linkSDKCallToService(ctx, routeID, clientName)
			}
		}
	} else {
		// For HTTP calls, try to find or create APIRoute
		query := `
			MATCH (r:APIRoute)
			WHERE r.path CONTAINS $path OR $path CONTAINS r.path
			RETURN elementId(r) AS id
			LIMIT 1
		`

		results, err := aa.client.ExecuteQuery(ctx, query, map[string]any{"path": call.Path})
		if err == nil && len(results) > 0 {
			routeID = results[0].AsMap()["id"].(string)
		} else {
			// Create APIRoute node for external endpoint
			routeProps := map[string]any{
				"path":        call.Path,
				"method":      call.Method,
				"protocol":    "HTTP",
				"description": fmt.Sprintf("External API endpoint called by %s", aa.serviceName),
				"isExternal":  true,
			}

			routeID, err = aa.client.MergeNode(ctx, []string{"APIRoute"},
				map[string]any{"path": call.Path},
				routeProps)
			if err != nil {
				return fmt.Errorf("failed to create APIRoute node: %w", err)
			}
		}
	}

	// Create CALLS_API relationship
	relProps := map[string]any{
		"framework": call.Framework,
		"timeout":   30000, // Default timeout
	}

	_, err = aa.client.CreateRelationship(ctx, functionID, routeID, string(models.CallsAPIRel), relProps)
	if err != nil {
		return fmt.Errorf("failed to create CALLS_API relationship: %w", err)
	}

	return nil
}

// linkSDKCallToService tries to link an SDKCall node to the target service
func (aa *APIAnalyzer) linkSDKCallToService(ctx context.Context, sdkCallID, clientName string) {
	// Extract potential service name from client name
	// "caddyClient" -> ["caddy", "veil-caddy"]
	// "veilClient" -> ["veil", "veil-platform-api", "veil-caddy", "veil-web"]

	// Remove common suffixes
	serviceName := strings.TrimSuffix(clientName, "Client")
	serviceName = strings.TrimSuffix(serviceName, "client")
	serviceName = strings.ToLower(serviceName)

	// Try to find service with matching name pattern
	query := `
		MATCH (s:Service)
		WHERE toLower(s.name) CONTAINS $serviceName
		   OR toLower(s.packageName) CONTAINS $serviceName
		RETURN elementId(s) AS id, s.name AS name
		LIMIT 1
	`

	results, err := aa.client.ExecuteQuery(ctx, query, map[string]any{"serviceName": serviceName})
	if err != nil || len(results) == 0 {
		return
	}

	targetServiceID := results[0].AsMap()["id"].(string)
	targetServiceName := results[0].AsMap()["name"].(string)

	// Create TARGETS_SERVICE relationship
	_, err = aa.client.CreateRelationship(ctx, sdkCallID, targetServiceID, "TARGETS_SERVICE", nil)
	if err == nil {
		fmt.Printf("  Linked SDK call to service: %s\n", targetServiceName)
	}
}

// Helper functions

func (aa *APIAnalyzer) readFunctionSource(filePath string, startByte, endByte int64) (string, error) {
	if startByte < 0 || endByte <= startByte {
		return "", fmt.Errorf("invalid byte offsets")
	}

	content, err := os.ReadFile(filePath)
	if err != nil {
		return "", err
	}

	if int64(len(content)) < endByte {
		return "", fmt.Errorf("file too short for byte offsets")
	}

	return string(content[startByte:endByte]), nil
}

func (aa *APIAnalyzer) readEntireFile(filePath string) (string, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return "", err
	}
	return string(content), nil
}

func (aa *APIAnalyzer) extractMethodFromRegex(regexStr string) string {
	if strings.Contains(regexStr, "get") || strings.Contains(regexStr, "Get") {
		return "GET"
	}
	if strings.Contains(regexStr, "post") || strings.Contains(regexStr, "Post") {
		return "POST"
	}
	if strings.Contains(regexStr, "put") || strings.Contains(regexStr, "Put") {
		return "PUT"
	}
	if strings.Contains(regexStr, "delete") || strings.Contains(regexStr, "Delete") {
		return "DELETE"
	}
	if strings.Contains(regexStr, "patch") || strings.Contains(regexStr, "Patch") {
		return "PATCH"
	}
	return "GET" // Default
}

func getStringFromMap(m map[string]any, key string) string {
	if val, ok := m[key]; ok && val != nil {
		if str, ok := val.(string); ok {
			return str
		}
	}
	return ""
}

func getInt64FromMap(m map[string]any, key string) int64 {
	if val, ok := m[key]; ok && val != nil {
		switch v := val.(type) {
		case int64:
			return v
		case int:
			return int64(v)
		case float64:
			return int64(v)
		}
	}
	return -1
}
