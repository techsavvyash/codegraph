package telemetry

import (
	"context"
	"fmt"

	neo4j "github.com/context-maximiser/code-graph/internal/graph"
)

// counters holds the raw graph-derived measurements for one service/scope
// pair, before RunID/timestamps are attached.
type counters struct {
	Files               int64
	Functions           int64
	Methods             int64
	Symbols             int64
	CallsEdges          int64
	UsesValueEdges      int64
	ImplementsEdges     int64
	APIRoutes           int64
	RangeSourceDist     map[string]int64
	DetectionSourceDist map[string]int64
	PromotedFunctions   int64
	DecoratedFunctions  int64
}

// computeCounters recomputes every RFC-013 Layer-3 counter from the graph
// for (serviceName, scopeID). Each measurement is its own query rather than
// one giant CALL{} chain: keeps the Cypher legible and lets each piece be
// verified independently against the live graph (see package doc).
func computeCounters(ctx context.Context, client *neo4j.Client, serviceName, scopeID string) (*counters, error) {
	c := &counters{
		RangeSourceDist:     map[string]int64{},
		DetectionSourceDist: map[string]int64{},
	}

	params := map[string]any{"serviceName": serviceName, "scopeId": scopeID}

	if err := scanScalar(ctx, client, `
		MATCH (f:File) WHERE f.serviceName = $serviceName AND f.scopeId = $scopeId
		RETURN count(f) AS n
	`, params, &c.Files); err != nil {
		return nil, fmt.Errorf("files: %w", err)
	}

	if err := scanScalar(ctx, client, `
		MATCH (fn:Function) WHERE fn.serviceName = $serviceName AND fn.scopeId = $scopeId
		RETURN count(fn) AS n
	`, params, &c.Functions); err != nil {
		return nil, fmt.Errorf("functions: %w", err)
	}

	if err := scanScalar(ctx, client, `
		MATCH (m:Method) WHERE m.serviceName = $serviceName AND m.scopeId = $scopeId
		RETURN count(m) AS n
	`, params, &c.Methods); err != nil {
		return nil, fmt.Errorf("methods: %w", err)
	}

	// Symbol nodes carry no serviceName/scopeId of their own; they're joined
	// via the reverse DEFINES edge from the code node that owns them
	// ((n)-[:DEFINES]->(:Symbol), verified against the live graph — DEFINES
	// points FROM the code node INTO the Symbol, not the other way round).
	if err := scanScalar(ctx, client, `
		MATCH (n)-[:DEFINES]->(s:Symbol)
		WHERE n.serviceName = $serviceName AND n.scopeId = $scopeId
		RETURN count(DISTINCT s) AS n
	`, params, &c.Symbols); err != nil {
		return nil, fmt.Errorf("symbols: %w", err)
	}

	if err := scanScalar(ctx, client, `
		MATCH (a)-[r:CALLS]->(b)
		WHERE a.serviceName = $serviceName AND a.scopeId = $scopeId
		  AND b.serviceName = $serviceName AND b.scopeId = $scopeId
		RETURN count(r) AS n
	`, params, &c.CallsEdges); err != nil {
		return nil, fmt.Errorf("calls edges: %w", err)
	}

	if err := scanScalar(ctx, client, `
		MATCH (a)-[r:USES_VALUE]->(b)
		WHERE a.serviceName = $serviceName AND a.scopeId = $scopeId
		  AND b.serviceName = $serviceName AND b.scopeId = $scopeId
		RETURN count(r) AS n
	`, params, &c.UsesValueEdges); err != nil {
		return nil, fmt.Errorf("uses_value edges: %w", err)
	}

	if err := scanScalar(ctx, client, `
		MATCH (a)-[r:IMPLEMENTS]->(b)
		WHERE a.serviceName = $serviceName AND a.scopeId = $scopeId
		  AND b.serviceName = $serviceName AND b.scopeId = $scopeId
		RETURN count(r) AS n
	`, params, &c.ImplementsEdges); err != nil {
		return nil, fmt.Errorf("implements edges: %w", err)
	}

	if err := scanScalar(ctx, client, `
		MATCH (fn)-[:EXPOSES_API]->(r:APIRoute)
		WHERE fn.serviceName = $serviceName AND fn.scopeId = $scopeId
		RETURN count(DISTINCT r) AS n
	`, params, &c.APIRoutes); err != nil {
		return nil, fmt.Errorf("api routes: %w", err)
	}

	// rangeSource distribution over Function+Method nodes (property
	// verified live: "treesitter", "scip-declaration", "go-ast").
	rangeDist, err := scanDist(ctx, client, `
		MATCH (n) WHERE n.serviceName = $serviceName AND n.scopeId = $scopeId
		  AND (n:Function OR n:Method) AND n.rangeSource IS NOT NULL
		RETURN n.rangeSource AS key, count(*) AS n
	`, params)
	if err != nil {
		return nil, fmt.Errorf("rangeSource dist: %w", err)
	}
	c.RangeSourceDist = rangeDist

	// detectionSource distribution merges two edge/node sources that share
	// the same property name: IMPLEMENTS edges (both endpoints in-service,
	// verified live: "scip", "ts-types-resolver") and APIRoute nodes
	// exposed by this service's functions (verified live: "decorator",
	// "external_params", "external_params+cross_pkg", "cross_pkg").
	implDist, err := scanDist(ctx, client, `
		MATCH (a)-[r:IMPLEMENTS]->(b)
		WHERE a.serviceName = $serviceName AND a.scopeId = $scopeId
		  AND b.serviceName = $serviceName AND b.scopeId = $scopeId
		  AND r.detectionSource IS NOT NULL
		RETURN r.detectionSource AS key, count(*) AS n
	`, params)
	if err != nil {
		return nil, fmt.Errorf("implements detectionSource dist: %w", err)
	}
	routeDist, err := scanDist(ctx, client, `
		MATCH (fn)-[:EXPOSES_API]->(r:APIRoute)
		WHERE fn.serviceName = $serviceName AND fn.scopeId = $scopeId
		  AND r.detectionSource IS NOT NULL
		RETURN r.detectionSource AS key, count(DISTINCT r) AS n
	`, params)
	if err != nil {
		return nil, fmt.Errorf("apiroute detectionSource dist: %w", err)
	}
	merged := map[string]int64{}
	for k, v := range implDist {
		merged[k] += v
	}
	for k, v := range routeDist {
		merged[k] += v
	}
	c.DetectionSourceDist = merged

	// promotedFunctions: kindSource="promotion" is stamped by the ingest
	// pipeline when promoteDeclaratorBoundFunctions upgrades a
	// declarator-bound symbol (RFC-010 §4.3). Graphs indexed before the
	// stamp existed report 0 until re-indexed.
	if err := scanScalar(ctx, client, `
		MATCH (n) WHERE n.serviceName = $serviceName AND n.scopeId = $scopeId
		  AND (n:Function OR n:Method) AND n.kindSource = 'promotion'
		RETURN count(n) AS n
	`, params, &c.PromotedFunctions); err != nil {
		return nil, fmt.Errorf("promoted functions: %w", err)
	}

	// decoratedFunctions: fn.decorators IS NOT NULL, across both labels.
	if err := scanScalar(ctx, client, `
		MATCH (n) WHERE n.serviceName = $serviceName AND n.scopeId = $scopeId
		  AND (n:Function OR n:Method) AND n.decorators IS NOT NULL
		RETURN count(n) AS n
	`, params, &c.DecoratedFunctions); err != nil {
		return nil, fmt.Errorf("decorated functions: %w", err)
	}

	return c, nil
}

// scanScalar runs a single-row, single-column (`n`) count query.
func scanScalar(ctx context.Context, client *neo4j.Client, cypher string, params map[string]any, dst *int64) error {
	records, err := client.ExecuteQuery(ctx, cypher, params)
	if err != nil {
		return err
	}
	if len(records) == 0 {
		*dst = 0
		return nil
	}
	v, ok := records[0].AsMap()["n"].(int64)
	if !ok {
		return fmt.Errorf("expected int64 for n, got %T", records[0].AsMap()["n"])
	}
	*dst = v
	return nil
}

// scanDist runs a `key, n` aggregation query and returns it as a map.
func scanDist(ctx context.Context, client *neo4j.Client, cypher string, params map[string]any) (map[string]int64, error) {
	records, err := client.ExecuteQuery(ctx, cypher, params)
	if err != nil {
		return nil, err
	}
	dist := make(map[string]int64, len(records))
	for _, rec := range records {
		m := rec.AsMap()
		key, ok := m["key"].(string)
		if !ok {
			continue
		}
		n, ok := m["n"].(int64)
		if !ok {
			continue
		}
		dist[key] = n
	}
	return dist, nil
}
