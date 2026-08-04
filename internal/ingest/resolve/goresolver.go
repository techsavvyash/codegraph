// Package resolve implements RFC-001 Layer 3: an in-repo Go structural type
// resolver that fills the gap left by scip-go's sparse is_implementation
// emission. It loads a Go module with go/packages + go/types (the
// authoritative Go type checker), enumerates every named interface and
// candidate concrete type defined in the project, and reports which
// candidates structurally satisfy which interfaces — independent of and in
// addition to whatever scip-go itself already emitted.
//
// The resolver never talks to SCIP symbol strings directly; it works in
// go/types space (package path + type name + method name) and hands off
// normalized descriptors. Joining those descriptors to the SCIP symbol
// strings actually present in a given index.scip is the job of
// ResolveImplementations / the symbol index in symbolindex.go — the two are
// split because the type-checking pass has no dependency on SCIP at all and
// is independently testable.
package resolve

import (
	"fmt"
	"go/types"

	"golang.org/x/tools/go/packages"
)

// packagesLoadMode is what we need from go/packages to run the type checker
// over the whole project and inspect method sets. NeedDeps+NeedImports are
// required for types.Implements to see through to imported interfaces used
// as embedded fields; NeedSyntax is not required since we don't inspect the
// AST, only *types.Package.
const packagesLoadMode = packages.NeedName |
	packages.NeedTypes |
	packages.NeedTypesInfo |
	packages.NeedImports |
	packages.NeedDeps |
	packages.NeedModule

// maxInterfaceCandidatePairs bounds the O(interfaces × candidates) structural
// check. Both sets are intra-project only (see loadTypeInfo), so in practice
// this is generous headroom; it exists purely as a defensive circuit
// breaker against pathological inputs so the resolver degrades to "skip and
// warn" instead of hanging the indexer.
const maxInterfaceCandidatePairs = 5_000_000

// TypeRelationship is the resolver's language-agnostic (go/types-space)
// output: a single named candidate type or method that structurally
// satisfies a named interface type or method. Symbol-string translation
// happens downstream in ResolveImplementations.
type TypeRelationship struct {
	// Candidate side (the "implements" / from side).
	CandidatePkgPath string // e.g. "example.com/simplemod/b"
	CandidateType    string // e.g. "FileStore"
	CandidateMethod  string // "" for a type-level relationship

	// Interface side (the "implemented" / to side).
	InterfacePkgPath string
	InterfaceType    string
	InterfaceMethod  string // "" for a type-level relationship

	// ViaPointer records whether satisfaction required the pointer type
	// (*CandidateType) rather than the value type. Informational only; it
	// does not affect symbol-string joining since Go SCIP symbols do not
	// distinguish value vs pointer receivers in the type descriptor.
	ViaPointer bool

	// Promoted is true when the candidate's method was found via struct
	// embedding (types.LookupFieldOrMethod returned a non-empty index with
	// depth > 0), i.e. it is a promoted method rather than one declared
	// directly on the candidate type.
	Promoted bool
}

// TypeResolveStats reports counts from the go/types structural-satisfaction
// pass, before any SCIP symbol join.
type TypeResolveStats struct {
	PackagesLoaded           int
	PackagesWithErrors       int
	InterfacesConsidered     int
	CandidatesConsidered     int
	PairsChecked             int
	TypeLevelRelationships   int
	MethodLevelRelationships int
	// GenericsSkipped counts named types/interfaces skipped because they are
	// (or belong to) an uninstantiated generic declaration — see the
	// "Generics" note on resolveTypeInfo.
	GenericsSkipped int
	// CapExceeded is true if InterfacesConsidered * CandidatesConsidered
	// exceeded maxInterfaceCandidatePairs and the resolver skipped the pass.
	CapExceeded bool
}

