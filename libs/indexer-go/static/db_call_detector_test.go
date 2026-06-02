package static

import (
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
		{"pgpool", true},
		{"repo", true},
		{"userrepo", true},
		{"store", true},
		{"conn", true},
		{"gorm", true},
		{"sqlx", true},
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
