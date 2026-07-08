package static

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

// eventEmission is one resolved (function → event) broadcast fact. A single trigger function
// that switches over statuses produces several of these (one per enumerated case).
type eventEmission struct {
	event       string // "settlement.failed" or "payout.*"
	group       string // "settlement"
	action      string // "failed" ("" for a group-fallback hub)
	destQueue   string // resolved queue string, e.g. "queue.event.event"
	destService string // owning service of destQueue, e.g. "event"
	transport   string // "sqs"
	dynamic     bool   // action was a runtime value
	line        int    // anchor line (send site, or call-to-emitter for the relay case)
}

// emitterMeta describes a function that physically sends an AsyncMessage. `relay` is true when
// the message's EventType field is one of the function's own parameters — i.e. the name is
// chosen by a *caller* and passed down (the sendSettlementEvent helper pattern).
type emitterMeta struct {
	destQueue     string
	destService   string
	transport     string
	relay         bool
	eventParamIdx int // positional index of the parameter that supplies the event name (-1 if not a relay)
}

// EventEmissionResolver performs a whole-repository AST pre-pass to attribute semantic event
// names to the functions that broadcast them, bridging the two-function "switch-relay" split
// where the event name is chosen in one function (a status switch) and the actual SQS send
// happens in a small helper it calls. Results are keyed by "<relPath>#<funcName>" so the
// per-function detector pass can look them up cheaply while it holds the Neo4j function id.
type EventEmissionResolver struct {
	consts      *constResolver
	emitters    map[string]emitterMeta     // bare funcName → emitter metadata
	emissions   map[string][]eventEmission // "<relPath>#<funcName>" → emissions
	projectPath string
}

// NewEventEmissionResolver builds the resolver by parsing every non-test .go file twice:
// Pass A classifies emitter functions, Pass B attributes events to producers.
func NewEventEmissionResolver(projectPath string, consts *constResolver) *EventEmissionResolver {
	r := &EventEmissionResolver{
		consts:      consts,
		emitters:    make(map[string]emitterMeta),
		emissions:   make(map[string][]eventEmission),
		projectPath: projectPath,
	}
	files := r.parseFiles()
	r.passAClassifyEmitters(files)
	r.resolveTransitiveRelays(files)
	r.passBAttributeEmissions(files)
	return r
}

// EmissionsFor returns the events attributed to the given function (may be empty).
func (r *EventEmissionResolver) EmissionsFor(relPath, funcName string) []eventEmission {
	if r == nil {
		return nil
	}
	return r.emissions[relPath+"#"+funcName]
}

type parsedFile struct {
	relPath string
	fset    *token.FileSet
	file    *ast.File
}

func (r *EventEmissionResolver) parseFiles() []parsedFile {
	var out []parsedFile
	_ = filepath.Walk(r.projectPath, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || info.IsDir() {
			return walkErr
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || isNoisyFilePath(path) {
			return nil
		}
		fset := token.NewFileSet()
		astFile, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return nil
		}
		relPath, relErr := filepath.Rel(r.projectPath, path)
		if relErr != nil {
			relPath = path
		}
		out = append(out, parsedFile{relPath: relPath, fset: fset, file: astFile})
		return nil
	})
	return out
}

// passAClassifyEmitters records, for every function that sends an AsyncMessage, its resolved
// destination queue/service and whether the event name is supplied by a parameter (relay).
func (r *EventEmissionResolver) passAClassifyEmitters(files []parsedFile) {
	for _, pf := range files {
		for _, decl := range pf.file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			send := r.firstAsyncSend(fn)
			if send == nil {
				continue
			}
			destQueue := r.resolveQueueArg(send.Args[1])
			meta := emitterMeta{
				destQueue:     destQueue,
				destService:   QueueToService(destQueue),
				transport:     "sqs",
				eventParamIdx: -1,
			}
			if evExpr := r.asyncEventTypeExpr(fn, send); evExpr != nil {
				if ident, isIdent := evExpr.(*ast.Ident); isIdent {
					if idx := paramIndex(fn, ident.Name); idx >= 0 {
						meta.relay = true
						meta.eventParamIdx = idx
					}
				}
			}
			r.emitters[fn.Name.Name] = meta
		}
	}
}

