package static

import (
	"context"
	"fmt"
	"strings"

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

// Detect runs all three structural detection strategies and creates APIRoute
// nodes + EXPOSES_API edges for detected API surface functions.
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

	// Strategy 3: Synthesize APIRoute nodes + EXPOSES_API edges.
	apiCount, err := d.synthesizeAPIRoutes(ctx)
	if err != nil {
		fmt.Printf("Warning: API route synthesis failed: %v\n", err)
	}

	fmt.Printf("Structural API surface detection complete: %d external-param, %d cross-pkg, %d API routes\n",
		extCount, crossPkgCount, apiCount)
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

// synthesizeAPIRoutes creates APIRoute nodes and EXPOSES_API edges for
// functions that match structural API surface criteria.
func (d *APISurfaceDetector) synthesizeAPIRoutes(ctx context.Context) (int, error) {
	// Find functions matching any structural signal.
	// Return enough info to create APIRoute nodes.
	cypher := `
		MATCH (fn)
		WHERE (fn:Function OR fn:Method)
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
		"scopeId": d.scopeCtx.ScopeID,
	})
	if err != nil {
		return 0, fmt.Errorf("API route synthesis query: %w", err)
	}

	created := 0
	handlerIDs := make([]string, 0, len(records))
	for _, rec := range records {
		rm := rec.AsMap()
		fnID := getStringFromMap(rm, "fnId")
		name := getStringFromMap(rm, "name")
		filePath := getStringFromMap(rm, "filePath")
		if fnID == "" || name == "" {
			continue
		}
		handlerIDs = append(handlerIDs, fnID)

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

	// Batch-tag all detected entry-point handlers so tools can distinguish them
	// from internal helper functions without re-traversing the graph.
	if len(handlerIDs) > 0 {
		tagCypher := `
			UNWIND $fnIds AS id
			MATCH (fn) WHERE elementId(fn) = id
			SET fn.isRPCHandler = true
		`
		if _, tagErr := d.client.ExecuteQuery(ctx, tagCypher, map[string]any{"fnIds": handlerIDs}); tagErr != nil {
			fmt.Printf("Warning: failed to batch-tag isRPCHandler: %v\n", tagErr)
		} else {
			fmt.Printf("  Tagged %d functions as isRPCHandler\n", len(handlerIDs))
		}
	}

	fmt.Printf("  Strategy 3: created %d APIRoute nodes with EXPOSES_API edges\n", created)
	return created, nil
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
