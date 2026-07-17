package static

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// InstallMethod describes how a SCIP indexer should be installed.
type InstallMethod string

const (
	InstallBinaryDownload InstallMethod = "binary"
	InstallNPM            InstallMethod = "npm"
	InstallCoursier       InstallMethod = "coursier"
	InstallGoInstall      InstallMethod = "go_install"
	InstallComposer       InstallMethod = "composer"
)

// IndexerRelease describes a downloadable SCIP indexer binary.
type IndexerRelease struct {
	Language    Language
	Binary      string            // e.g. "scip-go"
	Version     string            // e.g. "v0.1.26"
	Method      InstallMethod     // primary install method
	URL         string            // for binary downloads
	Checksums   map[string]string // SHA-256 hex keyed by "os/arch"
	Package     string            // npm package name or Maven coordinate
	MainClass   string            // for coursier (Java)
	FallbackCmd string            // fallback install command (e.g. go install ...)
}

// DefaultReleases returns the known-good release definitions for each language.
func DefaultReleases() []IndexerRelease {
	return []IndexerRelease{
		{
			Language: LanguageGo,
			Binary:   "scip-go",
			Version:  "v0.1.26",
			Method:   InstallBinaryDownload,
			URL:      "https://github.com/sourcegraph/scip-go/releases/download/{version}/scip-go_{version_num}_{os}_{arch}.tar.gz",
			Checksums: map[string]string{
				"linux/amd64":  "66257b6db74e13c2e756c9abba8e7d34e62eb91d16cdbe087a0b0c170c89c37d",
				"linux/arm64":  "bc8e5abb959521912d60181de8922e5158a609a2e9d87e6ed2b7801c11c0efab",
				"darwin/amd64": "768d8048d537f1e2a26735b37fa0481296c6a1010392b2750c88b73716b529cf",
				"darwin/arm64": "1b87a5e0b2af4e41bc1cc49220e7d3a84a831468ae6944a9574e7d4c1270909c",
			},
			FallbackCmd: "go install github.com/sourcegraph/scip-go/cmd/scip-go@v0.1.26",
		},
		{
			Language: LanguageTypeScript,
			Binary:   "scip-typescript",
			Version:  "0.3.11",
			Method:   InstallNPM,
			Package:  "@sourcegraph/scip-typescript",
		},
		{
			Language: LanguagePython,
			Binary:   "scip-python",
			Version:  "0.6.6",
			Method:   InstallNPM,
			Package:  "@sourcegraph/scip-python",
		},
		{
			Language:  LanguageJava,
			Binary:    "scip-java",
			Version:   "0.8.23",
			Method:    InstallCoursier,
			Package:   "com.sourcegraph:scip-java_2.13",
			MainClass: "com.sourcegraph.scip_java.ScipJava",
		},
		{
			Language: LanguagePHP,
			Binary:   "scip-php",
			Version:  "0.0.2",
			Method:   InstallComposer,
			Package:  "davidrjenni/scip-php",
		},
	}
}

// IndexerManager manages downloading, caching, and resolving SCIP indexer binaries.
type IndexerManager struct {
	cacheDir string // e.g. ~/.codegraph/indexers
	releases []IndexerRelease
}

// NewIndexerManager creates a new manager using the given cache directory.
// If cacheDir is empty, defaults to ~/.codegraph/indexers.
func NewIndexerManager(cacheDir string) *IndexerManager {
	if cacheDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "/tmp"
		}
		cacheDir = filepath.Join(home, ".codegraph", "indexers")
	}
	return &IndexerManager{
		cacheDir: cacheDir,
		releases: DefaultReleases(),
	}
}

// CacheDir returns the cache directory path.
func (m *IndexerManager) CacheDir() string {
	return m.cacheDir
}

// ResolveBinary returns the path to the SCIP indexer binary for the given language.
// It checks in order:
// 1. The local cache directory
// 2. The system PATH
// Returns empty string if not found.
func (m *IndexerManager) ResolveBinary(lang Language) string {
	config, err := GetLanguageConfig(lang)
	if err != nil {
		return ""
	}

	// Check cache first
	cachedPath := m.CachedBinaryPath(lang)
	if cachedPath != "" {
		if info, err := os.Stat(cachedPath); err == nil && !info.IsDir() {
			return cachedPath
		}
	}

	// Fall back to system PATH
	if path, err := exec.LookPath(config.SCIPBinary); err == nil {
		return path
	}

	return ""
}

// CachedBinaryPath returns the expected cache path for a language's indexer binary.
func (m *IndexerManager) CachedBinaryPath(lang Language) string {
	release := m.findRelease(lang)
	if release == nil {
		return ""
	}
	return filepath.Join(m.cacheDir, string(lang), release.Version, release.Binary)
}

// IsInstalled checks if the indexer for the given language is available.
func (m *IndexerManager) IsInstalled(lang Language) bool {
	return m.ResolveBinary(lang) != ""
}

