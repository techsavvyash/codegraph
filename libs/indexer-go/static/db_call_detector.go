package static

import (
	"context"
	"fmt"
	"go/ast"
	"go/token"
	"log"
	"maps"
	"regexp"
	"strings"
	"time"

	models "github.com/context-maximiser/code-graph/libs/core-models-go"
	"github.com/context-maximiser/code-graph/libs/neo4j-go"
)

// tableFromSQL extracts the first table name from a SQL string.
// Matches FROM, INTO, UPDATE, and JOIN clauses.
var tableFromSQL = regexp.MustCompile(`(?i)\b(?:FROM|INTO|UPDATE|JOIN)\s+["` + "`" + `]?(\w+)["` + "`" + `]?`)

// firstTableFromSQL resolves the real target table from a SQL string, handling CTEs.
// A leading `WITH cte AS (...)` clause is stripped first (so the FROM/INTO/UPDATE of the
// MAIN statement wins, not the CTE's inner table), and any captured name that is itself a
// CTE alias is skipped. Returns "" when no table can be resolved. (P2-4a)
func firstTableFromSQL(sql string) string {
	mainSQL, aliases := stripLeadingCTE(sql)
	for _, m := range tableFromSQL.FindAllStringSubmatch(mainSQL, -1) {
		if len(m) > 1 && !aliases[strings.ToLower(m[1])] {
			return m[1]
		}
	}
	return ""
}

// stripLeadingCTE removes a leading `WITH ... AS (...)[, ... AS (...)]` common-table-
// expression clause from a SQL string, returning the main statement plus the set of CTE
// alias names (lowercased). If the SQL does not start with WITH it is returned unchanged.
// The scan tracks parenthesis depth: CTE aliases are the identifiers at depth 0 (right
// after WITH or a top-level comma); the main statement begins at the first depth-0 DML
// keyword that appears after a CTE body paren has closed.
func stripLeadingCTE(sql string) (string, map[string]bool) {
	aliases := make(map[string]bool)
	trimmed := strings.TrimLeft(sql, " \t\r\n")
	if len(trimmed) < 5 || !strings.EqualFold(trimmed[:4], "WITH") || !isSQLSpace(trimmed[4]) {
		return sql, aliases
	}

	runes := []rune(trimmed)
	n := len(runes)
	depth := 0
	closedAny := false  // at least one CTE body paren has closed
	expectAlias := true // next depth-0 identifier is a CTE alias
	i := 4              // just past "WITH"

	for i < n {
		c := runes[i]
		switch {
		case c == '(':
			depth++
			expectAlias = false
			i++
		case c == ')':
			if depth > 0 {
				depth--
				if depth == 0 {
					closedAny = true
				}
			}
			i++
		case depth == 0 && c == ',':
			expectAlias = true
			i++
		case depth == 0 && closedAny && isSQLMainKeywordAt(runes, i):
			return string(runes[i:]), aliases
		case depth == 0 && expectAlias && isIdentStartRune(c):
			start := i
			for i < n && isIdentRune(runes[i]) {
				i++
			}
			word := string(runes[start:i])
			// "RECURSIVE" is a modifier, not an alias; the real alias follows it.
			if !strings.EqualFold(word, "RECURSIVE") {
				aliases[strings.ToLower(word)] = true
				expectAlias = false
			}
		default:
			i++
		}
	}
	// No main statement found (malformed) — fall back to the original string.
	return sql, aliases
}

// isSQLMainKeywordAt reports whether a top-level DML keyword (SELECT/INSERT/UPDATE/DELETE)
// starts at position i, on a word boundary.
func isSQLMainKeywordAt(runes []rune, i int) bool {
	for _, kw := range []string{"SELECT", "INSERT", "UPDATE", "DELETE"} {
		kr := []rune(kw)
		if i+len(kr) > len(runes) {
			continue
		}
		if !strings.EqualFold(string(runes[i:i+len(kr)]), kw) {
			continue
		}
		// Word boundary: the char after the keyword must not be an identifier char.
		if i+len(kr) == len(runes) || !isIdentRune(runes[i+len(kr)]) {
			return true
		}
	}
	return false
}

func isSQLSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\r' || b == '\n'
}

func isIdentStartRune(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_'
}

func isIdentRune(r rune) bool {
	return isIdentStartRune(r) || (r >= '0' && r <= '9')
}

