package oracle

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCompareGoCallGraphs_PerfectMatch(t *testing.T) {
	must := map[edgeKey]bool{
		edge(id("p", "", "a"), id("p", "", "b")): true,
		edge(id("p", "T", "m"), id("p", "T", "n")): true,
	}
	may := map[edgeKey]bool{
		edge(id("p", "", "a"), id("p", "", "b")): true,
		edge(id("p", "T", "m"), id("p", "T", "n")): true,
	}
	graph := map[edgeKey]bool{
		edge(id("p", "", "a"), id("p", "", "b")): true,
		edge(id("p", "T", "m"), id("p", "T", "n")): true,
	}

	cmp := compareGoCallGraphs(graph, must, may, nil)

	assert.Equal(t, 1.0, cmp.Recall)
	assert.Empty(t, cmp.Missing)
	assert.Empty(t, cmp.PrecisionSuspects)
}

func TestCompareGoCallGraphs_RemovedGraphEdgeIsRecallGap(t *testing.T) {
	must := map[edgeKey]bool{
		edge(id("p", "", "a"), id("p", "", "b")): true,
		edge(id("p", "T", "m"), id("p", "T", "n")): true,
	}
	may := must
	// Graph is missing the second must-edge entirely.
	graph := map[edgeKey]bool{
		edge(id("p", "", "a"), id("p", "", "b")): true,
	}

	cmp := compareGoCallGraphs(graph, must, may, nil)

	assert.InDelta(t, 0.5, cmp.Recall, 1e-9)
	assert.Len(t, cmp.Missing, 1)
	assert.Equal(t, edge(id("p", "T", "m"), id("p", "T", "n")), cmp.Missing[0])
	assert.Empty(t, cmp.PrecisionSuspects)
}

func TestCompareGoCallGraphs_InventedGraphEdgeIsPrecisionSuspect(t *testing.T) {
	must := map[edgeKey]bool{
		edge(id("p", "", "a"), id("p", "", "b")): true,
	}
	may := must // CHA is at least as big as static here
	// Graph has an edge that isn't in must OR may at all — pure fabrication.
	graph := map[edgeKey]bool{
		edge(id("p", "", "a"), id("p", "", "b")): true,
		edge(id("p", "", "x"), id("p", "", "y")): true,
	}

	cmp := compareGoCallGraphs(graph, must, may, nil)

	assert.Equal(t, 1.0, cmp.Recall, "must-edges are all present, recall unaffected by precision suspects")
	assert.Empty(t, cmp.Missing)
	assert.Len(t, cmp.PrecisionSuspects, 1)
	assert.Equal(t, edge(id("p", "", "x"), id("p", "", "y")), cmp.PrecisionSuspects[0])
}

func TestCompareGoCallGraphs_EmptyMustIsRecallOne(t *testing.T) {
	cmp := compareGoCallGraphs(map[edgeKey]bool{}, map[edgeKey]bool{}, map[edgeKey]bool{}, nil)
	assert.Equal(t, 1.0, cmp.Recall)
	assert.Empty(t, cmp.Missing)
	assert.Empty(t, cmp.PrecisionSuspects)
}

// TestCompareGoCallGraphs_StaleGraphEdgeExcludedFromRecall covers the guard
// added per team-lead review: a missing must-edge whose caller or callee
// was never indexed at all (source added after the last index run) must
// not be reported as a recall gap — it is graph staleness, not an oracle
// finding. Discovered via cmd/codegraph/cmd_verify.go's printReport ->
// printJSON, which postdated the last codegraph index run.
func TestCompareGoCallGraphs_StaleGraphEdgeExcludedFromRecall(t *testing.T) {
	a, b, c := id("p", "", "a"), id("p", "", "b"), id("p", "", "c")

	must := map[edgeKey]bool{
		edge(a, b): true, // both endpoints known — a real gap if missing from graph
		edge(a, c): true, // c is NOT a known node — stale-graph artifact
	}
	may := must
	graph := map[edgeKey]bool{} // graph has neither edge

	knownNodes := map[goFuncID]bool{a: true, b: true} // c deliberately absent

	cmp := compareGoCallGraphs(graph, must, may, knownNodes)

	assert.Equal(t, 1, cmp.StaleGraphEdges, "edge to unknown node c must be excluded, not reported")
	assert.Equal(t, 1, cmp.Comparable, "denominator excludes the stale edge")
	assert.Len(t, cmp.Missing, 1)
	assert.Equal(t, edge(a, b), cmp.Missing[0], "only the edge between two known nodes is a real gap")
	assert.Equal(t, 0.0, cmp.Recall)
}

func TestCompareGoCallGraphs_StaleGraphGuardDisabledWithNilKnownNodes(t *testing.T) {
	must := map[edgeKey]bool{
		edge(id("p", "", "a"), id("p", "", "totally-unindexed")): true,
	}
	cmp := compareGoCallGraphs(map[edgeKey]bool{}, must, must, nil)

	assert.Equal(t, 0, cmp.StaleGraphEdges, "nil knownNodes disables the guard entirely")
	assert.Equal(t, 1, cmp.Comparable)
	assert.Len(t, cmp.Missing, 1)
}
