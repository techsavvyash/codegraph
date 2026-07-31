// walk.go extracts per-file named-declaration counts from a project
// directory using tree-sitter (internal/ingest/structure), mirroring the
// SCIP indexer's own file discovery so the census compares like with like:
// the same files the indexer would have seen, filtered the same way.
package census

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/context-maximiser/code-graph/internal/ingest/structure"
)

// excludedDirs mirrors internal/ingest/scip/scip_parser.go's
// shouldExcludePath exactly: dependency/generated/build-output directories
// and test *fixture data* (not test files themselves — src/**/*.test.ts
// stays in scope, same as indexing).
var excludedDirs = []string{
	"node_modules/", "vendor/", ".git/",
	".next/", ".nuxt/", ".svelte-kit/",
	"dist/", "build/",
	"target/",
	"venv/", ".venv/", "__pycache__/",
	"testdata/", "fixtures/",
}

func shouldExcludePath(relPath string) bool {
	// Use forward-slash form and a trailing slash on dir segments so a
	// substring check matches the same way scip_parser.go's does (a path
	// that merely mentions "build" as part of a longer segment name, e.g.
	// "rebuild/", must NOT match "build/").
	normalized := filepath.ToSlash(relPath) + "/"
	for _, dir := range excludedDirs {
		if strings.Contains(normalized, "/"+dir) || strings.HasPrefix(normalized, dir) {
			return true
		}
	}
	return false
}

// FileCensus is one file's tree-sitter-derived named-declaration count.
type FileCensus struct {
	// RelPath is POSIX-relative to the project root, matching the
	// convention Function/Method.filePath uses in the graph.
	RelPath string
	// Declared is the count of NAMED function-like declarations: function
	// declarations, class/object methods, and const-bound arrows/function
	// expressions. Anonymous callbacks (no bound name) are never graph
	// nodes and are deliberately excluded — counting them would flag every
	// file using .map/.forEach as a false whole-file/partial dropout.
	Declared int
	// HasErrors reports the file's parse tree contained ERROR/MISSING
	// nodes; Declared may undercount if a definition sits inside a damaged
	// region (structure.Extract's documented per-definition fallback
	// caveat), so callers should treat a WARN/FAIL on such files as lower
	// confidence, not necessarily indexer fault.
	HasErrors bool
}

// WalkProject walks projectRoot, applying the same exclusion rules as SCIP
// indexing (node_modules, vendor, build output, test fixture directories —
// see shouldExcludePath), parses every file with a registered tree-sitter
// grammar (internal/ingest/structure.ForFile — anything else, including Go,
// is silently skipped: Go is covered by the Go oracle, and unregistered
// extensions carry no structure signal at all), and returns one FileCensus
// per parsed file whose Declared count is > 0 additions of activity (files
// with zero named declarations are also returned, since a file that SHOULD
// have declarations but parses to zero is exactly what whole-file dropout
// detection needs to see — the caller decides significance by comparing
// against the graph, not by filtering here).
func WalkProject(projectRoot string) ([]FileCensus, error) {
	var out []FileCensus

	err := filepath.Walk(projectRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relPath, relErr := filepath.Rel(projectRoot, path)
		if relErr != nil {
			return relErr
		}
		if relPath == "." {
			return nil
		}
		relPosix := filepath.ToSlash(relPath)

		if info.IsDir() {
			if strings.HasPrefix(info.Name(), ".") && info.Name() != "." {
				return filepath.SkipDir
			}
			if shouldExcludePath(relPosix + "/") {
				return filepath.SkipDir
			}
			return nil
		}

		if shouldExcludePath(relPosix) {
			return nil
		}

		lang, ok := structure.ForFile(path)
		if !ok {
			return nil // no grammar wired (includes .go — covered by the Go oracle)
		}

		content, readErr := os.ReadFile(path)
		if readErr != nil {
			// Unreadable file (permissions, symlink race, etc.): skip
			// rather than fail the whole census run.
			return nil
		}

		fs, extractErr := structure.Extract(lang, content)
		if extractErr != nil {
			return nil
		}

		declared := 0
		for _, fn := range fs.Functions {
			if fn.Name != "" {
				declared++
			}
		}

		out = append(out, FileCensus{
			RelPath:   relPosix,
			Declared:  declared,
			HasErrors: fs.HasErrors,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
