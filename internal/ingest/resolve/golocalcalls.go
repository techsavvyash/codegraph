package resolve

// golocalcalls.go implements a Go dispatch-edge resolver for a class of call
// sites scip-go cannot express: method calls (and method values) made through
// an interface whose type has NO package-scope graph node — function-local
// named interfaces, anonymous interface literals, and generic type parameters
// with anonymous constraint literals.
//
// Why the existing machinery misses these (verified against a raw index.scip):
//
//   scip-go assigns function-local interface types LOCAL symbols (`local N`).
//   The indexer creates no Reference node for `local N` symbols, so the call
//   site through such an interface has ZERO outgoing CALLS edges in the graph
//   — its callee vanishes and it reads as dead code. And scip-go emits NO
//   is_implementation relationship for anonymous/local-interface satisfaction
//   (only the *named* package-scope case gets one), so the IMPLEMENTS
//   fan-out that would otherwise rewrite an abstract-method reference to its
//   concrete implementers never has an edge to fan out from.
//
// This pass closes the gap by walking the type-checked AST, finding every
// method selection whose receiver is one of these graph-invisible interfaces,
// and synthesizing a DIRECT dispatch edge from the enclosing function to each
// package-scope concrete type that structurally satisfies the interface. It
// works entirely in go/types space (package path + type name + method name);
// joining those identities to real SCIP symbol strings is JoinLocalCalls'
// job, mirroring the ResolveGoTypes / ResolveImplementations split.
//
// Deliberately OUT of scope here (covered elsewhere, and skipped-with-a-count
// so the boundary is observable, not silent):
//   - package-scope named interfaces — the IMPLEMENTS machinery + CALLS
//     fan-out already resolve dispatch through these.
//   - module-scope (package-level var initializer) call sites — File-CALLS
//     territory owned by the call-graph builder.
//   - concrete-method selections — scip-go emits a normal reference the
//     call-graph builder turns into a CALLS edge.

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

// localCallsLoadMode adds NeedSyntax to the base resolver load mode: unlike
// ResolveGoTypes (which only inspects *types.Package method sets), this pass
// walks the AST to find selector expressions and their enclosing functions,
// so it needs the syntax trees and the TypesInfo that decorates them.
const localCallsLoadMode = packagesLoadMode | packages.NeedSyntax

// LocalCallKind distinguishes a genuine invocation from a method value taken
// through an interface. Values are dispatch-preserving (the callee can be
// invoked later through the stored value) but are not calls, mirroring the
// call-graph builder's CALLS vs USES_VALUE split.
type LocalCallKind string

const (
	LocalCallInvoke LocalCallKind = "CALLS"
	LocalCallValue  LocalCallKind = "USES_VALUE"
)

// localCallSite is one graph-invisible interface method selection found in the
// AST, in go/types space, before candidate matching. Caller identity is the
// nearest enclosing *ast.FuncDecl (closures fold up to it, matching scip-go's
// containment attribution).
type localCallSite struct {
	callerPkgPath string
	callerType    string // "" for a free function
	callerName    string
	iface         *types.Interface
	method        string        // interface method name selected
	kind          LocalCallKind // CALLS if the selector is a CallExpr.Fun, else USES_VALUE
	line          int
}

// LocalCallRelation is a resolved dispatch edge in go/types space: the
// enclosing caller invokes-or-references a concrete method that structurally
// satisfies the graph-invisible interface at the call site. FromSymbol/
// ToSymbol are populated only after JoinLocalCalls maps these identities onto
// real SCIP symbol strings.
type LocalCallRelation struct {
	// Caller identity (go/types space).
	CallerPkgPath string
	CallerType    string // "" for a free function
	CallerName    string

	// Callee identity (go/types space). CalleeType is the DECLARING type of
	// the method — for a promoted/embedded method that is the embedded type,
	// not the candidate, because that is what the scip-go symbol is named
	// after.
	CalleePkgPath string
	CalleeType    string
	CalleeName    string

	Kind LocalCallKind
	Line int // first line seen for this (caller, callee, kind), for provenance

	// FromSymbol/ToSymbol are the joined SCIP symbol strings, set by
	// JoinLocalCalls. Empty in the raw ResolveLocalInterfaceCalls output.
	FromSymbol string
	ToSymbol   string
}

