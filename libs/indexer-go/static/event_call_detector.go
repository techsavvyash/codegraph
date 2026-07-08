package static

import (
	"context"
	"fmt"
	"go/ast"
	"go/token"
	"log"
	"maps"
	"strings"
	"time"

	models "github.com/context-maximiser/code-graph/libs/core-models-go"
	"github.com/context-maximiser/code-graph/libs/neo4j-go"
)

// outboxFuncNames are bare function names that indicate an outbox-style event publish.
var outboxFuncNames = map[string]bool{
	"SaveOutboxEvent":    true,
	"InsertOutboxEvent":  true,
	"AddOutboxEvent":     true,
	"PublishOutboxEvent": true,
	"EnqueueOutboxEvent": true,
	"CreateOutboxEvent":  true,
	"StoreOutboxEvent":   true,
	"EmitEvent":          true,
	"PublishEvent":       true,
	"EnqueueEvent":       true,
	"DispatchEvent":      true,
}

// sqsPublishMethods are method names that indicate an SQS message send.
var sqsPublishMethods = map[string]bool{
	"SendMessage":            true,
	"SendMessageWithContext": true,
	"SendMessageBatch":       true,
}

// kafkaProduceMethods are Kafka producer method names.
var kafkaProduceMethods = map[string]bool{
	"Produce":       true,
	"WriteMessages": true,
	"WriteMessage":  true,
}

// natsPublishMethods are NATS publish method names.
var natsPublishMethods = map[string]bool{
	"Publish":        true,
	"PublishMsg":     true,
	"PublishRequest": true,
}

// EventCallDetector detects outbound async event publishes and consumer-side listener
// bindings. For Tazapay's uniform SQS/AsyncMessage pattern it writes semantic OutboxCall
// nodes, shared EventType hubs, EMITS_EVENT edges and CALLS_API edges (driven by the
// repo-level EventEmissionResolver). Generic Kafka/NATS/field-SQS/outbox-func publishes still
// produce coarse OutboxCall nodes. On the consumer side it marks SQS listener-entry functions
// and per-event handler functions so the cross-service resolver can write ROUTED_TO edges.
type EventCallDetector struct {
	client      *neo4j.Client
	serviceName string
	scopeCtx    models.ScopeContext
	fset        *token.FileSet
	callBuffer  *callNodeBuffer

	// consts resolves compile-time string constants/queue names within this service repo.
	consts *constResolver
	// emissions holds the pre-computed producer→event attributions for this service repo.
	emissions *EventEmissionResolver

	// varTransportMap: local var name → transport type ("sqs", "kafka", "kafka-consumer", "nats").
	// Reset per function — never reused across function boundaries.
	varTransportMap map[string]string
}

// NewEventCallDetector creates a detector scoped to a single service indexing run.
func NewEventCallDetector(client *neo4j.Client, serviceName string, scopeCtx models.ScopeContext) *EventCallDetector {
	return &EventCallDetector{
		client:          client,
		serviceName:     serviceName,
		scopeCtx:        scopeCtx,
		varTransportMap: make(map[string]string),
	}
}

// SetCallNodeBuffer configures an optional shared call node buffer.
func (d *EventCallDetector) SetCallNodeBuffer(buf *callNodeBuffer) {
	d.callBuffer = buf
}

// SetConstResolver injects the repo-wide constant resolver (queue/event name resolution).
func (d *EventCallDetector) SetConstResolver(c *constResolver) {
	d.consts = c
}

// SetEmissionResolver injects the pre-computed producer→event attribution index.
func (d *EventCallDetector) SetEmissionResolver(e *EventEmissionResolver) {
	d.emissions = e
}

