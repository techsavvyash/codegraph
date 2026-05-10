package static

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// TestShouldExcludePathIncludesFixturesAndSvelteKit guards against regression
// of B7. Test fixture trees and SvelteKit's generated .svelte-kit/ output are
// not part of production logic — leaving them in the graph pollutes "what
// files contain function X" answers and inflates edge counts.
func TestShouldExcludePathIncludesFixturesAndSvelteKit(t *testing.T) {
	excluded := []string{
		"test/fixtures/tiny-ts/src/index.ts",
		"libs/foo/testdata/sample.json",
		"apps/chat-ui/.svelte-kit/types/foo.d.ts",
		"node_modules/x/y.js",
		"vendor/x.go",
	}
	for _, p := range excluded {
		if !shouldExcludePath(p) {
			t.Errorf("expected %q to be excluded", p)
		}
	}

	kept := []string{
		"apps/cli/main.go",
		"libs/indexer-go/static/scip_parser.go",
		// Real test files alongside source — keep.
		"libs/foo/foo_test.go",
		"apps/chat-ui/src/lib/stores/chat.test.ts",
	}
	for _, p := range kept {
		if shouldExcludePath(p) {
			t.Errorf("expected %q to be kept (false positive)", p)
		}
	}
}

// TestParsePnpmWorkspaceExpandsGlobs verifies the workspace parser handles
// the actual shapes seen in this repo's pnpm-workspace.yaml: globbed entries
// (apps/*) and literal directory entries (tools/nx). Quoted and unquoted
// values must both work.
func TestParsePnpmWorkspaceExpandsGlobs(t *testing.T) {
	root := t.TempDir()
	mkdir := func(rel string) string {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(full, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		return full
	}
	app1 := mkdir("apps/chat-ui")
	app2 := mkdir("apps/dashboard")
	tools := mkdir("tools/nx")
	mkdir("libs/foo") // not in workspace, must NOT be picked up

	yaml := `packages:
  - "apps/*"
  - tools/nx
`
	yamlPath := filepath.Join(root, "pnpm-workspace.yaml")
	if err := os.WriteFile(yamlPath, []byte(yaml), 0o644); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	got := parsePnpmWorkspace(yamlPath, root)
	sort.Strings(got)
	want := []string{app1, app2, tools}
	sort.Strings(want)

	if len(got) != len(want) {
		t.Fatalf("expected %d packages, got %d: %v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] expected %q, got %q", i, want[i], got[i])
		}
	}
}

// TestParsePnpmWorkspaceIgnoresOtherTopLevelKeys verifies that list items under
// keys other than `packages:` are not treated as workspace entries (so a
// stray `catalog: - foo` is not mis-parsed).
func TestParsePnpmWorkspaceIgnoresOtherTopLevelKeys(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "apps/foo"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	yaml := `catalog:
  - "should-not-match"
packages:
  - apps/foo
`
	yamlPath := filepath.Join(root, "pnpm-workspace.yaml")
	if err := os.WriteFile(yamlPath, []byte(yaml), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := parsePnpmWorkspace(yamlPath, root)
	if len(got) != 1 {
		t.Fatalf("expected 1 match, got %d: %v", len(got), got)
	}
}

// TestWorkspacePackageLanguagePrefersTypeScript verifies the manifest-based
// classifier: a directory with both tsconfig.json and package.json resolves
// to TypeScript, package-only resolves to JavaScript, and empty dirs return "".
func TestWorkspacePackageLanguagePrefersTypeScript(t *testing.T) {
	tsDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tsDir, "tsconfig.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tsDir, "package.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := workspacePackageLanguage(tsDir); got != LanguageTypeScript {
		t.Errorf("ts+pkg dir: got %q, want TypeScript", got)
	}

	jsDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(jsDir, "package.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := workspacePackageLanguage(jsDir); got != LanguageJavaScript {
		t.Errorf("pkg-only dir: got %q, want JavaScript", got)
	}

	emptyDir := t.TempDir()
	if got := workspacePackageLanguage(emptyDir); got != "" {
		t.Errorf("empty dir: got %q, want \"\"", got)
	}
}
