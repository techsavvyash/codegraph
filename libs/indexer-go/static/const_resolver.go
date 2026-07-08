package static

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

// constResolver resolves compile-time string constants and simple string concatenations
// within a single service repository. It exists so the event detector can turn expressions
// like `constants.EventGroupSettlement + constants.CharDot + constants.EventActionFailed`
// into the literal "settlement.failed", and `svcenv.QueueEventURL` into "queue.event.event".
//
// Constants are keyed by their BARE name (e.g. "EventActionFailed", "QueueEventURL").
// Selector expressions (`pkg.Name`) are resolved by their `.Name` only — this side-steps
// import-alias ambiguity (`svcenv` aliasing package `env`) and is safe because event/queue
// constant names are effectively unique within a service repo.
type constResolver struct {
	// values maps a bare const name to its raw declaration expression, resolved lazily.
	values map[string]ast.Expr
}

// newConstResolver walks every non-test .go file under projectPath and collects string
// const declarations. Parse failures and noisy paths are skipped silently — a partially
// populated resolver still resolves everything it did see.
func newConstResolver(projectPath string) *constResolver {
	r := &constResolver{values: make(map[string]ast.Expr)}

	_ = filepath.Walk(projectPath, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || info.IsDir() {
			return walkErr
		}
		if !strings.HasSuffix(path, ".go") || skipForConstScan(path) {
			return nil
		}
		fset := token.NewFileSet()
		astFile, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return nil
		}
		for _, decl := range astFile.Decls {
			genDecl, ok := decl.(*ast.GenDecl)
			if !ok || genDecl.Tok != token.CONST {
				continue
			}
			for _, spec := range genDecl.Specs {
				valueSpec, ok := spec.(*ast.ValueSpec)
				if !ok || len(valueSpec.Values) == 0 {
					continue
				}
				for i, name := range valueSpec.Names {
					if i >= len(valueSpec.Values) || name.Name == "_" {
						continue
					}
					// First declaration wins (idempotent across duplicate bare names).
					if _, seen := r.values[name.Name]; !seen {
						r.values[name.Name] = valueSpec.Values[i]
					}
				}
			}
		}
		return nil
	})

	return r
}

// skipForConstScan reports whether a .go file should be excluded from constant collection.
// Unlike isNoisyFilePath (tuned for the reference indexer), this deliberately KEEPS the
// env/, constants/, and migration/ directories, because queue-URL and event-name string
// constants live there (e.g. settlement/env/service.go: QueueEventURL = "queue.event.event").
// Only genuinely irrelevant sources are skipped: tests, vendored deps, and protobuf stubs.
func skipForConstScan(path string) bool {
	p := strings.ToLower(path)
	if strings.HasSuffix(p, "_test.go") || strings.HasSuffix(p, ".pb.go") {
		return true
	}
	return strings.Contains(p, "/vendor/") || strings.HasPrefix(p, "vendor/") ||
		strings.Contains(p, "/mocks/") || strings.HasPrefix(p, "mocks/")
}

// ResolveString evaluates expr to a string. `fullyStatic` is true only when every operand
// resolved to a compile-time constant. When an operand is a runtime variable/parameter, the
// resolvable static prefix is still returned (e.g. `"settlement." ` for
// `EventGroupSettlement + CharDot + <var>`) with fullyStatic=false.
func (r *constResolver) ResolveString(expr ast.Expr) (val string, fullyStatic bool) {
	return r.resolve(expr, map[string]bool{})
}

func (r *constResolver) resolve(expr ast.Expr, visited map[string]bool) (string, bool) {
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind == token.STRING {
			if unquoted, err := unquoteGo(e.Value); err == nil {
				return unquoted, true
			}
		}
		return "", false

	case *ast.Ident:
		return r.resolveName(e.Name, visited)

	case *ast.SelectorExpr:
		// pkg.Name — resolve by the selector name only.
		return r.resolveName(e.Sel.Name, visited)

	case *ast.ParenExpr:
		return r.resolve(e.X, visited)

	case *ast.BinaryExpr:
		if e.Op != token.ADD {
			return "", false
		}
		xVal, xStatic := r.resolve(e.X, visited)
		yVal, yStatic := r.resolve(e.Y, visited)
		// Keep the resolvable prefix/suffix even when one side is dynamic so callers can
		// still recover a group prefix like "settlement.".
		return xVal + yVal, xStatic && yStatic

	default:
		return "", false
	}
}

func (r *constResolver) resolveName(name string, visited map[string]bool) (string, bool) {
	if visited[name] {
		return "", false // cycle guard
	}
	expr, ok := r.values[name]
	if !ok {
		return "", false // not a known const → runtime variable/parameter
	}
	visited[name] = true
	defer delete(visited, name)
	return r.resolve(expr, visited)
}

// unquoteGo strips the surrounding quotes from a Go string literal token. Handles the common
// interpreted ("...") and raw (`...`) forms without pulling in strconv's full escaping.
func unquoteGo(lit string) (string, error) {
	if len(lit) >= 2 {
		first := lit[0]
		last := lit[len(lit)-1]
		if (first == '"' && last == '"') || (first == '`' && last == '`') {
			return lit[1 : len(lit)-1], nil
		}
	}
	return lit, nil
}

// QueueToService returns the owning service of a Tazapay queue URL. Queue names follow the
// convention `queue.<service>.<name>` (or `dlq.<service>.<name>`), so the 2nd dot-segment is
// the owning/listening service. Returns "" when the string does not match the convention.
func QueueToService(queueURL string) string {
	parts := strings.Split(queueURL, ".")
	if len(parts) < 2 {
		return ""
	}
	return parts[1]
}

// splitEvent decomposes a resolved event string into its group/action parts.
//   - "settlement.failed"      → ("settlement", "failed", false, true)
//   - "settlement." (dynamic)  → ("settlement", "",       true,  true)  // group-fallback hub
//   - "payout.auto_initiate"   → ("payout", "auto_initiate", false, true)
//   - "noDotValue"             → ("", "", false, false)                 // not an event
//
// `dynamic` is true when the action could not be resolved statically. `ok` is false when the
// value cannot form a group.action pair and should not produce an EventType node.
func splitEvent(val string, fullyStatic bool) (group, action string, dynamic, ok bool) {
	idx := strings.Index(val, ".")
	if idx < 0 {
		return "", "", false, false
	}
	group = val[:idx]
	action = val[idx+1:]
	if group == "" {
		return "", "", false, false
	}
	if action == "" || !fullyStatic {
		// Trailing dot or a runtime-var action → group-fallback hub.
		return group, "", true, true
	}
	return group, action, false, true
}
