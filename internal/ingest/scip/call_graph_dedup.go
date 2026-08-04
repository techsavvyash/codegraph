package static

// minLineEdge is a builder-agnostic (caller, target) call-site triple: which
// function called which, and at what line. Both call-graph builders
// (SCIPCallGraphBuilder for Go, GenericCallGraphBuilder for everything else)
// resolve raw references to an enclosing caller using different range-search
// algorithms (AST body ranges vs. declaration-order inference), but once a
// reference has been resolved to (callerID, targetID, line), the problem of
// collapsing multiple call sites between the same pair down to one edge is
// identical — hence this shared type and helper.
type minLineEdge struct {
	CallerID string
	TargetID string
	Line     int
}

// collapseToMinLinePerPair collapses (caller, target, line) triples to one
// entry per distinct (caller, target) pair, keeping the smallest line.
//
// This must not depend on the order triples arrive in: Neo4j does not
// guarantee row order for a query without ORDER BY, so if a caller invokes
// the same target from two lines, "first row wins" would make the resulting
// CALLS.line property flip nondeterministically between indexing runs
// (observed via test/fixtures/tiny-go, where main() calls greet() from two
// call sites). Picking the minimum line is deterministic regardless of
// input order and matches "the first call site in the file".
//
// Self-calls (CallerID == TargetID, i.e. recursion) are KEPT as real CALLS
// edges — dropping them (the pre-RFC-013 behavior) made every recursive
// function structurally indistinguishable from an uncalled one to any
// downstream "has a caller" / "no incoming CALLS" consumer, silently
// hiding real self-recursive edges from the graph. Callers that treat
// "has an incoming CALLS edge" as "is used/reachable" must exclude
// self-edges explicitly at the point of interpretation (a recursive
// function with no EXTERNAL caller is still unreachable/dead) — see
// internal/ingest/scip's scipDegreeQuery/genericDegreeQuery, which exclude
// self-loops from the stored inDegree/outDegree properties, alongside
// this change.
//
// Pure function, no I/O — testable without Neo4j.
func collapseToMinLinePerPair(triples []minLineEdge) []minLineEdge {
	edges := make(map[string]*minLineEdge, len(triples))
	order := make([]string, 0, len(triples))

	for _, t := range triples {
		key := t.CallerID + "->" + t.TargetID
		if existing, ok := edges[key]; ok {
			if t.Line < existing.Line {
				existing.Line = t.Line
			}
			continue
		}
		cp := t
		edges[key] = &cp
		order = append(order, key)
	}

	out := make([]minLineEdge, 0, len(order))
	for _, key := range order {
		out = append(out, *edges[key])
	}
	return out
}
