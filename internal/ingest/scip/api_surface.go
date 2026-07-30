package static

import (
	"context"
	"errors"
	"fmt"
	"strings"

	neo4j "github.com/context-maximiser/code-graph/internal/graph"
	models "github.com/context-maximiser/code-graph/internal/model"
)

// APISurfaceDetector identifies API surface using purely graph-structural
// signals — zero framework pattern catalogs. It reads parameter types,
// cross-package call edges, and export status to determine which functions
// constitute the project's API boundary.
type APISurfaceDetector struct {
	client        *neo4j.Client
	projectModule string // Go module path (e.g., "github.com/context-maximiser/code-graph")
	serviceName   string // service whose surface is being detected
	scopeCtx      models.ScopeContext
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

// SetServiceName binds detection to one service. Every strategy both reads
// and WRITES function properties (hasExternalParams, isCrossPkgTarget), so an
// unbounded run mutates other services' nodes and synthesizes APIRoutes for
// every service in the database each time any one of them is indexed.
func (d *APISurfaceDetector) SetServiceName(name string) {
	d.serviceName = name
}

// Detect runs all three structural detection strategies and creates APIRoute
// nodes + EXPOSES_API edges for detected API surface functions.
func (d *APISurfaceDetector) Detect(ctx context.Context) error {
	if d.serviceName == "" {
		return errors.New("api surface detection requires a service name — unbounded detection mutates every service in the database")
	}
	fmt.Println("Running structural API surface detection...")

	// The three strategies are independent: a failure in one must not stop
	// the others, but every failure must still surface — the joined error lets
	// the indexer record the phase as failed in its report.
	var errs []error

	// Strategy 1: Mark functions with external parameter types.
	extCount, err := d.detectExternalParamFunctions(ctx)
	if err != nil {
		errs = append(errs, fmt.Errorf("external-param detection: %w", err))
	}

	// Strategy 2: Mark cross-package call targets.
	crossPkgCount, err := d.detectCrossPackageTargets(ctx)
	if err != nil {
		errs = append(errs, fmt.Errorf("cross-package detection: %w", err))
	}

	// Strategy 3: Synthesize APIRoute nodes + EXPOSES_API edges.
	apiCount, err := d.synthesizeAPIRoutes(ctx)
	if err != nil {
		errs = append(errs, fmt.Errorf("API route synthesis: %w", err))
	}

	// Strategy 4: Decorator-routed handlers (NestJS and similar frameworks).
	// Additive — runs regardless of whether strategies 1-3 found anything,
	// since decorator syntax is a definitive framework signal on its own.
	decoratorCount, err := d.detectDecoratorRoutes(ctx)
	if err != nil {
		errs = append(errs, fmt.Errorf("decorator route detection: %w", err))
	}

	fmt.Printf("Structural API surface detection complete: %d external-param, %d cross-pkg, %d API routes, %d decorator routes\n",
		extCount, crossPkgCount, apiCount, decoratorCount)
	return errors.Join(errs...)
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
		  AND fn.serviceName = $serviceName
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
		"serviceName":   d.serviceName,
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
		  AND callee.serviceName = $serviceName
		  AND caller.serviceName = $serviceName
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
		"scopeId":     d.scopeCtx.ScopeID,
		"serviceName": d.serviceName,
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

// synthesizeAPIRoutes creates APIRoute nodes and EXPOSES_API edges for
// functions that match structural API surface criteria.
func (d *APISurfaceDetector) synthesizeAPIRoutes(ctx context.Context) (int, error) {
	// Find functions matching any structural signal.
	// Return enough info to create APIRoute nodes.
	cypher := `
		MATCH (fn)
		WHERE (fn:Function OR fn:Method)
		  AND fn.serviceName = $serviceName
		  AND (fn.scopeId = $scopeId OR fn.scopeId = 'main')
		  AND coalesce(fn.isExported, false) = true
		  AND coalesce(fn.isTestFunction, false) = false
		  AND (
		    coalesce(fn.hasExternalParams, false) = true
		    OR (coalesce(fn.isCrossPkgTarget, false) = true AND coalesce(fn.outDegree, 0) > 0)
		  )
		RETURN elementId(fn) AS fnId,
		       fn.name AS name,
		       fn.filePath AS filePath,
		       fn.nodeKey AS nodeKey,
		       coalesce(fn.hasExternalParams, false) AS hasExternal,
		       coalesce(fn.isCrossPkgTarget, false) AS isCrossPkg,
		       fn.externalParamTypes AS externalParamTypes
	`

	records, err := d.client.ExecuteQuery(ctx, cypher, map[string]any{
		"scopeId":     d.scopeCtx.ScopeID,
		"serviceName": d.serviceName,
	})
	if err != nil {
		return 0, fmt.Errorf("API route synthesis query: %w", err)
	}

	created := 0
	for _, rec := range records {
		rm := rec.AsMap()
		fnID := getStringFromMap(rm, "fnId")
		name := getStringFromMap(rm, "name")
		filePath := getStringFromMap(rm, "filePath")
		if fnID == "" || name == "" {
			continue
		}

		hasExternal, _ := rm["hasExternal"].(bool)
		isCrossPkg, _ := rm["isCrossPkg"].(bool)

		// Infer protocol from external param types.
		protocol := "structural"
		method := "ANY"
		if hasExternal {
			if extTypes, ok := rm["externalParamTypes"].([]any); ok {
				protocol, method = inferProtocolFromTypes(extTypes)
			}
		}

		// Determine detection source for metadata.
		detectionSource := "cross_pkg"
		if hasExternal && isCrossPkg {
			detectionSource = "external_params+cross_pkg"
		} else if hasExternal {
			detectionSource = "external_params"
		}

		// Build structural path: package/function name.
		routePath := buildStructuralPath(filePath, name)

		nodeKey := models.APIRouteNodeKey(method, routePath)
		routeProps := map[string]any{
			"path":            routePath,
			"method":          method,
			"nodeKey":         nodeKey,
			"protocol":        protocol,
			"framework":       "structural",
			"detectionSource": detectionSource,
			"scope":           d.scopeCtx.Scope,
			"scopeId":         d.scopeCtx.ScopeID,
		}

		routeID, err := d.client.MergeNode(ctx, []string{"APIRoute"},
			map[string]any{"nodeKey": nodeKey, "scopeId": d.scopeCtx.ScopeID}, routeProps)
		if err != nil {
			fmt.Printf("Warning: failed to create structural APIRoute for %s: %v\n", name, err)
			continue
		}

		_, err = d.client.MergeRelationship(ctx, fnID, routeID, string(models.ExposesAPIRel), nil, nil)
		if err != nil {
			fmt.Printf("Warning: failed to create EXPOSES_API for %s: %v\n", name, err)
			continue
		}
		created++
	}

	fmt.Printf("  Strategy 3: created %d APIRoute nodes with EXPOSES_API edges\n", created)
	return created, nil
}

// --- Strategy 4: decorator-routed handlers -------------------------------
//
// SCIP indexers don't emit call-reference edges for decorator-invoked
// handlers (the framework's runtime calls them via reflection, not a code
// reference), so strategies 1-3 — which all key off CALLS edges or
// parameter typing — structurally cannot see them. The RFC-010 tree-sitter
// structure pass now captures decorator annotations at parse time
// (internal/ingest/structure), and call_graph_generic.go stamps them onto
// Function/Method nodes as fn.decorators / fn.classDecorators. This
// strategy reads those properties back and turns them into APIRoute nodes,
// exactly like synthesizeAPIRoutes but keyed off an explicit decorator
// name table instead of graph-structural signals.
//
// decoratorHTTPMethods maps a NestJS HTTP method decorator to its uppercase
// HTTP verb; "All" maps to the wildcard method "*". One place, easy to
// extend for other frameworks (Angular route decorators, etc. are
// deliberately NOT included — NestJS-only for now, see package docs).
var decoratorHTTPMethods = map[string]string{
	"Get":     "GET",
	"Post":    "POST",
	"Put":     "PUT",
	"Patch":   "PATCH",
	"Delete":  "DELETE",
	"Options": "OPTIONS",
	"Head":    "HEAD",
	"All":     "*",
}

// decoratorMessagingNames are decorators (NestJS microservices) marking a
// handler as a message-broker consumer — mapped to fn.consumesBroker, the
// same property SemanticEdgeDetector.DetectMessageConsumers sets.
var decoratorMessagingNames = map[string]bool{
	"EventPattern":   true,
	"MessagePattern": true,
}

// decoratorSchedulingNames are decorators marking a handler as a scheduled
// task — mapped to fn.scheduledTask, the same property
// SemanticEdgeDetector.DetectScheduledFunctions sets.
var decoratorSchedulingNames = map[string]bool{
	"Cron":     true,
	"Interval": true,
	"Timeout":  true,
}

// decoratedName is one decoded decorator: its bare name and optional
// argument, from the "Name" / "Name:arg" encoding written by
// call_graph_generic.go's encodeDecorators.
type decoratedName struct {
	Name string
	Arg  string
}

// parseDecoratorStrings decodes the []any (Neo4j string-list property,
// surfaced through the driver as []any of strings) written by
// encodeDecorators back into structured name/arg pairs. Splits on the FIRST
// ':' only — an arg containing ':' round-trips intact but is indistinguishable
// from a name/arg split at that character; this is the same documented
// known limitation as encodeDecorators.
//
// Pure function, no I/O — testable without Neo4j.
func parseDecoratorStrings(raw []any) []decoratedName {
	out := make([]decoratedName, 0, len(raw))
	for _, v := range raw {
		s, ok := v.(string)
		if !ok || s == "" {
			continue
		}
		if idx := strings.Index(s, ":"); idx >= 0 {
			out = append(out, decoratedName{Name: s[:idx], Arg: s[idx+1:]})
		} else {
			out = append(out, decoratedName{Name: s})
		}
	}
	return out
}

// joinPaths builds a route path from a controller-level prefix and a
// method-level route argument, normalizing slashes so segments never
// produce a doubled or missing '/'. Always returns a path starting with '/'.
func joinPaths(prefix, methodArg string) string {
	prefix = strings.Trim(prefix, "/")
	methodArg = strings.Trim(methodArg, "/")
	switch {
	case prefix == "" && methodArg == "":
		return "/"
	case prefix == "":
		return "/" + methodArg
	case methodArg == "":
		return "/" + prefix
	default:
		return "/" + prefix + "/" + methodArg
	}
}

// detectDecoratorRoutes creates APIRoute nodes + EXPOSES_API edges for
// functions carrying an HTTP-verb decorator (NestJS @Get/@Post/etc.), and
// sets consumesBroker/scheduledTask booleans for messaging/scheduling
// decorators. A function can match more than one category at once (e.g. an
// @Get handler that is ALSO @Cron-scheduled) — categories are independent,
// not mutually exclusive.
func (d *APISurfaceDetector) detectDecoratorRoutes(ctx context.Context) (int, error) {
	cypher := `
		MATCH (fn)
		WHERE (fn:Function OR fn:Method)
		  AND fn.serviceName = $serviceName
		  AND (fn.scopeId = $scopeId OR fn.scopeId = 'main')
		  AND fn.decorators IS NOT NULL
		  AND size(fn.decorators) > 0
		RETURN elementId(fn) AS fnId,
		       fn.name AS name,
		       fn.decorators AS decorators,
		       fn.classDecorators AS classDecorators,
		       fn.nodeKey AS nodeKey
	`
	records, err := d.client.ExecuteQuery(ctx, cypher, map[string]any{
		"scopeId":     d.scopeCtx.ScopeID,
		"serviceName": d.serviceName,
	})
	if err != nil {
		return 0, fmt.Errorf("decorator route query: %w", err)
	}

	created := 0
	for _, rec := range records {
		rm := rec.AsMap()
		fnID := getStringFromMap(rm, "fnId")
		name := getStringFromMap(rm, "name")
		if fnID == "" || name == "" {
			continue
		}

		methodDecorators := parseDecoratorStrings(asAnySlice(rm["decorators"]))
		classDecorators := parseDecoratorStrings(asAnySlice(rm["classDecorators"]))

		// Messaging and scheduling: independent boolean flags, checked across
		// this function's own decorators (NestJS applies these directly to
		// the handler method, not the class).
		hasMessaging := false
		hasScheduling := false
		for _, dec := range methodDecorators {
			if decoratorMessagingNames[dec.Name] {
				hasMessaging = true
			}
			if decoratorSchedulingNames[dec.Name] {
				hasScheduling = true
			}
		}
		if hasMessaging {
			if err := d.setBoolProperty(ctx, fnID, "consumesBroker"); err != nil {
				fmt.Printf("Warning: failed to set consumesBroker for %s: %v\n", name, err)
			}
		}
		if hasScheduling {
			if err := d.setBoolProperty(ctx, fnID, "scheduledTask"); err != nil {
				fmt.Printf("Warning: failed to set scheduledTask for %s: %v\n", name, err)
			}
		}

		// HTTP route: only created when an HTTP-verb decorator is present.
		var httpMethod string
		var methodArg string
		for _, dec := range methodDecorators {
			if verb, ok := decoratorHTTPMethods[dec.Name]; ok {
				httpMethod = verb
				methodArg = dec.Arg
				break
			}
		}
		if httpMethod == "" {
			continue
		}

		controllerPrefix := ""
		for _, dec := range classDecorators {
			if dec.Name == "Controller" {
				controllerPrefix = dec.Arg
				break
			}
		}

		routePath := joinPaths(controllerPrefix, methodArg)
		nodeKey := models.APIRouteNodeKey(httpMethod, routePath)
		routeProps := map[string]any{
			"path":            routePath,
			"method":          httpMethod,
			"nodeKey":         nodeKey,
			"protocol":        "HTTP",
			"framework":       "nestjs",
			"detectionSource": "decorator",
			"scope":           d.scopeCtx.Scope,
			"scopeId":         d.scopeCtx.ScopeID,
		}

		routeID, err := d.client.MergeNode(ctx, []string{"APIRoute"},
			map[string]any{"nodeKey": nodeKey, "scopeId": d.scopeCtx.ScopeID}, routeProps)
		if err != nil {
			fmt.Printf("Warning: failed to create decorator APIRoute for %s: %v\n", name, err)
			continue
		}

		_, err = d.client.MergeRelationship(ctx, fnID, routeID, string(models.ExposesAPIRel), nil, nil)
		if err != nil {
			fmt.Printf("Warning: failed to create EXPOSES_API for %s: %v\n", name, err)
			continue
		}
		created++
	}

	fmt.Printf("  Strategy 4: created %d decorator-detected APIRoute nodes with EXPOSES_API edges\n", created)
	return created, nil
}

// setBoolProperty sets a single boolean property on the node identified by
// elementId, scoped to this detector's service — reusing the exact property
// names semantic_edges.go already established (consumesBroker,
// scheduledTask) rather than inventing new ones.
func (d *APISurfaceDetector) setBoolProperty(ctx context.Context, fnID, prop string) error {
	cypher := fmt.Sprintf(`
		MATCH (fn) WHERE elementId(fn) = $fnId
		SET fn.%s = true
	`, prop)
	_, err := d.client.ExecuteQuery(ctx, cypher, map[string]any{"fnId": fnID})
	return err
}

// asAnySlice normalizes a Neo4j list-property value (surfaced through the
// driver as []any, or nil/absent) to a non-nil []any so callers can range
// over it unconditionally.
func asAnySlice(v any) []any {
	if s, ok := v.([]any); ok {
		return s
	}
	return nil
}

// inferProtocolFromTypes examines external parameter types to infer the
// communication protocol. Returns (protocol, httpMethod).
func inferProtocolFromTypes(extTypes []any) (string, string) {
	for _, t := range extTypes {
		ts, ok := t.(string)
		if !ok {
			continue
		}
		ts = strings.TrimPrefix(ts, "*")

		switch {
		case strings.Contains(ts, "net/http"):
			return "HTTP", inferHTTPMethod(ts)
		case strings.Contains(ts, "grpc"):
			return "gRPC", "ANY"
		case strings.Contains(ts, "amqp") || strings.Contains(ts, "rabbitmq"):
			return "AMQP", "ANY"
		case strings.Contains(ts, "kafka"):
			return "Kafka", "ANY"
		case strings.Contains(ts, "nats"):
			return "NATS", "ANY"
		case strings.Contains(ts, "sql") || strings.Contains(ts, "database"):
			return "DB", "ANY"
		case strings.Contains(ts, "gin-gonic/gin"):
			return "HTTP", "ANY"
		case strings.Contains(ts, "labstack/echo"):
			return "HTTP", "ANY"
		case strings.Contains(ts, "go-chi/chi"):
			return "HTTP", "ANY"
		case strings.Contains(ts, "gorilla/mux"):
			return "HTTP", "ANY"
		case strings.Contains(ts, "gofiber/fiber"):
			return "HTTP", "ANY"
		}
	}
	return "structural", "ANY"
}

// inferHTTPMethod tries to infer an HTTP method from net/http type usage.
func inferHTTPMethod(typeName string) string {
	// net/http.ResponseWriter → likely a handler (could be any method)
	// net/http.Request → likely a handler
	return "ANY"
}

// buildStructuralPath constructs a structural route path from a file path and
// function name, using the Go package directory as prefix.
func buildStructuralPath(filePath, funcName string) string {
	if filePath == "" {
		return "/" + funcName
	}
	// Extract directory (package path) from file path.
	lastSlash := strings.LastIndex(filePath, "/")
	if lastSlash < 0 {
		return "/" + funcName
	}
	pkgPath := filePath[:lastSlash]
	return "/" + pkgPath + "/" + funcName
}
