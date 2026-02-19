package static

import (
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

// IndexerRelease describes a downloadable SCIP indexer binary.
type IndexerRelease struct {
	Language Language
	Binary   string // e.g. "scip-go"
	Version  string // e.g. "v0.1.26"
	// URL pattern: {BaseURL}/{os}/{arch}/{binary} or full URL
	URL      string
	Checksum string // SHA-256 hex (optional; skip verification if empty)
}

// DefaultReleases returns the known-good release definitions for each language.
// URLs point to GitHub release assets; they may need updating when new versions ship.
func DefaultReleases() []IndexerRelease {
	return []IndexerRelease{
		{
			Language: LanguageGo,
			Binary:   "scip-go",
			Version:  "v0.1.26",
		},
		{
			Language: LanguageTypeScript,
			Binary:   "scip-typescript",
			Version:  "latest",
		},
		{
			Language: LanguagePython,
			Binary:   "scip-python",
			Version:  "latest",
		},
		{
			Language: LanguageJava,
			Binary:   "scip-java",
			Version:  "latest",
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
// If the binary is already installed from a URL, it downloads it.
// If no URL is configured, it falls back to the language's install command.
func (m *IndexerManager) Install(lang Language) error {
	config, err := GetLanguageConfig(lang)
	if err != nil {
		return fmt.Errorf("unsupported language: %s", lang)
	}

	release := m.findRelease(lang)
	if release == nil {
		return m.installViaCommand(config)
	}

	if release.URL != "" {
		return m.downloadAndCache(release)
	}

	// No download URL — fall back to the language's standard install command.
	return m.installViaCommand(config)
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
	if release.Checksum != "" {
		actualHash := hex.EncodeToString(hasher.Sum(nil))
		if actualHash != release.Checksum {
			return fmt.Errorf("checksum mismatch for %s: expected %s, got %s",
				release.Binary, release.Checksum, actualHash)
		}
	}

	// Make executable
	if err := os.Chmod(tmpFile.Name(), 0755); err != nil {
		return fmt.Errorf("failed to set executable bit: %w", err)
	}

	// Atomic rename to final path
	if err := os.Rename(tmpFile.Name(), destPath); err != nil {
		return fmt.Errorf("failed to move binary to cache: %w", err)
	}

	fmt.Printf("Installed %s %s at %s\n", release.Binary, release.Version, destPath)
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
