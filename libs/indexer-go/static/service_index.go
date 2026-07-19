package static

import (
	"context"
	"log"
	"slices"
	"sort"
	"strings"

	"github.com/context-maximiser/code-graph/libs/neo4j-go"
)

// serviceIndex is an in-memory lookup table for indexed services in a scope.
// It replaces per-call fuzzy Neo4j lookups with fast local map resolution.
type serviceIndex struct {
	byName  map[string]string // normalized alias -> Service elementId
	byProto map[string]string // normalized proto/package alias -> Service elementId
}

// loadServiceIndex loads all services in the scope once and builds lookup maps.
func loadServiceIndex(ctx context.Context, client *neo4j.Client, scopeID string) (*serviceIndex, error) {
	query := `
		MATCH (s:Service {scopeId: $scopeId})
		RETURN s.name AS name,
		       coalesce(s.packageName, '') AS packageName,
		       elementId(s) AS id
	`
	rows, err := client.ExecuteQuery(ctx, query, map[string]any{"scopeId": scopeID})
	if err != nil {
		return nil, err
	}

	b := newServiceIndexBuilder()
	for _, row := range rows {
		rm := row.AsMap()
		b.addService(
			getStringFromMap(rm, "name"),
			getStringFromMap(rm, "packageName"),
			getStringFromMap(rm, "id"),
		)
	}

	// Load proto contract → service mappings for precise resolution.
	protoQuery := `
		MATCH (pc:ProtoContract)-[:BELONGS_TO]->(s:Service {scopeId: $scopeId})
		RETURN pc.protoPackage AS pkg, pc.protoService AS svc, elementId(s) AS id
	`
	protoRows, _ := client.ExecuteQuery(ctx, protoQuery, map[string]any{"scopeId": scopeID})
	for _, row := range protoRows {
		rm := row.AsMap()
		b.addProtoContract(
			getStringFromMap(rm, "pkg"),
			getStringFromMap(rm, "svc"),
			getStringFromMap(rm, "id"),
		)
	}

	return b.build(), nil
}

// serviceIndexBuilder accumulates alias claims during load and arbitrates them
// in build(). An alias is claimed either as PRIMARY (the full lowered name /
// canonical key — unique per service by construction) or as a SEGMENT (one
// split word of the name). Segments collide across services —
// "settlement-orchestration" also yields "settlement", every proto package
// yields "grpc" and "v1", three dashboards yield "dashboard" — and the old
// byName[alias]=id blind overwrite made resolution depend on Service row load
// order (nondeterministic). Arbitration rules (P0-7): a primary claim owns its
// alias outright (a segment never overrides a full name); an uncontested
// segment keeps its sole claimant; contested aliases are DROPPED so a miss
// yields no edge, never a wrong edge.
type serviceIndexBuilder struct {
	nameClaims  *aliasClaims
	protoClaims *aliasClaims
}

func newServiceIndexBuilder() *serviceIndexBuilder {
	return &serviceIndexBuilder{
		nameClaims:  newAliasClaims(),
		protoClaims: newAliasClaims(),
	}
}

// addService registers a Service row (name + optional package name).
func (b *serviceIndexBuilder) addService(name, pkg, id string) {
	if id == "" {
		return
	}
	b.nameClaims.add(name, id)
	if pkg != "" {
		b.protoClaims.add(pkg, id)
		b.nameClaims.add(pkg, id)
	}
}

// addProtoContract registers a ProtoContract row (proto package + proto service name).
func (b *serviceIndexBuilder) addProtoContract(pkg, svc, id string) {
	if id == "" {
		return
	}
	if pkg != "" {
		b.protoClaims.add(pkg, id)
		b.nameClaims.add(pkg, id)
	}
	if svc != "" {
		b.protoClaims.add(svc, id)
		b.nameClaims.add(svc, id)
	}
}

func (b *serviceIndexBuilder) build() *serviceIndex {
	return &serviceIndex{
		byName:  b.nameClaims.finalize("byName"),
		byProto: b.protoClaims.finalize("byProto"),
	}
}

// aliasClaims tracks, per alias, the set of service ids claiming it and which
// id (if any) claims it as primary. primary[alias] == "" is the conflict
// sentinel: two different services claimed the same primary alias (e.g. the
// same proto service name declared in two repos) — ambiguous, resolves to
// nothing.
type aliasClaims struct {
	ids     map[string]map[string]bool
	primary map[string]string
}

func newAliasClaims() *aliasClaims {
	return &aliasClaims{
		ids:     make(map[string]map[string]bool),
		primary: make(map[string]string),
	}
}

