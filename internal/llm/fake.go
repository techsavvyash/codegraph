package llm

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"math"
	"sync"
)

// FakeEmbedder is a deterministic, network-free Embedder for tests and local
// pipelines. Identical texts embed identically; different texts are nearly
// orthogonal (hash-derived). Tests that need controlled geometry can set Fn.
type FakeEmbedder struct {
	Dim int
	// Fn overrides the vector for a text; nil entries fall back to hashing.
	Fn func(text string) []float32

	mu    sync.Mutex
	calls int
}

// NewFakeEmbedder returns a hash-based FakeEmbedder of the given dimension.
func NewFakeEmbedder(dim int) *FakeEmbedder {
	return &FakeEmbedder{Dim: dim}
}

func (f *FakeEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
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

func (f *FakeEmbedder) Dimensions() int { return f.Dim }
func (f *FakeEmbedder) Model() string   { return "fake-embedder" }

// Calls returns how many texts have been embedded (budget assertions).
func (f *FakeEmbedder) Calls() int {
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
	for i := 0; i < dim; i++ {
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

// FakeCompleter is a deterministic Completer. Fn, when set, controls the
// output (judge tests); otherwise a stable canned summary is returned.
type FakeCompleter struct {
	Fn func(system, user string) (string, error)

	mu    sync.Mutex
	calls int
}

func (f *FakeCompleter) Complete(_ context.Context, system, user string) (string, error) {
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
func (f *FakeCompleter) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func hexPrefix(b []byte, n int) string {
	const hextable = "0123456789abcdef"
	if n > len(b) {
		n = len(b)
	}
	out := make([]byte, n*2)
	for i := 0; i < n; i++ {
		out[i*2] = hextable[b[i]>>4]
		out[i*2+1] = hextable[b[i]&0x0f]
	}
	return string(out)
}
