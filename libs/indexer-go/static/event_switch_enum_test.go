package static

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"testing"
)

// parseSingleFunc parses src (a full file with exactly one func) and returns its FuncDecl.
func parseSingleFunc(t *testing.T, src string) *ast.FuncDecl {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "x.go", src, 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok {
			return fn
		}
	}
	t.Fatalf("no func in src")
	return nil
}

// TestCollectLocalEventAssigns_SwitchEnumeration verifies that a `<group> + CharDot + <switchTag>`
// assignment inside a value switch is enumerated into one concrete event per case-label constant,
// instead of collapsing to a single dynamic group hub.
func TestCollectLocalEventAssigns_SwitchEnumeration(t *testing.T) {
	consts := &constResolver{values: map[string]ast.Expr{}}
	decls := map[string]string{
		"EventGroupPayout":          `"payout"`,
		"CharDot":                   `"."`,
		"EventActionCreated":        `"created"`,
		"EventActionScreening":      `"screening"`,
		"EventActionComplianceHold": `"compliance_hold"`,
	}
	for name, src := range decls {
		consts.values[name] = mustParseExpr(t, src)
	}

	r := &EventEmissionResolver{consts: consts}

	src := `package p
func TriggerPayoutEvent(ctx c, payoutID, payoutStatus string) error {
	var eventType string
	switch payoutStatus {
	case constants.EventActionCreated:
		eventType = constants.EventGroupPayout + constants.CharDot + constants.EventActionCreated
	case constants.EventActionScreening, constants.EventActionComplianceHold:
		eventType = constants.EventGroupPayout + constants.CharDot + payoutStatus
	default:
		return nil
	}
	_ = eventType
	return nil
}`
	fn := parseSingleFunc(t, src)

	got := r.collectLocalEventAssigns(fn)
	var events []string
	var dynamic bool
	for _, evs := range got {
		for _, ev := range evs {
			events = append(events, ev.event)
			if ev.dynamic {
				dynamic = true
			}
		}
	}
	sort.Strings(events)

	want := []string{"payout.compliance_hold", "payout.created", "payout.screening"}
	if len(events) != len(want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("events = %v, want %v", events, want)
		}
	}
	if dynamic {
		t.Fatalf("expected no dynamic group-hub emission, got one in %v", events)
	}
}

// TestCollectLocalEventAssigns_UnresolvableFallsBackToHub verifies that when the concatenated
// variable is NOT the switch tag (so no case label constrains it), the emission falls back to the
// dynamic group hub rather than being dropped.
func TestCollectLocalEventAssigns_UnresolvableFallsBackToHub(t *testing.T) {
	consts := &constResolver{values: map[string]ast.Expr{}}
	consts.values["EventGroupPayout"] = mustParseExpr(t, `"payout"`)
	consts.values["CharDot"] = mustParseExpr(t, `"."`)
	consts.values["EventActionCreated"] = mustParseExpr(t, `"created"`)

	r := &EventEmissionResolver{consts: consts}

	src := `package p
func f(ctx c, payoutStatus, other string) {
	var eventType string
	switch payoutStatus {
	case constants.EventActionCreated:
		eventType = constants.EventGroupPayout + constants.CharDot + other
	}
	_ = eventType
}`
	fn := parseSingleFunc(t, src)

	got := r.collectLocalEventAssigns(fn)
	var events []string
	for _, evs := range got {
		for _, ev := range evs {
			events = append(events, ev.event)
		}
	}
	if len(events) != 1 || events[0] != "payout.*" {
		t.Fatalf("events = %v, want [payout.*]", events)
	}
}
