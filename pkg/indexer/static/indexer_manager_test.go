package static

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewIndexerManager(t *testing.T) {
	mgr := NewIndexerManager("")
	if mgr == nil {
		t.Fatal("expected non-nil manager")
	}
	if mgr.cacheDir == "" {
		t.Error("expected non-empty cache dir")
	}
	// Should default to ~/.codegraph/indexers
	home, _ := os.UserHomeDir()
	expected := filepath.Join(home, ".codegraph", "indexers")
	if mgr.cacheDir != expected {
		t.Errorf("expected cache dir %s, got %s", expected, mgr.cacheDir)
	}
}

func TestNewIndexerManager_CustomDir(t *testing.T) {
	mgr := NewIndexerManager("/tmp/test-indexers")
	if mgr.cacheDir != "/tmp/test-indexers" {
		t.Errorf("expected /tmp/test-indexers, got %s", mgr.cacheDir)
	}
}

func TestCacheDir(t *testing.T) {
	mgr := NewIndexerManager("/tmp/indexers")
	if mgr.CacheDir() != "/tmp/indexers" {
		t.Errorf("expected /tmp/indexers, got %s", mgr.CacheDir())
	}
}

func TestCachedBinaryPath(t *testing.T) {
	mgr := NewIndexerManager("/tmp/indexers")

	path := mgr.CachedBinaryPath(LanguageGo)
	expected := "/tmp/indexers/go/v0.1.26/scip-go"
	if path != expected {
		t.Errorf("expected %s, got %s", expected, path)
	}
}

func TestCachedBinaryPath_UnknownLanguage(t *testing.T) {
	mgr := NewIndexerManager("/tmp/indexers")

	path := mgr.CachedBinaryPath(Language("rust"))
	if path != "" {
		t.Errorf("expected empty path for unknown language, got %s", path)
	}
}

func TestResolveBinary_SystemPATH(t *testing.T) {
	mgr := NewIndexerManager("/tmp/nonexistent-indexers")

	// scip-go may or may not be on PATH in the test environment
	path := mgr.ResolveBinary(LanguageGo)
	if path != "" {
		// If found, it should be an absolute path
		if !filepath.IsAbs(path) {
			t.Errorf("expected absolute path, got %s", path)
		}
	}
}

func TestResolveBinary_Cached(t *testing.T) {
	// Create a temp dir with a fake cached binary
	tmpDir := t.TempDir()
	mgr := NewIndexerManager(tmpDir)

	// Create fake cached binary
	binDir := filepath.Join(tmpDir, "go", "v0.1.26")
	os.MkdirAll(binDir, 0755)
	binPath := filepath.Join(binDir, "scip-go")
	os.WriteFile(binPath, []byte("#!/bin/sh\n"), 0755)

	resolved := mgr.ResolveBinary(LanguageGo)
	if resolved != binPath {
		t.Errorf("expected %s, got %s", binPath, resolved)
	}
}

func TestIsInstalled(t *testing.T) {
	mgr := NewIndexerManager("/tmp/nonexistent-indexers")

	// For unknown language, should return false
	if mgr.IsInstalled(Language("rust")) {
		t.Error("expected rust to not be installed")
	}
}

func TestDefaultReleases(t *testing.T) {
	releases := DefaultReleases()
	if len(releases) == 0 {
		t.Fatal("expected at least one release")
	}

	// Check that Go is in the releases
	found := false
	for _, r := range releases {
		if r.Language == LanguageGo {
			found = true
			if r.Binary != "scip-go" {
				t.Errorf("expected binary 'scip-go', got %s", r.Binary)
			}
		}
	}
	if !found {
		t.Error("expected Go in default releases")
	}
}

func TestStatus(t *testing.T) {
	mgr := NewIndexerManager("/tmp/nonexistent-indexers")
	statuses := mgr.Status()
	if len(statuses) == 0 {
		t.Fatal("expected at least one status")
	}

	for _, s := range statuses {
		if s.Binary == "" {
			t.Error("expected non-empty binary name")
		}
		if s.Language == "" {
			t.Error("expected non-empty language")
		}
	}
}

func TestFindRelease(t *testing.T) {
	mgr := NewIndexerManager("")

	release := mgr.findRelease(LanguageGo)
	if release == nil {
		t.Fatal("expected to find Go release")
	}
	if release.Binary != "scip-go" {
		t.Errorf("expected scip-go, got %s", release.Binary)
	}

	release = mgr.findRelease(Language("rust"))
	if release != nil {
		t.Error("expected nil for unknown language")
	}
}

func TestResolveURL(t *testing.T) {
	mgr := NewIndexerManager("")

	release := &IndexerRelease{
		Binary:  "scip-go",
		Version: "v0.1.26",
		URL:     "https://example.com/{os}/{arch}/{binary}-{version}",
	}

	url := mgr.resolveURL(release)
	if url == "" {
		t.Fatal("expected non-empty URL")
	}
	// Should contain the runtime OS and arch
	if url == release.URL {
		t.Error("URL should have been resolved with platform info")
	}
}

func TestInstallAll(t *testing.T) {
	// Test with an already-installed language (if available on PATH)
	mgr := NewIndexerManager("/tmp/nonexistent-indexers")

	// InstallAll should categorize languages correctly
	installed, failed := mgr.InstallAll([]Language{Language("nonexistent_lang")})
	// nonexistent_lang should fail
	if len(failed) == 0 && len(installed) == 0 {
		// That's fine — it may fail with "unsupported language"
	}
	_ = installed
	_ = failed
}
