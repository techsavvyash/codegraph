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
