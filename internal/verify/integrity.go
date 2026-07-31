package verify

import (
	"context"

	neo4j "github.com/context-maximiser/code-graph/internal/graph"
)

// IntegrityOptions scopes an integrity run. Empty ServiceName means the whole
// graph; ScopeID defaults to all scopes (non-main scopes are themselves a
// finding of the scope-hygiene check).
type IntegrityOptions struct {
	ServiceName string
	ScopeID     string
	SampleLimit int // max offender samples per check; 0 → default
}

// RunIntegrity executes the RFC-013 Layer-1 invariant suite against the
// graph. Checks 1-5 (dangling-endpoints, identity-uniqueness, containment,
// range-sanity, stamping) honor ServiceName/ScopeID scoping. Checks 6-7
// (scope-hygiene, schema-presence) are always global — a leaked scope or a
// missing index is a whole-database concern, scoping them would hide the
// exact thing they exist to find. Check 8 (service-rootpath) honors
// ServiceName/ScopeID as a convenience filter over which Service nodes to
// inspect, but its WARN-only verdict is inherently per-Service anyway.
func RunIntegrity(ctx context.Context, client *neo4j.Client, opts IntegrityOptions) (*Report, error) {
	sampleLimit := resolveSampleLimit(opts.SampleLimit)
	sf := newScopeFilter("n", opts.ServiceName, opts.ScopeID)

	scope := opts.ServiceName
	if scope == "" {
		scope = "all"
	}
	report := &Report{Scope: scope}

	// 1. dangling-endpoints (one CheckResult per relationship type)
	for _, cr := range checkDanglingEndpoints(ctx, client, sf, sampleLimit) {
		report.Add(cr)
	}

	// 2. identity-uniqueness
	report.Add(checkIdentityUniqueness(ctx, client, sf, sampleLimit))

	// 3. containment
	report.Add(checkContainment(ctx, client, sf, sampleLimit))

	// 4. range-sanity
	report.Add(checkRangeSanity(ctx, client, sf, sampleLimit))

	// 5. stamping
	report.Add(checkStamping(ctx, client, opts, sampleLimit))

	// 6. scope-hygiene (global)
	report.Add(checkScopeHygiene(ctx, client, sampleLimit))

	// 7. schema-presence (global)
	report.Add(checkSchemaPresence(ctx, client, sampleLimit))

	// 8. service-rootpath
	report.Add(checkServiceRootPath(ctx, client, opts, sampleLimit))

	return report, nil
}