// LocalCallStats mirrors TypeResolveStats' conventions for the local-interface
// dispatch pass.
type LocalCallStats struct {
	PackagesLoaded     int
	PackagesWithErrors int
	// SitesSeen is every method selection through an interface receiver the
	// walk examined (before the package-named / module-scope skips).
	SitesSeen int
	// PackageNamedSkipped counts selections through a package-scope named
	// interface (or a type param constrained by one) — handled by the
	// IMPLEMENTS machinery, deliberately not our job.
	PackageNamedSkipped int
	// ModuleScopeSkipped counts graph-invisible-interface selections with no
	// enclosing FuncDecl (package-level var initializers) — File-CALLS
	// territory, out of scope.
	ModuleScopeSkipped int
	// HandledSites counts selections we kept (graph-invisible interface, with
	// an enclosing FuncDecl) and ran candidate matching for.
	HandledSites int
	// CandidatesConsidered is the number of package-scope named candidate
	// types enumerated (module packages only).
	CandidatesConsidered int
	// PairsChecked is the number of (site, candidate) structural checks run.
	PairsChecked int
	// RelationsFound is the number of deduped (caller, callee, kind) relations
	// produced (before any SCIP symbol join).
	RelationsFound int
	// CapExceeded is true if HandledSites * CandidatesConsidered would exceed
	// maxInterfaceCandidatePairs; the pass then skips candidate matching.
	CapExceeded bool
}

// ResolveLocalInterfaceCalls runs the full local/anonymous-interface dispatch
// pass over the project at projectRoot and returns every deduped (caller,
// callee, kind) dispatch relation, in go/types space. It does not touch SCIP
// or Neo4j — see JoinLocalCalls for the symbol-joined output the indexer
// consumes, and ResolveLocalInterfaceCallsJoined for the one-call convenience
// wrapper.
//
// knownSymbols is accepted for signature symmetry with ResolveImplementations
// and to let the caller run detection + join in one step via the *Joined
// wrapper; ResolveLocalInterfaceCalls itself ignores it (detection is pure
// go/types). Best-effort by design: packages.Load failures return an error
// for the caller to WARN-and-continue on, never a panic.
func ResolveLocalInterfaceCalls(projectRoot string, knownSymbols []string) ([]LocalCallRelation, LocalCallStats, error) {
	_ = knownSymbols // detection is SCIP-independent; join happens in JoinLocalCalls.

	cfg := &packages.Config{
		Mode: localCallsLoadMode,
		Dir:  projectRoot,
		Env:  cleanGoEnv(),
	}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		return nil, LocalCallStats{}, fmt.Errorf("packages.Load: %w", err)
	}
	if len(pkgs) == 0 {
		return nil, LocalCallStats{}, fmt.Errorf("packages.Load returned no packages (not a Go module? missing go.mod under %s)", projectRoot)
	}

	stats := LocalCallStats{PackagesLoaded: len(pkgs)}
	allFailed := true
	var firstErr error
	for _, p := range pkgs {
		if len(p.Errors) > 0 {
			stats.PackagesWithErrors++
			if firstErr == nil {
				firstErr = p.Errors[0]
			}
		} else {
			allFailed = false
		}
	}
	if allFailed {
		return nil, stats, fmt.Errorf("packages.Load: all %d package(s) failed to load, first error: %w", len(pkgs), firstErr)
	}

	// Enumerate package-scope named candidate types (module packages only) —
	// same source set collectNamedTypes uses, so the two passes agree on what
	// "a candidate that can be named by a scip-go symbol" means.
	_, candidateInfos := collectNamedTypesForLocalCalls(pkgs)
	stats.CandidatesConsidered = len(candidateInfos)

	sites := collectLocalInterfaceSites(pkgs, &stats)

	if stats.HandledSites > 0 && len(candidateInfos) > 0 {
		if stats.HandledSites*len(candidateInfos) > maxInterfaceCandidatePairs {
			stats.CapExceeded = true
			return nil, stats, fmt.Errorf("local-interface resolver skipped: %d sites x %d candidates exceeds cap of %d",
				stats.HandledSites, len(candidateInfos), maxInterfaceCandidatePairs)
		}
	}

	rels := matchSitesToCandidates(sites, candidateInfos, &stats)
	stats.RelationsFound = len(rels)
	return rels, stats, nil
}