// DetectInFunction walks funcDecl for async publish sites and listener bindings, writing
// OutboxCall/EventType nodes and CALLS_API/EMITS_EVENT edges for producers, and marking
// listener-entry and per-event handler functions for the cross-service resolver.
func (d *EventCallDetector) DetectInFunction(
	ctx context.Context,
	funcDecl *ast.FuncDecl,
	callerFuncID, filePath string,
	fset *token.FileSet,
) error {
	if funcDecl.Body == nil {
		return nil
	}
	d.fset = fset

	// Invariant: reset per function so bindings from other functions don't pollute.
	d.varTransportMap = make(map[string]string)

	// Pass 1 — collect variable-to-transport-type bindings from assignment statements.
	ast.Inspect(funcDecl.Body, func(n ast.Node) bool {
		if assign, ok := n.(*ast.AssignStmt); ok {
			d.processTransportAssignment(assign)
		}
		return true
	})

	// Pass 2 — generic (non-AsyncMessage) publish detection: Kafka/NATS/field-SQS/outbox funcs.
	var firstErr error
	ast.Inspect(funcDecl.Body, func(n ast.Node) bool {
		callExpr, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		pos := fset.Position(callExpr.Pos())
		if err := d.processPublishCallExpr(ctx, callExpr, callerFuncID, filePath, pos.Line); err != nil {
			if firstErr == nil {
				firstErr = err
			}
		}
		return true
	})

	// Pass 3 — semantic producer writes for Tazapay's SQS/AsyncMessage pattern (precomputed).
	d.writeEmissions(funcDecl, callerFuncID, filePath)

	// Pass 4 — consumer side: SQS listener-entry bindings + per-event handler marking.
	d.markListenerBindings(ctx, funcDecl)
	d.markEventHandler(ctx, funcDecl, callerFuncID)

	return firstErr
}

// writeEmissions materialises the pre-computed producer→event attributions for this function
// into the graph: an OutboxCall publish site, a shared EventType hub, a CALLS_API edge from
// the enclosing function and an EMITS_EVENT edge to the hub.
func (d *EventCallDetector) writeEmissions(funcDecl *ast.FuncDecl, callerFuncID, filePath string) {
	if d.emissions == nil || d.callBuffer == nil {
		return
	}
	for _, em := range d.emissions.EmissionsFor(filePath, funcDecl.Name.Name) {
		ocKey := models.OutboxCallNodeKey(d.serviceName, filePath, em.transport, em.event, em.line)
		ocProps := map[string]any{
			"nodeKey": ocKey,
			// name is the Neo4j Browser caption: "<callerService>:<group>.<action>",
			// e.g. "settlement:payout.created" (em.event already holds group.action).
			"name":          d.serviceName + ":" + em.event,
			"callerService": d.serviceName,
			"transport":     em.transport,
			"eventType":     em.event,
			"eventGroup":    em.group,
			"eventAction":   em.action,
			"queueOrTopic":  em.destQueue,
			"destQueue":     em.destQueue,
			"destService":   em.destService,
			"dynamic":       em.dynamic,
			"filePath":      filePath,
			"line":          em.line,
			"createdAt":     time.Now().UTC().Unix(),
			"updatedAt":     time.Now().UTC().Unix(),
		}
		maps.Copy(ocProps, d.scopeCtx.Props())
		d.callBuffer.addOutboxCall(ocKey, ocProps)
		d.callBuffer.addCallsAPIEdge(callerFuncID, ocKey, map[string]any{
			"line":      em.line,
			"transport": em.transport,
		})

		etKey := models.EventTypeNodeKey(em.group, em.action)
		etProps := map[string]any{
			"nodeKey": etKey,
			// name is the Neo4j Browser caption: the full event name "<group>.<action>"
			// (e.g. "payout.created", or "payout.*" for a dynamic hub).
			"name":      em.event,
			"eventType": em.event,
			"group":     em.group,
			"action":    em.action,
			"dynamic":   em.dynamic,
			"createdAt": time.Now().UTC().Unix(),
			"updatedAt": time.Now().UTC().Unix(),
		}
		maps.Copy(etProps, d.scopeCtx.Props())
		d.callBuffer.addEventTypeNode(etKey, etProps)
		d.callBuffer.addEmitsEventEdge(ocKey, etKey, map[string]any{
			"transport":   em.transport,
			"destQueue":   em.destQueue,
			"destService": em.destService,
		})
	}
}

