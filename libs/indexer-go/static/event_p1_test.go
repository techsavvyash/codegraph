package static

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"testing"
)

// newP1TestResolver builds an EventEmissionResolver with a const table covering the event
// names/queues used by the P1 tests.
func newP1TestResolver(t *testing.T) *EventEmissionResolver {
	t.Helper()
	consts := &constResolver{values: map[string]ast.Expr{}}
	decls := map[string]string{
		"Settlement":              `"settlement"`,
		"Payout":                  `"payout"`,
		"CharDot":                 `"."`,
		"VAResolved":              `"va_resolved"`,
		"Screening":               `"screening"`,
		"Created":                 `"created"`,
		"PayoutComplianceFailed":  `"payout.compliance_failed"`,
		"CollectComplianceFailed": `"collect.compliance_failed"`,
		"EventQueue":              `"queue.event.event"`,
		"SettlementQueue":         `"queue.settlement.settlement"`,
	}
	for name, src := range decls {
		consts.values[name] = mustParseExpr(t, src)
	}
	return &EventEmissionResolver{
		consts:        consts,
		emitters:      map[string]emitterMeta{},
		emissions:     map[string][]eventEmission{},
		msgRelays:     map[string]msgRelayMeta{},
		handlerEvents: map[string][]string{},
	}
}

// runProducerPasses runs the direct-producer attribution passes (Pass A + transitive relays +
// Pass B) over files, mirroring the producer half of NewEventEmissionResolver.
func runProducerPasses(r *EventEmissionResolver, files []parsedFile) {
	r.passAClassifyEmitters(files)
	r.resolveTransitiveRelays(files)
	r.passBAttributeEmissions(files)
}

func parseP1Files(t *testing.T, srcs map[string]string) []parsedFile {
	t.Helper()
	names := make([]string, 0, len(srcs))
	for n := range srcs {
		names = append(names, n)
	}
	sort.Strings(names)
	var out []parsedFile
	for _, name := range names {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, name, srcs[name], 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		out = append(out, parsedFile{relPath: name, fset: fset, file: f})
	}
	return out
}

// TestQueueReceiverAliases verifies that the queue-client package is recognised under an explicit
// alias and unaliased (default name), while blank/dot imports and unrelated packages are excluded.
func TestQueueReceiverAliases(t *testing.T) {
	src := `package p
import (
	sqsqueue "github.com/tazapay/grpc-framework/client/queue"
	"github.com/tazapay/grpc-framework/client/queue"
	_ "github.com/tazapay/grpc-framework/client/queue"
	"github.com/other/pkg"
)`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "x.go", src, 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := queueReceiverAliases(f)
	if !got["sqsqueue"] || !got["queue"] {
		t.Fatalf("expected {sqsqueue, queue}, got %v", got)
	}
	if got["pkg"] || got["_"] {
		t.Fatalf("unexpected alias captured: %v", got)
	}
}

// TestP1_1_AliasedDirectProducer proves an aliased `sqsqueue.SendSQSMsg` send (the fleet's 122
// aliased queue imports, e.g. settlement/service/grpc/v1/create_adjustment.go) is attributed
// exactly like the canonical `queue.SendSQSMsg`.
func TestP1_1_AliasedDirectProducer(t *testing.T) {
	r := newP1TestResolver(t)
	files := parseP1Files(t, map[string]string{
		"produce.go": `package p
import sqsqueue "github.com/tazapay/grpc-framework/client/queue"
func triggerVAResolved(ctx c) error {
	qMsg := &sqsqueue.AsyncMessage{
		EventType: constants.Settlement + constants.CharDot + constants.VAResolved,
		Data:      map[string]any{},
	}
	return sqsqueue.SendSQSMsg(ctx, fenv.Get(env.SettlementQueue), qMsg)
}`,
	})
	runProducerPasses(r, files)

	got := emissionEvents(r.EmissionsFor("produce.go", "triggerVAResolved"))
	if len(got) != 1 || got[0] != "settlement.va_resolved→settlement" {
		t.Fatalf("aliased-send emissions = %v, want [settlement.va_resolved→settlement]", got)
	}
}