// collectNamedTypesForLocalCalls enumerates package-scope named types in the
// module's own packages, split into (unused-here) interfaces and candidate
// types with a non-empty method set on T or *T. It reuses collectNamedTypes'
// exact selection rules via a throwaway stats sink so the two passes never
// diverge on what counts as a candidate.
func collectNamedTypesForLocalCalls(pkgs []*packages.Package) (interfaces, candidates []namedTypeInfo) {
	var sink TypeResolveStats
	return collectNamedTypes(pkgs, &sink)
}

// collectLocalInterfaceSites walks every loaded package's syntax and returns
// one localCallSite per method selection whose receiver is a graph-invisible
// interface (function-local named, anonymous literal, or a type param with an
// anonymous constraint), attributed to the nearest enclosing FuncDecl.
func collectLocalInterfaceSites(pkgs []*packages.Package, stats *LocalCallStats) []localCallSite {
	var sites []localCallSite
	seen := make(map[*types.Package]bool)

	for _, p := range pkgs {
		if p.Types == nil || p.TypesInfo == nil || seen[p.Types] {
			continue
		}
		seen[p.Types] = true
		pkgPath := p.Types.Path()

		for _, file := range p.Syntax {
			w := &siteWalker{
				pkgPath: pkgPath,
				info:    p.TypesInfo,
				fset:    p.Fset,
				stats:   stats,
			}
			// Pre-mark selector nodes that are the Fun of a CallExpr, so kind
			// classification is structural, not a guess.
			w.markCallFuns(file)
			sites = append(sites, w.walkFile(file)...)
		}
	}
	return sites
}

// siteWalker carries per-package state for the selector walk.
type siteWalker struct {
	pkgPath  string
	info     *types.Info
	fset     *token.FileSet
	stats    *LocalCallStats
	callFuns map[*ast.SelectorExpr]bool
}

// markCallFuns records every SelectorExpr that appears directly as a
// CallExpr.Fun, so a selection can be classified as an invocation (CALLS)
// versus a method value (USES_VALUE) structurally.
func (w *siteWalker) markCallFuns(file *ast.File) {
	w.callFuns = make(map[*ast.SelectorExpr]bool)
	ast.Inspect(file, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
				w.callFuns[sel] = true
			}
		}
		return true
	})
}

// walkFile descends into every top-level FuncDecl and collects the
// graph-invisible interface method selections inside it (folding closures up
// to the FuncDecl for caller attribution — ast.Inspect over the whole body
// includes closure bodies, and the caller identity is fixed to the FuncDecl).
// Selections at package level (var initializers, no enclosing FuncDecl) are
// counted as module-scope skips rather than silently dropped.
func (w *siteWalker) walkFile(file *ast.File) []localCallSite {
	var sites []localCallSite

	for _, decl := range file.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if fd.Body == nil {
			continue
		}
		caller, ok := w.funcDeclIdentity(fd)
		if !ok {
			continue
		}
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if site, ok := w.classifySelection(sel, caller); ok {
				sites = append(sites, site)
			}
			return true
		})
	}

	// Package-level selections outside any FuncDecl (var x = iface.M()) are
	// module-scope call sites: count them so the boundary is observable, but
	// leave attribution to the File-CALLS path in the call-graph builder.
	w.countModuleScopeSites(file)

	return sites
}

// countModuleScopeSites walks selections that live in top-level GenDecls
// (var/const initializers, outside any FuncDecl body) and, for those going
// through a graph-invisible interface, increments SitesSeen + ModuleScopeSkipped.
// It intentionally does not emit relations — module scope is File-CALLS
// territory.
func (w *siteWalker) countModuleScopeSites(file *ast.File) {
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		ast.Inspect(gd, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if iface, invisible := w.receiverInterface(sel); iface != nil && invisible {
				w.stats.SitesSeen++
				w.stats.ModuleScopeSkipped++
			}
			return true
		})
	}
}