// processTransportAssignment records SQS/Kafka/NATS constructor and composite literal
// assignments into varTransportMap.
func (d *EventCallDetector) processTransportAssignment(assign *ast.AssignStmt) {
	if len(assign.Lhs) == 0 || len(assign.Rhs) == 0 {
		return
	}
	lhsIdent, ok := assign.Lhs[0].(*ast.Ident)
	if !ok {
		return
	}

	// Handle composite literals: writer := &kafka.Writer{...}
	rhs := assign.Rhs[0]
	if unary, ok := rhs.(*ast.UnaryExpr); ok {
		rhs = unary.X
	}
	if compLit, ok := rhs.(*ast.CompositeLit); ok {
		if sel, ok := compLit.Type.(*ast.SelectorExpr); ok {
			if pkgIdent, ok := sel.X.(*ast.Ident); ok {
				pkg := strings.ToLower(pkgIdent.Name)
				typeName := sel.Sel.Name
				switch {
				case pkg == "kafka" && (typeName == "Writer" || typeName == "Producer"):
					d.varTransportMap[lhsIdent.Name] = "kafka"
				case pkg == "kafka" && typeName == "Consumer":
					d.varTransportMap[lhsIdent.Name] = "kafka-consumer"
				}
			}
		}
		return
	}

	callExpr, ok := assign.Rhs[0].(*ast.CallExpr)
	if !ok {
		return
	}
	sel, ok := callExpr.Fun.(*ast.SelectorExpr)
	if !ok {
		return
	}
	pkgIdent, ok := sel.X.(*ast.Ident)
	if !ok {
		return
	}

	pkg := strings.ToLower(pkgIdent.Name)
	funcName := sel.Sel.Name

	switch {
	case pkg == "sqs" && strings.HasPrefix(funcName, "New"):
		d.varTransportMap[lhsIdent.Name] = "sqs"
	case (pkg == "kafka" || pkg == "confluent_kafka") && strings.HasPrefix(funcName, "NewProducer"):
		d.varTransportMap[lhsIdent.Name] = "kafka"
	case (pkg == "kafka" || pkg == "confluent_kafka") && strings.HasPrefix(funcName, "NewConsumer"):
		d.varTransportMap[lhsIdent.Name] = "kafka-consumer"
	case pkg == "nats" && (funcName == "Connect" || funcName == "ConnectTLS"):
		d.varTransportMap[lhsIdent.Name] = "nats"
	case pkg == "stan" && funcName == "Connect":
		d.varTransportMap[lhsIdent.Name] = "nats"
	}
}

