package static

import (
	"go/ast"
	"strings"
)

// This file extends EventEmissionResolver to capture the event service's ASYNC FAN-OUT: the
// hop where an event-service route handler re-broadcasts an inbound event onto a downstream
// service's queue via a helper such as utils.SendToBalanceConsumer(ctx, qMsg).
//
// These helpers forward a whole *queue.AsyncMessage parameter (the event name is dynamic, set by
// the caller), so the semantic-name machinery used for direct producers does not fire. We model
// them as "message relays" with a fixed destination queue/service, then attribute the concrete
// event at the call site in one of two shapes:
//
//	(A) re-stamped literal — the caller sets qMsg.EventType = <group> + CharDot + <const> before
//	    forwarding (e.g. sendToBalanceConsumerForPayout → "payout.balance_detection").
//	(B) forwarded inbound event — the caller forwards the event it is currently handling; the
//	    event name is recovered from the enclosing route handler's dispatched action(s).
//
// The resulting emissions flow through the same writeEmissions path as direct producers, so each
// fan-out becomes an OutboxCall (destService = downstream) → EMITS_EVENT → the shared EventType
// hub. The cross-service resolver then links that hub to the downstream service's listener entry
// and per-event handler exactly as it does for direct producers.

// fanoutDest is a resolved downstream (queue, service) a message relay delivers to.
type fanoutDest struct {
	destQueue   string
	destService string
}

// msgRelayMeta describes a function that forwards a whole *queue.AsyncMessage parameter to one or
// more downstream queues. `dests` are delivered unconditionally; `guardedDests` are delivered only
// when the inbound action matches a `switch <action>` case that guards the send (e.g. the event
// service's sendMessageToSettlementConsumers sends to balance only for case Failed/Cancelled).
type msgRelayMeta struct {
	msgParamIdx  int                     // positional index of the forwarded *AsyncMessage parameter
	dests        []fanoutDest            // delivered regardless of action
	guardedDests map[string][]fanoutDest // action label → dests delivered only for that action
}

// allDests flattens unconditional and guarded destinations (used when the specific action is not
// known, e.g. at a re-stamp site).
func (m msgRelayMeta) allDests() []fanoutDest {
	out := append([]fanoutDest(nil), m.dests...)
	for _, ds := range m.guardedDests {
		out = append(out, ds...)
	}
	return dedupeDests(out)
}

// destsForAction returns the destinations reached for a specific inbound action: the unconditional
// ones plus any guarded by a case matching that action.
func (m msgRelayMeta) destsForAction(action string) []fanoutDest {
	out := append([]fanoutDest(nil), m.dests...)
	out = append(out, m.guardedDests[action]...)
	return dedupeDests(out)
}

// classifyMsgRelays finds terminal message relays: functions whose queue.SendSQSMsg /
// SendDelaySQSMsg call forwards one of their own parameters as the message argument. The
// destination queue is resolved from the send's URL argument.
func (r *EventEmissionResolver) classifyMsgRelays(files []parsedFile) {
	for _, pf := range files {
		queueAliases := queueReceiverAliases(pf.file)
		for _, decl := range pf.file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			var dests []fanoutDest
			msgIdx := -1
			for _, send := range r.allAsyncSends(fn, queueAliases) {
				argName := identOf(send.Args[2])
				if argName == "" {
					continue
				}
				idx := paramIndex(fn, argName)
				if idx < 0 {
					continue // message argument is a local, not a forwarded parameter
				}
				destQueue := r.resolveQueueArg(send.Args[1])
				if destQueue == "" {
					continue
				}
				dests = append(dests, fanoutDest{destQueue: destQueue, destService: QueueToService(destQueue)})
				msgIdx = idx
			}
			if len(dests) > 0 {
				r.msgRelays[fn.Name.Name] = msgRelayMeta{msgParamIdx: msgIdx, dests: dedupeDests(dests)}
			}
		}
	}
}

