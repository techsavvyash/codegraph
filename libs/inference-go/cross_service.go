package inference

import (
	"context"
	"fmt"

	models "github.com/context-maximiser/code-graph/libs/core-models-go"
	neo4j "github.com/context-maximiser/code-graph/libs/neo4j-go"
)

// CrossServiceBoundary represents a detected boundary between two services.
type CrossServiceBoundary struct {
	CallerNodeKey string `json:"callerNodeKey"`
	CallerService string `json:"callerService"`
	CalleeNodeKey string `json:"calleeNodeKey"`
	CalleeService string `json:"calleeService"`
	BoundaryType  string `json:"boundaryType"` // "direct_call", "api_call", "message_queue"
}

// CrossServiceDetector finds call boundaries between different Service nodes.
type CrossServiceDetector struct {
	client   *neo4j.Client
	scopeCtx models.ScopeContext
}

// NewCrossServiceDetector creates a new detector backed by the given Neo4j client.
func NewCrossServiceDetector(client *neo4j.Client) *CrossServiceDetector {
	return &CrossServiceDetector{
		client:   client,
		scopeCtx: models.DefaultScope(),
	}
}

// SetScope configures the scope context used for boundary detection queries.
func (d *CrossServiceDetector) SetScope(scope models.ScopeContext) {
	d.scopeCtx = scope
}

// DetectBoundaries runs three Cypher queries to find cross-service call
// boundaries: direct calls, API-mediated calls, and message queue consumers.
func (d *CrossServiceDetector) DetectBoundaries(ctx context.Context) ([]CrossServiceBoundary, error) {
	params := map[string]any{
		"scopeId": d.scopeCtx.ScopeID,
	}

	var boundaries []CrossServiceBoundary

	// 1. Direct cross-service calls.
	directQuery := `
MATCH (s1:Service)-[:CONTAINS*2..3]->(caller)-[:CALLS]->(callee)<-[:CONTAINS*2..3]-(s2:Service)
WHERE s1 <> s2
  AND (caller:Function OR caller:Method)
  AND (callee:Function OR callee:Method)
  AND (s1.scopeId = $scopeId OR s1.scopeId = 'main')
  AND (s2.scopeId = $scopeId OR s2.scopeId = 'main')
RETURN caller.nodeKey AS callerKey, s1.name AS callerService,
       callee.nodeKey AS calleeKey, s2.name AS calleeService`

	directRecords, err := d.client.ExecuteQuery(ctx, directQuery, params)
	if err != nil {
		return nil, fmt.Errorf("direct cross-service query failed: %w", err)
	}
	for _, rec := range directRecords {
		callerKey, _ := rec.Get("callerKey")
		callerService, _ := rec.Get("callerService")
		calleeKey, _ := rec.Get("calleeKey")
		calleeService, _ := rec.Get("calleeService")
		boundaries = append(boundaries, CrossServiceBoundary{
			CallerNodeKey: fmt.Sprintf("%v", callerKey),
			CallerService: fmt.Sprintf("%v", callerService),
			CalleeNodeKey: fmt.Sprintf("%v", calleeKey),
			CalleeService: fmt.Sprintf("%v", calleeService),
			BoundaryType:  "direct_call",
		})
	}

	// 2. API-mediated calls.
	apiQuery := `
MATCH (caller)-[:CALLS_API]->(route:APIRoute)<-[:EXPOSES_API]-(handler)
MATCH (s1:Service)-[:CONTAINS*2..3]->(caller)
MATCH (s2:Service)-[:CONTAINS*2..3]->(handler)
WHERE s1 <> s2
  AND (s1.scopeId = $scopeId OR s1.scopeId = 'main')
  AND (s2.scopeId = $scopeId OR s2.scopeId = 'main')
RETURN caller.nodeKey AS callerKey, s1.name AS callerService,
       handler.nodeKey AS calleeKey, s2.name AS calleeService`

	apiRecords, err := d.client.ExecuteQuery(ctx, apiQuery, params)
	if err != nil {
		return nil, fmt.Errorf("API cross-service query failed: %w", err)
	}
	for _, rec := range apiRecords {
		callerKey, _ := rec.Get("callerKey")
		callerService, _ := rec.Get("callerService")
		calleeKey, _ := rec.Get("calleeKey")
		calleeService, _ := rec.Get("calleeService")
		boundaries = append(boundaries, CrossServiceBoundary{
			CallerNodeKey: fmt.Sprintf("%v", callerKey),
			CallerService: fmt.Sprintf("%v", callerService),
			CalleeNodeKey: fmt.Sprintf("%v", calleeKey),
			CalleeService: fmt.Sprintf("%v", calleeService),
			BoundaryType:  "api_call",
		})
	}

	// 3. Message queue boundaries (consumer side only).
	mqQuery := `
MATCH (fn)
WHERE (fn:Function OR fn:Method)
  AND fn.consumesBroker = true
  AND (fn.scopeId = $scopeId OR fn.scopeId = 'main')
MATCH (svc:Service)-[:CONTAINS*2..3]->(fn)
WHERE svc.scopeId = $scopeId OR svc.scopeId = 'main'
RETURN fn.nodeKey AS callerKey, svc.name AS callerService`

	mqRecords, err := d.client.ExecuteQuery(ctx, mqQuery, params)
	if err != nil {
		return nil, fmt.Errorf("message queue boundary query failed: %w", err)
	}
	for _, rec := range mqRecords {
		callerKey, _ := rec.Get("callerKey")
		callerService, _ := rec.Get("callerService")
		boundaries = append(boundaries, CrossServiceBoundary{
			CallerNodeKey: fmt.Sprintf("%v", callerKey),
			CallerService: fmt.Sprintf("%v", callerService),
			CalleeNodeKey: "",
			CalleeService: "",
			BoundaryType:  "message_queue",
		})
	}

	return boundaries, nil
}
