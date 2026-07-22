package static

import (
	"context"
	"fmt"

	models "github.com/context-maximiser/code-graph/libs/core-models-go"
	"github.com/context-maximiser/code-graph/libs/neo4j-go"
)

// APISurfaceDetector identifies API surface using purely graph-structural
// signals — zero framework pattern catalogs. It reads parameter types,
// cross-package call edges, and export status to determine which functions
// constitute the project's API boundary.
type APISurfaceDetector struct {
	client       *neo4j.Client
	projectModule string // Go module path (e.g., "github.com/context-maximiser/code-graph")
	scopeCtx     models.ScopeContext
}

// NewAPISurfaceDetector creates a new structural API surface detector.
// projectModule is the Go module path used to distinguish internal vs external types.
func NewAPISurfaceDetector(client *neo4j.Client, projectModule string) *APISurfaceDetector {
	return &APISurfaceDetector{
		client:        client,
		projectModule: projectModule,
		scopeCtx:      models.DefaultScope(),
	}
}

// SetScope sets the scope context for the detector.
func (d *APISurfaceDetector) SetScope(scope models.ScopeContext) {
	d.scopeCtx = scope
}

// Detect runs the structural detection strategies and tags API surface functions.
func (d *APISurfaceDetector) Detect(ctx context.Context) error {
	fmt.Println("Running structural API surface detection...")

	// Strategy 1: Mark functions with external parameter types.
	extCount, err := d.detectExternalParamFunctions(ctx)
	if err != nil {
		fmt.Printf("Warning: external-param detection failed: %v\n", err)
	}

	// Strategy 2: Mark cross-package call targets.
	crossPkgCount, err := d.detectCrossPackageTargets(ctx)
	if err != nil {
		fmt.Printf("Warning: cross-package detection failed: %v\n", err)
	}

	// Strategy 3: Stamp RPC handlers (isRPCHandler) from the proto-generated
	// method shape, and link ProtoMethod → handler (IMPLEMENTED_BY) when proto
	// contracts are indexed. Without this, isRPCHandler is never populated and
	// every navigation query that pivots on it (api_callers fold-up, flow spine,
	// handler ranking, summaries) silently degrades.
	handlerCount, err := d.detectRPCHandlers(ctx)
	if err != nil {
		fmt.Printf("Warning: RPC-handler detection failed: %v\n", err)
	}
	implCount, err := d.linkProtoImplementations(ctx)
	if err != nil {
		fmt.Printf("Warning: proto IMPLEMENTED_BY linking failed: %v\n", err)
	}

	// Strategy 4: Materialize handler → cross-service-call reachability edges.
	// Depends on Strategy 3 (isRPCHandler) having run above.
	reachCount, err := d.resolveCrossServiceReachability(ctx)
	if err != nil {
		fmt.Printf("Warning: cross-service reachability resolution failed: %v\n", err)
	}

	fmt.Printf("Structural API surface detection complete: %d external-param, %d cross-pkg, %d rpc-handlers, %d proto-links, %d reaches-call\n",
		extCount, crossPkgCount, handlerCount, implCount, reachCount)
	return nil
}

// maxReachDepth bounds the CALLS-chain closure when materializing REACHES_CALL
// edges. It is a performance safety valve, not a semantic limiter: the deepest
// handler→cross-service call chain measured across the indexed fleet is 12 hops
// (e.g. onboarding UpdateMerchantBank → … → utils.GetBankFields → payoutrouter at
// 7), and extending the bound to 20 surfaces no additional edges — so 16 sits
// comfortably above the observed ceiling while keeping the closure bounded.
const maxReachDepth = 16

// maxAsyncEventHops bounds the fixpoint that propagates async-continuation edges
// across chained self-consumed events (producer → event → consumer → event →
// consumer → …). Observed real chains are 2 hops (create_payout: payout.approved
// → handlePayoutApproved → payout.auto_initiate → orchestration → PSP), so 5 is
// comfortable headroom; the loop also breaks early once an iteration adds nothing.
const maxAsyncEventHops = 5

