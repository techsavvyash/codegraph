// Package localiface holds the call sites that dispatch through
// graph-invisible interfaces (function-local named, anonymous literal,
// anonymous generic constraint) plus a package-scope named interface that
// must be SKIPPED. The resolver should synthesize dispatch edges from these
// enclosing functions to the concrete methods in the impls package.
package localiface

import "example.com/localiface/impls"

// PkgIface is a PACKAGE-SCOPE named interface. Dispatch through it is the
// IMPLEMENTS machinery's job, so the resolver must skip it (counted in
// PackageNamedSkipped) and produce NO relation for CallThroughPkgIface below.
type PkgIface interface {
	Model() string
}

// anyModeler is only used to give the impls types something to be assigned to
// via an untyped nil check; not itself load-bearing.

// CallLocalNamedIface is the semlink pattern: a FUNCTION-LOCAL named interface
// + type assertion + call. Both ValModel and PtrModel satisfy it; NearMiss
// (Model() int) must not. Expected: CALLS to impls.ValModel#Model and
// impls.PtrModel#Model.
func CallLocalNamedIface(v any) string {
	type modelNamer interface{ Model() string }
	if mn, ok := v.(modelNamer); ok {
		return mn.Model()
	}
	return ""
}

// CallAnonAssertion uses a BARE anonymous interface assertion + call.
// Expected: CALLS to impls.ValModel#Model and impls.PtrModel#Model.
func CallAnonAssertion(v any) string {
	if mn, ok := v.(interface{ Model() string }); ok {
		return mn.Model()
	}
	return ""
}

// CallTypeSwitchAnon uses a TYPE SWITCH with an anonymous-interface case +
// call in that branch. Expected: CALLS to impls.ValModel#Model and
// impls.PtrModel#Model.
func CallTypeSwitchAnon(v any) string {
	switch mn := v.(type) {
	case interface{ Model() string }:
		return mn.Model()
	default:
		return ""
	}
}

// MethodValueThroughLocalIface takes a METHOD VALUE through a local interface
// rather than calling it. Expected: USES_VALUE (not CALLS) to
// impls.ValModel#Model and impls.PtrModel#Model.
func MethodValueThroughLocalIface(v any) func() string {
	if mn, ok := v.(interface{ Model() string }); ok {
		f := mn.Model // method value, not a call
		return f
	}
	return nil
}

// CallGenericAnonConstraint dispatches through a generic type parameter whose
// constraint is an ANONYMOUS interface literal. Expected: CALLS to
// impls.Namer#Name (the only Name() string implementer).
func CallGenericAnonConstraint[T interface{ Name() string }](t T) string {
	return t.Name()
}

// CallGenericNamedConstraint dispatches through a generic type parameter whose
// constraint is a PACKAGE-SCOPE NAMED interface. Must be SKIPPED (counted in
// PackageNamedSkipped), producing NO relation.
func CallGenericNamedConstraint[T PkgIface](t T) string {
	return t.Model()
}

// CallThroughPkgIface dispatches through the package-scope named interface
// directly. Must be SKIPPED — IMPLEMENTS territory.
func CallThroughPkgIface(p PkgIface) string {
	return p.Model()
}

// CallInsideClosure makes the interface call INSIDE a closure. The edge must
// be attributed to the enclosing named function CallInsideClosure, not to the
// anonymous closure. Expected: CALLS to impls.ValModel#Model and
// impls.PtrModel#Model, from CallInsideClosure.
func CallInsideClosure(v any) func() string {
	return func() string {
		if mn, ok := v.(interface{ Model() string }); ok {
			return mn.Model()
		}
		return ""
	}
}

// CallPromotedMethod dispatches through a local interface satisfied by
// impls.Embedder via a PROMOTED method. The callee identity must name the
// DECLARING type (impls.base#Describe), not Embedder, because that is what the
// scip-go symbol is named after. Expected: CALLS to impls.base#Describe.
func CallPromotedMethod(v any) string {
	type describer interface{ Describe() string }
	if d, ok := v.(describer); ok {
		return d.Describe()
	}
	return ""
}

// consume references the impls types so they are load-bearing candidates the
// type checker keeps around (and so the module compiles). It never runs.
func consume() {
	_ = impls.ValModel{}
	_ = impls.PtrModel{}
	_ = impls.NearMiss{}
	_ = impls.Embedder{}
	_ = impls.Namer{}
}
