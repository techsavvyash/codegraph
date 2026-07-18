package static

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	models "github.com/context-maximiser/code-graph/libs/core-models-go"
)

// newTestDBDetector creates a detector with a nil client (safe for parse-only tests).
func newTestDBDetector() *DBCallDetector {
	return NewDBCallDetector(nil, "test-service", models.DefaultScope(), nil)
}

// ── processAssignment unit tests ─────────────────────────────────────────────

func TestDBDetector_ProcessAssignment_pgxpool(t *testing.T) {
	src := `package p
import "github.com/jackc/pgx/v5/pgxpool"
func f(ctx interface{}) {
	pool, _ := pgxpool.New(ctx, "postgres://localhost/db")
	_ = pool
}`
	fn, _ := parseFirstFuncBody(t, src)
	d := newTestDBDetector()
	for _, a := range collectAssignStmts(fn) {
		d.processAssignment(a)
	}
	if d.varDBType["pool"] != "pgxpool" {
		t.Errorf("expected pool → pgxpool, got %q", d.varDBType["pool"])
	}
}

func TestDBDetector_ProcessAssignment_sqlx(t *testing.T) {
	src := `package p
import "github.com/jmoiron/sqlx"
func f() {
	db, _ := sqlx.Connect("postgres", "postgres://localhost/db")
	_ = db
}`
	fn, _ := parseFirstFuncBody(t, src)
	d := newTestDBDetector()
	for _, a := range collectAssignStmts(fn) {
		d.processAssignment(a)
	}
	if d.varDBType["db"] != "sqlx" {
		t.Errorf("expected db → sqlx, got %q", d.varDBType["db"])
	}
}

func TestDBDetector_ProcessAssignment_gorm(t *testing.T) {
	src := `package p
import "gorm.io/gorm"
func f() {
	db, _ := gorm.Open(nil, nil)
	_ = db
}`
	fn, _ := parseFirstFuncBody(t, src)
	d := newTestDBDetector()
	for _, a := range collectAssignStmts(fn) {
		d.processAssignment(a)
	}
	if d.varDBType["db"] != "gorm" {
		t.Errorf("expected db → gorm, got %q", d.varDBType["db"])
	}
}

func TestDBDetector_ProcessAssignment_sqlOpen(t *testing.T) {
	src := `package p
import "database/sql"
func f() {
	db, _ := sql.Open("postgres", "postgres://localhost/db")
	_ = db
}`
	fn, _ := parseFirstFuncBody(t, src)
	d := newTestDBDetector()
	for _, a := range collectAssignStmts(fn) {
		d.processAssignment(a)
	}
	if d.varDBType["db"] != "sql" {
		t.Errorf("expected db → sql, got %q", d.varDBType["db"])
	}
}

// ── P0-2: *repository.PgxRepo parameter binding ───────────────────────────────

// runDBDetectorOverFile parses src, runs DetectInFunction over every function, and
// returns the buffered DBCall props. Used for full-pipeline (param-binding) tests.
func runDBDetectorOverFile(t *testing.T, src string) []map[string]any {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "test.go", src, 0)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	buf := newCallNodeBuffer(models.DefaultScope().ScopeID)
	d := newTestDBDetector()
	d.SetCallNodeBuffer(buf)
	for _, decl := range f.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok {
			if err := d.DetectInFunction(context.Background(), fn, "caller-elem-id", "utils/settlement.go", fset); err != nil {
				t.Fatalf("DetectInFunction: %v", err)
			}
		}
	}
	out := make([]map[string]any, 0, len(buf.dbCalls))
	for _, props := range buf.dbCalls {
		out = append(out, props)
	}
	return out
}

func TestDBDetector_PgxRepoParam_Pointer(t *testing.T) {
	// The settlement/utils/settlement.go:66 shape: repo *repository.PgxRepo passed
	// in, used as repo.<Repo>.<Method> with no in-function BeginTx binding.
	src := `package p
import (
	"context"
	"github.com/tazapay/settlement/repository"
)
func DeleteQueueRecord(ctx context.Context, repo *repository.PgxRepo, ids []string) error {
	_ = repo.QueueRemark.DeleteByIDs(ctx, ids)
	_ = repo.Queue.DeleteByIDs(ctx, ids)
	return nil
}`
	calls := runDBDetectorOverFile(t, src)
	if len(calls) != 2 {
		t.Fatalf("expected 2 DBCall nodes for repo.<Repo>.<Method>, got %d", len(calls))
	}
	repos := map[string]bool{}
	for _, c := range calls {
		repos[c["repositoryInterface"].(string)] = true
		if c["repositoryMethod"] != "DeleteByIDs" {
			t.Errorf("repositoryMethod = %v, want DeleteByIDs", c["repositoryMethod"])
		}
	}
	if !repos["QueueRemark"] || !repos["Queue"] {
		t.Errorf("expected QueueRemark and Queue repositories, got %v", repos)
	}
}

