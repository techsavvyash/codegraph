package static

import (
	"context"
	"fmt"
	"log"
	"maps"
	"strings"
	"time"

	models "github.com/context-maximiser/code-graph/libs/core-models-go"
	"github.com/context-maximiser/code-graph/libs/neo4j-go"
)

// SCIPRPCDetector detects outbound gRPC and HTTP call sites by querying the SCIP
// Symbol/Reference graph already ingested in Neo4j. It works for all languages
// (Go SCIP, TypeScript, Python, etc.) since it operates purely on graph data.
type SCIPRPCDetector struct {
	client      *neo4j.Client
	serviceName string
	scopeCtx    models.ScopeContext
	serviceIdx  *serviceIndex
	callBuffer  *callNodeBuffer
}

// NewSCIPRPCDetector creates a SCIP-based RPC call detector for a service.
func NewSCIPRPCDetector(client *neo4j.Client, serviceName string, scopeCtx models.ScopeContext) *SCIPRPCDetector {
	return &SCIPRPCDetector{
		client:      client,
		serviceName: serviceName,
		scopeCtx:    scopeCtx,
	}
}

// SetServiceIndex configures an optional preloaded service index for O(1) resolution.
func (d *SCIPRPCDetector) SetServiceIndex(idx *serviceIndex) {
	d.serviceIdx = idx
}

// SetCallNodeBuffer configures an optional shared call node buffer.
func (d *SCIPRPCDetector) SetCallNodeBuffer(buf *callNodeBuffer) {
	d.callBuffer = buf
}

// scipCallRef holds a matched reference with its classification.
type scipCallRef struct {
	filePath    string
	line        int
	symbol      string
	displayName string
	callType    string // "grpc" or "http"
}

// scipOutboxRef holds a matched outbox/async-publish reference.
type scipOutboxRef struct {
	filePath    string
	line        int
	symbol      string
	displayName string
	transport   string // "sqs", "kafka", "nats", "outbox"
}

// DetectRPCCalls queries the SCIP graph for gRPC, HTTP, and async publish call site
// references, then writes the corresponding call nodes with CALLS_API and CALLS_SERVICE edges.
func (d *SCIPRPCDetector) DetectRPCCalls(ctx context.Context) error {
	if d.serviceIdx == nil {
		idx, err := loadServiceIndex(ctx, d.client, d.scopeCtx.ScopeID)
		if err != nil {
			log.Printf("Warning: SCIPRPCDetector service index preload failed: %v", err)
		} else {
			d.serviceIdx = idx
		}
	}

	if d.callBuffer == nil {
		d.callBuffer = newCallNodeBuffer(d.scopeCtx.ScopeID)
	}

	grpcRefs, err := d.queryGRPCRefs(ctx)
	if err != nil {
		return fmt.Errorf("scip rpc detector: query gRPC refs: %w", err)
	}

	httpRefs, err := d.queryHTTPRefs(ctx)
	if err != nil {
		return fmt.Errorf("scip rpc detector: query HTTP refs: %w", err)
	}

	outboxRefs, err := d.queryOutboxRefs(ctx)
	if err != nil {
		return fmt.Errorf("scip rpc detector: query outbox refs: %w", err)
	}

	if len(grpcRefs)+len(httpRefs)+len(outboxRefs) == 0 {
		return nil
	}
	log.Printf("SCIPRPCDetector: found %d gRPC, %d HTTP, %d async refs in %s",
		len(grpcRefs), len(httpRefs), len(outboxRefs), d.serviceName)

	for _, ref := range grpcRefs {
		callerID, err := d.findEnclosingFunction(ctx, ref.filePath, ref.line)
		if err != nil || callerID == "" {
			continue
		}
		if err := d.writeGRPCCallNode(ctx, ref, callerID); err != nil {
			log.Printf("Warning: SCIPRPCDetector gRPC node for %s: %v", ref.symbol, err)
		}
	}

	for _, ref := range httpRefs {
		callerID, err := d.findEnclosingFunction(ctx, ref.filePath, ref.line)
		if err != nil || callerID == "" {
			continue
		}
		if err := d.writeHTTPCallNode(ctx, ref, callerID); err != nil {
			log.Printf("Warning: SCIPRPCDetector HTTP node for %s: %v", ref.symbol, err)
		}
	}

	for _, ref := range outboxRefs {
		callerID, err := d.findEnclosingFunction(ctx, ref.filePath, ref.line)
		if err != nil || callerID == "" {
			continue
		}
		if err := d.writeOutboxCallNode(ctx, ref, callerID); err != nil {
			log.Printf("Warning: SCIPRPCDetector outbox node for %s: %v", ref.symbol, err)
		}
	}

	if err := d.callBuffer.flush(ctx, d.client); err != nil {
		return fmt.Errorf("scip rpc detector: flush call buffer: %w", err)
	}

	return nil
}