// resolveCrossServiceReachability precomputes REACHES_CALL edges from every ENTRY
// POINT — synchronous RPC handlers AND asynchronous event consumers — to each
// resolved cross-service call it reaches. Without it, a cross-service call buried
// deep behind ordinary business functions (or behind a self-consumed event) is
// invisible to handler-scoped tools unless they run an unbounded, noisy CALLS*
// walk that still cannot cross an event boundary. Precomputing the closure at
// index time keeps those tools thin (a single hop) and — crucially — lets them
// follow the sync→async→PSP style continuation that a pure CALLS walk cannot.
//
// Two passes, both scoped and provenance-tagged:
//
//   Pass A (synchronous closure). For every entry point — isRPCHandler OR an async
//   consumer (a function that listens on a queue or handles specific events) —
//   MERGE (entry)-[:REACHES_CALL {async:false}]->(call) for every cross-service
//   call reachable via CALLS within maxReachDepth. Seeding async consumers here is
//   what gives the async downstream (e.g. InitiatePayoutOrchestration → payout-
//   router / SOL) a closure of its own; previously only isRPCHandler seeded, so
//   anything behind an event was unreachable.
//
//   Pass B (asynchronous continuation, iterated to a fixpoint). For every entry
//   point H that emits a SELF-consumed event E (destService == callerService), find
//   the event's consumer C and copy C's reachability onto H as
//   (H)-[:REACHES_CALL {async:true, viaEvent:E, viaConsumer:C}]->(call). Because C's
//   set already includes async edges from earlier iterations, chained event hops
//   propagate (payout.approved → handlePayoutApproved → payout.auto_initiate → …).
//   Only self-consumed events are followed, which bounds the closure to the flow's
//   own service and never bleeds into unrelated consumers of a shared bus.
//
// Guards throughout: targets must have a resolved targetService differing from the
// caller's own; async is part of the edge identity so sync and async edges to the
// same call coexist without clobbering each other.
func (d *APISurfaceDetector) resolveCrossServiceReachability(ctx context.Context) (int, error) {
	// entryPredicate matches both sync handlers and async consumers, bound to the
	// entry variable `h` used by both passes. Async-role properties (listensOnQueue,
	// handlesEvents*, all set by the event detector during this same index run,
	// before this pass) identify event consumers without depending on the
	// cross-service ROUTED_TO edges, which are written later by a global resolver.
	const entryPredicate = `(
		coalesce(h.isRPCHandler, false) = true
		OR h.listensOnQueue IS NOT NULL
		OR h.handlesEventsDirectly IS NOT NULL
		OR h.handlesEvents IS NOT NULL
	)`

	// --- Pass A: synchronous closures for every entry point. ---
	syncCypher := fmt.Sprintf(`
		MATCH (call)
		WHERE (call:GRPCCall OR call:HTTPCall)
		  AND coalesce(call.targetService, '') <> ''
		  AND toLower(call.targetService) <> toLower(coalesce(call.callerService, ''))
		MATCH (f)-[:CALLS_API]->(call)
		MATCH p = (h)-[:CALLS*0..%d]->(f)
		WHERE (h.scopeId = $scopeId OR h.scopeId = 'main')
		  AND %s
		WITH h, call, f, min(length(p)) AS hops
		MERGE (h)-[r:REACHES_CALL {async: false}]->(call)
		SET r.hops = hops, r.viaFunction = f.name
		RETURN count(r) AS cnt
	`, maxReachDepth, entryPredicate)

	total, err := d.runReachQuery(ctx, syncCypher)
	if err != nil {
		return 0, fmt.Errorf("cross-service reachability (sync): %w", err)
	}
	syncCount := total

	// --- Pass B: asynchronous continuation across self-consumed events. ---
	// Consumer selection prefers the precise per-event handler; when none resolved
	// (e.g. a dispatcher whose switch the event resolver could not enumerate), it
	// falls back to the queue's listener entry, which Pass A has given a closure via
	// the higher-order-dispatch CALLS edge (return <dispatcher>). Both are bounded
	// to self-consumed events.
	asyncCypher := fmt.Sprintf(`
		MATCH (h)-[:CALLS*0..%d]->()-[:CALLS_API]->(oc:OutboxCall)-[:EMITS_EVENT]->(et:EventType)
		WHERE (h.scopeId = $scopeId OR h.scopeId = 'main')
		  AND %s
		  AND coalesce(oc.destService, '') <> ''
		  AND toLower(oc.destService) = toLower(coalesce(oc.callerService, ''))
		// Consumer: precise per-event handler if any exists, else the queue entry.
		CALL {
			WITH oc, et
			OPTIONAL MATCH (ph)
			WHERE (ph.handlesEventsDirectly IS NOT NULL AND et.eventType IN ph.handlesEventsDirectly)
			   OR (ph.handlesEvents IS NOT NULL AND et.eventType IN ph.handlesEvents)
			WITH oc, collect(DISTINCT ph) AS precise
			OPTIONAL MATCH (qe) WHERE size(precise) = 0 AND qe.listensOnQueue = oc.destQueue
			// Aggregate qe first, THEN concatenate — combining a grouping key (precise)
			// with an aggregation (collect(qe)) in one expression is an illegal
			// implicit-grouping mix in Cypher.
			WITH precise, collect(DISTINCT qe) AS entries
			WITH precise + entries AS consumers
			RETURN consumers
		}
		UNWIND consumers AS c
		WITH h, et, c WHERE c IS NOT NULL AND c <> h
		MATCH (c)-[:REACHES_CALL]->(call)
		WHERE coalesce(call.targetService, '') <> ''
		  AND toLower(call.targetService) <> toLower(coalesce(call.callerService, ''))
		MERGE (h)-[ar:REACHES_CALL {async: true}]->(call)
		SET ar.viaEvent = et.eventType, ar.viaConsumer = c.name
		RETURN count(ar) AS cnt
	`, maxReachDepth, entryPredicate)

	// Iterate to a fixpoint: each round lets producers inherit the async edges that
	// the previous round added to intermediate consumers, so multi-hop event chains
	// resolve. Stop as soon as a round changes nothing.
	prevAsync := -1
	for i := range maxAsyncEventHops {
		if _, aerr := d.runReachQuery(ctx, asyncCypher); aerr != nil {
			return total, fmt.Errorf("cross-service reachability (async, iter %d): %w", i+1, aerr)
		}
		// Converge when the total distinct async-edge count stops growing (each round
		// lets producers inherit async edges the previous round added to consumers).
		distinct, derr := d.countAsyncReachEdges(ctx)
		if derr == nil {
			if distinct == prevAsync {
				break
			}
			prevAsync = distinct
		}
	}
	asyncTotal := max(prevAsync, 0)

	fmt.Printf("  Strategy 4: materialized %d REACHES_CALL edges (%d sync entry-point→call, %d async via self-consumed events)\n",
		syncCount+asyncTotal, syncCount, asyncTotal)
	return syncCount + asyncTotal, nil
}

