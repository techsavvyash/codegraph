package verify

import (
	"context"
	"fmt"

	driver "github.com/neo4j/neo4j-go-driver/v5/neo4j"

	neo4j "github.com/context-maximiser/code-graph/internal/graph"
	schema "github.com/context-maximiser/code-graph/internal/graph/schema"
)

// checkSchemaPresence implements RFC-013 invariant 7: required constraints,
// indexes, fulltext indexes, and the built-in LOOKUP indexes must exist.
// The expected set is read directly from internal/graph/schema/schema.go
// (GetConstraints/GetIndexes/GetFulltextIndexes) rather than hard-coded here
// — that package is the single source of truth for what CreateSchema
// creates, so this check can never drift from it. The LOOKUP indexes are
// checked separately by fixed expectation (exactly one NODE + one
// RELATIONSHIP lookup index): they are Neo4j built-ins, not part of the
// schema package's DDL list, and their disappearance (via a DropSchema that
// swept them up) was the incident this check exists to catch — see
// rfc/013-graph-correctness.md incident 4 and schema.go's ensureLookupIndexes.
func checkSchemaPresence(ctx context.Context, client *neo4j.Client, sampleLimit int) CheckResult {
	const name = "schema-presence"

	constraintRecs, err := client.ExecuteQuery(ctx, "SHOW CONSTRAINTS YIELD name RETURN name", nil)
	if err != nil {
		return CheckResult{Name: name, Status: StatusFail, Detail: fmt.Sprintf("SHOW CONSTRAINTS failed: %v", err)}
	}
	existingConstraints := toNameSet(constraintRecs)

	indexRecs, err := client.ExecuteQuery(ctx, "SHOW INDEXES YIELD name, type RETURN name, type", nil)
	if err != nil {
		return CheckResult{Name: name, Status: StatusFail, Detail: fmt.Sprintf("SHOW INDEXES failed: %v", err)}
	}
	existingIndexes := map[string]bool{}
	for _, rec := range indexRecs {
		existingIndexes[firstString(rec.AsMap(), "name")] = true
	}

	var missing []string

	for _, c := range schema.GetConstraints() {
		if !existingConstraints[c.Name] {
			missing = append(missing, fmt.Sprintf("constraint %s (FOR %s.%s IS UNIQUE)", c.Name, c.NodeLabel, c.Property))
		}
	}
	for _, idx := range schema.GetIndexes() {
		if !existingIndexes[idx.Name] {
			missing = append(missing, fmt.Sprintf("index %s (%s.%v)", idx.Name, idx.NodeLabel, idx.Properties))
		}
	}
	for _, idx := range schema.GetFulltextIndexes() {
		if !existingIndexes[idx.Name] {
			missing = append(missing, fmt.Sprintf("fulltext index %s (%s.%v)", idx.Name, idx.NodeLabel, idx.Properties))
		}
	}

	// Built-in LOOKUP indexes: incident 4 in RFC-013 — a DropSchema swept
	// these away and every labeled MATCH silently degraded to AllNodesScan.
	// SHOW INDEXES exposes entityType so we can tell node-label vs
	// relationship-type lookup indexes apart and require exactly one of each.
	lookupRecs, err := client.ExecuteQuery(ctx, "SHOW INDEXES YIELD name, type, entityType WHERE type = 'LOOKUP' RETURN name, entityType", nil)
	if err != nil {
		return CheckResult{Name: name, Status: StatusFail, Detail: fmt.Sprintf("SHOW INDEXES (LOOKUP) failed: %v", err)}
	}
	nodeLookup, relLookup := false, false
	for _, rec := range lookupRecs {
		m := rec.AsMap()
		switch firstString(m, "entityType") {
		case "NODE":
			nodeLookup = true
		case "RELATIONSHIP":
			relLookup = true
		}
	}
	if !nodeLookup {
		missing = append(missing, "LOOKUP index on node labels (expected exactly one, entityType=NODE)")
	}
	if !relLookup {
		missing = append(missing, "LOOKUP index on relationship types (expected exactly one, entityType=RELATIONSHIP)")
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
		Status:  StatusFail,
		Detail:  fmt.Sprintf("%d expected constraint(s)/index(es) are missing", len(missing)),
		Count:   int64(len(missing)),
		Samples: samples,
	}
}

func toNameSet(records []*driver.Record) map[string]bool {
	set := make(map[string]bool, len(records))
	for _, rec := range records {
		set[firstString(rec.AsMap(), "name")] = true
	}
	return set
}
