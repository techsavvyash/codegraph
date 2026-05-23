package static

import (
	"context"
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

	idx := &serviceIndex{
		byName:  make(map[string]string, len(rows)*4),
		byProto: make(map[string]string, len(rows)*2),
	}

	for _, row := range rows {
		rm := row.AsMap()
		name := getStringFromMap(rm, "name")
		pkg := getStringFromMap(rm, "packageName")
		id := getStringFromMap(rm, "id")
		if id == "" {
			continue
		}

		for _, alias := range serviceAliases(name) {
			if alias != "" {
				idx.byName[alias] = id
			}
		}
		for _, alias := range serviceAliases(pkg) {
			if alias != "" {
				idx.byProto[alias] = id
				idx.byName[alias] = id
			}
		}
	}

	return idx, nil
}

func (si *serviceIndex) resolveByName(name string) string {
	if si == nil || name == "" {
		return ""
	}
	for _, alias := range serviceAliases(name) {
		if id := si.byName[alias]; id != "" {
			return id
		}
	}

	// Fallback to fuzzy in-memory contains matching to retain old behavior.
	needle := canonicalServiceKey(name)
	if needle == "" {
		return ""
	}
	for alias, id := range si.byName {
		if strings.Contains(alias, needle) || strings.Contains(needle, alias) {
			return id
		}
	}
	return ""
}

func (si *serviceIndex) resolveByProto(pkg string) string {
	if si == nil || pkg == "" {
		return ""
	}
	for _, alias := range serviceAliases(pkg) {
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

func serviceAliases(raw string) []string {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return nil
	}

	aliasSet := map[string]bool{}
	add := func(s string) {
		if s = strings.TrimSpace(strings.ToLower(s)); s != "" {
			aliasSet[s] = true
		}
		if c := canonicalServiceKey(s); c != "" {
			aliasSet[c] = true
			aliasSet[strings.TrimSuffix(c, "service")] = true
		}
	}

	add(raw)
	for _, seg := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == '/' || r == '.' || r == '-' || r == '_'
	}) {
		add(seg)
	}

	aliases := make([]string, 0, len(aliasSet))
	for alias := range aliasSet {
		if alias != "" {
			aliases = append(aliases, alias)
		}
	}
	return aliases
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
