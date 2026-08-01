package static

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// LanguageRoot pairs a detected language with the directory where it was found.
type LanguageRoot struct {
	Language Language
	Path     string // absolute path
}

// Language represents a supported programming language
type Language string

const (
	LanguageGo         Language = "go"
	LanguageTypeScript Language = "typescript"
	LanguageJavaScript Language = "javascript"
	LanguagePython     Language = "python"
	LanguageJava       Language = "java"
	LanguageScala      Language = "scala"
	LanguageKotlin     Language = "kotlin"
	LanguagePHP        Language = "php"
)

// LanguageConfig holds configuration for a specific language's SCIP indexer
type LanguageConfig struct {
	Name           Language
	DisplayName    string
	SCIPBinary     string
	FileExtensions []string
	InstallCommand string
	InstallDocs    string
	IndexFlags     []string
	DetectionFiles []string // Files that indicate this language (e.g., go.mod, package.json)
}

// LanguageRegistry holds all supported language configurations
var LanguageRegistry = map[Language]*LanguageConfig{
	LanguageGo: {
		Name:           LanguageGo,
		DisplayName:    "Go",
		SCIPBinary:     "scip-go",
		FileExtensions: []string{".go"},
		InstallCommand: "go install github.com/sourcegraph/scip-go/cmd/scip-go@latest",
		InstallDocs:    "https://github.com/sourcegraph/scip-go",
		IndexFlags:     []string{},
		DetectionFiles: []string{"go.mod", "go.sum"},
	},
	LanguageTypeScript: {
		Name:           LanguageTypeScript,
		DisplayName:    "TypeScript",
		SCIPBinary:     "scip-typescript",
		FileExtensions: []string{".ts", ".tsx"},
		InstallCommand: "npm install -g @sourcegraph/scip-typescript",
		InstallDocs:    "https://github.com/sourcegraph/scip-typescript",
		IndexFlags:     []string{"index"},
		DetectionFiles: []string{"tsconfig.json", "package.json"},
	},
	LanguageJavaScript: {
		Name:           LanguageJavaScript,
		DisplayName:    "JavaScript",
		SCIPBinary:     "scip-typescript", // TypeScript indexer handles JS too
		FileExtensions: []string{".js", ".jsx"},
		InstallCommand: "npm install -g @sourcegraph/scip-typescript",
		InstallDocs:    "https://github.com/sourcegraph/scip-typescript",
		IndexFlags:     []string{"index"},
		DetectionFiles: []string{"package.json"},
	},
	LanguagePython: {
		Name:           LanguagePython,
		DisplayName:    "Python",
		SCIPBinary:     "scip-python",
		FileExtensions: []string{".py"},
		InstallCommand: "npm install -g @sourcegraph/scip-python",
		InstallDocs:    "https://github.com/sourcegraph/scip-python",
		IndexFlags:     []string{"index"},
		DetectionFiles: []string{"requirements.txt", "pyproject.toml", "setup.py", "Pipfile"},
	},
	LanguageJava: {
		Name:           LanguageJava,
		DisplayName:    "Java",
		SCIPBinary:     "scip-java",
		FileExtensions: []string{".java"},
		InstallCommand: "See installation docs for build tool integration",
		InstallDocs:    "https://sourcegraph.github.io/scip-java/",
		IndexFlags:     []string{},
		DetectionFiles: []string{"pom.xml", "build.gradle", "build.gradle.kts"},
	},
	LanguageScala: {
		Name:           LanguageScala,
		DisplayName:    "Scala",
		SCIPBinary:     "scip-java", // scip-java handles Scala too
		FileExtensions: []string{".scala"},
		InstallCommand: "See installation docs for build tool integration",
		InstallDocs:    "https://sourcegraph.github.io/scip-java/",
		IndexFlags:     []string{},
		DetectionFiles: []string{"build.sbt", "build.gradle"},
	},
	LanguageKotlin: {
		Name:           LanguageKotlin,
		DisplayName:    "Kotlin",
		SCIPBinary:     "scip-java", // scip-java handles Kotlin too
		FileExtensions: []string{".kt", ".kts"},
		InstallCommand: "See installation docs for build tool integration",
		InstallDocs:    "https://sourcegraph.github.io/scip-java/",
		IndexFlags:     []string{},
		DetectionFiles: []string{"build.gradle.kts"},
	},
	LanguagePHP: {
		Name:           LanguagePHP,
		DisplayName:    "PHP",
		SCIPBinary:     "scip-php",
		FileExtensions: []string{".php"},
		InstallCommand: "composer global require davidrjenni/scip-php",
		InstallDocs:    "https://github.com/davidrjenni/scip-php",
		IndexFlags:     []string{},
		DetectionFiles: []string{"composer.json", "composer.lock"},
	},
}