// runReachQuery executes a REACHES_CALL MERGE query and returns the reported count.
func (d *APISurfaceDetector) runReachQuery(ctx context.Context, cypher string) (int, error) {
	records, err := d.client.ExecuteQuery(ctx, cypher, map[string]any{
		"scopeId": d.scopeCtx.ScopeID,
	})
	if err != nil {
		return 0, err
	}
	if len(records) > 0 {
		if v, ok := records[0].AsMap()["cnt"].(int64); ok {
			return int(v), nil
		}
	}
	return 0, nil
}

// countAsyncReachEdges returns the number of distinct async REACHES_CALL edges in
// scope, used to detect fixpoint convergence.
func (d *APISurfaceDetector) countAsyncReachEdges(ctx context.Context) (int, error) {
	records, err := d.client.ExecuteQuery(ctx, `
		MATCH (h)-[r:REACHES_CALL {async: true}]->()
		WHERE h.scopeId = $scopeId OR h.scopeId = 'main'
		RETURN count(r) AS cnt
	`, map[string]any{"scopeId": d.scopeCtx.ScopeID})
	if err != nil {
		return 0, err
	}
	if len(records) > 0 {
		if v, ok := records[0].AsMap()["cnt"].(int64); ok {
			return int(v), nil
		}
	}
	return 0, nil
}

