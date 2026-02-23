package evals

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/context-maximiser/code-graph/libs/search-go"
)

func TestComputeRecallAtK(t *testing.T) {
	relevant := map[string]RelevanceGrade{
		"a": Perfect,
		"b": Relevant,
		"c": Partial,
	}

	tests := []struct {
		name      string
		retrieved []string
		k         int
		want      float64
	}{
		{"all found at k=5", []string{"a", "b", "c", "x", "y"}, 5, 1.0},
		{"2 of 3 found at k=3", []string{"a", "x", "b", "c"}, 3, 2.0 / 3},
		{"none found", []string{"x", "y", "z"}, 3, 0},
		{"k larger than retrieved", []string{"a"}, 10, 1.0 / 3},
		{"empty retrieved", []string{}, 5, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ComputeRecallAtK(tt.retrieved, relevant, tt.k)
			if !approxEqual(got, tt.want, 0.001) {
				t.Errorf("RecallAtK = %f, want %f", got, tt.want)
			}
		})
	}
}

func TestComputePrecisionAtK(t *testing.T) {
	relevant := map[string]RelevanceGrade{
		"a": Perfect,
		"b": Relevant,
	}

	tests := []struct {
		name      string
		retrieved []string
		k         int
		want      float64
	}{
		{"2 of 5 relevant", []string{"a", "x", "b", "y", "z"}, 5, 0.4},
		{"1 of 2", []string{"x", "a"}, 2, 0.5},
		{"none", []string{"x", "y"}, 2, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ComputePrecisionAtK(tt.retrieved, relevant, tt.k)
			if !approxEqual(got, tt.want, 0.001) {
				t.Errorf("PrecisionAtK = %f, want %f", got, tt.want)
			}
		})
	}
}

func TestComputeNDCG(t *testing.T) {
	relevant := map[string]RelevanceGrade{
		"a": Perfect,  // 3 → gain=7
		"b": Relevant, // 2 → gain=3
		"c": Partial,  // 1 → gain=1
	}

	t.Run("perfect ordering", func(t *testing.T) {
		retrieved := []string{"a", "b", "c"}
		got := ComputeNDCG(retrieved, relevant, 3)
		// Perfect ordering should give nDCG = 1.0
		if !approxEqual(got, 1.0, 0.001) {
			t.Errorf("nDCG (perfect) = %f, want 1.0", got)
		}
	})

	t.Run("reversed ordering", func(t *testing.T) {
		retrieved := []string{"c", "b", "a"}
		got := ComputeNDCG(retrieved, relevant, 3)
		// Reversed should be less than perfect
		if got >= 1.0 {
			t.Errorf("nDCG (reversed) = %f, should be < 1.0", got)
		}
		if got <= 0 {
			t.Errorf("nDCG (reversed) = %f, should be > 0", got)
		}
	})

	t.Run("no relevant results", func(t *testing.T) {
		retrieved := []string{"x", "y", "z"}
		got := ComputeNDCG(retrieved, relevant, 3)
		if got != 0 {
			t.Errorf("nDCG (no relevant) = %f, want 0", got)
		}
	})

	t.Run("empty relevant map", func(t *testing.T) {
		got := ComputeNDCG([]string{"a"}, map[string]RelevanceGrade{}, 1)
		if got != 0 {
			t.Errorf("nDCG (empty relevant) = %f, want 0", got)
		}
	})
}

func TestComputeMRR(t *testing.T) {
	relevant := map[string]RelevanceGrade{
		"a": Perfect,
		"b": Relevant,
	}

	tests := []struct {
		name      string
		retrieved []string
		want      float64
	}{
		{"first relevant at rank 1", []string{"a", "x", "y"}, 1.0},
		{"first relevant at rank 2", []string{"x", "b", "a"}, 0.5},
		{"first relevant at rank 3", []string{"x", "y", "a"}, 1.0 / 3},
		{"no relevant", []string{"x", "y", "z"}, 0},
		{"empty", []string{}, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ComputeMRR(tt.retrieved, relevant)
			if !approxEqual(got, tt.want, 0.001) {
				t.Errorf("MRR = %f, want %f", got, tt.want)
			}
		})
	}
}

func TestComputeLatencyStats(t *testing.T) {
	durations := []time.Duration{
		10 * time.Millisecond,
		20 * time.Millisecond,
		30 * time.Millisecond,
		40 * time.Millisecond,
		50 * time.Millisecond,
	}

	stats := ComputeLatencyStats(durations)

	if stats.Count != 5 {
		t.Errorf("Count = %d, want 5", stats.Count)
	}
	if stats.Min != 10*time.Millisecond {
		t.Errorf("Min = %v, want 10ms", stats.Min)
	}
	if stats.Max != 50*time.Millisecond {
		t.Errorf("Max = %v, want 50ms", stats.Max)
	}
	if stats.Mean != 30*time.Millisecond {
		t.Errorf("Mean = %v, want 30ms", stats.Mean)
	}
	if stats.P50 != 30*time.Millisecond {
		t.Errorf("P50 = %v, want 30ms", stats.P50)
	}

	t.Run("empty durations", func(t *testing.T) {
		s := ComputeLatencyStats(nil)
		if s.Count != 0 {
			t.Errorf("Count = %d, want 0", s.Count)
		}
	})
}