// queryGRPCRefs finds References whose resolved Symbol FQN indicates a gRPC client
// method call (Go, TypeScript, Python patterns).
func (d *SCIPRPCDetector) queryGRPCRefs(ctx context.Context) ([]scipCallRef, error) {
	query := `
		MATCH (svc:Service {name: $serviceName})-[:CONTAINS]->(f:File)
		      -[:CONTAINS]->(ref:Reference)
		      -[:REFERENCES]->(sym:Symbol)
		WHERE ref.scopeId = $scopeId
		  AND NOT ref.filePath ENDS WITH '_test.go'
		  AND (
		    (sym.symbol CONTAINS 'Client' AND (
		      sym.symbol CONTAINS '/grpc/' OR
		      sym.symbol CONTAINS '/grpcv1' OR
		      sym.symbol CONTAINS '/pb/' OR
		      sym.symbol CONTAINS '/proto/' OR
		      sym.symbol CONTAINS '_pb2_grpc'
		    ))
		    OR sym.symbol CONTAINS '@grpc/grpc-js'
		  )
		  AND NOT (sym.symbol CONTAINS 'New' AND sym.symbol ENDS WITH 'Client')
		RETURN ref.filePath AS filePath,
		       ref.startLine AS line,
		       sym.symbol AS symbol,
		       coalesce(sym.displayName, '') AS displayName
		LIMIT 2000
	`
	results, err := d.client.ExecuteQuery(ctx, query, map[string]any{
		"serviceName": d.serviceName,
		"scopeId":     d.scopeCtx.ScopeID,
	})
	if err != nil {
		return nil, err
	}

	refs := make([]scipCallRef, 0, len(results))
	for _, rec := range results {
		rm := rec.AsMap()
		fp := getStringFromMap(rm, "filePath")
		line := int(getInt64FromMap(rm, "line"))
		sym := getStringFromMap(rm, "symbol")
		dn := getStringFromMap(rm, "displayName")
		if fp != "" && line > 0 && sym != "" {
			refs = append(refs, scipCallRef{filePath: fp, line: line, symbol: sym, displayName: dn, callType: "grpc"})
		}
	}
	return refs, nil
}