// sprintfFormatLiteral returns the format-string literal of a fmt.Sprintf(<lit>, ...) call
// expression, or ("", false) if expr is not such a call. Used so SQL built with Sprintf
// (27 pgx files across the fleet) resolves to a table via its static format string. (P2-4b)
func sprintfFormatLiteral(expr ast.Expr) (string, bool) {
	call, ok := expr.(*ast.CallExpr)
	if !ok || len(call.Args) == 0 {
		return "", false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Sprintf" {
		return "", false
	}
	if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "fmt" {
		return "", false
	}
	lit, ok := call.Args[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	val := lit.Value
	if len(val) >= 2 && (val[0] == '"' || val[0] == '`') {
		return val[1 : len(val)-1], true
	}
	return "", false
}

// dbQueryMethods are method names on DB client variables that take a SQL string.
// Maps method name → argument index of the SQL string (-1 means no direct SQL arg).
var dbQueryMethods = map[string]int{
	// pgx / pgxpool — signature: method(ctx, sql, args...)
	"Query":     1,
	"QueryRow":  1,
	"Exec":      1,
	"QueryFunc": 1,
	// sqlx — NamedExec/NamedQuery(ctx, sql, arg)
	"NamedExec":  1,
	"NamedQuery": 1,
	// sqlx without context — Queryx/QueryRowx/MustExec/Preparex(sql, args...)
	"Queryx":    0,
	"QueryRowx": 0,
	"MustExec":  0,
	"Preparex":  0,
	// database/sql
	"Prepare":      0,
	"QueryContext": 1,
	"ExecContext":  1,
	// sqlx Select/Get(ctx, &dest, sql, args...)
	"Select": 2,
	"Get":    2,
	// GORM ORM methods — no SQL arg; table inferred from struct type
	"Find":    -1,
	"First":   -1,
	"Last":    -1,
	"Take":    -1,
	"Create":  -1,
	"Save":    -1,
	"Delete":  -1,
	"Count":   -1,
	"Scan":    -1,
	"Updates": -1,
	"Update":  -1,
	// MongoDB driver methods on *mongo.Collection — no SQL arg
	"FindOne":           -1,
	"InsertOne":         -1,
	"InsertMany":        -1,
	"UpdateOne":         -1,
	"UpdateMany":        -1,
	"ReplaceOne":        -1,
	"DeleteOne":         -1,
	"DeleteMany":        -1,
	"Aggregate":         -1,
	"CountDocuments":    -1,
	"FindOneAndUpdate":  -1,
	"FindOneAndDelete":  -1,
	"FindOneAndReplace": -1,
}

// DBCallInfo holds a detected database call site.
type DBCallInfo struct {
	CallerFunc    string
	CallerNodeKey string
	Operation     string // SELECT, INSERT, UPDATE, DELETE
	Table         string
	QueryPattern  string
	FilePath      string
	Line          int
}

// DBCallDetector detects outbound database call sites in Go AST and writes
// DBCall nodes with CALLS_DB edges into Neo4j.
type DBCallDetector struct {
	client      *neo4j.Client
	serviceName string
	scopeCtx    models.ScopeContext
	callBuffer  *callNodeBuffer

	// repoSQLMap holds pre-scanned repository SQL info keyed by "Interface.Method".
	// Populated from ScanRepositorySQL before detection begins.
	repoSQLMap RepoSQLMap

	// varDBType: local var name → db client kind ("pgx", "pgxpool", "sqlx", "gorm").
	// Reset per function.
	varDBType map[string]string

	// varStringValue: local var name → raw string value.
	// Used to track SQL strings assigned to local variables.
	varStringValue map[string]string

	// mongoFileCollection is the single MongoDB collection name resolved for the file
	// currently being scanned, or "" when none/ambiguous. Populated by BeginFile.
	// File-scoped (NOT reset per function) because ops-dashboard keeps one collection
	// per file: the struct + constructor + methods all live together. (P2-6)
	mongoFileCollection string

	// P3-1 coverage telemetry. Accumulated across the whole run (NOT reset per function)
	// so the end-of-run report can show how many recognised DB query-method call sites
	// were dropped, and why — the P0-2/P0-3 silent-drop class made visible.
	// These counters cover ONLY the generic driver path (pgx/pgxpool/sqlx/gorm/mongo via a
	// tracked var or struct-field receiver). They deliberately do NOT count the Tazapay-repo
	// pattern (repo.<Repo>.<Method>) or pgxscan.Get/Select, which write DBCalls via separate
	// functions — so genericWritten reconciles as seen-untracked-unresolvedSQL, and the run's
	// TOTAL DBCall count (from the buffer) is generic + repo-pattern + pgxscan.
	//   statCandidatesSeen        — generic DB query methods invoked (Query/Exec/QueryRow/...)
	//   statGenericWritten        — generic candidates that produced a DBCall node
	//   statRejectedUntrackedRecv — dropped: receiver not bound to a DB client/repo (also
	//                               absorbs non-DB calls that merely share a method name like
	//                               Get/Select/Find/Save — expected, not a real miss)
	//   statRejectedUnresolvedSQL — dropped: SQL could not be parsed into an operation
	statCandidatesSeen        int
	statGenericWritten        int
	statRejectedUntrackedRecv int
	statRejectedUnresolvedSQL int
}

// Stats returns the P3-1 generic-driver-path DB coverage counters accumulated over the run:
// candidates seen, written, rejected-for-untracked-receiver, rejected-for-unresolvable-SQL.
func (d *DBCallDetector) Stats() (candidatesSeen, genericWritten, rejectedUntrackedReceiver, rejectedUnresolvedSQL int) {
	return d.statCandidatesSeen, d.statGenericWritten, d.statRejectedUntrackedRecv, d.statRejectedUnresolvedSQL
}

// NewDBCallDetector creates a detector scoped to a single service indexing run.
// Pass a RepoSQLMap (from ScanRepositorySQL) to resolve real table names from
// pgx/*.go implementations; pass nil to fall back to name-prefix inference.
func NewDBCallDetector(client *neo4j.Client, serviceName string, scopeCtx models.ScopeContext, repoSQLMap RepoSQLMap) *DBCallDetector {
	if repoSQLMap == nil {
		repoSQLMap = RepoSQLMap{}
	}
	return &DBCallDetector{
		client:         client,
		serviceName:    serviceName,
		scopeCtx:       scopeCtx,
		repoSQLMap:     repoSQLMap,
		varDBType:      make(map[string]string),
		varStringValue: make(map[string]string),
	}
}

// SetCallNodeBuffer configures an optional shared call node buffer.
func (d *DBCallDetector) SetCallNodeBuffer(buf *callNodeBuffer) {
	d.callBuffer = buf
}

// BeginFile resolves the MongoDB collection for the file about to be scanned and
// caches it in mongoFileCollection. It walks for `<x>.Database(...).Collection(<name>)`
// constructor calls (the mongo-driver idiom) and resolves <name> either from a string
// literal or, best-effort, from a `<pkg>.Get<Xxx>Collection()` env getter by stripping
// the Get/Collection affixes. If the file resolves to exactly one collection it is used;
// zero or multiple (ambiguous) → "". Call once per file before DetectInFunction. (P2-6)
func (d *DBCallDetector) BeginFile(astFile *ast.File) {
	d.mongoFileCollection = ""
	if astFile == nil {
		return
	}
	found := make(map[string]bool)
	ast.Inspect(astFile, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Collection" || len(call.Args) == 0 {
			return true
		}
		// Require the receiver to be a `.Database(...)` call so `options.Collection()`
		// (the options builder, not a real collection handle) is not mistaken for one.
		inner, ok := sel.X.(*ast.CallExpr)
		if !ok {
			return true
		}
		innerSel, ok := inner.Fun.(*ast.SelectorExpr)
		if !ok || innerSel.Sel.Name != "Database" {
			return true
		}
		if name := resolveCollectionName(call.Args[0]); name != "" {
			found[name] = true
		}
		return true
	})
	if len(found) == 1 {
		for name := range found {
			d.mongoFileCollection = name
		}
	}
}

