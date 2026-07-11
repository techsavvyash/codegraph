package static

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"testing"
)

// parseFanoutFiles parses each src into a parsedFile (relPath = its map key).
func parseFanoutFiles(t *testing.T, srcs map[string]string) []parsedFile {
	t.Helper()
	var out []parsedFile
	// deterministic order
	names := make([]string, 0, len(srcs))
	for n := range srcs {
		names = append(names, n)
	}
	sort.Strings(names)
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

func newFanoutTestResolver(t *testing.T) *EventEmissionResolver {
	t.Helper()
	consts := &constResolver{values: map[string]ast.Expr{}}
	decls := map[string]string{
		"Payout":           `"payout"`,
		"Settlement":       `"settlement"`,
		"CharDot":          `"."`,
		"BalanceDetection": `"balance_detection"`,
		"Created":          `"created"`,
		"Failed":           `"failed"`,
		"BalanceQueue":     `"queue.balance.balance"`,
		"MonitoringQueue":  `"queue.monitoring.monitoring"`,
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

func runFanoutPasses(r *EventEmissionResolver, files []parsedFile) {
	r.classifyMsgRelays(files)
	r.resolveTransitiveMsgRelays(files)
	r.buildRouteHandlerEvents(files)
	r.attributeFanout(files)
}

func emissionEvents(emissions []eventEmission) []string {
	var out []string
	for _, e := range emissions {
		out = append(out, e.event+"→"+e.destService)
	}
	sort.Strings(out)
	return out
}

// TestFanout_ReStampedLiteral covers shape (A): the caller mutates qMsg.EventType to a literal
// before forwarding to a terminal relay.
func TestFanout_ReStampedLiteral(t *testing.T) {
	r := newFanoutTestResolver(t)
	files := parseFanoutFiles(t, map[string]string{
		"sqs.go": `package p
func SendToBalanceConsumer(ctx c, qMsg *queue.AsyncMessage) error {
	return queue.SendSQSMsg(ctx, fenv.Get(env.BalanceQueue), qMsg)
}
func sendToBalanceConsumerForPayout(ctx c, qMsg *queue.AsyncMessage) error {
	balanceDetectionMsg := *qMsg
	balanceDetectionMsg.EventType = constants.Payout + constants.CharDot + constants.BalanceDetection
	return utils.SendToBalanceConsumer(ctx, &balanceDetectionMsg)
}`,
	})
	runFanoutPasses(r, files)

	if _, ok := r.msgRelays["SendToBalanceConsumer"]; !ok {
		t.Fatalf("SendToBalanceConsumer not classified as a terminal msg relay")
	}
	got := emissionEvents(r.EmissionsFor("sqs.go", "sendToBalanceConsumerForPayout"))
	want := []string{"payout.balance_detection→balance"}
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("re-stamp emissions = %v, want %v", got, want)
	}
}

// TestFanout_ForwardedThroughWrapper covers shape (B): a route handler forwards the inbound event
// through an intermediate multi-destination relay. The event name comes from the router dispatch.
func TestFanout_ForwardedThroughWrapper(t *testing.T) {
	r := newFanoutTestResolver(t)
	files := parseFanoutFiles(t, map[string]string{
		"sqs.go": `package p
func SendToBalanceConsumer(ctx c, qMsg *queue.AsyncMessage) error {
	return queue.SendSQSMsg(ctx, fenv.Get(env.BalanceQueue), qMsg)
}
func SendToMonitoringConsumer(ctx c, qMsg *queue.AsyncMessage) error {
	return queue.SendSQSMsg(ctx, fenv.Get(env.MonitoringQueue), qMsg)
}
func fanoutSettlementCreated(ctx c, qMsg *queue.AsyncMessage) error {
	if err := utils.SendToBalanceConsumer(ctx, qMsg); err != nil { return err }
	return utils.SendToMonitoringConsumer(ctx, qMsg)
}`,
		"route.go": `package p
func settlementActionRoute(ctx c, payload *queue.AsyncMessage) error {
	switch action {
	case constants.Created:
		return settlementCreated(ctx, payload)
	default:
		return nil
	}
}
func settlementCreated(ctx c, payload *queue.AsyncMessage) error {
	return fanoutSettlementCreated(ctx, payload)
}`,
	})
	runFanoutPasses(r, files)

	// The intermediate wrapper must inherit both destinations.
	meta, ok := r.msgRelays["fanoutSettlementCreated"]
	if !ok || len(meta.dests) != 2 {
		t.Fatalf("fanoutSettlementCreated relay dests = %+v, want 2 (balance, monitoring)", meta.dests)
	}
	// The route handler must be attributed settlement.created to BOTH downstream services.
	got := emissionEvents(r.EmissionsFor("route.go", "settlementCreated"))
	want := []string{"settlement.created→balance", "settlement.created→monitoring"}
	if len(got) != len(want) {
		t.Fatalf("forwarded emissions = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("forwarded emissions = %v, want %v", got, want)
		}
	}
	// The pure forwarder itself must NOT be an attribution point (avoids double-count).
	if evs := r.EmissionsFor("sqs.go", "fanoutSettlementCreated"); len(evs) != 0 {
		t.Fatalf("intermediate forwarder should not be attributed, got %v", emissionEvents(evs))
	}
}

// TestFanout_PerActionGuard covers the precision case: a dispatcher fans out to balance only for
// specific actions (Failed/Cancelled) while the handler processes a wider set. succeeded must NOT
// be attributed to balance, only to the email destination its case guards.
func TestFanout_PerActionGuard(t *testing.T) {
	r := newFanoutTestResolver(t)
	// extend consts with the actions/queues this test needs
	r.consts.values["Succeeded"] = mustParseExpr(t, `"succeeded"`)
	r.consts.values["Cancelled"] = mustParseExpr(t, `"cancelled"`)
	r.consts.values["EmailQueue"] = mustParseExpr(t, `"queue.notification.email"`)

	files := parseFanoutFiles(t, map[string]string{
		"sqs.go": `package p
func SendToBalanceConsumer(ctx c, qMsg *queue.AsyncMessage) error {
	return queue.SendSQSMsg(ctx, fenv.Get(env.BalanceQueue), qMsg)
}
func SendToEmailConsumer(ctx c, qMsg *queue.AsyncMessage) error {
	return queue.SendSQSMsg(ctx, fenv.Get(env.EmailQueue), qMsg)
}
func sendMessageToSettlementConsumers(ctx c, status string, qMsg *queue.AsyncMessage) error {
	switch status {
	case constants.Succeeded:
		return utils.SendToEmailConsumer(ctx, qMsg)
	case constants.Failed:
		if err := utils.SendToBalanceConsumer(ctx, qMsg); err != nil { return err }
		return utils.SendToEmailConsumer(ctx, qMsg)
	case constants.Cancelled:
		return utils.SendToBalanceConsumer(ctx, qMsg)
	}
	return nil
}`,
		"route.go": `package p
func settlementActionRoute(ctx c, payload *queue.AsyncMessage) error {
	switch action {
	case constants.Succeeded, constants.Failed, constants.Cancelled:
		return settlementUpdateEvent(ctx, payload)
	default:
		return nil
	}
}
func settlementUpdateEvent(ctx c, payload *queue.AsyncMessage) error {
	return sendMessageToSettlementConsumers(ctx, action, payload)
}`,
	})
	runFanoutPasses(r, files)

	got := emissionEvents(r.EmissionsFor("route.go", "settlementUpdateEvent"))
	want := []string{
		"settlement.cancelled→balance",
		"settlement.failed→balance",
		"settlement.failed→notification",
		"settlement.succeeded→notification",
	}
	if len(got) != len(want) {
		t.Fatalf("guarded emissions = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("guarded emissions = %v, want %v", got, want)
		}
	}
	// The critical negative: succeeded must never reach balance.
	for _, e := range got {
		if e == "settlement.succeeded→balance" {
			t.Fatalf("false edge: settlement.succeeded must not fan out to balance")
		}
	}
}
