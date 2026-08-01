// godeadcheck.go: RFC-014's CHA cross-check. Every function the reachability
// classifier stamped `dead` must also be unreachable from main in the CHA
// may-call graph — CHA over-approximates dynamic dispatch, so a dead verdict
// on a CHA-reachable function means the classifier (or the graph it read)
// lost an edge. The sandwich discipline again: two independent derivations,
// disagreement = bug.
package oracle

import (
	"context"
	"fmt"
	"sort"

	neo4j "github.com/context-maximiser/code-graph/internal/graph"
)

// fetchDeadStampedSymbols returns the SCIP symbols of Function/Method nodes
// stamped reachability='dead' for the service. Empty when the classifier has
// not run — the cross-check then reports "no verdicts" rather than passing
// vacuously.
func fetchDeadStampedSymbols(ctx context.Context, client *neo4j.Client, serviceName, scopeID string) ([]string, error) {
	cypher := `
		MATCH (fn)
		WHERE (fn:Function OR fn:Method)
		  AND fn.serviceName = $serviceName
		  AND (fn.scopeId = $scopeId OR fn.scopeId = 'main')
		  AND fn.reachability = 'dead'
		MATCH (fn)-[:DEFINES]->(sym:Symbol)
		RETURN sym.symbol AS symbol
	`
	if scopeID == "" {
		scopeID = "main"
	}
	records, err := client.ExecuteQuery(ctx, cypher, map[string]any{
		"serviceName": serviceName,
		"scopeId":     scopeID,
	})
	if err != nil {
		return nil, fmt.Errorf("fetch dead-stamped symbols: %w", err)
	}
	symbols := make([]string, 0, len(records))
	for _, rec := range records {
		raw, _ := rec.Get("symbol")
		if s, _ := raw.(string); s != "" {
			symbols = append(symbols, s)
		}
	}
	return symbols, nil
}

// chaReachableFromMain BFS-expands the CHA may-edge set from every package
// `main` function (the roots a compiled binary actually starts from) and
// returns the reachable identity set. Synthetic init/closure endpoints were
// already folded/excluded during extraction, so this is a plain traversal
// over named-function identities.
func chaReachableFromMain(mayEdges map[edgeKey]bool) map[goFuncID]bool {
	adj := make(map[goFuncID][]goFuncID, len(mayEdges))
	nodes := make(map[goFuncID]bool, len(mayEdges)*2)
	for k := range mayEdges {
		adj[k.from] = append(adj[k.from], k.to)
		nodes[k.from] = true
		nodes[k.to] = true
	}

	reachable := make(map[goFuncID]bool)
	var queue []goFuncID
	for id := range nodes {
		if id.funcName == "main" && id.typeName == "" {
			reachable[id] = true
			queue = append(queue, id)
		}
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, next := range adj[cur] {
			if !reachable[next] {
				reachable[next] = true
				queue = append(queue, next)
			}
		}
	}
	return reachable
}

// crossCheckDeadVerdicts compares dead-stamped symbols against CHA
// reachability and returns the disagreement identities, sorted for
// deterministic output. Symbols that don't parse to a callable identity
// (abstract slots etc.) are skipped — they can't appear in the CHA graph.
func crossCheckDeadVerdicts(deadSymbols []string, chaReachable map[goFuncID]bool) []goFuncID {
	var disagreements []goFuncID
	for _, sym := range deadSymbols {
		id, ok := callableFuncID(sym)
		if !ok {
			continue
		}
		if chaReachable[id] {
			disagreements = append(disagreements, id)
		}
	}
	sort.Slice(disagreements, func(i, j int) bool {
		return funcIDString(disagreements[i]) < funcIDString(disagreements[j])
	})
	return disagreements
}
