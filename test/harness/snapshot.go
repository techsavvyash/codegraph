// Package harness provides deterministic graph snapshots for golden tests.
package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	neo4j "github.com/context-maximiser/code-graph/internal/graph"
)

// Snapshot is a canonical, comparable view of a Neo4j subgraph scoped by scopeId.
type Snapshot struct {
	Nodes []Node `json:"nodes"`
	Rels  []Rel  `json:"relationships"`
}

// Node is a single graph node, identified by (sorted labels, nodeKey).
type Node struct {
	Labels  []string       `json:"labels"`
	NodeKey string         `json:"nodeKey"`
	Props   map[string]any `json:"props"`
}

// Rel is a relationship. StartLabels/EndLabels distinguish edges that share endpoint nodeKeys
// across different label sets.
type Rel struct {
	Type        string         `json:"type"`
	StartLabels []string       `json:"startLabels"`
	StartKey    string         `json:"startKey"`
	EndLabels   []string       `json:"endLabels"`
	EndKey      string         `json:"endKey"`
	Props       map[string]any `json:"props"`
}

// Options controls what gets dumped.
type Options struct {
	// ScopeID filters to nodes/rels with this scopeId. Empty matches everything.
	ScopeID string
	// IgnoreNodeProps are property keys stripped from every node before comparison.
	// Use for volatile fields (createdAt, updatedAt, hash, etc.) when needed.
	IgnoreNodeProps []string
	// IgnoreRelProps are property keys stripped from every relationship.
	IgnoreRelProps []string
	// IgnoreLabels skips nodes whose labels include any of these (e.g. "GenerationDiagnostic").
	IgnoreLabels []string
	// Owned, when non-nil, restricts the dump to nodes the fixture owns, so a
	// golden test produces identical output whether the database is otherwise
	// empty or holds an unrelated dev graph in the same scope. Relationships
	// require BOTH endpoints owned.
	Owned *Ownership
}

// Ownership defines which nodes belong to a fixture. A node is owned when its
// serviceName matches one of Services (exactly or as a "<svc>/" sub-service),
// it is a Service node with such a name, its nodeKey contains one of Markers,
// or it is directly connected to a service-owned node — the last leg claims
// the shared, serviceName-less node classes (stdlib Symbols referenced by
// fixture code, structural APIRoute/SDKCall keyed on bare paths) whose
// properties are identical regardless of who else shares them.
type Ownership struct {
	Services []string
	Markers  []string
}

// ownedPredicate renders the ownership WHERE fragment for node variable v.
// Params ownedServices/ownedMarkers must be bound by the caller.
func ownedPredicate(v string) string {
	return fmt.Sprintf(`(
	   (%[1]s.serviceName IS NOT NULL
	    AND any(svc IN $ownedServices WHERE %[1]s.serviceName = svc OR %[1]s.serviceName STARTS WITH svc + '/'))
	OR (%[1]s:Service AND any(svc IN $ownedServices WHERE %[1]s.name = svc OR %[1]s.name STARTS WITH svc + '/'))
	OR (%[1]s.nodeKey IS NOT NULL AND any(m IN $ownedMarkers WHERE %[1]s.nodeKey CONTAINS m))
	OR EXISTS {
	     MATCH (%[1]s)--(nbr)
	     WHERE nbr.serviceName IS NOT NULL
	       AND any(svc IN $ownedServices WHERE nbr.serviceName = svc OR nbr.serviceName STARTS WITH svc + '/')
	   }
	)`, v)
}

// Dump queries Neo4j and returns a canonical Snapshot. Output is sorted so that two
// runs against equivalent graphs produce byte-identical JSON.
func Dump(ctx context.Context, client *neo4j.Client, opts Options) (*Snapshot, error) {
	nodes, err := dumpNodes(ctx, client, opts)
	if err != nil {
		return nil, fmt.Errorf("dump nodes: %w", err)
	}
	rels, err := dumpRels(ctx, client, opts)
	if err != nil {
		return nil, fmt.Errorf("dump rels: %w", err)
	}
	return &Snapshot{Nodes: nodes, Rels: rels}, nil
}

func dumpNodes(ctx context.Context, client *neo4j.Client, opts Options) ([]Node, error) {
	cypher := "MATCH (n) "
	params := map[string]any{}
	var conds []string
	if opts.ScopeID != "" {
		conds = append(conds, "n.scopeId = $scopeId")
		params["scopeId"] = opts.ScopeID
	}
	if opts.Owned != nil {
		conds = append(conds, ownedPredicate("n"))
		params["ownedServices"] = opts.Owned.Services
		params["ownedMarkers"] = opts.Owned.Markers
	}
	if len(conds) > 0 {
		cypher += "WHERE " + joinConds(conds) + " "
	}
	cypher += "RETURN labels(n) AS labels, n.nodeKey AS nodeKey, properties(n) AS props"

	records, err := client.ExecuteQuery(ctx, cypher, params)
	if err != nil {
		return nil, err
	}

	ignoreLabel := stringSet(opts.IgnoreLabels)
	ignoreProp := stringSet(opts.IgnoreNodeProps)

	out := make([]Node, 0, len(records))
	for _, r := range records {
		labels, _ := asStrings(r.Values[0])
		if anyInSet(labels, ignoreLabel) {
			continue
		}
		nodeKey, _ := r.Values[1].(string)
		props, _ := r.Values[2].(map[string]any)

		sort.Strings(labels)
		out = append(out, Node{
			Labels:  labels,
			NodeKey: nodeKey,
			Props:   stripProps(props, ignoreProp),
		})
	}

	sort.Slice(out, func(i, j int) bool {
		if !equalStrings(out[i].Labels, out[j].Labels) {
			return joinStrings(out[i].Labels) < joinStrings(out[j].Labels)
		}
		return out[i].NodeKey < out[j].NodeKey
	})
	return out, nil
}

