package static

import (
	"os"
	"path/filepath"
	"testing"
)

// TestExtractGoModulePath covers the real-world go.mod shapes we ship with
// this monorepo, plus edge cases that a naive line-prefix parser would
// mishandle (commented lines, quoted module values, indirect-block
// "module require" mentions, missing files).
//
// Why this matters: indexPackageDependencies matches imp.TargetPackage
// against Service.packageName. If extractGoModulePath misreads the path
// (or returns ""), DEPENDS_ON edges silently disappear.
func TestExtractGoModulePath(t *testing.T) {
	cases := []struct {
		name     string
		gomod    string
		expected string
	}{
		{
			name:     "standard module declaration",
			gomod:    "module github.com/context-maximiser/code-graph/libs/indexer-go\n\ngo 1.24\n",
			expected: "github.com/context-maximiser/code-graph/libs/indexer-go",
		},
		{
			name:     "module with trailing inline comment",
			gomod:    "module github.com/example/foo // some note\n\ngo 1.24\n",
			expected: "github.com/example/foo",
		},
		{
			name:     "quoted module path",
			gomod:    "module \"github.com/example/quoted\"\n\ngo 1.24\n",
			expected: "github.com/example/quoted",
		},
		{
			name:     "leading whitespace and blank lines",
			gomod:    "\n  \nmodule github.com/example/whitespaced\n",
			expected: "github.com/example/whitespaced",
		},
		{
			name: "module appears only inside require block (must not match)",
			gomod: `go 1.24

require (
	module-look-alike v1.0.0
)
`,
			expected: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(tc.gomod), 0o644); err != nil {
				t.Fatalf("write go.mod: %v", err)
			}
			got := extractGoModulePath(dir)
			if got != tc.expected {
				t.Errorf("extractGoModulePath: expected %q, got %q", tc.expected, got)
			}
		})
	}
}

func TestExtractGoModulePath_MissingFile(t *testing.T) {
	if got := extractGoModulePath(t.TempDir()); got != "" {
		t.Errorf("expected empty string for missing go.mod, got %q", got)
	}
}

// TestExtractPythonPackageName checks that we read the [project].name field
// from pyproject.toml and ignore name= keys in other tables (e.g. [tool.poetry]
// or build-system metadata) which would otherwise produce silent mismatches.
func TestExtractPythonPackageName(t *testing.T) {
	cases := []struct {
		name      string
		pyproject string
		expected  string
	}{
		{
			name: "project table with double-quoted name",
			pyproject: `[project]
name = "docs-intel"
version = "0.1.0"
`,
			expected: "docs-intel",
		},
		{
			name: "project table with single-quoted name",
			pyproject: `[project]
name = 'my-pkg'
`,
			expected: "my-pkg",
		},
		{
			name: "name in unrelated table must be ignored",
			pyproject: `[tool.poetry]
name = "wrong-name"

[project]
name = "right-name"
`,
			expected: "right-name",
		},
		{
			name: "no project table at all",
			pyproject: `[build-system]
requires = ["setuptools"]
`,
			expected: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte(tc.pyproject), 0o644); err != nil {
				t.Fatalf("write pyproject.toml: %v", err)
			}
			got := extractPythonPackageName(dir)
			if got != tc.expected {
				t.Errorf("extractPythonPackageName: expected %q, got %q", tc.expected, got)
			}
		})
	}
}

func TestExtractPythonPackageName_MissingFile(t *testing.T) {
	if got := extractPythonPackageName(t.TempDir()); got != "" {
		t.Errorf("expected empty string for missing pyproject.toml, got %q", got)
	}
}

// TestDetectPackageNameFallback verifies that when no manifest is present,
// detectPackageName returns the serviceName — preserving the old behaviour
// for languages we don't yet parse manifests for (Java, Kotlin, Scala).
func TestDetectPackageNameFallback(t *testing.T) {
	emptyDir := t.TempDir()

	si := &SCIPIndexer{
		serviceName: "fallback-svc",
		language:    LanguageJava,
	}
	got := si.detectPackageName(emptyDir)
	if got != "fallback-svc" {
		t.Errorf("expected fallback to serviceName, got %q", got)
	}
}

// TestDetectPackageNameGoUsesModulePath verifies that when a go.mod is
// present, detectPackageName returns the module path — NOT the serviceName.
// This is the bug B2 directly fixes: the resolver matched against the
// --service flag value rather than the canonical Go module path.
func TestDetectPackageNameGoUsesModulePath(t *testing.T) {
	dir := t.TempDir()
	gomod := "module github.com/context-maximiser/code-graph/libs/core-models-go\n\ngo 1.24\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	si := &SCIPIndexer{
		serviceName: "codegraph/libs/core-models-go", // the --service flag value
		language:    LanguageGo,
	}
	got := si.detectPackageName(dir)
	expected := "github.com/context-maximiser/code-graph/libs/core-models-go"
	if got != expected {
		t.Errorf("expected Go module path %q, got %q (regression: should not return serviceName)", expected, got)
	}
}
