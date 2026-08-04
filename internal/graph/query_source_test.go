package neo4j

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReadSourceFileForQueryCandidateOrder locks the resolution order shared
// by GetFunctionSourceCode and GetFunctionSourceCodeBySignature (RFC-012 R2):
// filePath as-is first (covers absolute paths and the case where cwd already
// sits at the project root), then rootPath/filePath (the owning Service's
// indexed root — authoritative regardless of caller cwd), then a cwd-relative
// legacy fallback for graphs indexed before rootPath existed.
func TestReadSourceFileForQueryCandidateOrder(t *testing.T) {
	t.Run("absolute filePath is read directly", func(t *testing.T) {
		dir := t.TempDir()
		abs := filepath.Join(dir, "abs.go")
		require.NoError(t, os.WriteFile(abs, []byte("package abs\n"), 0o644))

		content, err := readSourceFileForQuery(abs, "/nonexistent/root")
		require.NoError(t, err)
		assert.Equal(t, "package abs\n", string(content))
	})

	t.Run("absolute filePath that does not exist errors without trying rootPath", func(t *testing.T) {
		rootDir := t.TempDir()
		abs := filepath.Join(t.TempDir(), "missing.go")

		_, err := readSourceFileForQuery(abs, rootDir)
		require.Error(t, err)
	})

	t.Run("relative filePath resolves via rootPath when cwd-relative fails", func(t *testing.T) {
		rootDir := t.TempDir()
		rel := "pkg/target.go"
		require.NoError(t, os.MkdirAll(filepath.Join(rootDir, "pkg"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(rootDir, rel), []byte("from rootPath\n"), 0o644))

		content, err := readSourceFileForQuery(rel, rootDir)
		require.NoError(t, err)
		assert.Equal(t, "from rootPath\n", string(content))
	})

	t.Run("relative filePath resolvable from cwd wins over a nonexistent rootPath", func(t *testing.T) {
		wd, err := os.Getwd()
		require.NoError(t, err)
		bare := "query_source_test.go" // this file, known to exist relative to cwd
		require.FileExists(t, filepath.Join(wd, bare))

		content, err := readSourceFileForQuery(bare, "/nonexistent/root")
		require.NoError(t, err)
		assert.NotEmpty(t, content)
	})

	t.Run("no candidate resolves returns an error", func(t *testing.T) {
		_, err := readSourceFileForQuery("does/not/exist.go", t.TempDir())
		require.Error(t, err)
	})
}

func TestExtractSource(t *testing.T) {
	content := []byte("line one\nline two\nline three\n")

	t.Run("byte-exact extraction takes precedence when offsets are valid", func(t *testing.T) {
		// bytes 9-17 == "line two"
		src, ok := extractSource(content, 9, 17, 2, 2)
		require.True(t, ok)
		assert.Equal(t, "line two", src)
	})

	t.Run("falls back to line-based extraction when byte offsets are absent", func(t *testing.T) {
		src, ok := extractSource(content, -1, -1, 2, 3)
		require.True(t, ok)
		assert.Equal(t, "line two\nline three", src)
	})

	t.Run("falls back to line-based extraction when byte offsets are out of range", func(t *testing.T) {
		src, ok := extractSource(content, 0, 10000, 1, 1)
		require.True(t, ok)
		assert.Equal(t, "line one", src)
	})

	t.Run("returns false when neither byte nor line offsets are usable", func(t *testing.T) {
		_, ok := extractSource(content, -1, -1, 0, 0)
		assert.False(t, ok)
	})

	t.Run("returns false when line range exceeds file length", func(t *testing.T) {
		_, ok := extractSource(content, -1, -1, 1, 100)
		assert.False(t, ok)
	})
}
