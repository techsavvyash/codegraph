package static

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseFuncRanges(t *testing.T) {
	dir := t.TempDir()
	src := `package example

func TopLevel() {
	fmt.Println("hello")
}

type Foo struct{}

func (f *Foo) Method() {
	fmt.Println("method")
}

func init() {
	setup()
}
`
	tmpFile := filepath.Join(dir, "example.go")
	if err := os.WriteFile(tmpFile, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}

	ranges, err := parseFuncRanges(tmpFile)
	if err != nil {
		t.Fatal(err)
	}

	if len(ranges) != 3 {
		t.Fatalf("expected 3 func ranges, got %d", len(ranges))
	}

	// Check TopLevel
	if ranges[0].Name != "TopLevel" {
		t.Errorf("expected TopLevel, got %s", ranges[0].Name)
	}
	if ranges[0].DeclLine != 3 {
		t.Errorf("TopLevel DeclLine: got %d, want 3", ranges[0].DeclLine)
	}
	if ranges[0].StartLine != 3 || ranges[0].EndLine != 5 {
		t.Errorf("TopLevel range: got %d-%d, want 3-5", ranges[0].StartLine, ranges[0].EndLine)
	}

	// Check Method with receiver
	if ranges[1].Name != "Foo.Method" {
		t.Errorf("expected Foo.Method, got %s", ranges[1].Name)
	}

	// Check init
	if ranges[2].Name != "init" {
		t.Errorf("expected init, got %s", ranges[2].Name)
	}
}

func TestFindEnclosingFunc(t *testing.T) {
	ranges := []funcRange{
		{Name: "outer", StartLine: 5, EndLine: 20},
		{Name: "inner", StartLine: 10, EndLine: 15},
		{Name: "other", StartLine: 25, EndLine: 30},
	}

	tests := []struct {
		line int
		want string
	}{
		{1, ""},       // before any function
		{5, "outer"},  // at outer start
		{8, "outer"},  // inside outer, before inner
		{10, "inner"}, // at inner start (inner is narrower)
		{12, "inner"}, // inside inner
		{15, "inner"}, // at inner end
		{18, "outer"}, // inside outer, after inner
		{20, "outer"}, // at outer end
		{22, ""},      // between functions
		{27, "other"}, // inside other
		{35, ""},      // after all functions
	}

	for _, tc := range tests {
		got := findEnclosingFunc(ranges, tc.line)
		name := ""
		if got != nil {
			name = got.Name
		}
		if name != tc.want {
			t.Errorf("line %d: got %q, want %q", tc.line, name, tc.want)
		}
	}
}

func TestExprName(t *testing.T) {
	// This is a unit test for the exprName helper, but since it works on
	// ast.Expr which requires constructing AST nodes, we test it indirectly
	// via parseFuncRanges (the Foo.Method test above covers pointer receivers).
	// Here we just test the isGoFile helper.
	if !isGoFile("main.go") {
		t.Error("expected main.go to be a Go file")
	}
	if isGoFile("main.ts") {
		t.Error("expected main.ts to NOT be a Go file")
	}
	if isGoFile("") {
		t.Error("expected empty string to NOT be a Go file")
	}
}

func TestReadModulePath(t *testing.T) {
	dir := t.TempDir()
	goMod := filepath.Join(dir, "go.mod")
	content := "module github.com/example/myproject\n\ngo 1.24\n"
	if err := os.WriteFile(goMod, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	got := readModulePath(dir)
	want := "github.com/example/myproject"
	if got != want {
		t.Errorf("readModulePath() = %q, want %q", got, want)
	}
}

func TestReadModulePathMissing(t *testing.T) {
	dir := t.TempDir()
	got := readModulePath(dir)
	if got != "" {
		t.Errorf("readModulePath() = %q, want empty string for missing go.mod", got)
	}
}

func TestReadModulePathMalformed(t *testing.T) {
	dir := t.TempDir()
	goMod := filepath.Join(dir, "go.mod")
	if err := os.WriteFile(goMod, []byte("not a valid go.mod\n"), 0644); err != nil {
		t.Fatal(err)
	}

	got := readModulePath(dir)
	if got != "" {
		t.Errorf("readModulePath() = %q, want empty string for malformed go.mod", got)
	}
}

func TestIsExternalSymbol(t *testing.T) {
	cg := &SCIPCallGraphBuilder{modulePath: "github.com/tazapay/account"}

	tests := []struct {
		name   string
		symbol string
		want   bool
	}{
		{
			name:   "proto getter from external proto module",
			symbol: "scip-go gomod github.com/tazapay/proto v1.6.83 `github.com/tazapay/proto/gen/go/account/grpc/v1`/ForgotPasswordRequest#GetEmail().",
			want:   true,
		},
		{
			name:   "in-module method with same bare name",
			symbol: "scip-go gomod github.com/tazapay/account v1.0.0 `github.com/tazapay/account/utils`/SubmitEntityFields#GetEmail().",
			want:   false,
		},
		{
			name:   "in-module free function",
			symbol: "scip-go gomod github.com/tazapay/account v1.0.0 `github.com/tazapay/account/service/grpc/v1`/userErrorValidation().",
			want:   false,
		},
		{
			name:   "stdlib symbol",
			symbol: "scip-go gomod std v1.24 `strings`/ToLower().",
			want:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := cg.isExternalSymbol(tc.symbol); got != tc.want {
				t.Errorf("isExternalSymbol(%q) = %v, want %v", tc.symbol, got, tc.want)
			}
		})
	}
}