// processPublishCallExpr checks whether this call expression is an async event publish
// and, if so, writes an OutboxCall node plus a CALLS_API edge.
func (d *EventCallDetector) processPublishCallExpr(
	ctx context.Context,
	callExpr *ast.CallExpr,
	callerFuncID, filePath string,
	line int,
) error {
	var transport, eventType, queueOrTopic string
	detected := false

	// Pattern 1: bare function call — SaveOutboxEvent(...), PublishEvent(...), etc.
	if ident, ok := callExpr.Fun.(*ast.Ident); ok {
		if outboxFuncNames[ident.Name] {
			transport = "outbox"
			eventType = extractEventTypeArg(callExpr)
			detected = true
		}
	}

	if !detected {
		sel, ok := callExpr.Fun.(*ast.SelectorExpr)
		if !ok {
			return nil
		}

		switch recv := sel.X.(type) {
		case *ast.Ident:
			// Variable method call or package-level call.
			// NOTE: Tazapay's queue.SendSQSMsg / SendDelaySQSMsg (AsyncMessage) pattern is
			// intentionally NOT handled here — it is resolved semantically by the
			// EventEmissionResolver and materialised in writeEmissions (Pass 3).
			if outboxFuncNames[sel.Sel.Name] {
				transport = "outbox"
				eventType = extractEventTypeArg(callExpr)
				detected = true
			} else if varTransport, tracked := d.varTransportMap[recv.Name]; tracked {
				switch varTransport {
				case "sqs":
					if sqsPublishMethods[sel.Sel.Name] {
						transport = "sqs"
						queueOrTopic = extractQueueStringLiteral(callExpr)
						eventType = queueOrTopic
						detected = true
					}
				case "kafka":
					if kafkaProduceMethods[sel.Sel.Name] {
						transport = "kafka"
						queueOrTopic = extractFirstStringLiteral(callExpr)
						eventType = queueOrTopic
						detected = true
					}
				case "nats":
					if natsPublishMethods[sel.Sel.Name] {
						transport = "nats"
						queueOrTopic = extractStringArg(callExpr, 0)
						eventType = queueOrTopic
						detected = true
					}
				}
			} else if strings.ToLower(recv.Name) == "nats" && natsPublishMethods[sel.Sel.Name] {
				// Direct package call: nats.Publish(...) — uncommon but valid.
				transport = "nats"
				queueOrTopic = extractStringArg(callExpr, 0)
				eventType = queueOrTopic
				detected = true
			}

		case *ast.SelectorExpr:
			// Struct field access: h.sqsClient.SendMessage(...), h.producer.Produce(...).
			fieldLower := strings.ToLower(recv.Sel.Name)
			switch {
			case strings.Contains(fieldLower, "sqs") && sqsPublishMethods[sel.Sel.Name]:
				transport = "sqs"
				queueOrTopic = extractQueueStringLiteral(callExpr)
				eventType = queueOrTopic
				detected = true
			case (strings.Contains(fieldLower, "kafka") || strings.Contains(fieldLower, "producer")) &&
				kafkaProduceMethods[sel.Sel.Name]:
				transport = "kafka"
				queueOrTopic = extractFirstStringLiteral(callExpr)
				eventType = queueOrTopic
				detected = true
			case strings.Contains(fieldLower, "nats") && natsPublishMethods[sel.Sel.Name]:
				transport = "nats"
				queueOrTopic = extractStringArg(callExpr, 0)
				eventType = queueOrTopic
				detected = true
			case outboxFuncNames[sel.Sel.Name]:
				transport = "outbox"
				eventType = extractEventTypeArg(callExpr)
				detected = true
			// Tazapay outbox: repo.OutboxSettlement.BulkSave/Save or any repo.Outbox*.BulkSave/Save
			case strings.Contains(fieldLower, "outbox") && (sel.Sel.Name == "BulkSave" || sel.Sel.Name == "Save"):
				transport = "outbox"
				eventType = strings.ToLower(recv.Sel.Name) // "outboxsettlement"
				detected = true
			}
		}
	}

	if !detected {
		return nil
	}

	if eventType == "" {
		eventType = "dynamic"
	}
	if queueOrTopic == "" {
		queueOrTopic = eventType
	}

	nodeKey := fmt.Sprintf("outboxcall:%s:%s:%s:%s:%d", d.serviceName, filePath, transport, eventType, line)

	mergeProps := map[string]any{"nodeKey": nodeKey}
	setProps := map[string]any{
		"nodeKey":       nodeKey,
		"callerService": d.serviceName,
		"transport":     transport,
		"eventType":     eventType,
		"queueOrTopic":  queueOrTopic,
		"filePath":      filePath,
		"line":          line,
		"createdAt":     time.Now().UTC().Unix(),
		"updatedAt":     time.Now().UTC().Unix(),
	}
	maps.Copy(setProps, d.scopeCtx.Props())

	if d.callBuffer != nil {
		d.callBuffer.addOutboxCall(nodeKey, setProps)
		d.callBuffer.addCallsAPIEdge(callerFuncID, nodeKey, map[string]any{
			"line":      line,
			"transport": transport,
		})
		return nil
	}

	outboxCallID, err := d.client.MergeNode(ctx, []string{"OutboxCall"}, mergeProps, setProps)
	if err != nil {
		return fmt.Errorf("event detector: merge OutboxCall node: %w", err)
	}

	// CALLS_API: callerFunction → OutboxCall
	if _, err := d.client.MergeRelationship(ctx,
		callerFuncID, outboxCallID,
		string(models.CallsAPIRel),
		map[string]any{},
		map[string]any{"line": line, "transport": transport},
	); err != nil {
		log.Printf("Warning: event detector: CALLS_API edge for %s/%s: %v", transport, eventType, err)
	}

	return nil
}

