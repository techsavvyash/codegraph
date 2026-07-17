package docs

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// writeFile creates path (and parents) with content.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func listRelPaths(t *testing.T, root string) []string {
	t.Helper()
	src := &RepoMarkdownSource{Root: root}
	refs, err := src.List(context.Background())
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	paths := make([]string, len(refs))
	for i, r := range refs {
		paths[i] = r.RelPath
		if r.Format != "markdown" {
			t.Errorf("ref %s has format %q, want markdown", r.RelPath, r.Format)
		}
	}
	return paths
}

// TestRepoMarkdownSource_WalkFallback exercises the non-git path: exclusion
// dirs are skipped, non-markdown files ignored, output sorted.
func TestRepoMarkdownSource_WalkFallback(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "README.md"), "# Root")
	writeFile(t, filepath.Join(root, "docs", "guide.md"), "# Guide")
	writeFile(t, filepath.Join(root, "docs", "notes.txt"), "not markdown")
	writeFile(t, filepath.Join(root, "node_modules", "pkg", "README.md"), "excluded")
	writeFile(t, filepath.Join(root, "vendor", "lib", "doc.md"), "excluded")
	writeFile(t, filepath.Join(root, ".git", "info.md"), "excluded")

	got := listRelPaths(t, root)
	want := []string{"README.md", "docs/guide.md"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("path %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

// TestRepoMarkdownSource_GitListing verifies the git path: .gitignore is
// honored, untracked-but-unignored files are included, and a tracked file
// deleted from the worktree is excluded.
func TestRepoMarkdownSource_GitListing(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")

	writeFile(t, filepath.Join(root, "tracked.md"), "# Tracked")
	writeFile(t, filepath.Join(root, "deleted.md"), "# Deleted later")
	writeFile(t, filepath.Join(root, ".gitignore"), "ignored.md\ngenerated/\n")
	run("add", "-A")
	run("commit", "-q", "-m", "init")

	writeFile(t, filepath.Join(root, "untracked.md"), "# Untracked")
	writeFile(t, filepath.Join(root, "ignored.md"), "# Ignored")
	writeFile(t, filepath.Join(root, "generated", "api.md"), "# Generated")
	if err := os.Remove(filepath.Join(root, "deleted.md")); err != nil {
		t.Fatal(err)
	}

	got := listRelPaths(t, root)
	want := []string{"tracked.md", "untracked.md"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("path %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

// TestRepoMarkdownSource_ReadSizeLimit verifies oversized documents are
// rejected with ErrDocTooLarge (typed, so the ingestor can count skips).
func TestRepoMarkdownSource_ReadSizeLimit(t *testing.T) {
	root := t.TempDir()
	big := make([]byte, maxDocBytes+1)
	for i := range big {
		big[i] = 'a'
	}
	if err := os.WriteFile(filepath.Join(root, "big.md"), big, 0o644); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "ok.md"), "# OK")

	src := &RepoMarkdownSource{Root: root}

	if _, err := src.Read(context.Background(), DocRef{RelPath: "ok.md"}); err != nil {
		t.Errorf("small file should read: %v", err)
	}

	_, err := src.Read(context.Background(), DocRef{RelPath: "big.md"})
	var tooLarge *ErrDocTooLarge
	if !errors.As(err, &tooLarge) {
		t.Fatalf("want ErrDocTooLarge, got %v", err)
	}
	if tooLarge.RelPath != "big.md" {
		t.Errorf("error carries RelPath %q, want big.md", tooLarge.RelPath)
	}
}

// TestRepoMarkdownSource_SymlinkExcluded: symlinked markdown must not be
// listed (symlinks are not followed per RFC-011 §4.1).
func TestRepoMarkdownSource_SymlinkExcluded(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "real.md"), "# Real")
	if err := os.Symlink(filepath.Join(root, "real.md"), filepath.Join(root, "link.md")); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}

	got := listRelPaths(t, root)
	if len(got) != 1 || got[0] != "real.md" {
		t.Errorf("got %v, want [real.md]", got)
	}
}

// TestFirstH1 covers title extraction.
func TestFirstH1(t *testing.T) {
	cases := []struct {
		content string
		want    string
	}{
		{"# Title\n\nbody", "Title"},
		{"intro\n\n# Later Title\n", "Later Title"},
		{"## Only H2\n", ""},
		{"", ""},
		{"text mentioning # inline is not a heading", ""},
		{"  # leading whitespace is trimmed", "leading whitespace is trimmed"},
	}
	for _, tc := range cases {
		if got := firstH1(tc.content); got != tc.want {
			t.Errorf("firstH1(%q) = %q, want %q", tc.content, got, tc.want)
		}
	}
}
