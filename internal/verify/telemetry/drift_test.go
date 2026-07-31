package telemetry

import (
	"reflect"
	"sort"
	"testing"
)

func baseRecord() *RunRecord {
	return &RunRecord{
		RunID:               "svc@t1",
		ServiceName:         "svc",
		ScopeID:             "main",
		StartedAt:           "2026-08-01T00:00:00Z",
		FinishedAt:          "2026-08-01T00:01:00Z",
		Files:               100,
		Functions:           200,
		Methods:             300,
		Symbols:             400,
		CallsEdges:          500,
		ImplementsEdges:     50,
		APIRoutes:           20,
		CallsPerFunction:    1.0,
		RangeSourceDist:     map[string]int64{"treesitter": 400, "go-ast": 100},
		DetectionSourceDist: map[string]int64{"scip": 45, "decorator": 5},
		PromotedFunctions:   10,
		DecoratedFunctions:  5,
	}
}

func sortDrifts(d []Drift) {
	sort.Slice(d, func(i, j int) bool { return d[i].Counter < d[j].Counter })
}

func TestDiffRuns_NoDriftWhenIdentical(t *testing.T) {
	prev := baseRecord()
	cur := baseRecord()
	cur.RunID = "svc@t2"
	cur.FinishedAt = "2026-08-01T01:00:00Z"

	drifts := diffRuns(prev, cur)
	if len(drifts) != 0 {
		t.Fatalf("expected no drift for identical runs, got %+v", drifts)
	}
}

func TestDiffRuns_NilInputs(t *testing.T) {
	cur := baseRecord()
	if d := diffRuns(nil, cur); d != nil {
		t.Fatalf("expected nil drifts when previous is nil, got %+v", d)
	}
	if d := diffRuns(cur, nil); d != nil {
		t.Fatalf("expected nil drifts when current is nil, got %+v", d)
	}
	if d := diffRuns(nil, nil); d != nil {
		t.Fatalf("expected nil drifts when both nil, got %+v", d)
	}
}

func TestDiffRuns_NumericCounterAboveThreshold(t *testing.T) {
	prev := baseRecord()
	cur := baseRecord()
	cur.Functions = 260 // +30%, above 25% threshold

	drifts := diffRuns(prev, cur)
	found := false
	for _, d := range drifts {
		if d.Counter == "functions" {
			found = true
			if d.Previous != 200 || d.Current != 260 {
				t.Errorf("functions drift values = %+v, want prev=200 cur=260", d)
			}
		}
	}
	if !found {
		t.Fatalf("expected functions drift, got %+v", drifts)
	}
}

func TestDiffRuns_NumericCounterBelowThreshold(t *testing.T) {
	prev := baseRecord()
	cur := baseRecord()
	cur.Functions = 220 // +10%, below 25% threshold

	drifts := diffRuns(prev, cur)
	for _, d := range drifts {
		if d.Counter == "functions" {
			t.Fatalf("did not expect functions drift at +10%%, got %+v", d)
		}
	}
}

func TestDiffRuns_NumericCounterExactlyAtThreshold_NotFlagged(t *testing.T) {
	prev := baseRecord()
	cur := baseRecord()
	cur.Functions = 250 // exactly +25%: threshold check is "> 0.25", not ">="

	drifts := diffRuns(prev, cur)
	for _, d := range drifts {
		if d.Counter == "functions" {
			t.Fatalf("did not expect functions drift at exactly +25%%, got %+v", d)
		}
	}
}

func TestDiffRuns_NegativeDirectionAboveThreshold(t *testing.T) {
	prev := baseRecord()
	cur := baseRecord()
	cur.CallsEdges = 300 // -40%

	drifts := diffRuns(prev, cur)
	found := false
	for _, d := range drifts {
		if d.Counter == "callsEdges" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected callsEdges drift on -40%% drop, got %+v", drifts)
	}
}

func TestDiffRuns_ZeroBaseline_ZeroToN(t *testing.T) {
	prev := baseRecord()
	prev.APIRoutes = 0
	cur := baseRecord()
	cur.APIRoutes = 5

	drifts := diffRuns(prev, cur)
	found := false
	for _, d := range drifts {
		if d.Counter == "apiRoutes" {
			found = true
			if d.Previous != 0 || d.Current != 5 {
				t.Errorf("apiRoutes drift = %+v, want prev=0 cur=5", d)
			}
		}
	}
	if !found {
		t.Fatalf("expected apiRoutes drift on 0->5 (must not divide by zero), got %+v", drifts)
	}
}

func TestDiffRuns_ZeroBaseline_NToZero(t *testing.T) {
	prev := baseRecord()
	prev.APIRoutes = 5
	cur := baseRecord()
	cur.APIRoutes = 0

	drifts := diffRuns(prev, cur)
	found := false
	for _, d := range drifts {
		if d.Counter == "apiRoutes" {
			found = true
			if d.Previous != 5 || d.Current != 0 {
				t.Errorf("apiRoutes drift = %+v, want prev=5 cur=0", d)
			}
		}
	}
	if !found {
		t.Fatalf("expected apiRoutes drift on 5->0, got %+v", drifts)
	}
}

