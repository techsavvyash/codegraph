package static

import (
	"os"
	"path/filepath"
	"testing"

	models "github.com/context-maximiser/code-graph/internal/model"
	"github.com/sourcegraph/scip/bindings/go/scip"
)

// ===========================================================================
// 1a. convertSymbolKind — 12 table-driven cases
// ===========================================================================

func TestConvertSymbolKind(t *testing.T) {
	tests := []struct {
		name     string
		input    scip.SymbolInformation_Kind
		expected models.SymbolKind
	}{
		{"UnspecifiedKind", scip.SymbolInformation_UnspecifiedKind, models.VariableSymbol},
		{"Namespace", scip.SymbolInformation_Namespace, models.PackageSymbol},
		{"Type", scip.SymbolInformation_Type, models.TypeSymbol},
		{"Class", scip.SymbolInformation_Class, models.TypeSymbol},
		{"Interface", scip.SymbolInformation_Interface, models.InterfaceSymbol},
		{"Function", scip.SymbolInformation_Function, models.FunctionSymbol},
		{"Method", scip.SymbolInformation_Method, models.MethodSymbol},
		{"Field", scip.SymbolInformation_Field, models.FieldSymbol},
		{"Variable", scip.SymbolInformation_Variable, models.VariableSymbol},
		{"Constant", scip.SymbolInformation_Constant, models.ConstantSymbol},
		{"Parameter", scip.SymbolInformation_Parameter, models.ParameterSymbol},
		{"Unknown_999", scip.SymbolInformation_Kind(999), models.VariableSymbol},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := convertSymbolKind(tt.input)
			if got != tt.expected {
				t.Errorf("convertSymbolKind(%v) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

// ===========================================================================
// 1b. inferSymbolKind — 6 table-driven cases
// ===========================================================================

func TestInferSymbolKind(t *testing.T) {
	// Without interface-like context: every "#" is a struct, every term is a field.
	tests := []struct {
		name     string
		symbol   string
		expected models.SymbolKind
	}{
		{"Method_struct", "scip-go go pkg v1 Type#Method().", models.MethodSymbol},
		{"Function", "scip-go go pkg v1 Func().", models.FunctionSymbol},
		{"Field_on_type", "scip-go go pkg v1 Type#Field.", models.FieldSymbol},
		{"Class_unimplemented", "scip-go go pkg v1 Type#", models.TypeSymbol},
		{"Package", "scip-go go pkg v1 package/", models.PackageSymbol},
		{"Local_default", "local 0", models.VariableSymbol},
		{"Method_qualified", "scip-go go example.com/test v1 MyStruct#DoWork().", models.MethodSymbol},
		{"Package_var", "scip-go go pkg v1 PkgVar.", models.VariableSymbol},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := inferSymbolKind(tt.symbol)
			if got != tt.expected {
				t.Errorf("inferSymbolKind(%q) = %q, want %q", tt.symbol, got, tt.expected)
			}
		})
	}
}

// TestInferSymbolKindWith covers the interface-aware refinement: a type that
// something implements is an Interface; "."-terminated children of such a type
// are interface methods rather than struct fields.
func TestInferSymbolKindWith(t *testing.T) {
	ifaceType := "scip-go go pkg v1 Greeter#"
	ifaceMethod := "scip-go go pkg v1 Greeter#Greet."
	structType := "scip-go go pkg v1 EnglishGreeter#"
	structField := "scip-go go pkg v1 EnglishGreeter#Prefix."

	interfaceLike := map[string]bool{
		ifaceType:   true,
		ifaceMethod: true,
	}

	cases := []struct {
		name     string
		symbol   string
		expected models.SymbolKind
	}{
		{"Interface_when_implemented", ifaceType, models.InterfaceSymbol},
		{"Interface_method_term", ifaceMethod, models.MethodSymbol},
		{"Struct_when_not_implemented", structType, models.TypeSymbol},
		{"Field_term_on_struct", structField, models.FieldSymbol},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := inferSymbolKindWith(tc.symbol, interfaceLike)
			if got != tc.expected {
				t.Errorf("inferSymbolKindWith(%q) = %q, want %q", tc.symbol, got, tc.expected)
			}
		})
	}
}

