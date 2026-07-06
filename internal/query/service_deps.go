package query

import (
	"context"
	"fmt"

	"github.com/context-maximiser/code-graph/internal/graph"
)

// InterServiceEdge represents a dependency between two services.
type InterServiceEdge struct {
	FromService string   `json:"fromService"`
	ToService   string   `json:"toService"`
	RelType     string   `json:"relType"` // CALLS_SERVICE, PUBLISHES_TO, etc.
	Confidence  float64  `json:"confidence"`
	Source      string   `json:"source"` // How this was detected.
	Evidence    []string `json:"evidence,omitempty"`
}

// ServiceDepsQuery queries and manages inter-service dependencies.
type ServiceDepsQuery struct {
	client *neo4j.Client
}

// NewServiceDepsQuery creates a new service dependency query handler.
func NewServiceDepsQuery(client *neo4j.Client) *ServiceDepsQuery {
	return &ServiceDepsQuery{client: client}
}

// GetDependencies returns all service-level dependencies for a given service.
// If scopeID is non-empty, includes overlay-scoped edges.
func (q *ServiceDepsQuery) GetDependencies(ctx context.Context, serviceName, scopeID string) ([]InterServiceEdge, error) {
	scopeFilter := ""
	params := map[string]any{"name": serviceName}

	if scopeID != "" {
		scopeFilter = " AND (r.scopeId = $scopeId OR r.scopeId = 'main')"
		params["scopeId"] = scopeID
	}

	cypher := fmt.Sprintf(`
		MATCH (s:Service {name: $name})-[r:CALLS_SERVICE]->(t:Service)
		WHERE 1=1%s
		RETURN s.name AS from, t.name AS to, type(r) AS relType,
		       r.confidence AS confidence, r.source AS source, r.evidence AS evidence
		UNION ALL
		MATCH (s:Service)-[r:CALLS_SERVICE]->(t:Service {name: $name})
		WHERE 1=1%s
		RETURN s.name AS from, t.name AS to, type(r) AS relType,
		       r.confidence AS confidence, r.source AS source, r.evidence AS evidence
	`, scopeFilter, scopeFilter)

	records, err := q.client.ExecuteQuery(ctx, cypher, params)
	if err != nil {
		return nil, fmt.Errorf("failed to query service deps: %w", err)
	}

	var deps []InterServiceEdge
	for _, r := range records {
		m := r.AsMap()
		dep := InterServiceEdge{
			FromService: strVal(m, "from"),
			ToService:   strVal(m, "to"),
			RelType:     strVal(m, "relType"),
			Source:      strVal(m, "source"),
		}
		if conf, ok := m["confidence"].(float64); ok {
			dep.Confidence = conf
		}
		if ev, ok := m["evidence"].([]any); ok {
			for _, e := range ev {
				if s, ok := e.(string); ok {
					dep.Evidence = append(dep.Evidence, s)
				}
			}
		}
		deps = append(deps, dep)
	}
	return deps, nil
}

// CreateServiceEdge creates a CALLS_SERVICE relationship between two services.
func (q *ServiceDepsQuery) CreateServiceEdge(ctx context.Context, from, to string, confidence float64, source string, evidence []string, scopeID string) error {
	if scopeID == "" {
		scopeID = "main"
	}

	cypher := `
		MATCH (s:Service {name: $from})
		MATCH (t:Service {name: $to})
		MERGE (s)-[r:CALLS_SERVICE {scopeId: $scopeId}]->(t)
		SET r.confidence = $confidence,
		    r.source = $source,
		    r.evidence = $evidence,
		    r.scope = CASE WHEN $scopeId = 'main' THEN 'main' ELSE 'pr' END,
		    r.scopeId = $scopeId`

	params := map[string]any{
		"from":       from,
		"to":         to,
		"confidence": confidence,
		"source":     source,
		"evidence":   evidence,
		"scopeId":    scopeID,
	}

	_, err := q.client.ExecuteQuery(ctx, cypher, params)
	if err != nil {
		return fmt.Errorf("failed to create CALLS_SERVICE edge: %w", err)
	}
	return nil
}

// InferServiceDependencies analyzes SDKCall nodes (created by the symbol analyzer
// via CALLS_API relationships) to infer CALLS_SERVICE edges between services.
func (q *ServiceDepsQuery) InferServiceDependencies(ctx context.Context, scopeID string) (int, error) {
	if scopeID == "" {
		scopeID = "main"
	}

	cypher := `
		MATCH (caller:Service)-[:CONTAINS*]->(f:File)-[:CONTAINS]->(fn)-[:CALLS_API]->(call:SDKCall)
		WHERE call.url IS NOT NULL
		  AND (call.scopeId = $scopeId OR call.scopeId = 'main')
		WITH caller, call
		MATCH (target:Service)-[:CONTAINS*]->(targetFile:File)
		WHERE call.url CONTAINS target.name AND caller.name <> target.name
		WITH DISTINCT caller, target, collect(call.url) AS urls
		MERGE (caller)-[r:CALLS_SERVICE {scopeId: $scopeId}]->(target)
		SET r.confidence = 0.7,
		    r.source = 'sdk_call_inference',
		    r.evidence = urls,
		    r.scope = CASE WHEN $scopeId = 'main' THEN 'main' ELSE 'pr' END,
		    r.scopeId = $scopeId
		RETURN caller.name AS from, target.name AS to, size(urls) AS evidenceCount`

	records, err := q.client.ExecuteQuery(ctx, cypher, map[string]any{"scopeId": scopeID})
	if err != nil {
		return 0, fmt.Errorf("failed to infer service deps: %w", err)
	}

	return len(records), nil
}

func strVal(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}
