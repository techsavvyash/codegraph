package static

import (
	"go/ast"
	"go/parser"
	"os"
	"path/filepath"
	"testing"
)

func mustParseExpr(t *testing.T, src string) ast.Expr {
	t.Helper()
	expr, err := parser.ParseExpr(src)
	if err != nil {
		t.Fatalf("parse %q: %v", src, err)
	}
	return expr
}

// newTestConstResolver builds a resolver with a hand-populated const table mirroring the
// Tazapay settlement constants/env packages.
func newTestConstResolver(t *testing.T) *constResolver {
	t.Helper()
	r := &constResolver{values: map[string]ast.Expr{}}
	decls := map[string]string{
		"EventGroupSettlement":    `"settlement"`,
		"EventGroupPayout":        `"payout"`,
		"CharDot":                 `"."`,
		"EventActionFailed":       `"failed"`,
		"EventActionSuceeded":     `"succeeded"`,
		"EventPayoutAutoInitiate": `"payout.auto_initiate"`,
		"QueueEventURL":           `"queue.event.event"`,
		// transitive: composed from other consts
		"ComposedFailedEvent": `EventGroupSettlement + CharDot + EventActionFailed`,
	}
	for name, src := range decls {
		r.values[name] = mustParseExpr(t, src)
	}
	return r
}

func TestResolveString(t *testing.T) {
	r := newTestConstResolver(t)
	tests := []struct {
		name       string
		expr       string
		wantVal    string
		wantStatic bool
	}{
		{"string literal", `"settlement.failed"`, "settlement.failed", true},
		{"single const", `EventPayoutAutoInitiate`, "payout.auto_initiate", true},
		{"const + CharDot + const", `EventGroupSettlement + CharDot + EventActionFailed`, "settlement.failed", true},
		{"selector form", `constants.EventActionSuceeded`, "succeeded", true},
		{"env queue const", `QueueEventURL`, "queue.event.event", true},
		{"transitive const", `ComposedFailedEvent`, "settlement.failed", true},
		{"group + var (partial)", `EventGroupPayout + CharDot + payoutStatus`, "payout.", false},
		{"plain runtime var", `eventType`, "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			val, static := r.ResolveString(mustParseExpr(t, tc.expr))
			if val != tc.wantVal || static != tc.wantStatic {
				t.Errorf("ResolveString(%q) = (%q, %v), want (%q, %v)",
					tc.expr, val, static, tc.wantVal, tc.wantStatic)
			}
		})
	}
}

func TestQueueToService(t *testing.T) {
	tests := []struct {
		queue string
		want  string
	}{
		{"queue.settlement.payout", "settlement"},
		{"queue.event.event", "event"},
		{"dlq.balance.balance", "balance"},
		{"queue.settlement.initiatePayoutSettlementQueue", "settlement"},
		{"noconvention", ""},
		{"", ""},
		// P3-3: garbage inputs must yield "" rather than a nonsense segment.
		{"https://sqs.ap-south-1.amazonaws.com/123/queue", ""}, // parts[0] != queue/dlq
		{"queue.event_url", ""},                                // only two segments
		{"queue.Settlement.payout", ""},                        // uppercase service segment
		{"random.settlement.payout", ""},                       // wrong prefix
		{"dlq.balance", ""},                                    // two segments only
	}
	for _, tc := range tests {
		if got := QueueToService(tc.queue); got != tc.want {
			t.Errorf("QueueToService(%q) = %q, want %q", tc.queue, got, tc.want)
		}
	}
}

func TestSplitEvent(t *testing.T) {
	tests := []struct {
		val        string
		static     bool
		wantGroup  string
		wantAction string
		wantDyn    bool
		wantOK     bool
	}{
		{"settlement.failed", true, "settlement", "failed", false, true},
		{"payout.auto_initiate", true, "payout", "auto_initiate", false, true},
		{"settlement.", false, "settlement", "", true, true},
		{"payout.", false, "payout", "", true, true},
		{"nodot", true, "", "", false, false},
		{".action", true, "", "", false, false},
	}
	for _, tc := range tests {
		g, a, dyn, ok := splitEvent(tc.val, tc.static)
		if g != tc.wantGroup || a != tc.wantAction || dyn != tc.wantDyn || ok != tc.wantOK {
			t.Errorf("splitEvent(%q, %v) = (%q, %q, %v, %v), want (%q, %q, %v, %v)",
				tc.val, tc.static, g, a, dyn, ok, tc.wantGroup, tc.wantAction, tc.wantDyn, tc.wantOK)
		}
	}
}

func TestActionRouteGroup(t *testing.T) {
	tests := map[string]string{
		"settlementActionRoute": "settlement",
		"payinActionRoute":      "payin",
		"payoutActionRoute":     "payout",
		"eventRoutes":           "",
		"eventGroupRoute":       "",
		"ActionRoute":           "",
	}
	for name, want := range tests {
		if got := actionRouteGroup(name); got != want {
			t.Errorf("actionRouteGroup(%q) = %q, want %q", name, got, want)
		}
	}
}

// writeGoFile writes a minimal .go source file into dir for the collision-guard test.
func writeGoFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// TestConstResolver_CollisionGuard verifies P3-2: a bare const name declared with DIVERGENT
// string values across the repo is marked ambiguous and resolves to not-static, instead of
// silently picking the first-seen value. A name declared identically in two files is NOT
// flagged (idempotent duplicate).
func TestConstResolver_CollisionGuard(t *testing.T) {
	dir := t.TempDir()

	// "Failed" declared with two different values → ambiguous.
	writeGoFile(t, dir, "a.go", "package p\nconst Failed = \"failed\"\nconst Same = \"x\"\n")
	writeGoFile(t, dir, "b.go", "package p\nconst Failed = \"error\"\nconst Same = \"x\"\n")
	// "Ok" declared once → unaffected.
	writeGoFile(t, dir, "c.go", "package p\nconst Ok = \"ok\"\n")

	r := newConstResolver(dir)

	if got := r.AmbiguousCount(); got != 1 {
		t.Fatalf("AmbiguousCount() = %d, want 1 (only Failed diverges)", got)
	}

	// Ambiguous name resolves as a runtime var (not static).
	if val, static := r.ResolveString(&ast.Ident{Name: "Failed"}); static || val != "" {
		t.Errorf("ResolveString(Failed) = (%q, %v), want (\"\", false) — should be ambiguous", val, static)
	}

	// Identical duplicate is fine and still resolves.
	if val, static := r.ResolveString(&ast.Ident{Name: "Same"}); !static || val != "x" {
		t.Errorf("ResolveString(Same) = (%q, %v), want (\"x\", true)", val, static)
	}

	// Singleton const resolves normally.
	if val, static := r.ResolveString(&ast.Ident{Name: "Ok"}); !static || val != "ok" {
		t.Errorf("ResolveString(Ok) = (%q, %v), want (\"ok\", true)", val, static)
	}
}
