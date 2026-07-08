package static

import (
	"go/ast"
	"go/parser"
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
		"EventGroupSettlement": `"settlement"`,
		"EventGroupPayout":     `"payout"`,
		"CharDot":              `"."`,
		"EventActionFailed":    `"failed"`,
		"EventActionSuceeded":  `"succeeded"`,
		"EventPayoutAutoInitiate": `"payout.auto_initiate"`,
		"QueueEventURL":        `"queue.event.event"`,
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
