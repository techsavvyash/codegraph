package semlink

import (
	"context"
	"math"
	"sync"
	"sync/atomic"
	"testing"
)

func TestParseJudgeVerdict(t *testing.T) {
	cases := []struct {
		raw   string
		match bool
		conf  float64
		ok    bool
	}{
		{`{"match": true, "confidence": 0.9}`, true, 0.9, true},
		{`Sure! Here is my verdict: {"match": false, "confidence": 0.2} hope that helps`, false, 0.2, true},
		{"```json\n{\"match\": true, \"confidence\": 1}\n```", true, 1, true},
		{`no json at all`, false, 0, false},
		{`{broken`, false, 0, false},
		{``, false, 0, false},
	}
	for _, tc := range cases {
		match, conf, ok := parseJudgeVerdict(tc.raw)
		if match != tc.match || conf != tc.conf || ok != tc.ok {
			t.Errorf("parseJudgeVerdict(%q) = (%v, %v, %v), want (%v, %v, %v)",
				tc.raw, match, conf, ok, tc.match, tc.conf, tc.ok)
		}
	}
}

// TestSemanticConfidenceMapping pins the RFC-011 §6 bands: judge-confirmed in
// [0.30, 0.60], similarity-only ≤ 0.55, both below Layer D's 0.70 floor.
func TestSemanticConfidenceMapping(t *testing.T) {
	for _, jconf := range []float64{0, 0.5, 1, 2, -1} {
		conf := judgeConfBase + judgeConfSlope*clamp01(jconf)
		if conf < 0.30 || conf > 0.60 {
			t.Errorf("judge confidence %v maps to %v, outside [0.30, 0.60]", jconf, conf)
		}
	}

	for _, cos := range []float64{0.78, 0.9, 1.0} {
		conf := cos * simOnlyScale
		if conf > simOnlyCap {
			conf = simOnlyCap
		}
		if conf > 0.55 {
			t.Errorf("sim-only confidence for cos %v = %v exceeds cap", cos, conf)
		}
		if conf >= 0.70 {
			t.Errorf("semantic confidence %v reaches the deterministic floor", conf)
		}
	}
}

func TestOptionsDefaults(t *testing.T) {
	o := Options{}.withDefaults()
	if o.SimilarityThreshold != 0.78 || o.TopK != 10 || o.MaxLLMCalls != 2000 || o.Concurrency != 8 {
		t.Errorf("defaults wrong: %+v", o)
	}
	// The judge defaults ON: an unset Options{} must select the validated
	// mode, not silently degrade to similarity-only (regression guard — this
	// was a real bug).
	if !o.judgeEnabled() {
		t.Error("judge must default to enabled")
	}

	// Explicit values survive.
	off := false
	o = Options{SimilarityThreshold: 0.5, TopK: 3, MaxLLMCalls: 7, Judge: &off, Concurrency: 1}.withDefaults()
	if o.SimilarityThreshold != 0.5 || o.TopK != 3 || o.MaxLLMCalls != 7 || o.Concurrency != 1 {
		t.Errorf("explicit options clobbered: %+v", o)
	}
	if o.judgeEnabled() {
		t.Error("explicit Judge=false must survive withDefaults")
	}
}

// TestSpendBudgetConcurrent hammers the budget from many goroutines: the
// reservation must be exact — precisely MaxLLMCalls grants, never more —
// because parallel summarize/judge workers all draw from it.
func TestSpendBudgetConcurrent(t *testing.T) {
	r := &Runner{opts: Options{MaxLLMCalls: 50}.withDefaults()}
	const workers = 200

	var granted atomic.Int64
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if r.spendBudget() {
				granted.Add(1)
			}
		}()
	}
	wg.Wait()

	if granted.Load() != 50 {
		t.Errorf("granted = %d, want exactly 50", granted.Load())
	}
	if r.budgetUsed != 50 {
		t.Errorf("budgetUsed = %d, want 50", r.budgetUsed)
	}
}

// namedCompleter and anonCompleter pin summaryModelName's two paths.
type namedCompleter struct{}

func (namedCompleter) Complete(_ context.Context, _, _ string) (string, error) { return "", nil }
func (namedCompleter) Model() string                                           { return "gpt-5-nano" }

type anonCompleter struct{}

func (anonCompleter) Complete(_ context.Context, _, _ string) (string, error) { return "", nil }

// TestSummaryModelName guards the provenance stamp: a self-describing
// completer's model name must reach summaryModel (regression: the real
// openai-compat completer lacked Model() and every node got "completer").
func TestSummaryModelName(t *testing.T) {
	if got := (&Runner{completer: namedCompleter{}}).summaryModelName(); got != "gpt-5-nano" {
		t.Errorf("named completer: summaryModelName() = %q, want gpt-5-nano", got)
	}
	if got := (&Runner{completer: anonCompleter{}}).summaryModelName(); got != "completer" {
		t.Errorf("anonymous completer: summaryModelName() = %q, want completer fallback", got)
	}
}

// TestScoreToCosineConversion documents the Neo4j score normalization the
// matcher undoes: score = (cos+1)/2.
func TestScoreToCosineConversion(t *testing.T) {
	for _, tc := range []struct{ score, cos float64 }{
		{1.0, 1.0}, {0.5, 0.0}, {0.89, 0.78}, {0.0, -1.0},
	} {
		got := 2*tc.score - 1
		if math.Abs(got-tc.cos) > 1e-9 {
			t.Errorf("score %v → cos %v, want %v", tc.score, got, tc.cos)
		}
	}
}
