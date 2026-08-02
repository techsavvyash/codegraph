// Package impls holds the package-scope named concrete types that
// structurally satisfy the graph-invisible interfaces exercised in the parent
// package's call sites. These are the resolver's "candidates".
package impls

// ValModel satisfies a `Model() string` interface via a VALUE receiver, so
// both ValModel and *ValModel qualify.
type ValModel struct{ name string }

func (m ValModel) Model() string { return m.name }

// PtrModel satisfies a `Model() string` interface only via POINTER receiver.
type PtrModel struct{ name string }

func (m *PtrModel) Model() string { return m.name }

// NearMiss has a Model method with a DIFFERENT signature (returns int, not
// string). It must NOT match a `Model() string` interface.
type NearMiss struct{}

func (n NearMiss) Model() int { return 0 }

// base declares Describe; Embedder promotes it. The resolver must attribute
// the callee to base (the DECLARING type), not Embedder.
type base struct{}

func (b base) Describe() string { return "base" }

// Embedder embeds base, promoting Describe onto Embedder. It satisfies a
// `Describe() string` interface through the promoted method.
type Embedder struct {
	base
}

// Namer satisfies a `Name() string` interface used through a generic type
// parameter, via value receiver.
type Namer struct{ n string }

func (x Namer) Name() string { return x.n }
