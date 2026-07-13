package pipeline

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	neo4jdriver "github.com/neo4j/neo4j-go-driver/v5/neo4j"

	models "github.com/context-maximiser/code-graph/libs/core-models-go"
	"github.com/context-maximiser/code-graph/libs/neo4j-go"
)

// CrossServiceHandlerResolver matches GRPCCall and HTTPCall nodes that have a
// CALLS_SERVICE edge to an already-indexed Service, then writes a RESOLVES_TO
// edge to the concrete handler Function/Method in that service.
//
// Confidence levels:
//   - 0.9  proto_matched      — exactly one Function with that method name found
//   - 0.6  heuristic          — multiple candidates; first (alphabetically lowest nodeKey) chosen
//   - 0.85 http_route_matched — APIRoute path+method uniquely identifies a handler
//   - 0.6  heuristic          — multiple HTTP candidates; best path-overlap chosen
type CrossServiceHandlerResolver struct {
	client   *neo4j.Client
	scopeCtx models.ScopeContext
}

// NewCrossServiceHandlerResolver creates a resolver scoped to the given Neo4j client.
func NewCrossServiceHandlerResolver(client *neo4j.Client) *CrossServiceHandlerResolver {
	return &CrossServiceHandlerResolver{client: client}
}

// SetScope configures the tenant/scope context used to narrow queries.
func (r *CrossServiceHandlerResolver) SetScope(ctx models.ScopeContext) {
	r.scopeCtx = ctx
}

// Resolve finds all resolvable cross-service call sites and writes RESOLVES_TO edges.
// Returns the total number of edges written.
func (r *CrossServiceHandlerResolver) Resolve(ctx context.Context) (int, error) {
	grpcCount, err := r.resolveGRPC(ctx)
	if err != nil {
		log.Printf("[CrossServiceResolver] gRPC resolution error: %v", err)
	}

	// Second pass: link GRPCCall nodes that have no CALLS_SERVICE edge yet by matching
	// protoService+protoMethod against handler receiverType across all indexed services.
	unlinkedCount, err := r.resolveGRPCUnlinked(ctx)
	if err != nil {
		log.Printf("[CrossServiceResolver] gRPC unlinked resolution error: %v", err)
	}

	httpCount, err := r.resolveHTTP(ctx)
	if err != nil {
		log.Printf("[CrossServiceResolver] HTTP resolution error: %v", err)
	}

	total := grpcCount + unlinkedCount + httpCount
	log.Printf("[CrossServiceResolver] Wrote %d RESOLVES_TO edges (%d gRPC-linked, %d gRPC-unlinked, %d HTTP)",
		total, grpcCount, unlinkedCount, httpCount)

	// Async pass: link EventType hubs to their listener/handler functions via ROUTED_TO.
	asyncCount, err := r.resolveAsyncConsumers(ctx)
	if err != nil {
		log.Printf("[CrossServiceResolver] async resolution error: %v", err)
	}
	log.Printf("[CrossServiceResolver] Wrote %d ROUTED_TO edges (async event listeners/handlers)", asyncCount)

	// Downstream pass: bridge OutboxCall nodes into the synchronous traversal structure by
	// writing OutboxCall→CALLS_SERVICE and OutboxCall→RESOLVES_TO edges. This lets the
	// existing cross-service flow BFS see async hops the same way it sees gRPC/HTTP hops.
	svcCount, resCount, err := r.linkOutboxCallsToDownstream(ctx)
	if err != nil {
		log.Printf("[CrossServiceResolver] OutboxCall downstream link error: %v", err)
	}
	log.Printf("[CrossServiceResolver] Wrote %d CALLS_SERVICE + %d RESOLVES_TO edges (OutboxCall→downstream)", svcCount, resCount)

	// External pass: aggregate Service → DEPENDS_ON_EXTERNAL → ExternalService rollup edges.
	if err := r.resolveExternalDependencies(ctx); err != nil {
		log.Printf("[CrossServiceResolver] external dependency rollup error: %v", err)
	}

	return total, nil
}