// resolveCollectionName best-effort resolves a mongo collection name from the first
// argument of a `.Collection(<arg>)` call: a string literal is used verbatim; a
// `<pkg>.Get<Xxx>Collection()` getter yields snake_case("<Xxx>") (e.g.
// GetSettlementTransactionCollection → settlement_transaction). Anything else → "".
func resolveCollectionName(arg ast.Expr) string {
	if lit, ok := arg.(*ast.BasicLit); ok && lit.Kind == token.STRING {
		v := lit.Value
		if len(v) >= 2 && (v[0] == '"' || v[0] == '`') {
			return v[1 : len(v)-1]
		}
		return v
	}
	if call, ok := arg.(*ast.CallExpr); ok {
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
			g := sel.Sel.Name
			if strings.HasPrefix(g, "Get") && strings.HasSuffix(g, "Collection") && len(g) > len("GetCollection") {
				return camelToSnake(g[len("Get") : len(g)-len("Collection")])
			}
		}
	}
	return ""
}

// DetectInFunction walks funcDecl looking for outbound DB call sites and writes
// DBCall nodes plus CALLS_DB edges for each one found.
func (d *DBCallDetector) DetectInFunction(
	ctx context.Context,
	funcDecl *ast.FuncDecl,
	callerFuncID, filePath string,
	fset *token.FileSet,
) error {
	if funcDecl.Body == nil {
		return nil
	}
	if isNoisyFilePath(filePath) {
		return nil
	}

	// Invariant: reset per function so bindings from other functions don't leak.
	d.varDBType = make(map[string]string)
	d.varStringValue = make(map[string]string)

	// Pass 0 — bind *repository.PgxRepo parameters as tazapay-tx receivers.
	// 263+ non-test functions across the fleet take `repo *repository.PgxRepo` as a
	// parameter (settlement/utils, payment-router, account, payin, event, ...) and
	// call repo.<Repo>.<Method>() without ever binding it via BeginTx in-function.
	// Matched by TYPE NAME (PgxRepo), not the package alias, so aliased imports work.
	d.bindPgxRepoParams(funcDecl)

	// Pass 1 — collect variable → DB client type bindings and string variables.
	ast.Inspect(funcDecl.Body, func(n ast.Node) bool {
		if assign, ok := n.(*ast.AssignStmt); ok {
			d.processAssignment(assign)
			d.processStringAssignment(assign)
		}
		if decl, ok := n.(*ast.DeclStmt); ok {
			d.processDeclString(decl)
		}
		return true
	})

	// Pass 2 — detect call expressions on tracked variables (or struct fields).
	var firstErr error
	ast.Inspect(funcDecl.Body, func(n ast.Node) bool {
		callExpr, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		pos := fset.Position(callExpr.Pos())
		if err := d.processCallExpr(ctx, callExpr, callerFuncID, filePath, pos.Line); err != nil {
			if firstErr == nil {
				firstErr = err
			}
		}
		return true
	})

	return firstErr
}

