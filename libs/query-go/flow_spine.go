package query

import (
	"context"
	"fmt"
	"sort"

	"github.com/context-maximiser/code-graph/libs/core-models-go"
	"github.com/context-maximiser/code-graph/libs/inference-go"
	"github.com/context-maximiser/code-graph/libs/neo4j-go"
)

// FlowStep represents a single step in a flow spine.
type FlowStep struct {
	NodeKey string `json:"nodeKey"`
	Name    string `json:"name"`
	Label   string `json:"label"` // Function, Method, Service, APIRoute
	Order   int    `json:"order"`
}

// FlowSpineResult holds the generated flow and its steps.
type FlowSpineResult struct {
	FlowNodeKey string     `json:"flowNodeKey"`
	FlowName    string     `json:"flowName"`
	FlowType    string     `json:"flowType"`
	Steps       []FlowStep `json:"steps"`
}

// FlowSpineGenerator creates Flow spine nodes from API endpoints and call graphs.
type FlowSpineGenerator struct {
	client       *neo4j.Client
	scopeCtx     models.ScopeContext
	budget       inference.TraversalBudget
	deduplicator *inference.FlowDeduplicator
	seedFinder   *inference.StructuralSeedFinder
}

// NewFlowSpineGenerator creates a new generator with default traversal budget.
func NewFlowSpineGenerator(client *neo4j.Client) *FlowSpineGenerator {
	budget := inference.DefaultTraversalBudget
	return &FlowSpineGenerator{
		client:       client,
		scopeCtx:     models.DefaultScope(),
		budget:       budget,
		deduplicator: inference.NewFlowDeduplicator(),
		seedFinder:   inference.NewStructuralSeedFinder().WithBudget(budget),
	}
}

// SetBudget overrides the traversal budget used for flow generation.
func (g *FlowSpineGenerator) SetBudget(budget inference.TraversalBudget) {
	g.budget = budget
	g.seedFinder = inference.NewStructuralSeedFinder().WithBudget(budget)
}

// SetScope sets the scope context for flow generation.
func (g *FlowSpineGenerator) SetScope(scope models.ScopeContext) {
	g.scopeCtx = scope
}

// GenerateFromAPIEndpoints discovers API endpoints and builds flow spines by
// traversing the call graph from each handler function up to maxDepth.
func (g *FlowSpineGenerator) GenerateFromAPIEndpoints(ctx context.Context, maxDepth int) ([]FlowSpineResult, error) {
	if maxDepth <= 0 {
		maxDepth = g.budget.MaxDepth
	}
	if maxDepth <= 0 {
		maxDepth = 2
	}

	// Find API endpoints and their handler functions.
	cypher := `
		MATCH (route:APIRoute)
		WHERE route.scopeId = $scopeId OR route.scopeId = 'main'
		OPTIONAL MATCH (route)<-[:EXPOSES_API]-(handler)
		WHERE handler:Function OR handler:Method
		RETURN route.nodeKey AS routeKey, route.method AS method, route.path AS path,
		       handler.nodeKey AS handlerKey, handler.name AS handlerName,
		       labels(handler) AS handlerLabels`

	records, err := g.client.ExecuteQuery(ctx, cypher, map[string]any{"scopeId": g.scopeCtx.ScopeID})
	if err != nil {
		return nil, fmt.Errorf("failed to query API endpoints: %w", err)
	}

	var results []FlowSpineResult
	for _, r := range records {
		m := r.AsMap()
		routeKey := strVal(m, "routeKey")
		method := strVal(m, "method")
		path := strVal(m, "path")
		handlerKey := strVal(m, "handlerKey")
		handlerName := strVal(m, "handlerName")

		if routeKey == "" {
			continue
		}

		flowName := fmt.Sprintf("%s %s", method, path)
		flowNodeKey := models.FlowNodeKey("api", routeKey)

		steps := []FlowStep{
			{NodeKey: routeKey, Name: flowName, Label: "APIRoute", Order: 0},
		}

		// If there's a handler, traverse its call graph.
		if handlerKey != "" {
			handlerLabel := "Function"
			if labels, ok := m["handlerLabels"].([]any); ok {
				for _, l := range labels {
					if s, ok := l.(string); ok && s == "Method" {
						handlerLabel = "Method"
						break
					}
				}
			}
			steps = append(steps, FlowStep{
				NodeKey: handlerKey, Name: handlerName, Label: handlerLabel, Order: 1,
			})

			// Traverse CALLS edges from handler.
			callees, err := g.traceCallees(ctx, handlerKey, maxDepth-1, 2)
			if err != nil {
				fmt.Printf("Warning: failed to trace callees for %s: %v\n", handlerKey, err)
			} else {
				steps = append(steps, callees...)
			}
		}

		// Deduplicate and filter steps through the traversal budget.
		steps = g.deduplicateSteps(steps)

		// Persist the Flow node and HAS_STEP edges.
		if err := g.persistFlow(ctx, flowNodeKey, flowName, "api", routeKey, maxDepth, steps); err != nil {
			fmt.Printf("Warning: failed to persist flow %s: %v\n", flowName, err)
			continue
		}

		results = append(results, FlowSpineResult{
			FlowNodeKey: flowNodeKey,
			FlowName:    flowName,
			FlowType:    "api",
			Steps:       steps,
		})
	}

	if len(results) == 0 {
		fallback, err := g.GenerateFromStructuralEntrypoints(ctx, maxDepth)
		if err != nil {
			return nil, err
		}
		return fallback, nil
	}

	return results, nil
}

