package static

import (
	"bufio"
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	neo4j "github.com/context-maximiser/code-graph/internal/graph"
	models "github.com/context-maximiser/code-graph/internal/model"
)

// funcRange represents the line span of a function/method body in a Go file,
// plus the exact byte range of the whole declaration (used for source-code
// retrieval, as opposed to StartLine/EndLine which are used for line-based
// call-site containment).
type funcRange struct {
	Name         string
	DeclLine     int      // Line of the function name declaration (matches SCIP's startLine)
	StartLine    int      // Body opening brace line
	EndLine      int      // Body closing brace line
	StartByte    int      // Byte offset of the "func" keyword (or closure's FuncLit start)
	EndByte      int      // Byte offset just past the closing brace
	ParamTypes   []string // Fully-qualified parameter types (e.g., "net/http.ResponseWriter")
	ReceiverType string   // Receiver type for methods (e.g., "*SCIPIndexer")
	IsClosureVar bool     // true when this range comes from `var X = func(...){}` at package scope
}

// branchRange represents a conditional block (if/switch/select) span in a Go file.
type branchRange struct {
	StartLine int
	EndLine   int
	Depth     int // nesting level (1-based)
}

// SCIPCallGraphBuilder infers CALLS relationships between Functions/Methods
// by correlating SCIP Reference nodes with Go AST function body ranges.
type SCIPCallGraphBuilder struct {
	client      *neo4j.Client
	projectPath string
	modulePath  string // Go module path from go.mod, used to filter external targets
	serviceName string // Service node name used to restrict listGoFiles to this module only
	scopeCtx    models.ScopeContext
}

// NewSCIPCallGraphBuilder creates a new call graph builder.
func NewSCIPCallGraphBuilder(client *neo4j.Client, projectPath string) *SCIPCallGraphBuilder {
	return &SCIPCallGraphBuilder{
		client:      client,
		projectPath: projectPath,
		modulePath:  readModulePath(projectPath),
		scopeCtx:    models.DefaultScope(),
	}
}

// SetServiceName restricts the file list to only files owned by the named
// Service node. This prevents the builder from attempting to parse files
// from other modules that were indexed independently.
func (cg *SCIPCallGraphBuilder) SetServiceName(name string) {
	cg.serviceName = name
}

// readModulePath reads the module path from go.mod in the given directory.
// Returns empty string if go.mod is missing or malformed, which disables
// filtering (CONTAINS "" is always true).
func readModulePath(projectPath string) string {
	f, err := os.Open(filepath.Join(projectPath, "go.mod"))
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module"))
		}
	}
	return ""
}

// SetScope sets the scope context for the builder.
func (cg *SCIPCallGraphBuilder) SetScope(scope models.ScopeContext) {
	cg.scopeCtx = scope
}

// BuildCallGraph infers CALLS relationships for all Go source files in the graph.
func (cg *SCIPCallGraphBuilder) BuildCallGraph(ctx context.Context) error {
	if cg.serviceName == "" {
		return fmt.Errorf("SCIP call graph builder requires a service name: " +
			"file paths are relative to each service's root, so unbounded queries " +
			"attribute another service's same-named files to this one")
	}
	fmt.Println("Building call graph from SCIP references...")

	// Get all Go source files in the graph.
	files, err := cg.listGoFiles(ctx)
	if err != nil {
		return fmt.Errorf("failed to list Go files: %w", err)
	}

	totalCalls := 0
	for _, f := range files {
		n, err := cg.processFile(ctx, f.Path, f.ID)
		if err != nil {
			fmt.Printf("Warning: call graph for %s: %v\n", f.Path, err)
			continue
		}
		totalCalls += n
	}

	fmt.Printf("Call graph complete: created %d CALLS relationships across %d files\n", totalCalls, len(files))

	// Compute in/out degree properties on all Function/Method nodes in scope.
	if err := cg.ComputeDegreeProperties(ctx); err != nil {
		fmt.Printf("Warning: degree computation failed: %v\n", err)
	}

	return nil
}

