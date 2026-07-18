package static

import (
	"fmt"
	"go/ast"
	"go/token"
	"maps"
	"strings"
	"time"

	models "github.com/context-maximiser/code-graph/libs/core-models-go"
)

// apiClientImportPath is the framework outbound-HTTP package. All third-party /
// PSP egress HTTP in Tazapay services goes through it. Detection matches by
// IMPORT PATH (not the local alias token — the fleet uses `apiclient`,
// unaliased `api`, and `httpclient`) so unrelated packages sharing an alias are
// never misclassified.
const apiClientImportPath = "github.com/tazapay/grpc-framework/client/api"

// apiClientVerb describes one HTTP verb exported by client/api, both as a
// package-level helper and as a method on *api.Client (identical signatures).
type apiClientVerb struct {
	Method         string // fixed HTTP method; "" = read it from MethodArgIndex (Do)
	URLArgIndex    int    // 0-indexed position of the url arg in call.Args
	MethodArgIndex int    // position of the method arg; -1 = fixed method
}

// apiClientVerbRegistry is the ALLOWLIST of request-sending verbs in client/api.
// All take (ctx, url, ...) except Do, which takes (ctx, method, url, ...).
//
// DO NOT add JSON / XML / Form / Bytes / Multipart / Raw (body constructors),
// New / Default / SetHTTP / BasicAuthHeader, or the With* options — their
// absence is the guardrail: they build request inputs, they do not send HTTP.
var apiClientVerbRegistry = map[string]apiClientVerb{
	"Get":    {Method: "GET", URLArgIndex: 1, MethodArgIndex: -1},
	"Post":   {Method: "POST", URLArgIndex: 1, MethodArgIndex: -1},
	"Put":    {Method: "PUT", URLArgIndex: 1, MethodArgIndex: -1},
	"Patch":  {Method: "PATCH", URLArgIndex: 1, MethodArgIndex: -1},
	"Delete": {Method: "DELETE", URLArgIndex: 1, MethodArgIndex: -1},
	"Stream": {Method: "GET", URLArgIndex: 1, MethodArgIndex: -1},
	"Do":     {Method: "", URLArgIndex: 2, MethodArgIndex: 1},
}

// netHTTPMethodConsts maps net/http method constant names to their values, so a
// `Do(ctx, http.MethodPost, url, …)` call resolves without type information.
var netHTTPMethodConsts = map[string]string{
	"MethodGet": "GET", "MethodPost": "POST", "MethodPut": "PUT",
	"MethodPatch": "PATCH", "MethodDelete": "DELETE", "MethodHead": "HEAD",
	"MethodOptions": "OPTIONS",
}

// APIClientCallDetector detects outbound HTTP call sites made through
// grpc-framework/client/api and writes HTTPCall nodes with CALLS_API (and
// best-effort CALLS_SERVICE) edges. It covers the shapes observed in the fleet:
//
//	apiclient.Post(ctx, url, body, …)                 // package-level verb
//	c := apiclient.New(…); c.Post(ctx, url, …)        // client instance
//	apiclient.New(…).Get(ctx, url)                    // chained construction
//	func f(c *apiclient.Client) { c.Get(ctx, url) }   // client parameter
//	c, err := j.newMTLSClient(ctx); c.Post(ctx, url)  // same-file helper returning *api.Client
//	var pkgClient = apiclient.New(…)                  // package-level client var
//
// BeginFile must be called once per file (with that file's import map) before
// DetectInFunction runs on the file's functions.
type APIClientCallDetector struct {
	serviceName string
	scopeCtx    models.ScopeContext
	callBuffer  *callNodeBuffer
	constRes    *constResolver
	serviceIdx  *serviceIndex

	// fileClientFuncs: bare names of functions/methods declared in the current
	// file whose results include *api.Client (e.g. "newMTLSClient"). Reset per file.
	fileClientFuncs map[string]bool
	// fileClientVars: package-level var names in the current file bound to an
	// api client (var c = apiclient.New(…) or var c *apiclient.Client). Reset per file.
	fileClientVars map[string]bool
}

// NewAPIClientCallDetector creates a detector scoped to a single service indexing run.
func NewAPIClientCallDetector(serviceName string, scopeCtx models.ScopeContext) *APIClientCallDetector {
	return &APIClientCallDetector{
		serviceName:     serviceName,
		scopeCtx:        scopeCtx,
		fileClientFuncs: map[string]bool{},
		fileClientVars:  map[string]bool{},
	}
}

// SetCallNodeBuffer configures the shared call node buffer.
func (d *APIClientCallDetector) SetCallNodeBuffer(buf *callNodeBuffer) {
	d.callBuffer = buf
}