// processAssignment records DB client constructor calls into varDBType.
func (d *DBCallDetector) processAssignment(assign *ast.AssignStmt) {
	if len(assign.Lhs) == 0 || len(assign.Rhs) == 0 {
		return
	}
	lhsIdent, ok := assign.Lhs[0].(*ast.Ident)
	if !ok {
		return
	}

	callExpr, ok := assign.Rhs[0].(*ast.CallExpr)
	if !ok {
		// Multi-return: e.g. pool, err := pgxpool.New(...)
		if len(assign.Rhs) == 1 {
			return
		}
		callExpr, ok = assign.Rhs[0].(*ast.CallExpr)
		if !ok {
			return
		}
	}

	sel, ok := callExpr.Fun.(*ast.SelectorExpr)
	if !ok {
		return
	}

	// Tazapay: repo, err := repository.Pgx().BeginTx(ctx)
	// sel.X is a CallExpr (repository.Pgx()), not an Ident, so handle before pkgIdent check.
	if sel.Sel.Name == "BeginTx" {
		if innerCall, ok := sel.X.(*ast.CallExpr); ok && isRepositoryPgxCall(innerCall) {
			d.varDBType[lhsIdent.Name] = "tazapay-tx"
			return
		}
	}

	// P0-3 — Tazapay: repo := repository.Pgx()  (bare accessor, no BeginTx).
	// Schedulers/helpers bind the repo once and issue repo.<Repo>.<Method> calls
	// without a transaction. detectTazapayRepoCall resolves those once repo is bound.
	if isRepositoryPgxCall(callExpr) {
		d.varDBType[lhsIdent.Name] = "tazapay-tx"
		return
	}

	pkgIdent, ok := sel.X.(*ast.Ident)
	if !ok {
		return
	}

	pkg := pkgIdent.Name
	fn := sel.Sel.Name

	switch {
	case pkg == "pgxpool" && fn == "New":
		d.varDBType[lhsIdent.Name] = "pgxpool"
	case pkg == "pgx" && fn == "Connect":
		d.varDBType[lhsIdent.Name] = "pgx"
	case pkg == "sqlx" && (fn == "Connect" || fn == "Open" || fn == "MustConnect" || fn == "MustOpen"):
		d.varDBType[lhsIdent.Name] = "sqlx"
	case pkg == "gorm" && fn == "Open":
		d.varDBType[lhsIdent.Name] = "gorm"
	case pkg == "sql" && (fn == "Open" || fn == "OpenDB"):
		d.varDBType[lhsIdent.Name] = "sql"
	}
}

// processStringAssignment tracks variable assignments where the RHS is a string literal.
func (d *DBCallDetector) processStringAssignment(assign *ast.AssignStmt) {
	if len(assign.Lhs) == 0 || len(assign.Rhs) == 0 {
		return
	}
	for i, lhs := range assign.Lhs {
		if i >= len(assign.Rhs) {
			break
		}
		if ident, ok := lhs.(*ast.Ident); ok {
			if lit, ok := assign.Rhs[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
				val := lit.Value
				if len(val) >= 2 && (val[0] == '"' || val[0] == '`') {
					d.varStringValue[ident.Name] = val[1 : len(val)-1]
				}
			} else if format, ok := sprintfFormatLiteral(assign.Rhs[i]); ok {
				// P2-4b: query := fmt.Sprintf("UPDATE collection_account SET %s ...", ...)
				// — track the static format string so the table still resolves.
				d.varStringValue[ident.Name] = format
			}
		}
	}
}

// processDeclString tracks variable declarations where the variable is assigned a string literal.
func (d *DBCallDetector) processDeclString(decl *ast.DeclStmt) {
	genDecl, ok := decl.Decl.(*ast.GenDecl)
	if !ok || genDecl.Tok != token.VAR {
		return
	}
	for _, spec := range genDecl.Specs {
		if valSpec, ok := spec.(*ast.ValueSpec); ok {
			for i, name := range valSpec.Names {
				if i < len(valSpec.Values) {
					if lit, ok := valSpec.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
						val := lit.Value
						if len(val) >= 2 && (val[0] == '"' || val[0] == '`') {
							d.varStringValue[name.Name] = val[1 : len(val)-1]
						}
					}
				}
			}
		}
	}
}

// isRepositoryPgxCall returns true if expr is a call to Pgx() on the repository
// package or field. Accepts both `repository.Pgx()` (Ident) and `d.repository.Pgx()`
// (SelectorExpr whose rightmost identifier is "repository").
func isRepositoryPgxCall(expr *ast.CallExpr) bool {
	sel, ok := expr.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Pgx" {
		return false
	}
	switch x := sel.X.(type) {
	case *ast.Ident:
		return x.Name == "repository"
	case *ast.SelectorExpr:
		return x.Sel.Name == "repository"
	}
	return false
}

// bindPgxRepoParams binds every function parameter typed *<pkg>.PgxRepo (or the
// non-pointer <pkg>.PgxRepo) as a "tazapay-tx" receiver so that repo.<Repo>.<Method>
// calls inside the function are recognised, exactly as if the repo had been bound
// via repository.Pgx().BeginTx(...) in-function. Match is on the type NAME PgxRepo,
// not the package alias, so aliased repository imports are still caught.
func (d *DBCallDetector) bindPgxRepoParams(funcDecl *ast.FuncDecl) {
	if funcDecl.Type == nil || funcDecl.Type.Params == nil {
		return
	}
	for _, field := range funcDecl.Type.Params.List {
		if !isPgxRepoType(field.Type) {
			continue
		}
		// A single field may declare multiple names: (a, b *repository.PgxRepo).
		for _, name := range field.Names {
			if name.Name == "_" {
				continue
			}
			d.varDBType[name.Name] = "tazapay-tx"
		}
	}
}