// funcDeclIdentity returns the (callerType, callerName) identity for a
// FuncDecl. callerType is "" for a free function, or the receiver's declared
// named type (pointer/generic unwrapped) for a method. ok is false for a
// FuncDecl the resolver cannot name stably.
func (w *siteWalker) funcDeclIdentity(fd *ast.FuncDecl) (localCallSite, bool) {
	if fd.Name == nil || fd.Name.Name == "" {
		return localCallSite{}, false
	}
	id := localCallSite{callerPkgPath: w.pkgPath, callerName: fd.Name.Name}
	if fd.Recv != nil && len(fd.Recv.List) > 0 {
		recvType := recvTypeName(fd.Recv.List[0].Type)
		if recvType == "" {
			return localCallSite{}, false
		}
		id.callerType = recvType
	}
	return id, true
}

// recvTypeName extracts the named type name from a method receiver AST
// expression, unwrapping a leading pointer and any generic instantiation
// brackets (func (r *Foo[T]) -> "Foo").
func recvTypeName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.StarExpr:
		return recvTypeName(e.X)
	case *ast.IndexExpr: // Foo[T]
		return recvTypeName(e.X)
	case *ast.IndexListExpr: // Foo[T, U]
		return recvTypeName(e.X)
	case *ast.Ident:
		return e.Name
	}
	return ""
}

// classifySelection inspects one SelectorExpr and, if it is a method
// selection through a graph-invisible interface, returns the populated
// localCallSite (with caller identity copied from the enclosing FuncDecl).
// Package-scope named interfaces are counted-and-skipped. Every method
// selection through an interface is counted in SitesSeen.
func (w *siteWalker) classifySelection(sel *ast.SelectorExpr, caller localCallSite) (localCallSite, bool) {
	iface, invisible := w.receiverInterface(sel)
	if iface == nil {
		return localCallSite{}, false // not an interface method selection at all
	}
	w.stats.SitesSeen++
	if !invisible {
		w.stats.PackageNamedSkipped++
		return localCallSite{}, false
	}
	w.stats.HandledSites++

	site := caller // copies callerPkgPath/callerType/callerName
	site.iface = iface
	site.method = sel.Sel.Name
	site.line = w.fset.Position(sel.Sel.Pos()).Line
	if w.callFuns[sel] {
		site.kind = LocalCallInvoke
	} else {
		site.kind = LocalCallValue
	}
	return site, true
}

// receiverInterface returns the interface a method selection dispatches
// through, and whether that interface is graph-invisible (function-local
// named, anonymous literal, or a type param with an anonymous constraint).
//
// It returns (nil, false) when the selection is not a method selection on an
// interface receiver at all (concrete-method selections, field selections,
// package-qualified identifiers) — those are scip-go's job, not ours.
//
// A selection through a PACKAGE-SCOPE named interface returns (iface, false):
// it is an interface selection, but visible, so the caller counts it as a
// package-named skip. This distinction is what keeps the IMPLEMENTS
// machinery's territory off-limits while still handling everything else.
func (w *siteWalker) receiverInterface(sel *ast.SelectorExpr) (*types.Interface, bool) {
	selection := w.info.Selections[sel]
	if selection == nil || selection.Kind() != types.MethodVal {
		return nil, false
	}
	recv := selection.Recv()
	if recv == nil {
		return nil, false
	}

	// Type parameter receiver: dispatch is through the constraint's interface.
	if tp, ok := recv.(*types.TypeParam); ok {
		constraint := tp.Constraint()
		if constraint == nil {
			return nil, false
		}
		iface, ok := constraint.Underlying().(*types.Interface)
		if !ok {
			return nil, false
		}
		// A type param constrained by a package-scope NAMED interface is
		// covered by IMPLEMENTS; only an anonymous constraint literal is ours.
		if isPackageScopeNamedInterface(constraint) {
			return iface, false
		}
		return iface, true
	}

	// Non-type-param receiver: only interface-typed receivers are ours;
	// concrete-method selections are scip's job.
	if named, ok := recv.(*types.Named); ok {
		iface, ok := named.Underlying().(*types.Interface)
		if !ok {
			return nil, false // concrete named type: not our concern
		}
		if isPackageScopeNamedInterface(named) {
			return iface, false // package-scope named interface: IMPLEMENTS territory
		}
		// Function-local named interface (`type X interface{...}` in a body):
		// a *types.Named whose object's parent is NOT the package scope.
		return iface, true
	}

	// Anonymous interface literal receiver (x.(interface{ M() }), a param of
	// anonymous interface type, a type-switch anonymous-interface case).
	if iface, ok := recv.Underlying().(*types.Interface); ok {
		return iface, true
	}

	return nil, false
}