// Install downloads and caches the SCIP indexer for the given language.
// It dispatches based on the release's InstallMethod.
func (m *IndexerManager) Install(lang Language) error {
	release := m.findRelease(lang)
	if release == nil {
		// No release definition — fall back to language config
		config, err := GetLanguageConfig(lang)
		if err != nil {
			return fmt.Errorf("unsupported language: %s", lang)
		}
		return m.installViaCommand(config)
	}

	switch release.Method {
	case InstallBinaryDownload:
		return m.downloadAndCache(release)
	case InstallNPM:
		return m.installNPM(release)
	case InstallCoursier:
		return m.installCoursier(release)
	case InstallGoInstall:
		return m.installGoInstall(release)
	case InstallComposer:
		return m.installComposer(release)
	default:
		// Unknown method — fall back to language config
		config, err := GetLanguageConfig(lang)
		if err != nil {
			return fmt.Errorf("unsupported language: %s", lang)
		}
		return m.installViaCommand(config)
	}
}

// InstallAll installs indexers for the given languages.
func (m *IndexerManager) InstallAll(languages []Language) (installed []Language, failed map[Language]error) {
	failed = make(map[Language]error)
	for _, lang := range languages {
		if m.IsInstalled(lang) {
			installed = append(installed, lang)
			continue
		}
		if err := m.Install(lang); err != nil {
			failed[lang] = err
		} else {
			installed = append(installed, lang)
		}
	}
	return
}

// Status returns the installation status for all known languages.
func (m *IndexerManager) Status() []IndexerStatus {
	var statuses []IndexerStatus
	for _, release := range m.releases {
		status := IndexerStatus{
			Language: release.Language,
			Binary:   release.Binary,
			Version:  release.Version,
		}
		path := m.ResolveBinary(release.Language)
		if path != "" {
			status.Installed = true
			status.Path = path
		}
		statuses = append(statuses, status)
	}
	return statuses
}

// IndexerStatus reports installation status for a language.
type IndexerStatus struct {
	Language  Language `json:"language"`
	Binary    string   `json:"binary"`
	Version   string   `json:"version"`
	Installed bool     `json:"installed"`
	Path      string   `json:"path,omitempty"`
}

// downloadAndCache downloads a binary from URL and stores it in the cache directory.
// Supports both raw binaries and .tar.gz archives.
func (m *IndexerManager) downloadAndCache(release *IndexerRelease) error {
	destDir := filepath.Join(m.cacheDir, string(release.Language), release.Version)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("failed to create cache directory: %w", err)
	}

	destPath := filepath.Join(destDir, release.Binary)

	// Resolve platform-specific URL
	url := m.resolveURL(release)
	if url == "" {
		return fmt.Errorf("no download URL for %s on %s/%s", release.Binary, runtime.GOOS, runtime.GOARCH)
	}

	fmt.Printf("Downloading %s %s from %s...\n", release.Binary, release.Version, url)

	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}

	tmpFile, err := os.CreateTemp(destDir, release.Binary+"-*")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	hasher := sha256.New()
	writer := io.MultiWriter(tmpFile, hasher)

	if _, err := io.Copy(writer, resp.Body); err != nil {
		tmpFile.Close()
		return fmt.Errorf("download write failed: %w", err)
	}
	tmpFile.Close()

	// Verify checksum if provided
	platform := runtime.GOOS + "/" + runtime.GOARCH
	if expectedHash, ok := release.Checksums[platform]; ok && expectedHash != "" {
		actualHash := hex.EncodeToString(hasher.Sum(nil))
		if actualHash != expectedHash {
			return fmt.Errorf("checksum mismatch for %s: expected %s, got %s",
				release.Binary, expectedHash, actualHash)
		}
	}

	// Extract from tar.gz if needed, otherwise treat as raw binary
	if strings.HasSuffix(url, ".tar.gz") || strings.HasSuffix(url, ".tgz") {
		if err := extractTarGz(tmpFile.Name(), release.Binary, destPath); err != nil {
			return fmt.Errorf("failed to extract archive: %w", err)
		}
	} else {
		// Raw binary — just move it
		if err := os.Chmod(tmpFile.Name(), 0755); err != nil {
			return fmt.Errorf("failed to set executable bit: %w", err)
		}
		if err := os.Rename(tmpFile.Name(), destPath); err != nil {
			return fmt.Errorf("failed to move binary to cache: %w", err)
		}
	}

	fmt.Printf("Installed %s %s at %s\n", release.Binary, release.Version, destPath)
	return nil
}

// extractTarGz extracts binaryName from a .tar.gz archive and writes it to destPath.
func extractTarGz(archivePath, binaryName, destPath string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar read: %w", err)
		}

		// Match the binary by base name (it may be nested in a directory)
		if filepath.Base(hdr.Name) == binaryName && hdr.Typeflag == tar.TypeReg {
			out, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
			if err != nil {
				return fmt.Errorf("create dest file: %w", err)
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return fmt.Errorf("extract binary: %w", err)
			}
			out.Close()
			return nil
		}
	}

	return fmt.Errorf("binary %q not found in archive", binaryName)
}