// resolveTransitiveRelays extends the emitter index to cover multi-hop relay chains. A function
// that merely forwards one of its own parameters into a known relay's event-name argument
// position is itself a relay (it neither chooses nor sends the event — it passes it along). This
// is iterated to a fixpoint so chains of any depth are captured, e.g.
//
//	TriggerPayoutEvent → triggerPayoutEventNotifications → sendPayoutEvent(AsyncMessage send)
//
// where only sendPayoutEvent physically sends, but the middle function must be recognised as a
// relay for the switch-driven trigger to be attributed the events it broadcasts.
func (r *EventEmissionResolver) resolveTransitiveRelays(files []parsedFile) {
	for changed := true; changed; {
		changed = false
		for _, pf := range files {
			for _, decl := range pf.file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				if _, done := r.emitters[fn.Name.Name]; done {
					continue // already a direct emitter or an established relay
				}
				if meta, idx, ok := r.forwardsToRelay(fn); ok {
					r.emitters[fn.Name.Name] = emitterMeta{
						destQueue:     meta.destQueue,
						destService:   meta.destService,
						transport:     "sqs",
						relay:         true,
						eventParamIdx: idx,
					}
					changed = true
				}
			}
		}
	}
}

// forwardsToRelay reports whether fn calls a relay emitter, passing one of fn's own parameters
// into that relay's event-name argument position. It returns the callee's metadata (so the
// destination queue is inherited) and the index of fn's forwarded parameter (so the chain can be
// followed another hop). The first qualifying call in source order wins, which for Tazapay's
// helpers is the primary destination (e.g. the event queue rather than a conditional side queue).
func (r *EventEmissionResolver) forwardsToRelay(fn *ast.FuncDecl) (emitterMeta, int, bool) {
	var resMeta emitterMeta
	var resIdx int
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if found {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		meta, ok := r.emitters[calleeName(call)]
		if !ok || !meta.relay || meta.eventParamIdx < 0 || meta.eventParamIdx >= len(call.Args) {
			return true
		}
		argIdent, ok := call.Args[meta.eventParamIdx].(*ast.Ident)
		if !ok {
			return true
		}
		if idx := paramIndex(fn, argIdent.Name); idx >= 0 {
			resMeta, resIdx, found = meta, idx, true
			return false
		}
		return true
	})
	return resMeta, resIdx, found
}

// passBAttributeEmissions produces the per-function emission list using local information plus
// the emitter index from Pass A.
func (r *EventEmissionResolver) passBAttributeEmissions(files []parsedFile) {
	for _, pf := range files {
		for _, decl := range pf.file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			key := pf.relPath + "#" + fn.Name.Name
			localEvents := r.collectLocalEventAssigns(fn) // varName → []eventEmission (partial)
			var out []eventEmission

			// (a) Functions that both choose the name and send it themselves.
			for _, send := range r.allAsyncSends(fn) {
				destQueue := r.resolveQueueArg(send.Args[1])
				line := pf.fset.Position(send.Pos()).Line
				evExpr := r.asyncEventTypeExpr(fn, send)
				if evExpr == nil {
					continue
				}
				if ident, isIdent := evExpr.(*ast.Ident); isIdent {
					if isParam(fn, ident.Name) {
						continue // relay helper — the caller is the real producer
					}
					// Name assigned locally (switch in the same function as the send).
					for _, ev := range localEvents[ident.Name] {
						out = append(out, ev.withDest(destQueue, line))
					}
					continue
				}
				if ev, ok := r.resolveEventExpr(evExpr); ok {
					out = append(out, ev.withDest(destQueue, line))
				}
			}

			// (b) Relay callers: this function assigns the name(s) then calls a relay helper.
			for _, call := range r.callsToRelayEmitters(fn, pf.fset) {
				meta := r.emitters[call.name]
				for _, evs := range localEvents {
					for _, ev := range evs {
						out = append(out, ev.withDest(meta.destQueue, call.line))
					}
				}
			}

			if len(out) > 0 {
				r.emissions[key] = dedupeEmissions(out)
			}
		}
	}
}