// resolveTransitiveMsgRelays extends the relay set to intermediate forwarders: a function that
// passes one of its own parameters into a known relay's message-argument position is itself a
// relay, inheriting that relay's destinations. When the forwarding call sits inside a
// `switch <action>` case, the inherited destinations become guarded by that case's action labels
// — this is how the event service's per-action fan-out (balance only for Failed/Cancelled) is
// preserved. Iterated to a fixpoint so wrapper chains of any depth are captured.
func (r *EventEmissionResolver) resolveTransitiveMsgRelays(files []parsedFile) {
	for changed := true; changed; {
		changed = false
		for _, pf := range files {
			for _, decl := range pf.file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				if _, done := r.msgRelays[fn.Name.Name]; done {
					continue
				}
				if meta, ok := r.forwardsToMsgRelay(fn); ok {
					r.msgRelays[fn.Name.Name] = meta
					changed = true
				}
			}
		}
	}
}

// forwardsToMsgRelay builds the relay metadata for a function that forwards one of its own
// parameters into calls to already-known relays. Unconditional forwarding calls contribute
// unconditional destinations; calls inside a `switch <action>` case contribute destinations
// guarded by that case's action labels.
func (r *EventEmissionResolver) forwardsToMsgRelay(fn *ast.FuncDecl) (msgRelayMeta, bool) {
	callLabels := r.mapCallSwitchLabels(fn.Body)
	meta := msgRelayMeta{msgParamIdx: -1, guardedDests: map[string][]fanoutDest{}}
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		callee, ok := r.msgRelays[calleeName(call)]
		if !ok || callee.msgParamIdx < 0 || callee.msgParamIdx >= len(call.Args) {
			return true
		}
		argName := identOf(call.Args[callee.msgParamIdx])
		if argName == "" {
			return true
		}
		pidx := paramIndex(fn, argName)
		if pidx < 0 {
			return true // forwards a local, not one of fn's parameters — attributed here, not upstream
		}
		if meta.msgParamIdx < 0 {
			meta.msgParamIdx = pidx
		}
		found = true
		contributed := callee.allDests()
		if labels := callLabels[call]; len(labels) > 0 {
			for _, l := range labels {
				meta.guardedDests[l] = append(meta.guardedDests[l], contributed...)
			}
		} else {
			meta.dests = append(meta.dests, contributed...)
		}
		return true
	})
	meta.dests = dedupeDests(meta.dests)
	for l := range meta.guardedDests {
		meta.guardedDests[l] = dedupeDests(meta.guardedDests[l])
	}
	return meta, found
}

// mapCallSwitchLabels maps each call expression inside a value `switch` case to that case's
// resolved single-token action labels (innermost switch wins, matching preorder visitation).
func (r *EventEmissionResolver) mapCallSwitchLabels(body *ast.BlockStmt) map[*ast.CallExpr][]string {
	m := map[*ast.CallExpr][]string{}
	ast.Inspect(body, func(n ast.Node) bool {
		sw, ok := n.(*ast.SwitchStmt)
		if !ok || sw.Body == nil {
			return true
		}
		for _, stmt := range sw.Body.List {
			cc, ok := stmt.(*ast.CaseClause)
			if !ok {
				continue
			}
			labels := r.caseActions(cc)
			if len(labels) == 0 {
				continue
			}
			for _, bodyStmt := range cc.Body {
				ast.Inspect(bodyStmt, func(bn ast.Node) bool {
					if call, isCall := bn.(*ast.CallExpr); isCall {
						m[call] = labels
					}
					return true
				})
			}
		}
		return true
	})
	return m
}

// buildRouteHandlerEvents maps each dispatched route-handler function to the "group.action"
// events it handles. Tazapay routers dispatch group → "<group>ActionRoute", whose switch maps
// action constants → a handler call. We resolve each case's action constants and attribute
// "group.action" to every same-package function invoked in that case body.
func (r *EventEmissionResolver) buildRouteHandlerEvents(files []parsedFile) {
	for _, pf := range files {
		for _, decl := range pf.file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			group := actionRouteGroup(fn.Name.Name)
			if group == "" {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				cc, ok := n.(*ast.CaseClause)
				if !ok {
					return true
				}
				actions := r.caseActions(cc)
				if len(actions) == 0 {
					return true
				}
				for _, stmt := range cc.Body {
					ast.Inspect(stmt, func(bn ast.Node) bool {
						call, ok := bn.(*ast.CallExpr)
						if !ok {
							return true
						}
						handler := localCalleeName(call) // same-package dispatch only
						if handler == "" {
							return true
						}
						for _, a := range actions {
							r.handlerEvents[handler] = appendUniqueStr(r.handlerEvents[handler], group+"."+a)
						}
						return true
					})
				}
				return true
			})
		}
	}
}