// loadTypeInfo runs go/packages over the project and returns, per in-project
// package, its *types.Package. Only packages whose PkgPath is "in-project"
// (i.e. present in the loaded set, as opposed to pulled in purely as an
// external dependency for type-checking purposes) are returned in `project`;
// packages.Load with pattern "./..." only ever returns the project's own
// packages at the top level (imported std/vendor packages are reachable via
// pkg.Imports / pkg.Types.Imports() but are not part of the returned slice),
// so this is a direct pass-through, not a filter.
func loadTypeInfo(projectRoot string) ([]*packages.Package, TypeResolveStats, error) {
	cfg := &packages.Config{
		Mode: packagesLoadMode,
		Dir:  projectRoot,
		Env:  cleanGoEnv(),
	}

	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		return nil, TypeResolveStats{}, fmt.Errorf("packages.Load: %w", err)
	}
	if len(pkgs) == 0 {
		return nil, TypeResolveStats{}, fmt.Errorf("packages.Load returned no packages (not a Go module? missing go.mod under %s)", projectRoot)
	}

	stats := TypeResolveStats{PackagesLoaded: len(pkgs)}
	var loadErrs []error
	for _, p := range pkgs {
		if len(p.Errors) > 0 {
			stats.PackagesWithErrors++
			for _, e := range p.Errors {
				loadErrs = append(loadErrs, e)
			}
		}
	}

	// packages.Load reports most failures (missing go.mod, unresolvable
	// module, syntax errors) as package-level Errors rather than a non-nil
	// `err` return above — e.g. pointing Dir at a directory with no go.mod
	// yields a single synthetic package whose Errors describe the problem,
	// not a top-level Load() error. If every loaded package failed, there is
	// nothing usable to type-check, so treat that the same as a Load()
	// error for the purposes of the caller's best-effort WARN-and-continue
	// handling.
	if stats.PackagesWithErrors == len(pkgs) {
		return nil, stats, fmt.Errorf("packages.Load: all %d package(s) failed to load, first error: %w", len(pkgs), loadErrs[0])
	}

	return pkgs, stats, nil
}

// cleanGoEnv returns the environment go/packages should use to invoke the
// `go` toolchain. We start from the process environment (so PATH etc. are
// preserved) but strip GOFLAGS/GOWORK so an ambient monorepo workspace file
// or -mod=readonly flag set for the outer codegraph module cannot leak into
// (and break) loading of an unrelated fixture/target module.
func cleanGoEnv() []string {
	base := processEnviron()
	out := make([]string, 0, len(base)+2)
	for _, kv := range base {
		if hasEnvPrefix(kv, "GOFLAGS=") || hasEnvPrefix(kv, "GOWORK=") {
			continue
		}
		out = append(out, kv)
	}
	out = append(out, "GOFLAGS=", "GOWORK=off")
	return out
}

func hasEnvPrefix(kv, prefix string) bool {
	return len(kv) >= len(prefix) && kv[:len(prefix)] == prefix
}

// namedTypeInfo bundles a *types.Named with the package it was found in, so
// downstream code can report package paths without repeatedly recovering
// them from the Named itself (which requires Obj().Pkg(), and is nil for
// universe-scope types like `error`).
type namedTypeInfo struct {
	pkgPath string
	name    string
	named   *types.Named
}

// collectNamedTypes walks every in-project package's package-level scope and
// splits named types into interfaces (with >=1 method — empty interfaces are
// universally satisfied and structurally meaningless to record) and
// candidates (types with a non-empty method set on T or *T).
//
// Generics: a generic declaration (type Box[T any] struct{...}) has a
// non-nil TypeParams() on the *types.Named for its *generic* form. We skip
// those — both as interfaces and as candidates — because types.Implements
// requires fully-instantiated types and there is no single canonical
// instantiation to check structural satisfaction against; recording a
// relationship for the uninstantiated generic would be misleading (it may
// hold for some instantiations and not others). This is a counted, logged
// skip (GenericsSkipped), not a silent drop. Already-instantiated generic
// *uses* (e.g. a field of type Box[int]) are unaffected: Box[int] is not a
// package-scope named type, it's a type expression, so it never reaches this
// enumeration in the first place — meaning instantiated generics simply
// never participate in resolver-driven relationships at all, by construction
// of only walking package scopes.
func collectNamedTypes(pkgs []*packages.Package, stats *TypeResolveStats) (interfaces []namedTypeInfo, candidates []namedTypeInfo) {
	seen := make(map[*types.Package]bool)

	for _, p := range pkgs {
		if p.Types == nil || seen[p.Types] {
			continue
		}
		seen[p.Types] = true

		scope := p.Types.Scope()
		for _, name := range scope.Names() {
			obj := scope.Lookup(name)
			tn, ok := obj.(*types.TypeName)
			if !ok || tn.IsAlias() {
				continue
			}
			named, ok := tn.Type().(*types.Named)
			if !ok {
				continue
			}
			if named.TypeParams().Len() > 0 {
				stats.GenericsSkipped++
				continue
			}

			info := namedTypeInfo{pkgPath: p.Types.Path(), name: name, named: named}

			if iface, ok := named.Underlying().(*types.Interface); ok {
				if iface.NumMethods() == 0 {
					continue // empty interface: universally satisfied, not meaningful
				}
				interfaces = append(interfaces, info)
				continue
			}

			// Candidate: has a method set on T or *T. Struct/defined types
			// with zero methods can never implement a non-empty interface,
			// so skip them up front to shrink the O(I×C) product.
			if types.NewMethodSet(named).Len() > 0 || types.NewMethodSet(types.NewPointer(named)).Len() > 0 {
				candidates = append(candidates, info)
			}
		}
	}

	return interfaces, candidates
}