// withDest fills in the destination queue/service and anchor line for a partial emission.
func (e eventEmission) withDest(destQueue string, line int) eventEmission {
	e.destQueue = destQueue
	e.destService = QueueToService(destQueue)
	e.transport = "sqs"
	e.line = line
	return e
}

// collectLocalEventAssigns scans a function body for `<var> = <event-name-expr>` assignments
// (typically the cases of a status switch) whose RHS resolves to an event.
func (r *EventEmissionResolver) collectLocalEventAssigns(fn *ast.FuncDecl) map[string][]eventEmission {
	out := map[string][]eventEmission{}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for i, lhs := range assign.Lhs {
			ident, ok := lhs.(*ast.Ident)
			if !ok || i >= len(assign.Rhs) {
				continue
			}
			if ev, ok := r.resolveEventExpr(assign.Rhs[i]); ok {
				out[ident.Name] = append(out[ident.Name], ev)
			}
		}
		return true
	})
	return out
}

// resolveEventExpr resolves an expression to a partial eventEmission (group/action/event/
// dynamic set; dest/line filled later). ok is false when the expression is not an event.
func (r *EventEmissionResolver) resolveEventExpr(expr ast.Expr) (eventEmission, bool) {
	val, static := r.consts.ResolveString(expr)
	group, action, dynamic, ok := splitEvent(val, static)
	if !ok {
		return eventEmission{}, false
	}
	return eventEmission{
		event:   makeEventName(group, action),
		group:   group,
		action:  action,
		dynamic: dynamic,
	}, true
}

type relayCall struct {
	name string
	line int
}

// callsToRelayEmitters returns calls this function makes to functions classified as relay
// emitters in Pass A.
func (r *EventEmissionResolver) callsToRelayEmitters(fn *ast.FuncDecl, fset *token.FileSet) []relayCall {
	var out []relayCall
	seen := map[string]bool{}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		name := calleeName(call)
		if name == "" || seen[name] {
			return true
		}
		if meta, ok := r.emitters[name]; ok && meta.relay {
			seen[name] = true
			out = append(out, relayCall{name: name, line: fset.Position(call.Pos()).Line})
		}
		return true
	})
	return out
}

// firstAsyncSend returns the first queue.SendSQSMsg / queue.SendDelaySQSMsg call in the body.
func (r *EventEmissionResolver) firstAsyncSend(fn *ast.FuncDecl) *ast.CallExpr {
	sends := r.allAsyncSends(fn)
	if len(sends) == 0 {
		return nil
	}
	return sends[0]
}

// allAsyncSends returns every queue.SendSQSMsg / queue.SendDelaySQSMsg call in the body that
// carries at least a URL and a message argument.
func (r *EventEmissionResolver) allAsyncSends(fn *ast.FuncDecl) []*ast.CallExpr {
	var out []*ast.CallExpr
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		recv, ok := sel.X.(*ast.Ident)
		if !ok || recv.Name != "queue" {
			return true
		}
		if sel.Sel.Name != "SendSQSMsg" && sel.Sel.Name != "SendDelaySQSMsg" {
			return true
		}
		if len(call.Args) >= 3 {
			out = append(out, call)
		}
		return true
	})
	return out
}

