package llmtest

import (
	"context"
	"math"
	"testing"
)

// TestEmbedderDeterminismAndNorm pins the properties the semlink integration
// tests depend on: identical texts → identical vectors, distinct texts →
// distinct vectors, unit norm (so cosine geometry is exact), call counting.
func TestEmbedderDeterminismAndNorm(t *testing.T) {
	e := NewEmbedder(16)
	v1, err := e.Embed(context.Background(), []string{"hello", "world", "hello"})
	if err != nil {
		t.Fatal(err)
	}

	for i := range v1[0] {
		if v1[0][i] != v1[2][i] {
			t.Fatal("identical texts must embed identically")
		}
	}

	same := true
	for i := range v1[0] {
		if v1[0][i] != v1[1][i] {
			same = false
			break
		}
	}
	if same {
		t.Error("different texts must embed differently")
	}

	var norm float64
	for _, x := range v1[0] {
		norm += float64(x) * float64(x)
	}
	if math.Abs(norm-1.0) > 1e-5 {
		t.Errorf("vectors must be unit-norm, got %v", norm)
	}

	if e.Calls() != 3 {
		t.Errorf("Calls() = %d, want 3", e.Calls())
	}
}

// TestCompleterModelAndHooks pins the Completer double's contract: a stable
// canned summary by default, Fn override, call counting, and a Model() name
// that reaches summaryModel provenance stamps.
func TestCompleterModelAndHooks(t *testing.T) {
	c := &Completer{}
	if c.Model() != "fake-completer" {
		t.Errorf("Model() = %q, want fake-completer", c.Model())
	}

	out1, err := c.Complete(context.Background(), "sys", "user")
	if err != nil {
		t.Fatal(err)
	}
	out2, _ := c.Complete(context.Background(), "sys", "user")
	if out1 != out2 {
		t.Error("identical prompts must complete identically")
	}

	c.Fn = func(system, user string) (string, error) { return "hooked", nil }
	if out, _ := c.Complete(context.Background(), "s", "u"); out != "hooked" {
		t.Errorf("Fn override ignored, got %q", out)
	}
	if c.Calls() != 3 {
		t.Errorf("Calls() = %d, want 3", c.Calls())
	}
}