func TestDiffRuns_ZeroBaseline_ZeroToZero_NoDrift(t *testing.T) {
	prev := baseRecord()
	prev.APIRoutes = 0
	cur := baseRecord()
	cur.APIRoutes = 0

	drifts := diffRuns(prev, cur)
	for _, d := range drifts {
		if d.Counter == "apiRoutes" {
			t.Fatalf("did not expect apiRoutes drift for 0->0, got %+v", d)
		}
	}
}

func TestDiffRuns_CallsPerFunction_DropFlagged(t *testing.T) {
	prev := baseRecord()
	prev.CallsPerFunction = 2.0
	cur := baseRecord()
	cur.CallsPerFunction = 1.0 // -50%, a drop > 25%

	drifts := diffRuns(prev, cur)
	found := false
	for _, d := range drifts {
		if d.Counter == "callsPerFunction" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected callsPerFunction drift on 50%% drop, got %+v", drifts)
	}
}

func TestDiffRuns_CallsPerFunction_RiseNotFlagged(t *testing.T) {
	prev := baseRecord()
	prev.CallsPerFunction = 1.0
	cur := baseRecord()
	cur.CallsPerFunction = 2.0 // +100%, a rise — must not be flagged per spec

	drifts := diffRuns(prev, cur)
	for _, d := range drifts {
		if d.Counter == "callsPerFunction" {
			t.Fatalf("did not expect callsPerFunction drift on a rise, got %+v", d)
		}
	}
}

func TestDiffRuns_DistributionKeyAppears(t *testing.T) {
	prev := baseRecord()
	prev.RangeSourceDist = map[string]int64{"treesitter": 400}
	cur := baseRecord()
	cur.RangeSourceDist = map[string]int64{"treesitter": 400, "scip-declaration": 50}

	drifts := diffRuns(prev, cur)
	found := false
	for _, d := range drifts {
		if d.Counter == "rangeSourceDist[scip-declaration]" {
			found = true
			if d.Detail != "key appeared" {
				t.Errorf("detail = %q, want 'key appeared'", d.Detail)
			}
			if d.Previous != 0 || d.Current != 50 {
				t.Errorf("values = prev=%v cur=%v, want prev=0 cur=50", d.Previous, d.Current)
			}
		}
	}
	if !found {
		t.Fatalf("expected rangeSourceDist[scip-declaration] appearance drift, got %+v", drifts)
	}
}

func TestDiffRuns_DistributionKeyDisappears(t *testing.T) {
	prev := baseRecord()
	prev.RangeSourceDist = map[string]int64{"treesitter": 400, "go-ast": 100}
	cur := baseRecord()
	cur.RangeSourceDist = map[string]int64{"treesitter": 400}

	drifts := diffRuns(prev, cur)
	found := false
	for _, d := range drifts {
		if d.Counter == "rangeSourceDist[go-ast]" {
			found = true
			if d.Detail != "key disappeared" {
				t.Errorf("detail = %q, want 'key disappeared'", d.Detail)
			}
			if d.Previous != 100 || d.Current != 0 {
				t.Errorf("values = prev=%v cur=%v, want prev=100 cur=0", d.Previous, d.Current)
			}
		}
	}
	if !found {
		t.Fatalf("expected rangeSourceDist[go-ast] disappearance drift, got %+v", drifts)
	}
}

func TestDiffRuns_DistributionValueDriftWithinSharedKey(t *testing.T) {
	prev := baseRecord()
	prev.DetectionSourceDist = map[string]int64{"scip": 100}
	cur := baseRecord()
	cur.DetectionSourceDist = map[string]int64{"scip": 40} // -60%

	drifts := diffRuns(prev, cur)
	found := false
	for _, d := range drifts {
		if d.Counter == "detectionSourceDist[scip]" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected detectionSourceDist[scip] drift on 60%% drop, got %+v", drifts)
	}
}

func TestDiffRuns_MultipleDriftsAllReported(t *testing.T) {
	prev := baseRecord()
	cur := baseRecord()
	cur.Functions = 260  // +30%
	cur.CallsEdges = 250 // -50%
	cur.RangeSourceDist = map[string]int64{"treesitter": 400, "go-ast": 100, "new-source": 10}

	drifts := diffRuns(prev, cur)
	sortDrifts(drifts)

	wantCounters := []string{"callsEdges", "functions", "rangeSourceDist[new-source]"}
	gotCounters := make([]string, 0, len(drifts))
	for _, d := range drifts {
		gotCounters = append(gotCounters, d.Counter)
	}
	sort.Strings(gotCounters)
	sort.Strings(wantCounters)
	if !reflect.DeepEqual(gotCounters, wantCounters) {
		t.Fatalf("drift counters = %v, want %v", gotCounters, wantCounters)
	}
}

func TestFractionalDrift_Table(t *testing.T) {
	cases := []struct {
		name      string
		prev, cur float64
		wantWarn  bool
	}{
		{"zero to zero", 0, 0, false},
		{"zero to positive", 0, 5, true},
		{"positive to zero", 5, 0, true},
		{"within threshold up", 100, 124, false},
		{"within threshold down", 100, 76, false},
		{"above threshold up", 100, 126, true},
		{"above threshold down", 100, 74, true},
		{"identical", 100, 100, false},
		{"exactly at threshold", 100, 125, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, warn := fractionalDrift(tc.prev, tc.cur)
			if warn != tc.wantWarn {
				t.Errorf("fractionalDrift(%v, %v) warn = %v, want %v", tc.prev, tc.cur, warn, tc.wantWarn)
			}
		})
	}
}
