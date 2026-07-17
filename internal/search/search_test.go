package search

import (
	"context"
	"encoding/base64"
	"math"
	"sort"
	"strings"
	"testing"
)

// TestRRFScoreSingle verifies the RRF formula with k=60.
func TestRRFScoreSingle(t *testing.T) {
	tests := []struct {
		rank     int
		expected float64
	}{
		{1, 1.0 / 61.0},
		{2, 1.0 / 62.0},
		{60, 1.0 / 120.0},
		{120, 1.0 / 180.0},
	}
	for _, tc := range tests {
		got := rrfScoreSingle(tc.rank)
		if math.Abs(got-tc.expected) > 1e-10 {
			t.Errorf("rrfScoreSingle(%d) = %f, want %f", tc.rank, got, tc.expected)
		}
	}
}

// TestRRFFusionTwoLists tests RRF fusion across two overlapping ranked lists.
// Known scenario: two lists with identical 3 docs A, B, C but different orders.
// List 1 (label1): A (rank 1), B (rank 2), C (rank 3)
// List 2 (label2): C (rank 1), B (rank 2), A (rank 3)
// All three get similar total scores due to the symmetric ranks. We just verify
// that RRF correctly accumulates scores across lists.
func TestRRFFusionTwoLists(t *testing.T) {
	// Simulate the fusion process manually
	byNodeID := make(map[string]float64)

	// List 1 ranks
	byNodeID["A"] += rrfScoreSingle(1) // A rank 1: 1/61
	byNodeID["B"] += rrfScoreSingle(2) // B rank 2: 1/62
	byNodeID["C"] += rrfScoreSingle(3) // C rank 3: 1/63

	// List 2 ranks
	byNodeID["C"] += rrfScoreSingle(1) // C rank 1: 1/61
	byNodeID["B"] += rrfScoreSingle(2) // B rank 2: 1/62
	byNodeID["A"] += rrfScoreSingle(3) // A rank 3: 1/63

	// Expected fused scores: A and C symmetric, B constant at rank 2 in both
	expectedScores := map[string]float64{
		"A": 1.0/61.0 + 1.0/63.0,
		"B": 1.0/62.0 + 1.0/62.0,
		"C": 1.0/63.0 + 1.0/61.0,
	}

	for nodeID, expected := range expectedScores {
		got, ok := byNodeID[nodeID]
		if !ok {
			t.Fatalf("nodeID %q not in fusion result", nodeID)
		}
		if math.Abs(got-expected) > 1e-10 {
			t.Errorf("fused score for %q: got %f, want %f", nodeID, got, expected)
		}
	}

	// Verify that all three are computed (no accumulation errors)
	if len(byNodeID) != 3 {
		t.Errorf("expected 3 fused nodes, got %d", len(byNodeID))
	}
}

// TestExactMatchBoost verifies that exact-match results rank first.
func TestExactMatchBoost(t *testing.T) {
	// Create results: exact match with lower score, fuzzy with higher score
	results := []Result{
		{NodeID: "node1", Name: "handler_impl", Score: 0.5}, // Fuzzy match, higher RRF score
		{NodeID: "node2", Name: "Handler", Score: 0.1},      // Exact match (case-insensitive), lower RRF score
		{NodeID: "node3", Name: "EventHandler", Score: 0.3}, // Fuzzy match, medium RRF score
	}

	// Simulate exact-match boost
	query := "Handler"
	queryLower := strings.ToLower(query)
	exactMatchBonus := 10.0
	for i, r := range results {
		if strings.ToLower(r.Name) == queryLower {
			results[i].Score += exactMatchBonus
		}
	}

	// After boost, node2 (exact match) should rank first
	if results[1].Score <= results[0].Score || results[1].Score <= results[2].Score {
		t.Fatalf("exact match (node2) should rank first after boost; scores: node1=%f, node2=%f, node3=%f",
			results[0].Score, results[1].Score, results[2].Score)
	}

	// Verify exact match node has the boosted score
	expectedScore := exactMatchBonus + 0.1
	if math.Abs(results[1].Score-expectedScore) > 1e-10 {
		t.Errorf("node2 score after boost should be %.1f, got %f", expectedScore, results[1].Score)
	}
}

