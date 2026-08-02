// Package main is a tiny fixture module exercising every construct the Go
// differential oracle needs to classify: a plain function call, a method
// call, an interface call (static: no edge to either impl; CHA: edges to
// both), a closure whose own call is excluded but whose *body calls* fold
// up to the enclosing named function, and a generic function called at two
// instantiations (deduped to its origin in SSA).
package main

// --- interface dispatch as a stand-in for framework command dispatch:
// main hands a concrete Handler to runHandler, which invokes h.Handle()
// through the interface. There is NO static edge runHandler -> myHandler.Handle
// (the receiver is the Handler interface, not the concrete type), so
// reachedViaDispatch is reachable from main ONLY through the CHA may-edge that
// resolves the interface call to its concrete implementation.
//
// This is the in-module analog of the real cobra bug: a binary reaches its
// RunE handler through interface dispatch, and the dead-verdict cross-check
// must follow that dispatch or it goes vacuous. (Cross-MODULE dispatch — into
// a dependency's own body — is deliberately NOT traversed: MainReachable is
// built over ssautil.Packages, whose dependency bodies are type-info stubs,
// because building every dependency body via ssautil.AllPackages balloons CHA
// into near-total connectivity and makes the reachability set vacuously full.
// See the load-mode comment in goextract.go.) ---

type Handler interface {
	Handle()
}

type myHandler struct{}

func (myHandler) Handle() {
	reachedViaDispatch()
}

func runHandler(h Handler) {
	h.Handle()
}

func reachedViaDispatch() {}

// --- init roots: a declared init() calls reachedViaInit(). MainReachable
// must treat both the synthesized package initializer and declared inits as
// roots, so reachedViaInit is reachable even though nothing else calls it. ---

func reachedViaInit() {}

func init() {
	reachedViaInit()
}

// --- genuinely unreachable: called from nothing, so it must be absent from
// MainReachable and (when dead-stamped) a correct dead verdict, not a
// disagreement. ---

func neverCalled() {}

// --- escaped-but-never-called: the cobra RunE registration pattern. main
// stores escapedNotCalled into a registry field; nothing in this module ever
// invokes reg.fn — in the real binary a dependency body we deliberately do
// not traverse would. The BFS escape rule must mark escapedNotCalled AND its
// body's callee reachedViaEscape reachable, mirroring the classifier's
// USES_VALUE (address-taken is liveness-preserving) rule. ---

type registry struct{ fn func() }

var reg registry

func escapedNotCalled() { reachedViaEscape() }

func reachedViaEscape() {}

func register() {
	reg.fn = escapedNotCalled
}

// --- package-level function value: `var pkgHandler = pkgEscaped` takes
// pkgEscaped's address at package-init time and never calls it. The
// synthesized package initializer (`init`, a BFS root) is where the SSA store
// of the function constant lives, so the escape rule must catch pkgEscaped
// (and its body callee reachedViaPkgVar) purely from the operand scan of init
// — no call edge ever reaches either. This is the address-taken-at-load-time
// analog of the RunE-field pattern above. ---

var pkgHandler = pkgEscaped

func pkgEscaped() { reachedViaPkgVar() }

func reachedViaPkgVar() {}

// _ keeps pkgHandler from being an unused-variable compile error without
// calling it (an actual call would defeat the address-taken-only premise).
var _ = pkgHandler

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
	runHandler(myHandler{})
	register()
}
