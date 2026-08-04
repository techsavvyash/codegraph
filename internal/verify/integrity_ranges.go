package verify

import (
	"context"
	"fmt"

	neo4j "github.com/context-maximiser/code-graph/internal/graph"
)

// rangedLabels are the labels that carry startLine/endLine in
// internal/model/node.go (Class, Interface, Function, Method, Variable,
// Comment). Parameter carries only an index, no range.
var rangedLabels = []string{"Class", "Interface", "Function", "Method", "Variable", "Comment"}

// checkRangeSanity implements RFC-013 invariant 4: where startLine/endLine
// are present, require 0 <= startLine <= endLine. Inverted or negative
// ranges fail.
func checkRangeSanity(ctx context.Context, client *neo4j.Client, sf scopeFilter, sampleLimit int) CheckResult {
	const name = "range-sanity"

	var total int64
	var samples []string
	var offendingLabels []string

	for _, label := range rangedLabels {
		where := sf.whereAnd(`n.startLine IS NOT NULL AND n.endLine IS NOT NULL AND
			(n.startLine < 0 OR n.endLine < 0 OR n.startLine > n.endLine)`)
		cypher := fmt.Sprintf(`
			MATCH (n:%s)
			%s
			RETURN n
			LIMIT %d
		`, neo4j.Ident(label), where, sampleLimit)

		records, err := client.ExecuteQuery(ctx, cypher, sf.params)
		if err != nil {
			return CheckResult{Name: name, Status: StatusFail, Detail: fmt.Sprintf("query failed for label %s: %v", label, err)}
		}

		countCypher := fmt.Sprintf(`
			MATCH (n:%s)
			%s
			RETURN count(n) AS c
		`, neo4j.Ident(label), where)
		countRecs, err := client.ExecuteQuery(ctx, countCypher, sf.params)
		if err != nil {
			return CheckResult{Name: name, Status: StatusFail, Detail: fmt.Sprintf("count query failed for label %s: %v", label, err)}
		}
		var c int64
		if len(countRecs) > 0 {
			c = asInt64(countRecs[0].AsMap()["c"])
		}
		if c == 0 {
			continue
		}
		total += c
		offendingLabels = append(offendingLabels, label)
		for _, rec := range records {
			if int64(len(samples)) >= int64(sampleLimit) {
				break
			}
			samples = append(samples, describeNodeAny(rec.AsMap()["n"]))
		}
	}

	if total == 0 {
		return CheckResult{Name: name, Status: StatusPass, Count: 0}
	}
	return CheckResult{
		Name:    name,
		Status:  StatusFail,
		Detail:  fmt.Sprintf("inverted or negative startLine/endLine found in labels: %v", offendingLabels),
		Count:   total,
		Samples: samples,
	}
}
