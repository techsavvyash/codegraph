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
}

// Name returns the registry name ("typescript", "python", …).
func (l *Language) Name() string { return l.name }

// jsDeclarators and the per-language builders are lazy: grammars allocate C
// memory, so only the languages actually used get constructed.
var jsDeclarators = map[string]bool{"variable_declarator": true, "assignment_expression": true, "pair": true, "public_field_definition": true}

var registry = map[string]func() *Language{
	"typescript": func() *Language {
		return &Language{name: "typescript", ts: sitter.NewLanguage(tsts.LanguageTypescript()),
			functionKinds: jsFunctionKinds(), declaratorKinds: jsDeclarators, hasDecorators: true}
	},
	"tsx": func() *Language {
		return &Language{name: "tsx", ts: sitter.NewLanguage(tsts.LanguageTSX()),
			functionKinds: jsFunctionKinds(), declaratorKinds: jsDeclarators, hasDecorators: true}
	},
	"javascript": func() *Language {
		return &Language{name: "javascript", ts: sitter.NewLanguage(tsjs.Language()),
			functionKinds: jsFunctionKinds(), declaratorKinds: jsDeclarators}
	},
	"python": func() *Language {
		return &Language{name: "python", ts: sitter.NewLanguage(tspython.Language()),
			functionKinds: map[string]string{
				"function_definition": "function",
				"lambda":              "closure",
			},
			declaratorKinds: map[string]bool{"assignment": true}}
	},
	"java": func() *Language {
		return &Language{name: "java", ts: sitter.NewLanguage(tsjava.Language()),
			functionKinds: map[string]string{
				"method_declaration":      "method",
				"constructor_declaration": "method",
				"lambda_expression":       "closure",
			},
			declaratorKinds: map[string]bool{"variable_declarator": true, "assignment_expression": true}}
	},
	"scala": func() *Language {
		return &Language{name: "scala", ts: sitter.NewLanguage(tsscala.Language()),
			functionKinds: map[string]string{
				"function_definition": "function",
				"lambda_expression":   "closure",
			},
			declaratorKinds: map[string]bool{"val_definition": true, "var_definition": true}}
	},
	"kotlin": func() *Language {
		return &Language{name: "kotlin", ts: sitter.NewLanguage(tskotlin.Language()),
			functionKinds: map[string]string{
				"function_declaration": "function",
				"anonymous_function":   "closure",
				"lambda_literal":       "closure",
			},
			declaratorKinds: map[string]bool{"property_declaration": true}}
	},
	"php": func() *Language {
		return &Language{name: "php", ts: sitter.NewLanguage(tsphp.LanguagePHP()),
			functionKinds: map[string]string{
				"function_definition": "function",
				"method_declaration":  "method",
				"anonymous_function":  "closure",
				"arrow_function":      "closure",
			},
			declaratorKinds: map[string]bool{"assignment_expression": true}}
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
