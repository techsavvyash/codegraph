// Package verify implements the graph-correctness layers from RFC-013:
// integrity invariants, differential oracles, declaration census, and
// per-run telemetry drift. Shared report types live here so every layer
// renders and serializes uniformly.
package verify

// Status is the outcome of a single check.
type Status string

const (
	StatusPass Status = "pass"
	StatusWarn Status = "warn"
	StatusFail Status = "fail"
)

// CheckResult is one invariant/check outcome. Count carries the number of
// offending entities (0 for a pass); Samples holds up to a handful of
// human-readable identifiers for the offenders.
type CheckResult struct {
	Name    string   `json:"name"`
	Status  Status   `json:"status"`
	Detail  string   `json:"detail,omitempty"`
	Count   int64    `json:"count"`
	Samples []string `json:"samples,omitempty"`
}

// Report aggregates check results for one verify invocation.
type Report struct {
	Scope  string        `json:"scope,omitempty"` // e.g. service name or "all"
	Checks []CheckResult `json:"checks"`
}

func (r *Report) Add(c CheckResult) {
	r.Checks = append(r.Checks, c)
}

// Counts returns the number of pass/warn/fail checks.
func (r *Report) Counts() (pass, warn, fail int) {
	for _, c := range r.Checks {
		switch c.Status {
		case StatusPass:
			pass++
		case StatusWarn:
			warn++
		case StatusFail:
			fail++
		}
	}
	return
}

// Failed reports whether any check failed (or, with strict, warned).
func (r *Report) Failed(strict bool) bool {
	_, warn, fail := r.Counts()
	if fail > 0 {
		return true
	}
	return strict && warn > 0
}