// resolveExternalDependencies creates Service → DEPENDS_ON_EXTERNAL → ExternalService rollup
// edges by aggregating all ExternalCall nodes that reach the hub.
func (r *CrossServiceHandlerResolver) resolveExternalDependencies(ctx context.Context) error {
	_, err := r.client.ExecuteQuery(ctx, `
		MATCH (ec:ExternalCall)-[:USES_SERVICE]->(es:ExternalService)
		MATCH (svc:Service {name: ec.callerService})
		WITH svc, es, collect(DISTINCT ec.operation) AS ops, count(ec) AS n
		MERGE (svc)-[rel:DEPENDS_ON_EXTERNAL]->(es)
		SET rel.operations = ops, rel.callCount = n, rel.resolvedAt = $now
	`, map[string]any{"now": time.Now().Unix()})
	if err != nil {
		return fmt.Errorf("resolveExternalDependencies: %w", err)
	}
	return nil
}

// resolveAsyncConsumers links each EventType hub to the concrete listener/handler functions
// in its consuming service(s), mirroring the two-tier gRPC/HTTP resolution:
//
//   - Tier 1 (confidence 1.0): the SQS listener entry. For every distinct destQueue that an
//     EventType is emitted to, find the function marked listensOnQueue = destQueue and write
//     ROUTED_TO {tier:'entry'}. This is always correct — the queue→listener binding is exact.
//   - Tier 2 (confidence 0.8, best-effort): the precise per-event handler. Within destService,
//     find a function whose handlesEvents list contains the event name and write
//     ROUTED_TO {tier:'handler'}. Skipped silently when nothing matches (e.g. dynamic hubs).
func (r *CrossServiceHandlerResolver) resolveAsyncConsumers(ctx context.Context) (int, error) {
	written := 0

	// Tier 1 — listener entry (per distinct destQueue an event is emitted to).
	entryQuery := `
		MATCH (oc:OutboxCall)-[e:EMITS_EVENT]->(et:EventType)
		WITH DISTINCT et, e.destQueue AS destQueue, e.destService AS destService
		WHERE destQueue IS NOT NULL AND destQueue <> ''
		MATCH (f)
		WHERE (f:Function OR f:Method) AND f.listensOnQueue = destQueue
		RETURN elementId(et) AS etId, elementId(f) AS fId,
		       coalesce(destService, f.listensService, '') AS service
	`
	entryRows, err := r.client.ExecuteQuery(ctx, entryQuery, map[string]any{})
	if err != nil {
		return written, fmt.Errorf("resolveAsyncConsumers: entry query: %w", err)
	}
	for _, row := range entryRows {
		rm := row.AsMap()
		etId := getString(rm, "etId")
		fId := getString(rm, "fId")
		if etId == "" || fId == "" {
			continue
		}
		if err := r.writeRoutedToEdge(ctx, etId, fId, getString(rm, "service"), "entry", 1.0); err != nil {
			log.Printf("[CrossServiceResolver] ROUTED_TO(entry) write failed (%s → %s): %v", etId, fId, err)
			continue
		}
		written++
	}

	// Tier 2 — action-route dispatcher (best-effort; <group>ActionRoute functions).
	handlerQuery := `
		MATCH (oc:OutboxCall)-[e:EMITS_EVENT]->(et:EventType)
		WHERE coalesce(et.dynamic, false) = false
		WITH DISTINCT et, et.eventType AS event, e.destService AS destService
		WHERE destService IS NOT NULL AND destService <> ''
		MATCH (svc:Service {name: destService})-[:CONTAINS*1..5]->(f)
		WHERE (f:Function OR f:Method) AND f.handlesEvents IS NOT NULL AND event IN f.handlesEvents
		RETURN elementId(et) AS etId, elementId(f) AS fId, destService AS service
	`
	handlerRows, err := r.client.ExecuteQuery(ctx, handlerQuery, map[string]any{})
	if err != nil {
		return written, fmt.Errorf("resolveAsyncConsumers: handler query: %w", err)
	}
	for _, row := range handlerRows {
		rm := row.AsMap()
		etId := getString(rm, "etId")
		fId := getString(rm, "fId")
		if etId == "" || fId == "" {
			continue
		}
		if err := r.writeRoutedToEdge(ctx, etId, fId, getString(rm, "service"), "handler", 0.8); err != nil {
			log.Printf("[CrossServiceResolver] ROUTED_TO(handler) write failed (%s → %s): %v", etId, fId, err)
			continue
		}
		written++
	}

	// Tier 2b — direct action handler (highest confidence; functions stamped by
	// buildRouteHandlerEvents — the concrete business-logic function per action, e.g.
	// handlePayout for payout.failed rather than payoutActionRoute).
	actionHandlerQuery := `
		MATCH (oc:OutboxCall)-[e:EMITS_EVENT]->(et:EventType)
		WHERE coalesce(et.dynamic, false) = false
		WITH DISTINCT et, et.eventType AS event, e.destService AS destService
		WHERE destService IS NOT NULL AND destService <> ''
		MATCH (svc:Service {name: destService})-[:CONTAINS*1..5]->(f)
		WHERE (f:Function OR f:Method)
		  AND f.handlesEventsDirectly IS NOT NULL AND event IN f.handlesEventsDirectly
		RETURN elementId(et) AS etId, elementId(f) AS fId, destService AS service
	`
	actionHandlerRows, err := r.client.ExecuteQuery(ctx, actionHandlerQuery, map[string]any{})
	if err != nil {
		return written, fmt.Errorf("resolveAsyncConsumers: action_handler query: %w", err)
	}
	for _, row := range actionHandlerRows {
		rm := row.AsMap()
		etId := getString(rm, "etId")
		fId := getString(rm, "fId")
		if etId == "" || fId == "" {
			continue
		}
		if err := r.writeRoutedToEdge(ctx, etId, fId, getString(rm, "service"), "action_handler", 0.9); err != nil {
			log.Printf("[CrossServiceResolver] ROUTED_TO(action_handler) write failed (%s → %s): %v", etId, fId, err)
			continue
		}
		written++
	}

	return written, nil
}

