package verify

import (
	"fmt"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j/dbtype"
)

// defaultSampleLimit is used when IntegrityOptions.SampleLimit is 0.
const defaultSampleLimit = 5

// resolveSampleLimit applies the documented 0 -> default behavior.
func resolveSampleLimit(n int) int {
	if n <= 0 {
		return defaultSampleLimit
	}
	return n
}

// scopeFilter builds a Cypher WHERE fragment (without the leading "WHERE")
// plus its parameters for filtering a node variable by service/scope. Empty
// options mean "no filter" and produce an empty clause. Callers combine the
// clause with AND when they have additional predicates.
type scopeFilter struct {
	clause string // e.g. "n.serviceName = $serviceName AND n.scopeId = $scopeId"
	params map[string]any
}

func newScopeFilter(nodeVar, serviceName, scopeID string) scopeFilter {
	var parts []string
	params := map[string]any{}
	if serviceName != "" {
		parts = append(parts, fmt.Sprintf("%s.serviceName = $serviceName", nodeVar))
		params["serviceName"] = serviceName
	}
	if scopeID != "" {
		parts = append(parts, fmt.Sprintf("%s.scopeId = $scopeId", nodeVar))
		params["scopeId"] = scopeID
	}
	clause := ""
	for i, p := range parts {
		if i > 0 {
			clause += " AND "
		}
		clause += p
	}
	return scopeFilter{clause: clause, params: params}
}

func (f scopeFilter) empty() bool {
	return f.clause == ""
}

// whereAnd renders "WHERE <extra>", "WHERE <filter>", "WHERE <filter> AND <extra>",
// or "" if both are empty.
func (f scopeFilter) whereAnd(extra string) string {
	switch {
	case f.empty() && extra == "":
		return ""
	case f.empty():
		return "WHERE " + extra
	case extra == "":
		return "WHERE " + f.clause
	default:
		return "WHERE " + f.clause + " AND " + extra
	}
}

// firstString extracts a string field from a record map, tolerating nil.
func firstString(m map[string]any, key string) string {
	if v, ok := m[key]; ok && v != nil {
		if s, ok := v.(string); ok {
			return s
		}
		return fmt.Sprintf("%v", v)
	}
	return ""
}

// asInt64 coerces common Neo4j numeric record shapes to int64.
func asInt64(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	default:
		return 0
	}
}

// labelString renders a node's labels for sample identifiers, e.g. "Function"
// or "Function|Variable" for multi-labeled nodes.
func labelString(labels []string) string {
	s := ""
	for i, l := range labels {
		if i > 0 {
			s += "|"
		}
		s += l
	}
	if s == "" {
		return "Node"
	}
	return s
}

// describeNode renders a human-readable identifier for a sample offender
// from a Neo4j node value: "<Labels> <serviceName?/><filePath?:startLine?><name-or-nodeKey>".
func describeNode(n dbtype.Node) string {
	labels := labelString(n.Labels)
	props := n.Props

	name := firstString(props, "name")
	if name == "" {
		name = firstString(props, "nodeKey")
	}

	loc := ""
	if fp := firstString(props, "filePath"); fp != "" {
		loc = fp
		if sl, ok := props["startLine"]; ok && sl != nil {
			loc = fmt.Sprintf("%s:%d", loc, int64ish(sl))
		}
	} else if p := firstString(props, "path"); p != "" {
		loc = p
	}

	svc := firstString(props, "serviceName")

	switch {
	case svc != "" && loc != "":
		return fmt.Sprintf("%s %s %s %s", labels, svc, loc, name)
	case loc != "":
		return fmt.Sprintf("%s %s %s", labels, loc, name)
	case svc != "":
		return fmt.Sprintf("%s %s %s", labels, svc, name)
	default:
		return fmt.Sprintf("%s %s", labels, name)
	}
}

func int64ish(v any) int64 {
	return asInt64(v)
}

// describeNodeAny is describeNode for a record value of static type `any`
// (the shape returned by Record.AsMap()).
func describeNodeAny(v any) string {
	n, ok := v.(dbtype.Node)
	if !ok {
		return fmt.Sprintf("%v", v)
	}
	return describeNode(n)
}
