package telemetry

import (
	"encoding/json"
	"fmt"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j/dbtype"
)

// nodeProps extracts the property map from a record value that is expected
// to be a Neo4j node (returned as dbtype.Node by the driver).
func nodeProps(v any) (map[string]any, error) {
	n, ok := v.(dbtype.Node)
	if !ok {
		return nil, fmt.Errorf("expected dbtype.Node, got %T", v)
	}
	return n.Props, nil
}

// recordFromProps decodes an IndexRun node's property map back into a
// RunRecord, parsing the JSON-encoded distribution properties.
func recordFromProps(props map[string]any) (*RunRecord, error) {
	r := &RunRecord{
		RunID:            asString(props["runId"]),
		ServiceName:      asString(props["serviceName"]),
		ScopeID:          asString(props["scopeId"]),
		StartedAt:        asString(props["startedAt"]),
		FinishedAt:       asString(props["finishedAt"]),
		Files:            asInt64(props["files"]),
		Functions:        asInt64(props["functions"]),
		Methods:          asInt64(props["methods"]),
		Symbols:          asInt64(props["symbols"]),
		CallsEdges:       asInt64(props["callsEdges"]),
		UsesValueEdges:   asInt64(props["usesValueEdges"]),
		ImplementsEdges:  asInt64(props["implementsEdges"]),
		APIRoutes:        asInt64(props["apiRoutes"]),
		CallsPerFunction: asFloat64(props["callsPerFunction"]),

		PromotedFunctions:  asInt64(props["promotedFunctions"]),
		DecoratedFunctions: asInt64(props["decoratedFunctions"]),
	}

	rangeDist, err := unmarshalDist(props["rangeSourceDist"])
	if err != nil {
		return nil, fmt.Errorf("rangeSourceDist: %w", err)
	}
	r.RangeSourceDist = rangeDist

	detectionDist, err := unmarshalDist(props["detectionSourceDist"])
	if err != nil {
		return nil, fmt.Errorf("detectionSourceDist: %w", err)
	}
	r.DetectionSourceDist = detectionDist

	return r, nil
}

func unmarshalDist(v any) (map[string]int64, error) {
	s := asString(v)
	if s == "" {
		return map[string]int64{}, nil
	}
	dist := map[string]int64{}
	if err := json.Unmarshal([]byte(s), &dist); err != nil {
		return nil, err
	}
	return dist, nil
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

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

func asFloat64(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int64:
		return float64(n)
	case int:
		return float64(n)
	default:
		return 0
	}
}
