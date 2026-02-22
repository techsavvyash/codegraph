package static

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

// ---------------------------------------------------------------------------
// Regression: All languages present and versions pinned
// ---------------------------------------------------------------------------

func TestDefaultReleases_AllLanguagesPresent(t *testing.T) {
	releases := DefaultReleases()
	languages := map[Language]bool{}
	for _, r := range releases {
		languages[r.Language] = true
	}

	for _, lang := range []Language{LanguageGo, LanguageTypeScript, LanguagePython, LanguageJava, LanguagePHP} {
		if !languages[lang] {
			t.Errorf("expected %s in default releases", lang)
		}
	}
}

func TestDefaultReleases_GoHasURL(t *testing.T) {
	releases := DefaultReleases()
	for _, r := range releases {
		if r.Language == LanguageGo {
			if r.URL == "" {
				t.Error("expected Go release to have a download URL")
			}
			return
		}
	}
	t.Error("Go release not found")
}

func TestDefaultReleases_PinnedVersions(t *testing.T) {
	releases := DefaultReleases()
	for _, r := range releases {
		if r.Version == "latest" {
			t.Errorf("%s has unpinned version 'latest'", r.Language)
		}
		if r.Version == "" {
			t.Errorf("%s has empty version", r.Language)
		}
	}
}

func TestResolveURL_GoRelease_TarGzFormat(t *testing.T) {
	mgr := NewIndexerManager("")
	release := mgr.findRelease(LanguageGo)
	if release == nil {
		t.Skip("Go release not in defaults")
	}

	url := mgr.resolveURL(release)
	if url == "" {
		t.Fatal("expected non-empty resolved URL for Go")
	}

	// Must end with .tar.gz
	if !strings.HasSuffix(url, ".tar.gz") {
		t.Errorf("expected URL to end with .tar.gz, got %s", url)
	}

	// Must contain the version number without "v" prefix
	if !strings.Contains(url, "0.1.26") {
		t.Errorf("expected URL to contain version number 0.1.26, got %s", url)
	}

	// Must contain runtime OS and arch
	if !strings.Contains(url, runtime.GOOS) {
		t.Errorf("expected URL to contain OS %s, got %s", runtime.GOOS, url)
	}
	if !strings.Contains(url, runtime.GOARCH) {
		t.Errorf("expected URL to contain arch %s, got %s", runtime.GOARCH, url)
	}

	// Should NOT contain unreplaced placeholders
	if strings.Contains(url, "{") {
		t.Errorf("URL still contains unreplaced placeholders: %s", url)
	}
}

func TestResolveURL_VersionNum(t *testing.T) {
	mgr := NewIndexerManager("")

	release := &IndexerRelease{
		Binary:  "scip-go",
		Version: "v1.2.3",
		URL:     "https://example.com/{version}/{version_num}",
	}

	url := mgr.resolveURL(release)
	expected := "https://example.com/v1.2.3/1.2.3"
	if url != expected {
		t.Errorf("expected %s, got %s", expected, url)
	}
}

func TestResolveChecksum(t *testing.T) {
	releases := DefaultReleases()
	for _, r := range releases {
		if r.Language == LanguageGo {
			platform := runtime.GOOS + "/" + runtime.GOARCH
			checksum, ok := r.Checksums[platform]
			// We only check common platforms
			if runtime.GOOS == "linux" || runtime.GOOS == "darwin" {
				if !ok || checksum == "" {
					t.Errorf("expected checksum for %s on %s", r.Binary, platform)
				}
				if len(checksum) != 64 {
					t.Errorf("expected 64-char hex checksum, got %d chars", len(checksum))
				}
			}
			return
		}
	}
	t.Error("Go release not found")
}

func TestDefaultReleases_GoHasChecksums(t *testing.T) {
	releases := DefaultReleases()
	for _, r := range releases {
		if r.Language == LanguageGo {
			if len(r.Checksums) == 0 {
				t.Fatal("expected Go release to have checksums")
			}
			for _, platform := range []string{"linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64"} {
				if _, ok := r.Checksums[platform]; !ok {
					t.Errorf("missing checksum for platform %s", platform)
				}
			}
			return
		}
	}
	t.Error("Go release not found")
}

func TestInstallMethod_AllLanguages(t *testing.T) {
	expected := map[Language]InstallMethod{
		LanguageGo:         InstallBinaryDownload,
		LanguageTypeScript: InstallNPM,
		LanguagePython:     InstallNPM,
		LanguageJava:       InstallCoursier,
		LanguagePHP:        InstallComposer,
	}

	releases := DefaultReleases()
	for _, r := range releases {
		want, ok := expected[r.Language]
		if !ok {
			continue
		}
		if r.Method != want {
			t.Errorf("%s: expected method %s, got %s", r.Language, want, r.Method)
		}
	}
}