// ===========================================================================
// 1c. extractDisplayName — 7 table-driven cases
// ===========================================================================

func TestExtractDisplayName(t *testing.T) {
	tests := []struct {
		name     string
		symbol   string
		expected string
	}{
		{"Method_via_hash", "scip-go gomod pkg v1 Type#Method()", "Method"},
		{"Method_via_hash_with_dot", "scip-go gomod pkg v1 Type#Method().", "Method"},
		{"Type_trailing_hash", "scip-go gomod pkg v1 `pkg`/Greeter#", "Greeter"},
		{"Field_via_hash_dot", "scip-go gomod pkg v1 Greeter#Greet.", "Greet"},
		{"Package_trailing_slash", "scip-go gomod pkg v1 package/subpkg/", "subpkg"},
		{"Package_with_backticks", "scip-go gomod pkg v1 `example.com/foo`/", "foo"},
		{"Package_no_trailing_slash", "scip-go gomod pkg v1 package/subpkg", "subpkg"},
		{"SimpleFunc_no_hash_or_slash", "scip-go gomod pkg v1 SimpleFunc()", "SimpleFunc"},
		{"Short_less_than_5_parts", "short", "short"},
		{"Five_parts_plain", "a b c d e", "e"},
		{"Real_SCIP_Greet", "scip-go go example.com/test v1 Greet().", "Greet"},
		{"TS_constructor_backticks", "scip-typescript npm pkg 1 src/`logger.ts`/Foo#`<constructor>`().", "<constructor>"},
		{"TS_param_top_level", "scip-typescript npm pkg 1 src/`a.ts`/greet().(logger)", "logger"},
		{"TS_param_method", "scip-typescript npm pkg 1 src/`a.ts`/Foo#bar().(message)", "message"},
		{"TS_param_constructor", "scip-typescript npm pkg 1 src/`a.ts`/Foo#`<constructor>`().(prefix)", "prefix"},
		{"TS_meta_object_property", "scip-typescript npm pkg 1 src/`a.ts`/body0:", "body0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractDisplayName(tt.symbol)
			if got != tt.expected {
				t.Errorf("extractDisplayName(%q) = %q, want %q", tt.symbol, got, tt.expected)
			}
		})
	}
}

// ===========================================================================
// 1d. convertRange — 6 table-driven cases
// ===========================================================================

// TestConvertRange locks the 0-based (SCIP) -> 1-based (graph-wide
// convention) line conversion. Every case's expectedLine is the raw SCIP
// scipRange[0]/[2] value + 1; columns pass through unchanged.
func TestConvertRange(t *testing.T) {
	tests := []struct {
		name         string
		r            []int32
		isStart      bool
		expectedLine int
		expectedCol  int
	}{
		// 3-element (single-line) ranges: [startLine, startCol, endCol].
		{"3elem_start", []int32{10, 5, 15}, true, 11, 5},
		{"3elem_end", []int32{10, 5, 15}, false, 11, 15},
		// 4-element (multi-line) ranges: [startLine, startCol, endLine, endCol].
		{"4elem_start", []int32{10, 5, 20, 30}, true, 11, 5},
		{"4elem_end", []int32{10, 5, 20, 30}, false, 21, 30},
		// SCIP's first line is 0; the converted graph-wide line must be 1, not
		// silently rejected the way calculateByteOffsets used to reject a raw
		// 0-based first line via its "startLine <= 0" guard.
		{"3elem_first_line_start", []int32{0, 0, 5}, true, 1, 0},
		{"3elem_first_line_end", []int32{0, 0, 5}, false, 1, 5},
		{"4elem_first_line_start", []int32{0, 2, 3, 8}, true, 1, 2},
		{"4elem_first_line_end", []int32{0, 2, 3, 8}, false, 4, 8},
		// Invalid ranges return the zero value untouched — no +1 applied to
		// what is explicitly "no data".
		{"2elem_too_short", []int32{1, 2}, true, 0, 0},
		{"empty", []int32{}, false, 0, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			line, col := convertRange(tt.r, tt.isStart)
			if line != tt.expectedLine || col != tt.expectedCol {
				t.Errorf("convertRange(%v, %v) = (%d, %d), want (%d, %d)",
					tt.r, tt.isStart, line, col, tt.expectedLine, tt.expectedCol)
			}
		})
	}
}

