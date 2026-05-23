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

// sqsConsumeMethods are method names that indicate SQS message consumption.
var sqsConsumeMethods = map[string]bool{
	"ReceiveMessage":            true,
	"ReceiveMessageWithContext": true,
}

// kafkaProduceMethods are Kafka producer method names.
var kafkaProduceMethods = map[string]bool{
	"Produce":       true,
	"WriteMessages": true,
	"WriteMessage":  true,
}

// kafkaConsumeMethods are Kafka consumer method names.
var kafkaConsumeMethods = map[string]bool{
	"SubscribeTopics": true,
	"ReadMessage":     true,
	"Poll":            true,
}

// natsPublishMethods are NATS publish method names.
var natsPublishMethods = map[string]bool{
	"Publish":        true,
	"PublishMsg":     true,
	"PublishRequest": true,
}

// natsSubscribeMethods are NATS subscribe method names.
var natsSubscribeMethods = map[string]bool{
	"Subscribe":      true,
	"QueueSubscribe": true,
	"ChanSubscribe":  true,
}

// EventCallDetector detects outbound async event publishes (outbox, SQS, Kafka, NATS)
// and consumer-side registrations. Writes OutboxCall nodes with CALLS_API edges and
// CONSUMES_FROM edges between consumers and matching OutboxCall nodes.
type EventCallDetector struct {
	client      *neo4j.Client
	serviceName string
	scopeCtx    models.ScopeContext
	fset        *token.FileSet
	callBuffer  *callNodeBuffer

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

// DetectInFunction walks funcDecl looking for async event publish and consume sites,
// then writes OutboxCall nodes plus CALLS_API / CONSUMES_FROM edges.
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

	// Pass 2 — detect publish and consume call expressions.
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
		if err := d.processConsumeCallExpr(ctx, callExpr, callerFuncID, filePath, pos.Line); err != nil {
			if firstErr == nil {
				firstErr = err
			}
		}
		return true
	})

	return firstErr
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
		if eventType != "dynamic" {
			if svcID := d.resolveConsumerServiceID(ctx, eventType); svcID != "" {
				d.callBuffer.addCallsServiceEdge(nodeKey, svcID, map[string]any{"protocol": transport})
			}
		}
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

	// Best-effort: link to a consuming service via CALLS_SERVICE.
	if eventType != "dynamic" {
		d.linkProducerToConsumer(ctx, outboxCallID, eventType, transport)
	}

	return nil
}

// processConsumeCallExpr detects consumer-side patterns, marks the caller function with
// consumesEvent/consumesTransport properties, and writes CONSUMES_FROM edges to matching
// OutboxCall nodes from other services.
func (d *EventCallDetector) processConsumeCallExpr(
	ctx context.Context,
	callExpr *ast.CallExpr,
	callerFuncID, _ string,
	_ int,
) error {
	var transport, eventType string
	detected := false

	sel, ok := callExpr.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}

	switch recv := sel.X.(type) {
	case *ast.Ident:
		if varTransport, tracked := d.varTransportMap[recv.Name]; tracked {
			switch varTransport {
			case "sqs":
				if sqsConsumeMethods[sel.Sel.Name] {
					transport = "sqs"
					eventType = extractQueueStringLiteral(callExpr)
					detected = true
				}
			case "kafka-consumer":
				if kafkaConsumeMethods[sel.Sel.Name] {
					transport = "kafka"
					eventType = extractFirstStringLiteral(callExpr)
					detected = true
				}
			case "nats":
				if natsSubscribeMethods[sel.Sel.Name] {
					transport = "nats"
					eventType = extractStringArg(callExpr, 0)
					detected = true
				}
			}
		}

	case *ast.SelectorExpr:
		fieldLower := strings.ToLower(recv.Sel.Name)
		switch {
		case strings.Contains(fieldLower, "sqs") && sqsConsumeMethods[sel.Sel.Name]:
			transport = "sqs"
			eventType = extractQueueStringLiteral(callExpr)
			detected = true
		case (strings.Contains(fieldLower, "kafka") || strings.Contains(fieldLower, "consumer")) &&
			kafkaConsumeMethods[sel.Sel.Name]:
			transport = "kafka"
			detected = true
		case strings.Contains(fieldLower, "nats") && natsSubscribeMethods[sel.Sel.Name]:
			transport = "nats"
			eventType = extractStringArg(callExpr, 0)
			detected = true
		}
	}

	if !detected {
		return nil
	}

	if eventType == "" {
		eventType = "dynamic"
	}

	// Mark the caller function node with consumesEvent so producers can resolve it later.
	updateCypher := `
		MATCH (f) WHERE elementId(f) = $funcID
		SET f.consumesEvent = $eventType, f.consumesTransport = $transport
	`
	if _, err := d.client.ExecuteQuery(ctx, updateCypher, map[string]any{
		"funcID":    callerFuncID,
		"eventType": eventType,
		"transport": transport,
	}); err != nil {
		log.Printf("Warning: event detector: set consumesEvent on function: %v", err)
	}

	// Link this consumer function to any already-indexed matching OutboxCall nodes.
	if eventType != "dynamic" {
		d.linkConsumerToOutboxCalls(ctx, callerFuncID, eventType, transport)
	}

	return nil
}