// linkOutboxCallsToDownstream bridges async OutboxCall nodes into the synchronous traversal
// structure used by the cross-service flow tool. It writes two edge types:
//
//   - OutboxCall → CALLS_SERVICE → Service  (always, when destService is known)
//     Lets BFS treat an async emit the same as a gRPC/HTTP call: the next hop is
//     the destService, even though no synchronous handler exists there yet.
//
//   - OutboxCall → RESOLVES_TO → Function  (best-effort)
//     Uses the ROUTED_TO edges already written by resolveAsyncConsumers: prefers
//     the tier-2 per-event handler (confidence 0.8) over the tier-1 listener entry
//     (confidence 1.0 structurally, but less descriptive for a "handler" column).
//     Skipped when no ROUTED_TO target exists (e.g. dynamic/unresolved events).
//
// Returns (callsServiceEdges, resolvesToEdges, error).
func (r *CrossServiceHandlerResolver) linkOutboxCallsToDownstream(ctx context.Context) (int, int, error) {
	// --- CALLS_SERVICE pass ---
	// Enumerate distinct (OutboxCall, destService) pairs and merge the edge.
	svcQuery := `
		MATCH (oc:OutboxCall)
		WHERE oc.destService IS NOT NULL AND oc.destService <> ''
		MATCH (svc:Service {name: oc.destService})
		MERGE (oc)-[r:CALLS_SERVICE]->(svc)
		ON CREATE SET r.protocol = 'async', r.resolved = true
		ON MATCH  SET r.protocol = 'async', r.resolved = true
		RETURN count(r) AS written
	`
	svcRows, err := r.client.ExecuteQuery(ctx, svcQuery, map[string]any{})
	if err != nil {
		return 0, 0, fmt.Errorf("linkOutboxCallsToDownstream: CALLS_SERVICE: %w", err)
	}
	svcCount := 0
	if len(svcRows) > 0 {
		if v, ok := svcRows[0].AsMap()["written"].(int64); ok {
			svcCount = int(v)
		}
	}

	// --- RESOLVES_TO pass ---
	// For each OutboxCall that emits to an EventType, pick the best handler via ROUTED_TO.
	// Preference order: action_handler (0.9, direct business logic) >
	//                   handler        (0.8, <group>ActionRoute dispatcher) >
	//                   entry          (1.0 structural, but least specific for display).
	resQuery := `
		MATCH (oc:OutboxCall)-[:EMITS_EVENT]->(et:EventType)
		WHERE oc.destService IS NOT NULL AND oc.destService <> ''
		  AND NOT (oc)-[:RESOLVES_TO]->()
		OPTIONAL MATCH (et)-[ra:ROUTED_TO {tier: 'action_handler'}]->(ah)
		WHERE ra.service = oc.destService
		WITH oc, et, ah
		OPTIONAL MATCH (et)-[rh:ROUTED_TO {tier: 'handler'}]->(handler)
		WHERE rh.service = oc.destService AND ah IS NULL
		WITH oc, et, coalesce(ah, handler) AS preferred
		OPTIONAL MATCH (et)-[re:ROUTED_TO {tier: 'entry'}]->(entry)
		WHERE re.service = oc.destService AND preferred IS NULL
		WITH oc, coalesce(preferred, entry) AS best
		WHERE best IS NOT NULL
		MERGE (oc)-[r:RESOLVES_TO]->(best)
		ON CREATE SET r.confidence = 0.9, r.resolutionMethod = 'async_event'
		ON MATCH  SET r.confidence = 0.9, r.resolutionMethod = 'async_event'
		RETURN count(r) AS written
	`
	resRows, err := r.client.ExecuteQuery(ctx, resQuery, map[string]any{})
	if err != nil {
		return svcCount, 0, fmt.Errorf("linkOutboxCallsToDownstream: RESOLVES_TO: %w", err)
	}
	resCount := 0
	if len(resRows) > 0 {
		if v, ok := resRows[0].AsMap()["written"].(int64); ok {
			resCount = int(v)
		}
	}

	return svcCount, resCount, nil
}

