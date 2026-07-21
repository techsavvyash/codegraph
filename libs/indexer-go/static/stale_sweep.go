package static

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/context-maximiser/code-graph/libs/neo4j-go"
)

// SweepStaleCallNodes deletes this service's semantic call nodes that the current
// index run did not re-write.
//
// Semantic nodes (GRPCCall/HTTPCall/DBCall/CacheCall/OutboxCall/ExternalCall) are
// MERGEd by nodeKey, and the nodeKey embeds detector OUTPUT (target method, table,
// line…). When the source changes — or a detector gets smarter — the fresh run
// writes a node under a NEW key and the node carrying the old, now-wrong answer
// survives forever, so the graph asserts both. (Observed live: mp-dashboard's
// CreatePayout proxy call kept its mis-resolved PayoutProxyService node alongside
// the corrected settlement one after the getter-scanner fix.) Tombstones don't
// cover this: they only overlay PR scopes, never clean the main scope.
//
// Mechanism: every node written this run got `updatedAt >= runStart` via
// MERGE + SET n += props. Anything older under this service's scope was not
// re-emitted by the detectors that just ran — delete it. Only call-site-derived
// labels are swept; definition nodes (Function/File/…) have stable identity keyed
// by signature/path and are handled by the SCIP definition pass itself.
//
// EventType hubs are shared across services, so they are cleaned only when fully
// orphaned (no remaining producer or consumer edge from ANY service).
func SweepStaleCallNodes(
	ctx context.Context, client *neo4j.Client, service, scopeID string, runStart int64,
) (int, error) {
	// callerService (GRPCCall/HTTPCall/OutboxCall/ExternalCall) vs serviceName
	// (DBCall/CacheCall) — coalesce covers both conventions.
	cypher := `
		MATCH (c)
		WHERE (c:GRPCCall OR c:HTTPCall OR c:DBCall OR c:CacheCall OR c:OutboxCall OR c:ExternalCall)
		  AND c.scopeId = $scope
		  AND coalesce(c.callerService, c.serviceName) = $service
		  AND coalesce(c.updatedAt, 0) < $runStart
		WITH c LIMIT 5000
		DETACH DELETE c
		RETURN count(c) AS deleted
	`

	total := 0
	// Batched loop: DETACH DELETE of an unbounded match can blow the heap on a
	// first sweep after a long-lived graph; 5000 per transaction is safe.
	for {
		res, err := client.ExecuteQuery(ctx, cypher, map[string]any{
			"scope":    scopeID,
			"service":  service,
			"runStart": runStart,
		})
		if err != nil {
			return total, fmt.Errorf("stale call-node sweep for %s: %w", service, err)
		}
		n := 0
		if len(res) > 0 {
			if v, ok := res[0].AsMap()["deleted"].(int64); ok {
				n = int(v)
			}
		}
		total += n
		if n < 5000 {
			break
		}
	}

	// Orphaned EventType hubs: every producer AND consumer edge gone (possibly
	// after the sweep above removed the last OutboxCall). Global by design —
	// the hub is shared, so it is stale only when NO service references it.
	orphanCypher := `
		MATCH (e:EventType)
		WHERE NOT ()-[:EMITS_EVENT]->(e) AND NOT (e)-[:ROUTED_TO]->()
		DETACH DELETE e
		RETURN count(e) AS deleted
	`
	if res, err := client.ExecuteQuery(ctx, orphanCypher, nil); err == nil && len(res) > 0 {
		if v, ok := res[0].AsMap()["deleted"].(int64); ok {
			total += int(v)
		}
	}

	return total, nil
}

// hasProtoSources reports whether the project ships .proto contract sources.
// Bounded early-exit walk; vendor/generated dirs are skipped so a Go service
// with vendored protos does not masquerade as the contract repo.
func hasProtoSources(projectPath string) bool {
	found := false
	_ = filepath.WalkDir(projectPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // best-effort probe
		}
		if d.IsDir() {
			switch d.Name() {
			case "vendor", "node_modules", ".git":
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".proto") {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}
