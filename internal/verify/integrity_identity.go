package verify

import (
	"context"
	"fmt"

	neo4j "github.com/context-maximiser/code-graph/internal/graph"
)

// identityLabels are the node labels that carry a scopedKey identity
// (mirrors internal/graph/schema.GetConstraints — every label with a
// nodeKey index gets a scopedKey UNIQUE constraint). Hard-coded here rather
// than importing the schema package: the schema constraint list already
// guards these labels once the constraint exists, so this check exists for
// pre-constraint residue and any label that ever loses its constraint —
// importing schema would make the check tautological with schema-presence.
var identityLabels = []string{
	"Service", "File", "Symbol", "Function", "Method", "Class", "Interface",
	"Module", "Variable", "Parameter", "APIRoute", "Document", "DocumentChunk",
	"Feature", "Reference", "Flow", "PullRequest", "GeneratedDoc",
}

// checkIdentityUniqueness implements RFC-013 invariant 2: no duplicate
// scopedKey within a label. UNIQUE constraints on scopedKey exist for every
// label in identityLabels (see schema-presence check), so on a healthy graph
// this always passes; it still guards residue from before a constraint was
// created or a window where it was dropped.
func checkIdentityUniqueness(ctx context.Context, client *neo4j.Client, sf scopeFilter, sampleLimit int) CheckResult {
	const name = "identity-uniqueness"

	var totalDup int64
	var samples []string
	var offendingLabels []string

	for _, label := range identityLabels {
		where := sf.whereAnd("n.scopedKey IS NOT NULL")
		cypher := fmt.Sprintf(`
			MATCH (n:%s)
			%s
			WITH n.scopedKey AS key, count(*) AS c, collect(n)[0..%d] AS sampleNodes
			WHERE c > 1
			RETURN key, c, sampleNodes
			ORDER BY c DESC
			LIMIT 50
		`, neo4j.Ident(label), where, sampleLimit)

		records, err := client.ExecuteQuery(ctx, cypher, sf.params)
		if err != nil {
			return CheckResult{
				Name:   name,
				Status: StatusFail,
				Detail: fmt.Sprintf("query failed for label %s: %v", label, err),
			}
		}
		if len(records) == 0 {
			continue
		}
		offendingLabels = append(offendingLabels, label)
		for _, rec := range records {
			m := rec.AsMap()
			c := asInt64(m["c"])
			totalDup += c
			if int64(len(samples)) < int64(sampleLimit) {
				samples = append(samples, fmt.Sprintf("%s scopedKey=%q duplicated %d times", label, firstString(m, "key"), c))
			}
		}
	}

	if totalDup == 0 {
		return CheckResult{Name: name, Status: StatusPass, Count: 0}
	}
	return CheckResult{
		Name:    name,
		Status:  StatusFail,
		Detail:  fmt.Sprintf("duplicate scopedKey found in labels: %v", offendingLabels),
		Count:   totalDup,
		Samples: samples,
	}
}
