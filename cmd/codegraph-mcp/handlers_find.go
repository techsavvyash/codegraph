package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/context-maximiser/code-graph/internal/graph/schema"
	"github.com/context-maximiser/code-graph/internal/search"
)

// handleFindTool is the L1 node-listing primitive. It supports two modes:
// 1. Fulltext search when query is provided (via Searcher)
// 2. Structural listing when query is empty but label is provided (indexed scan)
// Both support optional service and scope_id filters, plus keyset pagination.
func (s *CodeGraphMCPServer) handleFindTool(ctx context.Context, args map[string]interface{}) ToolCallResponse {
	query, _ := args["query"].(string)
	label, _ := args["label"].(string)
	service, _ := args["service"].(string)
	scopeID := parseScopeContextArg(args).ScopeID
	if scopeID == "" {
		scopeID = "main"
	}

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

	cursor, _ := args["cursor"].(string)

	// Mode 1: fulltext search (query non-empty)
	if query != "" {
		var labels []string
		if label != "" {
			labels = []string{label}
		}
		semantic, _ := args["semantic"].(bool)
		opts := search.Options{
			Labels:   labels,
			ScopeID:  scopeID,
			Service:  service,
			Limit:    limit,
			Cursor:   cursor,
			Semantic: semantic,
		}

		searcher := search.NewSearcher(s.client)
		if s.embedder != nil {
			searcher.SetEmbedder(s.embedder)
		}
		resp, err := searcher.Search(ctx, query, opts)
		if err != nil {
			// Check if it's an invalid label error
			if strings.Contains(err.Error(), "invalid label") {
				return ToolCallResponse{
					Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("find: %v", err)}},
					IsError: true,
				}
			}
			return ToolCallResponse{
				Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("find: search failed: %v", err)}},
				IsError: true,
			}
		}

		// Format fulltext results
		out := map[string]interface{}{
			"results": resp.Results,
			"count":   len(resp.Results),
		}
		if resp.NextCursor != "" {
			out["next_cursor"] = resp.NextCursor
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

	// Mode 2: structural listing (query empty, label non-empty)
	if label != "" {
		// Structural listing accepts every label with a nodeKey constraint —
		// a wider set than fulltext search, which is limited to the indexed
		// hot labels. Listing Services/APIRoutes/Modules is a core discovery
		// path and needs no fulltext index.
		validLabelsMap := make(map[string]bool)
		for _, c := range schema.GetConstraints() {
			validLabelsMap[c.NodeLabel] = true
		}
		if !validLabelsMap[label] {
			validLabels := make([]string, 0, len(validLabelsMap))
			for k := range validLabelsMap {
				validLabels = append(validLabels, k)
			}
			sort.Strings(validLabels)
			return ToolCallResponse{
				Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("find: invalid label %q; valid labels: %v", label, validLabels)}},
				IsError: true,
			}
		}

		// Build structural listing query with keyset pagination
		conditions := []string{
			"(n.scopeId = $scopeId OR n.scopeId = 'main')",
		}
		params := map[string]any{
			"scopeId": scopeID,
		}

		if service != "" {
			conditions = append(conditions, "n.serviceName = $service")
			params["service"] = service
		}

		whereClause := strings.Join(conditions, " AND ")

		// Decode cursor if provided (format: base64(name\x00nodeID))
		var cursorName, cursorNodeID string
		if cursor != "" {
			decoded, err := base64.StdEncoding.DecodeString(cursor)
			if err == nil {
				parts := strings.SplitN(string(decoded), "\x00", 2)
				if len(parts) == 2 {
					cursorName = parts[0]
					cursorNodeID = parts[1]
				}
			}
		}

		// Base query with keyset pagination on (sortName, elementId). The sort
		// key is coalesce(name, path): File nodes carry path, not name — keying
		// on n.name alone would make every File tie at NULL and never paginate.
		queryStr := fmt.Sprintf(`
MATCH (n:%s)
WITH n, coalesce(n.name, n.path, n.title, n.headingPath, '') AS sortName
WHERE %s
`, label, whereClause)

		if cursorName != "" && cursorNodeID != "" {
			queryStr += `  AND (sortName > $cursorName OR (sortName = $cursorName AND elementId(n) > $cursorNodeId))
`
			params["cursorName"] = cursorName
			params["cursorNodeId"] = cursorNodeID
		}

		queryStr += `RETURN elementId(n) AS node_id,
       coalesce(n.nodeKey, '') AS node_key,
       labels(n)[0] AS label,
       sortName AS name,
       n.signature AS signature,
       n.filePath AS file_path,
       n.serviceName AS service,
       coalesce(n.startLine, 0) AS start_line,
       coalesce(n.endLine, 0) AS end_line
ORDER BY sortName, elementId(n)
LIMIT $limit`

		params["limit"] = limit + 1 // overfetch by 1 to detect more pages

		records, err := s.client.ExecuteQuery(ctx, queryStr, params)
		if err != nil {
			return ToolCallResponse{
				Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("find: structural listing failed: %v", err)}},
				IsError: true,
			}
		}

		type result struct {
			NodeID    string  `json:"node_id"`
			NodeKey   string  `json:"node_key,omitempty"`
			Label     string  `json:"label"`
			Name      string  `json:"name"`
			Signature string  `json:"signature,omitempty"`
			FilePath  string  `json:"file_path,omitempty"`
			Service   string  `json:"service,omitempty"`
			StartLine int     `json:"start_line,omitempty"`
			EndLine   int     `json:"end_line,omitempty"`
			Score     float64 `json:"score"` // always 0 for structural listing
		}

		results := make([]result, 0, len(records))
		for _, rec := range records {
			m := rec.AsMap()
			results = append(results, result{
				NodeID:    getStringFromRecord(m, "node_id"),
				NodeKey:   getStringFromRecord(m, "node_key"),
				Label:     getStringFromRecord(m, "label"),
				Name:      getStringFromRecord(m, "name"),
				Signature: getStringFromRecord(m, "signature"),
				FilePath:  getStringFromRecord(m, "file_path"),
				Service:   getStringFromRecord(m, "service"),
				StartLine: getIntFromRecord(m, "start_line"),
				EndLine:   getIntFromRecord(m, "end_line"),
				Score:     0,
			})
		}

		truncated := len(results) > limit
		if truncated {
			results = results[:limit]
		}

		out := map[string]interface{}{
			"results": results,
			"count":   len(results),
		}
		if truncated && len(results) > 0 {
			// Encode next cursor as base64(name\x00nodeID)
			lastResult := results[len(results)-1]
			cursorStr := lastResult.Name + "\x00" + lastResult.NodeID
			out["next_cursor"] = base64.StdEncoding.EncodeToString([]byte(cursorStr))
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

	// Mode 3: both query and label empty — error
	return ToolCallResponse{
		Content: []ToolContent{{Type: "text", Text: "find: either query or label must be provided"}},
		IsError: true,
	}
}

type expandNode struct {
	NodeID        string `json:"node_id"`
	Label         string `json:"label"`
	Name          string `json:"name,omitempty"`
	QualifiedName string `json:"qualified_name,omitempty"`
	FilePath      string `json:"file_path,omitempty"`
	StartLine     int    `json:"start_line,omitempty"`
	EndLine       int    `json:"end_line,omitempty"`
	Signature     string `json:"signature,omitempty"`
	Service       string `json:"service,omitempty"`
	Distance      int    `json:"distance"`
}

type expandEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Type string `json:"type"`
	// Provenance (RFC-011 I4): present on inferred edges (e.g. MENTIONS) so
	// surfaces can distinguish them from structural facts.
	Strategy   string  `json:"strategy,omitempty"`
	Confidence float64 `json:"confidence,omitempty"`
}
