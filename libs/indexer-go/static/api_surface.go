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

	fmt.Printf("Structural API surface detection complete: %d external-param, %d cross-pkg\n",
		extCount, crossPkgCount)
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

