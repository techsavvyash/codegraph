package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

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