func TestDBDetector_PgxRepoParam_NonPointerAndAliased(t *testing.T) {
	// Non-pointer repository.PgxRepo and an aliased import must both bind — the
	// match is on the type NAME PgxRepo, not the package alias.
	src := `package p
import (
	"context"
	repo2 "github.com/tazapay/account/repository"
)
func f(ctx context.Context, r repo2.PgxRepo, id string) {
	_ = r.Account.GetByID(ctx, id)
}`
	calls := runDBDetectorOverFile(t, src)
	if len(calls) != 1 {
		t.Fatalf("expected 1 DBCall node for aliased non-pointer PgxRepo, got %d", len(calls))
	}
	if calls[0]["repositoryInterface"] != "Account" {
		t.Errorf("repositoryInterface = %v, want Account", calls[0]["repositoryInterface"])
	}
}

func TestDBDetector_PgxRepoParam_NoLeakAcrossFunctions(t *testing.T) {
	// Binding must not leak: a function WITHOUT a PgxRepo param that reuses the
	// name `repo` for something else must produce no DBCall.
	src := `package p
import (
	"context"
	"github.com/tazapay/settlement/repository"
)
func withRepo(ctx context.Context, repo *repository.PgxRepo) {
	_ = repo.Queue.DeleteByIDs(ctx)
}
func withoutRepo(ctx context.Context, repo string) {
	_ = repo
}`
	calls := runDBDetectorOverFile(t, src)
	if len(calls) != 1 {
		t.Fatalf("expected exactly 1 DBCall (only withRepo), got %d", len(calls))
	}
}

// ── P0-3: bare `repo := repository.Pgx()` assignment binding ──────────────────

func TestDBDetector_ProcessAssignment_barePgx(t *testing.T) {
	// settlement/service/scheduler/scheduler.go:38 shape: repo := repository.Pgx()
	// with no BeginTx must still bind repo as a tazapay-tx receiver.
	src := `package p
import "github.com/tazapay/settlement/repository"
func f() {
	repo := repository.Pgx()
	_ = repo
}`
	fn, _ := parseFirstFuncBody(t, src)
	d := newTestDBDetector()
	for _, a := range collectAssignStmts(fn) {
		d.processAssignment(a)
	}
	if d.varDBType["repo"] != "tazapay-tx" {
		t.Errorf("expected repo → tazapay-tx, got %q", d.varDBType["repo"])
	}
}

func TestDBDetector_ProcessAssignment_barePgxFieldReceiver(t *testing.T) {
	// The `d.repository.Pgx()` selector-receiver form (isRepositoryPgxCall handles it).
	src := `package p
func f(d any) {
	repo := d.repository.Pgx()
	_ = repo
}`
	fn, _ := parseFirstFuncBody(t, src)
	det := newTestDBDetector()
	for _, a := range collectAssignStmts(fn) {
		det.processAssignment(a)
	}
	if det.varDBType["repo"] != "tazapay-tx" {
		t.Errorf("expected repo → tazapay-tx, got %q", det.varDBType["repo"])
	}
}

func TestDBDetector_BarePgx_EmitsDBCall(t *testing.T) {
	// Full pipeline: bare repo := repository.Pgx() then repo.<Repo>.<Method> yields a DBCall.
	src := `package p
import (
	"context"
	"github.com/tazapay/settlement/repository"
)
func schedule(ctx context.Context) {
	repo := repository.Pgx()
	_ = repo.Queue.GetPending(ctx)
}`
	calls := runDBDetectorOverFile(t, src)
	if len(calls) != 1 {
		t.Fatalf("expected 1 DBCall from bare-repo binding, got %d", len(calls))
	}
	if calls[0]["repositoryInterface"] != "Queue" {
		t.Errorf("repositoryInterface = %v, want Queue", calls[0]["repositoryInterface"])
	}
}

func TestDBDetector_ProcessAssignment_noFalseBinding(t *testing.T) {
	// A generic X() call unrelated to repository.Pgx() must NOT bind as tazapay-tx.
	src := `package p
func f(svc any) {
	x := svc.Something()
	_ = x
}`
	fn, _ := parseFirstFuncBody(t, src)
	d := newTestDBDetector()
	for _, a := range collectAssignStmts(fn) {
		d.processAssignment(a)
	}
	if _, bound := d.varDBType["x"]; bound {
		t.Errorf("unrelated X() call must not bind, got %q", d.varDBType["x"])
	}
}

// ── P0-4(b): camelToSnake acronym handling ────────────────────────────────────

