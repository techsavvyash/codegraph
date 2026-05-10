package static

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"testing"

	"github.com/sourcegraph/scip/bindings/go/scip"
)

// TestInspectFixture runs scip-go on a fixture and prints the resulting index
// using the codegraph SCIPParser. Skipped unless INSPECT_FIXTURE env var is
// set to a path relative to the repo root (e.g. test/fixtures/tiny-go).
//
// Usage:
//
//	INSPECT_FIXTURE=test/fixtures/tiny-go go test -run TestInspectFixture -v ./libs/indexer-go/static/
func TestInspectFixture(t *testing.T) {
	rel := os.Getenv("INSPECT_FIXTURE")
	if rel == "" {
		t.Skip("set INSPECT_FIXTURE=<path-relative-to-repo-root> to run")
	}

	repoRoot := findRepoRoot(t)
	fixturePath := filepath.Join(repoRoot, rel)
	if _, err := os.Stat(fixturePath); err != nil {
		t.Fatalf("fixture not found at %s: %v", fixturePath, err)
	}

	scipFile := filepath.Join(t.TempDir(), "index.scip")
	cmd := exec.Command("scip-go", "--output", scipFile)
	cmd.Dir = fixturePath
	cmd.Env = append(os.Environ(), "GOWORK=off")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("scip-go failed: %v\n%s", err, out)
	}

	parser := NewSCIPParser()
	if err := parser.ParseFile(scipFile); err != nil {
		t.Fatalf("parse: %v", err)
	}

	meta := parser.GetMetadata()
	fmt.Println("=== METADATA ===")
	fmt.Printf("project_root=%s\n", meta.ProjectRoot)
	fmt.Printf("tool=%s %s\n", meta.ToolInfo.Name, meta.ToolInfo.Version)

	docs, err := parser.ExtractDocuments()
	if err != nil {
		t.Fatalf("extract docs: %v", err)
	}
	fmt.Printf("\n=== DOCUMENTS (%d) ===\n", len(docs))
	for _, d := range docs {
		fmt.Printf("  %s (lang=%s)\n", d.Path, d.Language)
	}

	syms, err := parser.ExtractSymbols()
	if err != nil {
		t.Fatalf("extract symbols: %v", err)
	}
	sort.Slice(syms, func(i, j int) bool {
		if syms[i].Info.FilePath != syms[j].Info.FilePath {
			return syms[i].Info.FilePath < syms[j].Info.FilePath
		}
		return syms[i].Symbol.Descriptor < syms[j].Symbol.Descriptor
	})
	fmt.Printf("\n=== SYMBOLS (%d) ===\n", len(syms))
	for _, s := range syms {
		fmt.Printf("  [%s] %s :: %s   (file=%s line=%d, refs=%d)\n",
			s.Info.Kind, s.Info.DisplayName, s.Symbol.Descriptor,
			s.Info.FilePath, s.Info.StartLine, len(s.Refs))
	}

	// Surface raw SCIP-level relationships (definitions vs references per occurrence).
	fmt.Println("\n=== RAW OCCURRENCES (per document) ===")
	for _, doc := range parser.index.Documents {
		fmt.Printf("  %s\n", doc.RelativePath)
		for _, occ := range doc.Occurrences {
			role := "ref"
			if occ.SymbolRoles&int32(scip.SymbolRole_Definition) != 0 {
				role = "DEF"
			}
			fmt.Printf("    %s %s\n", role, occ.Symbol)
		}
	}

	// External symbols + their relationships (this is where IMPLEMENTS/INHERITS lives).
	fmt.Printf("\n=== EXTERNAL SYMBOLS (%d) ===\n", len(parser.index.ExternalSymbols))
	for _, ext := range parser.index.ExternalSymbols {
		fmt.Printf("  [%s] %s\n", ext.Kind, ext.Symbol)
		for _, rel := range ext.Relationships {
			tags := []string{}
			if rel.IsImplementation {
				tags = append(tags, "impl")
			}
			if rel.IsReference {
				tags = append(tags, "ref")
			}
			if rel.IsTypeDefinition {
				tags = append(tags, "typedef")
			}
			if rel.IsDefinition {
				tags = append(tags, "def")
			}
			fmt.Printf("      -> %s [%v]\n", rel.Symbol, tags)
		}
	}

	// In-document SymbolInformation (where most relationships live for owned symbols).
	fmt.Println("\n=== DOCUMENT SYMBOL INFO ===")
	for _, doc := range parser.index.Documents {
		if len(doc.Symbols) == 0 {
			continue
		}
		fmt.Printf("  %s\n", doc.RelativePath)
		for _, si := range doc.Symbols {
			fmt.Printf("    [%s] %s\n", si.Kind, si.Symbol)
			for _, rel := range si.Relationships {
				tags := []string{}
				if rel.IsImplementation {
					tags = append(tags, "impl")
				}
				if rel.IsReference {
					tags = append(tags, "ref")
				}
				if rel.IsTypeDefinition {
					tags = append(tags, "typedef")
				}
				if rel.IsDefinition {
					tags = append(tags, "def")
				}
				fmt.Printf("      -> %s [%v]\n", rel.Symbol, tags)
			}
		}
	}
}

func findRepoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	// file is libs/indexer-go/static/fixture_inspect_test.go — walk up to repo root.
	dir := filepath.Dir(file)
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("could not find repo root from %s", file)
	return ""
}
