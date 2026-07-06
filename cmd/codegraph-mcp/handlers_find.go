package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

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