// scipDegreeQuery builds the Cypher and bound parameters for
// ComputeDegreeProperties. Extracted as a pure function so tests can assert
// the SET target is constrained to a single service (via the bound
// $serviceName parameter) without a live Neo4j connection.
//
// inDegree excludes self-loops (caller = fn): now that
// collapseToMinLinePerPair keeps self-recursive CALLS edges (RFC-013),
// counting a function as its own caller would make every recursive
// function's inDegree >= 1 purely from calling itself, defeating every
// downstream "inDegree = 0 means no external caller" consumer (dead-code /
// entry-point / topological-root detection in
// internal/query/inference/graph_seeds.go and elsewhere) — a recursive
// function with no OTHER caller must still read as unreachable from
// outside.
//
// outDegree deliberately does NOT exclude self-loops: its purpose is "does
// this function have any real fan-out/behavior" (the topological-root
// filter's outDegree>0 check exists to exclude no-op stubs), and a
// self-call is real behavior — a purely self-recursive function with no
// external caller (e.g. a standalone factorial helper) should still read
// as "does something" and qualify as a topological root, not get filtered
// out as if it were a stub. Excluding self-loops from outDegree too would
// make such a function invisible to tier-3 seed detection entirely.
func scipDegreeQuery(serviceName string, scopeCtx models.ScopeContext) (string, map[string]any) {
	cypher := `
		MATCH (fn)
		WHERE (fn:Function OR fn:Method)
		  AND fn.serviceName = $serviceName
		  AND (fn.scopeId = $scopeId OR fn.scopeId = 'main')
		OPTIONAL MATCH (fn)<-[:CALLS]-(caller)
		WHERE (caller:Function OR caller:Method)
		  AND (caller.scopeId = $scopeId OR caller.scopeId = 'main')
		  AND caller <> fn
		OPTIONAL MATCH (fn)-[:CALLS]->(callee)
		WHERE (callee:Function OR callee:Method)
		  AND (callee.scopeId = $scopeId OR callee.scopeId = 'main')
		WITH fn, count(DISTINCT caller) AS inD, count(DISTINCT callee) AS outD
		SET fn.inDegree = inD, fn.outDegree = outD
	`
	params := map[string]any{
		"serviceName": serviceName,
		"scopeId":     scopeCtx.ScopeID,
	}
	return cypher, params
}

// ComputeDegreeProperties sets inDegree and outDegree properties on all
// Function/Method nodes in the current scope based on CALLS relationships.
func (cg *SCIPCallGraphBuilder) ComputeDegreeProperties(ctx context.Context) error {
	cypher, params := scipDegreeQuery(cg.serviceName, cg.scopeCtx)
	_, err := cg.client.ExecuteQuery(ctx, cypher, params)
	if err != nil {
		return fmt.Errorf("degree computation: %w", err)
	}
	return nil
}

// updateFunctionBodyRanges updates the startLine/endLine and startByte/endByte
// properties on Function/Method nodes to reflect the actual AST declaration
// range (the "func" keyword through the closing brace). SCIP indexing only
// stores the identifier's own occurrence range (just the symbol name), which
// makes both line-based containment queries (like findContainingFunction) and
// byte-based source-code retrieval fail — the former resolved to the wrong
// function, the latter returned only the ~15-character identifier span
// instead of the function body.
//
// Requires exactly one range update per node ID. selectCallerCandidate
// already guarantees this by construction (each funcRange resolves to at
// most one candidate, one-to-one), but this is checked explicitly rather
// than trusted implicitly: a second write to the same ID silently
// corrupting the first is exactly the failure mode this whole fix exists to
// eliminate, so a violation here is a programming error in the caller, not
// a data condition to paper over — it errors instead of writing.
func (cg *SCIPCallGraphBuilder) updateFunctionBodyRanges(ctx context.Context, callers []callerInfo) error {
	seen := make(map[string]bool, len(callers))
	for _, c := range callers {
		if seen[c.ID] {
			return fmt.Errorf("updateFunctionBodyRanges: node %s would receive 2+ range updates in one batch — "+
				"this indicates selectCallerCandidate's one-candidate-per-range invariant was violated upstream", c.ID)
		}
		seen[c.ID] = true
	}

	// rangeSource records provenance per RFC-005 I4 — these ranges come from
	// the Go AST (exact), vs "treesitter"/"scip-declaration" in the generic
	// builder.
	cypher := `
		UNWIND $updates AS u
		MATCH (fn) WHERE elementId(fn) = u.id
		SET fn.startLine = u.startLine,
		    fn.endLine = u.endLine,
		    fn.startByte = u.startByte,
		    fn.endByte = u.endByte,
		    fn.paramTypes = u.paramTypes,
		    fn.receiverType = u.receiverType,
		    fn.rangeSource = 'go-ast'
	`
	updates := make([]map[string]any, len(callers))
	for i, c := range callers {
		updates[i] = map[string]any{
			"id":           c.ID,
			"startLine":    c.StartLine,
			"endLine":      c.EndLine,
			"startByte":    c.StartByte,
			"endByte":      c.EndByte,
			"paramTypes":   c.ParamTypes,
			"receiverType": c.ReceiverType,
		}
	}
	_, err := cg.client.ExecuteQuery(ctx, cypher, map[string]any{"updates": updates})
	return err
}

// fileRef pairs a File node's service-relative path with its Neo4j element
// ID — the ID is the CALLS caller for module-scope call sites.
type fileRef struct {
	Path string
	ID   string
}