// detectExternalParamFunctions marks exported functions whose paramTypes
// contain types from outside the project module.
func (d *APISurfaceDetector) detectExternalParamFunctions(ctx context.Context) (int, error) {
	if d.projectModule == "" {
		return 0, nil // Cannot distinguish internal vs external without module path.
	}

	// Find all exported functions with paramTypes, then filter in Cypher.
	// We mark external-param types on the node for downstream use.
	cypher := `
		MATCH (fn)
		WHERE (fn:Function OR fn:Method)
		  AND (fn.scopeId = $scopeId OR fn.scopeId = 'main')
		  AND fn.paramTypes IS NOT NULL
		  AND size(fn.paramTypes) > 0
		  AND coalesce(fn.isExported, false) = true
		  AND coalesce(fn.isTestFunction, false) = false
		WITH fn, [pt IN fn.paramTypes WHERE NOT pt STARTS WITH $projectModule
		          AND pt CONTAINS '.'] AS externalTypes
		WHERE size(externalTypes) > 0
		SET fn.hasExternalParams = true, fn.externalParamTypes = externalTypes
		RETURN count(fn) AS cnt
	`

	records, err := d.client.ExecuteQuery(ctx, cypher, map[string]any{
		"scopeId":       d.scopeCtx.ScopeID,
		"projectModule": d.projectModule,
	})
	if err != nil {
		return 0, fmt.Errorf("external-param detection: %w", err)
	}

	cnt := 0
	if len(records) > 0 {
		if v, ok := records[0].AsMap()["cnt"].(int64); ok {
			cnt = int(v)
		}
	}
	fmt.Printf("  Strategy 1: marked %d functions with external parameter types\n", cnt)
	return cnt, nil
}

// detectCrossPackageTargets marks exported functions that are called from
// different packages within the project.
func (d *APISurfaceDetector) detectCrossPackageTargets(ctx context.Context) (int, error) {
	// Use CALLS edges where caller and callee are in different directory paths
	// (proxy for different Go packages).
	cypher := `
		MATCH (caller)-[:CALLS]->(callee)
		WHERE (caller:Function OR caller:Method)
		  AND (callee:Function OR callee:Method)
		  AND (caller.scopeId = $scopeId OR caller.scopeId = 'main')
		  AND (callee.scopeId = $scopeId OR callee.scopeId = 'main')
		  AND coalesce(callee.isExported, false) = true
		  AND coalesce(callee.isTestFunction, false) = false
		  AND caller.filePath IS NOT NULL
		  AND callee.filePath IS NOT NULL
		WITH callee, caller,
		     replace(callee.filePath, '/' + last(split(callee.filePath, '/')), '') AS calleePkg,
		     replace(caller.filePath, '/' + last(split(caller.filePath, '/')), '') AS callerPkg
		WHERE callerPkg <> calleePkg
		WITH callee, count(DISTINCT caller) AS crossPkgCallers
		SET callee.isCrossPkgTarget = true, callee.crossPkgCallerCount = crossPkgCallers
		RETURN count(callee) AS cnt
	`

	records, err := d.client.ExecuteQuery(ctx, cypher, map[string]any{
		"scopeId": d.scopeCtx.ScopeID,
	})
	if err != nil {
		return 0, fmt.Errorf("cross-package detection: %w", err)
	}

	cnt := 0
	if len(records) > 0 {
		if v, ok := records[0].AsMap()["cnt"].(int64); ok {
			cnt = int(v)
		}
	}
	fmt.Printf("  Strategy 2: marked %d functions as cross-package call targets\n", cnt)
	return cnt, nil
}