// isPackageScopeNamedInterface reports whether t is a *types.Named whose
// TypeName is declared directly in its package's top-level scope — i.e. a
// package-scope named interface, the case the IMPLEMENTS machinery owns.
// Function-local named interfaces are *types.Named too, but their object's
// parent scope is a function/block scope, not the package scope.
func isPackageScopeNamedInterface(t types.Type) bool {
	named, ok := t.(*types.Named)
	if !ok {
		return false
	}
	obj := named.Obj()
	if obj == nil || obj.Pkg() == nil {
		return false
	}
	return obj.Parent() == obj.Pkg().Scope()
}

// dedupeKey identifies a unique (caller, callee, kind) relation for
// deduplication. Two selections in the same caller to the same concrete method
// with the same kind collapse to one edge; the first line seen is kept.
type dedupeKey struct {
	callerPkg, callerType, callerName string
	calleePkg, calleeType, calleeName string
	kind                              LocalCallKind
}

// matchSitesToCandidates cross-checks each collected call site against every
// package-scope candidate type; for each candidate that structurally
// satisfies the site's interface it resolves the concrete method (via
// LookupFieldOrMethod, taking the DECLARING receiver for promoted methods)
// and emits a deduped LocalCallRelation. Output is sorted for deterministic
// results.
func matchSitesToCandidates(sites []localCallSite, candidates []namedTypeInfo, stats *LocalCallStats) []LocalCallRelation {
	byKey := make(map[dedupeKey]LocalCallRelation)

	for _, site := range sites {
		for _, cand := range candidates {
			stats.PairsChecked++

			implT := types.Implements(cand.named, site.iface)
			implPtr := types.Implements(types.NewPointer(cand.named), site.iface)
			if !implT && !implPtr {
				continue
			}

			recv := types.Type(cand.named)
			addressable := false
			if !implT && implPtr {
				recv = types.NewPointer(cand.named)
				addressable = true
			}

			obj, _, _ := types.LookupFieldOrMethod(recv, addressable, methodPkg(site.iface, site.method), site.method)
			fn, ok := obj.(*types.Func)
			if !ok {
				// types.Implements verified the method exists on this
				// receiver shape, so this should not happen; skip defensively.
				continue
			}

			calleePkg, calleeType := declaringMethodOwner(fn)
			if calleeType == "" {
				continue // method with no named receiver owner: unnameable
			}

			key := dedupeKey{
				callerPkg: site.callerPkgPath, callerType: site.callerType, callerName: site.callerName,
				calleePkg: calleePkg, calleeType: calleeType, calleeName: fn.Name(),
				kind: site.kind,
			}
			if _, exists := byKey[key]; exists {
				continue // keep first line seen
			}
			byKey[key] = LocalCallRelation{
				CallerPkgPath: site.callerPkgPath,
				CallerType:    site.callerType,
				CallerName:    site.callerName,
				CalleePkgPath: calleePkg,
				CalleeType:    calleeType,
				CalleeName:    fn.Name(),
				Kind:          site.kind,
				Line:          site.line,
			}
		}
	}

	out := make([]LocalCallRelation, 0, len(byKey))
	for _, r := range byKey {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		return localCallLess(out[i], out[j])
	})
	return out
}

