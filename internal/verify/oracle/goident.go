package oracle

import (
	"go/types"
	"strings"

	"golang.org/x/tools/go/ssa"
)

// goFuncID is the normalized (packagePath, receiverTypeName, funcName) view
// of an ssa.Function used as both the SSA-side and graph-side join key. It
// mirrors the shape scip-go symbol descriptors parse into (see
// resolve.ParseGoSymbolDescriptor) so edges from either side compare equal
// without ever string-constructing a SCIP symbol.
type goFuncID struct {
	pkgPath  string
	typeName string // "" for package-level functions
	funcName string
}

// edgeKey is a caller->callee pair keyed on the normalized function
// identity, used as the map key for must/may/graph edge sets.
type edgeKey struct {
	from goFuncID
	to   goFuncID
}

// funcIdentity derives a goFuncID for an *ssa.Function, taking Origin() for
// generic instantiations so every instantiation of a generic function or
// method dedupes to a single identity — matching how a single SCIP symbol
// covers a generic declaration regardless of how many concrete
// instantiations the indexer observed. Returns ok=false for functions with
// no stable package/type identity (e.g. the SSA program's synthetic root).
func funcIdentity(fn *ssa.Function) (goFuncID, bool) {
	if fn == nil {
		return goFuncID{}, false
	}
	if orig := fn.Origin(); orig != nil {
		fn = orig
	}
	if fn.Pkg == nil || fn.Pkg.Pkg == nil {
		return goFuncID{}, false
	}
	pkgPath := fn.Pkg.Pkg.Path()

	typeName := ""
	if recv := fn.Signature.Recv(); recv != nil {
		typeName = namedTypeName(recv.Type())
		if typeName == "" {
			return goFuncID{}, false
		}
	}

	name := fn.Name()
	if name == "" || strings.Contains(name, "$") {
		return goFuncID{}, false // anonymous/closure
	}

	return goFuncID{pkgPath: pkgPath, typeName: typeName, funcName: name}, true
}

// namedTypeName unwraps pointer receivers and returns the underlying named
// type's name, or "" if the receiver isn't a (possibly pointer-to) named
// type (shouldn't happen for method receivers in valid Go, but defensively
// excluded rather than panicking).
func namedTypeName(t types.Type) string {
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}
	named, ok := t.(*types.Named)
	if !ok {
		return ""
	}
	return named.Obj().Name()
}

// isExcludedFunc reports whether fn should be excluded from both the must
// and may SSA-side edge sets: synthetic wrappers/thunks/bounds, init
// functions, anonymous functions/closures, and the `go test` toolchain's
// generated driver package. Method-value thunks and bound-method closures
// are covered by the Synthetic-string check (SSA labels them "bound method
// wrapper" / "value method wrapper" / "thunk") and by the closure name
// check.
//
// Closures as CALLEES stay excluded here — the indexed graph never has a
// node for an anonymous function literal, so there is nothing to join a
// call to one against. Closures as CALLERS are handled separately, by
// enclosingNamedFunc folding the call up to the nearest named function
// before isExcludedFunc/funcIdentity ever see it as a caller — see fold()
// in goextract.go.
//
// Generic instantiations are a deliberate exception: ssa.InstantiateGenerics
// gives each instantiation (e.g. identity[int]) Synthetic == "instance of
// <origin>" and a nil Pkg, even though it is a real, non-synthetic call in
// source. funcIdentity resolves these back to their origin function via
// Origin(), so excluding on the raw Synthetic string here would silently
// drop every generic call — hence the carve-out.
func isExcludedFunc(fn *ssa.Function) bool {
	if fn == nil {
		return true
	}
	if fn.Synthetic != "" && !strings.HasPrefix(fn.Synthetic, "instance of ") {
		return true
	}
	if fn.Name() == "init" {
		return true
	}
	if fn.Parent() != nil {
		return true // anonymous function/closure literal
	}
	if strings.Contains(fn.Name(), "$") {
		return true
	}
	if fn.Pkg != nil && fn.Pkg.Pkg != nil && strings.HasSuffix(fn.Pkg.Pkg.Path(), ".test") {
		// packages.Config{Tests: true} synthesizes a "<pkgpath>.test" driver
		// package holding the compiler-generated main() that calls
		// TestMain/reflect-drives the test binary. It is a toolchain
		// artifact with no corresponding source, symbol, or CALLS edge in
		// the indexed graph — including it here would manufacture a
		// permanent, unfixable recall gap (main -> TestMain) on every
		// project with tests.
		return true
	}
	return false
}

// enclosingNamedFunc walks Parent() until it reaches a function with no
// parent (i.e. not a closure literal) and returns that outermost named
// function. For a non-closure fn, returns fn itself unchanged.
//
// This aligns the SSA call-graph model with scip-go's: scip-go's CALLS
// builder attributes a call made inside a closure literal (e.g. the
// function passed to sort.Slice or client.ExecuteRead) to the lexically
// enclosing named function, because it works off source containment
// ranges, not a separate SSA node per closure. Without this fold, every
// call originating inside a closure body is invisible under its enclosing
// function's identity in the must/may sets — even though it is a perfectly
// real static/may edge — producing false precision suspects on the graph
// side and false recall gaps on the must side alike.
func enclosingNamedFunc(fn *ssa.Function) *ssa.Function {
	for fn != nil && fn.Parent() != nil {
		fn = fn.Parent()
	}
	return fn
}
