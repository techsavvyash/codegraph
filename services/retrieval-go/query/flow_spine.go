package query

import (
	"context"
	"fmt"

	"github.com/context-maximiser/code-graph/libs/core-models-go"
	"github.com/context-maximiser/code-graph/libs/neo4j-client-go"
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
	client   *neo4j.Client
	scopeCtx models.ScopeContext
}

// NewFlowSpineGenerator creates a new generator.
func NewFlowSpineGenerator(client *neo4j.Client) *FlowSpineGenerator {
	return &FlowSpineGenerator{
		client:   client,
		scopeCtx: models.DefaultScope(),
	}
}

// SetScope sets the scope context for flow generation.
func (g *FlowSpineGenerator) SetScope(scope models.ScopeContext) {
	g.scopeCtx = scope
}

// GenerateFromAPIEndpoints discovers API endpoints and builds flow spines by
// traversing the call graph from each handler function up to maxDepth.
func (g *FlowSpineGenerator) GenerateFromAPIEndpoints(ctx context.Context, maxDepth int) ([]FlowSpineResult, error) {
	if maxDepth <= 0 {
		maxDepth = 2
	}

	// APIRoute nodes no longer exist — flow generation via structural entrypoints.
	cypher := `
		MATCH (fn)
		WHERE (fn:Function OR fn:Method)
		  AND (fn.scopeId = $scopeId OR fn.scopeId = 'main')
		  AND coalesce(fn.isRPCHandler, false) = true
		RETURN fn.nodeKey AS handlerKey, fn.name AS handlerName, labels(fn) AS handlerLabels`

	records, err := g.client.ExecuteQuery(ctx, cypher, map[string]any{"scopeId": g.scopeCtx.ScopeID})
	if err != nil {
		return nil, fmt.Errorf("failed to query entry points: %w", err)
	}

	var results []FlowSpineResult
	for _, r := range records {
		m := r.AsMap()
		handlerKey := strVal(m, "handlerKey")
		handlerName := strVal(m, "handlerName")
		if handlerKey == "" {
			continue
		}

		flowNodeKey := models.FlowNodeKey("api", handlerKey)
		handlerLabel := "Function"
		if labels, ok := m["handlerLabels"].([]any); ok {
			for _, l := range labels {
				if s, ok := l.(string); ok && s == "Method" {
					handlerLabel = "Method"
					break
				}
			}
		}

		steps := []FlowStep{{NodeKey: handlerKey, Name: handlerName, Label: handlerLabel, Order: 0}}
		callees, err := g.traceCallees(ctx, handlerKey, maxDepth-1, 1)
		if err != nil {
			fmt.Printf("Warning: failed to trace callees for %s: %v\n", handlerKey, err)
		} else {
			steps = append(steps, callees...)
		}

		if err := g.persistFlow(ctx, flowNodeKey, handlerName, "api", handlerKey, maxDepth, steps); err != nil {
			fmt.Printf("Warning: failed to persist flow %s: %v\n", handlerName, err)
			continue
		}
		results = append(results, FlowSpineResult{
			FlowNodeKey: flowNodeKey,
			FlowName:    handlerName,
			FlowType:    "api",
			Steps:       steps,
		})
	}

	return results, nil
}

// traceCallees recursively follows CALLS edges from a given function nodeKey.
func (g *FlowSpineGenerator) traceCallees(ctx context.Context, nodeKey string, remainingDepth, nextOrder int) ([]FlowStep, error) {
	if remainingDepth <= 0 {
		return nil, nil
	}

	cypher := `
		MATCH (caller {nodeKey: $nodeKey})-[:CALLS]->(callee)
		WHERE (caller.scopeId = $scopeId OR caller.scopeId = 'main')
		  AND (callee:Function OR callee:Method)
		  AND (callee.scopeId = $scopeId OR callee.scopeId = 'main')
		RETURN callee.nodeKey AS calleeKey, callee.name AS calleeName, labels(callee) AS calleeLabels
		LIMIT 20`

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
			WHERE (target:Function OR target:Method OR target:Service)
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
					if s == "Method" || s == "Service" {
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
