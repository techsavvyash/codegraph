package static

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
)

// callMeta carries the AST-derived enrichment we attach to each CALLS edge.
// See INDEXING_RCA_AND_SOLUTION.md §4.3–§4.6 for the underlying rationale.
type callMeta struct {
	OrderIndex     int      // monotonic per enclosing function (source order)
	LiteralArgs    []string // first 3 STRING args, ≤80 chars each
	NearestComment string   // comment within 2 lines above the call, ≤200 chars
	ReceiverChain  []string // dotted receiver path, e.g. ["repository","Pgx()","Payout","Save"]
}

const (
	maxLiteralArgs        = 3
	maxLiteralArgLen      = 80
	maxNearestCommentGap  = 2
	maxNearestCommentLen  = 200
	receiverChainMaxParts = 8
)

// parseCallMetadata parses filePath and returns per-call enrichment keyed by
// "<line>:<methodName>". Multiple CallExprs may share a line (chained calls
// like `repo.Pgx().Save(...)`); the method-name suffix disambiguates them so
// the SCIP-driven CALLS edge writer can look up the right entry.
func parseCallMetadata(filePath string) (map[string]callMeta, error) {
	src, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filePath, src, parser.ParseComments)
	if err != nil {
		return nil, err
	}

	result := make(map[string]callMeta)
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		order := 0
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			ce, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			order++
			line := fset.Position(callTargetPos(ce)).Line
			name := callTargetName(ce)
			if name == "" {
				return true
			}
			key := fmt.Sprintf("%d:%s", line, name)
			result[key] = callMeta{
				OrderIndex:     order,
				LiteralArgs:    extractLiteralArgs(ce, maxLiteralArgs, maxLiteralArgLen),
				NearestComment: nearestCommentForCall(ce, f.Comments, fset, maxNearestCommentGap, maxNearestCommentLen),
				ReceiverChain:  extractReceiverChain(ce.Fun),
			}
			return true
		})
	}
	return result, nil
}

// callTargetPos returns the token position of the called name — the position
// SCIP records on its Reference (the method-name token, not the receiver).
func callTargetPos(ce *ast.CallExpr) token.Pos {
	switch fn := ce.Fun.(type) {
	case *ast.SelectorExpr:
		return fn.Sel.Pos()
	case *ast.Ident:
		return fn.Pos()
	}
	return ce.Pos()
}

// callTargetName returns the bare method/function name at the call site, or
// "" when the callee is not a selector or identifier (function-typed
// expressions, type assertions, etc.).
func callTargetName(ce *ast.CallExpr) string {
	switch fn := ce.Fun.(type) {
	case *ast.SelectorExpr:
		return fn.Sel.Name
	case *ast.Ident:
		return fn.Name
	}
	return ""
}

// extractLiteralArgs collects the first maxCount STRING basic literals from
// the call's argument list, truncating each to maxLen characters. Non-string
// args are skipped (we don't synthesise summaries — only literals carry
// reliable identity signal at index time).
func extractLiteralArgs(ce *ast.CallExpr, maxCount, maxLen int) []string {
	if len(ce.Args) == 0 {
		return nil
	}
	lits := make([]string, 0, maxCount)
	for _, arg := range ce.Args {
		if len(lits) >= maxCount {
			break
		}
		bl, ok := arg.(*ast.BasicLit)
		if !ok || bl.Kind != token.STRING {
			continue
		}
		val := bl.Value
		if len(val) >= 2 && (val[0] == '"' || val[0] == '`') {
			val = val[1 : len(val)-1]
		}
		if len(val) > maxLen {
			val = val[:maxLen]
		}
		lits = append(lits, val)
	}
	if len(lits) == 0 {
		return nil
	}
	return lits
}

// nearestCommentForCall finds the comment group whose End() lies on the same
// or previous line as the call (within maxGap lines), and returns its
// trimmed text up to maxLen characters. Newlines are flattened to spaces so
// the result is a single edge property.
func nearestCommentForCall(ce *ast.CallExpr, comments []*ast.CommentGroup, fset *token.FileSet, maxGap, maxLen int) string {
	callLine := fset.Position(ce.Pos()).Line
	var best *ast.CommentGroup
	bestLine := -1
	for _, cg := range comments {
		endLine := fset.Position(cg.End()).Line
		if endLine > callLine {
			continue
		}
		if callLine-endLine > maxGap {
			continue
		}
		if endLine > bestLine {
			best = cg
			bestLine = endLine
		}
	}
	if best == nil {
		return ""
	}
	text := strings.TrimSpace(best.Text())
	if text == "" {
		return ""
	}
	text = strings.ReplaceAll(text, "\n", " ")
	text = strings.TrimSpace(text)
	if len(text) > maxLen {
		text = text[:maxLen]
	}
	return text
}

// extractReceiverChain walks the callee expression and returns the dotted
// receiver path as a slice. Embedded function calls are marked with a "()"
// suffix on the preceding name to preserve the distinction between a method
// invocation (`Pgx()`) and a plain field access (`Payout`).
//
// Examples:
//
//	foo.Bar()                       → ["foo", "Bar"]
//	repo.Pgx().Payout.Save(ctx, p)  → ["repository", "Pgx()", "Payout", "Save"]
//	pkg.Helper(x)                   → ["pkg", "Helper"]
//
// The slice is capped at receiverChainMaxParts to keep edge payloads bounded
// on unusual deeply-chained expressions.
func extractReceiverChain(expr ast.Expr) []string {
	var parts []string
	var walk func(ast.Expr)
	walk = func(e ast.Expr) {
		switch t := e.(type) {
		case *ast.SelectorExpr:
			walk(t.X)
			parts = append(parts, t.Sel.Name)
		case *ast.CallExpr:
			walk(t.Fun)
			if n := len(parts); n > 0 {
				parts[n-1] += "()"
			}
		case *ast.Ident:
			parts = append(parts, t.Name)
		case *ast.IndexExpr:
			walk(t.X)
		case *ast.IndexListExpr:
			walk(t.X)
		case *ast.StarExpr:
			walk(t.X)
		case *ast.ParenExpr:
			walk(t.X)
		}
	}
	walk(expr)
	if len(parts) > receiverChainMaxParts {
		parts = parts[len(parts)-receiverChainMaxParts:]
	}
	if len(parts) == 0 {
		return nil
	}
	return parts
}
