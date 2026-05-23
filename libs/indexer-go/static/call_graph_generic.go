package static

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	models "github.com/context-maximiser/code-graph/libs/core-models-go"
	"github.com/context-maximiser/code-graph/libs/neo4j-go"
)

// GenericCallGraphBuilder infers CALLS relationships for non-Go languages
// by using SCIP Reference → Symbol → Function/Method chains.
//
// Unlike SCIPCallGraphBuilder (which relies on Go AST for function body ranges),
// this builder computes body ranges from the ordered sequence of Function/Method
// declarations within each file: each function's body extends from its declaration
// line to the line before the next function declaration (or EOF).
type GenericCallGraphBuilder struct {
	client      *neo4j.Client
	serviceName string
	packageName string // NPM package name or module path used to filter targets
	scopeCtx    models.ScopeContext
}

// NewGenericCallGraphBuilder creates a new language-agnostic call graph builder.
func NewGenericCallGraphBuilder(client *neo4j.Client) *GenericCallGraphBuilder {
	return &GenericCallGraphBuilder{
		client:   client,
		scopeCtx: models.DefaultScope(),
	}
}

// SetServiceName restricts the builder to files owned by the given Service.
func (cg *GenericCallGraphBuilder) SetServiceName(name string) {
	cg.serviceName = name
}

// SetPackageName sets the package/module identifier used to filter call targets
// to only intra-project symbols (e.g., NPM package name "backend").
func (cg *GenericCallGraphBuilder) SetPackageName(name string) {
	cg.packageName = name
}

// SetScope sets the scope context for the builder.
func (cg *GenericCallGraphBuilder) SetScope(scope models.ScopeContext) {
	cg.scopeCtx = scope
}

// genericFuncInfo holds a function's graph ID and line range.
type genericFuncInfo struct {
	ID        string
	StartLine int
	EndLine   int
}

type degreeAccumulator struct {
	in   map[string]int
	out  map[string]int
	seen map[string]bool
}

func newDegreeAccumulator() *degreeAccumulator {
	return &degreeAccumulator{
		in:   make(map[string]int),
		out:  make(map[string]int),
		seen: make(map[string]bool),
	}
}

func (d *degreeAccumulator) add(fromID, toID string) {
	if d == nil || fromID == "" || toID == "" {
		return
	}
	key := fromID + "->" + toID
	if d.seen[key] {
		return
	}
	d.seen[key] = true
	d.out[fromID]++
	d.in[toID]++
}

// BuildCallGraph infers CALLS relationships for all source files in the service.
func (cg *GenericCallGraphBuilder) BuildCallGraph(ctx context.Context) error {
	fmt.Println("Building call graph from SCIP references (language-agnostic)...")

	files, err := cg.listFiles(ctx)
	if err != nil {
		return fmt.Errorf("failed to list files: %w", err)
	}

	functionsByFile, err := cg.getServiceFunctionsByFile(ctx)
	if err != nil {
		return fmt.Errorf("failed to preload service functions: %w", err)
	}

	degrees := newDegreeAccumulator()
	totalCalls := 0
	for _, filePath := range files {
		n, err := cg.processFile(ctx, filePath, functionsByFile[filePath], degrees)
		if err != nil {
			fmt.Printf("Warning: call graph for %s: %v\n", filePath, err)
			continue
		}
		totalCalls += n
	}

	fmt.Printf("Call graph complete: created %d CALLS relationships across %d files\n", totalCalls, len(files))

	// Write inDegree/outDegree from in-memory edge counts.
	if err := cg.writeDegreeProperties(ctx, functionsByFile, degrees); err != nil {
		fmt.Printf("Warning: degree computation failed: %v\n", err)
	}

	return nil
}

