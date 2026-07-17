package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

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

	refresh := false
	if v, ok := args["refresh"].(bool); ok {
		refresh = v
	}

	// Check cache. The cache holds the BASE payload only — examples are
	// per-request (include_examples varies by caller) and are appended after
	// retrieval, so a cached payload never leaks one caller's preference
	// into another's response.
	var schema map[string]interface{}
	s.schemaCacheMu.Lock()
	if !refresh && len(s.schemaCache) > 0 && s.now().Sub(s.schemaCacheTime) < s.schemaCacheTTL {
		schema = s.schemaCache
	}
	s.schemaCacheMu.Unlock()

	if schema == nil {
		// Cache miss or refresh requested — compute and cache.
		computed, err := s.computeSchema(ctx)
		if err != nil {
			return ToolCallResponse{
				Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("schema: failed to compute: %v", err)}},
				IsError: true,
			}
		}
		s.schemaCacheMu.Lock()
		s.schemaCache = computed
		s.schemaCacheTime = s.now()
		s.schemaCacheMu.Unlock()
		schema = computed
	}

	// Shallow-copy before per-request decoration so the cached map is never
	// mutated concurrently.
	out := make(map[string]interface{}, len(schema)+2)
	for k, v := range schema {
		out[k] = v
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

// computeSchema builds the full schema payload, using apoc.meta.stats if available.
func (s *CodeGraphMCPServer) computeSchema(ctx context.Context) (map[string]interface{}, error) {
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

	var labels []labelInfo
	var totalNodeCount int64
	var totalRelCount int64
	apocAvailable := true

	// Try to get label counts from apoc.meta.stats
	statsRecords, statsErr := s.client.ExecuteQuery(ctx,
		`CALL apoc.meta.stats() YIELD labels, nodeCount, relCount
		 RETURN labels, nodeCount, relCount`, nil)

	if statsErr == nil && len(statsRecords) > 0 {
		// apoc.meta.stats is available
		m := statsRecords[0].AsMap()

		// Extract nodeCount
		if nc, ok := m["nodeCount"].(int64); ok {
			totalNodeCount = nc
		}

		// Extract relCount
		if rc, ok := m["relCount"].(int64); ok {
			totalRelCount = rc
		}

		// Parse labels map: {"Label": count}
		if labelsMap, ok := m["labels"].(map[string]interface{}); ok {
			labelByName := make(map[string]int)
			for labelName, countVal := range labelsMap {
				count := 0
				if c, ok := countVal.(int64); ok {
					count = int(c)
				} else if c, ok := countVal.(float64); ok {
					count = int(c)
				}
				labelByName[labelName] = len(labels)
				labels = append(labels, labelInfo{Label: labelName, Count: count})
			}
			sort.Slice(labels, func(i, j int) bool { return labels[i].Label < labels[j].Label })
		}
	} else {
		// Fallback: apoc.meta.stats not available, use label scan
		apocAvailable = false

		labelRecords, err := s.client.ExecuteQuery(ctx,
			`MATCH (n) WITH labels(n) AS lbls
			 UNWIND lbls AS label
			 RETURN label, count(*) AS count
			 ORDER BY label`, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to enumerate labels: %w", err)
		}

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
	}

	// Properties per label via db.schema.nodeTypeProperties
	propRecords, _ := s.client.ExecuteQuery(ctx,
		`CALL db.schema.nodeTypeProperties() YIELD nodeType, propertyName, propertyTypes
		 RETURN nodeType, propertyName, propertyTypes`, nil)

	labelByName := make(map[string]int)
	for i, lbl := range labels {
		labelByName[lbl.Label] = i
	}

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
		return nil, fmt.Errorf("failed to enumerate relationships: %w", err)
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

	// Build output
	out := map[string]interface{}{
		"nodes":             labels,
		"relationships":     relTypes,
		"computed_at":       s.now().Format(time.RFC3339),
		"cache_ttl_seconds": int(s.schemaCacheTTL.Seconds()),
		"apoc":              apocAvailable,
		"nodeCount":         totalNodeCount,
		"relCount":          totalRelCount,
	}

	return out, nil
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