func (a *aliasClaims) add(raw, id string) {
	for _, alias := range primaryServiceAliases(raw) {
		a.claim(alias, id)
		if owner, claimed := a.primary[alias]; !claimed {
			a.primary[alias] = id
		} else if owner != id {
			a.primary[alias] = "" // primary vs primary conflict — ambiguous
		}
	}
	for _, alias := range segmentServiceAliases(raw) {
		a.claim(alias, id)
	}
}

func (a *aliasClaims) claim(alias, id string) {
	if alias == "" {
		return
	}
	set := a.ids[alias]
	if set == nil {
		set = make(map[string]bool)
		a.ids[alias] = set
	}
	set[id] = true
}

// finalize flattens the claims into the lookup map, dropping contested aliases
// (logged once per load, P3-1 style) per the P0-7 arbitration rules.
func (a *aliasClaims) finalize(kind string) map[string]string {
	out := make(map[string]string, len(a.ids))
	var contested []string
	for alias, ids := range a.ids {
		owner, isPrimary := a.primary[alias]
		switch {
		case isPrimary && owner != "":
			out[alias] = owner
		case isPrimary: // conflict sentinel — two services share this primary alias
			contested = append(contested, alias)
		case len(ids) == 1:
			for id := range ids {
				out[alias] = id
			}
		default:
			contested = append(contested, alias)
		}
	}
	if len(contested) > 0 {
		sort.Strings(contested)
		log.Printf("Warning: service index (%s): dropped %d contested alias(es) claimed by multiple services: %s",
			kind, len(contested), strings.Join(contested, ", "))
	}
	return out
}

func (si *serviceIndex) resolveByName(name string) string {
	if si == nil || name == "" {
		return ""
	}
	// Primary forms strictly before segment forms, each in deterministic order —
	// "settlement-orchestration" can never bind via its "settlement" segment
	// before its own full/canonical alias is tried (P0-7). Exact map hits only:
	// no substring, no prefix (P0-5 — prefix matching mis-bound
	// "settlementpricingservice" to settlement on live data). When every form
	// misses, the correct outcome is NO edge, not a guess — the authoritative
	// getter table (ScanGRPCClientGetters) supplies the real owner for every
	// legitimate case.
	for _, alias := range primaryServiceAliases(name) {
		if id := si.byName[alias]; id != "" {
			return id
		}
	}
	for _, alias := range segmentServiceAliases(name) {
		if id := si.byName[alias]; id != "" {
			return id
		}
	}
	return ""
}

func (si *serviceIndex) resolveByProto(pkg string) string {
	if si == nil || pkg == "" {
		return ""
	}
	for _, alias := range primaryServiceAliases(pkg) {
		if id := si.byProto[alias]; id != "" {
			return id
		}
	}
	for _, alias := range segmentServiceAliases(pkg) {
		if id := si.byProto[alias]; id != "" {
			return id
		}
	}
	return si.resolveByName(pkg)
}

func (si *serviceIndex) resolveFromURL(rawURL string) string {
	if si == nil || rawURL == "" {
		return ""
	}
	host := strings.ToLower(strings.TrimSpace(extractHostFromURL(rawURL)))
	if host == "" {
		return ""
	}

	candidates := []string{host}
	parts := strings.Split(host, ".")
	for _, p := range parts {
		if p != "" {
			candidates = append(candidates, p)
		}
	}
	if len(parts) > 0 && parts[0] != "" {
		candidates = append(candidates, parts[0])
	}

	for _, c := range candidates {
		if id := si.resolveByName(c); id != "" {
			return id
		}
	}
	return ""
}

// primaryServiceAliases returns the alias forms that identify a service
// uniquely by construction: the full lowered name, its canonical key
// (alphanumerics only), and the canonical key minus a trailing "service".
// Deterministic order, no duplicates.
func primaryServiceAliases(raw string) []string {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return nil
	}
	return appendAliasForms(nil, raw)
}

// segmentServiceAliases returns the per-segment alias forms of a name split on
// '/', '.', '-', '_'. Segments are NOT unique across services; the index build
// arbitrates contested ones (P0-7). Deterministic order, no duplicates.
func segmentServiceAliases(raw string) []string {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return nil
	}
	var out []string
	for _, seg := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == '/' || r == '.' || r == '-' || r == '_'
	}) {
		out = appendAliasForms(out, seg)
	}
	return out
}

// appendAliasForms appends s, canonical(s), and canonical(s) minus a trailing
// "service" to out, skipping empties and values already present.
func appendAliasForms(out []string, s string) []string {
	add := func(v string) {
		if v == "" || slices.Contains(out, v) {
			return
		}
		out = append(out, v)
	}
	add(s)
	if c := canonicalServiceKey(s); c != "" {
		add(c)
		add(strings.TrimSuffix(c, "service"))
	}
	return out
}

func canonicalServiceKey(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(raw))
	for _, r := range raw {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}
