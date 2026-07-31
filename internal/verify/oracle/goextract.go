package oracle

import (
	"fmt"

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

	prog, _ := ssautil.Packages(initial, ssa.InstantiateGenerics)
	prog.Build()

	extraction := &goExtraction{
		ModulePath: modulePath,
		MustEdges:  make(map[edgeKey]bool),
		MayEdges:   make(map[edgeKey]bool),
	}

	staticGraph := static.CallGraph(prog)
	extraction.fold(staticGraph, extraction.MustEdges)

	chaGraph := cha.CallGraph(prog)
	extraction.fold(chaGraph, extraction.MayEdges)

	return extraction, nil
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
