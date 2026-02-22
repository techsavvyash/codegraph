package benchmarks

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestPhaseTimerStartStop(t *testing.T) {
	pt := NewPhaseTimer()

	pt.Start("test phase")
	time.Sleep(10 * time.Millisecond)
	pt.Stop(42, "some detail")

	results := pt.Results()
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	r := results[0]
	if r.Name != "test phase" {
		t.Errorf("expected name 'test phase', got %q", r.Name)
	}
	if r.Duration <= 0 {
		t.Errorf("expected positive duration, got %v", r.Duration)
	}
	if r.Items != 42 {
		t.Errorf("expected 42 items, got %d", r.Items)
	}
	if r.Detail != "some detail" {
		t.Errorf("expected detail 'some detail', got %q", r.Detail)
	}
}

func TestPhaseTimerMultiplePhases(t *testing.T) {
	pt := NewPhaseTimer()

	pt.Start("phase 1")
	time.Sleep(5 * time.Millisecond)
	pt.Stop(10, "")

	pt.Start("phase 2")
	time.Sleep(5 * time.Millisecond)
	pt.Stop(20, "")

	pt.Start("phase 3")
	time.Sleep(5 * time.Millisecond)
	pt.Stop(30, "")

	results := pt.Results()
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	// Verify order
	for i, name := range []string{"phase 1", "phase 2", "phase 3"} {
		if results[i].Name != name {
			t.Errorf("result %d: expected name %q, got %q", i, name, results[i].Name)
		}
	}

	// Verify total >= sum of individual durations
	total := pt.Total()
	if total <= 0 {
		t.Errorf("expected positive total, got %v", total)
	}

	var sum time.Duration
	for _, r := range results {
		sum += r.Duration
	}
	if total != sum {
		t.Errorf("expected total %v to equal sum %v", total, sum)
	}
}

func TestPhaseTimerAutoStop(t *testing.T) {
	pt := NewPhaseTimer()

	pt.Start("phase 1")
	time.Sleep(5 * time.Millisecond)
	// Start phase 2 without stopping phase 1 — should auto-stop
	pt.Start("phase 2")
	time.Sleep(5 * time.Millisecond)
	pt.Stop(5, "")

	results := pt.Results()
	if len(results) != 2 {
		t.Fatalf("expected 2 results (auto-stop), got %d", len(results))
	}
	if results[0].Name != "phase 1" {
		t.Errorf("expected first phase 'phase 1', got %q", results[0].Name)
	}
	if results[0].Items != 0 {
		t.Errorf("auto-stopped phase should have 0 items, got %d", results[0].Items)
	}
}

func TestPhaseTimerPrintTable(t *testing.T) {
	pt := NewPhaseTimer()

	pt.Start("Generate index")
	time.Sleep(5 * time.Millisecond)
	pt.Stop(0, "")

	pt.Start("Parse SCIP")
	time.Sleep(5 * time.Millisecond)
	pt.Stop(100, "100 symbols")

	var buf bytes.Buffer
	pt.PrintTable(&buf)

	output := buf.String()

	// Verify table contains expected headers and phase names
	if !strings.Contains(output, "Phase") {
		t.Error("table should contain 'Phase' header")
	}
	if !strings.Contains(output, "Duration") {
		t.Error("table should contain 'Duration' header")
	}
	if !strings.Contains(output, "Generate index") {
		t.Error("table should contain phase name 'Generate index'")
	}
	if !strings.Contains(output, "Parse SCIP") {
		t.Error("table should contain phase name 'Parse SCIP'")
	}
	if !strings.Contains(output, "TOTAL") {
		t.Error("table should contain 'TOTAL' row")
	}
}

func TestPhaseTimerPrintJSON(t *testing.T) {
	pt := NewPhaseTimer()

	pt.Start("phase A")
	time.Sleep(5 * time.Millisecond)
	pt.Stop(10, "detail A")

	var buf bytes.Buffer
	if err := pt.PrintJSON(&buf); err != nil {
		t.Fatalf("PrintJSON failed: %v", err)
	}

	var output map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &output); err != nil {
		t.Fatalf("invalid JSON output: %v", err)
	}

	phases, ok := output["phases"].([]interface{})
	if !ok || len(phases) != 1 {
		t.Fatalf("expected 1 phase in JSON, got %v", output["phases"])
	}

	if _, ok := output["total_ms"]; !ok {
		t.Error("JSON should contain 'total_ms'")
	}
}

