// call_sites.go: shared call-site classification for both call-graph
// builders (Go/AST and generic/tree-sitter). A SCIP Reference to a function
// symbol is only a CALLS edge when the referenced identifier is the callee
// of an actual invocation — `handler = fn` is a function-VALUE use, not a
// call, and previously produced false CALLS edges (found by the RFC-013 Go
// oracle: `embedder.Fn = semlinkVectors`). Call sites with no enclosing
// function body are module-scope executions (package-level var initializers
// in Go, top-level statements in TS/Python/etc.) and are attributed to the
// FILE node: (File)-[:CALLS]->(Function), which liveness consumers treat as
// import-time invocation.
package static

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"

	"github.com/context-maximiser/code-graph/internal/ingest/structure"
)

// callSitePos is a (1-based line, 0-based column) callee-identifier position.
type callSitePos struct{ Line, Col int }

// callSiteIndex answers "is the reference at this position a genuine call?"
// for one file. A nil *callSiteIndex means no call-site information is
// available (no grammar, unreadable file) — callers must then preserve the
// legacy behavior (treat every reference as a call, attribute only within
// function bodies) rather than dropping edges on missing evidence.
type callSiteIndex struct {
	exact  map[callSitePos]bool
	byLine map[int]map[string]bool
}

func newCallSiteIndex() *callSiteIndex {
	return &callSiteIndex{
		exact:  make(map[callSitePos]bool),
		byLine: make(map[int]map[string]bool),
	}
}

func (idx *callSiteIndex) add(line, col int, name string) {
	idx.exact[callSitePos{line, col}] = true
	names := idx.byLine[line]
	if names == nil {
		names = make(map[string]bool)
		idx.byLine[line] = names
	}
	if name != "" {
		names[name] = true
	}
}

// isCallSite reports whether the reference at (line, col) with callee name
// `name` is a genuine invocation. Exact position match first; the
// (line, name) fallback absorbs column-encoding drift (SCIP occurrences on
// non-ASCII lines) and is sound for edge existence: it only confirms names
// that ARE called on that line, so at worst a value ref collapses into the
// dedup'd edge the real call on the same line justifies anyway.
func (idx *callSiteIndex) isCallSite(line, col int, name string) bool {
	if idx == nil {
		return false
	}
	if col >= 0 && idx.exact[callSitePos{line, col}] {
		return true
	}
	return name != "" && idx.byLine[line][name]
}

// callSiteIndexFromStructure adapts tree-sitter call sites (see
// structure.FileStructure.CallSites) for the generic builder. Returns nil
// for a nil structure, preserving the "no evidence, no filtering" contract.
func callSiteIndexFromStructure(fs *structure.FileStructure) *callSiteIndex {
	if fs == nil {
		return nil
	}
	idx := newCallSiteIndex()
	for _, cs := range fs.CallSites {
		idx.add(cs.Line, cs.Col, cs.Name)
	}
	return idx
}

// parseGoCallSites parses a Go file and indexes every CallExpr's callee
// identifier position. Columns are converted from go/token's 1-based bytes
// to SCIP's 0-based convention. Type conversions (`T(x)`) index T too —
// harmless, since references to types never resolve to Function/Method
// targets in the reference query.
func parseGoCallSites(filePath string) (*callSiteIndex, error) {
	src, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filePath, src, 0)
	if err != nil {
		return nil, err
	}

	idx := newCallSiteIndex()
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if id := goCalleeIdent(call.Fun); id != nil {
			pos := fset.Position(id.Pos())
			idx.add(pos.Line, pos.Column-1, id.Name)
		}
		return true
	})
	return idx, nil
}

// goCalleeIdent unwraps a CallExpr's Fun to the identifier a SCIP reference
// occurrence covers: `foo()` → foo, `pkg.Fn()` / `recv.Method()` → the Sel,
// `foo[T]()` (generic instantiation) → foo. Computed callees (`fns[i]()`,
// `factory()()`) return nil — no identifier, no call site.
func goCalleeIdent(e ast.Expr) *ast.Ident {
	switch v := e.(type) {
	case *ast.Ident:
		return v
	case *ast.SelectorExpr:
		return v.Sel
	case *ast.ParenExpr:
		return goCalleeIdent(v.X)
	case *ast.IndexExpr:
		return goCalleeIdent(v.X)
	case *ast.IndexListExpr:
		return goCalleeIdent(v.X)
	}
	return nil
}
