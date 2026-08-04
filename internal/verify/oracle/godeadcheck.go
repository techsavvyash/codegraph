// godeadcheck.go: RFC-014's CHA cross-check. Every function the reachability
// classifier stamped `dead` must also be unreachable from main in the CHA
// may-call graph — CHA over-approximates dynamic dispatch, so a dead verdict
// on a CHA-reachable function means the classifier (or the graph it read)
// lost an edge. The sandwich discipline again: two independent derivations,
// disagreement = bug.
//
// Reachability is taken from goExtraction.MainReachable, which is computed by
// a RAW BFS over the whole cha.CallGraph (see computeMainReachable in
// goextract.go). It deliberately does NOT reuse the folded MayEdges set: fold()
// drops every cross-module edge, so a reachability walk over MayEdges is
// severed at the first framework hop — main -> cobra.Execute (a different
// module) -> the RunE handler back in ours never connects. That made this
// check vacuous for everything behind the cobra CLI (only ~179 in-module
// identities counted as reachable, essentially just the in-module MCP
// dispatch), silently reporting 0 disagreements where real classifier
// false-deads existed. The raw graph keeps the cross-module and
// synthetic-wrapper hops that make the framework dispatch traversable.
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
