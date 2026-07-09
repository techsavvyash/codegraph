package static

import (
	"context"
	"fmt"
	"strings"

	neo4j "github.com/context-maximiser/code-graph/internal/graph"
)

// serviceScopedLabels are node labels the indexer writes that are exclusively
// owned by a single service: re-indexing must wipe this service's previous
// copies before writing fresh ones, or a re-index of unchanged source
// silently doubles every node/edge. Each carries a "serviceName" property set
// at creation time (computeDefinitionProps for Function/Method/Variable/
// Parameter, createFileNode for File, the reference-item builder in
// indexSymbols for Reference), so they can be matched directly without a
// graph traversal.
//
// Deliberately excluded:
//   - Service: the anchor node itself, never deleted.
//   - Class/Interface/Module: keyed by SCIP FQN and MERGEd across every
//     service that defines/implements the same type — deleting them here
//     would break sibling services sharing the same type.
//   - Symbol: package-keyed and shared the same way; handled separately by
//     deleteOrphanedSymbols below, since only the subset actually orphaned
//     by this pass's deletes may be removed.
//   - APIRoute/SDKCall: carry no serviceName property (global method+path /
//     target keys, same MERGE-sharing pattern as Class/Interface); handled
//     separately by deleteDisconnected after this loop severs this service's
//     edges to them.
var serviceScopedLabels = []string{"Function", "Method", "Variable", "Parameter", "Reference", "File"}

// deletePreviousSubgraph wipes everything a previous index of this exact
// service (identified by si.serviceName, within si.scopeCtx) wrote, so that
// IndexProject is idempotent: re-running it against unchanged or changed
// source produces the same graph, not an ever-growing superset of stale plus
// fresh nodes/edges. Must run after the Service node exists (so a
// never-before-indexed service is simply a no-op here) and before any other
// node/edge writes in this run.
//
// Returns a per-label deleted-node-count map for a single observability log
// line; callers should treat a non-nil error as fatal to the run (writing on
// top of a stale subgraph produces a graph that is wrong in a different way
// than before, not merely slow).
func (si *SCIPIndexer) deletePreviousSubgraph(ctx context.Context) (map[string]int, error) {
	counts := make(map[string]int, len(serviceScopedLabels)+3)

	for _, label := range serviceScopedLabels {
		n, err := si.deleteLabelByServiceName(ctx, label)
		if err != nil {
			return counts, fmt.Errorf("delete previous %s nodes: %w", label, err)
		}
		counts[label] = n
	}

	// APIRoute/SDKCall are keyed by bare method+path / target and MERGE-shared
	// across services, so this service may not delete a route another service
	// still exposes. DETACH DELETE of the Function/Method nodes above already
	// severed THIS service's EXPOSES_API/CALLS_API edges; a route left with no
	// relationships at all is garbage by construction (routes only exist
	// alongside their edges), while one still connected to another service's
	// function survives. Must run AFTER the label loop, or this run's own
	// still-standing edges would keep stale routes alive.
	n, err := si.deleteDisconnected(ctx, "APIRoute")
	if err != nil {
		return counts, fmt.Errorf("delete stale APIRoute nodes: %w", err)
	}
	counts["APIRoute"] = n

	n, err = si.deleteDisconnected(ctx, "SDKCall")
	if err != nil {
		return counts, fmt.Errorf("delete stale SDKCall nodes: %w", err)
	}
	counts["SDKCall"] = n

	// Symbol nodes are globally shared (the SCIP symbol string is the
	// nodeKey, merged — scoped by scopeId — across every service that
	// defines OR references it: see MergeNodesBatch's MERGE key). Deleting
	// this service's Function/Method/Variable/Parameter/Reference nodes above
	// may have orphaned some Symbols, but only the ones with NO remaining
	// inbound DEFINES *and* no remaining inbound REFERENCES are truly dead —
	// anything still referenced by another, not-yet-reindexed service in the
	// same scopeId keeps its Reference edges (those weren't touched above)
	// and is correctly excluded. This is deliberately stricter than "no
	// DEFINES alone": a reference-only external symbol (e.g. fmt.Println)
	// never has a DEFINES edge in this graph at all, so "no DEFINES" alone
	// would delete it out from under every other service still referencing
	// it; requiring "no REFERENCES either" avoids that.
	n, err = si.deleteOrphanedSymbols(ctx)
	if err != nil {
		return counts, fmt.Errorf("delete orphaned symbols: %w", err)
	}
	counts["Symbol (orphaned)"] = n

	return counts, nil
}

