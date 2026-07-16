package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// resolveNodeID resolves a node by either node_id (elementId) or by name.
// Returns the elementId, the resolved node details, or an ambiguity/not-found response.
func (s *CodeGraphMCPServer) resolveNodeID(ctx context.Context, nodeIDArg, name, label, serviceName string) (string, map[string]interface{}, ToolCallResponse) {
	// If node_id is provided, use it directly
	if nodeIDArg != "" {
		records, err := s.client.ExecuteQuery(ctx,
			`MATCH (n) WHERE elementId(n) = $id
			 RETURN elementId(n) AS node_id, n.nodeKey AS node_key, labels(n)[0] AS label,
			        coalesce(n.name, n.path, '') AS name,
			        n.fqn AS qualified_name,
			        n.filePath AS file_path,
			        n.startLine AS start_line,
			        n.endLine AS end_line,
			        n.signature AS signature,
			        n.serviceName AS service_name`,
			map[string]any{"id": nodeIDArg})
		if err != nil || len(records) == 0 {
			return "", nil, errorResponse(fmt.Sprintf("node_id not found: %s", nodeIDArg))
		}
		return nodeIDArg, records[0].AsMap(), ToolCallResponse{}
	}

	// Resolve by name
	if name == "" {
		return "", nil, errorResponse("either node_id or name is required")
	}

	// Build query to find nodes by name. The alternation MUST be
	// parenthesized — appended AND filters bind tighter than OR, which would
	// silently limit them to the path branch.
	queryStr := `MATCH (n) WHERE (n.name = $name OR n.path = $name)`
	params := map[string]any{"name": name}

	if label != "" {
		queryStr += ` AND $label IN labels(n)`
		params["label"] = label
	}
	if serviceName != "" {
		queryStr += ` AND n.serviceName = $service`
		params["service"] = serviceName
	}

	queryStr += `
RETURN elementId(n) AS node_id, n.nodeKey AS node_key, labels(n)[0] AS label,
       coalesce(n.name, n.path, '') AS name,
       n.fqn AS qualified_name,
       n.filePath AS file_path,
       n.startLine AS start_line,
       n.endLine AS end_line,
       n.signature AS signature,
       n.serviceName AS service_name
LIMIT 10`

	records, err := s.client.ExecuteQuery(ctx, queryStr, params)
	if err != nil {
		return "", nil, errorResponse(fmt.Sprintf("name resolution failed: %v", err))
	}

	if len(records) == 0 {
		return "", nil, ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("No node found with name=%q. Use codegraph_find with a name_pattern to search.", name)}},
			IsError: false,
		}
	}

	if len(records) > 1 {
		// ANY multiplicity is ambiguity — silently picking one would answer a
		// different question than the caller asked. Return candidates so the
		// agent can disambiguate and re-call.
		candidates := make([]map[string]interface{}, 0, len(records))
		for _, rec := range records {
			m := rec.AsMap()
			candidates = append(candidates, map[string]interface{}{
				"node_id":      getStringFromRecord(m, "node_id"),
				"label":        getStringFromRecord(m, "label"),
				"name":         getStringFromRecord(m, "name"),
				"file_path":    getStringFromRecord(m, "file_path"),
				"service_name": getStringFromRecord(m, "service_name"),
			})
		}
		out := map[string]interface{}{
			"error":      "ambiguous",
			"message":    fmt.Sprintf("Multiple nodes match name=%q. Disambiguate using label or service_name, or pass a node_id.", name),
			"candidates": candidates,
		}
		body, _ := json.MarshalIndent(out, "", "  ")
		return "", nil, ToolCallResponse{
			Content: []ToolContent{{Type: "text", Text: string(body)}},
			IsError: false,
		}
	}

	// Exactly one match
	nid := getStringFromRecord(records[0].AsMap(), "node_id")
	return nid, records[0].AsMap(), ToolCallResponse{}
}

