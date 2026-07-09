package static

import (
	"strings"
	"testing"
)

func TestIndexReportAddPhase(t *testing.T) {
	r := NewIndexReport()

	p1 := r.AddPhase("symbols")
	if p1 == nil {
		t.Fatal("AddPhase should not return nil")
	}
	if p1.Phase != "symbols" || p1.Written != 0 || p1.Skipped != 0 || p1.Failed != 0 {
		t.Errorf("AddPhase created wrong initial state: %+v", p1)
	}

	// Retrieve same phase — should return existing entry
	p1b := r.AddPhase("symbols")
	if p1b != p1 {
		t.Errorf("AddPhase should return same entry on second call")
	}
}

func TestIndexReportIncrements(t *testing.T) {
	r := NewIndexReport()

	r.IncrementWritten("symbols", 5)
	r.IncrementSkipped("symbols", 2)
	r.IncrementFailed("symbols", 1)

	p := r.AddPhase("symbols")
	if p.Written != 5 || p.Skipped != 2 || p.Failed != 1 {
		t.Errorf("Increment mismatch: %+v", p)
	}

	// Increment again
	r.IncrementWritten("symbols", 3)
	if p.Written != 8 {
		t.Errorf("Increment should cumulate: expected 8, got %d", p.Written)
	}
}

func TestIndexReportHasFailures(t *testing.T) {
	r := NewIndexReport()

	if r.HasFailures() {
		t.Errorf("Empty report should have no failures")
	}

	r.IncrementWritten("symbols", 5)
	if r.HasFailures() {
		t.Errorf("Report with only Written should have no failures")
	}

	r.IncrementFailed("symbols", 1)
	if !r.HasFailures() {
		t.Errorf("Report with Failed>0 should report HasFailures()=true")
	}
}

func TestIndexReportStringDeterministic(t *testing.T) {
	r := NewIndexReport()

	// Insert phases in specific order
	r.IncrementWritten("files", 10)
	r.IncrementSkipped("files", 1)

	r.IncrementWritten("symbols", 20)
	r.IncrementFailed("symbols", 2)

	r.IncrementWritten("references", 30)

	str := r.String()
	lines := strings.Split(str, "\n")

	// Check that header is present
	if !strings.Contains(lines[0], "Indexing Report") {
		t.Errorf("First line should contain header, got: %s", lines[0])
	}

	// Check phase order (insertion order)
	fileIdx := -1
	symbolIdx := -1
	refIdx := -1

	for i, line := range lines {
		if strings.Contains(line, "files:") {
			fileIdx = i
		}
		if strings.Contains(line, "symbols:") {
			symbolIdx = i
		}
		if strings.Contains(line, "references:") {
			refIdx = i
		}
	}

	if fileIdx < 0 || symbolIdx < 0 || refIdx < 0 {
		t.Fatalf("Could not find all phases in output:\n%s", str)
	}

	if !(fileIdx < symbolIdx && symbolIdx < refIdx) {
		t.Errorf("Phases not in insertion order. fileIdx=%d, symbolIdx=%d, refIdx=%d",
			fileIdx, symbolIdx, refIdx)
	}

	// Check counts format
	if !strings.Contains(str, "written=10") || !strings.Contains(str, "skipped=1") {
		t.Errorf("Files phase counts not found in:\n%s", str)
	}
	if !strings.Contains(str, "failed=2") {
		t.Errorf("Symbols failed count not found in:\n%s", str)
	}
}

func TestIndexReportWarnings(t *testing.T) {
	r := NewIndexReport()

	r.AddWarning("first warning")
	r.AddWarning("second warning")

	str := r.String()
	if !strings.Contains(str, "Warnings:") {
		t.Errorf("Report should contain Warnings section")
	}
	if !strings.Contains(str, "first warning") {
		t.Errorf("First warning not found")
	}
	if !strings.Contains(str, "second warning") {
		t.Errorf("Second warning not found")
	}
}

func TestIndexReportFailedPhaseCount(t *testing.T) {
	r := NewIndexReport()

	if r.FailedPhaseCount() != 0 {
		t.Errorf("Empty report should have 0 failed phases")
	}

	r.IncrementWritten("phase1", 5)
	r.IncrementWritten("phase2", 5)
	if r.FailedPhaseCount() != 0 {
		t.Errorf("Phases with only Written should not count as failed")
	}

	r.IncrementFailed("phase1", 1)
	if r.FailedPhaseCount() != 1 {
		t.Errorf("Expected 1 failed phase, got %d", r.FailedPhaseCount())
	}

	r.IncrementFailed("phase2", 1)
	if r.FailedPhaseCount() != 2 {
		t.Errorf("Expected 2 failed phases, got %d", r.FailedPhaseCount())
	}

	// Adding more failures to phase1 shouldn't increase the count
	r.IncrementFailed("phase1", 5)
	if r.FailedPhaseCount() != 2 {
		t.Errorf("Phase with multiple failures should count as 1 failed phase")
	}
}

func TestIndexReportNoPhases(t *testing.T) {
	r := NewIndexReport()
	str := r.String()

	if !strings.Contains(str, "(no phases recorded)") {
		t.Errorf("Empty report should say '(no phases recorded)'")
	}
}

func TestIndexReportCumulativeIncrements(t *testing.T) {
	r := NewIndexReport()

	// Simulate multiple batches incrementing the same phase
	r.IncrementWritten("symbols", 100)
	r.IncrementWritten("symbols", 50)
	r.IncrementWritten("symbols", 25)

	p := r.AddPhase("symbols")
	if p.Written != 175 {
		t.Errorf("Expected cumulative Written=175, got %d", p.Written)
	}
}
