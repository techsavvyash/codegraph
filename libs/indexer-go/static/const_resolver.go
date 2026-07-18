package static

import (
	"fmt"
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

	// P3-2 collision guard. Bare-name, first-wins resolution across a whole repo is a
	// latent correctness risk for generic const names (`Failed`, `Created`): if two
	// packages declare the same name with DIFFERENT string values, silently picking one
	// can attribute the wrong event/queue. These maps let us detect that and degrade the
	// name to "not static" (like a runtime var) instead of guessing.
	//   valueFile — bare name → file path of the first declaration (for WARN reporting)
	//   litValue  — bare name → the first declaration's string-literal value, when it was one
	//   ambiguous — bare name → true once a divergent string-literal value is seen
	// These are only READ in the resolve path, so a hand-built resolver with nil maps
	// (as in tests) is safe — nil-map reads return the zero value.
	valueFile map[string]string
	litValue  map[string]string
	ambiguous map[string]bool
}

// newConstResolver walks every non-test .go file under projectPath and collects string
// const declarations. Parse failures and noisy paths are skipped silently — a partially
// populated resolver still resolves everything it did see.
func newConstResolver(projectPath string) *constResolver {
	r := &constResolver{
		values:    make(map[string]ast.Expr),
		valueFile: make(map[string]string),
		litValue:  make(map[string]string),
		ambiguous: make(map[string]bool),
	}

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
					expr := valueSpec.Values[i]
					if _, seen := r.values[name.Name]; !seen {
						// First declaration wins (idempotent across duplicate bare names).
						r.values[name.Name] = expr
						r.valueFile[name.Name] = path
						if lit, ok := stringLitValue(expr); ok {
							r.litValue[name.Name] = lit
						}
						continue
					}
					// P3-2: duplicate bare name. Flag only string-literal-vs-string-literal
					// divergence — the concrete correctness risk (`Failed`/`Created` with
					// different values in different packages). Non-literal duplicates keep
					// first-wins to avoid false ambiguity on composed/aliased consts.
					newLit, newOK := stringLitValue(expr)
					oldLit, oldOK := r.litValue[name.Name]
					if newOK && oldOK && newLit != oldLit && !r.ambiguous[name.Name] {
						r.ambiguous[name.Name] = true
						fmt.Printf("Warning: const %q resolves to divergent values across the repo "+
							"(%q in %s vs %q in %s) — marking ambiguous (resolve → not static)\n",
							name.Name, oldLit, r.valueFile[name.Name], newLit, path)
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
	return r.resolve(expr, map[string]bool{}, nil)
}

// ResolveStringWithBindings is like ResolveString but treats each name in `bindings` as if it
// were a string const with the given value. This lets the emission resolver recover the concrete
// action of a `<group> + CharDot + <switchTag>` expression by binding the switch tag to each
// case-label constant (e.g. binding `payoutStatus` → "screening" yields "payout.screening").
// Bindings take precedence over collected const declarations.
func (r *constResolver) ResolveStringWithBindings(expr ast.Expr, bindings map[string]string) (val string, fullyStatic bool) {
	return r.resolve(expr, map[string]bool{}, bindings)
}

func (r *constResolver) resolve(expr ast.Expr, visited map[string]bool, bindings map[string]string) (string, bool) {
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind == token.STRING {
			if unquoted, err := unquoteGo(e.Value); err == nil {
				return unquoted, true
			}
		}
		return "", false

	case *ast.Ident:
		return r.resolveName(e.Name, visited, bindings)

	case *ast.SelectorExpr:
		// pkg.Name — resolve by the selector name only.
		return r.resolveName(e.Sel.Name, visited, bindings)

	case *ast.ParenExpr:
		return r.resolve(e.X, visited, bindings)

	case *ast.BinaryExpr:
		if e.Op != token.ADD {
			return "", false
		}
		xVal, xStatic := r.resolve(e.X, visited, bindings)
		yVal, yStatic := r.resolve(e.Y, visited, bindings)
		// Keep the resolvable prefix/suffix even when one side is dynamic so callers can
		// still recover a group prefix like "settlement.".
		return xVal + yVal, xStatic && yStatic

	default:
		return "", false
	}
}

func (r *constResolver) resolveName(name string, visited map[string]bool, bindings map[string]string) (string, bool) {
	if v, ok := bindings[name]; ok {
		return v, true // caller-supplied binding (e.g. switch tag → case-label value)
	}
	if r.ambiguous[name] {
		// P3-2: same bare name declared with divergent string values across the repo.
		// Treat as a runtime variable rather than silently picking one — downstream this
		// degrades to a group.* fallback hub instead of a wrong concrete event/queue.
		return "", false
	}
	if visited[name] {
		return "", false // cycle guard
	}
	expr, ok := r.values[name]
	if !ok {
		return "", false // not a known const → runtime variable/parameter
	}
	visited[name] = true
	defer delete(visited, name)
	return r.resolve(expr, visited, bindings)
}

// AmbiguousCount returns how many bare const names were flagged as ambiguous by the
// P3-2 collision guard (divergent string-literal values across the repo). Used for
// the per-run coverage telemetry (P3-1).
func (r *constResolver) AmbiguousCount() int {
	return len(r.ambiguous)
}

// stringLitValue returns the unquoted value of expr when it is a string literal.
func stringLitValue(expr ast.Expr) (string, bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	v, _ := unquoteGo(lit.Value)
	return v, true
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
//
// P3-3: the input is validated against `^(queue|dlq)\.[a-z0-9-]+\.` so that garbage values —
// hard-coded `https://sqs...amazonaws.com/...` URLs or env-key strings like `queue.event_url`
// (only two segments) — yield "" rather than a nonsense service segment leaking into the graph.
func QueueToService(queueURL string) string {
	parts := strings.Split(queueURL, ".")
	// Need at least three segments: <prefix>.<service>.<name>.
	if len(parts) < 3 {
		return ""
	}
	if parts[0] != "queue" && parts[0] != "dlq" {
		return ""
	}
	if !isQueueServiceSegment(parts[1]) {
		return ""
	}
	return parts[1]
}

// isQueueServiceSegment reports whether s is a valid queue service segment: non-empty and
// composed only of lowercase letters, digits, or hyphens (the `[a-z0-9-]+` of the convention).
func isQueueServiceSegment(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return false
		}
	}
	return true
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