// listGoFiles returns the .go files owned by the builder's Service node.
// BuildCallGraph guarantees serviceName is set — an unbounded listing would
// pull same-named files from every indexed service.
func (cg *SCIPCallGraphBuilder) listGoFiles(ctx context.Context) ([]fileRef, error) {
	query := `
		MATCH (s:Service {name: $serviceName})-[:CONTAINS]->(f:File)
		WHERE f.path ENDS WITH '.go'
		  AND f.scopeId = $scopeId
		RETURN f.path AS path, elementId(f) AS id
	`
	params := map[string]any{
		"scopeId":     cg.scopeCtx.ScopeID,
		"serviceName": cg.serviceName,
	}

	results, err := cg.client.ExecuteQuery(ctx, query, params)
	if err != nil {
		return nil, err
	}

	files := make([]fileRef, 0, len(results))
	for _, rec := range results {
		m := rec.AsMap()
		p := getStringFromMap(m, "path")
		id := getStringFromMap(m, "id")
		if p != "" && id != "" {
			files = append(files, fileRef{Path: p, ID: id})
		}
	}
	return files, nil
}

// callerInfo pairs an AST-derived body range with the graph node element ID.
type callerInfo struct {
	ID           string   // Neo4j element ID
	StartLine    int      // AST body start line
	EndLine      int      // AST body end line
	StartByte    int      // Byte offset of the "func" keyword (exact, from token.FileSet)
	EndByte      int      // Byte offset just past the closing brace (exact, from token.FileSet)
	ParamTypes   []string // Fully-qualified parameter types
	ReceiverType string   // Receiver type for methods
}