// writeRoutedToEdge merges a ROUTED_TO edge from an EventType hub to a listener/handler.
func (r *CrossServiceHandlerResolver) writeRoutedToEdge(
	ctx context.Context,
	eventTypeId, handlerFuncId, service, tier string,
	confidence float64,
) error {
	now := time.Now().UTC().Unix()
	_, err := r.client.MergeRelationship(
		ctx,
		eventTypeId, handlerFuncId,
		string(models.RoutedToRel),
		map[string]any{"tier": tier},
		map[string]any{
			"service":    service,
			"tier":       tier,
			"confidence": confidence,
			"resolvedAt": now,
		},
	)
	return err
}

// resolveGRPC processes GRPCCall → CALLS_SERVICE → Service edges and writes
// RESOLVES_TO edges to the best-matching handler Function in the target service.
func (r *CrossServiceHandlerResolver) resolveGRPC(ctx context.Context) (int, error) {
	// Fetch all GRPCCall nodes that already have a resolved target service.
	callsQuery := `
		MATCH (gc:GRPCCall)-[:CALLS_SERVICE]->(svc:Service)
		WHERE gc.targetMethod IS NOT NULL AND gc.targetMethod <> ''
		RETURN elementId(gc) AS gcId,
		       gc.targetMethod AS targetMethod,
		       elementId(svc) AS svcId,
		       svc.name AS svcName,
		       coalesce(gc.protoService, '') AS protoService
	`
	rows, err := r.client.ExecuteQuery(ctx, callsQuery, map[string]any{})
	if err != nil {
		return 0, fmt.Errorf("resolveGRPC: query CALLS_SERVICE: %w", err)
	}

	written := 0
	for _, row := range rows {
		rm := row.AsMap()
		gcId := getString(rm, "gcId")
		targetMethod := getString(rm, "targetMethod")
		svcId := getString(rm, "svcId")

		if gcId == "" || svcId == "" || targetMethod == "" {
			continue
		}

		// targetMethod format: "ServiceName.MethodName" or just "MethodName"
		methodName := extractMethodName(targetMethod)
		if methodName == "" {
			continue
		}

		// Two-pass resolution: try proto server type match first for high confidence.
		protoService := getString(rm, "protoService")
		var handlers []*neo4jdriver.Record
		confidence := 0.7
		resolutionMethod := "name_match"

		if protoService != "" {
			// receiverType may be stored with or without a pointer prefix
			// ("*BalanceServiceServer" or "BalanceServiceServer") depending on whether
			// the handler uses a pointer receiver — use ENDS WITH to match both.
			// SCIP indexers suffix method names with a descriptor like "()." so we
			// match both "GetBalance" and "GetBalance()." with STARTS WITH.
			protoServerType := protoService + "Server"
			protoHandlerQuery := `
				MATCH (svc)-[:CONTAINS*1..5]->(f)
				WHERE elementId(svc) = $svcId
				  AND (f:Function OR f:Method)
				  AND (f.name = $methodName OR f.name STARTS WITH ($methodName + '('))
				  AND f.receiverType ENDS WITH $protoServerType
				RETURN elementId(f) AS fId, f.name AS fName, f.nodeKey AS nodeKey
				ORDER BY f.nodeKey ASC
				LIMIT 3
			`
			protoHandlers, protoErr := r.client.ExecuteQuery(ctx, protoHandlerQuery, map[string]any{
				"svcId":           svcId,
				"methodName":      methodName,
				"protoServerType": protoServerType,
			})
			if protoErr == nil && len(protoHandlers) > 0 {
				handlers = protoHandlers
				confidence = 1.0
				resolutionMethod = "proto"
			}
		}

		// Fall back to name-only query if proto match found nothing.
		if len(handlers) == 0 {
			nameHandlerQuery := `
				MATCH (svc)-[:CONTAINS*1..5]->(f)
				WHERE elementId(svc) = $svcId
				  AND (f:Function OR f:Method)
				  AND (f.name = $methodName OR f.name STARTS WITH ($methodName + '('))
				RETURN elementId(f) AS fId, f.name AS fName, f.nodeKey AS nodeKey
				ORDER BY f.nodeKey ASC
				LIMIT 5
			`
			nameHandlers, nameErr := r.client.ExecuteQuery(ctx, nameHandlerQuery, map[string]any{
				"svcId":      svcId,
				"methodName": methodName,
			})
			if nameErr != nil || len(nameHandlers) == 0 {
				continue
			}
			handlers = nameHandlers
			if len(handlers) == 1 {
				confidence = 0.7
			} else {
				confidence = 0.6
				resolutionMethod = "heuristic"
			}
		}

		best := handlers[0].AsMap()
		fId := getString(best, "fId")
		if fId == "" {
			continue
		}

		if err := r.writeResolvesToEdge(ctx, gcId, fId, confidence, resolutionMethod); err != nil {
			log.Printf("[CrossServiceResolver] RESOLVES_TO write failed (%s → %s): %v", gcId, fId, err)
			continue
		}
		written++
	}
	return written, nil
}

