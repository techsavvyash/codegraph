// fetch.go loads the service's function population, the CALLS ∪ USES_VALUE
// union edges, and the tiered roots — three queries total, all service- and
// scope-bounded ("main" is always admitted alongside an itest scope, matching
// the indexer's own degree query convention).
package reachability

import (
	"context"
	"fmt"
	"strings"

	neo4j "github.com/context-maximiser/code-graph/internal/graph"
)

type rootEntry struct {
	ID   string
	Tier int
}

// isGoAbstractMethodSymbol reports whether a scip-go symbol denotes an
// interface's abstract method slot: descriptor `Type#Method.` with a bare
// trailing dot (concrete methods end `().`). Mirrors the Go oracle's
// isAbstractMethodSymbol rule; guarded to the gomod scheme so TS symbols
// (which parenthesize interface members too) are never misjudged.
func isGoAbstractMethodSymbol(sym string) bool {
	if !strings.Contains(sym, " gomod ") {
		return false
	}
	parts := strings.SplitN(sym, " ", 5)
	if len(parts) != 5 {
		return false
	}
	descriptor := parts[4]
	return strings.Contains(descriptor, "#") &&
		strings.HasSuffix(descriptor, ".") &&
		!strings.HasSuffix(descriptor, "().")
}

func scopeParams(serviceName, scopeID string) map[string]any {
	return map[string]any{"serviceName": serviceName, "scopeId": scopeID}
}

// fetchNodes loads every CONCRETE Function/Method in the service keyed by
// element ID. Abstract interface-method declarations are excluded (returned
// as the second value, a count): they are contracts, not executable code —
// the CALLS fan-out deliberately rewrites calls through them onto concrete
// implementations, so they'd all read as false "dead". Two abstractness
// signals, one per ecosystem:
//
//   - scip-go descriptor shape `Type#Method.` (bare trailing dot, no call
//     parens — same rule as the Go oracle's isAbstractMethodSymbol)
//   - a Method that is the TARGET of a method-level IMPLEMENTS edge
//     (concrete impl -> interface slot, both Go and TS emit this shape)
func fetchNodes(ctx context.Context, client *neo4j.Client, serviceName, scopeID string) (map[string]*FnVerdict, int, error) {
	query := `
		MATCH (fn)
		WHERE (fn:Function OR fn:Method)
		  AND fn.serviceName = $serviceName
		  AND (fn.scopeId = $scopeId OR fn.scopeId = 'main')
		OPTIONAL MATCH (fn)-[:DEFINES]->(sym:Symbol)
		RETURN elementId(fn) AS id,
		       coalesce(fn.nodeKey, '') AS nodeKey,
		       coalesce(fn.name, '') AS name,
		       CASE WHEN fn:Method THEN 'Method' ELSE 'Function' END AS label,
		       coalesce(fn.filePath, '') AS filePath,
		       coalesce(fn.startLine, 0) AS startLine,
		       coalesce(fn.isExported, false) AS isExported,
		       coalesce(sym.symbol, '') AS symbol,
		       EXISTS { MATCH (:Method)-[:IMPLEMENTS]->(fn) } AS isImplTarget
	`
	records, err := client.ExecuteQuery(ctx, query, scopeParams(serviceName, scopeID))
	if err != nil {
		return nil, 0, err
	}
	nodes := make(map[string]*FnVerdict, len(records))
	abstractSkipped := 0
	for _, rec := range records {
		m := rec.AsMap()
		id, _ := m["id"].(string)
		if id == "" {
			continue
		}
		symbol, _ := m["symbol"].(string)
		isImplTarget, _ := m["isImplTarget"].(bool)
		if isImplTarget || isGoAbstractMethodSymbol(symbol) {
			abstractSkipped++
			delete(nodes, id)
			continue
		}
		filePath, _ := m["filePath"].(string)
		name, _ := m["name"].(string)
		nodeKey, _ := m["nodeKey"].(string)
		label, _ := m["label"].(string)
		startLine, _ := m["startLine"].(int64)
		isExported, _ := m["isExported"].(bool)
		nodes[id] = &FnVerdict{
			ID:         id,
			classKey:   classKeyFromSymbol(symbol),
			NodeKey:    nodeKey,
			Name:       name,
			Label:      label,
			FilePath:   filePath,
			StartLine:  startLine,
			IsExported: isExported,
			InTestFile: IsTestFile(filePath),
		}
	}
	return nodes, abstractSkipped, nil
}

// classKeyFromSymbol derives class identity from a SCIP method symbol: the
// descriptor prefix up to the last '#' member separator (`…/UsersService#
// findAll().` → `…/UsersService`). Empty for free functions and symbols
// without a member separator, which opts them out of the constructor rule.
func classKeyFromSymbol(symbol string) string {
	idx := strings.LastIndex(symbol, "#")
	if idx <= 0 {
		return ""
	}
	return symbol[:idx]
}

