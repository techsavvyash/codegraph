package static

import (
	"fmt"
	"strings"
)

// IndexReport tracks per-phase counts and failures during indexing.
// Phases are pointer-backed so entries returned by AddPhase stay valid as the
// phase list grows (a value slice would leave held pointers dangling on the
// old backing array after append reallocates).
type IndexReport struct {
	phases   []*PhaseEntry
	warnings []string
}

// PhaseEntry holds statistics for a single indexing phase.
type PhaseEntry struct {
	Phase   string
	Written int
	Skipped int
	Failed  int
}

// NewIndexReport creates a new empty report.
func NewIndexReport() *IndexReport {
	return &IndexReport{
		phases:   []*PhaseEntry{},
		warnings: []string{},
	}
}

// AddPhase initializes or retrieves a phase entry by name.
// Insertion order is preserved; duplicate names update the existing entry.
func (r *IndexReport) AddPhase(name string) *PhaseEntry {
	for _, p := range r.phases {
		if p.Phase == name {
			return p
		}
	}
	entry := &PhaseEntry{Phase: name}
	r.phases = append(r.phases, entry)
	return entry
}

// IncrementWritten increments the Written count for a phase.
// Creates the phase if it doesn't exist.
func (r *IndexReport) IncrementWritten(phase string, count int) {
	p := r.AddPhase(phase)
	p.Written += count
}

// IncrementSkipped increments the Skipped count for a phase.
// Creates the phase if it doesn't exist.
func (r *IndexReport) IncrementSkipped(phase string, count int) {
	p := r.AddPhase(phase)
	p.Skipped += count
}

// IncrementFailed increments the Failed count for a phase.
// Creates the phase if it doesn't exist.
func (r *IndexReport) IncrementFailed(phase string, count int) {
	p := r.AddPhase(phase)
	p.Failed += count
}

// AddWarning appends a warning message to the report.
func (r *IndexReport) AddWarning(msg string) {
	r.warnings = append(r.warnings, msg)
}

// HasFailures returns true if any phase has a non-zero Failed count.
func (r *IndexReport) HasFailures() bool {
	for _, p := range r.phases {
		if p.Failed > 0 {
			return true
		}
	}
	return false
}

// String returns a deterministic multi-line summary of the report.
// Phase order is insertion order (not alphabetical).
func (r *IndexReport) String() string {
	var lines []string
	lines = append(lines, "=== Indexing Report ===")

	if len(r.phases) == 0 {
		lines = append(lines, "(no phases recorded)")
	} else {
		for _, p := range r.phases {
			line := fmt.Sprintf("%s: written=%d, skipped=%d, failed=%d",
				p.Phase, p.Written, p.Skipped, p.Failed)
			lines = append(lines, line)
		}
	}

	if len(r.warnings) > 0 {
		lines = append(lines, "")
		lines = append(lines, "Warnings:")
		for _, w := range r.warnings {
			lines = append(lines, "  - "+w)
		}
	}

	return strings.Join(lines, "\n")
}

// FailedPhaseCount returns the number of phases with at least one failure.
func (r *IndexReport) FailedPhaseCount() int {
	count := 0
	for _, p := range r.phases {
		if p.Failed > 0 {
			count++
		}
	}
	return count
}
