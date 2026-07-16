package docs

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// DocRef identifies one document available from a Source.
type DocRef struct {
	RelPath string // path relative to the source root (or remote page path)
	Title   string // may be empty; the ingestor falls back to first H1 / basename
	Format  string // "markdown"
}

// Source enumerates documents for ingestion. This is the pluggability seam of
// RFC-011 §4.1: v1 ships RepoMarkdownSource; a future Confluence/Notion
// adapter implements the same two methods.
type Source interface {
	List(ctx context.Context) ([]DocRef, error)
	Read(ctx context.Context, ref DocRef) ([]byte, error)
}

// maxDocBytes guards Neo4j node properties against pathological inputs
// (generated changelogs, vendored specs). Oversized files are skipped and
// counted in the ingest report, never silently truncated.
const maxDocBytes = 2 * 1024 * 1024

// RepoMarkdownSource lists markdown files in a repository working tree.
// When the root is a git repository it uses `git ls-files` (tracked +
// untracked-unignored), which honors .gitignore for free; otherwise it falls
// back to a directory walk with a standard exclusion set.
type RepoMarkdownSource struct {
	Root string
}

// walkExclusions are directory names skipped by the non-git fallback walk.
var walkExclusions = map[string]bool{
	".git":         true,
	"node_modules": true,
	"vendor":       true,
	"dist":         true,
	"build":        true,
	".next":        true,
	"target":       true,
}

// List returns the repository's markdown files in deterministic (sorted)
// order. Symlinks and files that vanished between listing and stat are
// excluded here; size limits are enforced by Read.
func (s *RepoMarkdownSource) List(ctx context.Context) ([]DocRef, error) {
	paths, err := s.gitListMarkdown(ctx)
	if err != nil {
		paths, err = s.walkMarkdown()
		if err != nil {
			return nil, fmt.Errorf("failed to list markdown files under %s: %w", s.Root, err)
		}
	}

	sort.Strings(paths)
	refs := make([]DocRef, 0, len(paths))
	for _, rel := range paths {
		info, err := os.Lstat(filepath.Join(s.Root, rel))
		if err != nil || !info.Mode().IsRegular() {
			continue // deleted-but-tracked, symlink, or otherwise unreadable
		}
		refs = append(refs, DocRef{RelPath: rel, Format: "markdown"})
	}
	return refs, nil
}

// Read returns the raw bytes of the document. Files over maxDocBytes are
// rejected with ErrDocTooLarge so the ingestor can count the skip.
func (s *RepoMarkdownSource) Read(_ context.Context, ref DocRef) ([]byte, error) {
	path := filepath.Join(s.Root, ref.RelPath)
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s: not a regular file", ref.RelPath)
	}
	if info.Size() > maxDocBytes {
		return nil, &ErrDocTooLarge{RelPath: ref.RelPath, Size: info.Size()}
	}
	return os.ReadFile(path)
}

// ErrDocTooLarge marks a document skipped for exceeding maxDocBytes.
type ErrDocTooLarge struct {
	RelPath string
	Size    int64
}

func (e *ErrDocTooLarge) Error() string {
	return fmt.Sprintf("%s: %d bytes exceeds the %d-byte document limit", e.RelPath, e.Size, maxDocBytes)
}

// gitListMarkdown lists tracked plus untracked-unignored *.md files via git.
// Returns an error when root is not a git work tree (caller falls back).
func (s *RepoMarkdownSource) gitListMarkdown(ctx context.Context) ([]string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", s.Root, "ls-files", "-z",
		"--cached", "--others", "--exclude-standard", "--", "*.md")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var paths []string
	for _, p := range bytes.Split(out, []byte{0}) {
		if len(p) == 0 {
			continue
		}
		paths = append(paths, string(p))
	}
	return paths, nil
}

// walkMarkdown is the non-git fallback: WalkDir with the standard exclusions.
func (s *RepoMarkdownSource) walkMarkdown() ([]string, error) {
	var paths []string
	err := filepath.WalkDir(s.Root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != s.Root && walkExclusions[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(d.Name()), ".md") {
			return nil
		}
		rel, err := filepath.Rel(s.Root, path)
		if err != nil {
			return err
		}
		paths = append(paths, filepath.ToSlash(rel))
		return nil
	})
	return paths, err
}