// ResolveGoTypes runs the full go/types structural-satisfaction pass over
// the project at projectRoot and returns every (candidate, interface) pair
// — at both type level and method level — where the candidate structurally
// implements the interface. It does not touch SCIP or Neo4j; see
// ResolveImplementations for the SCIP-symbol-joined entry point actually
// used by the indexer.
func ResolveGoTypes(projectRoot string) ([]TypeRelationship, TypeResolveStats, error) {
	pkgs, stats, err := loadTypeInfo(projectRoot)
	if err != nil {
		return nil, stats, err
	}

	interfaces, candidates := collectNamedTypes(pkgs, &stats)
	stats.InterfacesConsidered = len(interfaces)
	stats.CandidatesConsidered = len(candidates)

	pairCount := len(interfaces) * len(candidates)
	if pairCount > maxInterfaceCandidatePairs {
		stats.CapExceeded = true
		return nil, stats, fmt.Errorf("resolver skipped: %d interfaces x %d candidates = %d pairs exceeds cap of %d",
			len(interfaces), len(candidates), pairCount, maxInterfaceCandidatePairs)
	}

	var out []TypeRelationship
	for _, iface := range interfaces {
		ifaceUnderlying := iface.named.Underlying().(*types.Interface)

		for _, cand := range candidates {
			stats.PairsChecked++

			implT := types.Implements(cand.named, ifaceUnderlying)
			implPtr := types.Implements(types.NewPointer(cand.named), ifaceUnderlying)
			if !implT && !implPtr {
				continue
			}
			viaPointer := !implT && implPtr

			out = append(out, TypeRelationship{
				CandidatePkgPath: cand.pkgPath,
				CandidateType:    cand.name,
				InterfacePkgPath: iface.pkgPath,
				InterfaceType:    iface.name,
				ViaPointer:       viaPointer,
			})
			stats.TypeLevelRelationships++

			for i := 0; i < ifaceUnderlying.NumMethods(); i++ {
				ifaceMethod := ifaceUnderlying.Method(i)

				recv := types.Type(cand.named)
				addressable := viaPointer
				if viaPointer {
					recv = types.NewPointer(cand.named)
				}
				obj, index, _ := types.LookupFieldOrMethod(recv, addressable, ifaceMethod.Pkg(), ifaceMethod.Name())
				candMethod, ok := obj.(*types.Func)
				if !ok {
					// Should not happen: types.Implements already verified
					// the method exists on this receiver shape.
					continue
				}

				out = append(out, TypeRelationship{
					CandidatePkgPath: cand.pkgPath,
					CandidateType:    cand.name,
					CandidateMethod:  candMethod.Name(),
					InterfacePkgPath: iface.pkgPath,
					InterfaceType:    iface.name,
					InterfaceMethod:  ifaceMethod.Name(),
					ViaPointer:       viaPointer,
					Promoted:         len(index) > 1,
				})
				stats.MethodLevelRelationships++
			}
		}
	}

	return out, stats, nil
}
