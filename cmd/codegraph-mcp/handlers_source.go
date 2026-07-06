package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

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
