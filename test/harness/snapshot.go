// Package harness provides deterministic graph snapshots for golden tests.
package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/context-maximiser/code-graph/internal/graph"
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
	if opts.ScopeID != "" {
		cypher += "WHERE n.scopeId = $scopeId "
		params["scopeId"] = opts.ScopeID
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
	cypher := "MATCH (a)-[r]->(b) "
	params := map[string]any{}
	if opts.ScopeID != "" {
		cypher += "WHERE r.scopeId = $scopeId "
		params["scopeId"] = opts.ScopeID
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
