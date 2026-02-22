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
	Name      string
	DeclLine  int // Line of the function name declaration (matches SCIP's startLine)
	StartLine int // Body opening brace line
	EndLine   int // Body closing brace line
}

// SCIPCallGraphBuilder infers CALLS relationships between Functions/Methods
// by correlating SCIP Reference nodes with Go AST function body ranges.
type SCIPCallGraphBuilder struct {
	client      *neo4j.Client
	projectPath string
	modulePath  string // Go module path from go.mod, used to filter external targets
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
	return nil
}

// listGoFiles returns all file paths with a .go extension that are indexed in the graph.
func (cg *SCIPCallGraphBuilder) listGoFiles(ctx context.Context) ([]string, error) {
	query := `
		MATCH (f:File)
		WHERE f.path ENDS WITH '.go'
		  AND f.scopeId = $scopeId
		RETURN f.path AS path
	`
	results, err := cg.client.ExecuteQuery(ctx, query, map[string]any{
		"scopeId": cg.scopeCtx.ScopeID,
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

// callerInfo pairs an AST-derived body range with the graph node element ID.
type callerInfo struct {
	ID        string // Neo4j element ID
	StartLine int    // AST body start line
	EndLine   int    // AST body end line
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
			ID:        nodeID,
			StartLine: fr.StartLine,
			EndLine:   fr.EndLine,
		})
	}

	if len(callers) == 0 {
		return 0, nil
	}

	// Query: find all references in this file that point to symbols which
	// have a DEFINES edge from a Function or Method.
	// Filter targets to only intra-project calls when modulePath is set.
	// (CONTAINS "" is always true, so empty modulePath disables filtering.)
	query := `
		MATCH (ref:Reference {filePath: $filePath, scopeId: $scopeId})
		      -[:REFERENCES]->(sym:Symbol)
		      <-[:DEFINES]-(target)
		WHERE (target:Function OR target:Method)
		  AND target.signature CONTAINS $modulePath
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

		_, err := cg.client.MergeRelationship(ctx, caller.ID, targetID, string(models.CallsRel),
			nil,
			map[string]any{"line": refLine, "filePath": filePath})
		if err != nil {
			fmt.Printf("Warning: failed to create CALLS edge: %v\n", err)
			continue
		}
		created++
	}

	return created, nil
}

// graphNodesByName returns a map of base function name -> element ID for
// functions in the given file whose SCIP signature matches the file's
// Go package directory. This filters out cross-package reference nodes
// that SCIP stores as Function nodes in the same filePath.
func (cg *SCIPCallGraphBuilder) graphNodesByName(ctx context.Context, filePath string) (map[string]string, error) {
	// Derive the Go package directory from the file path (e.g.,
	// "pkg/indexer/static/scip_indexer.go" -> "pkg/indexer/static").
	pkgDir := filepath.Dir(filePath)

	query := `
		MATCH (f)
		WHERE (f:Function OR f:Method)
		  AND f.filePath = $filePath
		  AND f.scopeId = $scopeId
		  AND f.signature CONTAINS $pkgDir
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

	var ranges []funcRange
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}

		name := fn.Name.Name
		// For methods, include the receiver type in the name to disambiguate.
		if fn.Recv != nil && len(fn.Recv.List) > 0 {
			recvType := exprName(fn.Recv.List[0].Type)
			if recvType != "" {
				name = recvType + "." + name
			}
		}

		declLine := fset.Position(fn.Name.Pos()).Line
		startLine := fset.Position(fn.Body.Lbrace).Line
		endLine := fset.Position(fn.Body.Rbrace).Line

		ranges = append(ranges, funcRange{
			Name:      name,
			DeclLine:  declLine,
			StartLine: startLine,
			EndLine:   endLine,
		})
	}

	return ranges, nil
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