// GetLanguageConfig retrieves configuration for a language
func GetLanguageConfig(lang Language) (*LanguageConfig, error) {
	config, ok := LanguageRegistry[lang]
	if !ok {
		return nil, fmt.Errorf("unsupported language: %s", lang)
	}
	return config, nil
}

// parseGoWork reads a go.work file and returns the module paths listed under
// the use (...) stanza, as written (e.g. ".", "./libs/core-models-go").
func parseGoWork(goWorkPath string) []string {
	data, err := os.ReadFile(goWorkPath)
	if err != nil {
		return nil
	}
	var paths []string
	inUse := false
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "use (":
			inUse = true
		case inUse && line == ")":
			inUse = false
		case inUse && line != "" && !strings.HasPrefix(line, "//"):
			paths = append(paths, line)
		}
	}
	return paths
}

// parsePnpmWorkspace reads a pnpm-workspace.yaml file and returns the absolute
// directory paths matched by its `packages:` globs (e.g. "apps/*"). We do not
// pull in a YAML library for one stanza — the format we care about is a flat
// list of strings, parsed line by line. Globs are expanded with filepath.Glob
// rooted at workspaceRoot.
func parsePnpmWorkspace(yamlPath, workspaceRoot string) []string {
	data, err := os.ReadFile(yamlPath)
	if err != nil {
		return nil
	}
	var matches []string
	inPackages := false
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		// New top-level key ends the packages: block.
		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") && strings.HasSuffix(trimmed, ":") {
			inPackages = trimmed == "packages:"
			continue
		}
		if !inPackages {
			continue
		}
		// Workspace entries are list items: `- "apps/*"` or `- apps/*`.
		if !strings.HasPrefix(trimmed, "-") {
			continue
		}
		entry := strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))
		entry = strings.Trim(entry, "\"'")
		if entry == "" || strings.HasPrefix(entry, "!") {
			// Negation patterns are workspace-feature edge cases; ignore for now.
			continue
		}
		globbed, err := filepath.Glob(filepath.Join(workspaceRoot, entry))
		if err != nil {
			continue
		}
		for _, m := range globbed {
			info, statErr := os.Stat(m)
			if statErr != nil || !info.IsDir() {
				continue
			}
			matches = append(matches, filepath.Clean(m))
		}
	}
	return matches
}

// workspacePackageLanguage classifies a workspace package directory by its
// manifest. Returns LanguageTypeScript when a tsconfig.json is present or
// package.json declares a typescript dependency (preferred for typed
// projects), LanguageJavaScript when only package.json exists, or "" when
// neither is present (skip).
func workspacePackageLanguage(pkgDir string) Language {
	if _, err := os.Stat(filepath.Join(pkgDir, "tsconfig.json")); err == nil {
		return LanguageTypeScript
	}
	if _, err := os.Stat(filepath.Join(pkgDir, "package.json")); err == nil {
		if packageJSONDeclaresTypeScript(pkgDir) {
			return LanguageTypeScript
		}
		return LanguageJavaScript
	}
	return ""
}

// packageJSONDeclaresTypeScript reports whether dir/package.json lists
// "typescript" under dependencies or devDependencies — the signal that a
// project without a root tsconfig.json (SvelteKit and friends generate
// theirs under a build directory) is still a TypeScript project.
func packageJSONDeclaresTypeScript(dir string) bool {
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return false
	}
	var pkg struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return false
	}
	_, inDeps := pkg.Dependencies["typescript"]
	_, inDev := pkg.DevDependencies["typescript"]
	return inDeps || inDev
}

