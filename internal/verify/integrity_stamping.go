package verify

import (
	"context"
	"fmt"

	neo4j "github.com/context-maximiser/code-graph/internal/graph"
)

// stampedLabels are code-identity labels expected to carry serviceName and
// scopeId. Class/Interface/Module/Symbol are deliberately excluded: per
// internal/graph/schema.go's comment on file_service_path_idx and friends,
// those labels merge across services on FQN-based nodeKeys (a single
// serviceName would be incoherent for a shared interface), and this is
// confirmed empirically on the dev graph — Class/Interface nodes have no
// serviceName by design, not by omission.
var stampedLabels = []string{"Function", "Method", "File"}

// checkStamping implements RFC-013 invariant 5: code nodes must carry
// serviceName and scopeId.
//
// Scoping by serviceName here can't reuse the ordinary n.serviceName = $svc
// filter: a node MISSING serviceName would trivially be excluded by that
// filter, hiding exactly the nodes this check exists to find. Instead, when
// ServiceName is set, membership is derived structurally via
// Service{name}-[:CONTAINS]->File(-[:CONTAINS]->Function|Method)? so an
// unstamped node belonging to the service is still caught. ScopeID (when
// set) still filters directly on n.scopeId since a missing scopeId is
// exactly as much a finding as a missing serviceName and there is no
// structural fallback for it (scopeId has no analogous containment path).
func checkStamping(ctx context.Context, client *neo4j.Client, opts IntegrityOptions, sampleLimit int) CheckResult {
	const name = "stamping"

	var total int64
	var samples []string
	var offendingLabels []string

	for _, label := range stampedLabels {
		var cypher, countCypher string
		params := map[string]any{"limit": int64(sampleLimit)}
		if opts.ScopeID != "" {
			params["scopeId"] = opts.ScopeID
		}

		unstamped := "(n.serviceName IS NULL OR n.scopeId IS NULL)"
		scopeIDClause := ""
		if opts.ScopeID != "" {
			scopeIDClause = " AND n.scopeId = $scopeId"
		}

		if opts.ServiceName != "" {
			params["serviceName"] = opts.ServiceName
			if label == "File" {
				cypher = fmt.Sprintf(`
					MATCH (svc:Service {name: $serviceName})-[:CONTAINS]->(n:File)
					WHERE %s%s
					RETURN n LIMIT %d
				`, unstamped, scopeIDClause, sampleLimit)
				countCypher = fmt.Sprintf(`
					MATCH (svc:Service {name: $serviceName})-[:CONTAINS]->(n:File)
					WHERE %s%s
					RETURN count(n) AS c
				`, unstamped, scopeIDClause)
			} else {
				cypher = fmt.Sprintf(`
					MATCH (svc:Service {name: $serviceName})-[:CONTAINS]->(:File)-[:CONTAINS]->(n:%s)
					WHERE %s%s
					RETURN n LIMIT %d
				`, neo4j.Ident(label), unstamped, scopeIDClause, sampleLimit)
				countCypher = fmt.Sprintf(`
					MATCH (svc:Service {name: $serviceName})-[:CONTAINS]->(:File)-[:CONTAINS]->(n:%s)
					WHERE %s%s
					RETURN count(n) AS c
				`, neo4j.Ident(label), unstamped, scopeIDClause)
			}
		} else {
			cypher = fmt.Sprintf(`
				MATCH (n:%s)
				WHERE %s%s
				RETURN n LIMIT %d
			`, neo4j.Ident(label), unstamped, scopeIDClause, sampleLimit)
			countCypher = fmt.Sprintf(`
				MATCH (n:%s)
				WHERE %s%s
				RETURN count(n) AS c
			`, neo4j.Ident(label), unstamped, scopeIDClause)
		}

		records, err := client.ExecuteQuery(ctx, cypher, params)
		if err != nil {
			return CheckResult{Name: name, Status: StatusFail, Detail: fmt.Sprintf("query failed for label %s: %v", label, err)}
		}
		countRecs, err := client.ExecuteQuery(ctx, countCypher, params)
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
		offendingLabels = append(offendingLabels, fmt.Sprintf("%s(%d)", label, c))
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
		Detail:  fmt.Sprintf("nodes missing serviceName or scopeId: %v", offendingLabels),
		Count:   total,
		Samples: samples,
	}
}