// SetConstResolver configures the repo-wide constant resolver used to resolve
// URL arguments like constants.SlackWebhookURL.
func (d *APIClientCallDetector) SetConstResolver(r *constResolver) {
	d.constRes = r
}

// SetServiceIndex configures an optional preloaded service index so internal
// URLs can resolve to a CALLS_SERVICE edge.
func (d *APIClientCallDetector) SetServiceIndex(idx *serviceIndex) {
	d.serviceIdx = idx
}

// BeginFile collects file-level api-client facts: helper functions returning
// *api.Client and package-level client vars. Must run before DetectInFunction
// for any function in the file.
func (d *APIClientCallDetector) BeginFile(f *ast.File, importMap map[string]string) {
	d.fileClientFuncs = map[string]bool{}
	d.fileClientVars = map[string]bool{}
	if f == nil {
		return
	}

	for _, decl := range f.Decls {
		switch dd := decl.(type) {
		case *ast.FuncDecl:
			if dd.Type.Results == nil {
				continue
			}
			for _, res := range dd.Type.Results.List {
				if isAPIClientType(res.Type, importMap) {
					d.fileClientFuncs[dd.Name.Name] = true
					break
				}
			}
		case *ast.GenDecl:
			if dd.Tok != token.VAR {
				continue
			}
			for _, spec := range dd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				bound := vs.Type != nil && isAPIClientType(vs.Type, importMap)
				if !bound {
					for _, v := range vs.Values {
						if isAPIClientNewCall(v, importMap) {
							bound = true
							break
						}
					}
				}
				if bound {
					for _, name := range vs.Names {
						d.fileClientVars[name.Name] = true
					}
				}
			}
		}
	}
}

// DetectInFunction walks funcDecl for client/api HTTP calls and writes HTTPCall
// nodes plus CALLS_API edges into the shared call buffer.
func (d *APIClientCallDetector) DetectInFunction(
	funcDecl *ast.FuncDecl,
	callerFuncID string,
	importMap map[string]string,
	filePath string,
	fset *token.FileSet,
) {
	if d.callBuffer == nil || funcDecl.Body == nil {
		return
	}
	if isNoisyFilePath(filePath) {
		return
	}

	// Invariant: per-function state, never reused across function boundaries.
	localClients := map[string]bool{}
	localStrings := map[string]string{}

	// Bind parameters typed *api.Client (or api.Client) — a type fact, not a name heuristic.
	if funcDecl.Type.Params != nil {
		for _, param := range funcDecl.Type.Params.List {
			if isAPIClientType(param.Type, importMap) {
				for _, name := range param.Names {
					localClients[name.Name] = true
				}
			}
		}
	}

	// Pass 1 — collect client-var bindings and statically-resolvable string vars.
	// Must complete before pass 2 so every var is resolved before its call sites.
	ast.Inspect(funcDecl.Body, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok || len(assign.Lhs) == 0 || len(assign.Rhs) == 0 {
			return true
		}
		lhsIdent, ok := assign.Lhs[0].(*ast.Ident)
		if !ok {
			return true
		}
		rhs := assign.Rhs[0]

		// c := apiclient.New(…)  |  c, err := j.newMTLSClient(ctx)  |  c, err := NewCpnClient(ctx)
		if isAPIClientNewCall(rhs, importMap) || d.isFileClientFuncCall(rhs) {
			localClients[lhsIdent.Name] = true
			return true
		}

		// url := constants.Base + "/v1/x" — keep statically-resolvable strings so
		// URL args passed as local vars still resolve.
		if d.constRes != nil && len(assign.Lhs) == 1 && len(assign.Rhs) == 1 {
			if val, static := d.constRes.ResolveStringWithBindings(rhs, localStrings); static && val != "" {
				localStrings[lhsIdent.Name] = val
			}
		}
		return true
	})

	// Pass 2 — detect verb calls and write nodes/edges.
	ast.Inspect(funcDecl.Body, func(n ast.Node) bool {
		callExpr, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := callExpr.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		verb, ok := apiClientVerbRegistry[sel.Sel.Name]
		if !ok {
			return true
		}
		if !d.isAPIClientReceiver(sel.X, importMap, localClients) {
			return true
		}

		line := fset.Position(callExpr.Pos()).Line
		method := verb.Method
		if method == "" {
			method = resolveHTTPMethodArg(callExpr, verb.MethodArgIndex)
		}
		url, urlExpr := d.resolveURLArg(callExpr, verb.URLArgIndex, localStrings)

		d.writeHTTPCall(callerFuncID, filePath, method, url, urlExpr, line)
		return true
	})
}

