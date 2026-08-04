// Package oracle implements RFC-013 Layer 2: differential call-graph oracles
// that recompute graph facts from an independent implementation (go/types for
// Go, the target project's own TypeScript compiler for TS) and join them onto
// the indexed graph as precision/recall reports.
package oracle

// EdgeSample identifies one caller→callee pair in a mismatch bucket.
type EdgeSample struct {
	From string `json:"from"`
	To   string `json:"to"`
	Note string `json:"note,omitempty"`
}

// OracleReport is the outcome of one differential comparison.
//
// The sandwich principle (Go): static(direct) ⊆ graph CALLS ⊆ CHA(may-call).
// MissingFromGraph are must-oracle edges the graph lacks (recall gaps);
// PrecisionSuspects are graph edges outside the may-oracle (fabrication
// suspects). For sampled oracles (TS), SampledSites/ResolvedSites qualify the
// estimate and MayEdges is 0.
type OracleReport struct {
	Language          string       `json:"language"`
	ServiceName       string       `json:"serviceName"`
	GraphEdges        int          `json:"graphEdges"`
	MustEdges         int          `json:"mustEdges"`
	MayEdges          int          `json:"mayEdges,omitempty"`
	SampledSites      int          `json:"sampledSites,omitempty"`
	ResolvedSites     int          `json:"resolvedSites,omitempty"`
	MissingFromGraph  []EdgeSample `json:"missingFromGraph,omitempty"`
	PrecisionSuspects []EdgeSample `json:"precisionSuspects,omitempty"`
	Recall            float64      `json:"recall"`
	Notes             []string     `json:"notes,omitempty"`
}