// detectRPCHandlers stamps isRPCHandler=true on gRPC / HTTP-v3 request handlers
// using a purely structural signal — no framework catalog. Protobuf's Go code
// generator emits every unary RPC handler as a method on a `…Server` receiver
// with the fixed shape `func (s *XxxServiceServer) Method(ctx context.Context,
// req *XxxRequest) (*XxxResponse, error)`. We key off that contract:
//   - the node is a method (has a receiverType) ending in "Server"
//   - it is exported and non-test
//   - it takes exactly two params: a context first, a *…Request second
//
// returnType is not captured by the AST pass, so we deliberately do not gate on
// the *…Response return; the (Server receiver + ctx + *Request) triple is
// specific enough to avoid false positives from internal helpers, which take
// domain structs rather than proto request messages.
func (d *APISurfaceDetector) detectRPCHandlers(ctx context.Context) (int, error) {
	cypher := `
		MATCH (fn)
		WHERE (fn:Function OR fn:Method)
		  AND (fn.scopeId = $scopeId OR fn.scopeId = 'main')
		  AND coalesce(fn.isExported, false) = true
		  AND coalesce(fn.isTestFunction, false) = false
		  AND fn.receiverType IS NOT NULL
		  AND fn.receiverType ENDS WITH 'Server'
		  AND fn.paramTypes IS NOT NULL
		  AND size(fn.paramTypes) = 2
		  AND toLower(fn.paramTypes[0]) CONTAINS 'context'
		  AND fn.paramTypes[1] ENDS WITH 'Request'
		SET fn.isRPCHandler = true
		RETURN count(fn) AS cnt
	`

	records, err := d.client.ExecuteQuery(ctx, cypher, map[string]any{
		"scopeId": d.scopeCtx.ScopeID,
	})
	if err != nil {
		return 0, fmt.Errorf("rpc-handler detection: %w", err)
	}

	cnt := 0
	if len(records) > 0 {
		if v, ok := records[0].AsMap()["cnt"].(int64); ok {
			cnt = int(v)
		}
	}
	fmt.Printf("  Strategy 3a: stamped %d functions as RPC handlers\n", cnt)
	return cnt, nil
}

// linkProtoImplementations connects each ProtoMethod to the concrete handler that
// implements it (ProtoMethod)-[:IMPLEMENTED_BY]->(handler), and stamps the handler
// as isRPCHandler as an authoritative side effect. This is the precise complement
// to the structural pass above: when the proto contract repo is indexed, the RPC
// name in the proto service is the ground truth for "this is a handler".
//
// It is a no-op when no ProtoMethod nodes exist (proto not yet indexed), so it is
// safe to always run. Matching is by the bare RPC name (the node name carries a
// receiver prefix "Recv.Method" and a SCIP "()." suffix, both stripped here),
// constrained to "…Server" receivers so a same-named caller/helper is never
// mistaken for the handler. ProtoMethod nodes are not scope-filtered because proto
// contracts are cross-service rendezvous nodes (indexed under their own service).
func (d *APISurfaceDetector) linkProtoImplementations(ctx context.Context) (int, error) {
	cypher := `
		MATCH (pm:ProtoMethod)
		MATCH (fn)
		WHERE (fn:Function OR fn:Method)
		  AND (fn.scopeId = $scopeId OR fn.scopeId = 'main')
		  AND coalesce(fn.isExported, false) = true
		  AND coalesce(fn.isTestFunction, false) = false
		  AND fn.receiverType IS NOT NULL
		  AND fn.receiverType ENDS WITH 'Server'
		  AND last(split(fn.name, '.')) = pm.name
		MERGE (pm)-[:IMPLEMENTED_BY]->(fn)
		SET fn.isRPCHandler = true
		RETURN count(DISTINCT fn) AS cnt
	`

	records, err := d.client.ExecuteQuery(ctx, cypher, map[string]any{
		"scopeId": d.scopeCtx.ScopeID,
	})
	if err != nil {
		return 0, fmt.Errorf("proto IMPLEMENTED_BY linking: %w", err)
	}

	cnt := 0
	if len(records) > 0 {
		if v, ok := records[0].AsMap()["cnt"].(int64); ok {
			cnt = int(v)
		}
	}
	fmt.Printf("  Strategy 3b: linked %d handlers to proto methods (IMPLEMENTED_BY)\n", cnt)
	return cnt, nil
}

