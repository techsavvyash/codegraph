// Package llmtest provides deterministic, network-free test doubles for the
// llm provider contract (the httptest idiom). It is test support only: no
// production code imports it, and there is no "fake" provider in llm.New —
// tests construct these doubles directly.
package llmtest

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"math"
	"sync"

	"github.com/context-maximiser/code-graph/internal/llm"
)

// Compile-time conformance to the production contract.
var (
	_ llm.Embedder  = (*Embedder)(nil)
	_ llm.Completer = (*Completer)(nil)
)

// Embedder is a deterministic, network-free llm.Embedder. Identical texts
// embed identically; different texts are nearly orthogonal (hash-derived
// unit vectors). Tests that need controlled geometry can set Fn.
type Embedder struct {
	Dim int
	// Fn overrides the vector for a text; nil entries fall back to hashing.
	Fn func(text string) []float32

	mu    sync.Mutex
	calls int
}

// NewEmbedder returns a hash-based Embedder of the given dimension.
func NewEmbedder(dim int) *Embedder {
	return &Embedder{Dim: dim}
}

func (f *Embedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	f.mu.Lock()
	f.calls += len(texts)
	f.mu.Unlock()

	out := make([][]float32, len(texts))
	for i, t := range texts {
		if f.Fn != nil {
			if v := f.Fn(t); v != nil {
				out[i] = v
				continue
			}
		}
		out[i] = hashVector(t, f.Dim)
	}
	return out, nil
}

func (f *Embedder) Dimensions() int { return f.Dim }
func (f *Embedder) Model() string   { return "fake-embedder" }

// Calls returns how many texts have been embedded (budget assertions).
func (f *Embedder) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// Completer is a deterministic llm.Completer. Fn, when set, controls the
// output (judge tests); otherwise a stable canned summary is returned.
type Completer struct {
	Fn func(system, user string) (string, error)

	mu    sync.Mutex
	calls int
}

// Model mirrors the real adapters so summaryModel stamps are meaningful in
// fake-provider runs too.
func (f *Completer) Model() string { return "fake-completer" }

func (f *Completer) Complete(_ context.Context, system, user string) (string, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()

	if f.Fn != nil {
		return f.Fn(system, user)
	}
	// Stable, obviously-fake output: enough for summary-plumbing tests.
	sum := sha256.Sum256([]byte(system + "\x00" + user))
	return "fake-summary-" + hexPrefix(sum[:], 8), nil
}

// Calls returns how many completions have been requested.
func (f *Completer) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// hashVector derives a unit-norm vector from the SHA-256 of text.
func hashVector(text string, dim int) []float32 {
	seed := sha256.Sum256([]byte(text))
	v := make([]float32, dim)
	var norm float64
	buf := seed[:]
	for i := range dim {
		if len(buf) < 4 {
			next := sha256.Sum256(buf)
			buf = next[:]
		}
		bits := binary.BigEndian.Uint32(buf[:4])
		buf = buf[4:]
		val := float64(int32(bits)) / float64(math.MaxInt32)
		v[i] = float32(val)
		norm += val * val
	}
	if norm > 0 {
		scale := float32(1 / math.Sqrt(norm))
		for i := range v {
			v[i] *= scale
		}
	}
	return v
}

func hexPrefix(b []byte, n int) string {
	const hextable = "0123456789abcdef"
	if n > len(b) {
		n = len(b)
	}
	out := make([]byte, n*2)
	for i := range n {
		out[i*2] = hextable[b[i]>>4]
		out[i*2+1] = hextable[b[i]&0x0f]
	}
	return string(out)
}