// installNPM installs a SCIP indexer via npm.
func (m *IndexerManager) installNPM(release *IndexerRelease) error {
	npmPath, err := exec.LookPath("npm")
	if err != nil {
		return fmt.Errorf("npm not found on PATH; install Node.js first")
	}

	pkg := release.Package + "@" + release.Version
	fmt.Printf("Installing %s via: npm install -g %s\n", release.Binary, pkg)

	cmd := exec.Command(npmPath, "install", "-g", pkg)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("npm install failed: %w", err)
	}
	return nil
}

// installCoursier installs a SCIP indexer via coursier bootstrap.
func (m *IndexerManager) installCoursier(release *IndexerRelease) error {
	csPath, err := exec.LookPath("cs")
	if err != nil {
		csPath, err = exec.LookPath("coursier")
		if err != nil {
			return fmt.Errorf("coursier (cs) not found on PATH; install from https://get-coursier.io")
		}
	}

	destDir := filepath.Join(m.cacheDir, string(release.Language), release.Version)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("failed to create cache directory: %w", err)
	}
	destPath := filepath.Join(destDir, release.Binary)

	coordinate := release.Package + ":" + release.Version
	fmt.Printf("Installing %s via: %s bootstrap %s\n", release.Binary, filepath.Base(csPath), coordinate)

	cmd := exec.Command(csPath, "bootstrap", "--standalone",
		"-o", destPath,
		coordinate,
		"--main", release.MainClass,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("coursier bootstrap failed: %w", err)
	}

	fmt.Printf("Installed %s %s at %s\n", release.Binary, release.Version, destPath)
	return nil
}

// installGoInstall installs a SCIP indexer via go install.
func (m *IndexerManager) installGoInstall(release *IndexerRelease) error {
	goPath, err := exec.LookPath("go")
	if err != nil {
		return fmt.Errorf("go not found on PATH; install Go first")
	}

	if release.FallbackCmd == "" {
		return fmt.Errorf("no go install command configured for %s", release.Binary)
	}

	fmt.Printf("Installing %s via: go install %s\n", release.Binary, release.FallbackCmd)

	// FallbackCmd is like "go install github.com/.../scip-go@v0.1.26"
	// Extract the package path (everything after "go install ")
	pkgPath := release.FallbackCmd
	pkgPath = strings.TrimPrefix(pkgPath, "go install ")

	cmd := exec.Command(goPath, "install", pkgPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go install failed: %w", err)
	}
	return nil
}

// installComposer installs a SCIP indexer via composer global require.
func (m *IndexerManager) installComposer(release *IndexerRelease) error {
	composerPath, err := exec.LookPath("composer")
	if err != nil {
		return fmt.Errorf("composer not found on PATH; install Composer first (https://getcomposer.org)")
	}

	pkg := release.Package + ":" + release.Version
	fmt.Printf("Installing %s via: composer global require %s\n", release.Binary, pkg)

	cmd := exec.Command(composerPath, "global", "require", pkg)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("composer global require failed: %w", err)
	}
	return nil
}

// installViaCommand runs the language's standard install command.
func (m *IndexerManager) installViaCommand(config *LanguageConfig) error {
	if config.InstallCommand == "" || strings.HasPrefix(config.InstallCommand, "See ") {
		return fmt.Errorf("no automated install available for %s. Please install manually: %s",
			config.DisplayName, config.InstallDocs)
	}

	fmt.Printf("Installing %s via: %s\n", config.SCIPBinary, config.InstallCommand)
	parts := strings.Fields(config.InstallCommand)
	cmd := exec.Command(parts[0], parts[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("install command failed: %w", err)
	}
	return nil
}

// resolveURL builds a platform-specific download URL from a release's URL pattern.
func (m *IndexerManager) resolveURL(release *IndexerRelease) string {
	if release.URL == "" {
		return ""
	}
	url := release.URL
	url = strings.ReplaceAll(url, "{os}", runtime.GOOS)
	url = strings.ReplaceAll(url, "{arch}", runtime.GOARCH)
	url = strings.ReplaceAll(url, "{binary}", release.Binary)
	url = strings.ReplaceAll(url, "{version}", release.Version)
	// {version_num} strips the leading "v" (e.g. "v0.1.26" -> "0.1.26")
	url = strings.ReplaceAll(url, "{version_num}", strings.TrimPrefix(release.Version, "v"))
	return url
}

func (m *IndexerManager) findRelease(lang Language) *IndexerRelease {
	for i := range m.releases {
		if m.releases[i].Language == lang {
			return &m.releases[i]
		}
	}
	return nil
}