// queryHTTPRefs finds References whose resolved Symbol FQN indicates an HTTP client call.
func (d *SCIPRPCDetector) queryHTTPRefs(ctx context.Context) ([]scipCallRef, error) {
	query := `
		MATCH (svc:Service {name: $serviceName})-[:CONTAINS]->(f:File)
		      -[:CONTAINS]->(ref:Reference)
		      -[:REFERENCES]->(sym:Symbol)
		WHERE ref.scopeId = $scopeId
		  AND NOT ref.filePath ENDS WITH '_test.go'
		  AND (
		    (sym.symbol CONTAINS 'net/http' AND (
		      sym.symbol CONTAINS 'NewRequest' OR
		      sym.symbol CONTAINS 'NewRequestWithContext' OR
		      sym.symbol CONTAINS '.Get#' OR
		      sym.symbol CONTAINS '.Post#' OR
		      sym.symbol CONTAINS '.Head#' OR
		      sym.symbol CONTAINS '.Do#'
		    ))
		    OR (sym.symbol CONTAINS 'grpc-framework/client/api' AND (
		      sym.symbol CONTAINS 'Get().' OR
		      sym.symbol CONTAINS 'Post().' OR
		      sym.symbol CONTAINS 'Put().' OR
		      sym.symbol CONTAINS 'Patch().' OR
		      sym.symbol CONTAINS 'Delete().' OR
		      sym.symbol CONTAINS 'Do().' OR
		      sym.symbol CONTAINS 'Stream().'
		    ))
		    OR sym.symbol CONTAINS 'resty'
		    OR sym.symbol CONTAINS 'axios'
		    OR sym.symbol CONTAINS 'node-fetch'
		    OR (sym.symbol CONTAINS 'requests' AND (
		      sym.symbol CONTAINS '.get' OR sym.symbol CONTAINS '.post' OR
		      sym.symbol CONTAINS '.put' OR sym.symbol CONTAINS '.delete'
		    ))
		    OR (sym.symbol CONTAINS 'httpx' AND (
		      sym.symbol CONTAINS '.get' OR sym.symbol CONTAINS '.post'
		    ))
		  )
		RETURN ref.filePath AS filePath,
		       ref.startLine AS line,
		       sym.symbol AS symbol,
		       coalesce(sym.displayName, '') AS displayName
		LIMIT 2000
	`
	results, err := d.client.ExecuteQuery(ctx, query, map[string]any{
		"serviceName": d.serviceName,
		"scopeId":     d.scopeCtx.ScopeID,
	})
	if err != nil {
		return nil, err
	}

	refs := make([]scipCallRef, 0, len(results))
	for _, rec := range results {
		rm := rec.AsMap()
		fp := getStringFromMap(rm, "filePath")
		line := int(getInt64FromMap(rm, "line"))
		sym := getStringFromMap(rm, "symbol")
		dn := getStringFromMap(rm, "displayName")
		if fp != "" && line > 0 && sym != "" {
			refs = append(refs, scipCallRef{filePath: fp, line: line, symbol: sym, displayName: dn, callType: "http"})
		}
	}
	return refs, nil
}

// findEnclosingFunction returns the element ID of the Function/Method node whose
// body range contains the given line in the given file.
func (d *SCIPRPCDetector) findEnclosingFunction(ctx context.Context, filePath string, line int) (string, error) {
	query := `
		MATCH (f:File {path: $filePath})-[:CONTAINS]->(fn)
		WHERE (fn:Function OR fn:Method)
		  AND fn.startLine IS NOT NULL
		  AND fn.endLine IS NOT NULL
		  AND fn.startLine <= $line AND fn.endLine >= $line
		RETURN elementId(fn) AS id, (fn.endLine - fn.startLine) AS span
		ORDER BY span ASC
		LIMIT 1
	`
	results, err := d.client.ExecuteQuery(ctx, query, map[string]any{
		"filePath": filePath,
		"line":     line,
	})
	if err != nil || len(results) == 0 {
		return "", err
	}
	return getStringFromMap(results[0].AsMap(), "id"), nil
}