// isPgxRepoType reports whether expr is *<pkg>.PgxRepo or <pkg>.PgxRepo, matching
// on the selector's type name only (package alias is irrelevant).
func isPgxRepoType(expr ast.Expr) bool {
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	return sel.Sel.Name == "PgxRepo"
}

// detectTazapayRepoCall checks whether callExpr matches the Tazapay repository chain pattern:
//   - repository.Pgx().<RepoName>.<MethodName>(ctx, ...)
//   - repo.<RepoName>.<MethodName>(ctx, ...)  where repo is tracked as "tazapay-tx"
//
// Returns repo name, method name, and whether the pattern matched.
func (d *DBCallDetector) detectTazapayRepoCall(sel *ast.SelectorExpr) (repoName, methodName string, ok bool) {
	methodName = sel.Sel.Name

	// sel.X must be a SelectorExpr representing <root>.<RepoName>
	fieldSel, isSel := sel.X.(*ast.SelectorExpr)
	if !isSel {
		return
	}
	repoName = fieldSel.Sel.Name

	switch inner := fieldSel.X.(type) {
	case *ast.CallExpr:
		// repository.Pgx().<RepoName>.<MethodName>
		if isRepositoryPgxCall(inner) {
			ok = true
		}
	case *ast.Ident:
		// repo.<RepoName>.<MethodName> where repo was bound via BeginTx
		if d.varDBType[inner.Name] == "tazapay-tx" {
			ok = true
		}
	}
	return
}

// processCallExpr detects a DB method call on a tracked variable or a struct field
// whose name contains common DB field names (db, repo, store, pool).
func (d *DBCallDetector) processCallExpr(
	ctx context.Context,
	callExpr *ast.CallExpr,
	callerFuncID, filePath string,
	line int,
) error {
	sel, ok := callExpr.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}

	// Tazapay repository pattern: repository.Pgx().<Repo>.<Method> or repo.<Repo>.<Method>
	// These use business-named methods (GetPayoutEvent, UpdateStatusByID, etc.) that are not
	// in dbQueryMethods, so we handle them before that check.
	if repoName, methodName, matched := d.detectTazapayRepoCall(sel); matched {
		return d.writeTazapayRepoCall(ctx, callerFuncID, filePath, line, repoName, methodName)
	}

	// pgxscan package functions: pgxscan.Get(ctx, querier, &dest, query, args...)
	// pgxscan.Select(ctx, querier, &dest, query, args...) — SQL is at argument index 3.
	if pkgIdent, ok := sel.X.(*ast.Ident); ok && pkgIdent.Name == "pgxscan" {
		method := sel.Sel.Name
		if method == "Get" || method == "Select" {
			sqlStr := d.extractStringArgWithVars(callExpr, 3)
			table := ""
			if sqlStr != "" {
				table = firstTableFromSQL(sqlStr)
			}
			return d.writeRawDBCall(ctx, callerFuncID, filePath, line, "SELECT", table, sqlStr, "pgxscan")
		}
		return nil
	}

	methodName := sel.Sel.Name
	sqlArgIdx, isQueryMethod := dbQueryMethods[methodName]
	if !isQueryMethod {
		return nil
	}
	d.statCandidatesSeen++ // P3-1: a recognised DB query method invocation.

	var dbKind string
	detected := false

	// Primary path: receiver is a simple local variable in varDBType.
	if recv, ok := sel.X.(*ast.Ident); ok {
		if kind, tracked := d.varDBType[recv.Name]; tracked {
			dbKind = kind
			detected = true
		}
	}

	// Secondary path: struct field access like r.db.Query(...) or s.pool.Exec(...).
	if !detected {
		if fieldSel, ok := sel.X.(*ast.SelectorExpr); ok {
			if isDBFieldName(fieldSel.Sel.Name) {
				dbKind = "unknown"
				detected = true
			}
		}
	}

	// GORM ORM method detection — db.Model(&T{}).Find/Create/Save/Delete.
	// GORM calls are chained; the receiver is typically a *gorm.DB variable.
	if !detected {
		if recv, ok := sel.X.(*ast.Ident); ok {
			if d.varDBType[recv.Name] == "gorm" {
				dbKind = "gorm"
				detected = true
			}
		}
	}

	if !detected {
		d.statRejectedUntrackedRecv++ // P3-1: DB method on an unbound receiver — dropped.
		return nil
	}

	sqlStr := ""
	if sqlArgIdx >= 0 && sqlArgIdx < len(callExpr.Args) {
		sqlStr = d.extractStringArgWithVars(callExpr, sqlArgIdx)
	}

	operation, table := parseSQL(sqlStr, methodName, callExpr, dbKind)
	if operation == "" {
		d.statRejectedUnresolvedSQL++ // P3-1: SQL not parseable into an operation — dropped.
		return nil                    // not a recognisable DB call
	}

	// P2-6: a mongo-specific method on a struct-field receiver is a MongoDB call.
	// Tag dbKind="mongo" and fill table with the file's resolved collection (mongo
	// method names other than the gorm-ambiguous "Find" are unambiguous).
	mongoKind := false
	if methodName != "Find" && mongoMethodToOperation(methodName) != "" && dbKind == "unknown" {
		mongoKind = true
		if table == "" {
			table = d.mongoFileCollection
		}
	}

	// P2-5: include method+table so two distinct DB calls on one source line
	// (e.g. a ternary or chained statement) do not MERGE into a single node.
	nodeKey := fmt.Sprintf("dbcall:%s:%s:%s:%d:%s:%s", d.scopeCtx.ScopeID, filePath, d.serviceName, line, methodName, table)

	mergeProps := map[string]any{"nodeKey": nodeKey}
	setProps := map[string]any{
		"nodeKey":      nodeKey,
		"name":         methodName,
		"serviceName":  d.serviceName,
		"table":        table,
		"operation":    operation,
		"queryPattern": sqlStr,
		"filePath":     filePath,
		"line":         line,
		"createdAt":    time.Now().UTC().Unix(),
		"updatedAt":    time.Now().UTC().Unix(),
	}
	if mongoKind {
		setProps["dbKind"] = "mongo"
	}
	maps.Copy(setProps, d.scopeCtx.Props())

	d.statGenericWritten++ // P3-1: generic-path candidate resolved to a DBCall node.

	if d.callBuffer != nil {
		d.callBuffer.addDBCall(nodeKey, setProps)
		d.callBuffer.addCallsDBEdge(callerFuncID, nodeKey, map[string]any{"line": line})
		return nil
	}

	dbCallID, err := d.client.MergeNode(ctx, []string{"DBCall"}, mergeProps, setProps)
	if err != nil {
		return fmt.Errorf("db call detector: merge DBCall node at %s:%d: %w", filePath, line, err)
	}

	if _, err := d.client.MergeRelationship(ctx,
		callerFuncID, dbCallID,
		string(models.CallsDBRel),
		map[string]any{},
		map[string]any{"line": line},
	); err != nil {
		log.Printf("Warning: db detector: CALLS_DB edge at %s:%d: %v", filePath, line, err)
	}

	return nil
}

