package static

import (
	"context"
	"fmt"
	"go/ast"
	"go/token"
	"log"
	"strings"

	"github.com/context-maximiser/code-graph/libs/core-models-go"
	"github.com/context-maximiser/code-graph/libs/neo4j-go"
)

// CallGraphExtractor extracts call relationships from Go AST
type CallGraphExtractor struct {
	client      *neo4j.Client
	fset        *token.FileSet
	currentFunc string // Current function being analyzed
	packageName string
	filePath    string
}

// NewCallGraphExtractor creates a new call graph extractor
func NewCallGraphExtractor(client *neo4j.Client) *CallGraphExtractor {
	return &CallGraphExtractor{
		client: client,
	}
}

// ExtractCallsFromFunction analyzes a function declaration for call relationships
func (cge *CallGraphExtractor) ExtractCallsFromFunction(ctx context.Context, funcDecl *ast.FuncDecl, funcID string, fset *token.FileSet, packageName, filePath string) error {
	cge.fset = fset
	cge.currentFunc = funcID
	cge.packageName = packageName
	cge.filePath = filePath

	if funcDecl.Body == nil {
		return nil // No body to analyze
	}

	// Walk through the function body to find calls
	ast.Inspect(funcDecl.Body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CallExpr:
			cge.processCallExpression(ctx, node, funcID)
		case *ast.FuncLit:
			// Handle anonymous functions/closures
			if node.Body != nil {
				ast.Inspect(node.Body, func(inner ast.Node) bool {
					if callExpr, ok := inner.(*ast.CallExpr); ok {
						cge.processCallExpression(ctx, callExpr, funcID)
					}
					return true
				})
			}
		}
		return true
	})

	return nil
}

// processCallExpression handles individual call expressions
func (cge *CallGraphExtractor) processCallExpression(ctx context.Context, callExpr *ast.CallExpr, callerID string) {
	pos := cge.fset.Position(callExpr.Pos())

	// Extract the called function name
	calledName := cge.extractCalledFunctionName(callExpr)
	if calledName == "" {
		return // Skip if we can't determine the called function
	}

	// Determine if this is a dynamic call (interface method, function pointer, etc.)
	isDynamic := cge.isDynamicCall(callExpr)

	// Try to find the target function in the database
	targetFuncID := cge.findTargetFunction(ctx, calledName)
	if targetFuncID == "" {
		// Create a placeholder function node for external calls
		targetFuncID = cge.createPlaceholderFunction(ctx, calledName)
	}

	if targetFuncID != "" {
		// Create CALLS relationship
		relProps := map[string]any{
			"line":        pos.Line,
			"isDynamic":   isDynamic,
			"isRecursive": calledName == cge.getCurrentFunctionName(callerID),
		}

		_, err := cge.client.CreateRelationship(ctx, callerID, targetFuncID, string(models.CallsRel), relProps)
		if err != nil {
			log.Printf("Warning: failed to create CALLS relationship from %s to %s: %v", callerID, targetFuncID, err)
		}
	}
}

// extractCalledFunctionName extracts the function name from a call expression
func (cge *CallGraphExtractor) extractCalledFunctionName(callExpr *ast.CallExpr) string {
	switch fun := callExpr.Fun.(type) {
	case *ast.Ident:
		// Simple function call: funcName()
		return fun.Name
	case *ast.SelectorExpr:
		// Method call or package function: obj.method() or pkg.func()
		if ident, ok := fun.X.(*ast.Ident); ok {
			return fmt.Sprintf("%s.%s", ident.Name, fun.Sel.Name)
		}
		// For more complex selectors, just use the method name
		return fun.Sel.Name
	case *ast.FuncLit:
		// Anonymous function call
		return "<anonymous>"
	default:
		// Other complex expressions
		return "<complex>"
	}
}

// isDynamicCall determines if a call is dynamic (through interface, function pointer, etc.)
func (cge *CallGraphExtractor) isDynamicCall(callExpr *ast.CallExpr) bool {
	switch callExpr.Fun.(type) {
	case *ast.Ident:
		// Could be a function variable
		return false // For now, assume direct calls
	case *ast.SelectorExpr:
		// Could be interface method call
		return false // For now, assume direct calls
	case *ast.IndexExpr:
		// Function from slice/map
		return true
	case *ast.ParenExpr:
		// Parenthesized expression, likely dynamic
		return true
	default:
		return true
	}
}