// asyncEventTypeExpr resolves the EventType field expression of the AsyncMessage that a send
// call publishes. The message argument (Args[2]) is either an inline &queue.AsyncMessage{...}
// or a local variable assigned one earlier in the function.
func (r *EventEmissionResolver) asyncEventTypeExpr(fn *ast.FuncDecl, send *ast.CallExpr) ast.Expr {
	msgArg := send.Args[2]
	if lit := asyncMessageLiteral(msgArg); lit != nil {
		return eventTypeField(lit)
	}
	ident, ok := msgArg.(*ast.Ident)
	if !ok {
		return nil
	}
	// Trace the local `<name> := &queue.AsyncMessage{...}` binding.
	var found ast.Expr
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if found != nil {
			return false
		}
		assign, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for i, lhs := range assign.Lhs {
			lhsIdent, ok := lhs.(*ast.Ident)
			if !ok || lhsIdent.Name != ident.Name || i >= len(assign.Rhs) {
				continue
			}
			if lit := asyncMessageLiteral(assign.Rhs[i]); lit != nil {
				found = eventTypeField(lit)
				return false
			}
		}
		return true
	})
	return found
}

// resolveQueueArg resolves a send's URL argument to a queue string, unwrapping env.Get(...).
func (r *EventEmissionResolver) resolveQueueArg(expr ast.Expr) string {
	if call, ok := expr.(*ast.CallExpr); ok && len(call.Args) >= 1 {
		return r.resolveQueueArg(call.Args[0])
	}
	val, _ := r.consts.ResolveString(expr)
	return val
}

// --- small AST helpers ---

// asyncMessageLiteral returns the composite literal if expr is `&queue.AsyncMessage{...}` or
// `queue.AsyncMessage{...}`, else nil.
func asyncMessageLiteral(expr ast.Expr) *ast.CompositeLit {
	if unary, ok := expr.(*ast.UnaryExpr); ok {
		expr = unary.X
	}
	lit, ok := expr.(*ast.CompositeLit)
	if !ok {
		return nil
	}
	sel, ok := lit.Type.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "AsyncMessage" {
		return nil
	}
	return lit
}

// eventTypeField returns the value expression of the `EventType:` field in a composite literal.
func eventTypeField(lit *ast.CompositeLit) ast.Expr {
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "EventType" {
			return kv.Value
		}
	}
	return nil
}

// isParam reports whether name is a parameter of fn.
func isParam(fn *ast.FuncDecl, name string) bool {
	return paramIndex(fn, name) >= 0
}

// paramIndex returns the positional index of the named parameter within fn's flattened
// parameter list (grouped params like `a, b string` each occupy their own position), or -1 if
// fn has no such parameter. Unnamed parameters still occupy a position so indices line up with
// call-site argument positions.
func paramIndex(fn *ast.FuncDecl, name string) int {
	if fn.Type == nil || fn.Type.Params == nil {
		return -1
	}
	idx := 0
	for _, field := range fn.Type.Params.List {
		if len(field.Names) == 0 {
			idx++ // unnamed parameter — occupies a position but cannot be matched by name
			continue
		}
		for _, ident := range field.Names {
			if ident.Name == name {
				return idx
			}
			idx++
		}
	}
	return -1
}

// calleeName returns the bare function name of a call expression (both foo() and pkg.foo()).
func calleeName(call *ast.CallExpr) string {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		return fun.Name
	case *ast.SelectorExpr:
		return fun.Sel.Name
	}
	return ""
}

// makeEventName joins group/action into an event name, using a "*" wildcard action for hubs.
func makeEventName(group, action string) string {
	if action == "" {
		return group + ".*"
	}
	return group + "." + action
}

// dedupeEmissions removes duplicate (event, destQueue) pairs, keeping the first occurrence.
func dedupeEmissions(in []eventEmission) []eventEmission {
	seen := map[string]bool{}
	var out []eventEmission
	for _, e := range in {
		k := e.event + "|" + e.destQueue
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, e)
	}
	return out
}
