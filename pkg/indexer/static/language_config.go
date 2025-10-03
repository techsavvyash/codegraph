package static

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

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
	Name            Language
	DisplayName     string
	SCIPBinary      string
	FileExtensions  []string
	InstallCommand  string
	InstallDocs     string
	IndexFlags      []string
	DetectionFiles  []string // Files that indicate this language (e.g., go.mod, package.json)
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
		InstallCommand: "pip install scip-python",
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
		InstallCommand: "composer require --dev davidrjenni/scip-php",
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
			if dirName == "node_modules" || dirName == ".git" || dirName == "vendor" {
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