// ===========================================================================
// 1e. shouldExcludePath — 13 table-driven cases
// ===========================================================================

func TestShouldExcludePath(t *testing.T) {
	tests := []struct {
		path     string
		expected bool
	}{
		{"src/main.go", false},
		{"node_modules/express/index.js", true},
		{"vendor/github.com/pkg/errors/errors.go", true},
		{".git/config", true},
		{".next/cache/build.js", true},
		{".nuxt/dist/app.js", true},
		{"dist/bundle.js", true},
		{"build/output.js", true},
		{"target/classes/Main.class", true},
		{"venv/lib/site.py", true},
		{".venv/lib/site.py", true},
		{"__pycache__/mod.pyc", true},
		{"pkg/handler.go", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := shouldExcludePath(tt.path)
			if got != tt.expected {
				t.Errorf("shouldExcludePath(%q) = %v, want %v", tt.path, got, tt.expected)
			}
		})
	}
}

// ===========================================================================
// 1f. inferLanguage — 16 table-driven cases
// ===========================================================================

func TestInferLanguage(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		{"main.go", "Go"},
		{"app.ts", "TypeScript"},
		{"component.tsx", "TypeScript"},
		{"script.js", "JavaScript"},
		{"component.jsx", "JavaScript"},
		{"module.py", "Python"},
		{"Main.java", "Java"},
		{"App.scala", "Scala"},
		{"App.kt", "Kotlin"},
		{"App.kts", "Kotlin"},
		{"lib.rs", "Rust"},
		{"app.rb", "Ruby"},
		{"index.php", "PHP"},
		{"main.c", "C"},
		{"main.cpp", "C++"},
		{"Program.cs", "C#"},
		{"unknown.xyz", "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := inferLanguage(tt.path)
			if got != tt.expected {
				t.Errorf("inferLanguage(%q) = %q, want %q", tt.path, got, tt.expected)
			}
		})
	}
}

// ===========================================================================
// 1g. helpers.go — getStringFromMap and getInt64FromMap
// ===========================================================================

func TestGetStringFromMap(t *testing.T) {
	tests := []struct {
		name     string
		m        map[string]any
		key      string
		expected string
	}{
		{"found", map[string]any{"k": "hello"}, "k", "hello"},
		{"non_string", map[string]any{"k": 42}, "k", ""},
		{"missing", map[string]any{"other": "val"}, "k", ""},
		{"nil_value", map[string]any{"k": nil}, "k", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getStringFromMap(tt.m, tt.key)
			if got != tt.expected {
				t.Errorf("getStringFromMap(%v, %q) = %q, want %q", tt.m, tt.key, got, tt.expected)
			}
		})
	}
}

func TestGetInt64FromMap(t *testing.T) {
	tests := []struct {
		name     string
		m        map[string]any
		key      string
		expected int64
	}{
		{"int64", map[string]any{"k": int64(100)}, "k", 100},
		{"int", map[string]any{"k": 42}, "k", 42},
		{"float64", map[string]any{"k": float64(3.14)}, "k", 3},
		{"non_numeric", map[string]any{"k": "str"}, "k", -1},
		{"missing", map[string]any{}, "k", -1},
		{"nil_value", map[string]any{"k": nil}, "k", -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getInt64FromMap(tt.m, tt.key)
			if got != tt.expected {
				t.Errorf("getInt64FromMap(%v, %q) = %d, want %d", tt.m, tt.key, got, tt.expected)
			}
		})
	}
}

// ===========================================================================
// 2a. extractGoImports
// ===========================================================================

