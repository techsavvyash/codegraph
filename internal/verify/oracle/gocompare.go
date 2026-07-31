package oracle

import "sort"

// goComparison is the pure-function outcome of comparing graph, must, and
// may edge sets — independently testable without Neo4j or SSA, per the
// sandwich principle: must ⊆ graph ⊆ may.
type goComparison struct {
	// Comparable is the number of must-edges counted as the recall
	// denominator: must-edges whose endpoints both exist as known nodes in
	// the graph. A must-edge with an endpoint the graph has never indexed
	// (source added after the last index run — graph staleness, not an
	// oracle finding) is excluded from both the denominator and Missing,
	// and counted in StaleGraphEdges instead.
	Comparable        int
	Missing           []edgeKey // must-edges absent from graph, both endpoints known (real recall gaps)
	PrecisionSuspects []edgeKey // graph edges absent from may (fabrication suspects)
	StaleGraphEdges   int       // must-edges skipped because an endpoint isn't a known graph node
	Recall            float64
}

// compareGoCallGraphs applies the sandwich principle to three independently
// built edge sets. knownNodes gates the stale-graph guard: a missing
// must-edge only counts as a real recall gap if BOTH its caller and callee
// exist as nodes in knownNodes (mirroring the TS oracle's nodeExists
// exclusion) — otherwise the graph simply hasn't been re-indexed since that
// source was added/changed, which is drift-layer territory, not an oracle
// precision/recall finding. A nil or empty knownNodes disables the guard
// (every must-edge is treated as comparable), which is what
// TestCompareGoCallGraphs_PerfectMatch and friends rely on.
//
// sampleLimit caps how many mismatch samples are retained in the returned
// slices (0 means "use a small sane default" — callers needing the true
// count should use len(missing)/len(suspects) computed before truncation,
// which the oracle orchestrator does separately).
func compareGoCallGraphs(graphEdges, mustEdges, mayEdges map[edgeKey]bool, knownNodes map[goFuncID]bool) *goComparison {
	cmp := &goComparison{}

	for k := range mustEdges {
		if len(knownNodes) > 0 && (!knownNodes[k.from] || !knownNodes[k.to]) {
			cmp.StaleGraphEdges++
			continue
		}
		cmp.Comparable++
		if !graphEdges[k] {
			cmp.Missing = append(cmp.Missing, k)
		}
	}
	for k := range graphEdges {
		if !mayEdges[k] {
			cmp.PrecisionSuspects = append(cmp.PrecisionSuspects, k)
		}
	}

	sortEdgeKeys(cmp.Missing)
	sortEdgeKeys(cmp.PrecisionSuspects)

	if cmp.Comparable == 0 {
		cmp.Recall = 1.0
	} else {
		cmp.Recall = 1.0 - float64(len(cmp.Missing))/float64(cmp.Comparable)
	}

	return cmp
}

// sortEdgeKeys gives deterministic ordering to edge-key slices derived from
// map iteration, so sample output (and test assertions) are stable.
func sortEdgeKeys(keys []edgeKey) {
	sort.Slice(keys, func(i, j int) bool {
		return edgeKeyLess(keys[i], keys[j])
	})
}

func edgeKeyLess(a, b edgeKey) bool {
	if af, bf := funcIDString(a.from), funcIDString(b.from); af != bf {
		return af < bf
	}
	return funcIDString(a.to) < funcIDString(b.to)
}

func funcIDString(id goFuncID) string {
	if id.typeName == "" {
		return id.pkgPath + "/" + id.funcName
	}
	return id.pkgPath + "/" + id.typeName + "#" + id.funcName
}