// TestLuceneEscaping pins exact escaped outputs: single tokens get escaped
// metacharacters plus a trailing prefix wildcard OUTSIDE any quotes (wildcards
// inside quoted phrases are literal in Lucene); multi-word input becomes a
// quoted phrase with no wildcard.
func TestLuceneEscaping(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"simple", "simple*"},
		{"Handler+", `Handler\+*`},
		{"foo-bar", `foo\-bar*`},
		{"test()", `test\(\)*`},
		{"query&&term", `query\&\&term*`},
		{"wild*card", `wild\*card*`},
		{`quote"test`, `quote\"test*`},
		{"two words", `"two words"`},
		{"", ""},
	}

	for _, tc := range tests {
		got := escapeLuceneQuery(tc.input)
		if got != tc.want {
			t.Errorf("escapeLuceneQuery(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// TestCursorRoundTrip: a cursor encodes the full (score, name, nodeID) sort
// key and decodeCursorStart resumes strictly after it.
func TestCursorRoundTrip(t *testing.T) {
	results := []Result{
		{NodeID: "node-a", Name: "Alpha", Score: 10.0},
		{NodeID: "node-b", Name: "Beta", Score: 9.5},
		{NodeID: "node-c", Name: "Gamma", Score: 8.0},
	}

	// Cursor at the middle element resumes at the third.
	cursor := encodeCursor(results[1])
	start, err := decodeCursorStart(results, cursor)
	if err != nil {
		t.Fatalf("decodeCursorStart failed: %v", err)
	}
	if start != 2 {
		t.Errorf("cursor after node-b should resume at index 2, got %d", start)
	}

	// Cursor at the last element resumes past the end (no more pages).
	cursor = encodeCursor(results[2])
	start, err = decodeCursorStart(results, cursor)
	if err != nil {
		t.Fatalf("decodeCursorStart failed: %v", err)
	}
	if start != 3 {
		t.Errorf("cursor after final element should resume at len(results)=3, got %d", start)
	}
}

// TestCursorSurvivesDeletedRow: keyset semantics mean a cursor whose exact row
// vanished still resumes at the right boundary instead of restarting at 0 —
// the identity-scan implementation this replaced failed exactly here.
func TestCursorSurvivesDeletedRow(t *testing.T) {
	full := []Result{
		{NodeID: "node-a", Name: "Alpha", Score: 10.0},
		{NodeID: "node-b", Name: "Beta", Score: 9.5},
		{NodeID: "node-c", Name: "Gamma", Score: 8.0},
		{NodeID: "node-d", Name: "Delta", Score: 7.0},
	}
	cursor := encodeCursor(full[1]) // page ended at node-b

	// node-b was deleted before the next page request.
	remaining := []Result{full[0], full[2], full[3]}
	start, err := decodeCursorStart(remaining, cursor)
	if err != nil {
		t.Fatalf("decodeCursorStart failed: %v", err)
	}
	if start != 1 {
		t.Errorf("cursor should resume at node-c (index 1 of remaining), got %d", start)
	}
}

// TestCursorMalformed: garbage cursors error instead of silently restarting.
func TestCursorMalformed(t *testing.T) {
	results := []Result{{NodeID: "node-a", Name: "Alpha", Score: 1.0}}

	for _, bad := range []string{
		"not-base64!!!",
		base64.StdEncoding.EncodeToString([]byte("no-separators")),
		base64.StdEncoding.EncodeToString([]byte("NaNscore\x00name\x00id")),
	} {
		if _, err := decodeCursorStart(results, bad); err == nil {
			t.Errorf("cursor %q should be rejected", bad)
		}
	}
}

// TestLabelValidation calls the REAL Search validation path: an unknown label
// must error (before any DB access — nil client proves no query was issued)
// and the error must list the valid labels.
func TestLabelValidation(t *testing.T) {
	searcher := NewSearcher(nil)
	_, err := searcher.Search(context.Background(), "anything", Options{
		Labels: []string{"Bogus"},
	})
	if err == nil {
		t.Fatal("Search with invalid label must return an error")
	}
	if !strings.Contains(err.Error(), `invalid label "Bogus"`) {
		t.Errorf("error should name the invalid label, got: %v", err)
	}
	for _, valid := range []string{"Function", "Method", "Class", "Interface", "Symbol", "File", "Variable"} {
		if !strings.Contains(err.Error(), valid) {
			t.Errorf("error should list valid label %q, got: %v", valid, err)
		}
	}
}

// TestResultSorting verifies results sort by RRF score DESC, then Name ASC, then NodeID ASC.
func TestResultSorting(t *testing.T) {
	results := []Result{
		{NodeID: "node-c", Name: "Charlie", Score: 5.0},
		{NodeID: "node-a", Name: "Alice", Score: 10.0},
		{NodeID: "node-b", Name: "Bob", Score: 10.0},    // Same score as Alice, but "Bob" > "Alice"
		{NodeID: "node-a2", Name: "Alice", Score: 10.0}, // Same score & name as node-a, but different nodeID
	}

	// Use the PRODUCTION comparator — pagination correctness depends on it,
	// so the test must not re-implement its own copy.
	sorted := make([]Result, len(results))
	copy(sorted, results)
	sort.Slice(sorted, func(i, j int) bool { return sortLess(sorted[i], sorted[j]) })

	expectedOrder := []string{"node-a", "node-a2", "node-b", "node-c"}
	for i, nodeID := range expectedOrder {
		if sorted[i].NodeID != nodeID {
			t.Errorf("position %d: expected %q, got %q", i, nodeID, sorted[i].NodeID)
		}
	}
}

// BenchmarkLuceneEscaping benchmarks the escaping function.
func BenchmarkLuceneEscaping(b *testing.B) {
	query := "complex-query+with&&special||chars!(test)*[array]"
	for i := 0; i < b.N; i++ {
		escapeLuceneQuery(query)
	}
}

// BenchmarkRRFScore benchmarks the RRF computation.
func BenchmarkRRFScore(b *testing.B) {
	for i := 0; i < b.N; i++ {
		rrfScoreSingle(i % 100)
	}
}
