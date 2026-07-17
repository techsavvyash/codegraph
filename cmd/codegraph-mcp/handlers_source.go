package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

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

	// Documents and chunks store their content in the graph (RFC-011 §3.1),
	// so doc sources never touch the filesystem — the cwd-relative path
	// limitation of code sources does not apply here.
	if nodeID != "" {
		if resp, handled := s.docSource(ctx, nodeID); handled {
			return resp
		}
	}

	var cypher string
	params := map[string]any{}
	if nodeID != "" {
		cypher = `MATCH (f) WHERE elementId(f) = $id AND (f:Function OR f:Method)
		          RETURN f.name AS name, f.filePath AS filePath,
		                 f.startLine AS startLine, f.endLine AS endLine,
		                 coalesce(f.startByte, -1) AS startByte,
		                 coalesce(f.endByte, -1) AS endByte,
		                 coalesce(f.rangeSource, '') AS rangeSource,
		                 f.signature AS signature, f.serviceName AS service
		          LIMIT 1`
		params["id"] = nodeID
	} else {
		cypher = `MATCH (f) WHERE (f:Function OR f:Method) AND f.name = $name
		          RETURN f.name AS name, f.filePath AS filePath,
		                 f.startLine AS startLine, f.endLine AS endLine,
		                 coalesce(f.startByte, -1) AS startByte,
		                 coalesce(f.endByte, -1) AS endByte,
		                 coalesce(f.rangeSource, '') AS rangeSource,
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
	startByte := getIntFromRecord(m, "startByte")
	endByte := getIntFromRecord(m, "endByte")
	rangeSource := getStringFromRecord(m, "rangeSource")
	signature := getStringFromRecord(m, "signature")
	service := getStringFromRecord(m, "service")

	if filePath == "" {
		return errorResponse(fmt.Sprintf("source: %s has no filePath in graph", name))
	}

	data, readErr := readSourceFile(filePath, service, s.workspaceRoot)
	if readErr != nil {
		return errorResponse(fmt.Sprintf("source: failed to read %s: %v", filePath, readErr))
	}

	// Byte-exact extraction when the span came from a parse tree (RFC-010):
	// treesitter and go-ast ranges cover the full function node, so the byte
	// slice IS the body. Anything else — scip-declaration stubs (whose bytes
	// are just the identifier) or pre-provenance nodes — falls back to
	// whole-line extraction over [startLine, endLine].
	var src string
	if (rangeSource == "treesitter" || rangeSource == "go-ast") &&
		startByte >= 0 && startByte < endByte && endByte <= len(data) {
		src = string(data[startByte:endByte])
	} else {
		lines := strings.Split(string(data), "\n")
		if startLine < 1 {
			startLine = 1
		}
		if endLine < startLine || endLine > len(lines) {
			endLine = len(lines)
		}
		src = strings.Join(lines[startLine-1:endLine], "\n")
	}

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
	switch rangeSource {
	case "scip-declaration":
		// Callers must know this is not the body: the graph only has the
		// declaration line for this node (no grammar / interface method /
		// parse-error region).
		b.WriteString("  \nrange: scip-declaration (declaration line only; body span unavailable)")
	case "":
	default:
		fmt.Fprintf(&b, "  \nrange: %s", rangeSource)
	}
	fmt.Fprintf(&b, "  \n%s:%d-%d\n\n```%s\n%s\n```\n",
		filePath, startLine, endLine, lang, src)

	return ToolCallResponse{Content: []ToolContent{{Type: "text", Text: b.String()}}}
}

// docSource serves source for Document/DocumentChunk node IDs. Returns
// handled=false when the id is not a doc node (the code path takes over).
func (s *CodeGraphMCPServer) docSource(ctx context.Context, nodeID string) (ToolCallResponse, bool) {
	records, err := s.client.ExecuteQuery(ctx, `
		MATCH (n) WHERE elementId(n) = $id AND (n:Document OR n:DocumentChunk)
		RETURN labels(n)[0] AS label, n.nodeKey AS nodeKey,
		       coalesce(n.title, '') AS title,
		       coalesce(n.sourceUrl, '') AS sourceUrl,
		       coalesce(n.headingPath, '') AS headingPath,
		       coalesce(n.content, '') AS content,
		       coalesce(n.documentKey, '') AS documentKey,
		       coalesce(n.serviceName, '') AS service
	`, map[string]any{"id": nodeID})
	if err != nil || len(records) == 0 {
		return ToolCallResponse{}, false
	}

	m := records[0].AsMap()
	label := getStringFromRecord(m, "label")
	service := getStringFromRecord(m, "service")

	var b strings.Builder
	switch label {
	case "DocumentChunk":
		fmt.Fprintf(&b, "**%s**", getStringFromRecord(m, "nodeKey"))
		if hp := getStringFromRecord(m, "headingPath"); hp != "" {
			fmt.Fprintf(&b, "  \nsection: %s", hp)
		}
		if service != "" {
			fmt.Fprintf(&b, "  \nservice: `%s`", service)
		}
		fmt.Fprintf(&b, "\n\n```markdown\n%s\n```\n", getStringFromRecord(m, "content"))
	case "Document":
		// Document content lives on its chunks; reassemble in order.
		chunkRecords, err := s.client.ExecuteQuery(ctx, `
			MATCH (d)-[:HAS_CHUNK]->(c:DocumentChunk) WHERE elementId(d) = $id
			RETURN c.content AS content ORDER BY c.chunkIndex
		`, map[string]any{"id": nodeID})
		if err != nil {
			return errorResponse(fmt.Sprintf("source: failed to load document chunks: %v", err)), true
		}
		parts := make([]string, 0, len(chunkRecords))
		for _, rec := range chunkRecords {
			parts = append(parts, getStringFromRecord(rec.AsMap(), "content"))
		}
		fmt.Fprintf(&b, "**%s**", getStringFromRecord(m, "title"))
		if su := getStringFromRecord(m, "sourceUrl"); su != "" {
			fmt.Fprintf(&b, "  \n%s", su)
		}
		if service != "" {
			fmt.Fprintf(&b, "  \nservice: `%s`", service)
		}
		fmt.Fprintf(&b, "\n\n```markdown\n%s\n```\n", strings.Join(parts, "\n\n"))
	default:
		return ToolCallResponse{}, false
	}

	return ToolCallResponse{Content: []ToolContent{{Type: "text", Text: b.String()}}}, true
}
