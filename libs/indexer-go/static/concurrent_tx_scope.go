package static

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"strings"
)

// concurrentScope represents a goroutine / errgroup.Go / worker dispatch body.
// All call sites whose line falls inside [StartLine, EndLine] execute
// concurrently with the enclosing caller's normal sequence.
type concurrentScope struct {
	Kind              string // goroutine | errgroup | waitgroup | channel_send
	EnclosingFunction string
	FilePath          string
	StartLine         int
	EndLine           int
	NodeKey           string // cscScopeKey(filePath, startLine, endLine, kind)
}

// txScope represents a transaction boundary detected via either an explicit
// BeginTx/Commit pair (pgx_begintx) or a closure-bearing helper like
// WithTx / InTx / Transaction (with_tx_func).
type txScope struct {
	Kind              string // pgx_begintx | with_tx_func | gorm_transaction
	EnclosingFunction string
	FilePath          string
	StartLine         int
	EndLine           int
	Isolation         string
	NodeKey           string // txScopeKey(filePath, startLine, endLine, kind)
}

// cscScopeKey returns the stable identity key for a ConcurrentScope node.
func cscScopeKey(filePath string, startLine, endLine int, kind string) string {
	return fmt.Sprintf("csc:%s:%d:%d:%s", filePath, startLine, endLine, kind)
}

// txKey returns the stable identity key for a TxScope node.
func txKey(filePath string, startLine, endLine int, kind string) string {
	return fmt.Sprintf("tx:%s:%d:%d:%s", filePath, startLine, endLine, kind)
}

// txClosureNamePattern matches function names whose presence + trailing
// closure indicates a transactional scope, e.g. WithTx, InTx, Transaction,
// RunInTransaction.
var txClosureNamePattern = regexp.MustCompile(`(?i)withtx|intx|transaction`)

// parseConcurrentAndTxScopes parses filePath and returns the concurrent and
// transactional scope spans discovered in the file. Best-effort: parse errors
// surface to the caller but per-construct detection failures are silently
// skipped — losing a scope is preferable to dropping the file's CALLS edges.
func parseConcurrentAndTxScopes(filePath string) ([]concurrentScope, []txScope, error) {
	src, err := os.ReadFile(filePath)
	if err != nil {
		return nil, nil, err
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filePath, src, 0)
	if err != nil {
		return nil, nil, err
	}

	var concs []concurrentScope
	var txs []txScope

	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		fname := fn.Name.Name
		if fn.Recv != nil && len(fn.Recv.List) > 0 {
			if recv := exprName(fn.Recv.List[0].Type); recv != "" {
				fname = recv + "." + fname
			}
		}
		bodyEnd := fset.Position(fn.Body.Rbrace).Line

		ast.Inspect(fn.Body, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.GoStmt:
				start, end := goStmtBodyRange(node, fset)
				concs = append(concs, concurrentScope{
					Kind:              "goroutine",
					EnclosingFunction: fname,
					FilePath:          filePath,
					StartLine:         start,
					EndLine:           end,
					NodeKey:           cscScopeKey(filePath, start, end, "goroutine"),
				})
			case *ast.CallExpr:
				// errgroup.Go(func() error { ... })
				if isErrgroupGo(node) {
					if lit := lastFuncLit(node); lit != nil {
						start := fset.Position(lit.Body.Lbrace).Line
						end := fset.Position(lit.Body.Rbrace).Line
						concs = append(concs, concurrentScope{
							Kind:              "errgroup",
							EnclosingFunction: fname,
							FilePath:          filePath,
							StartLine:         start,
							EndLine:           end,
							NodeKey:           cscScopeKey(filePath, start, end, "errgroup"),
						})
					}
				}
				// WithTx(ctx, func(tx) error { ... }) etc.
				if isTxClosure(node) {
					if lit := lastFuncLit(node); lit != nil {
						start := fset.Position(lit.Body.Lbrace).Line
						end := fset.Position(lit.Body.Rbrace).Line
						txs = append(txs, txScope{
							Kind:              "with_tx_func",
							EnclosingFunction: fname,
							FilePath:          filePath,
							StartLine:         start,
							EndLine:           end,
							NodeKey:           txKey(filePath, start, end, "with_tx_func"),
						})
					}
				}
			case *ast.AssignStmt:
				// `tx, err := repo.Pgx().BeginTx(ctx)` style.
				if isBeginTxAssign(node) {
					start := fset.Position(node.Pos()).Line
					end := findPgxTxCloser(fn.Body, fset, node, bodyEnd)
					txs = append(txs, txScope{
						Kind:              "pgx_begintx",
						EnclosingFunction: fname,
						FilePath:          filePath,
						StartLine:         start,
						EndLine:           end,
						NodeKey:           txKey(filePath, start, end, "pgx_begintx"),
					})
				}
			}
			return true
		})
	}

	return concs, txs, nil
}