// deleteLabelByServiceName deletes every node of the given label carrying
// this indexer's (serviceName, scopeId), batched via CALL{}IN TRANSACTIONS so
// large services don't blow a single transaction's memory (mirrors the
// pattern proven in schema.SchemaManager.Migrate's scopedKey backfill).
func (si *SCIPIndexer) deleteLabelByServiceName(ctx context.Context, label string) (int, error) {
	cypher := fmt.Sprintf(`
		MATCH (n:%s {serviceName: $serviceName, scopeId: $scopeId})
		CALL { WITH n DETACH DELETE n } IN TRANSACTIONS OF 1000 ROWS
		RETURN count(n) AS total
	`, neo4j.Ident(label))
	return si.runScopedDeleteCount(ctx, cypher)
}

// deleteDisconnected deletes nodes of the given label in this scope that have
// no relationships left at all (used for APIRoute/SDKCall after the label
// loop severed this service's edges — see deletePreviousSubgraph for why the
// shared-across-services key spaces forbid deleting by reachability).
func (si *SCIPIndexer) deleteDisconnected(ctx context.Context, label string) (int, error) {
	cypher := fmt.Sprintf(`
		MATCH (n:%s {scopeId: $scopeId})
		WHERE NOT (n)--()
		CALL { WITH n DETACH DELETE n } IN TRANSACTIONS OF 1000 ROWS
		RETURN count(n) AS total
	`, neo4j.Ident(label))
	return si.runScopedDeleteCount(ctx, cypher)
}

// deleteOrphanedSymbols removes Symbol nodes left with no inbound DEFINES and
// no inbound REFERENCES after the label deletes above — see the doc comment
// on deletePreviousSubgraph for why both conditions are required.
func (si *SCIPIndexer) deleteOrphanedSymbols(ctx context.Context) (int, error) {
	cypher := `
		MATCH (sym:Symbol {scopeId: $scopeId})
		WHERE NOT (sym)<-[:DEFINES]-() AND NOT (sym)<-[:REFERENCES]-()
		WITH sym
		CALL { WITH sym DETACH DELETE sym } IN TRANSACTIONS OF 1000 ROWS
		RETURN count(sym) AS total
	`
	return si.runScopedDeleteCount(ctx, cypher)
}

// deleteCountLogOrder is the fixed order formatDeleteCounts prints counts in,
// so the one-line summary is deterministic across runs (map iteration order
// is not).
var deleteCountLogOrder = append(append([]string{}, serviceScopedLabels...), "APIRoute", "SDKCall", "Symbol (orphaned)")

// formatDeleteCounts renders a deletePreviousSubgraph result as a single
// deterministic "Label=N, Label=N, ..." line for logging.
func formatDeleteCounts(counts map[string]int) string {
	parts := make([]string, 0, len(deleteCountLogOrder))
	for _, label := range deleteCountLogOrder {
		parts = append(parts, fmt.Sprintf("%s=%d", label, counts[label]))
	}
	return strings.Join(parts, ", ")
}

// runScopedDeleteCount executes a delete cypher built by the helpers above,
// always binding serviceName/scopeId from this indexer's own identity —
// callers never pass params directly, so every delete site is guaranteed to
// be bounded to this run's service and scope.
func (si *SCIPIndexer) runScopedDeleteCount(ctx context.Context, cypher string) (int, error) {
	records, err := si.client.ExecuteQuery(ctx, cypher, map[string]any{
		"serviceName": si.serviceName,
		"scopeId":     si.scopeCtx.ScopeID,
	})
	if err != nil {
		return 0, err
	}
	if len(records) == 0 {
		return 0, nil
	}
	total := getInt64FromMap(records[0].AsMap(), "total")
	if total < 0 {
		total = 0
	}
	return int(total), nil
}