// DetectAllLanguages walks the project directory tree and returns every
// (language, directory) root found. It is monorepo-aware:
//
//   - Go workspaces (go.work): each "use" entry becomes its own root; the
//     workspace root itself is skipped since it typically only contains test/
//     files. Nested go.mod files are suppressed via the normal dedup logic.
//
//   - JavaScript workspaces: TypeScript roots that are sub-directories of a
//     detected JavaScript root are suppressed, because the JavaScript pass
//     uses --pnpm-workspaces / --yarn-workspaces which already covers them.
//
//   - Same-directory dedup: when TypeScript and JavaScript are both found in
//     the same directory, TypeScript wins and JavaScript is suppressed.
//
// Directories named node_modules, .git, vendor, .venv, __pycache__, dist,
// build, .svelte-kit, .nx, bin, and tmp are skipped entirely. The walk is
// limited to four levels of nesting (relative to projectPath) to keep it fast.
func DetectAllLanguages(projectPath string) ([]LanguageRoot, error) {
	absRoot, err := filepath.Abs(projectPath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve project path: %w", err)
	}

	// Priority order: TypeScript before JavaScript so TS wins same-dir dedup.
	detectionOrder := []Language{
		LanguageGo,
		LanguageTypeScript,
		LanguageJavaScript,
		LanguagePython,
		LanguageJava,
		LanguageScala,
		LanguageKotlin,
		LanguagePHP,
	}

	skipDirs := map[string]bool{
		"node_modules": true, ".git": true, "vendor": true,
		".venv": true, "__pycache__": true, "dist": true,
		"build": true, ".svelte-kit": true, ".nx": true,
		"bin": true, "tmp": true, "coverage": true,
		// Test fixture trees are excluded from file extraction by
		// shouldExcludePath; keep root detection consistent so a fixture's
		// go.mod/package.json can't spawn a phantom Service.
		"testdata": true, "fixtures": true,
	}

	var roots []LanguageRoot
	// foundLangDirs tracks directories already claimed per language so that
	// nested sub-modules of the same language are not double-counted.
	foundLangDirs := make(map[Language][]string)

	// ── Go workspace pre-processing ─────────────────────────────────────────
	// If go.work exists at the project root, expand each "use" entry into its
	// own LanguageRoot and claim the workspace root for Go so the WalkDir loop
	// does not re-detect any Go root inside the workspace.
	goWorkPath := filepath.Join(absRoot, "go.work")
	if _, err := os.Stat(goWorkPath); err == nil {
		for _, usePath := range parseGoWork(goWorkPath) {
			if usePath == "." {
				// Workspace root only hosts test/ wrappers; skip.
				continue
			}
			absUsePath := filepath.Clean(filepath.Join(absRoot, usePath))
			roots = append(roots, LanguageRoot{Language: LanguageGo, Path: absUsePath})
		}
		// Claim the whole workspace root for Go so WalkDir skips nested go.mod dirs.
		foundLangDirs[LanguageGo] = append(foundLangDirs[LanguageGo], absRoot)
	}

	// ── pnpm/npm/yarn workspace pre-processing ─────────────────────────────
	// If pnpm-workspace.yaml exists at the project root, expand each package
	// glob into its own TypeScript/JavaScript LanguageRoot and claim the
	// workspace root for both languages. This prevents the root tsconfig from
	// being detected as a single TS root that swallows every workspace package
	// under one Service node (the chat-ui-rolled-into-codegraph symptom).
	pnpmWorkspacePath := filepath.Join(absRoot, "pnpm-workspace.yaml")
	if _, err := os.Stat(pnpmWorkspacePath); err == nil {
		workspacePkgs := 0
		for _, pkgDir := range parsePnpmWorkspace(pnpmWorkspacePath, absRoot) {
			lang := workspacePackageLanguage(pkgDir)
			if lang == "" {
				continue
			}
			roots = append(roots, LanguageRoot{Language: lang, Path: pkgDir})
			foundLangDirs[lang] = append(foundLangDirs[lang], pkgDir)
			workspacePkgs++
		}
		// Claim the workspace root for TS/JS so WalkDir does not re-detect
		// the empty root tsconfig as a phantom service — but ONLY when the
		// workspace actually declared packages. pnpm-workspace.yaml is also
		// used as a plain config file (onlyBuiltDependencies etc.) by
		// single-package projects like SvelteKit apps; claiming the root on
		// such a file made the whole project undetectable ("no supported
		// languages detected" on web/studio).
		if workspacePkgs > 0 {
			foundLangDirs[LanguageTypeScript] = append(foundLangDirs[LanguageTypeScript], absRoot)
			foundLangDirs[LanguageJavaScript] = append(foundLangDirs[LanguageJavaScript], absRoot)
		}
	}

	const maxSeparators = 3 // walk up to 4 levels deep (0-indexed)

	walkErr := filepath.WalkDir(absRoot, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return nil // skip unreadable entries
		}
		if !d.IsDir() {
			return nil
		}

		// Enforce depth limit.
		rel, _ := filepath.Rel(absRoot, path)
		if rel != "." && strings.Count(rel, string(filepath.Separator)) >= maxSeparators {
			return filepath.SkipDir
		}

		// Skip non-source directories.
		if skipDirs[d.Name()] {
			return filepath.SkipDir
		}

		for _, lang := range detectionOrder {
			config, err := GetLanguageConfig(lang)
			if err != nil {
				continue
			}

			// Skip if path is inside a directory already claimed for this language.
			nested := false
			for _, found := range foundLangDirs[lang] {
				if path == found || strings.HasPrefix(path, found+string(filepath.Separator)) {
					nested = true
					break
				}
			}
			if nested {
				continue
			}

			// Check detection files.
			detected := false
			for _, detectionFile := range config.DetectionFiles {
				if _, err := os.Stat(filepath.Join(path, detectionFile)); err != nil {
					continue
				}
				// TypeScript requires tsconfig.json — or a package.json that
				// declares a typescript dependency (frameworks like SvelteKit
				// generate their tsconfig under .svelte-kit/, so the root has
				// none; scip-typescript handles such projects fine).
				if lang == LanguageTypeScript {
					if _, err := os.Stat(filepath.Join(path, "tsconfig.json")); err != nil &&
						!packageJSONDeclaresTypeScript(path) {
						continue
					}
				}
				detected = true
				break
			}

			if detected {
				roots = append(roots, LanguageRoot{Language: lang, Path: path})
				foundLangDirs[lang] = append(foundLangDirs[lang], path)
			}
		}
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("failed to walk project directory: %w", walkErr)
	}

	// ── Post-walk deduplication ──────────────────────────────────────────────

	// Build index sets for the two JS/TS languages.
	tsRootPaths := make(map[string]bool)
	jsRootPaths := make(map[string]bool)
	for _, r := range roots {
		switch r.Language {
		case LanguageTypeScript:
			tsRootPaths[r.Path] = true
		case LanguageJavaScript:
			jsRootPaths[r.Path] = true
		}
	}

	// isInsideJSRoot reports whether path is a sub-directory of any JS root.
	isInsideJSRoot := func(path string) bool {
		for jsPath := range jsRootPaths {
			if strings.HasPrefix(path, jsPath+string(filepath.Separator)) {
				return true
			}
		}
		return false
	}

	var filtered []LanguageRoot
	for _, r := range roots {
		// Same-dir dedup: drop JS when TS is at the exact same directory.
		if r.Language == LanguageJavaScript && tsRootPaths[r.Path] {
			continue
		}
		// Workspace dedup: drop TS when inside a JS workspace root (the JS pass
		// uses --pnpm-workspaces / --yarn-workspaces and already covers the TS subdir).
		if r.Language == LanguageTypeScript && isInsideJSRoot(r.Path) {
			continue
		}
		filtered = append(filtered, r)
	}

	return filtered, nil
}

