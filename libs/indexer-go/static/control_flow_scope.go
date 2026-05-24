package static

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
)

// controlFlowScope represents a single conditional/loop block in a Go source file.
// Depth is 1-based (top-level scope in a function = 1).
type controlFlowScope struct {
	Kind           string // if, else, switch, switch_case, for, range, select, select_case
	Condition      string // raw source text ≤200 chars
	FilePath       string
	StartLine      int
	EndLine        int
	Depth          int    // 1-based nesting level
	ParentScopeKey string // nodeKey of enclosing scope; empty for outermost scopes
	NodeKey        string // cfsScopeKey(filePath, startLine, endLine, kind)
}

// cfsScopeKey returns the stable identity key for a ControlFlowScope node.
// Pattern: cfs:<filePath>:<startLine>:<endLine>:<kind>
func cfsScopeKey(filePath string, startLine, endLine int, kind string) string {
	return fmt.Sprintf("cfs:%s:%d:%d:%s", filePath, startLine, endLine, kind)
}

// nodeText extracts source text for an AST node, normalises whitespace, and
// truncates to 200 characters.
func nodeText(src []byte, fset *token.FileSet, node ast.Node) string {
	if node == nil {
		return ""
	}
	start := fset.Position(node.Pos()).Offset
	end := fset.Position(node.End()).Offset
	if start < 0 || end > len(src) || start >= end {
		return ""
	}
	raw := string(src[start:end])
	// Normalise whitespace: collapse newlines/tabs to single spaces.
	parts := strings.Fields(raw)
	text := strings.Join(parts, " ")
	if len(text) > 200 {
		text = text[:200]
	}
	return text
}

// caseExprText joins multiple case-expression AST nodes into a single string,
// separated by ", ", truncated to 200 characters.
func caseExprText(src []byte, fset *token.FileSet, exprs []ast.Expr) string {
	parts := make([]string, 0, len(exprs))
	for _, e := range exprs {
		t := nodeText(src, fset, e)
		if t != "" {
			parts = append(parts, t)
		}
	}
	text := strings.Join(parts, ", ")
	if len(text) > 200 {
		text = text[:200]
	}
	return text
}

