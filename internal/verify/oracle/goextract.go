package oracle

import (
	"fmt"
	"strings"

	"golang.org/x/tools/go/callgraph"
	"golang.org/x/tools/go/callgraph/cha"
	"golang.org/x/tools/go/callgraph/static"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

// packagesLoadMode mirrors internal/ingest/resolve's loadTypeInfo but adds
// the AST/syntax needs of SSA construction (NeedFiles, NeedCompiledGoFiles,
// NeedSyntax, NeedTypesInfo).
const packagesLoadMode = packages.NeedName |
	packages.NeedFiles |
	packages.NeedCompiledGoFiles |
	packages.NeedImports |
	packages.NeedDeps |
	packages.NeedTypes |
	packages.NeedSyntax |
	packages.NeedTypesInfo |
	packages.NeedModule

// goExtraction is the outcome of loading a project, building its SSA
// program, and computing the static (must) and CHA (may) call-graph edge
// sets, already filtered to intra-module edges with exclusions applied.
type goExtraction struct {
	ModulePath string
	MustEdges  map[edgeKey]bool
	MayEdges   map[edgeKey]bool

	// MainReachable is the set of in-module function identities reachable
	// from a binary's true entry points (package `main` and every init) over
	// the RAW, unfolded CHA call graph, augmented with the address-taken
	// escape rule (see computeMainReachable). Unlike MustEdges/MayEdges —
	// which are folded to intra-module edges (fold() drops every cross-module
	// edge) — this reachability is computed by traversing the raw
	// cha.CallGraph with NO exclusions, straight through synthetic wrappers
	// and closures. Folding severs main -> cobra.Execute -> RunE handler, so a
	// reachability set derived from the folded edges is vacuous for anything
	// behind a framework; MainReachable exists precisely to avoid that. See
	// the dead-verdict cross-check in godeadcheck.go.
	MainReachable map[goFuncID]bool

	// Notes-worthy counts, surfaced in the final report.
	SyntheticExcluded int // synthetic/init/closure functions skipped as callers or callees
	CrossModuleEdges  int // edges dropped because an endpoint is outside the project module
	UnmappableFuncs   int // ssa.Functions that could not be reduced to a goFuncID
}

// extractGoCallGraphs loads projectRoot as a Go module, builds an SSA
// program over it, and returns the must (static direct calls) and may (CHA)
// edge sets restricted to intra-project-module functions.
func extractGoCallGraphs(projectRoot string) (*goExtraction, error) {
	cfg := &packages.Config{
		Mode: packagesLoadMode,
		Dir:  projectRoot,
		Env:  cleanGoEnv(),
		// Tests: true is required so the SSA program includes _test.go
		// files. Without it, go/packages loads only the non-test package
		// variant, and every call originating in test code (setup helpers,
		// TestXxx functions, table-driven test bodies) is structurally
		// invisible to both static and CHA — even though scip-go indexes
		// test files and the graph legitimately carries CALLS edges for
		// them. Omitting this turned thousands of genuine test-code edges
		// into false "precision suspects" during development of this
		// oracle (discovered by sampling: >90% of suspects had a caller
		// name of the form TestXxx with zero may-graph presence at all).
		Tests: true,
	}

	initial, err := packages.Load(cfg, "./...")
	if err != nil {
		return nil, fmt.Errorf("packages.Load: %w", err)
	}
	if len(initial) == 0 {
		return nil, fmt.Errorf("packages.Load returned no packages (not a Go module? missing go.mod under %s)", projectRoot)
	}
	if packages.PrintErrors(initial) > 0 {
		return nil, fmt.Errorf("packages.Load reported errors under %s (run `go build ./...` there to see them)", projectRoot)
	}

	modulePath := ""
	for _, p := range initial {
		if p.Module != nil && p.Module.Main {
			modulePath = p.Module.Path
			break
		}
	}
	if modulePath == "" {
		return nil, fmt.Errorf("could not determine main module path for %s", projectRoot)
	}

	// ssautil.Packages (NOT AllPackages): build SSA bodies for the initial
	// (in-module) packages and leave their dependencies as "from type
	// information" stubs. This is deliberate and load-bearing for the
	// dead-verdict cross-check.
	//
	// The tempting alternative — AllPackages, which builds every dependency
	// body so the raw BFS can walk THROUGH a framework (cobra
	// ExecuteC -> (*Command).execute -> the RunE closure) — was measured and
	// rejected: whole-program CHA then resolves every dependency's dynamic
	// dispatch (interface invokes, func-value calls) against the entire loaded
	// world, and that noise floods back into in-module space. Concretely, with
	// AllPackages the codegraph self-run produced 3 SPURIOUS dead-verdict
	// disagreements on top of the 1 real one, each via a nonsense chain:
	// graph.NewClient -[func-value]-> protobuf/flag/testing internals
	// -[interface Reset()/Set()]-> an unrelated in-module method that merely
	// shares a signature (e.g. proto.Reset resolving to benchmarks.PhaseTimer.Reset).
	// Reachable identities ballooned 389 -> 1722, i.e. near-total connectivity.
	//
	// Instead, MainReachable reaches framework-dispatched handlers WITHOUT
	// dependency bodies, via the address-taken escape rule in
	// computeMainReachable: a cobra RunE handler is stored as a function VALUE
	// into cobra.Command, so it is reachable the moment the storing in-module
	// code is reached, regardless of whether cobra's own body is built. That
	// mirrors the classifier's graph-side USES_VALUE (address-taken) liveness
	// exactly, so the two derivations stay in agreement.
	prog, _ := ssautil.Packages(initial, ssa.InstantiateGenerics)
	prog.Build()

	extraction := &goExtraction{
		ModulePath:    modulePath,
		MustEdges:     make(map[edgeKey]bool),
		MayEdges:      make(map[edgeKey]bool),
		MainReachable: make(map[goFuncID]bool),
	}

	staticGraph := static.CallGraph(prog)
	extraction.fold(staticGraph, extraction.MustEdges)

	chaGraph := cha.CallGraph(prog)
	extraction.fold(chaGraph, extraction.MayEdges)
	extraction.computeMainReachable(chaGraph)

	return extraction, nil
}

// computeMainReachable does a raw BFS over the CHA call graph — no folding,
// no exclusions — from the program's true entry points, augmented by the
// address-taken escape rule, and projects every reached node onto its
// in-module named-function identity. The projection (enclosingNamedFunc +
// funcIdentity) matches how the graph attributes calls, so a resulting
// MainReachable[id] means "the classifier's tier-2/tier-3 roots can reach id
// through some may-call chain or by taking its address".
//
// This deliberately does NOT reuse fold()/isExcludedFunc: the whole point is
// to keep the synthetic-wrapper edges and closure hops that fold() discards.
// It also does not build dependency bodies (see the ssautil.Packages rationale
// in extractGoCallGraphs) — instead the escape rule below recovers reachability
// of handlers a dependency invokes internally (cobra RunE, http.HandleFunc
// callbacks), because those handlers are address-taken by in-module code before
// control ever enters the dependency.
func (ex *goExtraction) computeMainReachable(g *callgraph.Graph) {
	var queue []*callgraph.Node
	seen := make(map[*callgraph.Node]bool, len(g.Nodes))

	// Roots: package main() and every init (synthesized `init` covering
	// package-level var initialization, plus declared `init#N`), restricted
	// to in-module, non-test-driver functions — mirroring the reachability
	// classifier's tier-2 (name IN ['main','init']) and tier-3
	// (module-load/package-init) roots.
	for fn, node := range g.Nodes {
		if node == nil || fn == nil || !isMainReachableRoot(fn, ex.ModulePath) {
			continue
		}
		if !seen[node] {
			seen[node] = true
			queue = append(queue, node)
		}
	}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]

		if named := enclosingNamedFunc(cur.Func); named != nil {
			if id, ok := funcIdentity(named); ok && pkgInModule(id.pkgPath, ex.ModulePath) {
				ex.MainReachable[id] = true
			}
		}

		for _, edge := range cur.Out {
			nxt := edge.Callee
			if nxt == nil || seen[nxt] {
				continue
			}
			seen[nxt] = true
			queue = append(queue, nxt)
		}

		// Escape rule: any function referenced as a VALUE by a reachable
		// function is itself reachable. A cobra RunE handler, an
		// errgroup.Go closure, an http.HandleFunc callback — all escape
		// into a dependency (whose body is a type-info stub here, see the
		// load-mode comment above) and are invoked by code this BFS
		// deliberately does not traverse, so no call edge ever enters
		// them. Scanning instruction operands for *ssa.Function values
		// catches MakeClosure fns and stored/passed function constants
		// (a superset of static callees, which the edge walk above
		// already covers — the seen-set dedupes the overlap). The
		// classifier makes the identical choice graph-side: USES_VALUE
		// (address-taken) is liveness-preserving, so reachable ⊇
		// address-taken on both sides and the rule can never manufacture
		// a false disagreement. Dependency stubs have no blocks, so this
		// scan is a no-op for them and imports none of the whole-program
		// CHA noise that ssautil.AllPackages would.
		if cur.Func == nil {
			continue
		}
		var rands []*ssa.Value
		for _, b := range cur.Func.Blocks {
			for _, instr := range b.Instrs {
				rands = instr.Operands(rands[:0])
				for _, rand := range rands {
					if rand == nil || *rand == nil {
						continue
					}
					escaped, ok := (*rand).(*ssa.Function)
					if !ok {
						continue
					}
					nxt := g.Nodes[escaped]
					if nxt == nil || seen[nxt] {
						continue
					}
					seen[nxt] = true
					queue = append(queue, nxt)
				}
			}
		}
	}
}