// handleExpandTool traverses edges from a starting node using apoc.path.subgraphAll.
// Supports name-or-id addressing. Returns reachable nodes and connecting edges.
func (s *CodeGraphMCPServer) handleExpandTool(ctx context.Context, args map[string]interface{}) ToolCallResponse {
	// Resolve node by node_id or name
	nodeIDArg, _ := args["node_id"].(string)
	nameArg, _ := args["name"].(string)
	labelArg, _ := args["label"].(string)
	serviceArg, _ := args["service_name"].(string)

	nodeID, nodeRecord, errResp := s.resolveNodeID(ctx, nodeIDArg, nameArg, labelArg, serviceArg)
	if errResp.Content != nil {
		return errResp
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

	depth := 3
	if d, ok := args["depth"].(float64); ok {
		depth = int(d)
	}
	if depth < 1 {
		depth = 1
	}
	if depth > 10 {
		depth = 10
	}

	maxNodes := 500
	if m, ok := args["max_nodes"].(float64); ok {
		maxNodes = int(m)
	}
	if maxNodes < 1 {
		maxNodes = 1
	}
	if maxNodes > 2000 {
		maxNodes = 2000
	}

	format := "json"
	if f, ok := args["format"].(string); ok && f != "" {
		format = f
	}

	// Rel types are interpolated into the APOC relationshipFilter string, so
	// only plain identifiers are allowed (every real rel type in this graph
	// is one). Anything else is rejected rather than escaped.
	for _, rt := range relTypes {
		if !isPlainIdent(rt) {
			return errorResponse(fmt.Sprintf("expand: invalid rel_type %q (must be a plain identifier; call codegraph_schema to list valid types)", rt))
		}
	}

	// Build the APOC relationship filter. Direction markers apply PER TYPE,
	// not to the whole list: "CALLS>|CONTAINS>" — a single trailing ">" would
	// leave every other type bidirectional.
	relFilter := buildRelationshipFilter(relTypes, direction)

	// apoc.path.spanningTree is expandConfig with NODE_GLOBAL uniqueness: it
	// yields exactly one (shortest-hop) path per reachable node, so each node
	// appears once and length(path) is its distance.
	cypher := fmt.Sprintf(`
MATCH (start) WHERE elementId(start) = $startId
CALL apoc.path.spanningTree(start, {
  relationshipFilter: '%s',
  maxLevel: %d,
  limit: %d
})
YIELD path
WITH last(nodes(path)) AS n, length(path) AS distance
WHERE distance > 0
RETURN elementId(n) AS node_id,
       labels(n)[0] AS label,
       coalesce(n.name, n.path, '') AS name,
       n.fqn AS qualified_name,
       n.filePath AS file_path,
       n.startLine AS start_line,
       n.endLine AS end_line,
       n.signature AS signature,
       n.serviceName AS service,
       distance
ORDER BY distance, label, name`,
		relFilter, depth, maxNodes+1)

	records, err := s.client.ExecuteQuery(ctx, cypher, map[string]any{"startId": nodeID})
	if err != nil {
		return errorResponse(fmt.Sprintf("expand: apoc.path.spanningTree failed: %v", err))
	}

	// Build start node from resolved record
	startNode := expandNode{
		NodeID:        nodeID,
		Label:         getStringFromRecord(nodeRecord, "label"),
		Name:          getStringFromRecord(nodeRecord, "name"),
		QualifiedName: getStringFromRecord(nodeRecord, "qualified_name"),
		FilePath:      getStringFromRecord(nodeRecord, "file_path"),
		StartLine:     getIntFromRecord(nodeRecord, "start_line"),
		EndLine:       getIntFromRecord(nodeRecord, "end_line"),
		Signature:     getStringFromRecord(nodeRecord, "signature"),
		Service:       getStringFromRecord(nodeRecord, "service_name"),
		Distance:      0,
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
			EndLine:       getIntFromRecord(m, "end_line"),
			Signature:     getStringFromRecord(m, "signature"),
			Service:       getStringFromRecord(m, "service"),
			Distance:      getIntFromRecord(m, "distance"),
		})
	}

	// max_nodes caps the returned set INCLUDING the start node. The traversal
	// asked for one extra so truncation is detectable.
	truncated := len(nodes) > maxNodes
	if truncated {
		nodes = nodes[:maxNodes]
	}

	// Fetch edges among the returned nodes
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
			 RETURN elementId(a) AS from, elementId(b) AS to, type(r) AS type,
			        coalesce(r.strategy, '') AS strategy,
			        coalesce(r.confidence, -1.0) AS confidence`,
			map[string]any{"ids": ids, "relTypes": relTypes})
		if err == nil {
			for _, rec := range edgeRecords {
				m := rec.AsMap()
				e := expandEdge{
					From: getStringFromRecord(m, "from"),
					To:   getStringFromRecord(m, "to"),
					Type: getStringFromRecord(m, "type"),
				}
				// Only provenanced (inferred) edges carry these; structural
				// facts stay clean.
				if strategy := getStringFromRecord(m, "strategy"); strategy != "" {
					e.Strategy = strategy
					if conf, ok := m["confidence"].(float64); ok && conf >= 0 {
						e.Confidence = conf
					}
				}
				edges = append(edges, e)
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

// lineRangeSuffix renders ":start-end" (or ":start" for single-line /
// unknown-end ranges, "" when no location at all) for file references in
// text output.
func lineRangeSuffix(startLine, endLine int) string {
	if startLine <= 0 {
		return ""
	}
	if endLine > startLine {
		return fmt.Sprintf(":%d-%d", startLine, endLine)
	}
	return fmt.Sprintf(":%d", startLine)
}

// isPlainIdent reports whether s is a plain identifier ([A-Za-z_][A-Za-z0-9_]*),
// safe to interpolate into an APOC relationshipFilter string.
func isPlainIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r == '_':
		case r >= '0' && r <= '9':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// buildRelationshipFilter renders an APOC relationshipFilter for the given
// types and direction. Direction markers are PER TYPE: "CALLS>|CONTAINS>",
// "<CALLS|<CONTAINS", or "CALLS|CONTAINS" for both.
func buildRelationshipFilter(relTypes []string, direction string) string {
	parts := make([]string, len(relTypes))
	for i, rt := range relTypes {
		switch direction {
		case "out":
			parts[i] = rt + ">"
		case "in":
			parts[i] = "<" + rt
		default:
			parts[i] = rt
		}
	}
	return strings.Join(parts, "|")
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
		fmt.Fprintf(&b, "  %s -->|%s| %s\n", from, edgeLabel(e), to)
	}
	b.WriteString("```\n")
	return b.String()
}