// processFile parses a single Go file with the AST to get function body ranges,
// maps them to graph node IDs, queries references, and creates CALLS edges.
// fileID is the File node's element ID, the caller for module-scope call
// sites (package-level `var x = compute()` initializers).
func (cg *SCIPCallGraphBuilder) processFile(ctx context.Context, filePath, fileID string) (int, error) {
	fullPath := filePath
	if !filepath.IsAbs(filePath) {
		fullPath = filepath.Join(cg.projectPath, filePath)
	}

	// Parse Go source for function body ranges (AST gives us real start/end).
	funcRanges, err := parseFuncRanges(fullPath)
	if err != nil {
		return 0, err
	}

	// Callee-identifier positions: the gate between genuine calls and
	// function-VALUE references. Parsed from the same source the func ranges
	// came from; a file that parses for ranges parses here too.
	callSites, err := parseGoCallSites(fullPath)
	if err != nil {
		return 0, err
	}

	// Parse branch ranges for conditional metadata on CALLS edges.
	branches, _ := parseBranchRanges(fullPath)

	// Upgrade Variable nodes for top-level `var X = func(...){}` to also bear
	// the :Function label. This makes them visible to graphNodesByName below
	// AND to the directTarget filter used when resolving callers from other
	// files (`buildManager(...)` becomes a real CALLS target).
	closureNames := make([]string, 0)
	for _, fr := range funcRanges {
		if fr.IsClosureVar {
			closureNames = append(closureNames, fr.Name)
		}
	}
	if len(closureNames) > 0 {
		if err := cg.upgradeClosureVarsToFunction(ctx, filePath, closureNames); err != nil {
			fmt.Printf("Warning: upgrade closure vars in %s: %v\n", filePath, err)
		}
	}

	// Load graph node candidates for functions in this file, keyed by base
	// name. We filter to only functions whose SCIP signature matches this
	// file's Go package path, so cross-package references stored as
	// Function nodes are excluded. Multiple candidates per name are
	// expected (e.g. several types in the file each with a same-named
	// method) and disambiguated below by definition-position containment,
	// never by "last one wins".
	graphNodes, err := cg.graphNodesByName(ctx, filePath)
	if err != nil {
		return 0, err
	}

	// Build callerInfo list: pair each AST func range with its graph node,
	// selected deterministically by definition-position containment
	// (selectCallerCandidate). A funcRange with zero or 2+ matching
	// candidates is skipped rather than guessed — see
	// skippedAmbiguousRanges below — so a same-name collision never
	// corrupts a node's stored body range or misattributes its calls.
	var callers []callerInfo
	skippedAmbiguousRanges := 0
	for _, fr := range funcRanges {
		// Use base name (strip receiver prefix like "SCIPIndexer.")
		baseName := fr.Name
		if idx := strings.LastIndex(baseName, "."); idx >= 0 {
			baseName = baseName[idx+1:]
		}
		candidates, ok := graphNodes[baseName]
		if !ok {
			continue
		}
		selected, ok := selectCallerCandidate(candidates, fr)
		if !ok {
			skippedAmbiguousRanges++
			continue
		}
		callers = append(callers, callerInfo{
			ID:           selected.ID,
			StartLine:    fr.StartLine,
			EndLine:      fr.EndLine,
			StartByte:    fr.StartByte,
			EndByte:      fr.EndByte,
			ParamTypes:   fr.ParamTypes,
			ReceiverType: fr.ReceiverType,
		})
	}
	if skippedAmbiguousRanges > 0 {
		fmt.Printf("Debug: %s: skipped %d func range(s) with zero or 2+ matching graph node candidates (same-name disambiguation)\n",
			filePath, skippedAmbiguousRanges)
	}

	// A file with zero resolved callers can still hold module-scope call
	// sites (`var _ = register()` in a constants-only file), so processing
	// continues even when callers is empty — resolveCallEdges attributes
	// those to the File node.
	if len(callers) > 0 {
		// Update graph nodes with AST-derived body ranges so that line-based
		// lookups (e.g., findContainingFunction in API analysis) work correctly.
		// SCIP only stores the declaration line; the AST gives us the real body range.
		if err := cg.updateFunctionBodyRanges(ctx, callers); err != nil {
			fmt.Printf("Warning: failed to update body ranges for %s: %v\n", filePath, err)
		}
	}

	// Query: find all references in this file that point to symbols which
	// have a DEFINES edge from a Function or Method.
	// Filter targets to only intra-project calls when modulePath is set.
	// (CONTAINS "" is always true, so empty modulePath disables filtering.)
	//
	// IMPLEMENTS traversal: if the direct target has incoming IMPLEMENTS
	// edges from concrete types, return those instead (may-call fan-out).
	// Otherwise fall back to the direct target.
	// The reference match is service-bounded: filePath is service-relative, so
	// a same-named file in another service would have its call sites attributed
	// to this file's callers by line number. Targets stay cross-service
	// (filtered by modulePath) — a real reference to another sub-service's
	// symbol is a legitimate edge.
	query := `
		MATCH (ref:Reference {filePath: $filePath, scopeId: $scopeId, serviceName: $serviceName})
		      -[:REFERENCES]->(sym:Symbol)
		      <-[:DEFINES]-(directTarget)
		WHERE (directTarget:Function OR directTarget:Method)
		  AND directTarget.signature CONTAINS $modulePath
		OPTIONAL MATCH (concreteTarget)-[:IMPLEMENTS]->(directTarget)
		WHERE (concreteTarget:Function OR concreteTarget:Method)
		  AND concreteTarget.signature CONTAINS $modulePath
		WITH ref, directTarget,
		     COLLECT(DISTINCT concreteTarget) AS concretes
		UNWIND
		  CASE WHEN SIZE(concretes) > 0 THEN concretes
		       ELSE [directTarget]
		  END AS target
		RETURN ref.startLine AS refLine,
		       coalesce(ref.startColumn, -1) AS refCol,
		       directTarget.name AS refName,
		       elementId(target) AS targetId
	`

	results, err := cg.client.ExecuteQuery(ctx, query, map[string]any{
		"filePath":    filePath,
		"scopeId":     cg.scopeCtx.ScopeID,
		"serviceName": cg.serviceName,
		"modulePath":  cg.modulePath,
	})
	if err != nil {
		return 0, err
	}

	rows := make([]callRefRow, 0, len(results))
	for _, rec := range results {
		rm := rec.AsMap()
		rows = append(rows, callRefRow{
			Line: int(getInt64FromMap(rm, "refLine")),
			Col:  int(getInt64FromMap(rm, "refCol")),
			// The reference occurrence covers the callee identifier, whose
			// text is the DIRECT target's name even after IMPLEMENTS fan-out
			// rewrites the edge target to a concrete implementation.
			Name:     getStringFromMap(rm, "refName"),
			TargetID: getStringFromMap(rm, "targetId"),
		})
	}

	edges, valueEdges := resolveCallEdges(rows, callers, branches, callSites, fileID)

	created := 0
	for _, e := range edges {
		_, err := cg.client.MergeRelationship(ctx, e.CallerID, e.TargetID, string(models.CallsRel),
			nil,
			map[string]any{
				"line":          e.Line,
				"filePath":      filePath,
				"branchDepth":   e.BranchDepth,
				"isConditional": e.IsConditional,
				"scope":         cg.scopeCtx.Scope,
				"scopeId":       cg.scopeCtx.ScopeID,
			})
		if err != nil {
			fmt.Printf("Warning: failed to create CALLS edge: %v\n", err)
			continue
		}
		created++
	}

	// Address-taken references (`cfg.Fn = handler`) — distinct edge type so
	// liveness can keep such functions conservatively alive without CALLS
	// fabricating invocations. Not counted in `created`: the returned count
	// feeds the "CALLS relationships" log line and telemetry expectations.
	for _, e := range valueEdges {
		if _, err := cg.client.MergeRelationship(ctx, e.CallerID, e.TargetID, string(models.UsesValueRel),
			nil,
			map[string]any{
				"line":     e.Line,
				"filePath": filePath,
				"scope":    cg.scopeCtx.Scope,
				"scopeId":  cg.scopeCtx.ScopeID,
			}); err != nil {
			fmt.Printf("Warning: failed to create USES_VALUE edge: %v\n", err)
		}
	}

	return created, nil
}

