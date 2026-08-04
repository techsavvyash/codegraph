package verify

import (
	"context"
	"fmt"

	neo4j "github.com/context-maximiser/code-graph/internal/graph"
)

// checkContainment implements RFC-013 invariant 3: every Function/Method is
// reachable from a File via CONTAINS. Empirically the graph's CONTAINS
// hierarchy is flat for callables — File -[:CONTAINS]-> Function/Method
// directly, never via an intermediate Class/Interface node (verified against
// the live dev graph: 0 Class-[:CONTAINS]->Method edges exist; Class and
// Interface are structurally siblings of Function/Method under File, not
// their parents). So a single-hop check is both correct and sufficient here.
func checkContainment(ctx context.Context, client *neo4j.Client, sf scopeFilter, sampleLimit int) CheckResult {
	const name = "containment"

	where := sf.whereAnd("(n:Function OR n:Method) AND NOT ()-[:CONTAINS]->(n)")
	cypher := fmt.Sprintf(`
		MATCH (n)
		%s
		RETURN n, labels(n) AS labs
		LIMIT %d
	`, where, sampleLimit)

	records, err := client.ExecuteQuery(ctx, cypher, sf.params)
	if err != nil {
		return CheckResult{Name: name, Status: StatusFail, Detail: fmt.Sprintf("query failed: %v", err)}
	}

	countCypher := fmt.Sprintf(`
		MATCH (n)
		%s
		RETURN count(n) AS c
	`, where)
	countRecs, err := client.ExecuteQuery(ctx, countCypher, sf.params)
	if err != nil {
		return CheckResult{Name: name, Status: StatusFail, Detail: fmt.Sprintf("count query failed: %v", err)}
	}
	var count int64
	if len(countRecs) > 0 {
		count = asInt64(countRecs[0].AsMap()["c"])
	}

	if count == 0 {
		return CheckResult{Name: name, Status: StatusPass, Count: 0}
	}

	var samples []string
	for _, rec := range records {
		nodeAny := rec.AsMap()["n"]
		samples = append(samples, describeNodeAny(nodeAny))
	}

	return CheckResult{
		Name:    name,
		Status:  StatusFail,
		Detail:  fmt.Sprintf("%d Function/Method nodes have no incoming CONTAINS edge from any node", count),
		Count:   count,
		Samples: samples,
	}
}