// listFiles returns file paths owned by the service.
func (cg *GenericCallGraphBuilder) listFiles(ctx context.Context) ([]string, error) {
	query := `
		MATCH (s:Service {name: $serviceName})-[:CONTAINS]->(f:File)
		WHERE f.scopeId = $scopeId
		RETURN f.path AS path
	`
	results, err := cg.client.ExecuteQuery(ctx, query, map[string]any{
		"scopeId":     cg.scopeCtx.ScopeID,
		"serviceName": cg.serviceName,
	})
	if err != nil {
		return nil, err
	}

	paths := make([]string, 0, len(results))
	for _, rec := range results {
		p := getStringFromMap(rec.AsMap(), "path")
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths, nil
}

// processFile computes function body ranges from declaration order,
// then maps references to enclosing callers and creates CALLS edges.
func (cg *GenericCallGraphBuilder) processFile(ctx context.Context, filePath string, funcs []genericFuncInfo, degrees *degreeAccumulator) (int, error) {
	if isNoisyFilePath(filePath) {
		return 0, nil
	}
	// Step 1: Use preloaded Function/Method nodes for this file.
	if len(funcs) == 0 {
		return 0, nil
	}

	// Step 2: Compute body ranges from declaration order.
	// Sort by startLine, then set each function's endLine to (next function's startLine - 1).
	sort.Slice(funcs, func(i, j int) bool {
		return funcs[i].StartLine < funcs[j].StartLine
	})
	for i := range funcs {
		if i+1 < len(funcs) {
			funcs[i].EndLine = funcs[i+1].StartLine - 1
		} else {
			// Last function: extend to a large line number (EOF proxy).
			funcs[i].EndLine = funcs[i].StartLine + 10000
		}
		// Ensure at least a 1-line range.
		if funcs[i].EndLine <= funcs[i].StartLine {
			funcs[i].EndLine = funcs[i].StartLine + 1
		}
	}

	// Step 3: Update the endLine in Neo4j so downstream tools have body ranges.
	if err := cg.updateBodyRanges(ctx, funcs); err != nil {
		fmt.Printf("Warning: failed to update body ranges for %s: %v\n", filePath, err)
	}

	// Step 4: Query references in this file that point to project-internal symbols.
	refs, err := cg.getReferencesInFile(ctx, filePath)
	if err != nil {
		return 0, err
	}

	// Step 5: Map references to enclosing callers and create CALLS edges.
	created := 0
	seen := map[string]bool{}

	for _, ref := range refs {
		caller := findEnclosingGenericFunc(funcs, ref.line)
		if caller == nil {
			continue
		}

		pairKey := caller.ID + "->" + ref.targetID
		if caller.ID == ref.targetID || seen[pairKey] {
			continue
		}
		seen[pairKey] = true

		_, err := cg.client.MergeRelationship(ctx, caller.ID, ref.targetID,
			string(models.CallsRel), nil,
			map[string]any{"line": ref.line, "filePath": filePath})
		if err != nil {
			continue
		}
		degrees.add(caller.ID, ref.targetID)
		created++
	}

	// Step 6: Cross-service pass — find references that resolve to functions owned by a
	// different indexed Service. These are cross-service calls; write GRPCCall/HTTPCall
	// nodes instead of CALLS edges so the service boundary is explicit in the graph.
	crossRefs, err := cg.getCrossServiceRefsInFile(ctx, filePath)
	if err != nil {
		fmt.Printf("Warning: cross-service ref query for %s: %v\n", filePath, err)
	} else {
		seenCross := map[string]bool{}
		for _, ref := range crossRefs {
			caller := findEnclosingGenericFunc(funcs, ref.line)
			if caller == nil {
				continue
			}
			key := fmt.Sprintf("%s:%s:%d", caller.ID, ref.symbol, ref.line)
			if seenCross[key] {
				continue
			}
			seenCross[key] = true
			if err := cg.writeCrossServiceCall(ctx, caller.ID, ref, filePath); err != nil {
				fmt.Printf("Warning: cross-service call node for %s: %v\n", filePath, err)
			}
		}
	}

	return created, nil
}

// getServiceFunctionsByFile loads all Function/Method nodes for the service in one query.
func (cg *GenericCallGraphBuilder) getServiceFunctionsByFile(ctx context.Context) (map[string][]genericFuncInfo, error) {
	query := `
		MATCH (svc:Service {name: $serviceName, scopeId: $scopeId})-[:CONTAINS*1..3]->(fn)
		WHERE (fn:Function OR fn:Method)
		  AND fn.startLine IS NOT NULL
		MATCH (fn)<-[:CONTAINS]-(file:File)
		WHERE file.scopeId = $scopeId
		RETURN file.path AS filePath,
		       elementId(fn) AS id,
		       fn.startLine AS startLine
	`
	results, err := cg.client.ExecuteQuery(ctx, query, map[string]any{
		"serviceName": cg.serviceName,
		"scopeId":     cg.scopeCtx.ScopeID,
	})
	if err != nil {
		return nil, err
	}

	rows := make([]map[string]any, 0, len(results))
	for _, rec := range results {
		rows = append(rows, rec.AsMap())
	}
	return groupFunctionsByFile(rows), nil
}

// groupFunctionsByFile transforms flat query rows to filePath -> function list.
func groupFunctionsByFile(rows []map[string]any) map[string][]genericFuncInfo {
	out := make(map[string][]genericFuncInfo)
	for _, rm := range rows {
		filePath := getStringFromMap(rm, "filePath")
		id := getStringFromMap(rm, "id")
		startLine := int(getInt64FromMap(rm, "startLine"))
		if filePath == "" || id == "" || startLine <= 0 {
			continue
		}
		out[filePath] = append(out[filePath], genericFuncInfo{
			ID:        id,
			StartLine: startLine,
			EndLine:   startLine,
		})
	}
	return out
}

// refInfo holds a reference's line and the target function's element ID.
type refInfo struct {
	line     int
	targetID string
}

// getReferencesInFile queries Reference nodes in this file that point to
// Symbols defined by Function/Method nodes within the same project.
// When IMPLEMENTS edges exist (from Phase 1 relationship ingestion),
// the query follows them to resolve polymorphic calls to concrete
// implementations instead of stopping at the interface method.
func (cg *GenericCallGraphBuilder) getReferencesInFile(ctx context.Context, filePath string) ([]refInfo, error) {
	// Filter targets to project-internal symbols using the package name.
	// CONTAINS "" is always true, so empty packageName disables filtering.
	//
	// IMPLEMENTS traversal: if the direct target has incoming IMPLEMENTS
	// edges from concrete types, return those instead (may-call fan-out).
	// Otherwise fall back to the direct target.
	query := `
		MATCH (ref:Reference {filePath: $filePath, scopeId: $scopeId})
		      -[:REFERENCES]->(sym:Symbol)
		      <-[:DEFINES]-(directTarget)
		WHERE (directTarget:Function OR directTarget:Method)
		  AND directTarget.signature CONTAINS $packageName
		OPTIONAL MATCH (concreteTarget)-[:IMPLEMENTS]->(directTarget)
		WHERE (concreteTarget:Function OR concreteTarget:Method)
		  AND concreteTarget.signature CONTAINS $packageName
		WITH ref, directTarget,
		     COLLECT(DISTINCT concreteTarget) AS concretes
		UNWIND
		  CASE WHEN SIZE(concretes) > 0 THEN concretes
		       ELSE [directTarget]
		  END AS target
		RETURN ref.startLine AS refLine,
		       elementId(target) AS targetId
	`
	results, err := cg.client.ExecuteQuery(ctx, query, map[string]any{
		"filePath":    filePath,
		"scopeId":     cg.scopeCtx.ScopeID,
		"packageName": cg.packageName,
	})
	if err != nil {
		return nil, err
	}

	refs := make([]refInfo, 0, len(results))
	for _, rec := range results {
		rm := rec.AsMap()
		line := int(getInt64FromMap(rm, "refLine"))
		targetID := getStringFromMap(rm, "targetId")
		if line > 0 && targetID != "" {
			refs = append(refs, refInfo{line: line, targetID: targetID})
		}
	}
	return refs, nil
}

// crossServiceRefInfo holds a cross-service reference with target service identity.
type crossServiceRefInfo struct {
	line            int
	symbol          string
	displayName     string
	targetService   string
	targetServiceID string
}

// getCrossServiceRefsInFile queries references in this file where the resolved Symbol
// is defined by a Function/Method that belongs to a DIFFERENT indexed Service.
// This avoids the third-party noise problem: only symbols owned by an already-indexed
// Service node are returned, so stdlib and vendor calls are silently ignored.
func (cg *GenericCallGraphBuilder) getCrossServiceRefsInFile(ctx context.Context, filePath string) ([]crossServiceRefInfo, error) {
	// The `NOT signature CONTAINS $packageName` guard excludes intra-service symbols so
	// there is no overlap with the existing CALLS edge pass.
	query := `
		MATCH (ref:Reference {filePath: $filePath, scopeId: $scopeId})
		      -[:REFERENCES]->(sym:Symbol)
		      <-[:DEFINES]-(target)
		WHERE (target:Function OR target:Method)
		  AND NOT target.signature CONTAINS $packageName
		MATCH (targetFile:File)-[:CONTAINS]->(target)
		MATCH (targetSvc:Service)-[:CONTAINS]->(targetFile)
		WHERE targetSvc.name <> $serviceName
		RETURN ref.startLine AS refLine,
		       sym.symbol AS symbol,
		       coalesce(sym.displayName, '') AS displayName,
		       targetSvc.name AS targetService,
		       elementId(targetSvc) AS targetServiceId
		LIMIT 500
	`
	results, err := cg.client.ExecuteQuery(ctx, query, map[string]any{
		"filePath":    filePath,
		"scopeId":     cg.scopeCtx.ScopeID,
		"packageName": cg.packageName,
		"serviceName": cg.serviceName,
	})
	if err != nil {
		return nil, err
	}

	refs := make([]crossServiceRefInfo, 0, len(results))
	for _, rec := range results {
		rm := rec.AsMap()
		line := int(getInt64FromMap(rm, "refLine"))
		sym := getStringFromMap(rm, "symbol")
		dn := getStringFromMap(rm, "displayName")
		svcName := getStringFromMap(rm, "targetService")
		svcID := getStringFromMap(rm, "targetServiceId")
		if line > 0 && sym != "" && svcName != "" {
			refs = append(refs, crossServiceRefInfo{
				line:            line,
				symbol:          sym,
				displayName:     dn,
				targetService:   svcName,
				targetServiceID: svcID,
			})
		}
	}
	return refs, nil
}

// writeCrossServiceCall writes a GRPCCall or HTTPCall node for a detected cross-service
// reference, plus CALLS_API and CALLS_SERVICE edges.
func (cg *GenericCallGraphBuilder) writeCrossServiceCall(
	ctx context.Context,
	callerID string,
	ref crossServiceRefInfo,
	filePath string,
) error {
	if isGRPCLikeSymbol(ref.symbol) {
		return cg.writeGenericGRPCCall(ctx, callerID, ref, filePath)
	}
	return cg.writeGenericHTTPCall(ctx, callerID, ref, filePath)
}

// writeGenericGRPCCall creates a GRPCCall node and edges for a cross-service gRPC reference.
func (cg *GenericCallGraphBuilder) writeGenericGRPCCall(
	ctx context.Context,
	callerID string,
	ref crossServiceRefInfo,
	filePath string,
) error {
	targetSvcName, methodName := parseGRPCSymbol(ref.symbol, ref.displayName)
	if targetSvcName == "" {
		targetSvcName = ref.targetService
	}

	nodeKey := fmt.Sprintf("grpccall:generic:%s:%s:%s.%s:%d",
		cg.serviceName, filePath, targetSvcName, methodName, ref.line)

	mergeProps := map[string]any{"nodeKey": nodeKey}
	setProps := map[string]any{
		"nodeKey":       nodeKey,
		"callerService": cg.serviceName,
		"targetService": ref.targetService,
		"targetMethod":  targetSvcName + "." + methodName,
		"protoPackage":  extractProtoPackage(ref.symbol),
		"filePath":      filePath,
		"line":          ref.line,
		"createdAt":     currentUnixTime(),
		"updatedAt":     currentUnixTime(),
	}
	for k, v := range cg.scopeCtx.Props() {
		setProps[k] = v
	}

	grpcCallID, err := cg.client.MergeNode(ctx, []string{"GRPCCall"}, mergeProps, setProps)
	if err != nil {
		return fmt.Errorf("merge GRPCCall: %w", err)
	}

	if _, err := cg.client.MergeRelationship(ctx, callerID, grpcCallID,
		string(models.CallsAPIRel), map[string]any{}, map[string]any{"line": ref.line},
	); err != nil {
		fmt.Printf("Warning: generic gRPC CALLS_API: %v\n", err)
	}

	if ref.targetServiceID != "" {
		if _, err := cg.client.MergeRelationship(ctx, grpcCallID, ref.targetServiceID,
			string(models.CallsServiceRel), map[string]any{}, map[string]any{"protocol": "grpc"},
		); err != nil {
			fmt.Printf("Warning: generic gRPC CALLS_SERVICE: %v\n", err)
		}
	}

	return nil
}

// writeGenericHTTPCall creates an HTTPCall node and edges for a cross-service HTTP reference.
func (cg *GenericCallGraphBuilder) writeGenericHTTPCall(
	ctx context.Context,
	callerID string,
	ref crossServiceRefInfo,
	filePath string,
) error {
	method := inferHTTPMethodFromSymbol(ref.symbol, ref.displayName)

	nodeKey := fmt.Sprintf("httpcall:generic:%s:%s:%s:%d",
		cg.serviceName, filePath, method, ref.line)

	mergeProps := map[string]any{"nodeKey": nodeKey}
	setProps := map[string]any{
		"nodeKey":       nodeKey,
		"callerService": cg.serviceName,
		"targetService": ref.targetService,
		"url":           "dynamic",
		"method":        method,
		"filePath":      filePath,
		"line":          ref.line,
		"createdAt":     currentUnixTime(),
		"updatedAt":     currentUnixTime(),
	}
	for k, v := range cg.scopeCtx.Props() {
		setProps[k] = v
	}

	httpCallID, err := cg.client.MergeNode(ctx, []string{"HTTPCall"}, mergeProps, setProps)
	if err != nil {
		return fmt.Errorf("merge HTTPCall: %w", err)
	}

	if _, err := cg.client.MergeRelationship(ctx, callerID, httpCallID,
		string(models.CallsAPIRel), map[string]any{}, map[string]any{"line": ref.line},
	); err != nil {
		fmt.Printf("Warning: generic HTTP CALLS_API: %v\n", err)
	}

	if ref.targetServiceID != "" {
		if _, err := cg.client.MergeRelationship(ctx, httpCallID, ref.targetServiceID,
			string(models.CallsServiceRel), map[string]any{}, map[string]any{"protocol": "http"},
		); err != nil {
			fmt.Printf("Warning: generic HTTP CALLS_SERVICE: %v\n", err)
		}
	}

	return nil
}

// isGRPCLikeSymbol returns true when a SCIP symbol FQN suggests a gRPC call site.
func isGRPCLikeSymbol(symbol string) bool {
	lsym := strings.ToLower(symbol)
	for _, indicator := range []string{"/grpc/", "/grpcv1", "/pb/", "/proto/", "_pb2_grpc", "@grpc/grpc-js"} {
		if strings.Contains(lsym, indicator) {
			return true
		}
	}
	return false
}

// currentUnixTime returns the current UTC Unix timestamp as int64.
func currentUnixTime() int64 {
	return time.Now().UTC().Unix()
}

// updateBodyRanges writes computed endLine values back to Neo4j.
func (cg *GenericCallGraphBuilder) updateBodyRanges(ctx context.Context, funcs []genericFuncInfo) error {
	updates := make([]map[string]any, len(funcs))
	for i, f := range funcs {
		updates[i] = map[string]any{
			"id":      f.ID,
			"endLine": f.EndLine,
		}
	}
	cypher := `
		UNWIND $updates AS u
		MATCH (fn) WHERE elementId(fn) = u.id
		SET fn.endLine = u.endLine
	`
	_, err := cg.client.ExecuteQuery(ctx, cypher, map[string]any{"updates": updates})
	return err
}

// writeDegreeProperties sets inDegree/outDegree using already-built CALLS pairs.
func (cg *GenericCallGraphBuilder) writeDegreeProperties(
	ctx context.Context,
	functionsByFile map[string][]genericFuncInfo,
	degrees *degreeAccumulator,
) error {
	updates := make([]map[string]any, 0, len(functionsByFile)*2)
	for _, funcs := range functionsByFile {
		for _, fn := range funcs {
			updates = append(updates, map[string]any{
				"id":        fn.ID,
				"inDegree":  degrees.in[fn.ID],
				"outDegree": degrees.out[fn.ID],
			})
		}
	}
	cypher := `
		UNWIND $updates AS u
		MATCH (fn) WHERE elementId(fn) = u.id
		SET fn.inDegree = u.inDegree, fn.outDegree = u.outDegree
	`
	_, err := cg.client.ExecuteQuery(ctx, cypher, map[string]any{"updates": updates})
	if err != nil {
		return fmt.Errorf("degree computation: %w", err)
	}
	fmt.Println("Computed inDegree/outDegree for all Function/Method nodes")
	return nil
}

// findEnclosingGenericFunc finds the innermost function whose computed body
// range contains the given line number.
func findEnclosingGenericFunc(funcs []genericFuncInfo, line int) *genericFuncInfo {
	var best *genericFuncInfo
	bestSpan := int(^uint(0) >> 1)

	for i := range funcs {
		f := &funcs[i]
		if line >= f.StartLine && line <= f.EndLine {
			span := f.EndLine - f.StartLine
			if span < bestSpan {
				best = f
				bestSpan = span
			}
		}
	}
	return best
}
