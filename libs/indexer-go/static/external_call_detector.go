package static

import (
	"go/ast"
	"go/token"
	"maps"
	"path/filepath"
	"strings"
	"time"

	models "github.com/context-maximiser/code-graph/libs/core-models-go"
)

// mfaFiles and mfaFuncPrefixes identify code paths that touch MFA flows so that
// ExternalCall nodes emitted from them are tagged flowTag="mfa".
var mfaFiles = map[string]bool{
	"mfa_authentication.go": true,
	"auth_challenge.go":     true,
}

var mfaFuncPrefixes = []string{
	"MFA", "Mfa", "VerifyMFA", "performCustomAuth",
	"updatePhoneNumber", "GetAccessToken", "getPrivateCognito",
	"GetUsernameFromCognito",
}

// ExternalCallDetector detects outbound calls to external managed services
// (AWS Cognito, SES, etc.) by matching wrapper import paths against the registry.
// It is stateless across files; an import map must be built per file before calling
// DetectInFunction.
type ExternalCallDetector struct {
	serviceName string
	scopeCtx    models.ScopeContext
	callBuffer  *callNodeBuffer
}

// NewExternalCallDetector creates a detector scoped to a single service indexing run.
func NewExternalCallDetector(serviceName string, scopeCtx models.ScopeContext) *ExternalCallDetector {
	return &ExternalCallDetector{
		serviceName: serviceName,
		scopeCtx:    scopeCtx,
	}
}

// SetCallNodeBuffer configures the shared call node buffer.
func (d *ExternalCallDetector) SetCallNodeBuffer(buf *callNodeBuffer) {
	d.callBuffer = buf
}

// DetectInFunction walks funcDecl for external service wrapper calls and writes
// ExternalCall + ExternalService nodes with CALLS_API and USES_SERVICE edges into
// the shared call buffer.
func (d *ExternalCallDetector) DetectInFunction(
	funcDecl *ast.FuncDecl,
	callerFuncID string,
	importMap map[string]string,
	filePath string,
	fset *token.FileSet,
) {
	if d.callBuffer == nil || funcDecl.Body == nil {
		return
	}

	flowTag := detectFlowTag(funcDecl.Name.Name, filePath)

	ast.Inspect(funcDecl.Body, func(n ast.Node) bool {
		callExpr, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := callExpr.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkgIdent, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		importPath, ok := importMap[pkgIdent.Name]
		if !ok {
			return true
		}
		fnTable, ok := externalCallRegistry[importPath]
		if !ok {
			return true
		}
		op, ok := fnTable[sel.Sel.Name]
		if !ok {
			return true
		}

		line := fset.Position(callExpr.Pos()).Line
		ecKey := models.ExternalCallNodeKey(d.serviceName, filePath, op.Provider, op.Service, op.Operation, line)
		esKey := models.ExternalServiceNodeKey(op.Provider, op.Service)

		now := time.Now().UTC().Unix()

		ecProps := map[string]any{
			"nodeKey":         ecKey,
			"name":            d.serviceName + ":" + op.Service + "." + op.Operation,
			"provider":        op.Provider,
			"externalService": op.Service,
			"operation":       op.Operation,
			"variant":         op.Variant,
			"wrapperFunc":     op.WrapperFunc,
			"callerService":   d.serviceName,
			"filePath":        filePath,
			"line":            line,
			"createdAt":       now,
			"updatedAt":       now,
		}
		if flowTag != "" {
			ecProps["flowTag"] = flowTag
		}
		maps.Copy(ecProps, d.scopeCtx.Props())

		esProps := map[string]any{
			"nodeKey":     esKey,
			"name":        op.Service,
			"provider":    op.Provider,
			"category":    op.Category,
			"displayName": op.DisplayName,
			"createdAt":   now,
			"updatedAt":   now,
		}
		maps.Copy(esProps, d.scopeCtx.Props())

		d.callBuffer.addExternalCall(ecKey, ecProps)
		d.callBuffer.addExternalServiceNode(esKey, esProps)
		d.callBuffer.addCallsAPIEdge(callerFuncID, ecKey, map[string]any{"line": line})
		d.callBuffer.addUsesServiceEdge(ecKey, esKey, map[string]any{
			"operation":   op.Operation,
			"variant":     op.Variant,
			"wrapperFunc": op.WrapperFunc,
		})

		return true
	})
}

// detectFlowTag returns "mfa" when the call site is inside an MFA-related function
// or file, otherwise "".
func detectFlowTag(funcName, filePath string) string {
	base := filepath.Base(filePath)
	if mfaFiles[base] {
		return "mfa"
	}
	for _, prefix := range mfaFuncPrefixes {
		if strings.HasPrefix(funcName, prefix) {
			return "mfa"
		}
	}
	return ""
}