func TestExtractGoImports(t *testing.T) {
	tests := []struct {
		name     string
		source   string
		expected []struct {
			pkg        string
			isExternal bool
		}
	}{
		{
			"single_import",
			`import "fmt"`,
			[]struct {
				pkg        string
				isExternal bool
			}{
				{"fmt", true}, // "fmt" has no "/" → isExternal = !contains("/") = true
			},
		},
		{
			"import_block",
			"import (\n\t\"fmt\"\n\t\"github.com/pkg/errors\"\n)",
			[]struct {
				pkg        string
				isExternal bool
			}{
				{"fmt", false},                  // block import: isExternal = contains("/") = false
				{"github.com/pkg/errors", true}, // block import: isExternal = contains("/") = true
			},
		},
		{
			"blank_import",
			`import _ "net/http/pprof"`,
			// The regex `import\s+"([^"]+)"` won't match `import _ "..."` because of the `_` space
			// Actually let's check: `import _ "net/http/pprof"` — the regex is `import\s+"`, so
			// there's `_ ` between import and the quote. Let's verify behavior.
			nil, // blank import with _ won't match single import regex
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			imports := extractGoImports("test.go", tt.source)
			if tt.expected == nil {
				if len(imports) != 0 {
					t.Errorf("expected no imports, got %d", len(imports))
				}
				return
			}
			if len(imports) != len(tt.expected) {
				t.Fatalf("expected %d imports, got %d: %+v", len(tt.expected), len(imports), imports)
			}
			for i, exp := range tt.expected {
				if imports[i].TargetPackage != exp.pkg {
					t.Errorf("import[%d] package = %q, want %q", i, imports[i].TargetPackage, exp.pkg)
				}
				if imports[i].IsExternal != exp.isExternal {
					t.Errorf("import[%d] isExternal = %v, want %v", i, imports[i].IsExternal, exp.isExternal)
				}
			}
		})
	}
}

// ===========================================================================
// 2b. extractTypeScriptImports
// ===========================================================================