// dbFieldTokens are the exact identifier tokens that mark a struct field as a DB
// client / collection handle. Matched token-exactly (not substring) so "collection",
// "protocol", "columnName" no longer false-positive on "col", and "database" does not
// hit "db". (P2-6)
var dbFieldTokens = map[string]bool{
	"db": true, "pool": true, "repo": true, "store": true,
	"conn": true, "gorm": true, "sqlx": true, "col": true,
}

// isDBFieldName returns true if a struct field name (original case) contains a DB-field
// token as a whole camelCase/underscore-delimited word — e.g. "db", "colSecondary"
// (→ col), "userStore" (→ store) match; "collectionAccount", "protocol" do not.
func isDBFieldName(name string) bool {
	for _, tok := range splitIdentTokens(name) {
		if dbFieldTokens[tok] {
			return true
		}
	}
	return false
}

// splitIdentTokens splits an identifier into lowercased word tokens on camelCase and
// underscore boundaries, reusing the acronym-aware camelToSnake rule.
func splitIdentTokens(name string) []string {
	return strings.Split(camelToSnake(name), "_")
}

// parseSQL extracts the SQL operation and table name from a SQL string literal.
// Falls back to inferring operation from the method name when the SQL is empty or dynamic.
func parseSQL(sqlStr, methodName string, callExpr *ast.CallExpr, dbKind string) (operation, table string) {
	if sqlStr != "" {
		operation = inferOperationFromSQL(sqlStr)
		table = firstTableFromSQL(sqlStr)
		if operation != "" {
			return
		}
	}

	// GORM ORM method → infer operation and table from method + first struct arg.
	if dbKind == "gorm" {
		operation = gormMethodToOperation(methodName)
		if operation != "" {
			table = extractGORMTable(callExpr)
			return
		}
	}

	// MongoDB driver method on *mongo.Collection (field named "col" or similar).
	// dbKind is "unknown" when detected via struct field name heuristic.
	if op := mongoMethodToOperation(methodName); op != "" {
		return op, ""
	}

	// For sqlx Select/Get: infer table from SQL or struct type arg.
	if methodName == "Select" || methodName == "Get" {
		operation = "SELECT"
		return
	}

	return "", ""
}

// inferOperationFromSQL returns the SQL DML keyword (uppercase) or "".
func inferOperationFromSQL(sql string) string {
	upper := strings.TrimSpace(strings.ToUpper(sql))
	for _, op := range []string{"SELECT", "INSERT", "UPDATE", "DELETE", "WITH"} {
		if strings.HasPrefix(upper, op) {
			if op == "WITH" {
				return "SELECT" // CTEs are always selects in practice
			}
			return op
		}
	}
	return ""
}

