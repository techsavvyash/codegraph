package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReadSourceFileCandidateOrder locks the resolution order documented on
// readSourceFile: absolute path short-circuits; otherwise rootPath/filePath
// is tried BEFORE workspaceRoot/filePath (rootPath is the owning service's
// authoritative indexed root, RFC-012 R2 — workspaceRoot is just the MCP
// process's cwd, frequently unrelated to any indexed repo).
func TestReadSourceFileCandidateOrder(t *testing.T) {
	t.Run("absolute path is read directly, ignoring rootPath/workspaceRoot", func(t *testing.T) {
		dir := t.TempDir()
		abs := filepath.Join(dir, "abs.go")
		require.NoError(t, os.WriteFile(abs, []byte("package abs\n"), 0o644))

		data, err := readSourceFile(abs, "svc", "/nonexistent/root", "/nonexistent/workspace")
		require.NoError(t, err)
		assert.Equal(t, "package abs\n", string(data))
	})

	t.Run("rootPath/filePath wins when both rootPath and workspaceRoot could resolve it", func(t *testing.T) {
		rootDir := t.TempDir()
		workspaceDir := t.TempDir()
		rel := "pkg/file.go"

		require.NoError(t, os.MkdirAll(filepath.Join(rootDir, "pkg"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(rootDir, rel), []byte("from rootPath\n"), 0o644))

		require.NoError(t, os.MkdirAll(filepath.Join(workspaceDir, "pkg"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(workspaceDir, rel), []byte("from workspaceRoot\n"), 0o644))

		data, err := readSourceFile(rel, "svc", rootDir, workspaceDir)
		require.NoError(t, err)
		assert.Equal(t, "from rootPath\n", string(data), "rootPath candidate must be tried first")
	})

	t.Run("falls back to workspaceRoot/filePath when rootPath does not resolve", func(t *testing.T) {
		workspaceDir := t.TempDir()
		rel := "pkg/file.go"
		require.NoError(t, os.MkdirAll(filepath.Join(workspaceDir, "pkg"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(workspaceDir, rel), []byte("from workspaceRoot\n"), 0o644))

		data, err := readSourceFile(rel, "svc", "/nonexistent/root", workspaceDir)
		require.NoError(t, err)
		assert.Equal(t, "from workspaceRoot\n", string(data))
	})

	t.Run("falls back to workspaceRoot/<service-without-org-prefix>/filePath", func(t *testing.T) {
		workspaceDir := t.TempDir()
		rel := "src/file.ts"
		require.NoError(t, os.MkdirAll(filepath.Join(workspaceDir, "libs/foo/src"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(workspaceDir, "libs/foo", rel), []byte("nested package\n"), 0o644))

		data, err := readSourceFile(rel, "codegraph/libs/foo", "", workspaceDir)
		require.NoError(t, err)
		assert.Equal(t, "nested package\n", string(data))
	})

	t.Run("falls back to bare filePath relative to process cwd as last resort", func(t *testing.T) {
		wd, err := os.Getwd()
		require.NoError(t, err)
		bare := "handlers_source_readfile_test.go" // this file, known to exist relative to cwd
		require.FileExists(t, filepath.Join(wd, bare))

		data, err := readSourceFile(bare, "svc", "/nonexistent/root", "/nonexistent/workspace")
		require.NoError(t, err)
		assert.NotEmpty(t, data)
	})

	t.Run("empty filePath errors", func(t *testing.T) {
		_, err := readSourceFile("", "svc", "/root", "/workspace")
		require.Error(t, err)
	})

	t.Run("no candidate resolves returns the last error", func(t *testing.T) {
		_, err := readSourceFile("does/not/exist.go", "svc", t.TempDir(), t.TempDir())
		require.Error(t, err)
	})
}