// isMainReachableRoot reports whether fn is a binary entry point the
// reachability classifier would treat as a root: an in-module package `main`
// or any init (synthesized `init` or a declared `init#N`). Test-driver
// packages (`<pkg>.test`) are excluded — their synthesized main is a
// toolchain artifact with no graph node, exactly as isExcludedFunc excludes
// it from the folded edge sets.
func isMainReachableRoot(fn *ssa.Function, modulePath string) bool {
	if fn.Pkg == nil || fn.Pkg.Pkg == nil {
		return false
	}
	pkgPath := fn.Pkg.Pkg.Path()
	if !pkgInModule(pkgPath, modulePath) {
		return false
	}
	if strings.HasSuffix(pkgPath, ".test") {
		return false
	}
	name := fn.Name()
	if name == "main" && fn.Pkg.Pkg.Name() == "main" {
		return true
	}
	return name == "init" || strings.HasPrefix(name, "init#")
}

// fold walks every edge in g and inserts intra-module, non-excluded edges
// into dst, counting exclusions as it goes.
//
// Caller-side closure folding happens before exclusion/identity: a call
// whose Caller is a closure literal is attributed to its enclosing named
// function (enclosingNamedFunc) so the SSA-side edge set matches scip-go's
// containment-based attribution. Callee-side closures are still excluded
// outright — the graph has no node for an anonymous function literal to
// join against.
func (ex *goExtraction) fold(g *callgraph.Graph, dst map[edgeKey]bool) {
	for fn, node := range g.Nodes {
		if fn == nil {
			continue // synthetic root
		}
		for _, edge := range node.Out {
			caller := enclosingNamedFunc(edge.Caller.Func)
			callee := edge.Callee.Func

			if isExcludedFunc(caller) || isExcludedFunc(callee) {
				ex.SyntheticExcluded++
				continue
			}

			fromID, ok := funcIdentity(caller)
			if !ok {
				ex.UnmappableFuncs++
				continue
			}
			toID, ok := funcIdentity(callee)
			if !ok {
				ex.UnmappableFuncs++
				continue
			}

			if fromID.pkgPath != ex.ModulePath && !pkgInModule(fromID.pkgPath, ex.ModulePath) {
				ex.CrossModuleEdges++
				continue
			}
			if toID.pkgPath != ex.ModulePath && !pkgInModule(toID.pkgPath, ex.ModulePath) {
				ex.CrossModuleEdges++
				continue
			}

			dst[edgeKey{from: fromID, to: toID}] = true
		}
	}
}

// pkgInModule reports whether pkgPath is the module itself or one of its
// sub-packages (module/...). Both static.CallGraph and cha.CallGraph range
// over every function reachable from the loaded packages, which includes
// stdlib and third-party dependencies pulled in for type-checking; those
// must never appear in must/may edge sets meant to compare against
// intra-project CALLS edges.
func pkgInModule(pkgPath, modulePath string) bool {
	if pkgPath == modulePath {
		return true
	}
	return len(pkgPath) > len(modulePath) &&
		pkgPath[:len(modulePath)] == modulePath &&
		pkgPath[len(modulePath)] == '/'
}