func TestLoadDataset(t *testing.T) {
	// Find testdata relative to this test file
	testdataPath := filepath.Join("testdata", "codegraph_retrieval_golden.yaml")
	if _, err := os.Stat(testdataPath); os.IsNotExist(err) {
		t.Skip("testdata not found, skipping")
	}

	ds, err := LoadDataset(testdataPath)
	if err != nil {
		t.Fatalf("LoadDataset: %v", err)
	}

	if ds.Name != "CodeGraph Retrieval Golden Set" {
		t.Errorf("Name = %q, want 'CodeGraph Retrieval Golden Set'", ds.Name)
	}
	if len(ds.Queries) != 10 {
		t.Errorf("got %d queries, want 10", len(ds.Queries))
	}
	if ds.DefaultK != 20 {
		t.Errorf("DefaultK = %d, want 20", ds.DefaultK)
	}

	// Verify first query structure
	q := ds.Queries[0]
	if q.ID != "q01" {
		t.Errorf("first query ID = %q, want 'q01'", q.ID)
	}
	if len(q.Expected) < 1 {
		t.Fatal("first query has no expected results")
	}
	if q.Expected[0].Grade != Perfect {
		t.Errorf("first expected grade = %d, want %d (Perfect)", q.Expected[0].Grade, Perfect)
	}
}

func TestLoadDatasetJSON(t *testing.T) {
	// Create a temporary JSON dataset
	tmpDir := t.TempDir()
	jsonPath := filepath.Join(tmpDir, "test.json")
	content := `{
		"name": "test",
		"defaultK": 10,
		"queries": [
			{
				"id": "t1",
				"query": "test query",
				"expected": [{"nodeKey": "k1", "grade": 2}]
			}
		]
	}`
	if err := os.WriteFile(jsonPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	ds, err := LoadDataset(jsonPath)
	if err != nil {
		t.Fatalf("LoadDataset JSON: %v", err)
	}
	if ds.Name != "test" {
		t.Errorf("Name = %q, want 'test'", ds.Name)
	}
	if len(ds.Queries) != 1 {
		t.Errorf("got %d queries, want 1", len(ds.Queries))
	}
}

func TestLoadDatasetUnsupported(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(path, []byte("hello"), 0644)

	_, err := LoadDataset(path)
	if err == nil {
		t.Fatal("expected error for unsupported format")
	}
}

func TestRelevanceMap(t *testing.T) {
	q := EvalQuery{
		Expected: []ExpectedResult{
			{NodeKey: "a", Grade: Perfect},
			{NodeKey: "b", Grade: Partial},
		},
	}
	m := q.RelevanceMap()
	if m["a"] != Perfect {
		t.Errorf("a = %d, want %d", m["a"], Perfect)
	}
	if m["b"] != Partial {
		t.Errorf("b = %d, want %d", m["b"], Partial)
	}
}

func TestPrintReport(t *testing.T) {
	run := &EvalRun{
		Dataset: "test",
		Mode:    ModeHybrid,
		K:       20,
		Weights: search.DefaultWeights,
		Results: []QueryMetrics{
			{
				ID:            "q1",
				Query:         "test query",
				RecallAtK:     0.667,
				NDCG:          0.812,
				MRR:           1.0,
				PrecisionAtK:  0.1,
				Found:         2,
				TotalExpected: 3,
			},
		},
		Aggregate: AggregateMetrics{
			MeanRecallAtK:    0.667,
			MeanNDCG:         0.812,
			MeanMRR:          1.0,
			MeanPrecisionAtK: 0.1,
		},
		Latency: LatencyStats{
			Count: 1,
			P50:   45 * time.Millisecond,
			P95:   120 * time.Millisecond,
			P99:   180 * time.Millisecond,
			Mean:  62 * time.Millisecond,
		},
	}

	var buf bytes.Buffer
	PrintReport(&buf, run)
	output := buf.String()

	if len(output) == 0 {
		t.Fatal("PrintReport produced empty output")
	}
	// Verify key sections exist
	for _, want := range []string{"Recall@K", "nDCG", "MRR", "MEAN", "Latency"} {
		if !bytes.Contains([]byte(output), []byte(want)) {
			t.Errorf("output missing %q", want)
		}
	}
}

func TestPrintJSON(t *testing.T) {
	run := &EvalRun{
		Dataset: "test",
		Mode:    ModeHybrid,
		K:       20,
	}

	var buf bytes.Buffer
	if err := PrintJSON(&buf, run); err != nil {
		t.Fatalf("PrintJSON: %v", err)
	}
	if buf.Len() == 0 {
		t.Fatal("PrintJSON produced empty output")
	}
}

func TestSourceContribAdd(t *testing.T) {
	a := SourceContrib{VectorHits: 3, FullTextHits: 2, VectorOnly: 1}
	b := SourceContrib{VectorHits: 1, SemanticHits: 4, SemanticOnly: 2}
	a.AddContrib(b)

	if a.VectorHits != 4 {
		t.Errorf("VectorHits = %d, want 4", a.VectorHits)
	}
	if a.SemanticHits != 4 {
		t.Errorf("SemanticHits = %d, want 4", a.SemanticHits)
	}
}

func approxEqual(a, b, epsilon float64) bool {
	if a == b {
		return true
	}
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	return diff < epsilon
}