// writeGRPCCallNode creates a GRPCCall node and its CALLS_API / CALLS_SERVICE edges.
func (d *SCIPRPCDetector) writeGRPCCallNode(ctx context.Context, ref scipCallRef, callerID string) error {
	protoService, methodName := parseGRPCSymbol(ref.symbol, ref.displayName)

	nodeKey := fmt.Sprintf("grpccall:scip:%s:%s:%s.%s:%d",
		d.serviceName, ref.filePath, protoService, methodName, ref.line)

	// Derive bare target service name from protoService: "BalanceService" → "balance".
	targetServiceName := strings.ToLower(strings.TrimSuffix(protoService, "Service"))
	if targetServiceName == "" {
		targetServiceName = strings.ToLower(protoService)
	}

	mergeProps := map[string]any{"nodeKey": nodeKey}
	setProps := map[string]any{
		"nodeKey":       nodeKey,
		"callerService": d.serviceName,
		"targetService": targetServiceName,
		"targetMethod":  protoService + "." + methodName,
		"protoPackage":  extractProtoPackage(ref.symbol),
		"protoService":  protoService,
		"protoMethod":   methodName,
		"filePath":      ref.filePath,
		"line":          ref.line,
		"createdAt":     time.Now().UTC().Unix(),
		"updatedAt":     time.Now().UTC().Unix(),
	}
	maps.Copy(setProps, d.scopeCtx.Props())

	if d.callBuffer != nil {
		d.callBuffer.addGRPCCall(nodeKey, setProps)
		d.callBuffer.addCallsAPIEdge(callerID, nodeKey, map[string]any{"line": ref.line})
		if protoService != "" {
			targetSvcID := d.resolveSCIPTargetService(ctx, protoService)
			if targetSvcID != "" {
				d.callBuffer.addCallsServiceEdge(nodeKey, targetSvcID, map[string]any{"protocol": "grpc"})
			}
		}
		return nil
	}

	grpcCallID, err := d.client.MergeNode(ctx, []string{"GRPCCall"}, mergeProps, setProps)
	if err != nil {
		return fmt.Errorf("merge GRPCCall: %w", err)
	}

	if _, err := d.client.MergeRelationship(ctx, callerID, grpcCallID,
		string(models.CallsAPIRel), map[string]any{}, map[string]any{"line": ref.line},
	); err != nil {
		log.Printf("Warning: SCIP gRPC CALLS_API: %v", err)
	}

	if protoService != "" {
		targetSvcID := d.resolveSCIPTargetService(ctx, protoService)
		if targetSvcID != "" {
			if _, err := d.client.MergeRelationship(ctx, grpcCallID, targetSvcID,
				string(models.CallsServiceRel), map[string]any{}, map[string]any{"protocol": "grpc"},
			); err != nil {
				log.Printf("Warning: SCIP gRPC CALLS_SERVICE: %v", err)
			}
		}
	}

	return nil
}

// writeHTTPCallNode creates an HTTPCall node and its CALLS_API edge.
func (d *SCIPRPCDetector) writeHTTPCallNode(ctx context.Context, ref scipCallRef, callerID string) error {
	method := inferHTTPMethodFromSymbol(ref.symbol, ref.displayName)

	nodeKey := fmt.Sprintf("httpcall:scip:%s:%s:%s:%d",
		d.serviceName, ref.filePath, method, ref.line)

	mergeProps := map[string]any{"nodeKey": nodeKey}
	setProps := map[string]any{
		"nodeKey":       nodeKey,
		"callerService": d.serviceName,
		"targetService": "",
		"url":           "dynamic",
		"method":        method,
		"filePath":      ref.filePath,
		"line":          ref.line,
		"createdAt":     time.Now().UTC().Unix(),
		"updatedAt":     time.Now().UTC().Unix(),
	}
	maps.Copy(setProps, d.scopeCtx.Props())

	if d.callBuffer != nil {
		d.callBuffer.addHTTPCall(nodeKey, setProps)
		d.callBuffer.addCallsAPIEdge(callerID, nodeKey, map[string]any{"line": ref.line})
		return nil
	}

	httpCallID, err := d.client.MergeNode(ctx, []string{"HTTPCall"}, mergeProps, setProps)
	if err != nil {
		return fmt.Errorf("merge HTTPCall: %w", err)
	}

	if _, err := d.client.MergeRelationship(ctx, callerID, httpCallID,
		string(models.CallsAPIRel), map[string]any{}, map[string]any{"line": ref.line},
	); err != nil {
		log.Printf("Warning: SCIP HTTP CALLS_API: %v", err)
	}

	return nil
}

// resolveSCIPTargetService does a fuzzy name match against Service nodes.
func (d *SCIPRPCDetector) resolveSCIPTargetService(ctx context.Context, name string) string {
	if name == "" {
		return ""
	}
	if d.serviceIdx != nil {
		if id := d.serviceIdx.resolveByName(name); id != "" {
			return id
		}
		if id := d.serviceIdx.resolveByProto(name); id != "" {
			return id
		}
	}
	cypher := `
		MATCH (s:Service)
		WHERE toLower(s.name) CONTAINS toLower($name)
		   OR toLower($name) CONTAINS toLower(s.name)
		RETURN elementId(s) AS id
		LIMIT 1
	`
	results, err := d.client.ExecuteQuery(ctx, cypher, map[string]any{"name": name})
	if err != nil || len(results) == 0 {
		return ""
	}
	return getStringFromMap(results[0].AsMap(), "id")
}