// callRefRow is one raw reference-to-function row returned by the
// call-reference query, before call-site classification, caller resolution
// and dedup. Col is the occurrence's 0-based start column (-1 when the
// Reference node predates column stamping); Name is the referenced
// function's identifier as it appears at the reference site.
type callRefRow struct {
	Line     int
	Col      int
	Name     string
	TargetID string
}

// callEdge is a single resolved CALLS edge, one per distinct (caller, target)
// pair in a file.
type callEdge struct {
	CallerID      string
	TargetID      string
	Line          int
	BranchDepth   int
	IsConditional bool
}

// resolveCallEdges classifies each reference row against the file's call
// sites, maps genuine calls to their enclosing caller (or to the File node
// for module-scope sites), and collapses multiple call sites between the
// same (caller, target) pair into a single edge, deterministically keeping
// the smallest call-site line (see collapseToMinLinePerPair, shared with the
// generic/non-Go builder). Branch metadata (branchDepth, isConditional) is
// then computed from the winning line, so it always describes the call site
// that was actually kept.
//
// Classification (RFC-013 follow-up, tasks #18/#19):
//   - not a call site (function-VALUE reference, `handler = fn`) →
//     (caller|File)-[:USES_VALUE]-> — returned separately, NOT a CALLS
//     edge. Kept in-graph so liveness can treat address-taken functions as
//     conservatively live (dynamic dispatch through a stored callback).
//   - call site inside a function body → (Function|Method)-[:CALLS]->
//   - call site outside every body (package-level var initializer) →
//     (File)-[:CALLS]->, import-time invocation
//
// A nil sites index disables classification entirely (legacy: every in-body
// ref is a CALLS edge, module-scope refs drop, no USES_VALUE) — the Go path
// always has one, but the shared shape keeps the generic builder's
// no-grammar fallback honest.
//
// Pure function, no I/O — testable without Neo4j.
func resolveCallEdges(rows []callRefRow, callers []callerInfo, branches []branchRange, sites *callSiteIndex, fileID string) ([]callEdge, []minLineEdge) {
	triples := make([]minLineEdge, 0, len(rows))
	var valueTriples []minLineEdge
	for _, row := range rows {
		caller := findEnclosingCaller(callers, row.Line)
		if sites != nil && !sites.isCallSite(row.Line, row.Col, row.Name) {
			// Function-value reference: record who takes the address.
			switch {
			case caller != nil:
				valueTriples = append(valueTriples, minLineEdge{CallerID: caller.ID, TargetID: row.TargetID, Line: row.Line})
			case fileID != "":
				valueTriples = append(valueTriples, minLineEdge{CallerID: fileID, TargetID: row.TargetID, Line: row.Line})
			}
			continue
		}
		switch {
		case caller != nil:
			triples = append(triples, minLineEdge{CallerID: caller.ID, TargetID: row.TargetID, Line: row.Line})
		case sites != nil && fileID != "":
			// Module-scope call: confirmed invocation with no enclosing
			// function body — the file itself is the caller.
			triples = append(triples, minLineEdge{CallerID: fileID, TargetID: row.TargetID, Line: row.Line})
		}
	}

	collapsed := collapseToMinLinePerPair(triples)

	out := make([]callEdge, 0, len(collapsed))
	for _, e := range collapsed {
		depth, isCond := branchDepthAtLine(branches, e.Line)
		out = append(out, callEdge{
			CallerID:      e.CallerID,
			TargetID:      e.TargetID,
			Line:          e.Line,
			BranchDepth:   depth,
			IsConditional: isCond,
		})
	}
	return out, collapseToMinLinePerPair(valueTriples)
}

// upgradeClosureVarsToFunction adds the :Function label to Variable nodes
// whose source is `var X = func(...){}` at package scope. The Variable label
// is preserved (multi-label); the added :Function label makes them visible to
// graphNodesByName and to call-graph target resolution. Without this, calls
// to `X(...)` from any file resolve to nothing and calls inside X's body are
// never attributed to a caller.
func (cg *SCIPCallGraphBuilder) upgradeClosureVarsToFunction(ctx context.Context, filePath string, names []string) error {
	if len(names) == 0 {
		return nil
	}
	// Service-bounded because this MUTATES matched nodes: without it, a
	// Variable with the same name in another service's same-named file gets
	// the :Function label added to it.
	query := `
		MATCH (v:Variable)
		WHERE v.filePath = $filePath
		  AND v.scopeId = $scopeId
		  AND v.serviceName = $serviceName
		  AND v.name IN $names
		SET v:Function
	`
	_, err := cg.client.ExecuteQuery(ctx, query, map[string]any{
		"filePath":    filePath,
		"scopeId":     cg.scopeCtx.ScopeID,
		"serviceName": cg.serviceName,
		"names":       names,
	})
	return err
}