func TestExtractTypeScriptImports(t *testing.T) {
	tests := []struct {
		name   string
		source string
		check  func(t *testing.T, imports []*models.PackageImport)
	}{
		{
			"named_import",
			`import { useState } from 'react'`,
			func(t *testing.T, imports []*models.PackageImport) {
				if len(imports) == 0 {
					t.Fatal("expected at least 1 import")
				}
				found := false
				for _, imp := range imports {
					if imp.TargetPackage == "react" {
						found = true
						if !imp.IsExternal {
							t.Error("expected react to be external")
						}
					}
				}
				if !found {
					t.Error("expected to find react import")
				}
			},
		},
		{
			"namespace_import",
			`import * as React from 'react'`,
			func(t *testing.T, imports []*models.PackageImport) {
				if len(imports) == 0 {
					t.Fatal("expected at least 1 import")
				}
				found := false
				for _, imp := range imports {
					if imp.TargetPackage == "react" && len(imp.ImportedNames) > 0 && imp.ImportedNames[0] == "React" {
						found = true
					}
				}
				if !found {
					t.Error("expected namespace import of React from react")
				}
			},
		},
		{
			"default_import",
			`import App from './App'`,
			func(t *testing.T, imports []*models.PackageImport) {
				if len(imports) == 0 {
					t.Fatal("expected at least 1 import")
				}
				found := false
				for _, imp := range imports {
					if imp.TargetPackage == "./App" {
						found = true
						if imp.IsExternal {
							t.Error("expected ./App to be local (not external)")
						}
					}
				}
				if !found {
					t.Error("expected to find ./App import")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			imports := extractTypeScriptImports("test.ts", tt.source)
			tt.check(t, imports)
		})
	}
}

// ===========================================================================
// 2c. extractPythonImports
// ===========================================================================

func TestExtractPythonImports(t *testing.T) {
	tests := []struct {
		name   string
		source string
		check  func(t *testing.T, imports []*models.PackageImport)
	}{
		{
			"simple_import",
			"import os",
			func(t *testing.T, imports []*models.PackageImport) {
				if len(imports) != 1 {
					t.Fatalf("expected 1 import, got %d", len(imports))
				}
				if imports[0].TargetPackage != "os" {
					t.Errorf("expected package 'os', got %q", imports[0].TargetPackage)
				}
				if !imports[0].IsExternal {
					t.Error("expected os to be external")
				}
			},
		},
		{
			"from_import",
			"from flask import Flask, jsonify",
			func(t *testing.T, imports []*models.PackageImport) {
				if len(imports) != 1 {
					t.Fatalf("expected 1 import, got %d", len(imports))
				}
				if imports[0].TargetPackage != "flask" {
					t.Errorf("expected package 'flask', got %q", imports[0].TargetPackage)
				}
				if len(imports[0].ImportedNames) < 2 {
					t.Fatalf("expected at least 2 imported names, got %d", len(imports[0].ImportedNames))
				}
				if imports[0].ImportedNames[0] != "Flask" {
					t.Errorf("expected first name 'Flask', got %q", imports[0].ImportedNames[0])
				}
				if imports[0].ImportedNames[1] != "jsonify" {
					t.Errorf("expected second name 'jsonify', got %q", imports[0].ImportedNames[1])
				}
			},
		},
		{
			"dotted_import",
			"import os.path",
			func(t *testing.T, imports []*models.PackageImport) {
				if len(imports) != 1 {
					t.Fatalf("expected 1 import, got %d", len(imports))
				}
				if imports[0].TargetPackage != "os.path" {
					t.Errorf("expected 'os.path', got %q", imports[0].TargetPackage)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			imports := extractPythonImports("test.py", tt.source)
			tt.check(t, imports)
		})
	}
}

// ===========================================================================
// 2d. extractJavaImports
// ===========================================================================

func TestExtractJavaImports(t *testing.T) {
	tests := []struct {
		name   string
		source string
		check  func(t *testing.T, imports []*models.PackageImport)
	}{
		{
			"stdlib_import",
			"import java.util.List;",
			func(t *testing.T, imports []*models.PackageImport) {
				if len(imports) != 1 {
					t.Fatalf("expected 1 import, got %d", len(imports))
				}
				if imports[0].TargetPackage != "java.util.List" {
					t.Errorf("expected 'java.util.List', got %q", imports[0].TargetPackage)
				}
				if !imports[0].IsExternal {
					t.Error("expected java import to be external")
				}
			},
		},
		{
			"custom_import",
			"import com.example.service.UserService;",
			func(t *testing.T, imports []*models.PackageImport) {
				if len(imports) != 1 {
					t.Fatalf("expected 1 import, got %d", len(imports))
				}
				if imports[0].TargetPackage != "com.example.service.UserService" {
					t.Errorf("expected 'com.example.service.UserService', got %q", imports[0].TargetPackage)
				}
			},
		},
		{
			"multiple_imports",
			"import java.util.List;\nimport java.util.Map;",
			func(t *testing.T, imports []*models.PackageImport) {
				if len(imports) != 2 {
					t.Fatalf("expected 2 imports, got %d", len(imports))
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			imports := extractJavaImports("Test.java", tt.source)
			tt.check(t, imports)
		})
	}
}

// ===========================================================================
// Part 3: ExtractSymbols / ExtractDocuments / GetServiceInfo with SCIP fixture
// ===========================================================================

func loadFixtureParser(t *testing.T) *SCIPParser {
	t.Helper()
	fixturePath := filepath.Join("testdata", "tiny_project.scip")
	if _, err := os.Stat(fixturePath); os.IsNotExist(err) {
		t.Skipf("SCIP fixture not found at %s (run testdata/generate_fixture.sh to create)", fixturePath)
	}
	parser := NewSCIPParser()
	if err := parser.ParseFile(fixturePath); err != nil {
		t.Fatalf("failed to parse fixture: %v", err)
	}
	return parser
}

func TestExtractSymbols_Fixture(t *testing.T) {
	parser := loadFixtureParser(t)

	symbols, err := parser.ExtractSymbols("")
	if err != nil {
		t.Fatalf("ExtractSymbols failed: %v", err)
	}

	if len(symbols) < 3 {
		t.Fatalf("expected at least 3 symbols, got %d", len(symbols))
	}

	// Look for Greet function symbol.
	var greetSymbol *models.SymbolDefinition
	var mainFuncSymbol *models.SymbolDefinition
	for _, sym := range symbols {
		if sym.Info != nil {
			if sym.Info.DisplayName == "Greet" || sym.Info.DisplayName == "Greet()" || sym.Info.DisplayName == "Greet()." {
				greetSymbol = sym
			}
			if sym.Info.DisplayName == "main" || sym.Info.DisplayName == "main()" || sym.Info.DisplayName == "main()." {
				if sym.Info.Kind == models.FunctionSymbol || sym.Info.Kind == models.MethodSymbol {
					mainFuncSymbol = sym
				}
			}
		}
	}

	if greetSymbol == nil {
		// Dump symbols for debugging
		for _, sym := range symbols {
			t.Logf("  symbol: kind=%s display=%q file=%s", sym.Info.Kind, sym.Info.DisplayName, sym.Info.FilePath)
		}
		t.Fatal("expected to find Greet symbol")
	}

	if greetSymbol.Info.Kind != models.FunctionSymbol {
		t.Errorf("Greet symbol kind = %q, want %q", greetSymbol.Info.Kind, models.FunctionSymbol)
	}
	if greetSymbol.Info.FilePath != "lib.go" {
		t.Errorf("Greet symbol file = %q, want 'lib.go'", greetSymbol.Info.FilePath)
	}

	// At least 1 reference.
	if len(greetSymbol.Refs) == 0 {
		t.Error("Greet symbol has no references")
	}

	// Check for a definition reference.
	defRef := greetSymbol.GetDefinitionReference()
	if defRef == nil {
		t.Error("Greet has no definition reference")
	}

	if mainFuncSymbol == nil {
		t.Log("main function symbol not found (may be inlined by scip-go)")
	} else if mainFuncSymbol.Info.FilePath != "main.go" {
		t.Errorf("main func file = %q, want 'main.go'", mainFuncSymbol.Info.FilePath)
	}

	// No symbols from excluded paths.
	for _, sym := range symbols {
		if sym.Info != nil && shouldExcludePath(sym.Info.FilePath) {
			t.Errorf("found symbol from excluded path: %s", sym.Info.FilePath)
		}
	}
}

func TestExtractDocuments_Fixture(t *testing.T) {
	parser := loadFixtureParser(t)

	docs, err := parser.ExtractDocuments()
	if err != nil {
		t.Fatalf("ExtractDocuments failed: %v", err)
	}

	if len(docs) != 2 {
		t.Fatalf("expected 2 documents, got %d", len(docs))
	}

	paths := map[string]bool{}
	for _, doc := range docs {
		paths[doc.Path] = true
		if doc.Language != "Go" {
			t.Errorf("document %s language = %q, want 'Go'", doc.Path, doc.Language)
		}
	}

	if !paths["lib.go"] {
		t.Error("expected lib.go in documents")
	}
	if !paths["main.go"] {
		t.Error("expected main.go in documents")
	}
}

func TestGetServiceInfo_Fixture(t *testing.T) {
	parser := loadFixtureParser(t)

	svc, err := parser.GetServiceInfo()
	if err != nil {
		t.Fatalf("GetServiceInfo failed: %v", err)
	}

	if svc == nil {
		t.Fatal("expected non-nil service")
	}

	if svc.Language != "Go" {
		t.Errorf("service language = %q, want 'Go'", svc.Language)
	}
}

// ===========================================================================
// Edge cases for SCIPParser methods
// ===========================================================================

func TestExtractSymbols_NoIndexLoaded(t *testing.T) {
	parser := NewSCIPParser()
	_, err := parser.ExtractSymbols("")
	if err == nil {
		t.Error("expected error when no index loaded")
	}
}

func TestExtractDocuments_NoIndexLoaded(t *testing.T) {
	parser := NewSCIPParser()
	_, err := parser.ExtractDocuments()
	if err == nil {
		t.Error("expected error when no index loaded")
	}
}

func TestGetMetadata_NoIndexLoaded(t *testing.T) {
	parser := NewSCIPParser()
	if parser.GetMetadata() != nil {
		t.Error("expected nil metadata when no index loaded")
	}
}

func TestGetServiceInfo_NoIndexLoaded(t *testing.T) {
	parser := NewSCIPParser()
	_, err := parser.GetServiceInfo()
	if err == nil {
		t.Error("expected error when no index loaded")
	}
}
