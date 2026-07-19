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

	fmt.Printf("Structural API surface detection complete: %d external-param, %d cross-pkg, %d rpc-handlers, %d proto-links\n",
		extCount, crossPkgCount, handlerCount, implCount)
	return nil
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

