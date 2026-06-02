package static

import (
	"go/ast"
	goparser "go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

// RepoMethodInfo holds the DB info resolved from a pgx/*.go implementation method.
type RepoMethodInfo struct {
	Table     string // e.g. "collection_account_documents"
	Operation string // SELECT, INSERT, UPDATE, DELETE
}

// RepoSQLMap maps "InterfaceName.MethodName" → resolved DB info.
// Key uses the PascalCase interface name, e.g. "Account.GetByID".
type RepoSQLMap map[string]RepoMethodInfo

// ScanRepositorySQL pre-scans the service's pgx/ directory to build a lookup
// map of repository interface method → table + operation.
//
// It works for any Tazapay service that follows the standard layout:
//
//	<projectPath>/pgx/*.go  — implementation structs with pointer receivers
//
// If the pgx/ directory doesn't exist, an empty map is returned (non-fatal).
func ScanRepositorySQL(projectPath string) (RepoSQLMap, error) {
	pgxDir := filepath.Join(projectPath, "pgx")
	if _, err := os.Stat(pgxDir); os.IsNotExist(err) {
		return RepoSQLMap{}, nil
	}

	result := make(RepoSQLMap)

	entries, err := os.ReadDir(pgxDir)
	if err != nil {
		return result, err
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		path := filepath.Join(pgxDir, entry.Name())
		if err := scanPgxFile(path, result); err != nil {
			// Non-fatal: skip bad files and continue.
			continue
		}
	}

	return result, nil
}

// scanPgxFile parses a single pgx/*.go file and adds its method SQL info to m.
func scanPgxFile(path string, m RepoSQLMap) error {
	fset := token.NewFileSet()
	f, err := goparser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return err
	}

	// First pass: collect package-level const names → string values.
	// These are SQL strings that methods reference by name rather than inline.
	constValues := make(map[string]string)
	for _, decl := range f.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range genDecl.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range vs.Names {
				if i >= len(vs.Values) {
					continue
				}
				if s := extractStringLit(vs.Values[i]); s != "" {
					constValues[name.Name] = s
				}
			}
		}
	}

	// Second pass: for each function with a pointer receiver, extract SQL info.
	for _, decl := range f.Decls {
		funcDecl, ok := decl.(*ast.FuncDecl)
		if !ok || funcDecl.Recv == nil || len(funcDecl.Recv.List) == 0 || funcDecl.Body == nil {
			continue
		}

		receiverType := receiverTypeName(funcDecl.Recv.List[0])
		if receiverType == "" {
			continue
		}
		// Tazapay convention: interface name = title-case first char of receiver type.
		// e.g. "account" → "Account", "accountMetadata" → "AccountMetadata"
		interfaceName := strings.ToUpper(receiverType[:1]) + receiverType[1:]
		methodName := funcDecl.Name.Name

		table, operation := extractSQLInfoFromFunc(funcDecl, constValues)
		if table == "" && operation == "" {
			// No SQL found — keep prefix-inference as fallback (done in detector).
			continue
		}

		key := interfaceName + "." + methodName
		m[key] = RepoMethodInfo{Table: table, Operation: operation}
	}

	return nil
}

// receiverTypeName returns the base type name of a receiver field (strips pointer).
func receiverTypeName(field *ast.Field) string {
	switch t := field.Type.(type) {
	case *ast.StarExpr:
		if ident, ok := t.X.(*ast.Ident); ok {
			return ident.Name
		}
	case *ast.Ident:
		return t.Name
	}
	return ""
}

// extractSQLInfoFromFunc looks for SQL strings inside a function body and
// resolves table + operation. It checks:
//  1. Backtick/double-quote string literals in the body
//  2. Identifiers that reference a package-level const in constValues
func extractSQLInfoFromFunc(funcDecl *ast.FuncDecl, constValues map[string]string) (table, operation string) {
	ast.Inspect(funcDecl.Body, func(n ast.Node) bool {
		if table != "" {
			return false // found — stop early
		}

		var sqlStr string

		switch node := n.(type) {
		case *ast.BasicLit:
			if node.Kind == token.STRING {
				sqlStr = unquoteString(node.Value)
			}
		case *ast.Ident:
			// Could be a reference to a package-level const.
			if v, ok := constValues[node.Name]; ok {
				sqlStr = v
			}
		}

		if sqlStr == "" {
			return true
		}

		// Only treat long-ish strings with SQL keywords as SQL.
		upper := strings.ToUpper(sqlStr)
		if !strings.ContainsAny(upper, "SUID") { // SELECT/INSERT/UPDATE/DELETE
			return true
		}
		if m := tableFromSQL.FindStringSubmatch(sqlStr); len(m) > 1 {
			table = strings.ToLower(m[1])
			operation = inferOperationFromSQL(sqlStr)
		}
		return table == ""
	})
	return
}

// extractStringLit returns the raw string value of a *ast.BasicLit string node,
// or "" if the node is not a string literal.
func extractStringLit(expr ast.Expr) string {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return ""
	}
	return unquoteString(lit.Value)
}

// unquoteString strips surrounding backticks or double-quotes from a Go string literal.
func unquoteString(s string) string {
	if len(s) < 2 {
		return ""
	}
	if s[0] == '`' && s[len(s)-1] == '`' {
		return s[1 : len(s)-1]
	}
	if s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