// resolveGRPCUnlinked handles GRPCCall nodes that have no CALLS_SERVICE edge yet.
// It searches all indexed services for a handler whose receiverType matches
// protoService+"Server" and whose name matches protoMethod, then writes both
// CALLS_SERVICE (GRPCCall→Service) and RESOLVES_TO (GRPCCall→Method) edges.
func (r *CrossServiceHandlerResolver) resolveGRPCUnlinked(ctx context.Context) (int, error) {
	// Fetch GRPCCall nodes with protoService set but no CALLS_SERVICE edge yet.
	callsQuery := `
		MATCH (gc:GRPCCall)
		WHERE gc.protoService IS NOT NULL AND gc.protoService <> ''
		  AND gc.protoMethod  IS NOT NULL AND gc.protoMethod  <> ''
		  AND NOT (gc)-[:CALLS_SERVICE]->()
		RETURN elementId(gc) AS gcId,
		       gc.protoService AS protoService,
		       gc.protoMethod  AS protoMethod
	`
	rows, err := r.client.ExecuteQuery(ctx, callsQuery, map[string]any{})
	if err != nil {
		return 0, fmt.Errorf("resolveGRPCUnlinked: fetch unlinked: %w", err)
	}

	written := 0
	for _, row := range rows {
		rm := row.AsMap()
		gcId := getString(rm, "gcId")
		protoService := getString(rm, "protoService")
		protoMethod := getString(rm, "protoMethod")

		if gcId == "" || protoService == "" || protoMethod == "" {
			continue
		}

		// Search all services for a method whose receiverType ends with protoService+"Server"
		// and whose name = protoMethod. Use ENDS WITH to handle both plain and pointer-prefixed
		// receiver types ("BalanceServiceServer" and "*BalanceServiceServer").
		protoServerType := protoService + "Server"
		handlerQuery := `
			MATCH (svc:Service)-[:CONTAINS*1..5]->(f)
			WHERE (f:Function OR f:Method)
			  AND (f.name = $methodName OR f.name STARTS WITH ($methodName + '('))
			  AND f.receiverType ENDS WITH $protoServerType
			RETURN elementId(svc) AS svcId, elementId(f) AS fId, f.nodeKey AS nodeKey
			ORDER BY f.nodeKey ASC
			LIMIT 3
		`
		handlers, err := r.client.ExecuteQuery(ctx, handlerQuery, map[string]any{
			"methodName":      protoMethod,
			"protoServerType": protoServerType,
		})
		if err != nil {
			continue
		}

		// Fallback: receiver type match failed; try name-only across services that implement
		// the proto server interface (finds the method even if receiverType wasn't stored).
		if len(handlers) == 0 {
			nameOnlyQuery := `
				MATCH (svc:Service)-[:CONTAINS*1..5]->(f)
				WHERE (f:Function OR f:Method)
				  AND (f.name = $methodName OR f.name STARTS WITH ($methodName + '('))
				  AND (f.receiverType CONTAINS $protoService OR f.signature CONTAINS $protoService)
				RETURN elementId(svc) AS svcId, elementId(f) AS fId, f.nodeKey AS nodeKey
				ORDER BY f.nodeKey ASC
				LIMIT 3
			`
			handlers, err = r.client.ExecuteQuery(ctx, nameOnlyQuery, map[string]any{
				"methodName":   protoMethod,
				"protoService": protoService,
			})
			if err != nil || len(handlers) == 0 {
				continue
			}
		}

		best := handlers[0].AsMap()
		svcId := getString(best, "svcId")
		fId := getString(best, "fId")
		if svcId == "" || fId == "" {
			continue
		}

		// Write CALLS_SERVICE: GRPCCall → Service
		if _, err := r.client.MergeRelationship(ctx,
			gcId, svcId,
			string(models.CallsServiceRel),
			map[string]any{},
			map[string]any{"protocol": "grpc", "resolved": true},
		); err != nil {
			log.Printf("[CrossServiceResolver] CALLS_SERVICE write failed (%s → %s): %v", gcId, svcId, err)
			continue
		}

		// Write RESOLVES_TO: GRPCCall → handler Method
		confidence := 1.0
		resolutionMethod := "proto"
		if len(handlers) > 1 {
			confidence = 0.6
			resolutionMethod = "heuristic"
		}
		if err := r.writeResolvesToEdge(ctx, gcId, fId, confidence, resolutionMethod); err != nil {
			log.Printf("[CrossServiceResolver] RESOLVES_TO write failed (%s → %s): %v", gcId, fId, err)
			continue
		}
		written++
	}
	return written, nil
}

