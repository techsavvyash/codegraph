// Package main is a tiny fixture module exercising every construct the Go
// differential oracle needs to classify: a plain function call, a method
// call, an interface call (static: no edge to either impl; CHA: edges to
// both), a closure whose own call is excluded but whose *body calls* fold
// up to the enclosing named function, and a generic function called at two
// instantiations (deduped to its origin in SSA).
package main

// --- package-level function calling another function ---

func add(a, b int) int {
	return a + b
}

func compute() int {
	return add(1, 2)
}

// --- method calling a method ---

type Counter struct {
	n int
}

func (c *Counter) Increment() {
	c.bump()
}

func (c *Counter) bump() {
	c.n++
}

// --- interface with two implementations, called through the interface ---

type Greeter interface {
	Greet() string
}

type EnglishGreeter struct{}

func (EnglishGreeter) Greet() string { return "hello" }

type FrenchGreeter struct{}

func (FrenchGreeter) Greet() string { return "bonjour" }

func greetVia(g Greeter) string {
	return g.Greet()
}

func useGreeters() {
	greetVia(EnglishGreeter{})
	greetVia(FrenchGreeter{})
}

// --- closure: the closure call itself is excluded from both must and may
// graphs, but a call made INSIDE the closure body folds up to the
// enclosing named function (withClosure), matching scip-go's
// containment-based attribution. ---

func withClosure() int {
	adder := func(x int) int {
		return x + add(x, 1)
	}
	return adder(41)
}

// --- generic function called at two instantiations, deduped to origin ---

func identity[T any](v T) T {
	return v
}

func useGenerics() (int, string) {
	return identity(1), identity("two")
}

func main() {
	compute()
	c := &Counter{}
	c.Increment()
	useGreeters()
	withClosure()
	useGenerics()
}