// graphNodeCandidate is one Function/Method node matching a base name in
// graphNodesByName's result: its element ID plus the pristine SCIP
// definition-occurrence line (the func/method name token's own line — NOT
// the AST body range, which processFile overwrites later in the same run
// via updateFunctionBodyRanges). DefLine is what disambiguates same-named
// candidates: it is stamped once at initial SCIP ingestion
// (scip_indexer.go's MergeNodesBatch(Definition), which always runs before
// BuildCallGraph in the pipeline) and is therefore fresh and exact even on
// a re-index of a graph a PRIOR run had already corrupted via the bug this
// type exists to fix.
type graphNodeCandidate struct {
	ID      string
	DefLine int
}

// graphNodesByName returns, for each base function/method name in the given
// file, every matching Function/Method node (there can be more than one: Go
// files routinely declare multiple methods sharing a bare name across
// different receiver types, e.g. several stage structs each with a Run
// method). Callers must disambiguate by definition position — see
// selectCallerCandidate — rather than picking an arbitrary one.
//
// This filters out cross-package reference nodes that SCIP stores as
// Function nodes in the same filePath, matching on the file's Go package
// directory.
func (cg *SCIPCallGraphBuilder) graphNodesByName(ctx context.Context, filePath string) (map[string][]graphNodeCandidate, error) {
	// Derive the Go package directory from the file path (e.g.,
	// "pkg/indexer/static/scip_indexer.go" -> "pkg/indexer/static").
	pkgDir := filepath.Dir(filePath)

	// Match concrete definitions ("().") and top-level callable vars
	// (term descriptors at package scope, no "#" parent — these are
	// `var X = func(...){}` upgraded with :Function via
	// upgradeClosureVarsToFunction). Interface method nodes (signature ends
	// in "." with a "#" parent) stay excluded — they collide with
	// implementation method names in the same file and SCIP fans out to
	// impls via IMPLEMENTS in processFile.
	// Service-bounded: these element IDs receive AST-derived body ranges via
	// updateFunctionBodyRanges, so matching another service's same-named file
	// would overwrite that service's line/byte ranges with this file's.
	query := `
		MATCH (f)
		WHERE (f:Function OR f:Method)
		  AND f.filePath = $filePath
		  AND f.scopeId = $scopeId
		  AND f.serviceName = $serviceName
		  AND f.signature CONTAINS $pkgDir
		  AND (f.signature ENDS WITH '().'
		       OR (f.signature ENDS WITH '.' AND NOT f.signature CONTAINS '#'))
		RETURN elementId(f) AS id, f.name AS name, f.startLine AS defLine
	`

	results, err := cg.client.ExecuteQuery(ctx, query, map[string]any{
		"filePath":    filePath,
		"scopeId":     cg.scopeCtx.ScopeID,
		"serviceName": cg.serviceName,
		"pkgDir":      pkgDir,
	})
	if err != nil {
		return nil, err
	}

	m := make(map[string][]graphNodeCandidate, len(results))
	for _, rec := range results {
		rm := rec.AsMap()
		id := getStringFromMap(rm, "id")
		name := getStringFromMap(rm, "name")
		if id == "" || name == "" {
			continue
		}
		baseName := strings.TrimSuffix(name, "().")
		m[baseName] = append(m[baseName], graphNodeCandidate{
			ID:      id,
			DefLine: int(getInt64FromMap(rm, "defLine")),
		})
	}
	return m, nil
}

// selectCallerCandidate disambiguates same-named graph node candidates for
// one AST funcRange by exact definition-position containment: the winning
// candidate is the one whose SCIP definition-occurrence line
// (graphNodeCandidate.DefLine — the function name token's own line) falls
// within [fr.DeclLine, fr.EndLine]. fr.DeclLine (not fr.StartLine, the body
// open-brace line) is used as the lower bound because the definition
// occurrence is the name token itself, which sits on the declaration line,
// at or before the opening brace.
//
// Ambiguity is never silently resolved: zero matches (a node this AST range
// doesn't correspond to at all — e.g. a stale/renamed declaration) or two-or
// -more matches (defensive; should not happen given DefLine's precision, but
// would indicate a deeper indexing inconsistency) both return ok=false, and
// the caller must skip the range update for that funcRange rather than
// guess. This is the fix for the same-name range-clobbering bug: previously
// graphNodesByName collapsed every same-named candidate to a single
// map entry (last Cypher row wins, nondeterministically), so multiple
// distinct AST ranges all resolved to the same node and
// updateFunctionBodyRanges stamped whichever range happened to be applied
// last onto ALL of them — corrupting every same-named node but one, and, via
// findEnclosingCaller, misattributing every call site in the corrupted
// node's stolen range to the wrong function entirely.
func selectCallerCandidate(candidates []graphNodeCandidate, fr funcRange) (graphNodeCandidate, bool) {
	var match graphNodeCandidate
	matches := 0
	for _, c := range candidates {
		if c.DefLine >= fr.DeclLine && c.DefLine <= fr.EndLine {
			match = c
			matches++
		}
	}
	if matches != 1 {
		return graphNodeCandidate{}, false
	}
	return match, true
}