// resolveHTTP processes HTTPCall → CALLS_SERVICE → Service edges and writes
// RESOLVES_TO edges to the handler Function that matches the target route path.
func (r *CrossServiceHandlerResolver) resolveHTTP(ctx context.Context) (int, error) {
	callsQuery := `
		MATCH (hc:HTTPCall)-[:CALLS_SERVICE]->(svc:Service)
		WHERE hc.url IS NOT NULL AND hc.url <> '' AND hc.url <> 'dynamic'
		RETURN elementId(hc) AS hcId,
		       hc.url AS url,
		       hc.method AS httpMethod,
		       elementId(svc) AS svcId,
		       svc.name AS svcName
	`
	rows, err := r.client.ExecuteQuery(ctx, callsQuery, map[string]any{})
	if err != nil {
		return 0, fmt.Errorf("resolveHTTP: query CALLS_SERVICE: %w", err)
	}

	written := 0
	for _, row := range rows {
		rm := row.AsMap()
		hcId := getString(rm, "hcId")
		rawURL := getString(rm, "url")
		httpMethod := strings.ToUpper(getString(rm, "httpMethod"))
		svcId := getString(rm, "svcId")

		if hcId == "" || svcId == "" || rawURL == "" {
			continue
		}

		urlPath := extractURLPath(rawURL)
		if urlPath == "" {
			continue
		}
		if httpMethod == "" {
			httpMethod = "ANY"
		}

		// APIRoute nodes no longer exist — HTTP handler resolution via route path unavailable.
		handlerQuery := `
			MATCH (svc)-[:CONTAINS*1..5]->(f)
			WHERE elementId(svc) = $svcId
			  AND (f:Function OR f:Method)
			  AND f.nodeKey IS NOT NULL
			  AND (toLower(coalesce(f.name,'')) CONTAINS toLower($urlPath)
			       OR toLower($urlPath) CONTAINS toLower(coalesce(f.name,'')))
			RETURN elementId(f) AS fId, f.name AS fName, f.name AS routePath
			ORDER BY size(coalesce(f.name,'')) DESC
			LIMIT 5
		`
		handlers, err := r.client.ExecuteQuery(ctx, handlerQuery, map[string]any{
			"svcId":      svcId,
			"urlPath":    urlPath,
			"httpMethod": httpMethod,
		})
		if err != nil || len(handlers) == 0 {
			continue
		}

		confidence, resolutionMethod := scoreHTTPMatch(len(handlers))
		best := handlers[0].AsMap()
		fId := getString(best, "fId")
		if fId == "" {
			continue
		}

		if err := r.writeResolvesToEdge(ctx, hcId, fId, confidence, resolutionMethod); err != nil {
			log.Printf("[CrossServiceResolver] RESOLVES_TO write failed (%s → %s): %v", hcId, fId, err)
			continue
		}
		written++
	}
	return written, nil
}

