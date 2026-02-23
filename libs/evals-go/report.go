package evals

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// PrintReport writes an ASCII table summary of an eval run.
func PrintReport(w io.Writer, run *EvalRun) {
	fmt.Fprintf(w, "\nRetrieval Eval: %s  (mode=%s, k=%d)\n", run.Dataset, run.Mode, run.K)
	fmt.Fprintf(w, "Weights: vector=%.2f  fulltext=%.2f  semantic=%.2f\n\n", run.Weights.Vector, run.Weights.FullText, run.Weights.Semantic)

	// Header
	fmt.Fprintf(w, "%-4s %-45s %8s %8s %8s %8s %12s\n",
		"#", "Query", "Recall@K", "nDCG", "MRR", "Prec", "Found/Total")
	fmt.Fprintln(w, strings.Repeat("-", 100))

	// Per-query rows
	for i, r := range run.Results {
		query := truncate(r.Query, 43)
		fmt.Fprintf(w, "%-4d %-45s %8.3f %8.3f %8.3f %8.3f %5d/%5d\n",
			i+1, query, r.RecallAtK, r.NDCG, r.MRR, r.PrecisionAtK, r.Found, r.TotalExpected)
	}

	// Aggregate
	fmt.Fprintln(w, strings.Repeat("-", 100))
	agg := run.Aggregate
	fmt.Fprintf(w, "%-4s %-45s %8.3f %8.3f %8.3f %8.3f\n",
		"", "MEAN", agg.MeanRecallAtK, agg.MeanNDCG, agg.MeanMRR, agg.MeanPrecisionAtK)

	// Source contribution
	sc := agg.TotalContrib
	fmt.Fprintf(w, "\nSource Contribution: Vector: %d (unique: %d) | FullText: %d (unique: %d) | Semantic: %d (unique: %d) | Hybrid: %d\n",
		sc.VectorHits, sc.VectorOnly, sc.FullTextHits, sc.FullTextOnly, sc.SemanticHits, sc.SemanticOnly, sc.HybridHits)

	// Latency
	lat := run.Latency
	fmt.Fprintf(w, "Latency (n=%d): P50=%v  P95=%v  P99=%v  mean=%v\n\n",
		lat.Count, lat.P50, lat.P95, lat.P99, lat.Mean)
}

// PrintJSON writes the full eval run as formatted JSON.
func PrintJSON(w io.Writer, run *EvalRun) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(run)
}

// truncate shortens a string to maxLen, adding "..." if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}