// GenerateFromStructuralEntrypoints builds flows from framework-agnostic entrypoint
// candidates when APIRoute nodes are unavailable. It uses the StructuralSeedFinder
// to classify, score, and prioritize seeds instead of hardcoded name patterns.
func (g *FlowSpineGenerator) GenerateFromStructuralEntrypoints(ctx context.Context, maxDepth int) ([]FlowSpineResult, error) {
	if maxDepth <= 0 {
		maxDepth = 2
	}

	// Query candidate Function/Method nodes with caller counts and export status.
	cypher := `
		MATCH (fn)
		WHERE (fn:Function OR fn:Method)
		  AND (fn.scopeId = $scopeId OR fn.scopeId = 'main')
		OPTIONAL MATCH (caller)-[:CALLS]->(fn)
		WHERE (caller:Function OR caller:Method)
		  AND (caller.scopeId = $scopeId OR caller.scopeId = 'main')
		WITH fn, count(caller) AS incomingCalls
		RETURN fn.nodeKey AS nodeKey, fn.name AS name, labels(fn) AS labels,
		       coalesce(fn.isExported, false) AS isExported,
		       incomingCalls`

	records, err := g.client.ExecuteQuery(ctx, cypher, map[string]any{"scopeId": g.scopeCtx.ScopeID})
	if err != nil {
		return nil, fmt.Errorf("failed to query structural entrypoints: %w", err)
	}

	// Build NodeInfo slice for the seed finder.
	var nodes []inference.NodeInfo
	for _, r := range records {
		m := r.AsMap()
		nodeKey := strVal(m, "nodeKey")
		name := strVal(m, "name")
		if nodeKey == "" || name == "" {
			continue
		}

		nodeType := "Function"
		if labels, ok := m["labels"].([]any); ok {
			for _, l := range labels {
				if s, ok := l.(string); ok && s == "Method" {
					nodeType = "Method"
					break
				}
			}
		}

		isExported := false
		if v, ok := m["isExported"].(bool); ok {
			isExported = v
		}

		callerCount := int64(0)
		if v, ok := m["incomingCalls"].(int64); ok {
			callerCount = v
		}

		nodes = append(nodes, inference.NodeInfo{
			NodeKey:    nodeKey,
			Name:       name,
			NodeType:   nodeType,
			IsExported: isExported,
			HasCallers: callerCount > 0,
		})
	}

	// Classify and score seeds using the StructuralSeedFinder.
	seeds := g.seedFinder.ClassifySeeds(nodes)

	// Sort by priority descending for deterministic, highest-quality-first ordering.
	sort.Slice(seeds, func(i, j int) bool {
		return seeds[i].Priority > seeds[j].Priority
	})

	// Cap to budget.MaxSteps to avoid generating too many flows.
	if g.budget.MaxSteps > 0 && len(seeds) > g.budget.MaxSteps {
		seeds = seeds[:g.budget.MaxSteps]
	}

	var results []FlowSpineResult
	for _, seed := range seeds {
		flowNodeKey := models.FlowNodeKey("entrypoint", seed.NodeKey)

		steps := []FlowStep{{NodeKey: seed.NodeKey, Name: seed.Name, Label: seed.NodeType, Order: 0}}
		callees, traceErr := g.traceCallees(ctx, seed.NodeKey, maxDepth, 1)
		if traceErr == nil {
			steps = append(steps, callees...)
		}

		// Deduplicate and filter steps through the traversal budget.
		steps = g.deduplicateSteps(steps)

		if err := g.persistFlow(ctx, flowNodeKey, seed.Name, "entrypoint", seed.NodeKey, maxDepth, steps); err != nil {
			continue
		}

		results = append(results, FlowSpineResult{
			FlowNodeKey: flowNodeKey,
			FlowName:    seed.Name,
			FlowType:    "entrypoint",
			Steps:       steps,
		})
	}

	return results, nil
}

