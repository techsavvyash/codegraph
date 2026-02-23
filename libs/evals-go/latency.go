package evals

import (
	"sort"
	"time"
)

// LatencyStats holds percentile and summary latency statistics.
type LatencyStats struct {
	Count int           `json:"count"`
	P50   time.Duration `json:"p50"`
	P95   time.Duration `json:"p95"`
	P99   time.Duration `json:"p99"`
	Min   time.Duration `json:"min"`
	Max   time.Duration `json:"max"`
	Mean  time.Duration `json:"mean"`
}

// ComputeLatencyStats computes percentile statistics from a slice of durations.
func ComputeLatencyStats(durations []time.Duration) LatencyStats {
	n := len(durations)
	if n == 0 {
		return LatencyStats{}
	}

	sorted := make([]time.Duration, n)
	copy(sorted, durations)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	var total time.Duration
	for _, d := range sorted {
		total += d
	}

	return LatencyStats{
		Count: n,
		P50:   percentile(sorted, 50),
		P95:   percentile(sorted, 95),
		P99:   percentile(sorted, 99),
		Min:   sorted[0],
		Max:   sorted[n-1],
		Mean:  total / time.Duration(n),
	}
}

// percentile returns the p-th percentile value from a sorted slice.
func percentile(sorted []time.Duration, p int) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := (p * (len(sorted) - 1)) / 100
	return sorted[idx]
}