// edgeLabel renders the edge type, annotated with provenance for inferred
// edges: "MENTIONS (docmine/codespan 0.90)".
func edgeLabel(e expandEdge) string {
	if e.Strategy == "" {
		return e.Type
	}
	return fmt.Sprintf("%s (%s %.2f)", e.Type, e.Strategy, e.Confidence)
}

func renderExpandText(nodes []expandNode, edges []expandEdge, truncated bool) string {
	var b strings.Builder
	if len(nodes) > 0 {
		fmt.Fprintf(&b, "Start: %s %s\n", nodes[0].Label, nodes[0].Name)
		if nodes[0].FilePath != "" {
			fmt.Fprintf(&b, "  %s%s", nodes[0].FilePath, lineRangeSuffix(nodes[0].StartLine, nodes[0].EndLine))
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
				fmt.Fprintf(&b, " (%s%s)", n.FilePath, lineRangeSuffix(n.StartLine, n.EndLine))
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}

// handlePathTool finds paths between two nodes using allShortestPaths or simple paths.
// Supports name-or-id addressing for both source and target.
func (s *CodeGraphMCPServer) handlePathTool(ctx context.Context, args map[string]interface{}) ToolCallResponse {
	// Resolve source node
	fromIDArg, _ := args["from_id"].(string)
	fromName, _ := args["from_name"].(string)
	fromLabel, _ := args["from_label"].(string)
	fromService, _ := args["from_service"].(string)

	fromID, _, errResp := s.resolveNodeID(ctx, fromIDArg, fromName, fromLabel, fromService)
	if errResp.Content != nil {
		return errResp
	}

	// Resolve target node
	toIDArg, _ := args["to_id"].(string)
	toName, _ := args["to_name"].(string)
	toLabel, _ := args["to_label"].(string)
	toService, _ := args["to_service"].(string)

	toID, _, errResp := s.resolveNodeID(ctx, toIDArg, toName, toLabel, toService)
	if errResp.Content != nil {
		return errResp
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

	// Build pattern with sanitized rel types
	relTypePattern := ":`" + strings.Join(relTypes, "`|`") + "`"
	var leftArrow, rightArrow string
	switch direction {
	case "out":
		leftArrow, rightArrow = "-", "->"
	case "in":
		leftArrow, rightArrow = "<-", "-"
	case "both":
		leftArrow, rightArrow = "-", "-"
	}

	pathExpr := fmt.Sprintf("(a)%s[%s*1..%d]%s(b)", leftArrow, relTypePattern, maxHops, rightArrow)
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
			Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("No path found from source to target within %d hops via %v (direction=%s)", maxHops, relTypes, direction)}},
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