// parseGRPCSymbol extracts the target service name and method name from a SCIP symbol FQN.
// e.g. "github.com/org/repo/proto/payment/grpc/v1 . PaymentServiceClient CreatePayment()."
//
//	→ ("PaymentService", "CreatePayment")
func parseGRPCSymbol(symbol, displayName string) (serviceName, methodName string) {
	if displayName != "" {
		methodName = displayName
	}

	// Scan tokens for a token ending in "ServiceClient" or "Client".
	tokens := strings.FieldsFunc(symbol, func(r rune) bool {
		return r == ' ' || r == '/' || r == '.' || r == '#'
	})
	for _, tok := range tokens {
		if strings.HasSuffix(tok, "ServiceClient") {
			serviceName = strings.TrimSuffix(tok, "Client")
			break
		}
		if strings.HasSuffix(tok, "Client") && len(tok) > 6 {
			serviceName = strings.TrimSuffix(tok, "Client")
			break
		}
	}

	// If displayName wasn't set, find the method name as the last meaningful token.
	if methodName == "" && len(tokens) > 0 {
		for i := len(tokens) - 1; i >= 0; i-- {
			tok := tokens[i]
			if tok == "" || tok == "()" || (tok == strings.ToLower(tok) && len(tok) > 2) {
				continue
			}
			methodName = strings.TrimSuffix(tok, "()")
			break
		}
	}

	return
}

// extractProtoPackage attempts to extract the proto package path component from a symbol FQN.
func extractProtoPackage(symbol string) string {
	for seg := range strings.SplitSeq(symbol, " ") {
		for part := range strings.SplitSeq(seg, "/") {
			lp := strings.ToLower(part)
			if strings.Contains(lp, "grpc") || lp == "pb" || strings.Contains(lp, "proto") {
				return part
			}
		}
	}
	return ""
}

// inferHTTPMethodFromSymbol guesses the HTTP method from a SCIP symbol FQN or display name.
func inferHTTPMethodFromSymbol(symbol, displayName string) string {
	combined := strings.ToUpper(symbol + " " + displayName)
	for _, method := range []string{"POST", "PUT", "PATCH", "DELETE", "HEAD", "GET"} {
		if strings.Contains(combined, method) {
			return method
		}
	}
	return "ANY"
}

