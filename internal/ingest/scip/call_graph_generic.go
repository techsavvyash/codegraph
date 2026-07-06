package static

import (
	"context"
	"fmt"
	"sort"

	models "github.com/context-maximiser/code-graph/internal/model"
	"github.com/context-maximiser/code-graph/internal/graph"
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

// BuildCallGraph infers CALLS relationships for all source files in the service.
func (cg *GenericCallGraphBuilder) BuildCallGraph(ctx context.Context) error {
	fmt.Println("Building call graph from SCIP references (language-agnostic)...")

	files, err := cg.listFiles(ctx)
	if err != nil {
		return fmt.Errorf("failed to list files: %w", err)
	}

	totalCalls := 0
	for _, filePath := range files {
		n, err := cg.processFile(ctx, filePath)
		if err != nil {
			fmt.Printf("Warning: call graph for %s: %v\n", filePath, err)
			continue
		}
		totalCalls += n
	}

	fmt.Printf("Call graph complete: created %d CALLS relationships across %d files\n", totalCalls, len(files))

	// Compute degree properties.
	if err := cg.computeDegreeProperties(ctx); err != nil {
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
func (cg *GenericCallGraphBuilder) processFile(ctx context.Context, filePath string) (int, error) {
	// Step 1: Get all Function/Method nodes in this file with their declaration lines.
	funcs, err := cg.getFunctionsInFile(ctx, filePath)
	if err != nil || len(funcs) == 0 {
		return 0, err
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
	edges := resolveGenericCallEdges(refs, funcs)

	created := 0
	for _, e := range edges {
		_, err := cg.client.MergeRelationship(ctx, e.CallerID, e.TargetID,
			string(models.CallsRel), nil,
			map[string]any{
				"line":     e.Line,
				"filePath": filePath,
				"scope":    cg.scopeCtx.Scope,
				"scopeId":  cg.scopeCtx.ScopeID,
			})
		if err != nil {
			continue
		}
		created++
	}

	return created, nil
}

// genericCallEdge is a single resolved CALLS edge, one per distinct
// (caller, target) pair in a file. This builder tracks no branch metadata
// (unlike callEdge in call_graph_scip.go), so it's structurally identical to
// the shared minLineEdge — kept as its own name for readability at call
// sites in this file.
type genericCallEdge = minLineEdge

// resolveGenericCallEdges maps raw reference rows to their enclosing caller
// and collapses multiple call sites between the same (caller, target) pair
// into a single edge, deterministically keeping the smallest call-site line.
// Shares its dedup logic with the Go/SCIP builder's resolveCallEdges via
// collapseToMinLinePerPair — see that function's doc for why order-dependent
// "first wins" resolution is wrong here (this was the same nondeterministic
// CALLS.line bug found and fixed in call_graph_scip.go).
//
// Pure function, no I/O — testable without Neo4j.
func resolveGenericCallEdges(refs []refInfo, funcs []genericFuncInfo) []genericCallEdge {
	triples := make([]minLineEdge, 0, len(refs))
	for _, ref := range refs {
		caller := findEnclosingGenericFunc(funcs, ref.line)
		if caller == nil {
			continue
		}
		triples = append(triples, minLineEdge{CallerID: caller.ID, TargetID: ref.targetID, Line: ref.line})
	}
	return collapseToMinLinePerPair(triples)
}

// getFunctionsInFile returns all Function/Method nodes in a file with their IDs and declaration lines.
func (cg *GenericCallGraphBuilder) getFunctionsInFile(ctx context.Context, filePath string) ([]genericFuncInfo, error) {
	query := `
		MATCH (f:File {path: $filePath, scopeId: $scopeId})-[:CONTAINS]->(fn)
		WHERE (fn:Function OR fn:Method)
		  AND fn.startLine IS NOT NULL
		RETURN elementId(fn) AS id, fn.startLine AS startLine
		ORDER BY fn.startLine
	`
	results, err := cg.client.ExecuteQuery(ctx, query, map[string]any{
		"filePath": filePath,
		"scopeId":  cg.scopeCtx.ScopeID,
	})
	if err != nil {
		return nil, err
	}

	funcs := make([]genericFuncInfo, 0, len(results))
	for _, rec := range results {
		rm := rec.AsMap()
		id := getStringFromMap(rm, "id")
		startLine := int(getInt64FromMap(rm, "startLine"))
		if id != "" && startLine > 0 {
			funcs = append(funcs, genericFuncInfo{
				ID:        id,
				StartLine: startLine,
				EndLine:   startLine, // will be computed
			})
		}
	}
	return funcs, nil
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

// computeDegreeProperties sets inDegree and outDegree on Function/Method nodes.
func (cg *GenericCallGraphBuilder) computeDegreeProperties(ctx context.Context) error {
	// Scope to service functions only to avoid cross-service contamination.
	cypher := `
		MATCH (s:Service {name: $serviceName})-[:CONTAINS]->(:File)-[:CONTAINS]->(fn)
		WHERE (fn:Function OR fn:Method)
		  AND (fn.scopeId = $scopeId OR fn.scopeId = 'main')
		OPTIONAL MATCH (fn)<-[:CALLS]-(caller)
		WHERE (caller:Function OR caller:Method)
		  AND (caller.scopeId = $scopeId OR caller.scopeId = 'main')
		OPTIONAL MATCH (fn)-[:CALLS]->(callee)
		WHERE (callee:Function OR callee:Method)
		  AND (callee.scopeId = $scopeId OR callee.scopeId = 'main')
		WITH fn, count(DISTINCT caller) AS inD, count(DISTINCT callee) AS outD
		SET fn.inDegree = inD, fn.outDegree = outD
	`
	_, err := cg.client.ExecuteQuery(ctx, cypher, map[string]any{
		"scopeId":     cg.scopeCtx.ScopeID,
		"serviceName": cg.serviceName,
	})
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