// isAPIClientReceiver reports whether the receiver expression of a verb call is
// the client/api package itself, a tracked client variable, or a chained
// constructor/helper call returning a client.
func (d *APIClientCallDetector) isAPIClientReceiver(
	recv ast.Expr,
	importMap map[string]string,
	localClients map[string]bool,
) bool {
	switch r := recv.(type) {
	case *ast.Ident:
		// Package-level verb (apiclient.Post) or tracked client var (c.Post).
		return importMap[r.Name] == apiClientImportPath ||
			localClients[r.Name] || d.fileClientVars[r.Name]
	case *ast.CallExpr:
		// Chained: apiclient.New(…).Get(…) or j.newMTLSClient(ctx).Post(…).
		return isAPIClientNewCall(r, importMap) || d.isFileClientFuncCall(r)
	}
	return false
}

// isFileClientFuncCall reports whether expr calls a function/method declared in
// the current file whose results include *api.Client.
func (d *APIClientCallDetector) isFileClientFuncCall(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		return d.fileClientFuncs[fun.Name]
	case *ast.SelectorExpr:
		return d.fileClientFuncs[fun.Sel.Name]
	}
	return false
}

// resolveURLArg resolves the URL argument to a static string where possible:
// string literal, repo constant (constants.X), or a local var bound to a static
// string. Falls back to ("dynamic", <best-effort expr>) — never skips the call.
func (d *APIClientCallDetector) resolveURLArg(
	callExpr *ast.CallExpr,
	argIdx int,
	localStrings map[string]string,
) (url, urlExpr string) {
	if argIdx < 0 || argIdx >= len(callExpr.Args) {
		return "dynamic", ""
	}
	arg := callExpr.Args[argIdx]

	if lit := extractStringArg(callExpr, argIdx); lit != "" {
		return lit, ""
	}
	if d.constRes != nil {
		if val, static := d.constRes.ResolveStringWithBindings(arg, localStrings); static && val != "" {
			return val, ""
		}
	}
	if expr := exprString(arg); expr != "?" {
		return "dynamic", expr
	}
	return "dynamic", ""
}

// writeHTTPCall buffers the HTTPCall node, its CALLS_API edge, and a
// best-effort CALLS_SERVICE edge for resolvable internal URLs.
func (d *APIClientCallDetector) writeHTTPCall(
	callerFuncID, filePath, method, url, urlExpr string,
	line int,
) {
	nodeKey := fmt.Sprintf("httpcall:%s:%s:%s:%s:%d", d.serviceName, filePath, method, url, line)

	now := time.Now().UTC().Unix()
	setProps := map[string]any{
		"nodeKey":       nodeKey,
		"callerService": d.serviceName,
		"targetService": "",
		"url":           url,
		"method":        method,
		"wrapper":       "grpc-framework/client/api",
		"filePath":      filePath,
		"line":          line,
		"createdAt":     now,
		"updatedAt":     now,
	}
	if urlExpr != "" {
		setProps["urlExpr"] = urlExpr
	}
	maps.Copy(setProps, d.scopeCtx.Props())

	d.callBuffer.addHTTPCall(nodeKey, setProps)
	d.callBuffer.addCallsAPIEdge(callerFuncID, nodeKey, map[string]any{"line": line})

	if url != "dynamic" && d.serviceIdx != nil {
		if targetID := d.serviceIdx.resolveFromURL(url); targetID != "" {
			d.callBuffer.addCallsServiceEdge(nodeKey, targetID, map[string]any{"protocol": "http"})
		}
	}
}

// resolveHTTPMethodArg extracts the HTTP method from a Do(ctx, method, url, …)
// call: a string literal or a net/http Method* constant. Falls back to "ANY".
func resolveHTTPMethodArg(callExpr *ast.CallExpr, argIdx int) string {
	if argIdx < 0 || argIdx >= len(callExpr.Args) {
		return "ANY"
	}
	if lit := extractStringArg(callExpr, argIdx); lit != "" {
		return strings.ToUpper(lit)
	}
	if sel, ok := callExpr.Args[argIdx].(*ast.SelectorExpr); ok {
		if m, known := netHTTPMethodConsts[sel.Sel.Name]; known {
			return m
		}
	}
	return "ANY"
}

// isAPIClientType reports whether a type expression denotes api.Client or
// *api.Client for the client/api import path.
func isAPIClientType(expr ast.Expr, importMap map[string]string) bool {
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Client" {
		return false
	}
	pkgIdent, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	return importMap[pkgIdent.Name] == apiClientImportPath
}

// isAPIClientNewCall reports whether expr constructs a client via client/api —
// New(…) or Default(). Both return *Client, so a client-var bound to either (or
// a verb chained directly onto either) is an api-client call site.
func isAPIClientNewCall(expr ast.Expr, importMap map[string]string) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || (sel.Sel.Name != "New" && sel.Sel.Name != "Default") {
		return false
	}
	pkgIdent, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	return importMap[pkgIdent.Name] == apiClientImportPath
}
