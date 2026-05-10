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

	"github.com/context-maximiser/code-graph/libs/core-models-go"
	"github.com/context-maximiser/code-graph/libs/neo4j-go"
)

// funcRange represents the line span of a function/method body in a Go file.
type funcRange struct {
	Name         string
	DeclLine     int      // Line of the function name declaration (matches SCIP's startLine)
	StartLine    int      // Body opening brace line
	EndLine      int      // Body closing brace line
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
	fmt.Println("Building call graph from SCIP references...")

	// Get all Go source files in the graph.
	files, err := cg.listGoFiles(ctx)
	if err != nil {
		return fmt.Errorf("failed to list Go files: %w", err)
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

	// Compute in/out degree properties on all Function/Method nodes in scope.
	if err := cg.ComputeDegreeProperties(ctx); err != nil {
		fmt.Printf("Warning: degree computation failed: %v\n", err)
	}

	return nil
}

// ComputeDegreeProperties sets inDegree and outDegree properties on all
// Function/Method nodes in the current scope based on CALLS relationships.
func (cg *SCIPCallGraphBuilder) ComputeDegreeProperties(ctx context.Context) error {
	cypher := `
		MATCH (fn)
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
		"scopeId": cg.scopeCtx.ScopeID,
	})
	if err != nil {
		return fmt.Errorf("degree computation: %w", err)
	}
	fmt.Println("Computed inDegree/outDegree for all Function/Method nodes")
	return nil
}

// updateFunctionBodyRanges updates the startLine and endLine properties on
// Function/Method nodes to reflect the actual AST body range (Lbrace to Rbrace).
// SCIP indexing only stores the declaration line, which makes line-based
// containment queries (like findContainingFunction) fail.
func (cg *SCIPCallGraphBuilder) updateFunctionBodyRanges(ctx context.Context, callers []callerInfo) error {
	cypher := `
		UNWIND $updates AS u
		MATCH (fn) WHERE elementId(fn) = u.id
		SET fn.startLine = u.startLine,
		    fn.endLine = u.endLine,
		    fn.paramTypes = u.paramTypes,
		    fn.receiverType = u.receiverType
	`
	updates := make([]map[string]any, len(callers))
	for i, c := range callers {
		updates[i] = map[string]any{
			"id":           c.ID,
			"startLine":    c.StartLine,
			"endLine":      c.EndLine,
			"paramTypes":   c.ParamTypes,
			"receiverType": c.ReceiverType,
		}
	}
	_, err := cg.client.ExecuteQuery(ctx, cypher, map[string]any{"updates": updates})
	return err
}

// listGoFiles returns all file paths with a .go extension that are indexed in the graph.
// When serviceName is set, only files owned by that Service node are returned, preventing
// cross-module path mismatches during call graph construction.
func (cg *SCIPCallGraphBuilder) listGoFiles(ctx context.Context) ([]string, error) {
	var query string
	var params map[string]any

	if cg.serviceName != "" {
		query = `
			MATCH (s:Service {name: $serviceName})-[:CONTAINS]->(f:File)
			WHERE f.path ENDS WITH '.go'
			  AND f.scopeId = $scopeId
			RETURN f.path AS path
		`
		params = map[string]any{
			"scopeId":     cg.scopeCtx.ScopeID,
			"serviceName": cg.serviceName,
		}
	} else {
		query = `
			MATCH (f:File)
			WHERE f.path ENDS WITH '.go'
			  AND f.scopeId = $scopeId
			RETURN f.path AS path
		`
		params = map[string]any{
			"scopeId": cg.scopeCtx.ScopeID,
		}
	}

	results, err := cg.client.ExecuteQuery(ctx, query, params)
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

// callerInfo pairs an AST-derived body range with the graph node element ID.
type callerInfo struct {
	ID           string   // Neo4j element ID
	StartLine    int      // AST body start line
	EndLine      int      // AST body end line
	ParamTypes   []string // Fully-qualified parameter types
	ReceiverType string   // Receiver type for methods
}

// processFile parses a single Go file with the AST to get function body ranges,
// maps them to graph node IDs, queries references, and creates CALLS edges.
func (cg *SCIPCallGraphBuilder) processFile(ctx context.Context, filePath string) (int, error) {
	fullPath := filePath
	if !filepath.IsAbs(filePath) {
		fullPath = filepath.Join(cg.projectPath, filePath)
	}

	// Parse Go source for function body ranges (AST gives us real start/end).
	funcRanges, err := parseFuncRanges(fullPath)
	if err != nil {
		return 0, err
	}
	if len(funcRanges) == 0 {
		return 0, nil
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

	// Load graph node IDs for functions in this file, keyed by base name.
	// We filter to only functions whose SCIP signature matches this file's
	// Go package path, so cross-package references stored as Function nodes
	// are excluded.
	graphNodes, err := cg.graphNodesByName(ctx, filePath)
	if err != nil {
		return 0, err
	}

	// Build callerInfo list: pair each AST func range with its graph ID.
	var callers []callerInfo
	for _, fr := range funcRanges {
		// Use base name (strip receiver prefix like "SCIPIndexer.")
		baseName := fr.Name
		if idx := strings.LastIndex(baseName, "."); idx >= 0 {
			baseName = baseName[idx+1:]
		}
		nodeID, ok := graphNodes[baseName]
		if !ok {
			continue
		}
		callers = append(callers, callerInfo{
			ID:           nodeID,
			StartLine:    fr.StartLine,
			EndLine:      fr.EndLine,
			ParamTypes:   fr.ParamTypes,
			ReceiverType: fr.ReceiverType,
		})
	}

	if len(callers) == 0 {
		return 0, nil
	}

	// Update graph nodes with AST-derived body ranges so that line-based
	// lookups (e.g., findContainingFunction in API analysis) work correctly.
	// SCIP only stores the declaration line; the AST gives us the real body range.
	if err := cg.updateFunctionBodyRanges(ctx, callers); err != nil {
		fmt.Printf("Warning: failed to update body ranges for %s: %v\n", filePath, err)
	}

	// Query: find all references in this file that point to symbols which
	// have a DEFINES edge from a Function or Method.
	// Filter targets to only intra-project calls when modulePath is set.
	// (CONTAINS "" is always true, so empty modulePath disables filtering.)
	//
	// IMPLEMENTS traversal: if the direct target has incoming IMPLEMENTS
	// edges from concrete types, return those instead (may-call fan-out).
	// Otherwise fall back to the direct target.
	query := `
		MATCH (ref:Reference {filePath: $filePath, scopeId: $scopeId})
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
		       elementId(target) AS targetId
	`

	results, err := cg.client.ExecuteQuery(ctx, query, map[string]any{
		"filePath":   filePath,
		"scopeId":    cg.scopeCtx.ScopeID,
		"modulePath": cg.modulePath,
	})
	if err != nil {
		return 0, err
	}

	created := 0
	seen := map[string]bool{}

	for _, rec := range results {
		rm := rec.AsMap()
		refLine := int(getInt64FromMap(rm, "refLine"))
		targetID := getStringFromMap(rm, "targetId")

		caller := findEnclosingCaller(callers, refLine)
		if caller == nil {
			continue
		}

		pairKey := caller.ID + "->" + targetID
		if caller.ID == targetID || seen[pairKey] {
			continue
		}
		seen[pairKey] = true

		// Compute branch metadata for this call site.
		depth, isCond := branchDepthAtLine(branches, refLine)

		_, err := cg.client.MergeRelationship(ctx, caller.ID, targetID, string(models.CallsRel),
			nil,
			map[string]any{
				"line":          refLine,
				"filePath":      filePath,
				"branchDepth":   depth,
				"isConditional": isCond,
				"scope":         cg.scopeCtx.Scope,
				"scopeId":       cg.scopeCtx.ScopeID,
			})
		if err != nil {
			fmt.Printf("Warning: failed to create CALLS edge: %v\n", err)
			continue
		}
		created++
	}

	return created, nil
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
	query := `
		MATCH (v:Variable)
		WHERE v.filePath = $filePath
		  AND v.scopeId = $scopeId
		  AND v.name IN $names
		SET v:Function
	`
	_, err := cg.client.ExecuteQuery(ctx, query, map[string]any{
		"filePath": filePath,
		"scopeId":  cg.scopeCtx.ScopeID,
		"names":    names,
	})
	return err
}

// graphNodesByName returns a map of base function name -> element ID for
// functions in the given file whose SCIP signature matches the file's
// Go package directory. This filters out cross-package reference nodes
// that SCIP stores as Function nodes in the same filePath.
func (cg *SCIPCallGraphBuilder) graphNodesByName(ctx context.Context, filePath string) (map[string]string, error) {
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
	query := `
		MATCH (f)
		WHERE (f:Function OR f:Method)
		  AND f.filePath = $filePath
		  AND f.scopeId = $scopeId
		  AND f.signature CONTAINS $pkgDir
		  AND (f.signature ENDS WITH '().'
		       OR (f.signature ENDS WITH '.' AND NOT f.signature CONTAINS '#'))
		RETURN elementId(f) AS id, f.name AS name
	`

	results, err := cg.client.ExecuteQuery(ctx, query, map[string]any{
		"filePath": filePath,
		"scopeId":  cg.scopeCtx.ScopeID,
		"pkgDir":   pkgDir,
	})
	if err != nil {
		return nil, err
	}

	m := make(map[string]string, len(results))
	for _, rec := range results {
		rm := rec.AsMap()
		id := getStringFromMap(rm, "id")
		name := getStringFromMap(rm, "name")
		if id != "" && name != "" {
			baseName := strings.TrimSuffix(name, "().")
			m[baseName] = id
		}
	}
	return m, nil
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

		ranges = append(ranges, funcRange{
			Name:         name,
			DeclLine:     declLine,
			StartLine:    startLine,
			EndLine:      endLine,
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
