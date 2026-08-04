package resolve

import "strings"

// symbolDescriptor is the normalized (packagePath, typeName, methodName)
// view of a single SCIP symbol string, extracted by parsing the descriptor
// suffix rather than being constructed from go/types data. Building this
// index from what scip-go *actually emitted* (as opposed to synthesizing
// symbol strings from go/types package paths and hoping they match
// character-for-character) is what lets ResolveImplementations join
// resolver output onto real graph nodes.
type symbolDescriptor struct {
	pkgPath string // e.g. "github.com/context-maximiser/code-graph/internal/model"
	typ     string // e.g. "GraphStore" ("" for package- or function-level symbols)
	method  string // e.g. "UpsertNode" ("" for type-level or non-method symbols)
}

// symbolLookup maps normalized descriptors to the exact SCIP symbol string
// they came from, built once per index from the full set of known symbols.
// Two descriptor keys are indexed per method-bearing symbol: the
// (pkg, type, method) triple and, for type-level symbols, the (pkg, type)
// pair — see typeKey/methodKey.
type symbolLookup struct {
	byMethod map[symbolDescriptor]string // (pkg, type, method) -> full symbol string
	byType   map[symbolDescriptor]string // (pkg, type, "") -> full symbol string
}

func methodKey(pkgPath, typ, method string) symbolDescriptor {
	return symbolDescriptor{pkgPath: pkgPath, typ: typ, method: method}
}

func typeKey(pkgPath, typ string) symbolDescriptor {
	return symbolDescriptor{pkgPath: pkgPath, typ: typ}
}

// buildSymbolLookup parses every symbol string in knownSymbols and indexes
// the ones that look like Go type or type-method descriptors. Symbols that
// don't parse as such (packages, free functions, locals, parameters, other
// languages, malformed strings) are silently skipped — they are not
// candidates for IMPLEMENTS join targets in the first place.
func buildSymbolLookup(knownSymbols []string) *symbolLookup {
	lk := &symbolLookup{
		byMethod: make(map[symbolDescriptor]string),
		byType:   make(map[symbolDescriptor]string),
	}

	for _, sym := range knownSymbols {
		pkgPath, typ, method, ok := parseGoSymbolDescriptor(sym)
		if !ok {
			continue
		}
		if method != "" {
			lk.byMethod[methodKey(pkgPath, typ, method)] = sym
		} else if typ != "" {
			lk.byType[typeKey(pkgPath, typ)] = sym
		}
	}

	return lk
}

// parseGoSymbolDescriptor parses a scip-go symbol string of the form:
//
//	scip-go gomod <module> <version> `<pkgPath>`/Type#Method().
//	scip-go gomod <module> <version> `<pkgPath>`/Type#Method.        (interface abstract method)
//	scip-go gomod <module> <version> `<pkgPath>`/Type#
//	scip-go gomod <module> <version> `<pkgPath>`/Func().
//
// and returns (pkgPath, typeName, methodName, ok). ok is false for anything
// that isn't at least a package-qualified descriptor (locals, params, or
// non-Go/malformed symbol strings).
//
// The package path is read from the backtick-quoted segment, NOT from the
// SCIP "name" field (which is the Go module path, not the package path —
// they differ for any package below the module root). Backticks are only
// present when the path needs quoting (contains '.', '/', etc, which for Go
// import paths is effectively always), matching what scip-go emits; the
// fallback branch below handles the rare unquoted case defensively.
func parseGoSymbolDescriptor(sym string) (pkgPath, typ, method string, ok bool) {
	parts := strings.SplitN(sym, " ", 5)
	if len(parts) != 5 {
		return "", "", "", false
	}
	descriptor := parts[4]

	var pkgEnd int
	if strings.HasPrefix(descriptor, "`") {
		end := strings.Index(descriptor[1:], "`")
		if end < 0 {
			return "", "", "", false
		}
		pkgPath = descriptor[1 : 1+end]
		pkgEnd = 1 + end + 1 // past the closing backtick
	} else {
		// Unquoted package path: runs up to the last '/' before the
		// type/function descriptor begins. scip-go always backtick-quotes
		// Go import paths in practice, so this is a defensive fallback
		// rather than an observed shape.
		idx := strings.LastIndex(descriptor, "/")
		if idx < 0 {
			return "", "", "", false
		}
		pkgPath = descriptor[:idx]
		pkgEnd = idx
	}

	rest := descriptor[pkgEnd:]
	rest = strings.TrimPrefix(rest, "/")
	if rest == "" {
		return "", "", "", false // bare package symbol, e.g. `pkg`/
	}

	// rest is now one of:
	//   Type#                    (type-level)
	//   Type#Method().           (concrete method)
	//   Type#Method.             (interface abstract method / field)
	//   Func().                  (free function — no '#')
	//   Field.                   (package-level term — no '#')
	if hashIdx := strings.Index(rest, "#"); hashIdx >= 0 {
		typ = rest[:hashIdx]
		if typ == "" {
			return "", "", "", false
		}
		member := rest[hashIdx+1:]
		switch {
		case member == "":
			return pkgPath, typ, "", true // type-level: `pkg`/Type#
		case strings.HasSuffix(member, "()."):
			method = strings.TrimSuffix(member, "().")
		case strings.HasSuffix(member, "()"):
			method = strings.TrimSuffix(member, "()")
		case strings.HasSuffix(member, "."):
			// Could be an interface abstract method OR a struct field.
			// Both are legitimate join targets for method-level
			// relationships (interface method) but only the method case is
			// ever produced by the resolver as a lookup key, so recording
			// it under the same (pkg, type, name) key is safe — a field
			// with the same name as an interface method is not a
			// realistic collision the resolver could produce.
			method = strings.TrimSuffix(member, ".")
		default:
			return "", "", "", false
		}
		if method == "" {
			return "", "", "", false
		}
		return pkgPath, typ, method, true
	}

	// No '#': free function or package-level term. Not a type/method
	// descriptor, so no join key.
	return "", "", "", false
}
