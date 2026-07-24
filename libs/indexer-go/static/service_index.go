package static

import (
	"context"
	"fmt"
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
	// Contract-repo services (own .proto contracts, implement no handler) are never
	// runtime call targets and must be excluded from resolution (F1). See
	// loadContractRepoIDs for the rationale.
	contractRepos, err := loadContractRepoIDs(ctx, client, scopeID)
	if err != nil {
		return nil, err
	}

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
		id := getStringFromMap(rm, "id")
		if contractRepos[id] {
			continue // contract repo — not a resolvable runtime service
		}
		b.addService(
			getStringFromMap(rm, "name"),
			getStringFromMap(rm, "packageName"),
			id,
		)
	}

	// Load proto contract → service mappings for precise resolution. Claims whose
	// owning service is a contract repo are skipped: in the shared-proto layout every
	// contract BELONGS_TO the one contract repo, so registering its proto-service
	// names (FXService, AccountService, …) here would make them PRIMARY aliases for
	// the contract repo and shadow the real implementer (F1). In a co-located layout
	// the owning service is a real service (not in contractRepos) and its precise
	// proto→service claims are kept.
	protoQuery := `
		MATCH (pc:ProtoContract)-[:BELONGS_TO]->(s:Service {scopeId: $scopeId})
		RETURN pc.protoPackage AS pkg, pc.protoService AS svc, elementId(s) AS id
	`
	protoRows, _ := client.ExecuteQuery(ctx, protoQuery, map[string]any{"scopeId": scopeID})
	for _, row := range protoRows {
		rm := row.AsMap()
		id := getStringFromMap(rm, "id")
		if contractRepos[id] {
			continue
		}
		b.addProtoContract(
			getStringFromMap(rm, "pkg"),
			getStringFromMap(rm, "svc"),
			id,
		)
	}

	return b.build(), nil
}

// loadContractRepoIDs returns the element-id set of Service nodes that are shared
// proto-contract repositories: they own one or more ProtoContract nodes yet
// implement no gRPC server handler (no "…Server" receiver method). Such a service
// is a contract rendezvous, never a runtime call target — its proto-service names
// belong to the services that IMPLEMENT them, not to the repo that DECLARES them —
// so it is excluded from name/proto resolution entirely (F1). Detection is purely
// structural (owns contracts + implements none): a co-located layout, where a real
// service ships both its .proto files and their handlers, has server methods and is
// therefore NOT excluded, keeping its precise proto→service claims. Returns an
// empty set (resolution unchanged) when no proto contracts are indexed.
func loadContractRepoIDs(ctx context.Context, client *neo4j.Client, scopeID string) (map[string]bool, error) {
	query := `
		MATCH (s:Service {scopeId: $scopeId})
		WHERE EXISTS { (:ProtoContract)-[:BELONGS_TO]->(s) }
		  AND NOT EXISTS {
		    MATCH (s)-[:CONTAINS*1..8]->(h)
		    WHERE (h:Function OR h:Method) AND h.receiverType ENDS WITH 'Server'
		  }
		RETURN elementId(s) AS id
	`
	rows, err := client.ExecuteQuery(ctx, query, map[string]any{"scopeId": scopeID})
	if err != nil {
		return nil, fmt.Errorf("loadContractRepoIDs: %w", err)
	}
	out := make(map[string]bool, len(rows))
	for _, row := range rows {
		if id := getStringFromMap(row.AsMap(), "id"); id != "" {
			out[id] = true
		}
	}
	if len(out) > 0 {
		log.Printf("service index: excluding %d contract-repo service(s) from resolution (own proto contracts, implement no handler)", len(out))
	}
	return out, nil
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