// gormMethodToOperation maps GORM chainable method names to SQL operations.
func gormMethodToOperation(method string) string {
	switch method {
	case "Find", "First", "Last", "Take", "Pluck", "Count", "Scan":
		return "SELECT"
	case "Create", "Save", "CreateInBatches":
		return "INSERT"
	case "Updates", "Update", "UpdateColumn", "UpdateColumns":
		return "UPDATE"
	case "Delete", "Unscoped":
		return "DELETE"
	}
	return ""
}

// extractGORMTable tries to infer the table name from a GORM Model(&T{}) or
// db.Find(&[]T{}) argument by reading the pointer-to-struct type name.
func extractGORMTable(callExpr *ast.CallExpr) string {
	for _, arg := range callExpr.Args {
		unary, ok := arg.(*ast.UnaryExpr)
		if !ok {
			continue
		}
		switch t := unary.X.(type) {
		case *ast.CompositeLit:
			switch typ := t.Type.(type) {
			case *ast.Ident:
				return strings.ToLower(typ.Name)
			case *ast.SelectorExpr:
				return strings.ToLower(typ.Sel.Name)
			case *ast.ArrayType:
				if ident, ok := typ.Elt.(*ast.Ident); ok {
					return strings.ToLower(ident.Name)
				}
			}
		case *ast.Ident:
			return strings.ToLower(t.Name)
		}
	}
	return ""
}

// writeTazapayRepoCall creates a DBCall node for Tazapay's repository.Pgx().<Repo>.<Method> pattern.
// Table and operation are resolved from the pre-scanned RepoSQLMap when available;
// otherwise they fall back to name-prefix inference.
func (d *DBCallDetector) writeTazapayRepoCall(
	ctx context.Context,
	callerFuncID, filePath string,
	line int,
	repoName, methodName string,
) error {
	operation := inferOperationFromRepoMethod(methodName)
	table := camelToSnake(repoName)

	var repositoryFilePath string
	// Override with real SQL-derived values if the pre-scanner found this method.
	if info, ok := d.repoSQLMap[repoName+"."+methodName]; ok {
		if info.Table != "" {
			table = info.Table
		}
		if info.Operation != "" {
			operation = info.Operation
		}
		repositoryFilePath = info.FilePath
	}

	// P2-5: include repo+method so two repo calls on one source line stay distinct.
	nodeKey := fmt.Sprintf("dbcall:%s:%s:%s:%d:%s:%s", d.scopeCtx.ScopeID, filePath, d.serviceName, line, repoName, methodName)

	mergeProps := map[string]any{"nodeKey": nodeKey}
	setProps := map[string]any{
		"nodeKey":             nodeKey,
		"name":                methodName,
		"serviceName":         d.serviceName,
		"repositoryInterface": repoName,
		"repositoryMethod":    methodName,
		"table":               table,
		"operation":           operation,
		"queryPattern":        repoName + "." + methodName,
		"filePath":            filePath,
		"line":                line,
		"createdAt":           time.Now().UTC().Unix(),
		"updatedAt":           time.Now().UTC().Unix(),
	}
	if repositoryFilePath != "" {
		setProps["repositoryFilePath"] = repositoryFilePath
	}
	maps.Copy(setProps, d.scopeCtx.Props())

	if d.callBuffer != nil {
		d.callBuffer.addDBCall(nodeKey, setProps)
		d.callBuffer.addCallsDBEdge(callerFuncID, nodeKey, map[string]any{"line": line})
		return nil
	}

	dbCallID, err := d.client.MergeNode(ctx, []string{"DBCall"}, mergeProps, setProps)
	if err != nil {
		return fmt.Errorf("db call detector: merge tazapay repo call at %s:%d: %w", filePath, line, err)
	}

	if _, err := d.client.MergeRelationship(ctx,
		callerFuncID, dbCallID,
		string(models.CallsDBRel),
		map[string]any{},
		map[string]any{"line": line},
	); err != nil {
		log.Printf("Warning: db detector: CALLS_DB edge at %s:%d: %v", filePath, line, err)
	}

	return nil
}

// inferOperationFromRepoMethod maps common method name prefixes to SQL operations.
func inferOperationFromRepoMethod(method string) string {
	lower := strings.ToLower(method)
	switch {
	case strings.HasPrefix(lower, "get") || strings.HasPrefix(lower, "find") ||
		strings.HasPrefix(lower, "list") || strings.HasPrefix(lower, "fetch") ||
		strings.HasPrefix(lower, "search") || strings.HasPrefix(lower, "count"):
		return "SELECT"
	case strings.HasPrefix(lower, "upsert") || strings.HasPrefix(lower, "bulkupsert") ||
		strings.HasPrefix(lower, "bulk_upsert"):
		return "UPSERT"
	case strings.HasPrefix(lower, "save") || strings.HasPrefix(lower, "create") ||
		strings.HasPrefix(lower, "insert") || strings.HasPrefix(lower, "add") ||
		strings.HasPrefix(lower, "bulksave") || strings.HasPrefix(lower, "bulk_save"):
		return "INSERT"
	case strings.HasPrefix(lower, "update") || strings.HasPrefix(lower, "set") ||
		strings.HasPrefix(lower, "patch") || strings.HasPrefix(lower, "bulkupdate") ||
		strings.HasPrefix(lower, "bulk_update"):
		return "UPDATE"
	case strings.HasPrefix(lower, "delete") || strings.HasPrefix(lower, "remove") ||
		strings.HasPrefix(lower, "purge"):
		return "DELETE"
	}
	return "QUERY"
}