// traceCallees recursively follows CALLS edges from a given function nodeKey.
// It respects the budget's MaxFanout and MaxDepth, and filters blocked names.
func (g *FlowSpineGenerator) traceCallees(ctx context.Context, nodeKey string, remainingDepth, nextOrder int) ([]FlowStep, error) {
	if remainingDepth <= 0 {
		return nil, nil
	}

	fanout := g.budget.MaxFanout
	if fanout <= 0 {
		fanout = 10
	}

	cypher := fmt.Sprintf(`
		MATCH (caller {nodeKey: $nodeKey})-[:CALLS]->(callee)
		WHERE (caller.scopeId = $scopeId OR caller.scopeId = 'main')
		  AND (callee:Function OR callee:Method)
		  AND (callee.scopeId = $scopeId OR callee.scopeId = 'main')
		RETURN callee.nodeKey AS calleeKey, callee.name AS calleeName, labels(callee) AS calleeLabels
		LIMIT %d`, fanout)

	records, err := g.client.ExecuteQuery(ctx, cypher, map[string]any{"nodeKey": nodeKey, "scopeId": g.scopeCtx.ScopeID})
	if err != nil {
		return nil, err
	}

	var steps []FlowStep
	order := nextOrder
	for _, r := range records {
		m := r.AsMap()
		calleeKey := strVal(m, "calleeKey")
		calleeName := strVal(m, "calleeName")
		if calleeKey == "" {
			continue
		}

		calleeLabel := "Function"
		if labels, ok := m["calleeLabels"].([]any); ok {
			for _, l := range labels {
				if s, ok := l.(string); ok && s == "Method" {
					calleeLabel = "Method"
					break
				}
			}
		}

		// Apply budget filters: skip blocked names and disallowed node types.
		if g.budget.IsNameBlocked(calleeName) {
			continue
		}
		if !g.budget.IsNodeAllowed(calleeLabel) {
			continue
		}

		steps = append(steps, FlowStep{
			NodeKey: calleeKey, Name: calleeName, Label: calleeLabel, Order: order,
		})
		order++

		// Recurse deeper.
		deeper, err := g.traceCallees(ctx, calleeKey, remainingDepth-1, order)
		if err != nil {
			continue
		}
		steps = append(steps, deeper...)
		order += len(deeper)
	}

	return steps, nil
}

// persistFlow creates/merges a Flow node and its HAS_STEP relationships.
func (g *FlowSpineGenerator) persistFlow(ctx context.Context, flowNodeKey, name, flowType, entrypointKey string, maxDepth int, steps []FlowStep) error {
	flowProps := map[string]any{
		"name":          name,
		"entrypointKey": entrypointKey,
		"flowType":      flowType,
		"maxDepth":      maxDepth,
		"nodeKey":       flowNodeKey,
		"scope":         g.scopeCtx.Scope,
		"scopeId":       g.scopeCtx.ScopeID,
	}

	flowID, err := g.client.MergeNode(ctx, []string{"Flow"},
		map[string]any{"nodeKey": flowNodeKey, "scopeId": g.scopeCtx.ScopeID}, flowProps)
	if err != nil {
		return fmt.Errorf("failed to create Flow node: %w", err)
	}

	// Create HAS_STEP edges to each step's node.
	for _, step := range steps {
		cypher := `
			MATCH (target {nodeKey: $targetKey})
			WHERE (target:Function OR target:Method OR target:APIRoute OR target:Service)
			  AND (target.scopeId = $scopeId OR target.scopeId = 'main')
			WITH target LIMIT 1
			MATCH (flow:Flow {nodeKey: $flowKey, scopeId: $scopeId})
			MERGE (flow)-[r:HAS_STEP {order: $order}]->(target)
			SET r.stepName = $stepName, r.scope = $scope, r.scopeId = $scopeId`

		_, err := g.client.ExecuteQuery(ctx, cypher, map[string]any{
			"flowKey":   flowNodeKey,
			"scopeId":   g.scopeCtx.ScopeID,
			"scope":     g.scopeCtx.Scope,
			"targetKey": step.NodeKey,
			"order":     step.Order,
			"stepName":  step.Name,
		})
		if err != nil {
			fmt.Printf("Warning: failed to create HAS_STEP for step %d (%s): %v\n", step.Order, step.Name, err)
		}
	}

	_ = flowID // used by MergeNode
	return nil
}