// HandlerEventsFor returns the pre-computed "group.action" event list for a concrete handler
// function (not a routing dispatcher). The mapping is built by buildRouteHandlerEvents by tracing
// <group>ActionRoute switch dispatch: each case body's called function is recorded here with the
// events it handles. EventCallDetector uses this to stamp handlesEventsDirectly on the actual
// business-logic functions rather than on the dispatch intermediary.
func (r *EventEmissionResolver) HandlerEventsFor(funcName string) []string {
	return r.handlerEvents[funcName]
}

// attributeRelayStructural writes a structural (dynamic, no EventType) emission on each
// message relay function so that relay helpers like SendToWebhookConsumer appear in the graph
// as connected to their destination service. Without this, the connection is only visible on
// the callers (payinPaid → OutboxCall → notification), not on the relay itself.
// A structural emission has group="" which tells writeEmissions to skip EventType/EMITS_EVENT
// creation — only an OutboxCall + CALLS_API edge is written. The cross_service_resolver then
// derives OutboxCall → CALLS_SERVICE → Service from oc.destService automatically.
func (r *EventEmissionResolver) attributeRelayStructural(files []parsedFile) {
	for _, pf := range files {
		for _, decl := range pf.file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			meta, ok := r.msgRelays[fn.Name.Name]
			if !ok {
				continue
			}
			dests := meta.allDests()
			if len(dests) == 0 {
				continue
			}
			key := pf.relPath + "#" + fn.Name.Name
			line := pf.fset.Position(fn.Pos()).Line
			var out []eventEmission
			for _, d := range dests {
				if d.destService == "" {
					continue
				}
				// group="" is the sentinel that signals "structural, skip EventType node".
				// event is the raw queue name so the OutboxCall caption is human-readable.
				out = append(out, eventEmission{
					event:       d.destQueue,
					group:       "",
					action:      "",
					destQueue:   d.destQueue,
					destService: d.destService,
					transport:   "sqs",
					dynamic:     true,
					line:        line,
				})
			}
			if len(out) > 0 {
				r.emissions[key] = dedupeEmissions(append(r.emissions[key], out...))
			}
		}
	}
}

// caseActions resolves a case clause's label constants to single-token (dot-free) action names.
func (r *EventEmissionResolver) caseActions(cc *ast.CaseClause) []string {
	var out []string
	for _, lbl := range cc.List {
		val, static := r.consts.ResolveString(lbl)
		if static && val != "" && !strings.Contains(val, ".") {
			out = appendUniqueStr(out, val)
		}
	}
	return out
}

// attributeFanout walks every function and, for each call it makes to a message relay, attributes
// the forwarded event(s) × the relay's destinations as emissions. Attribution happens only where
// the event identity is known: at a re-stamp site (A) or at a dispatched route handler (B). Pure
// intermediate forwarders contribute their destinations (via classification) but are not
// themselves attribution points, which keeps each fan-out attributed once at its semantic origin.
func (r *EventEmissionResolver) attributeFanout(files []parsedFile) {
	for _, pf := range files {
		for _, decl := range pf.file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			key := pf.relPath + "#" + fn.Name.Name
			callLabels := r.mapCallSwitchLabels(fn.Body)
			var out []eventEmission
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				meta, ok := r.msgRelays[calleeName(call)]
				if !ok || meta.msgParamIdx < 0 || meta.msgParamIdx >= len(call.Args) {
					return true
				}
				line := pf.fset.Position(call.Pos()).Line
				msgArg := call.Args[meta.msgParamIdx]

				// (A) re-stamped literal event(s): a fixed event, delivered to every destination
				// the relay can reach (the guard action need not match the re-stamped action).
				if lits := r.staticEventLiterals(fn, msgArg); len(lits) > 0 {
					for _, ev := range lits {
						out = append(out, emissionsForEvent(ev, meta.allDests(), line)...)
					}
					return true
				}

				// (B) forwarded inbound event: the enclosing route handler's dispatched action(s),
				// each delivered only to the destinations reached for that action (intersecting the
				// router dispatch with the relay's per-action fan-out guards).
				events := r.handlerEvents[fn.Name.Name]
				if len(events) == 0 {
					return true // event identity unknown here — a pure forwarder; skip
				}
				fnGuard := callLabels[call] // handler may itself switch before fanning out
				for _, ev := range events {
					_, action, _, ok := splitEvent(ev, true)
					if !ok {
						continue
					}
					if len(fnGuard) > 0 && !containsStr(fnGuard, action) {
						continue
					}
					out = append(out, emissionsForEvent(ev, meta.destsForAction(action), line)...)
				}
				return true
			})
			if len(out) > 0 {
				r.emissions[key] = dedupeEmissions(append(r.emissions[key], out...))
			}
		}
	}
}

