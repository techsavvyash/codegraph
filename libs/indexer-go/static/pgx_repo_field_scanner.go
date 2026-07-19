package static

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

// ScanPgxRepoFields walks every non-noisy Go file under projectPath and returns
// the set of struct FIELD NAMES that are typed *<pkg>.PgxRepo (or the non-pointer
// form) somewhere in the service. It is repo-wide so a repo chain rooted at a
// struct field — w.Repo.<Repo>.<Method>(ctx, ...) — resolves even when the struct
// is declared in a different file from the methods that use it (onboarding's
// Worker: field in kyb.go, calls in update_kyb.go). (P0-6)
//
// Ambiguity guard: a field name also used for a NON-PgxRepo-typed field anywhere
// is dropped from the result, so an unrelated `.Repo.` chain on a different struct
// can never be mis-attributed as a DB call. This mirrors the const-resolver's
// P3-2 rule — when a name is ambiguous, resolve nothing rather than guess.
func ScanPgxRepoFields(projectPath string) map[string]bool {
	pgxRepo := map[string]bool{} // field names seen typed PgxRepo
	other := map[string]bool{}   // field names seen typed as anything else

	_ = filepath.Walk(projectPath, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || info.IsDir() {
			return walkErr
		}
		if !strings.HasSuffix(path, ".go") || isNoisyFilePath(path) {
			return nil
		}
		fset := token.NewFileSet()
		astFile, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return nil // skip unparseable files, same as the detection walk
		}
		ast.Inspect(astFile, func(n ast.Node) bool {
			st, ok := n.(*ast.StructType)
			if !ok || st.Fields == nil {
				return true
			}
			for _, field := range st.Fields.List {
				isRepo := isPgxRepoType(field.Type)
				if len(field.Names) == 0 {
					// Embedded field — addressed by its type name.
					if isRepo {
						pgxRepo["PgxRepo"] = true
					}
					continue
				}
				for _, name := range field.Names {
					if name.Name == "_" {
						continue
					}
					if isRepo {
						pgxRepo[name.Name] = true
					} else {
						other[name.Name] = true
					}
				}
			}
			return true
		})
		return nil
	})

	out := make(map[string]bool, len(pgxRepo))
	for name := range pgxRepo {
		if !other[name] {
			out[name] = true
		}
	}
	return out
}