// camelToSnake converts a CamelCase repo name to snake_case table name, treating
// consecutive capitals (acronyms) as a single token so they are not shattered:
//
//	"PayoutAttempt"  → "payout_attempt"   "BulkPayout"    → "bulk_payout"
//	"ForterAPITrace" → "forter_api_trace" "DLQEvent"      → "dlq_event"
//	"ConfigMFASMS"   → "config_mfasms"    "APIAccessControl" → "api_access_control"
//
// Rule: insert '_' before an uppercase letter only when the previous character is
// lowercase/digit (word boundary) OR the next character is lowercase (acronym→word,
// e.g. the "T" in "APITrace"). This is the standard camelCase→snake_case algorithm.
func camelToSnake(s string) string {
	runes := []rune(s)
	var out []rune
	for i, r := range runes {
		if i > 0 && r >= 'A' && r <= 'Z' {
			prev := runes[i-1]
			prevLowerOrDigit := (prev >= 'a' && prev <= 'z') || (prev >= '0' && prev <= '9')
			nextLower := i+1 < len(runes) && runes[i+1] >= 'a' && runes[i+1] <= 'z'
			if prevLowerOrDigit || nextLower {
				out = append(out, '_')
			}
		}
		out = append(out, []rune(strings.ToLower(string(r)))...)
	}
	return string(out)
}

// mongoMethodToOperation maps MongoDB driver method names to DML operation strings.
func mongoMethodToOperation(method string) string {
	switch method {
	case "FindOne", "Find", "Aggregate", "CountDocuments",
		"FindOneAndUpdate", "FindOneAndDelete", "FindOneAndReplace":
		return "SELECT"
	case "InsertOne", "InsertMany":
		return "INSERT"
	case "UpdateOne", "UpdateMany", "ReplaceOne":
		return "UPDATE"
	case "DeleteOne", "DeleteMany":
		return "DELETE"
	}
	return ""
}

// writeRawDBCall is a shared helper for writing a DBCall node and CALLS_DB edge
// when the operation and table are already resolved (pgxscan, mongo, etc.).
func (d *DBCallDetector) writeRawDBCall(
	ctx context.Context,
	callerFuncID, filePath string,
	line int,
	operation, table, queryPattern, dbKind string,
) error {
	// P2-5: include operation+table so two raw calls on one source line stay distinct.
	nodeKey := fmt.Sprintf("dbcall:%s:%s:%s:%s:%d:%s:%s", d.scopeCtx.ScopeID, filePath, d.serviceName, dbKind, line, operation, table)

	mergeProps := map[string]any{"nodeKey": nodeKey}
	setProps := map[string]any{
		"nodeKey":      nodeKey,
		"name":         dbKind + "." + operation,
		"serviceName":  d.serviceName,
		"table":        table,
		"operation":    operation,
		"queryPattern": queryPattern,
		"dbKind":       dbKind,
		"filePath":     filePath,
		"line":         line,
		"createdAt":    time.Now().UTC().Unix(),
		"updatedAt":    time.Now().UTC().Unix(),
	}
	maps.Copy(setProps, d.scopeCtx.Props())

	if d.callBuffer != nil {
		d.callBuffer.addDBCall(nodeKey, setProps)
		d.callBuffer.addCallsDBEdge(callerFuncID, nodeKey, map[string]any{"line": line})
		return nil
	}

	dbCallID, err := d.client.MergeNode(ctx, []string{"DBCall"}, mergeProps, setProps)
	if err != nil {
		return fmt.Errorf("db call detector: merge DBCall node at %s:%d: %w", filePath, line, err)
	}
	if _, err := d.client.MergeRelationship(ctx,
		callerFuncID, dbCallID,
		string(models.CallsDBRel),
		map[string]any{},
		map[string]any{"line": line},
	); err != nil {
		log.Printf("Warning: db detector: CALLS_DB edge at %s:%d: %v", filePath, line, err)
	}
	return nil
}

// extractStringArgWithVars safely extracts a string from a call argument, including
// tracking back to a locally assigned variable in varStringValue if it's an identifier.
func (d *DBCallDetector) extractStringArgWithVars(callExpr *ast.CallExpr, n int) string {
	if n >= len(callExpr.Args) {
		return ""
	}
	arg := callExpr.Args[n]

	// Case 1: Direct string literal
	if lit, ok := arg.(*ast.BasicLit); ok && lit.Kind == token.STRING {
		val := lit.Value
		if len(val) >= 2 && (val[0] == '"' || val[0] == '`') {
			return val[1 : len(val)-1]
		}
		return val
	}

	// Case 2: Local string variable reference (e.g. query := "...")
	if ident, ok := arg.(*ast.Ident); ok {
		if val, exists := d.varStringValue[ident.Name]; exists {
			return val
		}
	}

	// Case 3: Inline fmt.Sprintf("SELECT ... FROM x ...", ...) — use the format literal. (P2-4b)
	if format, ok := sprintfFormatLiteral(arg); ok {
		return format
	}

	return ""
}
