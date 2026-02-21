package query

import (
	"context"
	"fmt"

	"github.com/context-maximiser/code-graph/libs/neo4j-client-go"
)

// OverlayResolver handles overlay-aware queries that merge main + PR scope results
// with deterministic precedence: overlay nodes win over main nodes for the same nodeKey,
// and tombstoned main nodes are hidden.
type OverlayResolver struct {
	client *neo4j.Client
}

// NewOverlayResolver creates a new overlay resolver.
func NewOverlayResolver(client *neo4j.Client) *OverlayResolver {
	return &OverlayResolver{client: client}
}

// ResolveNode looks up a node by nodeKey with overlay precedence:
// 1. If the node exists in the overlay scope, return the overlay version.
// 2. If the node is tombstoned in the overlay, return nil (hidden).
// 3. Otherwise return the main scope version.
func (r *OverlayResolver) ResolveNode(ctx context.Context, nodeKey, scopeID string) (map[string]any, error) {
	if scopeID == "" || scopeID == "main" {
		return r.resolveMainOnly(ctx, nodeKey)
	}

	// Check overlay scope first
	cypher := `
		MATCH (n {nodeKey: $nodeKey, scopeId: $scopeId})
		RETURN n, labels(n) AS nodeLabels
		LIMIT 1`
	records, err := r.client.ExecuteQuery(ctx, cypher, map[string]any{
		"nodeKey": nodeKey,
		"scopeId": scopeID,
	})
	if err != nil {
		return nil, fmt.Errorf("overlay lookup failed: %w", err)
	}
	if len(records) > 0 {
		return records[0].AsMap(), nil
	}

	// Check for tombstone
	tombCypher := `
		MATCH (t:Tombstone {targetNodeKey: $nodeKey, scopeId: $scopeId})
		RETURN t LIMIT 1`
	tombRecords, err := r.client.ExecuteQuery(ctx, tombCypher, map[string]any{
		"nodeKey": nodeKey,
		"scopeId": scopeID,
	})
	if err != nil {
		return nil, fmt.Errorf("tombstone check failed: %w", err)
	}
	if len(tombRecords) > 0 {
		return nil, nil // Node is tombstoned — hidden
	}

	// Fall through to main scope
	return r.resolveMainOnly(ctx, nodeKey)
}

func (r *OverlayResolver) resolveMainOnly(ctx context.Context, nodeKey string) (map[string]any, error) {
	cypher := `
		MATCH (n {nodeKey: $nodeKey})
		WHERE n.scopeId = 'main' OR n.scopeId IS NULL
		RETURN n, labels(n) AS nodeLabels
		LIMIT 1`
	records, err := r.client.ExecuteQuery(ctx, cypher, map[string]any{"nodeKey": nodeKey})
	if err != nil {
		return nil, fmt.Errorf("main scope lookup failed: %w", err)
	}
	if len(records) > 0 {
		return records[0].AsMap(), nil
	}
	return nil, nil
}

// ResolveSymbol looks up a symbol by its SCIP string with overlay precedence.
func (r *OverlayResolver) ResolveSymbol(ctx context.Context, symbol, scopeID string) (map[string]any, error) {
	if scopeID == "" || scopeID == "main" {
		return r.resolveSymbolMain(ctx, symbol)
	}

	// Overlay first
	cypher := `
		MATCH (s:Symbol {symbol: $symbol, scopeId: $scopeId})
		RETURN s, labels(s) AS nodeLabels
		LIMIT 1`
	records, err := r.client.ExecuteQuery(ctx, cypher, map[string]any{
		"symbol":  symbol,
		"scopeId": scopeID,
	})
	if err != nil {
		return nil, fmt.Errorf("overlay symbol lookup failed: %w", err)
	}
	if len(records) > 0 {
		return records[0].AsMap(), nil
	}

	// Check tombstone
	tombCypher := `
		MATCH (t:Tombstone {targetNodeKey: $symbol, scopeId: $scopeId})
		RETURN t LIMIT 1`
	tombRecords, err := r.client.ExecuteQuery(ctx, tombCypher, map[string]any{
		"symbol":  symbol,
		"scopeId": scopeID,
	})
	if err != nil {
		return nil, fmt.Errorf("tombstone check failed: %w", err)
	}
	if len(tombRecords) > 0 {
		return nil, nil
	}

	return r.resolveSymbolMain(ctx, symbol)
}

func (r *OverlayResolver) resolveSymbolMain(ctx context.Context, symbol string) (map[string]any, error) {
	cypher := `
		MATCH (s:Symbol {symbol: $symbol})
		WHERE s.scopeId = 'main' OR s.scopeId IS NULL
		RETURN s, labels(s) AS nodeLabels
		LIMIT 1`
	records, err := r.client.ExecuteQuery(ctx, cypher, map[string]any{"symbol": symbol})
	if err != nil {
		return nil, fmt.Errorf("main symbol lookup failed: %w", err)
	}
	if len(records) > 0 {
		return records[0].AsMap(), nil
	}
	return nil, nil
}

// ResolveFlow looks up a flow with overlay precedence.
func (r *OverlayResolver) ResolveFlow(ctx context.Context, flowNodeKey, scopeID string) (map[string]any, error) {
	if scopeID == "" || scopeID == "main" {
		return r.resolveFlowMain(ctx, flowNodeKey)
	}

	cypher := `
		MATCH (f:Flow {nodeKey: $flowKey, scopeId: $scopeId})
		RETURN f, labels(f) AS nodeLabels
		LIMIT 1`
	records, err := r.client.ExecuteQuery(ctx, cypher, map[string]any{
		"flowKey": flowNodeKey,
		"scopeId": scopeID,
	})
	if err != nil {
		return nil, fmt.Errorf("overlay flow lookup failed: %w", err)
	}
	if len(records) > 0 {
		return records[0].AsMap(), nil
	}

	tombCypher := `
		MATCH (t:Tombstone {targetNodeKey: $flowKey, scopeId: $scopeId})
		RETURN t LIMIT 1`
	tombRecords, err := r.client.ExecuteQuery(ctx, tombCypher, map[string]any{
		"flowKey": flowNodeKey,
		"scopeId": scopeID,
	})
	if err != nil {
		return nil, fmt.Errorf("tombstone check failed: %w", err)
	}
	if len(tombRecords) > 0 {
		return nil, nil
	}

	return r.resolveFlowMain(ctx, flowNodeKey)
}

func (r *OverlayResolver) resolveFlowMain(ctx context.Context, flowNodeKey string) (map[string]any, error) {
	cypher := `
		MATCH (f:Flow {nodeKey: $flowKey})
		WHERE f.scopeId = 'main' OR f.scopeId IS NULL
		RETURN f, labels(f) AS nodeLabels
		LIMIT 1`
	records, err := r.client.ExecuteQuery(ctx, cypher, map[string]any{"flowKey": flowNodeKey})
	if err != nil {
		return nil, fmt.Errorf("main flow lookup failed: %w", err)
	}
	if len(records) > 0 {
		return records[0].AsMap(), nil
	}
	return nil, nil
}