// goStmtBodyRange returns the line range that should be associated with a
// `go func() { … }()` or `go SomeFunc(…)` statement. When the goroutine call
// dispatches a literal function we return that body's brace range; otherwise
// we use the GoStmt's own range, which still gives us a usable signal but no
// per-callee scope.
func goStmtBodyRange(g *ast.GoStmt, fset *token.FileSet) (int, int) {
	if lit, ok := g.Call.Fun.(*ast.FuncLit); ok && lit.Body != nil {
		return fset.Position(lit.Body.Lbrace).Line, fset.Position(lit.Body.Rbrace).Line
	}
	return fset.Position(g.Pos()).Line, fset.Position(g.End()).Line
}

// isErrgroupGo detects calls shaped like `<x>.Go(<args>, func() error { … })`.
// We don't have type info during pure AST walking, so we approximate with
// "selector method named Go AND last argument is a function literal." This
// catches errgroup.Group, taskgroup.Group, and similar third-party look-alikes.
func isErrgroupGo(ce *ast.CallExpr) bool {
	sel, ok := ce.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Go" {
		return false
	}
	return lastFuncLit(ce) != nil
}

// isTxClosure detects calls whose name matches `(?i)withtx|intx|transaction`
// AND whose last argument is a function literal. Matches WithTx, RunInTx,
// RunInTransaction, gorm's Transaction, etc.
func isTxClosure(ce *ast.CallExpr) bool {
	name := ""
	switch fn := ce.Fun.(type) {
	case *ast.SelectorExpr:
		name = fn.Sel.Name
	case *ast.Ident:
		name = fn.Name
	default:
		return false
	}
	if !txClosureNamePattern.MatchString(name) {
		return false
	}
	return lastFuncLit(ce) != nil
}

// lastFuncLit returns the last argument of ce when it is a *ast.FuncLit, or
// nil otherwise. This pattern is the load-bearing signal for both errgroup
// and tx-closure detection.
func lastFuncLit(ce *ast.CallExpr) *ast.FuncLit {
	if len(ce.Args) == 0 {
		return nil
	}
	lit, ok := ce.Args[len(ce.Args)-1].(*ast.FuncLit)
	if !ok || lit.Body == nil {
		return nil
	}
	return lit
}

// isBeginTxAssign matches assignments whose RHS is a call ending in BeginTx,
// e.g. `tx, err := repo.Pgx().BeginTx(ctx)` or `tx := db.BeginTx(ctx, opts)`.
// Pure AST: the selector's tail token is sufficient and avoids type
// resolution costs.
func isBeginTxAssign(stmt *ast.AssignStmt) bool {
	for _, rhs := range stmt.Rhs {
		ce, ok := rhs.(*ast.CallExpr)
		if !ok {
			continue
		}
		if sel, ok := ce.Fun.(*ast.SelectorExpr); ok {
			if strings.EqualFold(sel.Sel.Name, "BeginTx") || strings.EqualFold(sel.Sel.Name, "Begin") {
				return true
			}
		}
	}
	return false
}

// findPgxTxCloser scans the function body for the highest-line call to
// Commit / Rollback / EndPgxTx that occurs after the BeginTx assignment, and
// returns that line. Falls back to the function body's closing brace line so
// the scope at least bounds the rest of the function.
//
// We deliberately ignore variable identity here: matching the receiver
// expression of the BeginTx call against later Commit calls requires
// resolving identifiers across statements, which adds cost without much
// gain — a function rarely opens more than one tx, and over-approximating the
// scope to the function tail is acceptable for the indexer's purposes.
func findPgxTxCloser(body *ast.BlockStmt, fset *token.FileSet, beginAssign *ast.AssignStmt, fallbackEnd int) int {
	beginLine := fset.Position(beginAssign.Pos()).Line
	closer := fallbackEnd

	ast.Inspect(body, func(n ast.Node) bool {
		ce, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := ce.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		name := sel.Sel.Name
		if !isTxCloserName(name) {
			return true
		}
		line := fset.Position(ce.Pos()).Line
		if line > beginLine && line < closer {
			closer = line
		}
		return true
	})

	return closer
}

func isTxCloserName(name string) bool {
	switch name {
	case "Commit", "Rollback", "EndPgxTx", "EndTx":
		return true
	}
	return false
}

// findInnermostConcurrentScope returns the tightest-spanning ConcurrentScope
// containing line, or nil if none does.
func findInnermostConcurrentScope(scopes []concurrentScope, line int) *concurrentScope {
	var best *concurrentScope
	bestSpan := int(^uint(0) >> 1)
	for i := range scopes {
		s := &scopes[i]
		if line >= s.StartLine && line <= s.EndLine {
			span := s.EndLine - s.StartLine
			if span < bestSpan {
				best = s
				bestSpan = span
			}
		}
	}
	return best
}

// findInnermostTxScope returns the tightest-spanning TxScope containing line,
// or nil if none does.
func findInnermostTxScope(scopes []txScope, line int) *txScope {
	var best *txScope
	bestSpan := int(^uint(0) >> 1)
	for i := range scopes {
		s := &scopes[i]
		if line >= s.StartLine && line <= s.EndLine {
			span := s.EndLine - s.StartLine
			if span < bestSpan {
				best = s
				bestSpan = span
			}
		}
	}
	return best
}