func (d *EventCallDetector) resolveConsumerServiceID(ctx context.Context, eventType string) string {
	cypher := `
		MATCH (f)
		WHERE f.consumesEvent = $eventType
		  AND (f:Function OR f:Method)
		MATCH (svc:Service)-[:CONTAINS]->(file:File)-[:CONTAINS]->(f)
		WHERE svc.name <> $serviceName
		RETURN elementId(svc) AS svcID
		LIMIT 1
	`
	results, err := d.client.ExecuteQuery(ctx, cypher, map[string]any{
		"eventType":   eventType,
		"serviceName": d.serviceName,
	})
	if err != nil || len(results) == 0 {
		return ""
	}
	return getStringFromMap(results[0].AsMap(), "svcID")
}

// linkProducerToConsumer finds a consuming service for the given eventType and writes a
// CALLS_SERVICE edge from the OutboxCall node to that service.
func (d *EventCallDetector) linkProducerToConsumer(ctx context.Context, outboxCallID, eventType, transport string) {
	svcID := d.resolveConsumerServiceID(ctx, eventType)
	if svcID == "" {
		return
	}
	if _, err := d.client.MergeRelationship(ctx,
		outboxCallID, svcID,
		string(models.CallsServiceRel),
		map[string]any{},
		map[string]any{"protocol": transport},
	); err != nil {
		log.Printf("Warning: event detector: CALLS_SERVICE edge for %s: %v", eventType, err)
	}
}

// linkConsumerToOutboxCalls finds OutboxCall nodes in other services that publish the
// given eventType and writes CONSUMES_FROM edges from the consumer function to each.
func (d *EventCallDetector) linkConsumerToOutboxCalls(ctx context.Context, callerFuncID, eventType, transport string) {
	cypher := `
		MATCH (oc:OutboxCall {eventType: $eventType})
		WHERE oc.callerService <> $serviceName
		RETURN elementId(oc) AS ocID
		LIMIT 20
	`
	results, err := d.client.ExecuteQuery(ctx, cypher, map[string]any{
		"eventType":   eventType,
		"serviceName": d.serviceName,
	})
	if err != nil {
		return
	}
	for _, row := range results {
		ocID, _ := row.AsMap()["ocID"].(string)
		if ocID == "" {
			continue
		}
		if _, err := d.client.MergeRelationship(ctx,
			callerFuncID, ocID,
			string(models.ConsumesFromRel),
			map[string]any{},
			map[string]any{"transport": transport, "eventType": eventType},
		); err != nil {
			log.Printf("Warning: event detector: CONSUMES_FROM edge for %s: %v", eventType, err)
		}
	}
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