// parseControlFlowScopes parses a Go source file and returns all control-flow
// scope blocks (if/else/switch/for/range/select and their case/comm clauses).
// Depth and ParentScopeKey are computed post-hoc.
func parseControlFlowScopes(filePath string) ([]controlFlowScope, error) {
	src, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filePath, src, 0)
	if err != nil {
		return nil, err
	}

	var scopes []controlFlowScope

	ast.Inspect(f, func(n ast.Node) bool {
		if n == nil {
			return false
		}
		switch stmt := n.(type) {
		case *ast.IfStmt:
			start := fset.Position(stmt.Pos()).Line
			end := fset.Position(stmt.End()).Line
			cond := nodeText(src, fset, stmt.Cond)
			scopes = append(scopes, controlFlowScope{
				Kind:      "if",
				Condition: cond,
				FilePath:  filePath,
				StartLine: start,
				EndLine:   end,
				NodeKey:   cfsScopeKey(filePath, start, end, "if"),
			})
			// Capture an explicit "else" block only when Else is a *ast.BlockStmt
			// (i.e., not an else-if, which becomes its own IfStmt and is visited
			// separately by ast.Inspect).
			if block, ok := stmt.Else.(*ast.BlockStmt); ok {
				elseStart := fset.Position(block.Pos()).Line
				elseEnd := fset.Position(block.End()).Line
				scopes = append(scopes, controlFlowScope{
					Kind:      "else",
					Condition: "",
					FilePath:  filePath,
					StartLine: elseStart,
					EndLine:   elseEnd,
					NodeKey:   cfsScopeKey(filePath, elseStart, elseEnd, "else"),
				})
			}

		case *ast.SwitchStmt:
			start := fset.Position(stmt.Pos()).Line
			end := fset.Position(stmt.End()).Line
			cond := nodeText(src, fset, stmt.Tag)
			scopes = append(scopes, controlFlowScope{
				Kind:      "switch",
				Condition: cond,
				FilePath:  filePath,
				StartLine: start,
				EndLine:   end,
				NodeKey:   cfsScopeKey(filePath, start, end, "switch"),
			})

		case *ast.TypeSwitchStmt:
			start := fset.Position(stmt.Pos()).Line
			end := fset.Position(stmt.End()).Line
			cond := nodeText(src, fset, stmt.Assign)
			scopes = append(scopes, controlFlowScope{
				Kind:      "switch",
				Condition: cond,
				FilePath:  filePath,
				StartLine: start,
				EndLine:   end,
				NodeKey:   cfsScopeKey(filePath, start, end, "switch"),
			})

		case *ast.CaseClause:
			start := fset.Position(stmt.Pos()).Line
			end := fset.Position(stmt.End()).Line
			var cond string
			if len(stmt.List) == 0 {
				cond = "default"
			} else {
				cond = caseExprText(src, fset, stmt.List)
			}
			scopes = append(scopes, controlFlowScope{
				Kind:      "switch_case",
				Condition: cond,
				FilePath:  filePath,
				StartLine: start,
				EndLine:   end,
				NodeKey:   cfsScopeKey(filePath, start, end, "switch_case"),
			})

		case *ast.ForStmt:
			start := fset.Position(stmt.Pos()).Line
			end := fset.Position(stmt.End()).Line
			cond := nodeText(src, fset, stmt.Cond)
			scopes = append(scopes, controlFlowScope{
				Kind:      "for",
				Condition: cond,
				FilePath:  filePath,
				StartLine: start,
				EndLine:   end,
				NodeKey:   cfsScopeKey(filePath, start, end, "for"),
			})

		case *ast.RangeStmt:
			start := fset.Position(stmt.Pos()).Line
			end := fset.Position(stmt.End()).Line
			cond := nodeText(src, fset, stmt.X)
			scopes = append(scopes, controlFlowScope{
				Kind:      "range",
				Condition: cond,
				FilePath:  filePath,
				StartLine: start,
				EndLine:   end,
				NodeKey:   cfsScopeKey(filePath, start, end, "range"),
			})

		case *ast.SelectStmt:
			start := fset.Position(stmt.Pos()).Line
			end := fset.Position(stmt.End()).Line
			scopes = append(scopes, controlFlowScope{
				Kind:      "select",
				Condition: "",
				FilePath:  filePath,
				StartLine: start,
				EndLine:   end,
				NodeKey:   cfsScopeKey(filePath, start, end, "select"),
			})

		case *ast.CommClause:
			start := fset.Position(stmt.Pos()).Line
			end := fset.Position(stmt.End()).Line
			var cond string
			if stmt.Comm == nil {
				cond = "default"
			} else {
				cond = nodeText(src, fset, stmt.Comm)
			}
			scopes = append(scopes, controlFlowScope{
				Kind:      "select_case",
				Condition: cond,
				FilePath:  filePath,
				StartLine: start,
				EndLine:   end,
				NodeKey:   cfsScopeKey(filePath, start, end, "select_case"),
			})
		}
		return true
	})

	// Post-hoc: compute Depth and ParentScopeKey.
	// For each scope i, depth = 1 + count of strictly-containing scopes j.
	// A scope j strictly contains i when j.StartLine ≤ i.StartLine AND
	// j.EndLine ≥ i.EndLine AND the ranges are not identical.
	for i := range scopes {
		depth := 0
		var parentKey string
		parentSpan := int(^uint(0) >> 1) // max int — track tightest container

		for j := range scopes {
			if i == j {
				continue
			}
			p := &scopes[j]
			s := &scopes[i]
			if p.StartLine <= s.StartLine && p.EndLine >= s.EndLine &&
				!(p.StartLine == s.StartLine && p.EndLine == s.EndLine) {
				depth++
				span := p.EndLine - p.StartLine
				if span < parentSpan {
					parentSpan = span
					parentKey = p.NodeKey
				}
			}
		}
		scopes[i].Depth = depth + 1
		scopes[i].ParentScopeKey = parentKey
	}

	return scopes, nil
}

// findInnermostScope returns the tightest-spanning controlFlowScope that
// contains line, or nil if no scope contains line.
func findInnermostScope(scopes []controlFlowScope, line int) *controlFlowScope {
	var best *controlFlowScope
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
