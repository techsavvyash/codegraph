package main

// Numeric constrains Sum's type parameter. No concrete type in this fixture
// ever names Numeric in an "implements"-shaped position — Go generics are
// structural-constraint satisfaction resolved by the compiler at
// instantiation, not nominal interface implementation, so the pipeline
// should never emit an IMPLEMENTS edge from int/float64 (or any type) to
// Numeric. This file exists to empirically check that claim against the
// live pipeline output rather than assume it.
type Numeric interface {
	~int | ~float64
}

func Sum[T Numeric](xs []T) T {
	var total T
	for _, x := range xs {
		total += x
	}
	return total
}

// useSum instantiates Sum with a concrete numeric type so the symbol isn't
// dead code the pipeline might drop for unrelated reasons.
func useSum() int {
	return Sum([]int{1, 2, 3})
}