// findTargetFunction searches for the target function in the database
func (cge *CallGraphExtractor) findTargetFunction(ctx context.Context, functionName string) string {
	// Try different search strategies
	queries := []struct {
		cypher string
		params map[string]any
	}{
		{
			// Exact name match in same package
			cypher: `
				MATCH (f:Function)
				WHERE f.name = $funcName AND f.packageName = $packageName
				RETURN f.id AS id
				LIMIT 1
			`,
			params: map[string]any{
				"funcName":    functionName,
				"packageName": cge.packageName,
			},
		},
		{
			// Exact name match anywhere
			cypher: `
				MATCH (f:Function)
				WHERE f.name = $funcName
				RETURN f.id AS id
				LIMIT 1
			`,
			params: map[string]any{
				"funcName": functionName,
			},
		},
		{
			// Method name match (for receiver.method patterns)
			cypher: `
				MATCH (f:Function)
				WHERE f.name CONTAINS $methodName
				RETURN f.id AS id
				LIMIT 1
			`,
			params: map[string]any{
				"methodName": strings.Split(functionName, ".")[len(strings.Split(functionName, "."))-1],
			},
		},
	}

	for _, query := range queries {
		results, err := cge.client.ExecuteQuery(ctx, query.cypher, query.params)
		if err != nil {
			continue
		}

		for _, record := range results {
			if id, ok := record.AsMap()["id"]; ok {
				if idStr, ok := id.(string); ok {
					return idStr
				}
			}
		}
	}

	return "" // Not found
}

// createPlaceholderFunction creates a placeholder function node for external calls
func (cge *CallGraphExtractor) createPlaceholderFunction(ctx context.Context, functionName string) string {
	// Check if placeholder already exists
	existing := cge.findTargetFunction(ctx, functionName)
	if existing != "" {
		return existing
	}

	// Create a new placeholder function
	funcProps := map[string]any{
		"name":         functionName,
		"isExternal":   true,
		"isPlaceholder": true,
		"packageName":  cge.inferPackageName(functionName),
		"filePath":     "<external>",
		"signature":    fmt.Sprintf("%s(...)", functionName),
	}

	funcID, err := cge.client.MergeNode(ctx, []string{"Function"},
		map[string]any{"name": functionName, "isPlaceholder": true}, funcProps)
	if err != nil {
		log.Printf("Warning: failed to create placeholder function for %s: %v", functionName, err)
		return ""
	}

	return funcID
}

// inferPackageName tries to infer package name from function name
func (cge *CallGraphExtractor) inferPackageName(functionName string) string {
	parts := strings.Split(functionName, ".")
	if len(parts) > 1 {
		// If it's pkg.func or obj.method, use the first part
		return parts[0]
	}
	return "unknown"
}

// getCurrentFunctionName extracts the current function name from its ID
func (cge *CallGraphExtractor) getCurrentFunctionName(funcID string) string {
	// This would need to be implemented based on how function IDs are structured
	// For now, return empty to disable recursion detection
	return ""
}

// ExtractDataFlows analyzes data flow between functions (simplified version)
func (cge *CallGraphExtractor) ExtractDataFlows(ctx context.Context, funcDecl *ast.FuncDecl, funcID string) error {
	if funcDecl.Body == nil {
		return nil
	}

	// Simple data flow analysis: track variable assignments and returns
	var returnVars []string
	var assignedVars []string

	ast.Inspect(funcDecl.Body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.ReturnStmt:
			// Track return statements
			for _, result := range node.Results {
				if ident, ok := result.(*ast.Ident); ok {
					returnVars = append(returnVars, ident.Name)
				}
			}
		case *ast.AssignStmt:
			// Track assignments
			for _, lhs := range node.Lhs {
				if ident, ok := lhs.(*ast.Ident); ok {
					assignedVars = append(assignedVars, ident.Name)
				}
			}
		}
		return true
	})

	// Create data flow relationships based on variable usage
	// This is a simplified version - a full implementation would track
	// actual data dependencies between function calls
	for _, varName := range returnVars {
		// Find functions that use this variable (simplified)
		cge.createDataFlowRelationship(ctx, funcID, varName, "return")
	}

	return nil
}

// createDataFlowRelationship is a placeholder for future data-flow analysis.
// FLOWS_TO and NEXT_EXECUTION are excluded from the call-graph pipeline (see
// noiseRelTypes in scip_indexer.go); do not write them here.
func (cge *CallGraphExtractor) createDataFlowRelationship(ctx context.Context, sourceID, varName, flowType string) {
	log.Printf("Data flow: %s returns %s (%s)", sourceID, varName, flowType)
}