// methodPkg returns the package the interface method belongs to, needed by
// types.LookupFieldOrMethod to disambiguate unexported method names. Exported
// methods are package-agnostic, but passing the interface method's own package
// is always correct.
func methodPkg(iface *types.Interface, name string) *types.Package {
	for m := range iface.Methods() {
		if m.Name() == name {
			return m.Pkg()
		}
	}
	return nil
}

// declaringMethodOwner returns the (pkgPath, typeName) of the type that
// actually DECLARES fn — i.e. fn's receiver's named type, pointer unwrapped.
// For a promoted/embedded method LookupFieldOrMethod returns the *types.Func
// of the embedded type's method, whose receiver is that embedded type, so this
// naturally yields the declaring type (matching the scip-go symbol name) rather
// than the outer candidate. Returns ("", "") if fn has no nameable named
// receiver.
func declaringMethodOwner(fn *types.Func) (pkgPath, typeName string) {
	sig, ok := fn.Type().(*types.Signature)
	if !ok || sig.Recv() == nil {
		return "", ""
	}
	named := namedFromRecvType(sig.Recv().Type())
	if named == nil || named.Obj() == nil || named.Obj().Pkg() == nil {
		return "", ""
	}
	return named.Obj().Pkg().Path(), named.Obj().Name()
}

// namedFromRecvType unwraps a receiver type to its *types.Named, stripping a
// leading pointer. Generic receivers arrive as *types.Named already (their
// Obj carries the base type name), so no instantiation unwrapping is needed
// for the name.
func namedFromRecvType(t types.Type) *types.Named {
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}
	named, _ := t.(*types.Named)
	return named
}

// LocalCallJoinStats reports the outcome of joining go/types-space relations
// onto the SCIP symbol strings scip-go actually emitted.
type LocalCallJoinStats struct {
	// Emitted counts relations where BOTH endpoints resolved to a known SCIP
	// symbol and were therefore emitted with FromSymbol/ToSymbol populated.
	Emitted int
	// DroppedMissingSymbol counts relations dropped because at least one
	// endpoint (caller or callee) did not resolve to any symbol in
	// knownSymbols — an edge to a non-existent graph node cannot be created.
	DroppedMissingSymbol int
}

// JoinLocalCalls maps each relation's caller/callee go/types identity onto the
// exact SCIP symbol string scip-go emitted for it, using knownSymbols. Callers
// and callees may be free functions (“ `pkg`/Name(). “, no '#') as well as
// methods (“ `pkg`/Type#Method(). “); both shapes are indexed. Only relations
// whose BOTH endpoints resolve are returned (with FromSymbol/ToSymbol set);
// the rest are counted in DroppedMissingSymbol, mirroring
// ResolveImplementations' join contract.
func JoinLocalCalls(rels []LocalCallRelation, knownSymbols []string) ([]LocalCallRelation, LocalCallJoinStats) {
	idx := buildCallableSymbolIndex(knownSymbols)

	var stats LocalCallJoinStats
	out := make([]LocalCallRelation, 0, len(rels))
	for _, r := range rels {
		from, ok := idx.lookup(r.CallerPkgPath, r.CallerType, r.CallerName)
		if !ok {
			stats.DroppedMissingSymbol++
			continue
		}
		to, ok := idx.lookup(r.CalleePkgPath, r.CalleeType, r.CalleeName)
		if !ok {
			stats.DroppedMissingSymbol++
			continue
		}
		r.FromSymbol = from
		r.ToSymbol = to
		out = append(out, r)
		stats.Emitted++
	}
	return out, stats
}

// callableSymbolIndex maps a callable's (pkg, type, name) identity to the SCIP
// symbol string it came from. Unlike symbolLookup (which is IMPLEMENTS-only
// and therefore type/method-bound), this also indexes free functions, since a
// local-interface dispatch caller or callee can be a package-level function.
type callableSymbolIndex struct {
	byIdent map[callableIdent]string
}

type callableIdent struct {
	pkgPath string
	typ     string // "" for a free function
	name    string
}

func (idx *callableSymbolIndex) lookup(pkgPath, typ, name string) (string, bool) {
	s, ok := idx.byIdent[callableIdent{pkgPath: pkgPath, typ: typ, name: name}]
	return s, ok
}

