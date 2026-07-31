package telemetry

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j/dbtype"
)

func TestUnmarshalDist_RoundTrip(t *testing.T) {
	dist := map[string]int64{"treesitter": 1383, "scip-declaration": 425}
	encoded, err := json.Marshal(dist)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	got, err := unmarshalDist(string(encoded))
	if err != nil {
		t.Fatalf("unmarshalDist: %v", err)
	}
	if !reflect.DeepEqual(got, dist) {
		t.Fatalf("round trip mismatch: got %v, want %v", got, dist)
	}
}

func TestUnmarshalDist_EmptyString(t *testing.T) {
	got, err := unmarshalDist("")
	if err != nil {
		t.Fatalf("unmarshalDist: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty map for empty string, got %v", got)
	}
}

func TestUnmarshalDist_Nil(t *testing.T) {
	got, err := unmarshalDist(nil)
	if err != nil {
		t.Fatalf("unmarshalDist: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty map for nil, got %v", got)
	}
}

func TestUnmarshalDist_InvalidJSON(t *testing.T) {
	if _, err := unmarshalDist("not json"); err == nil {
		t.Fatal("expected an error for invalid JSON, got nil")
	}
}

func TestRecordFromProps_RoundTrip(t *testing.T) {
	rangeDist := map[string]int64{"treesitter": 10, "go-ast": 5}
	detectionDist := map[string]int64{"scip": 3, "decorator": 7}
	rangeJSON, _ := json.Marshal(rangeDist)
	detectionJSON, _ := json.Marshal(detectionDist)

	props := map[string]any{
		"runId":               "svc@t1",
		"serviceName":         "svc",
		"scopeId":             "main",
		"startedAt":           "2026-08-01T00:00:00Z",
		"finishedAt":          "2026-08-01T00:01:00Z",
		"files":               int64(10),
		"functions":           int64(20),
		"methods":             int64(30),
		"symbols":             int64(40),
		"callsEdges":          int64(50),
		"implementsEdges":     int64(5),
		"apiRoutes":           int64(2),
		"callsPerFunction":    2.5,
		"rangeSourceDist":     string(rangeJSON),
		"detectionSourceDist": string(detectionJSON),
		"promotedFunctions":   int64(1),
		"decoratedFunctions":  int64(4),
	}

	got, err := recordFromProps(props)
	if err != nil {
		t.Fatalf("recordFromProps: %v", err)
	}

	want := &RunRecord{
		RunID:               "svc@t1",
		ServiceName:         "svc",
		ScopeID:             "main",
		StartedAt:           "2026-08-01T00:00:00Z",
		FinishedAt:          "2026-08-01T00:01:00Z",
		Files:               10,
		Functions:           20,
		Methods:             30,
		Symbols:             40,
		CallsEdges:          50,
		ImplementsEdges:     5,
		APIRoutes:           2,
		CallsPerFunction:    2.5,
		RangeSourceDist:     rangeDist,
		DetectionSourceDist: detectionDist,
		PromotedFunctions:   1,
		DecoratedFunctions:  4,
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("recordFromProps mismatch:\ngot  %+v\nwant %+v", got, want)
	}
}

func TestNodeProps_WrongType(t *testing.T) {
	if _, err := nodeProps("not a node"); err == nil {
		t.Fatal("expected an error for a non-node value, got nil")
	}
}

func TestNodeProps_ExtractsFromDbtypeNode(t *testing.T) {
	node := dbtype.Node{
		Labels: []string{"IndexRun"},
		Props:  map[string]any{"runId": "svc@t1"},
	}
	props, err := nodeProps(node)
	if err != nil {
		t.Fatalf("nodeProps: %v", err)
	}
	if props["runId"] != "svc@t1" {
		t.Fatalf("props[runId] = %v, want svc@t1", props["runId"])
	}
}

func TestAsInt64_Variants(t *testing.T) {
	cases := []struct {
		in   any
		want int64
	}{
		{int64(5), 5},
		{int(5), 5},
		{float64(5), 5},
		{nil, 0},
		{"not a number", 0},
	}
	for _, tc := range cases {
		if got := asInt64(tc.in); got != tc.want {
			t.Errorf("asInt64(%v) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestAsFloat64_Variants(t *testing.T) {
	cases := []struct {
		in   any
		want float64
	}{
		{float64(5.5), 5.5},
		{int64(5), 5},
		{int(5), 5},
		{nil, 0},
		{"not a number", 0},
	}
	for _, tc := range cases {
		if got := asFloat64(tc.in); got != tc.want {
			t.Errorf("asFloat64(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