// findEnclosingCaller finds the innermost callerInfo whose body range
// contains the given line number.
func findEnclosingCaller(callers []callerInfo, line int) *callerInfo {
	var best *callerInfo
	bestSpan := int(^uint(0) >> 1)

	for i := range callers {
		c := &callers[i]
		if line >= c.StartLine && line <= c.EndLine {
			span := c.EndLine - c.StartLine
			if span < bestSpan {
				best = c
				bestSpan = span
			}
		}
	}
	return best
}

// parseFuncRanges parses a Go source file and returns the line ranges for each
// function and method body.
func parseFuncRanges(filePath string) ([]funcRange, error) {
	src, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filePath, src, 0)
	if err != nil {
		return nil, err
	}

	// Collect import paths so we can resolve qualified type names.
	importMap := buildImportMap(f)

	var ranges []funcRange
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}

		name := fn.Name.Name
		var receiverType string
		// For methods, include the receiver type in the name to disambiguate.
		if fn.Recv != nil && len(fn.Recv.List) > 0 {
			recvType := exprName(fn.Recv.List[0].Type)
			if recvType != "" {
				name = recvType + "." + name
				receiverType = exprTypeName(fn.Recv.List[0].Type)
			}
		}

		// Extract parameter types.
		var paramTypes []string
		if fn.Type.Params != nil {
			for _, field := range fn.Type.Params.List {
				typeName := resolveTypeName(field.Type, importMap)
				// A field may declare multiple names (e.g., a, b int).
				count := len(field.Names)
				if count == 0 {
					count = 1 // unnamed parameter
				}
				for range count {
					paramTypes = append(paramTypes, typeName)
				}
			}
		}

		declLine := fset.Position(fn.Name.Pos()).Line
		startLine := fset.Position(fn.Body.Lbrace).Line
		endLine := fset.Position(fn.Body.Rbrace).Line
		// Byte range covers the whole declaration — "func" keyword through the
		// closing brace — not just the body, so source-code retrieval returns
		// the complete function rather than only its body block. fn.Pos()
		// resolves to the "func" keyword (FuncDecl.Pos() == Type.Pos()); fn.End()
		// resolves to Body.End(), i.e. one past the closing brace.
		startByte := fset.Position(fn.Pos()).Offset
		endByte := fset.Position(fn.End()).Offset

		ranges = append(ranges, funcRange{
			Name:         name,
			DeclLine:     declLine,
			StartLine:    startLine,
			EndLine:      endLine,
			StartByte:    startByte,
			EndByte:      endByte,
			ParamTypes:   paramTypes,
			ReceiverType: receiverType,
		})
	}

	// Top-level `var X = func(...){}` and `var X func(...)` initialized to a
	// FuncLit. scip-go classifies these as Variable kind, but they're callable
	// and have a body. We treat them as funcRanges so calls inside the closure
	// get attributed to the var (via the upgraded :Function label, see
	// upgradeClosureVarsToFunction).
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range vs.Names {
				if i >= len(vs.Values) {
					continue
				}
				lit, ok := vs.Values[i].(*ast.FuncLit)
				if !ok || lit.Body == nil {
					continue
				}
				var paramTypes []string
				if lit.Type.Params != nil {
					for _, field := range lit.Type.Params.List {
						typeName := resolveTypeName(field.Type, importMap)
						count := len(field.Names)
						if count == 0 {
							count = 1
						}
						for range count {
							paramTypes = append(paramTypes, typeName)
						}
					}
				}
				ranges = append(ranges, funcRange{
					Name:         name.Name,
					DeclLine:     fset.Position(name.Pos()).Line,
					StartLine:    fset.Position(lit.Body.Lbrace).Line,
					EndLine:      fset.Position(lit.Body.Rbrace).Line,
					StartByte:    fset.Position(lit.Pos()).Offset,
					EndByte:      fset.Position(lit.End()).Offset,
					ParamTypes:   paramTypes,
					IsClosureVar: true,
				})
			}
		}
	}

	return ranges, nil
}

// buildImportMap builds a mapping from local package alias/name to import path
// for a Go file's imports. For example, "http" -> "net/http", "models" -> "github.com/...".
func buildImportMap(f *ast.File) map[string]string {
	m := make(map[string]string)
	for _, imp := range f.Imports {
		if imp.Path == nil {
			continue
		}
		importPath := strings.Trim(imp.Path.Value, `"`)
		var localName string
		if imp.Name != nil {
			localName = imp.Name.Name
		} else {
			// Default: last segment of import path
			parts := strings.Split(importPath, "/")
			localName = parts[len(parts)-1]
		}
		if localName != "_" && localName != "." {
			m[localName] = importPath
		}
	}
	return m
}