// markListenerBindings finds server.SQSListener{ QueueURL: <q>, Route: <fn> } composite
// literals and marks the referenced route-entry function with the queue it listens on. This
// is the tier-1 anchor the cross-service resolver uses to write a guaranteed ROUTED_TO edge.
func (d *EventCallDetector) markListenerBindings(ctx context.Context, funcDecl *ast.FuncDecl) {
	if d.consts == nil {
		return
	}
	// Detect by the presence of both QueueURL and Route fields rather than by the literal's
	// type name. In real code the binding is written as an elided element inside a slice —
	// `[]*server.SQSListener{ {QueueURL: ..., Route: ...} }` — where the inner composite
	// literal carries no explicit type (cl.Type is nil), so a type-name check never fires.
	// The QueueURL+Route field pairing is distinctive enough to identify the binding.
	ast.Inspect(funcDecl.Body, func(n ast.Node) bool {
		cl, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		var queueURL, routeFn string
		var hasQueue, hasRoute bool
		for _, elt := range cl.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := kv.Key.(*ast.Ident)
			if !ok {
				continue
			}
			switch key.Name {
			case "QueueURL":
				hasQueue = true
				queueURL = d.resolveQueueExpr(kv.Value)
			case "Route":
				hasRoute = true
				routeFn = routeFuncName(kv.Value)
			}
		}
		if !hasQueue || !hasRoute {
			return true
		}
		if queueURL != "" && routeFn != "" {
			d.markListenerEntry(ctx, routeFn, queueURL)
		}
		return true
	})
}

// markListenerEntry sets listensOnQueue / listensService on the named route-entry function
// within this service.
func (d *EventCallDetector) markListenerEntry(ctx context.Context, routeFn, queueURL string) {
	if _, err := d.client.ExecuteQuery(ctx, `
		MATCH (svc:Service {name: $svc})-[:CONTAINS*1..5]->(f)
		WHERE (f:Function OR f:Method)
		  AND (f.name = $fn OR f.name STARTS WITH ($fn + '('))
		SET f.listensOnQueue = $queue, f.listensService = $svc
	`, map[string]any{
		"svc":   d.serviceName,
		"fn":    routeFn,
		"queue": queueURL,
	}); err != nil {
		log.Printf("Warning: event detector: mark listener entry %s/%s: %v", routeFn, queueURL, err)
	}
}

// markEventHandler is the best-effort tier-2 signal. Tazapay's event router splits dispatch
// into "<group>ActionRoute" functions that switch over action constants (e.g.
// settlementActionRoute → case constants.Failed). We derive the group from the function name,
// resolve the action constants in its switch cases, and stamp handlesEvents = ["group.action"]
// on the function so the resolver can attach a precise (lower-confidence) ROUTED_TO edge.
func (d *EventCallDetector) markEventHandler(ctx context.Context, funcDecl *ast.FuncDecl, callerFuncID string) {
	group := actionRouteGroup(funcDecl.Name.Name)
	if group == "" {
		return
	}
	actions := d.collectCaseActions(funcDecl)
	if len(actions) == 0 {
		return
	}
	events := make([]string, 0, len(actions))
	for _, a := range actions {
		events = append(events, group+"."+a)
	}
	if _, err := d.client.ExecuteQuery(ctx, `
		MATCH (f) WHERE elementId(f) = $funcID
		SET f.handlesEvents = $events, f.handlesEventGroup = $group
	`, map[string]any{
		"funcID": callerFuncID,
		"events": events,
		"group":  group,
	}); err != nil {
		log.Printf("Warning: event detector: set handlesEvents on %s: %v", funcDecl.Name.Name, err)
	}
}