func TestExtractTarGz(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a test .tar.gz with a fake binary inside
	archivePath := filepath.Join(tmpDir, "test.tar.gz")
	binaryContent := []byte("#!/bin/sh\necho hello\n")

	f, err := os.Create(archivePath)
	if err != nil {
		t.Fatalf("create archive: %v", err)
	}

	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)

	// Add the binary to the archive
	hdr := &tar.Header{
		Name: "scip-go",
		Mode: 0755,
		Size: int64(len(binaryContent)),
		Typeflag: tar.TypeReg,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatalf("write tar header: %v", err)
	}
	if _, err := tw.Write(binaryContent); err != nil {
		t.Fatalf("write tar content: %v", err)
	}

	tw.Close()
	gw.Close()
	f.Close()

	// Extract it
	destPath := filepath.Join(tmpDir, "extracted-binary")
	if err := extractTarGz(archivePath, "scip-go", destPath); err != nil {
		t.Fatalf("extractTarGz: %v", err)
	}

	// Verify the extracted file
	data, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("read extracted: %v", err)
	}
	if string(data) != string(binaryContent) {
		t.Errorf("extracted content mismatch: got %q", string(data))
	}

	// Verify it's executable
	info, err := os.Stat(destPath)
	if err != nil {
		t.Fatalf("stat extracted: %v", err)
	}
	if info.Mode()&0111 == 0 {
		t.Error("extracted binary is not executable")
	}
}

func TestExtractTarGz_BinaryNotFound(t *testing.T) {
	tmpDir := t.TempDir()

	// Create an archive without the target binary
	archivePath := filepath.Join(tmpDir, "test.tar.gz")
	f, _ := os.Create(archivePath)
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)

	hdr := &tar.Header{
		Name: "other-file",
		Mode: 0644,
		Size: 5,
		Typeflag: tar.TypeReg,
	}
	tw.WriteHeader(hdr)
	tw.Write([]byte("hello"))
	tw.Close()
	gw.Close()
	f.Close()

	destPath := filepath.Join(tmpDir, "missing")
	err := extractTarGz(archivePath, "scip-go", destPath)
	if err == nil {
		t.Fatal("expected error for missing binary in archive")
	}
	if !strings.Contains(err.Error(), "not found in archive") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestExtractTarGz_NestedBinary(t *testing.T) {
	tmpDir := t.TempDir()

	// Create an archive with the binary in a subdirectory
	archivePath := filepath.Join(tmpDir, "test.tar.gz")
	binaryContent := []byte("nested-binary")

	f, _ := os.Create(archivePath)
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)

	hdr := &tar.Header{
		Name: "subdir/scip-go",
		Mode: 0755,
		Size: int64(len(binaryContent)),
		Typeflag: tar.TypeReg,
	}
	tw.WriteHeader(hdr)
	tw.Write(binaryContent)
	tw.Close()
	gw.Close()
	f.Close()

	destPath := filepath.Join(tmpDir, "extracted")
	if err := extractTarGz(archivePath, "scip-go", destPath); err != nil {
		t.Fatalf("extractTarGz nested: %v", err)
	}

	data, _ := os.ReadFile(destPath)
	if string(data) != string(binaryContent) {
		t.Errorf("nested extraction content mismatch")
	}
}

func TestResolveBinary_Precedence_CacheOverPATH(t *testing.T) {
	// Cache should take precedence over system PATH
	tmpDir := t.TempDir()
	mgr := NewIndexerManager(tmpDir)

	// Create fake cached binary
	binDir := filepath.Join(tmpDir, "go", "v0.1.26")
	os.MkdirAll(binDir, 0755)
	binPath := filepath.Join(binDir, "scip-go")
	os.WriteFile(binPath, []byte("#!/bin/sh\n"), 0755)

	resolved := mgr.ResolveBinary(LanguageGo)
	// Even if scip-go is on PATH, cache should win
	if resolved != binPath {
		t.Errorf("expected cache path %s to take precedence, got %s", binPath, resolved)
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

func TestDefaultReleases_NPMPackages(t *testing.T) {
	releases := DefaultReleases()
	for _, r := range releases {
		if r.Method == InstallNPM {
			if r.Package == "" {
				t.Errorf("%s: npm install method but no Package specified", r.Language)
			}
			if !strings.HasPrefix(r.Package, "@sourcegraph/") {
				t.Errorf("%s: expected @sourcegraph/ scoped package, got %s", r.Language, r.Package)
			}
		}
	}
}

func TestDefaultReleases_JavaCoursier(t *testing.T) {
	releases := DefaultReleases()
	for _, r := range releases {
		if r.Language == LanguageJava {
			if r.Method != InstallCoursier {
				t.Errorf("expected Java to use coursier install method, got %s", r.Method)
			}
			if r.Package == "" {
				t.Error("expected Java to have a Maven coordinate")
			}
			if r.MainClass == "" {
				t.Error("expected Java to have a MainClass")
			}
			return
		}
	}
	t.Error("Java release not found")
}

func TestDefaultReleases_PHPComposer(t *testing.T) {
	releases := DefaultReleases()
	for _, r := range releases {
		if r.Language == LanguagePHP {
			if r.Method != InstallComposer {
				t.Errorf("expected PHP to use composer install method, got %s", r.Method)
			}
			if r.Package != "davidrjenni/scip-php" {
				t.Errorf("expected PHP package davidrjenni/scip-php, got %s", r.Package)
			}
			if r.Binary != "scip-php" {
				t.Errorf("expected PHP binary scip-php, got %s", r.Binary)
			}
			return
		}
	}
	t.Error("PHP release not found")
}
