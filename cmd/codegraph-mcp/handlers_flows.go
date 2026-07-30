package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/context-maximiser/code-graph/internal/query"
	"github.com/context-maximiser/code-graph/internal/query/inference"
)

// handleFlowsToolV2 is the RFC-004 flows primitive. Wraps the existing
// FlowSpineGenerator logic with format=json|text|mermaid output.
// Supports name-or-id addressing via optional from/from_name arguments to generate
// a flow from a specific node.
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
	persist := true
	if p, ok := args["persist"].(bool); ok {
		persist = p
	}

	scopeCtx := parseScopeContextArg(args)
	explicitService := getOptionalStringArg(args, "service_name")
	serviceNames := s.resolveWorkspaceServices(ctx, scopeCtx.ScopeID, explicitService)

	gen := query.NewFlowSpineGenerator(s.client)
	gen.SetScope(scopeCtx)
	gen.SetServiceFilter(serviceNames)
	gen.SetPersist(persist)

	// Check if from/from_name is provided for manual node-driven flow generation.
	fromIDArg, _ := args["from"].(string)
	fromName, _ := args["from_name"].(string)
	fromLabel, _ := args["from_label"].(string)
	fromService, _ := args["from_service"].(string)

	var flows []query.FlowSpineResult
	var err error

	// If from/from_name is provided, resolve the node and generate a single flow from it.
	if fromIDArg != "" || fromName != "" {
		_, nodeRecord, errResp := s.resolveNodeID(ctx, fromIDArg, fromName, fromLabel, fromService)
		if errResp.Content != nil {
			return errResp
		}

		// The flow generator addresses nodes by nodeKey (its traversal and
		// HAS_STEP writes MATCH on nodeKey) — the elementId that resolveNodeID
		// primarily yields matches nothing there.
		nodeKey := getStringFromRecord(nodeRecord, "node_key")
		nodeName := getStringFromRecord(nodeRecord, "name")
		nodeLabel := getStringFromRecord(nodeRecord, "label")
		if nodeKey == "" {
			return errorResponse(fmt.Sprintf("flows: resolved node %q has no nodeKey; flows can only start from indexed code nodes", nodeName))
		}

		flows, err = gen.GenerateFlowFromNode(ctx, nodeKey, nodeName, nodeLabel, maxDepth)
		if err != nil {
			return errorResponse(fmt.Sprintf("flows: generation from node failed: %v", err))
		}
		// No workspace filtering here: the caller addressed a specific node,
		// so dropping its flow because the files live outside the current
		// workspace would silently answer with "no flows" for a node that
		// verifiably exists.
	} else {
		// Normal mode: generate flows from all structural seeds, filtered to
		// the current workspace so discovery stays relevant to the open repo —
		// UNLESS the caller passed an explicit service_name, in which case
		// that scoping already replaces workspace relevance (same design call
		// as the from-mode comment above). Without this bypass, a server
		// process whose cwd isn't the indexed repo root (e.g. the Studio MCP
		// bridge, spawned with cwd=bin/) drops every result.
		flows, err = gen.GenerateFlows(ctx, maxDepth)
		if err != nil {
			return errorResponse(fmt.Sprintf("flows: generation failed: %v", err))
		}
		if explicitService == "" {
			flows = s.filterFlowsToWorkspace(ctx, scopeCtx.ScopeID, flows)
		}
	}

	if len(flows) > limit {
		flows = flows[:limit]
	}

	s.enrichFlowSteps(ctx, scopeCtx.ScopeID, flows)

	if len(flows) == 0 {
		if format == "json" {
			body, _ := json.MarshalIndent(map[string]interface{}{
				"flow_count": 0,
				"flows":      []query.FlowSpineResult{},
			}, "", "  ")
			return ToolCallResponse{Content: []ToolContent{{Type: "text", Text: string(body)}}}
		}
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

// enrichFlowSteps batch-fills NodeID (elementId)/FilePath/StartLine on every
// step across all flows with one query, mutating flows in place. Steps whose
// nodeKey no longer resolves (a node could vanish between the trace and this
// enrichment pass) are left with those three fields empty rather than
// failing the whole request.
func (s *CodeGraphMCPServer) enrichFlowSteps(ctx context.Context, scopeID string, flows []query.FlowSpineResult) {
	keys := make([]string, 0)
	seen := make(map[string]bool)
	for _, f := range flows {
		for _, step := range f.Steps {
			if step.NodeKey == "" || seen[step.NodeKey] {
				continue
			}
			seen[step.NodeKey] = true
			keys = append(keys, step.NodeKey)
		}
	}
	if len(keys) == 0 {
		return
	}

	cypher := `
		UNWIND $keys AS k
		MATCH (n:Function|Method|Service|APIRoute {nodeKey: k})
		WHERE (n.scopeId = $scopeId OR n.scopeId = 'main')
		RETURN k AS nodeKey, elementId(n) AS id, coalesce(n.filePath, '') AS filePath,
		       coalesce(n.startLine, 0) AS startLine`

	records, err := s.client.ExecuteQuery(ctx, cypher, map[string]any{"keys": keys, "scopeId": scopeID})
	if err != nil {
		// Enrichment is best-effort — the flow steps are still valid without
		// nodeId/filePath/startLine.
		return
	}

	type enrichment struct {
		id        string
		filePath  string
		startLine int
	}
	byKey := make(map[string]enrichment, len(records))
	for _, r := range records {
		m := r.AsMap()
		nk := getStringFromRecord(m, "nodeKey")
		if nk == "" {
			continue
		}
		byKey[nk] = enrichment{
			id:        getStringFromRecord(m, "id"),
			filePath:  getStringFromRecord(m, "filePath"),
			startLine: getIntFromRecord(m, "startLine"),
		}
	}

	for fi := range flows {
		for si := range flows[fi].Steps {
			step := &flows[fi].Steps[si]
			e, ok := byKey[step.NodeKey]
			if !ok {
				continue
			}
			step.NodeID = e.id
			step.FilePath = e.filePath
			step.StartLine = e.startLine
		}
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
	explicitService := getOptionalStringArg(args, "service_name")
	serviceNames := s.resolveWorkspaceServices(ctx, scopeCtx.ScopeID, explicitService)
	tierFilter := 0
	if t, ok := args["tier"].(float64); ok && t >= 1 && t <= 4 {
		tierFilter = int(t)
	}

	// The shared high-centrality definition (mean + 1 stddev of
	// betweennessCentrality) — same as GraphSeedFinder's Tier 4. Without GDS
	// data the threshold comparison below matches nothing, which is correct.
	centralityThreshold, hasCentralityData, err := inference.ComputeCentralityThreshold(ctx, s.client, scopeCtx.ScopeID)
	if err != nil || !hasCentralityData {
		centralityThreshold = -1 // sentinel: fn.betweennessCentrality > -1 never matches NULL, and no node carries the property
	}

	type entryOut struct {
		NodeKey     string `json:"node_key"`
		Name        string `json:"name"`
		FilePath    string `json:"file_path,omitempty"`
		Tier        int    `json:"tier"`
		TierLabel   string `json:"tier_label"`
		Source      string `json:"detection_source,omitempty"`
		ServiceName string `json:"service,omitempty"`
		NodeID      string `json:"node_id,omitempty"`
		Label       string `json:"label,omitempty"`
		StartLine   int    `json:"start_line,omitempty"`
		OutDegree   int    `json:"out_degree"`
		InDegree    int    `json:"in_degree"`
	}

	params := map[string]any{
		"scopeId":             scopeCtx.ScopeID,
		"serviceNames":        serviceNames,
		"centralityThreshold": centralityThreshold,
	}
	seen := make(map[string]bool)
	entries := []entryOut{}
	var tierErrors []string

	addEntry := func(e entryOut) {
		if e.NodeKey == "" || seen[e.NodeKey] {
			return
		}
		// Workspace relevance is a discovery heuristic; an explicit
		// service_name is the caller stating precisely what it wants, so it
		// bypasses this check. Without the bypass, a server process whose
		// cwd isn't the indexed repo root (e.g. the Studio MCP bridge,
		// spawned with cwd=bin/) drops every entry.
		if explicitService == "" && e.FilePath != "" && !s.fileInWorkspace(e.FilePath) {
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
			// A broken tier query silently returning nothing reads as "this
			// codebase has no entry points" — surface it instead.
			tierErrors = append(tierErrors, fmt.Sprintf("tier %d (%s): %v", tier, label, err))
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
				NodeID:      getStringFromRecord(m, "nodeId"),
				Label:       getStringFromRecord(m, "label"),
				StartLine:   getIntFromRecord(m, "startLine"),
				OutDegree:   getIntFromRecord(m, "outDegree"),
				InDegree:    getIntFromRecord(m, "inDegree"),
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
		       coalesce(r.detectionSource, r.protocol) AS source,
		       elementId(fn) AS nodeId, labels(fn)[0] AS label, fn.startLine AS startLine,
		       COUNT { (fn)-[:CALLS]->() } AS outDegree, COUNT { ()-[:CALLS]->(fn) } AS inDegree
		ORDER BY fn.name`, serviceFilterClause("fn")),
		func(m map[string]interface{}) string { return getStringFromRecord(m, "source") })

	// Tier 2: Interface implementations with no callers. Method-level
	// IMPLEMENTS edges from SCIP relationship ingestion point at the abstract
	// *member* (a Method contained by a File), not at an Interface node —
	// Class→Interface is the only shape that targets :Interface directly. So
	// the predicate is "fn implements anything", not "fn implements an
	// :Interface".
	runTier(2, "Interface impl", fmt.Sprintf(`
		MATCH (fn)-[:IMPLEMENTS]->(abstract)
		WHERE (fn:Function OR fn:Method)
		  AND (fn.scopeId = $scopeId OR fn.scopeId = 'main')
		  AND coalesce(fn.isTestFunction, false) = false
		  AND (fn.filePath IS NULL OR NOT fn.filePath ENDS WITH '_test.go')
		  %s
		OPTIONAL MATCH (caller)-[:CALLS]->(fn)
		WHERE caller:Function OR caller:Method
		WITH fn, head(collect(abstract.name)) AS abstractName, count(caller) AS callerCount
		WHERE callerCount = 0
		RETURN DISTINCT fn.nodeKey AS nodeKey, fn.name AS name,
		       coalesce(fn.filePath, '') AS filePath,
		       fn.serviceName AS serviceName,
		       coalesce(abstractName, '') AS source,
		       elementId(fn) AS nodeId, labels(fn)[0] AS label, fn.startLine AS startLine,
		       COUNT { (fn)-[:CALLS]->() } AS outDegree, COUNT { ()-[:CALLS]->(fn) } AS inDegree
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
		RETURN fn.nodeKey AS nodeKey, fn.name AS name,
		       coalesce(fn.filePath, '') AS filePath,
		       fn.serviceName AS serviceName,
		       toString(calleeCount) AS source,
		       elementId(fn) AS nodeId, labels(fn)[0] AS label, fn.startLine AS startLine,
		       COUNT { (fn)-[:CALLS]->() } AS outDegree, COUNT { ()-[:CALLS]->(fn) } AS inDegree
		ORDER BY calleeCount DESC, fn.name
		LIMIT 50`, serviceFilterClause("fn")),
		func(m map[string]interface{}) string { return getStringFromRecord(m, "source") + " callees" })

	// Tier 4: High centrality — the same betweennessCentrality > mean+stddev
	// definition GraphSeedFinder uses (inference.ComputeCentralityThreshold).
	// Requires the GDS metrics stage; without it no node carries
	// betweennessCentrality and this tier is empty by design.
	runTier(4, "High centrality", fmt.Sprintf(`
		MATCH (fn) WHERE (fn:Function OR fn:Method)
		  AND (fn.scopeId = $scopeId OR fn.scopeId = 'main')
		  AND coalesce(fn.isTestFunction, false) = false
		  AND (fn.filePath IS NULL OR NOT fn.filePath ENDS WITH '_test.go')
		  %s
		WITH fn WHERE fn.betweennessCentrality > $centralityThreshold
		RETURN fn.nodeKey AS nodeKey, fn.name AS name,
		       coalesce(fn.filePath, '') AS filePath,
		       fn.serviceName AS serviceName,
		       toString(round(fn.betweennessCentrality, 2)) AS source,
		       elementId(fn) AS nodeId, labels(fn)[0] AS label, fn.startLine AS startLine,
		       COUNT { (fn)-[:CALLS]->() } AS outDegree, COUNT { ()-[:CALLS]->(fn) } AS inDegree
		ORDER BY fn.betweennessCentrality DESC LIMIT 30`, serviceFilterClause("fn")),
		func(m map[string]interface{}) string { return "bc:" + getStringFromRecord(m, "source") })

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
		payload := map[string]interface{}{
			"count":   len(entries),
			"entries": entries,
		}
		if len(tierErrors) > 0 {
			payload["tier_errors"] = tierErrors
		}
		body, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			return errorResponse(fmt.Sprintf("entry_points: encode failed: %v", err))
		}
		return ToolCallResponse{Content: []ToolContent{{Type: "text", Text: string(body)}}}
	}
}