// TestP1_2_RestampDirectProducer proves the create-then-restamp producer shape (monitoring/
// utils/event.go TriggerTMSComplianceFailedEvent): a bare &queue.AsyncMessage{Data:…} whose
// EventType is set on a following line via `qMsg.EventType = …`, then sent directly. Both static
// restamps must be attributed; before P1-2 the send was silently dropped (no EventType in literal).
func TestP1_2_RestampDirectProducer(t *testing.T) {
	r := newP1TestResolver(t)
	files := parseP1Files(t, map[string]string{
		"produce.go": `package p
import "github.com/tazapay/grpc-framework/client/queue"
func triggerComplianceFailed(ctx c, txnID string) error {
	qMsg := &queue.AsyncMessage{ Data: make(map[string]any) }
	switch {
	case strings.HasPrefix(txnID, "pt_"):
		qMsg.EventType = constants.PayoutComplianceFailed
	case strings.HasPrefix(txnID, "co_"):
		qMsg.EventType = constants.CollectComplianceFailed
	}
	return queue.SendSQSMsg(ctx, fenv.Get(env.EventQueue), qMsg)
}`,
	})
	runProducerPasses(r, files)

	got := emissionEvents(r.EmissionsFor("produce.go", "triggerComplianceFailed"))
	want := []string{"collect.compliance_failed→event", "payout.compliance_failed→event"}
	if len(got) != len(want) {
		t.Fatalf("restamp emissions = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("restamp emissions = %v, want %v", got, want)
		}
	}
}

// TestP1_1_RelayCallerDirectConstArg proves the caller of a relay helper is attributed the event
// even when the name is passed as a DIRECT constant argument (no local var). Mirrors settlement
// create_adjustment.go: createAdjustmentAndQueueEntry calls the aliased-send relay
// triggerAdjustmentEvent(ctx, constants.EventAdjustmentCreated, id). This is the live-data gap
// exposed by Q1 — the send became visible (P1-1) but caller attribution missed the const arg.
func TestP1_1_RelayCallerDirectConstArg(t *testing.T) {
	r := newP1TestResolver(t)
	r.consts.values["EventAdjustmentCreated"] = mustParseExpr(t, `"adjustment.created"`)
	files := parseP1Files(t, map[string]string{
		"adjust.go": `package p
import sqsqueue "github.com/tazapay/grpc-framework/client/queue"
func triggerAdjustmentEvent(ctx c, eventType, adjustmentID string) error {
	qMsg := &sqsqueue.AsyncMessage{ EventType: eventType, Data: map[string]any{} }
	return sqsqueue.SendSQSMsg(ctx, fenv.Get(env.EventQueue), qMsg)
}
func createAdjustmentAndQueueEntry(ctx c) error {
	return triggerAdjustmentEvent(ctx, constants.EventAdjustmentCreated, adjID)
}`,
	})
	runProducerPasses(r, files)

	// The relay helper itself chooses nothing — it must not be attributed.
	if evs := r.EmissionsFor("adjust.go", "triggerAdjustmentEvent"); len(evs) != 0 {
		t.Fatalf("relay helper should not be attributed, got %v", emissionEvents(evs))
	}
	// The caller must be attributed adjustment.created via the direct const argument.
	got := emissionEvents(r.EmissionsFor("adjust.go", "createAdjustmentAndQueueEntry"))
	if len(got) != 1 || got[0] != "adjustment.created→event" {
		t.Fatalf("relay-caller emissions = %v, want [adjustment.created→event]", got)
	}
}

// TestP1_2_LiteralDefaultPlusConditionalRestamps proves that when the composite literal sets a
// default EventType AND later `if` branches restamp it, ALL possible events are attributed — the
// literal default and every conditional override. Mirrors payment-orchestration TriggerPayinSQS
// (Q4): before the fix only the literal default 'pat_webhook.status' was captured.
func TestP1_2_LiteralDefaultPlusConditionalRestamps(t *testing.T) {
	r := newP1TestResolver(t)
	r.consts.values["PATStatus"] = mustParseExpr(t, `"pat_webhook.status"`)
	r.consts.values["PATPmt"] = mustParseExpr(t, `"pat_webhook.pmt_method_details"`)
	r.consts.values["PATTax"] = mustParseExpr(t, `"pat_webhook.tax_invoice_generated"`)
	files := parseP1Files(t, map[string]string{
		"payin.go": `package p
import "github.com/tazapay/grpc-framework/client/queue"
func TriggerPayinSQS(ctx c, data *T) error {
	queueMsg := &queue.AsyncMessage{ EventType: constants.PATStatus, Data: map[string]any{} }
	if data.Status == "others" {
		queueMsg.EventType = constants.PATPmt
	}
	if data.Status == "tax_invoice_generated" {
		queueMsg.EventType = constants.PATTax
	}
	return queue.SendDelaySQSMsg(ctx, fenv.Get(env.EventQueue), queueMsg, data.DelaySeconds)
}`,
	})
	runProducerPasses(r, files)

	got := emissionEvents(r.EmissionsFor("payin.go", "TriggerPayinSQS"))
	want := []string{
		"pat_webhook.pmt_method_details→event",
		"pat_webhook.status→event",
		"pat_webhook.tax_invoice_generated→event",
	}
	if len(got) != len(want) {
		t.Fatalf("literal+restamp emissions = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("literal+restamp emissions = %v, want %v", got, want)
		}
	}
}