func TestCamelToSnake_Acronyms(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"PayoutAttempt", "payout_attempt"},
		{"BulkPayout", "bulk_payout"},
		{"ForterAPITrace", "forter_api_trace"},
		{"DLQEvent", "dlq_event"},
		{"ConfigMFASMS", "config_mfasms"},
		{"APIAccessControl", "api_access_control"},
		{"OutboxBFI", "outbox_bfi"},
		{"GPI", "gpi"},
		{"LookupVASP", "lookup_vasp"},
		{"MISData", "mis_data"},
		{"Account", "account"},
	}
	for _, c := range cases {
		if got := camelToSnake(c.in); got != c.want {
			t.Errorf("camelToSnake(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// ── inferOperationFromSQL unit tests ──────────────────────────────────────────

func TestInferOperationFromSQL(t *testing.T) {
	cases := []struct {
		sql  string
		want string
	}{
		{"SELECT * FROM payments", "SELECT"},
		{"select id from users where id = $1", "SELECT"},
		{"INSERT INTO orders (id) VALUES ($1)", "INSERT"},
		{"UPDATE payments SET status = $1 WHERE id = $2", "UPDATE"},
		{"DELETE FROM sessions WHERE expired_at < NOW()", "DELETE"},
		{"WITH cte AS (SELECT 1) SELECT * FROM cte", "SELECT"},
		{"", ""},
		{"EXPLAIN SELECT 1", ""},
	}
	for _, c := range cases {
		got := inferOperationFromSQL(c.sql)
		if got != c.want {
			t.Errorf("inferOperationFromSQL(%q) = %q, want %q", c.sql, got, c.want)
		}
	}
}

// ── tableFromSQL regex unit tests ─────────────────────────────────────────────

func TestTableFromSQL_Regex(t *testing.T) {
	cases := []struct {
		sql   string
		table string
	}{
		{"SELECT * FROM payments WHERE id = $1", "payments"},
		{"INSERT INTO orders (id) VALUES ($1)", "orders"},
		{"UPDATE users SET name = $1", "users"},
		{"DELETE FROM sessions WHERE id = $1", "sessions"},
		{"SELECT p.id FROM payments p JOIN transactions t ON p.id = t.payment_id", "payments"},
	}
	for _, c := range cases {
		m := tableFromSQL.FindStringSubmatch(c.sql)
		got := ""
		if len(m) > 1 {
			got = m[1]
		}
		if got != c.table {
			t.Errorf("tableFromSQL(%q) = %q, want %q", c.sql, got, c.table)
		}
	}
}

// ── gormMethodToOperation unit tests ──────────────────────────────────────────

func TestGORMMethodToOperation(t *testing.T) {
	cases := []struct {
		method string
		want   string
	}{
		{"Find", "SELECT"},
		{"First", "SELECT"},
		{"Create", "INSERT"},
		{"Save", "INSERT"},
		{"Updates", "UPDATE"},
		{"Delete", "DELETE"},
		{"Count", "SELECT"},
		{"NonExistent", ""},
	}
	for _, c := range cases {
		got := gormMethodToOperation(c.method)
		if got != c.want {
			t.Errorf("gormMethodToOperation(%q) = %q, want %q", c.method, got, c.want)
		}
	}
}

// ── isDBFieldName unit tests ──────────────────────────────────────────────────

func TestIsDBFieldName(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"db", true},
		{"pool", true},
		{"repo", true},
		{"store", true},
		{"conn", true},
		{"gorm", true},
		{"sqlx", true},
		// P2-6: matching is now token-exact on camelCase/underscore boundaries, not a
		// substring. camelCase and snake_case field names still resolve …
		{"pgPool", true},
		{"userRepo", true},
		{"colSecondary", true}, // ops-dashboard mongo secondary handle → col
		{"db_conn", true},
		// … but lowercase-concatenated blobs (the old substring false-positive class,
		// DB-8) and unrelated fields no longer match.
		{"pgpool", false},
		{"userrepo", false},
		{"collectionAccount", false}, // "collection" ≠ "col"
		{"protocol", false},
		{"client", false},
		{"handler", false},
		{"logger", false},
	}
	for _, c := range cases {
		got := isDBFieldName(c.name)
		if got != c.want {
			t.Errorf("isDBFieldName(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

// ── dbQueryMethods coverage test ─────────────────────────────────────────────

func TestDBQueryMethods_ContainsExpectedMethods(t *testing.T) {
	expected := []string{"Query", "Exec", "NamedExec", "Select", "Find", "Create", "Delete"}
	for _, m := range expected {
		if _, ok := dbQueryMethods[m]; !ok {
			t.Errorf("dbQueryMethods missing expected method %q", m)
		}
	}
}
