package static

import (
	"go/parser"
	"go/token"
	"testing"
)

// ── P2-4a: CTE-aware table extraction ────────────────────────────────────────

func TestFirstTableFromSQL_CTE(t *testing.T) {
	cases := []struct {
		name string
		sql  string
		want string
	}{
		{
			name: "plain select, no CTE",
			sql:  "SELECT * FROM balance WHERE id = $1",
			want: "balance",
		},
		{
			name: "leading CTE — outer FROM wins, not the CTE inner table",
			sql: `WITH bt_sum AS (
					SELECT account_id, SUM(amount) AS expected FROM balance_transaction GROUP BY account_id
				)
				SELECT b.account_id FROM balance b LEFT JOIN bt_sum ON b.account_id = bt_sum.account_id`,
			want: "balance",
		},
		{
			name: "CTE feeding an INSERT",
			sql:  `WITH input_data AS (SELECT 1) INSERT INTO outbound_webhook (x) SELECT x FROM input_data`,
			want: "outbound_webhook",
		},
		{
			name: "CTE feeding an UPDATE",
			sql:  `WITH ranked AS (SELECT id FROM events) UPDATE real_target SET y = 1 FROM ranked WHERE real_target.id = ranked.id`,
			want: "real_target",
		},
		{
			name: "multiple CTEs",
			sql:  `WITH a AS (SELECT * FROM inner_a), b AS (SELECT * FROM inner_b) SELECT * FROM main_tbl JOIN a JOIN b`,
			want: "main_tbl",
		},
		{
			name: "recursive CTE",
			sql:  `WITH RECURSIVE tree AS (SELECT * FROM nodes) SELECT * FROM hierarchy JOIN tree`,
			want: "hierarchy",
		},
		{
			name: "join-only alias skip (alias appears before real table)",
			sql:  `WITH cte1 AS (SELECT 1) SELECT * FROM cte1 JOIN real_one ON true`,
			// cte1 in the main FROM is a CTE alias → skipped; real_one wins.
			want: "real_one",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := firstTableFromSQL(c.sql); got != c.want {
				t.Errorf("firstTableFromSQL(%q) = %q, want %q", c.name, got, c.want)
			}
		})
	}
}

// ── P2-4b: fmt.Sprintf format-literal extraction ─────────────────────────────

func TestSprintfFormatLiteral_Table(t *testing.T) {
	cases := []struct {
		name   string
		src    string
		want   string
		wantOK bool
	}{
		{"sprintf update", `fmt.Sprintf("UPDATE collection_account SET %s", x)`, "UPDATE collection_account SET %s", true},
		{"sprintf backtick", "fmt.Sprintf(`SELECT * FROM foo WHERE %s`, w)", "SELECT * FROM foo WHERE %s", true},
		{"not sprintf", `strings.Join(a, b)`, "", false},
		{"sprintf non-literal first arg", `fmt.Sprintf(base, x)`, "", false},
		{"plain literal", `"SELECT 1"`, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e, err := parser.ParseExpr(c.src)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			got, ok := sprintfFormatLiteral(e)
			if ok != c.wantOK || got != c.want {
				t.Errorf("sprintfFormatLiteral(%q) = (%q,%v), want (%q,%v)", c.src, got, ok, c.want, c.wantOK)
			}
		})
	}
}

// ── P2-1: external-call registry wrapper coverage ────────────────────────────

func TestExternalRegistry_P2Wrappers(t *testing.T) {
	want := map[string][]string{
		"github.com/tazapay/grpc-framework/client/featureflag": {"IsEnabled"},
		"github.com/tazapay/grpc-framework/client/kms":         {"GetPublicKey", "SignWithECDSASHA256"},
		"github.com/tazapay/grpc-framework/client/cloudfront":  {"PutKV"},
		"github.com/tazapay/grpc-framework/client/webauthn":    {"BeginRegistration", "FinishRegistration", "BeginLogin", "FinishLogin", "BeginUsernamelessLogin", "FinishUsernamelessLogin"},
		"github.com/tazapay/grpc-framework/client/sms":         {"SendMessage", "IsNumberOptedOut", "OptInNumber", "GetSMSAttributes"},
		"github.com/tazapay/grpc-framework/client/recaptcha":   {"CreateAssessment"},
		"github.com/tazapay/grpc-framework/client/chrome":      {"Run"},
	}
	for path, funcs := range want {
		table, ok := externalCallRegistry[path]
		if !ok {
			t.Errorf("registry missing import path %q", path)
			continue
		}
		for _, fn := range funcs {
			if _, ok := table[fn]; !ok {
				t.Errorf("registry %q missing func %q", path, fn)
			}
		}
	}
	// session must NOT be registered (config loader only, no external op).
	if _, ok := externalCallRegistry["github.com/tazapay/grpc-framework/client/session"]; ok {
		t.Errorf("session should not be in the registry (constructor/config helper)")
	}
}

// ── P2-6: mongo collection resolution ────────────────────────────────────────

func TestResolveCollectionName(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		{`"payouts"`, "payouts"},
		{"`beneficiaries`", "beneficiaries"},
		{`opsenv.GetPayoutCollection()`, "payout"},
		{`opsenv.GetSettlementTransactionCollection()`, "settlement_transaction"},
		{`opsenv.GetBFITransactionCollection()`, "bfi_transaction"},
		{`opsenv.GetOpsDashboardDB()`, ""}, // not a *Collection getter
		{`somethingElse(x)`, ""},
	}
	for _, c := range cases {
		e, err := parser.ParseExpr(c.src)
		if err != nil {
			t.Fatalf("parse %q: %v", c.src, err)
		}
		if got := resolveCollectionName(e); got != c.want {
			t.Errorf("resolveCollectionName(%q) = %q, want %q", c.src, got, c.want)
		}
	}
}

func TestBeginFile_MongoCollection(t *testing.T) {
	parseFile := func(src string) *DBCallDetector {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, "x.go", src, 0)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		d := &DBCallDetector{}
		d.BeginFile(f)
		return d
	}

	// Single collection via env getter, with the options.Collection() builder present —
	// the builder must NOT be mistaken for a collection handle.
	single := `package mongo
func NewPayout(client *mongo.Client) *payout {
	col := client.Database(opsenv.GetOpsDashboardDB()).
		Collection(opsenv.GetPayoutCollection(), options.Collection())
	return &payout{col: col}
}`
	if got := parseFile(single).mongoFileCollection; got != "payout" {
		t.Errorf("single-collection file resolved %q, want %q", got, "payout")
	}

	// Ambiguous: two distinct collections in one file → empty.
	ambiguous := `package mongo
func A(c *mongo.Client) { _ = c.Database(d).Collection(opsenv.GetPayoutCollection()) }
func B(c *mongo.Client) { _ = c.Database(d).Collection(opsenv.GetBeneficiaryCollection()) }`
	if got := parseFile(ambiguous).mongoFileCollection; got != "" {
		t.Errorf("ambiguous file resolved %q, want empty", got)
	}

	// No mongo collection at all.
	none := `package p
func F() { _ = options.Collection() }`
	if got := parseFile(none).mongoFileCollection; got != "" {
		t.Errorf("no-collection file resolved %q, want empty", got)
	}
}