// staticEventLiterals returns the statically-resolvable "group.action" event names stamped onto
// the message referred to by arg, within fn. It handles an inline &queue.AsyncMessage{EventType:…}
// literal, a local `<name> := &queue.AsyncMessage{EventType:…}` binding, and a later
// `<name>.EventType = …` re-assignment (the *qMsg-clone-then-restamp pattern).
func (r *EventEmissionResolver) staticEventLiterals(fn *ast.FuncDecl, arg ast.Expr) []string {
	if lit := asyncMessageLiteral(arg); lit != nil {
		return r.eventLiteralFromField(eventTypeField(lit))
	}
	name := identOf(arg)
	if name == "" {
		return nil
	}
	var out []string
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for i, lhs := range assign.Lhs {
			if i >= len(assign.Rhs) {
				continue
			}
			switch l := lhs.(type) {
			case *ast.SelectorExpr:
				// <name>.EventType = <expr>
				if l.Sel.Name == "EventType" && identOf(l.X) == name {
					out = append(out, r.eventLiteralFromField(assign.Rhs[i])...)
				}
			case *ast.Ident:
				// <name> := &queue.AsyncMessage{EventType: <expr>}
				if l.Name == name {
					if lit := asyncMessageLiteral(assign.Rhs[i]); lit != nil {
						out = append(out, r.eventLiteralFromField(eventTypeField(lit))...)
					}
				}
			}
		}
		return true
	})
	return uniqueStrs(out)
}

// eventLiteralFromField resolves an EventType field expression to a single fully-static
// "group.action" value (empty slice when it is dynamic or not an event).
func (r *EventEmissionResolver) eventLiteralFromField(expr ast.Expr) []string {
	if expr == nil {
		return nil
	}
	val, static := r.consts.ResolveString(expr)
	if !static {
		return nil
	}
	// splitEvent accepts both group.action and single-token ("quote_expiry") names.
	if _, _, _, ok := splitEvent(val, true); !ok {
		return nil
	}
	return []string{val}
}

// --- small helpers ---

// identOf returns the underlying identifier name of an expression, unwrapping &x / *x, or "".
func identOf(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.UnaryExpr:
		return identOf(e.X)
	case *ast.StarExpr:
		return identOf(e.X)
	}
	return ""
}

// localCalleeName returns the callee name only for same-package calls (bare identifiers), so
// dispatched route handlers are captured while pkg-qualified helper calls (utils.X, trace.X)
// are ignored.
func localCalleeName(call *ast.CallExpr) string {
	if id, ok := call.Fun.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

// emissionsForEvent builds one eventEmission per destination for a "group.action" event name.
func emissionsForEvent(ev string, dests []fanoutDest, line int) []eventEmission {
	group, action, dynamic, ok := splitEvent(ev, true)
	if !ok {
		return nil
	}
	out := make([]eventEmission, 0, len(dests))
	for _, d := range dests {
		out = append(out, eventEmission{
			event:       makeEventName(group, action, dynamic),
			group:       group,
			action:      action,
			dynamic:     dynamic,
			destQueue:   d.destQueue,
			destService: d.destService,
			transport:   "sqs",
			line:        line,
		})
	}
	return out
}

func containsStr(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func dedupeDests(in []fanoutDest) []fanoutDest {
	seen := map[string]bool{}
	var out []fanoutDest
	for _, d := range in {
		k := d.destQueue + "|" + d.destService
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, d)
	}
	return out
}

func appendUniqueStr(list []string, v string) []string {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

func uniqueStrs(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range in {
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}