func TestPhaseTimerStopWithoutStart(t *testing.T) {
	pt := NewPhaseTimer()
	// Should be a no-op, not panic
	pt.Stop(10, "")
	if len(pt.Results()) != 0 {
		t.Error("stopping without starting should produce no results")
	}
}

func TestPhaseTimerAddResult(t *testing.T) {
	pt := NewPhaseTimer()

	pt.Start("parent phase")
	time.Sleep(5 * time.Millisecond)
	pt.Stop(100, "")

	// Add sub-phase results
	pt.AddResult("  MergeNode(Symbol)", 3*time.Second, 50, "")
	pt.AddResult("  CreateRel(DEFINES)", 2*time.Second, 50, "")

	results := pt.Results()
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	// Verify sub-phases are marked correctly
	if results[0].IsSubPhase {
		t.Error("parent phase should not be a sub-phase")
	}
	if !results[1].IsSubPhase {
		t.Error("first added result should be a sub-phase")
	}
	if !results[2].IsSubPhase {
		t.Error("second added result should be a sub-phase")
	}

	// Verify sub-phase values
	if results[1].Duration != 3*time.Second {
		t.Errorf("expected 3s duration, got %v", results[1].Duration)
	}
	if results[1].Items != 50 {
		t.Errorf("expected 50 items, got %d", results[1].Items)
	}

	// Verify Total() excludes sub-phases
	total := pt.Total()
	if total >= 1*time.Second {
		t.Errorf("Total() should exclude sub-phases, got %v", total)
	}
}

func TestPhaseTimerSubPhaseRendering(t *testing.T) {
	pt := NewPhaseTimer()

	pt.Start("Index defs")
	time.Sleep(5 * time.Millisecond)
	pt.Stop(100, "")

	pt.AddResult("  MergeNode(Symbol)", 3*time.Second, 100, "")
	pt.AddResult("  MergeNode(Definition)", 2*time.Second, 90, "")
	pt.AddResult("  CreateRel(DEFINES)", 1*time.Second, 90, "")

	pt.Start("Index refs")
	time.Sleep(5 * time.Millisecond)
	pt.Stop(200, "")

	var buf bytes.Buffer
	pt.PrintTable(&buf)
	output := buf.String()

	// Should contain tree characters
	if !strings.Contains(output, "├─") {
		t.Error("table should contain '├─' for non-last sub-phases")
	}
	if !strings.Contains(output, "└─") {
		t.Error("table should contain '└─' for last sub-phase")
	}

	// Sub-phases should not have numbered prefixes
	if strings.Contains(output, "2    MergeNode") || strings.Contains(output, "3    MergeNode") {
		t.Error("sub-phases should not be numbered")
	}

	// Phase numbering should skip sub-phases: "Index defs" = 1, "Index refs" = 2
	if !strings.Contains(output, "1    Index defs") {
		t.Error("first parent phase should be numbered 1")
	}
	if !strings.Contains(output, "2    Index refs") {
		t.Error("second parent phase should be numbered 2")
	}
}

func TestPhaseTimerSubPhaseJSON(t *testing.T) {
	pt := NewPhaseTimer()

	pt.Start("parent")
	time.Sleep(5 * time.Millisecond)
	pt.Stop(10, "")

	pt.AddResult("  sub1", 1*time.Second, 5, "")

	var buf bytes.Buffer
	if err := pt.PrintJSON(&buf); err != nil {
		t.Fatalf("PrintJSON failed: %v", err)
	}

	var output map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &output); err != nil {
		t.Fatalf("invalid JSON output: %v", err)
	}

	phases, ok := output["phases"].([]interface{})
	if !ok || len(phases) != 2 {
		t.Fatalf("expected 2 phases in JSON, got %v", output["phases"])
	}

	// Second phase should have subPhase: true
	subPhase := phases[1].(map[string]interface{})
	if v, ok := subPhase["subPhase"]; !ok || v != true {
		t.Errorf("sub-phase should have subPhase: true, got %v", subPhase)
	}

	// Sub-phase should have percent 0
	if pct, ok := subPhase["percent"].(float64); !ok || pct != 0 {
		t.Errorf("sub-phase percent should be 0, got %v", subPhase["percent"])
	}
}