// TestP1_2_RestampDynamicDegradesToHub proves a partially-static restamp
// (`Settlement + CharDot + <param>`) degrades to a settlement.* group hub rather than being
// silently skipped — the "silent drops are the enemy" ground rule.
func TestP1_2_RestampDynamicDegradesToHub(t *testing.T) {
	r := newP1TestResolver(t)
	files := parseP1Files(t, map[string]string{
		"produce.go": `package p
import "github.com/tazapay/grpc-framework/client/queue"
func triggerDynamic(ctx c, status string) error {
	qMsg := &queue.AsyncMessage{ Data: make(map[string]any) }
	qMsg.EventType = constants.Settlement + constants.CharDot + status
	return queue.SendSQSMsg(ctx, fenv.Get(env.SettlementQueue), qMsg)
}`,
	})
	runProducerPasses(r, files)

	got := r.EmissionsFor("produce.go", "triggerDynamic")
	if len(got) != 1 {
		t.Fatalf("dynamic restamp = %v, want exactly one (settlement.* hub)", emissionEvents(got))
	}
	if got[0].event != "settlement.*" || !got[0].dynamic {
		t.Fatalf("dynamic restamp = %q (dynamic=%v), want settlement.* hub", got[0].event, got[0].dynamic)
	}
}

// TestP1_3_SelectorTagEnumeration proves a selector-tag switch (`switch payout.status`) enumerates
// a `<group> + CharDot + payout.status` case body into a concrete event per case label, instead of
// collapsing to a dynamic hub. The tag binds by its selector name.
func TestP1_3_SelectorTagEnumeration(t *testing.T) {
	r := newP1TestResolver(t)
	fn := parseSingleFunc(t, `package p
func f(ctx c, payout *worker) {
	var eventType string
	switch payout.status {
	case constants.Screening:
		eventType = constants.Payout + constants.CharDot + payout.status
	case constants.Created:
		eventType = constants.Payout + constants.CharDot + payout.status
	}
	_ = eventType
}`)

	got := r.collectLocalEventAssigns(fn)
	var events []string
	var dynamic bool
	for _, evs := range got {
		for _, ev := range evs {
			events = append(events, ev.event)
			dynamic = dynamic || ev.dynamic
		}
	}
	sort.Strings(events)
	want := []string{"payout.created", "payout.screening"}
	if len(events) != len(want) {
		t.Fatalf("selector-tag events = %v, want %v", events, want)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("selector-tag events = %v, want %v", events, want)
		}
	}
	if dynamic {
		t.Fatalf("expected concrete events, got a dynamic hub in %v", events)
	}
}

// TestP1_3_CallTagFallsBackToHub proves a call-tag switch (`switch v.GetLabel()`) — which has no
// bindable tag name — does not panic and degrades a dynamic case body to a group hub rather than
// dropping it. This documents the opaque-key behaviour introduced by P1-3.
func TestP1_3_CallTagFallsBackToHub(t *testing.T) {
	r := newP1TestResolver(t)
	fn := parseSingleFunc(t, `package p
func f(ctx c, v *thing, other string) {
	var eventType string
	switch v.GetLabel() {
	case constants.Screening:
		eventType = constants.Payout + constants.CharDot + other
	}
	_ = eventType
}`)

	got := r.collectLocalEventAssigns(fn)
	var events []string
	for _, evs := range got {
		for _, ev := range evs {
			events = append(events, ev.event)
		}
	}
	if len(events) != 1 || events[0] != "payout.*" {
		t.Fatalf("call-tag events = %v, want [payout.*]", events)
	}
}
