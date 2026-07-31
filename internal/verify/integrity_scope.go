package verify

import (
	"context"
	"fmt"
	"os"

	neo4j "github.com/context-maximiser/code-graph/internal/graph"
)

// mainScopeID is the production scope; anything else is transient overlay
// (PR scopes, test residue) and worth surfacing, never worth failing on.
const mainScopeID = "main"

// checkScopeHygiene implements RFC-013 invariant 6: report non-'main'
// scopeId values and their node counts as a WARN — this is how leaked
// itest-* residue or abandoned PR-overlay scopes get noticed. Global by
// design (per the brief: "schema/scope checks stay global") — a leaked
// scope is a whole-database concern, not a single-service one.
func checkScopeHygiene(ctx context.Context, client *neo4j.Client, sampleLimit int) CheckResult {
	const name = "scope-hygiene"

	cypher := `
		MATCH (n)
		WHERE n.scopeId IS NOT NULL AND n.scopeId <> $mainScope
		RETURN n.scopeId AS scopeId, count(*) AS c
		ORDER BY c DESC
		LIMIT $limit
	`
	records, err := client.ExecuteQuery(ctx, cypher, map[string]any{
		"mainScope": mainScopeID,
		"limit":     int64(sampleLimit * 4), // scope-hygiene samples are cheap and informative; show a few more
	})
	if err != nil {
		return CheckResult{Name: name, Status: StatusFail, Detail: fmt.Sprintf("query failed: %v", err)}
	}

	if len(records) == 0 {
		return CheckResult{Name: name, Status: StatusPass, Count: 0}
	}

	var total int64
	var samples []string
	for _, rec := range records {
		m := rec.AsMap()
		c := asInt64(m["c"])
		total += c
		if int64(len(samples)) < int64(sampleLimit) {
			samples = append(samples, fmt.Sprintf("scopeId=%q: %d nodes", firstString(m, "scopeId"), c))
		}
	}

	return CheckResult{
		Name:    name,
		Status:  StatusWarn,
		Detail:  fmt.Sprintf("%d distinct non-main scopeId(s) found (%d nodes total) — likely leaked itest-* or stale PR-overlay residue", len(records), total),
		Count:   total,
		Samples: samples,
	}
}

// checkServiceRootPath implements RFC-013 invariant 8: Service.rootPath that
// doesn't exist on this machine is a WARN, never a fail — graphs move
// between machines/CI runners where the original checkout path is absent.
func checkServiceRootPath(ctx context.Context, client *neo4j.Client, opts IntegrityOptions, sampleLimit int) CheckResult {
	const name = "service-rootpath"

	where := "n.rootPath IS NOT NULL"
	params := map[string]any{}
	if opts.ServiceName != "" {
		where += " AND n.name = $serviceName"
		params["serviceName"] = opts.ServiceName
	}
	if opts.ScopeID != "" {
		where += " AND n.scopeId = $scopeId"
		params["scopeId"] = opts.ScopeID
	}

	cypher := fmt.Sprintf(`
		MATCH (n:Service)
		WHERE %s
		RETURN n.name AS name, n.rootPath AS rootPath
	`, where)
	records, err := client.ExecuteQuery(ctx, cypher, params)
	if err != nil {
		return CheckResult{Name: name, Status: StatusFail, Detail: fmt.Sprintf("query failed: %v", err)}
	}

	var missing []string
	for _, rec := range records {
		m := rec.AsMap()
		rootPath := firstString(m, "rootPath")
		if rootPath == "" {
			continue
		}
		if _, err := os.Stat(rootPath); err != nil {
			missing = append(missing, fmt.Sprintf("Service %s rootPath=%s (%v)", firstString(m, "name"), rootPath, err))
		}
	}

	if len(missing) == 0 {
		return CheckResult{Name: name, Status: StatusPass, Count: 0}
	}

	samples := missing
	if len(samples) > sampleLimit {
		samples = samples[:sampleLimit]
	}
	return CheckResult{
		Name:    name,
		Status:  StatusWarn,
		Detail:  fmt.Sprintf("%d Service node(s) have a rootPath that does not exist on this machine", len(missing)),
		Count:   int64(len(missing)),
		Samples: samples,
	}
}
