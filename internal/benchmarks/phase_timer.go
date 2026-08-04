package benchmarks

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

// PhaseResult holds the timing result for a single phase.
type PhaseResult struct {
	Name       string        `json:"name"`
	Duration   time.Duration `json:"duration"`
	Items      int           `json:"items"`
	Detail     string        `json:"detail,omitempty"`
	IsSubPhase bool          `json:"subPhase,omitempty"`
}

// PhaseTimer provides reusable phase-level timing with item counts.
type PhaseTimer struct {
	results   []PhaseResult
	current   string
	start     time.Time
	wallStart time.Time
	wallStop  time.Time
}

// NewPhaseTimer creates a new PhaseTimer.
func NewPhaseTimer() *PhaseTimer {
	return &PhaseTimer{}
}

// Start begins timing a named phase. If a previous phase is still running,
// it is automatically stopped with zero items.
func (pt *PhaseTimer) Start(name string) {
	if pt.current != "" {
		pt.Stop(0, "")
	}
	pt.current = name
	pt.start = time.Now()
}

// Stop ends the current phase, recording the item count and optional detail.
func (pt *PhaseTimer) Stop(items int, detail string) {
	if pt.current == "" {
		return
	}
	pt.results = append(pt.results, PhaseResult{
		Name:     pt.current,
		Duration: time.Since(pt.start),
		Items:    items,
		Detail:   detail,
	})
	pt.current = ""
}

// AddResult adds a pre-computed phase result (for cumulative sub-phase timing).
// Names starting with "  " are treated as sub-phases of the preceding parent phase.
func (pt *PhaseTimer) AddResult(name string, duration time.Duration, items int, detail string) {
	pt.results = append(pt.results, PhaseResult{
		Name:       name,
		Duration:   duration,
		Items:      items,
		Detail:     detail,
		IsSubPhase: strings.HasPrefix(name, "  "),
	})
}

// StartWall begins wall-clock timing (captures real elapsed time including parallel work).
func (pt *PhaseTimer) StartWall() { pt.wallStart = time.Now() }

// StopWall ends wall-clock timing.
func (pt *PhaseTimer) StopWall() { pt.wallStop = time.Now() }

// WallDuration returns the wall-clock elapsed time between StartWall and StopWall.
// Returns zero if wall timing was not used.
func (pt *PhaseTimer) WallDuration() time.Duration {
	if pt.wallStart.IsZero() || pt.wallStop.IsZero() {
		return 0
	}
	return pt.wallStop.Sub(pt.wallStart)
}

// Results returns all recorded phase results.
func (pt *PhaseTimer) Results() []PhaseResult {
	return pt.results
}

// Total returns the sum of all top-level phase durations (excludes sub-phases).
func (pt *PhaseTimer) Total() time.Duration {
	var total time.Duration
	for _, r := range pt.results {
		if !r.IsSubPhase {
			total += r.Duration
		}
	}
	return total
}

// PrintTable writes a formatted ASCII table of phase timings to w.
func (pt *PhaseTimer) PrintTable(w io.Writer) {
	total := pt.Total()

	fmt.Fprintf(w, "\n%-4s %-30s %12s %10s %16s %6s\n",
		"#", "Phase", "Duration", "Items", "Rate", "%")
	fmt.Fprintln(w, strings.Repeat("\u2500", 82))

	phaseNum := 0
	for i, r := range pt.results {
		rate := "-"
		if r.Items > 0 && r.Duration > 0 {
			itemsPerSec := float64(r.Items) / r.Duration.Seconds()
			rate = fmt.Sprintf("%.0f/s", itemsPerSec)
		}

		items := "-"
		if r.Items > 0 {
			items = fmt.Sprintf("%d", r.Items)
		}

		if r.IsSubPhase {
			// Determine if this is the last sub-phase in a run
			isLast := i+1 >= len(pt.results) || !pt.results[i+1].IsSubPhase
			prefix := "├─ "
			if isLast {
				prefix = "└─ "
			}
			displayName := prefix + strings.TrimLeft(r.Name, " ")
			fmt.Fprintf(w, "%-4s %-30s %12s %10s %16s\n",
				"", displayName, formatDuration(r.Duration), items, rate)
		} else {
			phaseNum++
			pct := 0.0
			if total > 0 {
				pct = float64(r.Duration) / float64(total) * 100
			}
			fmt.Fprintf(w, "%-4d %-30s %12s %10s %16s %5.1f%%\n",
				phaseNum, r.Name, formatDuration(r.Duration), items, rate, pct)
		}
	}

	fmt.Fprintln(w, strings.Repeat("\u2500", 82))
	fmt.Fprintf(w, "%-4s %-30s %12s\n", "", "TOTAL (summed)", formatDuration(total))
	if wall := pt.WallDuration(); wall > 0 {
		fmt.Fprintf(w, "%-4s %-30s %12s\n", "", "WALL CLOCK", formatDuration(wall))
	}
	fmt.Fprintln(w)
}

// PrintJSON writes the phase results as JSON to w.
func (pt *PhaseTimer) PrintJSON(w io.Writer) error {
	type jsonResult struct {
		Name       string  `json:"name"`
		DurationMs float64 `json:"duration_ms"`
		Items      int     `json:"items"`
		Detail     string  `json:"detail,omitempty"`
		Percent    float64 `json:"percent"`
		SubPhase   bool    `json:"subPhase,omitempty"`
	}

	total := pt.Total()
	results := make([]jsonResult, len(pt.results))
	for i, r := range pt.results {
		pct := 0.0
		if total > 0 && !r.IsSubPhase {
			pct = float64(r.Duration) / float64(total) * 100
		}
		results[i] = jsonResult{
			Name:       r.Name,
			DurationMs: float64(r.Duration.Milliseconds()),
			Items:      r.Items,
			Detail:     r.Detail,
			Percent:    pct,
			SubPhase:   r.IsSubPhase,
		}
	}

	wallMs := float64(pt.WallDuration().Milliseconds())

	output := struct {
		Phases   []jsonResult `json:"phases"`
		TotalMs  float64      `json:"total_ms"`
		TotalStr string       `json:"total"`
		WallMs   float64      `json:"wall_ms,omitempty"`
		WallStr  string       `json:"wall,omitempty"`
	}{
		Phases:   results,
		TotalMs:  float64(total.Milliseconds()),
		TotalStr: formatDuration(total),
		WallMs:   wallMs,
		WallStr:  formatDuration(pt.WallDuration()),
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(output)
}

func formatDuration(d time.Duration) string {
	if d < time.Millisecond {
		return fmt.Sprintf("%.2fms", float64(d.Microseconds())/1000.0)
	}
	if d < time.Second {
		return fmt.Sprintf("%.0fms", float64(d.Milliseconds()))
	}
	return fmt.Sprintf("%.2fs", d.Seconds())
}
