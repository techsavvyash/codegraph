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
	"FindOne":        -1,
	"InsertOne":      -1,
	"InsertMany":     -1,
	"UpdateOne":      -1,
	"UpdateMany":     -1,
	"ReplaceOne":     -1,
	"DeleteOne":      -1,
	"DeleteMany":     -1,
	"Aggregate":      -1,
	"CountDocuments": -1,
	"FindOneAndUpdate": -1,
	"FindOneAndDelete": -1,
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

	// varDBType: local var name → db client kind ("pgx", "pgxpool", "sqlx", "gorm").
	// Reset per function.
	varDBType map[string]string

	// varStringValue: local var name → raw string value.
	// Used to track SQL strings assigned to local variables.
	varStringValue map[string]string
}

// NewDBCallDetector creates a detector scoped to a single service indexing run.
func NewDBCallDetector(client *neo4j.Client, serviceName string, scopeCtx models.ScopeContext) *DBCallDetector {
	return &DBCallDetector{
		client:         client,
		serviceName:    serviceName,
		scopeCtx:       scopeCtx,
		varDBType:      make(map[string]string),
		varStringValue: make(map[string]string),
	}
}

// SetCallNodeBuffer configures an optional shared call node buffer.
func (d *DBCallDetector) SetCallNodeBuffer(buf *callNodeBuffer) {
	d.callBuffer = buf
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

// isRepositoryPgxCall returns true if expr is `repository.Pgx()`.
func isRepositoryPgxCall(expr *ast.CallExpr) bool {
	sel, ok := expr.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Pgx" {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	return ok && ident.Name == "repository"
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
				if m := tableFromSQL.FindStringSubmatch(sqlStr); len(m) > 1 {
					table = m[1]
				}
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
			fieldLower := strings.ToLower(fieldSel.Sel.Name)
			if isDBFieldName(fieldLower) {
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
		return nil
	}

	sqlStr := ""
	if sqlArgIdx >= 0 && sqlArgIdx < len(callExpr.Args) {
		sqlStr = d.extractStringArgWithVars(callExpr, sqlArgIdx)
	}

	operation, table := parseSQL(sqlStr, methodName, callExpr, dbKind)
	if operation == "" {
		return nil // not a recognisable DB call
	}

	nodeKey := fmt.Sprintf("dbcall:%s:%s:%s:%d", d.scopeCtx.ScopeID, filePath, d.serviceName, line)

	mergeProps := map[string]any{"nodeKey": nodeKey}
	setProps := map[string]any{
		"nodeKey":      nodeKey,
		"serviceName":  d.serviceName,
		"table":        table,
		"operation":    operation,
		"queryPattern": sqlStr,
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

// isDBFieldName returns true if a struct field name looks like a DB client field.
func isDBFieldName(lower string) bool {
	for _, kw := range []string{"db", "pool", "repo", "store", "conn", "gorm", "sqlx", "col"} {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// parseSQL extracts the SQL operation and table name from a SQL string literal.
// Falls back to inferring operation from the method name when the SQL is empty or dynamic.
func parseSQL(sqlStr, methodName string, callExpr *ast.CallExpr, dbKind string) (operation, table string) {
	if sqlStr != "" {
		operation = inferOperationFromSQL(sqlStr)
		if m := tableFromSQL.FindStringSubmatch(sqlStr); len(m) > 1 {
			table = m[1]
		}
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
// Operation is inferred from the method name prefix (Get→SELECT, Save→INSERT, Update→UPDATE,
// Delete→DELETE); table name is derived from the repo name (snake_case).
func (d *DBCallDetector) writeTazapayRepoCall(
	ctx context.Context,
	callerFuncID, filePath string,
	line int,
	repoName, methodName string,
) error {
	operation := inferOperationFromRepoMethod(methodName)
	table := camelToSnake(repoName)

	nodeKey := fmt.Sprintf("dbcall:%s:%s:%s:%d", d.scopeCtx.ScopeID, filePath, d.serviceName, line)

	mergeProps := map[string]any{"nodeKey": nodeKey}
	setProps := map[string]any{
		"nodeKey":      nodeKey,
		"serviceName":  d.serviceName,
		"repo":         repoName,
		"table":        table,
		"operation":    operation,
		"queryPattern": repoName + "." + methodName,
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

// camelToSnake converts a CamelCase repo name to snake_case table name.
// "PayoutAttempt" → "payout_attempt", "BulkPayout" → "bulk_payout"
func camelToSnake(s string) string {
	var out []rune
	for i, r := range s {
		if i > 0 && r >= 'A' && r <= 'Z' {
			out = append(out, '_')
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
	nodeKey := fmt.Sprintf("dbcall:%s:%s:%s:%s:%d", d.scopeCtx.ScopeID, filePath, d.serviceName, dbKind, line)

	mergeProps := map[string]any{"nodeKey": nodeKey}
	setProps := map[string]any{
		"nodeKey":      nodeKey,
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

	return ""
}
