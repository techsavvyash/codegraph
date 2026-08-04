package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveRootPathValidatesAndNormalizes(t *testing.T) {
	t.Run("accepts an existing directory and returns an absolute path", func(t *testing.T) {
		dir := t.TempDir()
		resolved, err := resolveRootPath(dir)
		require.NoError(t, err)
		assert.True(t, filepath.IsAbs(resolved))

		wantResolved := dir
		if evaled, err := filepath.EvalSymlinks(dir); err == nil {
			wantResolved = evaled
		}
		assert.Equal(t, wantResolved, resolved)
	})

	t.Run("resolves a relative path to absolute", func(t *testing.T) {
		dir := t.TempDir()
		wd, err := os.Getwd()
		require.NoError(t, err)
		rel, err := filepath.Rel(wd, dir)
		require.NoError(t, err)

		resolved, err := resolveRootPath(rel)
		require.NoError(t, err)
		assert.True(t, filepath.IsAbs(resolved))
	})

	t.Run("errors when the path does not exist", func(t *testing.T) {
		_, err := resolveRootPath(filepath.Join(t.TempDir(), "does-not-exist"))
		require.Error(t, err)
	})

	t.Run("errors when the path is a file, not a directory", func(t *testing.T) {
		dir := t.TempDir()
		file := filepath.Join(dir, "a-file.txt")
		require.NoError(t, os.WriteFile(file, []byte("x"), 0o644))

		_, err := resolveRootPath(file)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not a directory")
	})
}