// collectCaseActions resolves the constants referenced in a function's switch cases to their
// string values, keeping only single-token (dot-free) values that look like event actions.
func (d *EventCallDetector) collectCaseActions(funcDecl *ast.FuncDecl) []string {
	if d.consts == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	ast.Inspect(funcDecl.Body, func(n ast.Node) bool {
		cc, ok := n.(*ast.CaseClause)
		if !ok {
			return true
		}
		for _, expr := range cc.List {
			val, static := d.consts.ResolveString(expr)
			if !static || val == "" || strings.Contains(val, ".") {
				continue
			}
			if !seen[val] {
				seen[val] = true
				out = append(out, val)
			}
		}
		return true
	})
	return out
}

// resolveQueueExpr resolves a listener QueueURL expression to its queue string, unwrapping
// env.Get(...) wrappers.
func (d *EventCallDetector) resolveQueueExpr(expr ast.Expr) string {
	if call, ok := expr.(*ast.CallExpr); ok && len(call.Args) >= 1 {
		return d.resolveQueueExpr(call.Args[0])
	}
	if d.consts == nil {
		return ""
	}
	val, _ := d.consts.ResolveString(expr)
	return val
}

// routeFuncName extracts the handler function name from an SQSListener Route field value,
// which is typically a call (sqs.GetEventRoutes()) but may be a selector or bare identifier.
func routeFuncName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.CallExpr:
		return calleeName(e)
	case *ast.SelectorExpr:
		return e.Sel.Name
	case *ast.Ident:
		return e.Name
	}
	return ""
}

// actionRouteGroup returns the lowercase event group encoded in a "<group>ActionRoute"
// function name (e.g. "settlementActionRoute" → "settlement"), or "" if it doesn't match.
func actionRouteGroup(name string) string {
	const suffix = "ActionRoute"
	if !strings.HasSuffix(name, suffix) {
		return ""
	}
	prefix := name[:len(name)-len(suffix)]
	if prefix == "" {
		return ""
	}
	return strings.ToLower(prefix)
}

// extractEventTypeArg returns the first string literal in the call that is not a context
// argument. Used for outbox-style calls where the event type is a positional string arg.
func extractEventTypeArg(callExpr *ast.CallExpr) string {
	for _, arg := range callExpr.Args {
		if ident, ok := arg.(*ast.Ident); ok {
			name := strings.ToLower(ident.Name)
			if name == "ctx" || name == "context" {
				continue
			}
		}
		if lit, ok := arg.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			val := lit.Value
			if len(val) >= 2 {
				return val[1 : len(val)-1]
			}
		}
	}
	return ""
}

// extractQueueStringLiteral scans the call expression tree for the first string literal
// that looks like a queue URL or name (used for SQS calls where the queue is embedded
// in a SendMessageInput struct literal).
func extractQueueStringLiteral(callExpr *ast.CallExpr) string {
	var found string
	ast.Inspect(callExpr, func(n ast.Node) bool {
		if found != "" {
			return false
		}
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		val := lit.Value
		if len(val) < 4 {
			return true
		}
		s := val[1 : len(val)-1]
		// Prefer strings that look like queue identifiers (URL or short name).
		if strings.Contains(s, "sqs.") || strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "arn:") {
			found = s
		} else if found == "" {
			found = s
		}
		return true
	})
	return found
}

// extractFirstStringLiteral returns the first string literal found anywhere in the call
// expression tree. Used for Kafka calls where the topic is embedded in a Message struct.
func extractFirstStringLiteral(callExpr *ast.CallExpr) string {
	var found string
	ast.Inspect(callExpr, func(n ast.Node) bool {
		if found != "" {
			return false
		}
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		val := lit.Value
		if len(val) >= 2 {
			found = val[1 : len(val)-1]
		}
		return true
	})
	return found
}
