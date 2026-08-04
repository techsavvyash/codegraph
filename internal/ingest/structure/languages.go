package structure

import (
	"path/filepath"
	"strings"
	"sync"

	tskotlin "github.com/tree-sitter-grammars/tree-sitter-kotlin/bindings/go"
	sitter "github.com/tree-sitter/go-tree-sitter"
	tsjava "github.com/tree-sitter/tree-sitter-java/bindings/go"
	tsjs "github.com/tree-sitter/tree-sitter-javascript/bindings/go"
	tsphp "github.com/tree-sitter/tree-sitter-php/bindings/go"
	tspython "github.com/tree-sitter/tree-sitter-python/bindings/go"
	tsscala "github.com/tree-sitter/tree-sitter-scala/bindings/go"
	tsts "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

// Language pairs a tree-sitter grammar with the node-kind tables the
// extractor needs. Immutable after construction; safe to share.
type Language struct {
	name string
	ts   *sitter.Language
	// functionKinds maps the grammar's function-like node kinds to
	// FunctionNode.Kind ("function" | "method" | "closure"). Kind names
	// were derived empirically from each grammar (RFC-010), not guessed.
	functionKinds map[string]string
	// declaratorKinds are the node kinds that bind an anonymous function
	// to a variable (const g = () => …); see collect's widening step.
	declaratorKinds map[string]bool
	// hasDecorators reports whether the grammar has decorator syntax
	// (`@Foo(...)`) at all. Only TypeScript/TSX set this: plain JavaScript's
	// stable grammar has no `decorator` node kind, so gating avoids a wasted
	// walk over every class/method in JS files where it can never match.
	hasDecorators bool
	// callKinds maps the grammar's call-like node kinds to the field name
	// holding the callee expression ("" = first named child, for grammars
	// without a field on that node). Used to collect callee-identifier
	// positions so the call-graph builders can tell a genuine call site from
	// a function-VALUE reference (`handler = fn` vs `fn()`) — see
	// FileStructure.IsCallSiteAt. Kind/field names derived from each grammar's
	// node-types, mirroring how functionKinds was built for RFC-010.
	callKinds map[string]string
	// hasCallDecorators marks grammars where a decorator expression is itself
	// an invocation at load time (`@foo` applies foo(target)): TS/TSX and
	// Python. Java-style annotations are NOT calls and must not set this.
	hasCallDecorators bool
}

// Name returns the registry name ("typescript", "python", …).
func (l *Language) Name() string { return l.name }

// jsDeclarators and the per-language builders are lazy: grammars allocate C
// memory, so only the languages actually used get constructed.
var jsDeclarators = map[string]bool{"variable_declarator": true, "assignment_expression": true, "pair": true, "public_field_definition": true}

var registry = map[string]func() *Language{
	"typescript": func() *Language {
		return &Language{name: "typescript", ts: sitter.NewLanguage(tsts.LanguageTypescript()),
			functionKinds: jsFunctionKinds(), declaratorKinds: jsDeclarators, hasDecorators: true,
			callKinds: jsCallKinds(), hasCallDecorators: true}
	},
	"tsx": func() *Language {
		return &Language{name: "tsx", ts: sitter.NewLanguage(tsts.LanguageTSX()),
			functionKinds: jsFunctionKinds(), declaratorKinds: jsDeclarators, hasDecorators: true,
			callKinds: jsCallKinds(), hasCallDecorators: true}
	},
	"javascript": func() *Language {
		return &Language{name: "javascript", ts: sitter.NewLanguage(tsjs.Language()),
			functionKinds: jsFunctionKinds(), declaratorKinds: jsDeclarators,
			callKinds: jsCallKinds()}
	},
	"python": func() *Language {
		return &Language{name: "python", ts: sitter.NewLanguage(tspython.Language()),
			functionKinds: map[string]string{
				"function_definition": "function",
				"lambda":              "closure",
			},
			declaratorKinds: map[string]bool{"assignment": true},
			callKinds:       map[string]string{"call": "function"},
			// A bare `@foo` decorator invokes foo(target) at definition time.
			hasCallDecorators: true}
	},
	"java": func() *Language {
		return &Language{name: "java", ts: sitter.NewLanguage(tsjava.Language()),
			functionKinds: map[string]string{
				"method_declaration":      "method",
				"constructor_declaration": "method",
				"lambda_expression":       "closure",
			},
			declaratorKinds: map[string]bool{"variable_declarator": true, "assignment_expression": true},
			callKinds: map[string]string{
				"method_invocation":          "name",
				"object_creation_expression": "type",
				"method_reference":           "", // Type::method — a deferred but real call target
			}}
	},
	"scala": func() *Language {
		return &Language{name: "scala", ts: sitter.NewLanguage(tsscala.Language()),
			functionKinds: map[string]string{
				"function_definition": "function",
				"lambda_expression":   "closure",
			},
			declaratorKinds: map[string]bool{"val_definition": true, "var_definition": true},
			callKinds:       map[string]string{"call_expression": "function"}}
	},
	"kotlin": func() *Language {
		return &Language{name: "kotlin", ts: sitter.NewLanguage(tskotlin.Language()),
			functionKinds: map[string]string{
				"function_declaration": "function",
				"anonymous_function":   "closure",
				"lambda_literal":       "closure",
			},
			declaratorKinds: map[string]bool{"property_declaration": true},
			// tree-sitter-kotlin's call_expression has no callee field; the
			// callee expression is the first named child (call_suffix is last).
			callKinds: map[string]string{"call_expression": ""}}
	},
	"php": func() *Language {
		return &Language{name: "php", ts: sitter.NewLanguage(tsphp.LanguagePHP()),
			functionKinds: map[string]string{
				"function_definition": "function",
				"method_declaration":  "method",
				"anonymous_function":  "closure",
				"arrow_function":      "closure",
			},
			declaratorKinds: map[string]bool{"assignment_expression": true},
			callKinds: map[string]string{
				"function_call_expression":   "function",
				"member_call_expression":     "name",
				"scoped_call_expression":     "name",
				"object_creation_expression": "",
			}}
	},
}

func jsFunctionKinds() map[string]string {
	return map[string]string{
		"function_declaration":           "function",
		"generator_function_declaration": "function",
		"function_expression":            "closure",
		"generator_function":             "closure",
		"arrow_function":                 "closure",
		"method_definition":              "method",
	}
}

func jsCallKinds() map[string]string {
	return map[string]string{
		"call_expression": "function",
		"new_expression":  "constructor",
	}
}

var (
	langCacheMu sync.Mutex
	langCache   = map[string]*Language{}
)

// ForLanguage returns the Language registered under name, or (nil, false).
// Instances are cached; concurrent callers share them (grammars are
// immutable, and Extract owns its parser).
func ForLanguage(name string) (*Language, bool) {
	langCacheMu.Lock()
	defer langCacheMu.Unlock()
	if l, ok := langCache[name]; ok {
		return l, true
	}
	build, ok := registry[strings.ToLower(name)]
	if !ok {
		return nil, false
	}
	l := build()
	langCache[name] = l
	return l, true
}

// extToLanguage routes file extensions to registry names. Polyglot services
// mix extensions within one file list, so grammar choice is per file, not
// per service.
var extToLanguage = map[string]string{
	".ts":    "typescript",
	".mts":   "typescript",
	".cts":   "typescript",
	".tsx":   "tsx",
	".js":    "javascript",
	".mjs":   "javascript",
	".cjs":   "javascript",
	".jsx":   "javascript",
	".py":    "python",
	".pyi":   "python",
	".java":  "java",
	".scala": "scala",
	".sc":    "scala",
	".kt":    "kotlin",
	".kts":   "kotlin",
	".php":   "php",
}

// ForFile returns the Language for a file path by extension, or
// (nil, false) when no grammar is wired for it — callers then fall back to
// the SCIP declaration line, never a guessed range (RFC-010 §8).
func ForFile(path string) (*Language, bool) {
	lang, ok := extToLanguage[strings.ToLower(filepath.Ext(path))]
	if !ok {
		return nil, false
	}
	return ForLanguage(lang)
}