// buildCallableSymbolIndex parses every known symbol into either a type-method
// descriptor (via the shared parseGoSymbolDescriptor) or a free-function
// descriptor (via parseFreeFunctionCallable, mirrored from
// internal/verify/oracle/gograph.go's parseFreeFunctionSymbol — see its doc)
// and indexes the callable ones by identity.
func buildCallableSymbolIndex(knownSymbols []string) *callableSymbolIndex {
	idx := &callableSymbolIndex{byIdent: make(map[callableIdent]string, len(knownSymbols))}
	for _, sym := range knownSymbols {
		if pkgPath, typ, method, ok := parseGoSymbolDescriptor(sym); ok && method != "" {
			idx.byIdent[callableIdent{pkgPath: pkgPath, typ: typ, name: method}] = sym
			continue
		}
		if pkgPath, name, ok := parseFreeFunctionCallable(sym); ok {
			idx.byIdent[callableIdent{pkgPath: pkgPath, name: name}] = sym
		}
	}
	return idx
}

// parseFreeFunctionCallable parses the one Go symbol shape
// parseGoSymbolDescriptor deliberately rejects: a package-level function
// “ `<pkgPath>`/Name(). “ with no '#'. Duplicated here (rather than shared)
// exactly as internal/verify/oracle/gograph.go's parseFreeFunctionSymbol is —
// the shared parser's contract is scoped to type-bound IMPLEMENTS joins and
// must not be widened. Only the call-suffix form (`Name().`) is a callable;
// the bare-dot form (`Name.`, a package-level term) returns ok=false.
func parseFreeFunctionCallable(sym string) (pkgPath, name string, ok bool) {
	parts := strings.SplitN(sym, " ", 5)
	if len(parts) != 5 {
		return "", "", false
	}
	descriptor := parts[4]
	if !strings.HasPrefix(descriptor, "`") {
		return "", "", false
	}
	end := strings.Index(descriptor[1:], "`")
	if end < 0 {
		return "", "", false
	}
	pkgPath = descriptor[1 : 1+end]
	rest := strings.TrimPrefix(descriptor[1+end+1:], "/")
	if strings.Contains(rest, "#") || rest == "" {
		return "", "", false // type/method-bound or bare package symbol
	}
	if !strings.HasSuffix(rest, "().") {
		return "", "", false // not a call — e.g. a package-level term
	}
	name = strings.TrimSuffix(rest, "().")
	if name == "" {
		return "", "", false
	}
	return pkgPath, name, true
}

// ResolveLocalInterfaceCallsJoined runs detection and the SCIP symbol join in
// one call — the entry point the indexer uses. It returns the joined
// relations (FromSymbol/ToSymbol populated), the detection stats, the join
// stats, and any detection error (best-effort: caller WARNs and continues).
func ResolveLocalInterfaceCallsJoined(projectRoot string, knownSymbols []string) ([]LocalCallRelation, LocalCallStats, LocalCallJoinStats, error) {
	rels, stats, err := ResolveLocalInterfaceCalls(projectRoot, knownSymbols)
	if err != nil {
		return nil, stats, LocalCallJoinStats{}, err
	}
	joined, joinStats := JoinLocalCalls(rels, knownSymbols)
	return joined, stats, joinStats, nil
}

// localCallLess is the deterministic sort order for LocalCallRelation output.
func localCallLess(a, b LocalCallRelation) bool {
	if a.CallerPkgPath != b.CallerPkgPath {
		return a.CallerPkgPath < b.CallerPkgPath
	}
	if a.CallerType != b.CallerType {
		return a.CallerType < b.CallerType
	}
	if a.CallerName != b.CallerName {
		return a.CallerName < b.CallerName
	}
	if a.CalleePkgPath != b.CalleePkgPath {
		return a.CalleePkgPath < b.CalleePkgPath
	}
	if a.CalleeType != b.CalleeType {
		return a.CalleeType < b.CalleeType
	}
	if a.CalleeName != b.CalleeName {
		return a.CalleeName < b.CalleeName
	}
	return a.Kind < b.Kind
}