// writeResolvesToEdge merges a RESOLVES_TO edge between callSiteId and handlerFuncId.
func (r *CrossServiceHandlerResolver) writeResolvesToEdge(
	ctx context.Context,
	callSiteId, handlerFuncId string,
	confidence float64, resolutionMethod string,
) error {
	now := time.Now().UTC().Unix()
	_, err := r.client.MergeRelationship(
		ctx,
		callSiteId, handlerFuncId,
		string(models.ResolvesToRel),
		map[string]any{},
		map[string]any{
			"confidence":       confidence,
			"resolutionMethod": resolutionMethod,
			"resolvedAt":       now,
		},
	)
	return err
}

// extractMethodName returns the last segment of a dot-separated method path.
// "PaymentService.CreatePayment" → "CreatePayment"
// "CreatePayment" → "CreatePayment"
func extractMethodName(targetMethod string) string {
	parts := strings.Split(targetMethod, ".")
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}

// extractURLPath strips scheme+host from a URL, returning just the path component.
// "https://payments.internal/v1/charge" → "/v1/charge"
func extractURLPath(rawURL string) string {
	for _, prefix := range []string{"https://", "http://", "grpc://"} {
		if strings.HasPrefix(rawURL, prefix) {
			rawURL = rawURL[len(prefix):]
			break
		}
	}
	// rawURL is now "host/path" or just "path" — skip to the first '/'
	if idx := strings.Index(rawURL, "/"); idx >= 0 {
		return rawURL[idx:]
	}
	// No slash: treat the whole string as a path segment (e.g. relative URLs)
	return "/" + rawURL
}


func scoreHTTPMatch(candidates int) (confidence float64, method string) {
	if candidates == 1 {
		return 0.85, "http_route_matched"
	}
	return 0.6, "heuristic"
}

func getString(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}