// GetFlow retrieves a flow spine by its nodeKey.
func (g *FlowSpineGenerator) GetFlow(ctx context.Context, flowNodeKey string) (*FlowSpineResult, error) {
	cypher := `
		MATCH (f:Flow {nodeKey: $flowKey})
		WHERE f.scopeId = $scopeId OR f.scopeId = 'main'
		OPTIONAL MATCH (f)-[r:HAS_STEP]->(step)
		WHERE step.scopeId = $scopeId OR step.scopeId = 'main'
		RETURN f.name AS flowName, f.flowType AS flowType, f.nodeKey AS flowNodeKey,
		       step.nodeKey AS stepKey, step.name AS stepName, labels(step) AS stepLabels,
		       r.order AS stepOrder
		ORDER BY r.order`

	records, err := g.client.ExecuteQuery(ctx, cypher, map[string]any{"flowKey": flowNodeKey, "scopeId": g.scopeCtx.ScopeID})
	if err != nil {
		return nil, fmt.Errorf("failed to get flow: %w", err)
	}

	if len(records) == 0 {
		return nil, fmt.Errorf("flow not found: %s", flowNodeKey)
	}

	first := records[0].AsMap()
	result := &FlowSpineResult{
		FlowNodeKey: strVal(first, "flowNodeKey"),
		FlowName:    strVal(first, "flowName"),
		FlowType:    strVal(first, "flowType"),
	}

	for _, r := range records {
		m := r.AsMap()
		stepKey := strVal(m, "stepKey")
		if stepKey == "" {
			continue
		}
		order := 0
		if o, ok := m["stepOrder"].(int64); ok {
			order = int(o)
		}

		stepLabel := "Function"
		if labels, ok := m["stepLabels"].([]any); ok {
			for _, l := range labels {
				if s, ok := l.(string); ok {
					if s == "Method" || s == "APIRoute" || s == "Service" {
						stepLabel = s
						break
					}
				}
			}
		}

		result.Steps = append(result.Steps, FlowStep{
			NodeKey: stepKey,
			Name:    strVal(m, "stepName"),
			Label:   stepLabel,
			Order:   order,
		})
	}

	return result, nil
}

// ListFlows lists all flow spines, optionally filtered by type.
func (g *FlowSpineGenerator) ListFlows(ctx context.Context, flowType string) ([]FlowSpineResult, error) {
	var cypher string
	params := map[string]any{}

	params["scopeId"] = g.scopeCtx.ScopeID
	if flowType != "" {
		cypher = `MATCH (f:Flow {flowType: $flowType})
			WHERE f.scopeId = $scopeId OR f.scopeId = 'main'
			RETURN f.nodeKey AS flowNodeKey, f.name AS flowName, f.flowType AS flowType
			ORDER BY f.name`
		params["flowType"] = flowType
	} else {
		cypher = `MATCH (f:Flow)
			WHERE f.scopeId = $scopeId OR f.scopeId = 'main'
			RETURN f.nodeKey AS flowNodeKey, f.name AS flowName, f.flowType AS flowType
			ORDER BY f.name`
	}

	records, err := g.client.ExecuteQuery(ctx, cypher, params)
	if err != nil {
		return nil, fmt.Errorf("failed to list flows: %w", err)
	}

	var results []FlowSpineResult
	for _, r := range records {
		m := r.AsMap()
		results = append(results, FlowSpineResult{
			FlowNodeKey: strVal(m, "flowNodeKey"),
			FlowName:    strVal(m, "flowName"),
			FlowType:    strVal(m, "flowType"),
		})
	}

	return results, nil
}

// deduplicateSteps converts FlowSteps to inference.FlowStepInfo, runs the
// deduplicator with the current budget, and converts back.
func (g *FlowSpineGenerator) deduplicateSteps(steps []FlowStep) []FlowStep {
	if g.deduplicator == nil || len(steps) == 0 {
		return steps
	}

	infos := make([]inference.FlowStepInfo, len(steps))
	for i, s := range steps {
		infos[i] = inference.FlowStepInfo{
			NodeKey:  s.NodeKey,
			Name:     s.Name,
			NodeType: s.Label,
			Order:    s.Order,
		}
	}

	deduped := g.deduplicator.Deduplicate(infos, g.budget)

	out := make([]FlowStep, len(deduped))
	for i, d := range deduped {
		out[i] = FlowStep{
			NodeKey: d.NodeKey,
			Name:    d.Name,
			Label:   d.NodeType,
			Order:   d.Order,
		}
	}
	return out
}