// TestIsExternalSymbolUnknownModule guards the empty-go.mod case: with no module
// path we cannot judge, so nothing is classified external (falls back to normal
// resolution rather than filtering everything).
func TestIsExternalSymbolUnknownModule(t *testing.T) {
	cg := &SCIPCallGraphBuilder{modulePath: ""}
	sym := "scip-go gomod github.com/tazapay/proto v1.6.83 `x`/ForgotPasswordRequest#GetEmail()."
	if cg.isExternalSymbol(sym) {
		t.Error("with empty modulePath, isExternalSymbol should report false")
	}
}

func TestIsPackageQualifiedCall(t *testing.T) {
	importNames := map[string]bool{"cache": true, "repository": true}

	tests := []struct {
		name  string
		chain []string
		want  bool
	}{
		{
			name:  "package-qualified method on global var (cache.PSPErrorCache.Get)",
			chain: []string{"cache", "PSPErrorCache", "Get"},
			want:  true,
		},
		{
			name:  "call on local variable/param (req.GetEmail)",
			chain: []string{"req", "GetEmail"},
			want:  false,
		},
		{
			name:  "call-valued root is not a package (getCache().Get)",
			chain: []string{"getCache()", "Get"},
			want:  false,
		},
		{
			name:  "empty chain",
			chain: nil,
			want:  false,
		},
		{
			name:  "single-element chain (bare free function)",
			chain: []string{"Helper"},
			want:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cm := callMeta{ReceiverChain: tc.chain}
			if got := isPackageQualifiedCall(cm, importNames); got != tc.want {
				t.Errorf("isPackageQualifiedCall(%v) = %v, want %v", tc.chain, got, tc.want)
			}
		})
	}
}

// TestIsPackageQualifiedCallNoImports guards the empty-import-set case: with no
// known imports we cannot classify a root as a package, so nothing is treated as
// package-qualified (the plausibility check keeps its original behaviour).
func TestIsPackageQualifiedCallNoImports(t *testing.T) {
	cm := callMeta{ReceiverChain: []string{"cache", "PSPErrorCache", "Get"}}
	if isPackageQualifiedCall(cm, nil) {
		t.Error("with no imports, isPackageQualifiedCall should report false")
	}
}

func TestFileImportNames(t *testing.T) {
	dir := t.TempDir()
	src := `package p

import (
	"context"
	"github.com/tazapay/settlement-orchestration/service/cache"
	svcresponse "github.com/tazapay/settlement-orchestration/response"
	_ "embed"
)

func f(ctx context.Context) {}
`
	tmpFile := filepath.Join(dir, "f.go")
	if err := os.WriteFile(tmpFile, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}

	names := fileImportNames(tmpFile)
	if !names["context"] {
		t.Error("expected 'context' import name")
	}
	if !names["cache"] {
		t.Error("expected 'cache' (last path segment) import name")
	}
	if !names["svcresponse"] {
		t.Error("expected 'svcresponse' alias import name")
	}
	if names["_"] {
		t.Error("blank import '_' must be excluded")
	}
}

func TestParseFuncRangesInvalidFile(t *testing.T) {
	_, err := parseFuncRanges("/nonexistent/path.go")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestParseFuncRangesInvalidGo(t *testing.T) {
	dir := t.TempDir()
	tmpFile := filepath.Join(dir, "bad.go")
	if err := os.WriteFile(tmpFile, []byte("not valid go"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := parseFuncRanges(tmpFile)
	if err == nil {
		t.Error("expected error for invalid Go file")
	}
}