// resolveTypeName resolves an AST type expression to a qualified type string,
// using the import map to resolve package-qualified names (e.g., http.Request -> net/http.Request).
func resolveTypeName(expr ast.Expr, importMap map[string]string) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + resolveTypeName(t.X, importMap)
	case *ast.SelectorExpr:
		if ident, ok := t.X.(*ast.Ident); ok {
			if fullPkg, found := importMap[ident.Name]; found {
				return fullPkg + "." + t.Sel.Name
			}
			return ident.Name + "." + t.Sel.Name
		}
		return t.Sel.Name
	case *ast.ArrayType:
		return "[]" + resolveTypeName(t.Elt, importMap)
	case *ast.MapType:
		return "map[" + resolveTypeName(t.Key, importMap) + "]" + resolveTypeName(t.Value, importMap)
	case *ast.InterfaceType:
		return "interface{}"
	case *ast.FuncType:
		return "func"
	case *ast.ChanType:
		return "chan " + resolveTypeName(t.Value, importMap)
	case *ast.Ellipsis:
		return "..." + resolveTypeName(t.Elt, importMap)
	case *ast.IndexExpr:
		return resolveTypeName(t.X, importMap) + "[" + resolveTypeName(t.Index, importMap) + "]"
	case *ast.IndexListExpr:
		return resolveTypeName(t.X, importMap)
	case *ast.StructType:
		return "struct{}"
	}
	return "unknown"
}

// exprTypeName extracts a string representation of a receiver type expression,
// including pointer markers (e.g., "*SCIPIndexer").
func exprTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + exprTypeName(t.X)
	case *ast.IndexExpr:
		return exprTypeName(t.X)
	case *ast.IndexListExpr:
		return exprTypeName(t.X)
	}
	return ""
}

// parseBranchRanges extracts line ranges for conditional blocks (if/switch/select)
// from a Go source file. Each range includes its nesting depth.
func parseBranchRanges(filePath string) ([]branchRange, error) {
	src, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filePath, src, 0)
	if err != nil {
		return nil, err
	}

	var ranges []branchRange
	var walk func(node ast.Node, depth int)
	walk = func(node ast.Node, depth int) {
		if node == nil {
			return
		}
		switch n := node.(type) {
		case *ast.IfStmt:
			start := fset.Position(n.Pos()).Line
			end := fset.Position(n.End()).Line
			ranges = append(ranges, branchRange{StartLine: start, EndLine: end, Depth: depth + 1})
			ast.Inspect(n.Body, func(child ast.Node) bool {
				if child == n.Body {
					return true
				}
				walk(child, depth+1)
				return false
			})
			if n.Else != nil {
				walk(n.Else, depth)
			}
			return
		case *ast.SwitchStmt:
			start := fset.Position(n.Pos()).Line
			end := fset.Position(n.End()).Line
			ranges = append(ranges, branchRange{StartLine: start, EndLine: end, Depth: depth + 1})
			return
		case *ast.TypeSwitchStmt:
			start := fset.Position(n.Pos()).Line
			end := fset.Position(n.End()).Line
			ranges = append(ranges, branchRange{StartLine: start, EndLine: end, Depth: depth + 1})
			return
		case *ast.SelectStmt:
			start := fset.Position(n.Pos()).Line
			end := fset.Position(n.End()).Line
			ranges = append(ranges, branchRange{StartLine: start, EndLine: end, Depth: depth + 1})
			return
		}
	}

	ast.Inspect(f, func(node ast.Node) bool {
		walk(node, 0)
		return true
	})

	return ranges, nil
}

// branchDepthAtLine returns the maximum nesting depth of conditional blocks
// enclosing the given line, and whether the line is inside any conditional block.
func branchDepthAtLine(branches []branchRange, line int) (int, bool) {
	maxDepth := 0
	for _, b := range branches {
		if line >= b.StartLine && line <= b.EndLine {
			if b.Depth > maxDepth {
				maxDepth = b.Depth
			}
		}
	}
	return maxDepth, maxDepth > 0
}

// exprName extracts the type name from a receiver expression, handling
// pointer receivers like *Foo.
func exprName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return exprName(t.X)
	case *ast.IndexExpr:
		return exprName(t.X)
	case *ast.IndexListExpr:
		return exprName(t.X)
	}
	return ""
}

// findEnclosingFunc finds the innermost function whose body range contains
// the given line number.
func findEnclosingFunc(ranges []funcRange, line int) *funcRange {
	var best *funcRange
	bestSpan := int(^uint(0) >> 1) // max int

	for i := range ranges {
		r := &ranges[i]
		if line >= r.StartLine && line <= r.EndLine {
			span := r.EndLine - r.StartLine
			if span < bestSpan {
				best = r
				bestSpan = span
			}
		}
	}
	return best
}

// isGoFile checks whether a path ends with .go
func isGoFile(path string) bool {
	return strings.HasSuffix(path, ".go")
}