func dumpRels(ctx context.Context, client *neo4j.Client, opts Options) ([]Rel, error) {
	var cypher string
	params := map[string]any{}
	if opts.Owned != nil {
		// Evaluating the ownership predicate (with its EXISTS neighbor walk)
		// on both endpoints of every relationship in a populated dev graph
		// times out the transaction. Collect the owned node set once — one
		// pass over nodes, same cost as dumpNodes — then expand relationships
		// only from those nodes and membership-test the far endpoint.
		relCond := "b IN owned"
		if opts.ScopeID != "" {
			relCond += " AND r.scopeId = $scopeId"
			params["scopeId"] = opts.ScopeID
		}
		cypher = "MATCH (a) WHERE " + ownedPredicate("a") + `
			WITH collect(a) AS owned
			UNWIND owned AS a
			MATCH (a)-[r]->(b)
			WHERE ` + relCond + " "
		params["ownedServices"] = opts.Owned.Services
		params["ownedMarkers"] = opts.Owned.Markers
	} else {
		cypher = "MATCH (a)-[r]->(b) "
		if opts.ScopeID != "" {
			cypher += "WHERE r.scopeId = $scopeId "
			params["scopeId"] = opts.ScopeID
		}
	}
	cypher += `RETURN type(r) AS type,
		labels(a) AS startLabels, a.nodeKey AS startKey,
		labels(b) AS endLabels,   b.nodeKey AS endKey,
		properties(r) AS props`

	records, err := client.ExecuteQuery(ctx, cypher, params)
	if err != nil {
		return nil, err
	}

	ignoreLabel := stringSet(opts.IgnoreLabels)
	ignoreProp := stringSet(opts.IgnoreRelProps)

	out := make([]Rel, 0, len(records))
	for _, rec := range records {
		typ, _ := rec.Values[0].(string)
		startLabels, _ := asStrings(rec.Values[1])
		startKey, _ := rec.Values[2].(string)
		endLabels, _ := asStrings(rec.Values[3])
		endKey, _ := rec.Values[4].(string)
		props, _ := rec.Values[5].(map[string]any)

		if anyInSet(startLabels, ignoreLabel) || anyInSet(endLabels, ignoreLabel) {
			continue
		}
		sort.Strings(startLabels)
		sort.Strings(endLabels)

		out = append(out, Rel{
			Type:        typ,
			StartLabels: startLabels,
			StartKey:    startKey,
			EndLabels:   endLabels,
			EndKey:      endKey,
			Props:       stripProps(props, ignoreProp),
		})
	}

	sort.Slice(out, func(i, j int) bool {
		ai, bi := out[i], out[j]
		if ai.Type != bi.Type {
			return ai.Type < bi.Type
		}
		if al, bl := joinStrings(ai.StartLabels), joinStrings(bi.StartLabels); al != bl {
			return al < bl
		}
		if ai.StartKey != bi.StartKey {
			return ai.StartKey < bi.StartKey
		}
		if al, bl := joinStrings(ai.EndLabels), joinStrings(bi.EndLabels); al != bl {
			return al < bl
		}
		return ai.EndKey < bi.EndKey
	})
	return out, nil
}

// MarshalCanonical produces a byte-stable JSON encoding suitable for golden file diffs.
func (s *Snapshot) MarshalCanonical() ([]byte, error) {
	return json.MarshalIndent(s, "", "  ")
}

// --- helpers ---

func joinConds(conds []string) string {
	out := ""
	for i, c := range conds {
		if i > 0 {
			out += " AND "
		}
		out += c
	}
	return out
}

func stringSet(xs []string) map[string]struct{} {
	if len(xs) == 0 {
		return nil
	}
	m := make(map[string]struct{}, len(xs))
	for _, x := range xs {
		m[x] = struct{}{}
	}
	return m
}

func anyInSet(xs []string, set map[string]struct{}) bool {
	if set == nil {
		return false
	}
	for _, x := range xs {
		if _, ok := set[x]; ok {
			return true
		}
	}
	return false
}

func stripProps(in map[string]any, ignore map[string]struct{}) map[string]any {
	if in == nil {
		return map[string]any{}
	}
	if ignore == nil {
		return in
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		if _, drop := ignore[k]; drop {
			continue
		}
		out[k] = v
	}
	return out
}

func asStrings(v any) ([]string, bool) {
	switch s := v.(type) {
	case []string:
		return s, true
	case []any:
		out := make([]string, 0, len(s))
		for _, x := range s {
			str, ok := x.(string)
			if !ok {
				return nil, false
			}
			out = append(out, str)
		}
		return out, true
	}
	return nil, false
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func joinStrings(s []string) string {
	out := ""
	for i, x := range s {
		if i > 0 {
			out += "|"
		}
		out += x
	}
	return out
}