// DetectLanguage attempts to detect the primary language of a project
func DetectLanguage(projectPath string) (Language, error) {
	// Priority order for detection
	detectionOrder := []Language{
		LanguageGo,
		LanguageTypeScript,
		LanguageJavaScript,
		LanguagePython,
		LanguageJava,
		LanguageScala,
		LanguageKotlin,
	}

	for _, lang := range detectionOrder {
		config, err := GetLanguageConfig(lang)
		if err != nil {
			continue
		}

		// Check for detection files
		for _, detectionFile := range config.DetectionFiles {
			filePath := filepath.Join(projectPath, detectionFile)
			if _, err := os.Stat(filePath); err == nil {
				// Special case: distinguish TypeScript from JavaScript
				if lang == LanguageTypeScript {
					// Check if tsconfig.json exists
					tsconfigPath := filepath.Join(projectPath, "tsconfig.json")
					if _, err := os.Stat(tsconfigPath); err == nil {
						return LanguageTypeScript, nil
					}
					// If only package.json exists, might be JavaScript
					continue
				}
				return lang, nil
			}
		}
	}

	// Fallback: detect by counting file extensions
	extCounts := make(map[Language]int)

	err := filepath.Walk(projectPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors
		}
		if info.IsDir() {
			// Skip common directories
			dirName := info.Name()
			if dirName == "node_modules" || dirName == ".git" || dirName == "vendor" ||
				dirName == "testdata" || dirName == "fixtures" {
				return filepath.SkipDir
			}
			return nil
		}

		ext := strings.ToLower(filepath.Ext(path))
		for lang, config := range LanguageRegistry {
			for _, langExt := range config.FileExtensions {
				if ext == langExt {
					extCounts[lang]++
					break
				}
			}
		}
		return nil
	})

	if err != nil {
		return "", fmt.Errorf("failed to walk project directory: %w", err)
	}

	// Find language with most files
	var detectedLang Language
	maxCount := 0
	for lang, count := range extCounts {
		if count > maxCount {
			maxCount = count
			detectedLang = lang
		}
	}

	if maxCount == 0 {
		return "", fmt.Errorf("could not detect language for project at %s", projectPath)
	}

	return detectedLang, nil
}

// InferLanguageFromExtension infers language from file extension
func InferLanguageFromExtension(filePath string) string {
	ext := strings.ToLower(filepath.Ext(filePath))

	for _, config := range LanguageRegistry {
		for _, langExt := range config.FileExtensions {
			if ext == langExt {
				return config.DisplayName
			}
		}
	}

	return "unknown"
}

// GetSupportedLanguages returns a list of all supported languages
func GetSupportedLanguages() []Language {
	langs := make([]Language, 0, len(LanguageRegistry))
	for lang := range LanguageRegistry {
		langs = append(langs, lang)
	}
	return langs
}

// FormatLanguageList returns a formatted string of supported languages
func FormatLanguageList() string {
	var parts []string
	for _, config := range LanguageRegistry {
		parts = append(parts, string(config.Name))
	}
	return strings.Join(parts, ", ")
}