// queryOutboxRefs finds References whose resolved Symbol FQN indicates an async message
// publish call — SQS, Kafka, NATS, or outbox-pattern wrappers — for all languages.
func (d *SCIPRPCDetector) queryOutboxRefs(ctx context.Context) ([]scipOutboxRef, error) {
	query := `
		MATCH (svc:Service {name: $serviceName})-[:CONTAINS]->(f:File)
		      -[:CONTAINS]->(ref:Reference)
		      -[:REFERENCES]->(sym:Symbol)
		WHERE ref.scopeId = $scopeId
		  AND NOT ref.filePath ENDS WITH '_test.go'
		  AND (
		    (sym.symbol CONTAINS 'sqs' AND (
		      sym.symbol CONTAINS 'SendMessage' OR
		      sym.symbol CONTAINS 'send_message' OR
		      sym.symbol CONTAINS 'SendMessageBatch'
		    ))
		    OR sym.symbol CONTAINS '@aws-sdk/client-sqs'
		    OR (sym.symbol CONTAINS 'kafka' AND (
		      sym.symbol CONTAINS '#Produce' OR
		      sym.symbol CONTAINS '#produce' OR
		      sym.symbol CONTAINS 'WriteMessages' OR
		      sym.symbol CONTAINS 'WriteMessage' OR
		      sym.symbol CONTAINS 'Producer#send'
		    ))
		    OR sym.symbol CONTAINS 'kafkajs'
		    OR (sym.symbol CONTAINS 'nats' AND (
		      sym.symbol CONTAINS '#Publish' OR
		      sym.symbol CONTAINS '#publish' OR
		      sym.symbol CONTAINS 'PublishMsg' OR
		      sym.symbol CONTAINS 'PublishRequest'
		    ))
		    OR sym.displayName IN [
		      'SaveOutboxEvent', 'InsertOutboxEvent', 'AddOutboxEvent',
		      'PublishOutboxEvent', 'EnqueueOutboxEvent', 'CreateOutboxEvent',
		      'StoreOutboxEvent', 'EmitEvent', 'PublishEvent', 'EnqueueEvent',
		      'DispatchEvent'
		    ]
		  )
		RETURN ref.filePath AS filePath,
		       ref.startLine AS line,
		       sym.symbol AS symbol,
		       coalesce(sym.displayName, '') AS displayName
		LIMIT 2000
	`
	results, err := d.client.ExecuteQuery(ctx, query, map[string]any{
		"serviceName": d.serviceName,
		"scopeId":     d.scopeCtx.ScopeID,
	})
	if err != nil {
		return nil, err
	}

	refs := make([]scipOutboxRef, 0, len(results))
	for _, rec := range results {
		rm := rec.AsMap()
		fp := getStringFromMap(rm, "filePath")
		line := int(getInt64FromMap(rm, "line"))
		sym := getStringFromMap(rm, "symbol")
		dn := getStringFromMap(rm, "displayName")
		if fp != "" && line > 0 && sym != "" {
			refs = append(refs, scipOutboxRef{
				filePath:    fp,
				line:        line,
				symbol:      sym,
				displayName: dn,
				transport:   inferTransportFromSymbol(sym, dn),
			})
		}
	}
	return refs, nil
}

// writeOutboxCallNode creates an OutboxCall node and its CALLS_API edge.
func (d *SCIPRPCDetector) writeOutboxCallNode(ctx context.Context, ref scipOutboxRef, callerID string) error {
	nodeKey := fmt.Sprintf("outboxcall:scip:%s:%s:%s:dynamic:%d",
		d.serviceName, ref.filePath, ref.transport, ref.line)

	mergeProps := map[string]any{"nodeKey": nodeKey}
	setProps := map[string]any{
		"nodeKey":       nodeKey,
		"callerService": d.serviceName,
		"transport":     ref.transport,
		"eventType":     "dynamic",
		"queueOrTopic":  "dynamic",
		"filePath":      ref.filePath,
		"line":          ref.line,
		"createdAt":     time.Now().UTC().Unix(),
		"updatedAt":     time.Now().UTC().Unix(),
	}
	maps.Copy(setProps, d.scopeCtx.Props())

	if d.callBuffer != nil {
		d.callBuffer.addOutboxCall(nodeKey, setProps)
		d.callBuffer.addCallsAPIEdge(callerID, nodeKey, map[string]any{
			"line":      ref.line,
			"transport": ref.transport,
		})
		return nil
	}

	outboxCallID, err := d.client.MergeNode(ctx, []string{"OutboxCall"}, mergeProps, setProps)
	if err != nil {
		return fmt.Errorf("merge OutboxCall: %w", err)
	}

	if _, err := d.client.MergeRelationship(ctx, callerID, outboxCallID,
		string(models.CallsAPIRel), map[string]any{}, map[string]any{"line": ref.line, "transport": ref.transport},
	); err != nil {
		log.Printf("Warning: SCIP outbox CALLS_API: %v", err)
	}

	return nil
}

// inferTransportFromSymbol determines the async transport type from a SCIP symbol FQN
// or display name ("sqs", "kafka", "nats", or "outbox" for wrapper patterns).
func inferTransportFromSymbol(symbol, displayName string) string {
	lsym := strings.ToLower(symbol + " " + displayName)
	switch {
	case strings.Contains(lsym, "sqs") || strings.Contains(lsym, "@aws-sdk/client-sqs"):
		return "sqs"
	case strings.Contains(lsym, "kafka") || strings.Contains(lsym, "kafkajs"):
		return "kafka"
	case strings.Contains(lsym, "nats"):
		return "nats"
	default:
		return "outbox"
	}
}