// fetchEdges loads the union CALLS ∪ USES_VALUE adjacency between the
// service's Function/Method nodes (File-caller edges are NOT loaded here —
// they surface as tier-3 roots instead, since files are treated as
// unconditionally loaded). Returns adjacency plus per-node incoming counts
// (for dead-cluster detection).
func fetchEdges(ctx context.Context, client *neo4j.Client, serviceName, scopeID string) (map[string][]string, map[string]int, error) {
	query := `
		MATCH (a)-[r:CALLS|USES_VALUE]->(b)
		WHERE (a:Function OR a:Method) AND (b:Function OR b:Method)
		  AND a.serviceName = $serviceName AND b.serviceName = $serviceName
		  AND (a.scopeId = $scopeId OR a.scopeId = 'main')
		  AND (b.scopeId = $scopeId OR b.scopeId = 'main')
		  AND a <> b
		RETURN DISTINCT elementId(a) AS fromId, elementId(b) AS toId
	`
	records, err := client.ExecuteQuery(ctx, query, scopeParams(serviceName, scopeID))
	if err != nil {
		return nil, nil, err
	}
	adj := make(map[string][]string, len(records))
	incoming := make(map[string]int, len(records))
	for _, rec := range records {
		m := rec.AsMap()
		from, _ := m["fromId"].(string)
		to, _ := m["toId"].(string)
		if from == "" || to == "" {
			continue
		}
		adj[from] = append(adj[from], to)
		incoming[to]++
	}
	return adj, incoming, nil
}

// fetchRoots collects the tiered liveness roots:
//
//	tier 1 — EXPOSES_API handlers (all detection sources)
//	tier 2 — Go main/init + scheduled tasks + broker consumers
//	tier 3 — module-load execution: targets of (File)-[:CALLS|USES_VALUE]->
//	         (called at import time, or address-taken at module scope)
//
// Deduplication and tier precedence happen in classify (lowest tier wins).
func fetchRoots(ctx context.Context, client *neo4j.Client, serviceName, scopeID string) ([]rootEntry, error) {
	type tierQuery struct {
		tier  int
		query string
	}
	queries := []tierQuery{
		{TierAPIExposed, `
			MATCH (fn)-[:EXPOSES_API]->(:APIRoute)
			WHERE (fn:Function OR fn:Method)
			  AND fn.serviceName = $serviceName
			  AND (fn.scopeId = $scopeId OR fn.scopeId = 'main')
			RETURN DISTINCT elementId(fn) AS id
		`},
		{TierRuntimeMain, `
			MATCH (fn)
			WHERE (fn:Function OR fn:Method)
			  AND fn.serviceName = $serviceName
			  AND (fn.scopeId = $scopeId OR fn.scopeId = 'main')
			  AND (
			        (fn:Function AND fn.name IN ['main', 'init'] AND fn.filePath ENDS WITH '.go')
			     OR coalesce(fn.scheduledTask, false) = true
			     OR coalesce(fn.consumesBroker, false) = true
			  )
			RETURN DISTINCT elementId(fn) AS id
		`},
		{TierModuleLoad, `
			MATCH (f:File)-[:CALLS|USES_VALUE]->(fn)
			WHERE (fn:Function OR fn:Method)
			  AND f.serviceName = $serviceName
			  AND (f.scopeId = $scopeId OR f.scopeId = 'main')
			  AND fn.serviceName = $serviceName
			RETURN DISTINCT elementId(fn) AS id
		`},
	}

	var roots []rootEntry
	for _, tq := range queries {
		records, err := client.ExecuteQuery(ctx, tq.query, scopeParams(serviceName, scopeID))
		if err != nil {
			return nil, err
		}
		for _, rec := range records {
			m := rec.AsMap()
			if id, _ := m["id"].(string); id != "" {
				roots = append(roots, rootEntry{ID: id, Tier: tq.tier})
			}
		}
	}
	return roots, nil
}

// Stamp writes each verdict back onto its node: fn.reachability, and for
// live nodes fn.reachabilityTier + fn.reachabilityRoot (removed otherwise so
// stale values from a previous run can't linger). Before writing, ALL
// reachability props in the service/scope are removed: nodes that left the
// population between runs (abstract declarations once the exclusion landed,
// deleted-and-recreated functions) must not carry a stale verdict — the CHA
// cross-check caught exactly this (45 dead-stamped nodes vs 16 real).
func Stamp(ctx context.Context, client *neo4j.Client, result *Result) error {
	scopeID := result.ScopeID
	if scopeID == "" {
		scopeID = "main"
	}
	clear := `
		MATCH (fn)
		WHERE (fn:Function OR fn:Method)
		  AND fn.serviceName = $serviceName
		  AND (fn.scopeId = $scopeId OR fn.scopeId = 'main')
		  AND fn.reachability IS NOT NULL
		REMOVE fn.reachability, fn.reachabilityTier, fn.reachabilityRoot, fn.deadCluster
	`
	if err := client.ExecuteQueryWithoutRecords(ctx, clear, map[string]any{
		"serviceName": result.ServiceName,
		"scopeId":     scopeID,
	}); err != nil {
		return fmt.Errorf("clear stale verdicts: %w", err)
	}
	updates := make([]map[string]any, 0, len(result.Verdicts))
	for _, v := range result.Verdicts {
		u := map[string]any{
			"id":      v.ID,
			"verdict": string(v.Verdict),
			"cluster": v.DeadCluster,
		}
		if v.Verdict == VerdictLive {
			u["tier"] = v.Tier
			u["root"] = v.RootName
		} else {
			u["tier"] = nil
			u["root"] = nil
		}
		updates = append(updates, u)
	}
	query := `
		UNWIND $updates AS u
		MATCH (fn) WHERE elementId(fn) = u.id
		SET fn.reachability = u.verdict,
		    fn.reachabilityTier = u.tier,
		    fn.reachabilityRoot = u.root,
		    fn.deadCluster = u.cluster
	`
	return client.ExecuteQueryWithoutRecords(ctx, query, map[string]any{"updates": updates})
}
