package static

import (
	"context"
	"fmt"
	"go/ast"
	"go/token"
	"log"
	"maps"
	"strings"
	"time"

	models "github.com/context-maximiser/code-graph/libs/core-models-go"
	"github.com/context-maximiser/code-graph/libs/neo4j-go"
)

// httpClientTypes are type names that indicate an *http.Client variable.
var httpClientTypes = map[string]bool{
	"http.Client": true,
	"Client":      true, // when imported as `import "net/http"`, the type is just Client
}

// RPCCallDetector detects outbound gRPC call sites in Go AST and writes
// GRPCCall nodes with CALLS_API and CALLS_SERVICE edges into Neo4j.
type RPCCallDetector struct {
	client      *neo4j.Client
	serviceName string
	scopeCtx    models.ScopeContext
	fset        *token.FileSet
	serviceIdx  *serviceIndex
	callBuffer  *callNodeBuffer

	// varTypeMap: local var name → gRPC client type name (e.g. "PaymentServiceClient").
	// Reset per function — never reused across function boundaries.
	varTypeMap map[string]string
	// varPkgMap: local var name → package alias used in the constructor call (e.g. "pb").
	varPkgMap map[string]string

	// getterServices: authoritative grpcclient getter → owning-service binding
	// (from ScanGRPCClientGetters). Detector-scoped — set once, survives the
	// per-function reset of varTypeMap/varPkgMap. Empty when the caller has no
	// grpcclient package.
	getterServices GetterServiceMap
}

// NewRPCCallDetector creates a detector scoped to a single service indexing run.
func NewRPCCallDetector(client *neo4j.Client, serviceName string, scopeCtx models.ScopeContext) *RPCCallDetector {
	return &RPCCallDetector{
		client:      client,
		serviceName: serviceName,
		scopeCtx:    scopeCtx,
		varTypeMap:  make(map[string]string),
		varPkgMap:   make(map[string]string),
	}
}

// SetServiceIndex configures an optional preloaded service index for O(1) resolution.
func (d *RPCCallDetector) SetServiceIndex(idx *serviceIndex) {
	d.serviceIdx = idx
}

// SetCallNodeBuffer configures an optional shared call node buffer.
func (d *RPCCallDetector) SetCallNodeBuffer(buf *callNodeBuffer) {
	d.callBuffer = buf
}

// SetGetterServiceMap configures the authoritative grpcclient getter → service
// table (from ScanGRPCClientGetters). When set, it overrides the fuzzy getter-name
// token derivation for grpcclient.Get*Service call sites.
func (d *RPCCallDetector) SetGetterServiceMap(m GetterServiceMap) {
	d.getterServices = m
}

// DetectInFunction walks funcDecl looking for outbound gRPC call sites and writes
// GRPCCall nodes plus CALLS_API / CALLS_SERVICE edges for each one found.
func (d *RPCCallDetector) DetectInFunction(
	ctx context.Context,
	funcDecl *ast.FuncDecl,
	callerFuncID, filePath string,
	fset *token.FileSet,
) error {
	if funcDecl.Body == nil {
		return nil
	}
	if isNoisyFilePath(filePath) {
		return nil
	}
	d.fset = fset

	// Invariant: reset per function so assignments from other functions don't pollute.
	d.varTypeMap = make(map[string]string)
	d.varPkgMap = make(map[string]string)

	// Pass 1 — collect variable-to-client-type bindings from assignment statements.
	// This must complete before pass 2 so every var is resolved before its call sites.
	ast.Inspect(funcDecl.Body, func(n ast.Node) bool {
		if assign, ok := n.(*ast.AssignStmt); ok {
			d.processAssignment(assign)
		}
		return true
	})

	// Pass 2 — detect gRPC and HTTP call expressions and write nodes/edges.
	var firstErr error
	ast.Inspect(funcDecl.Body, func(n ast.Node) bool {
		callExpr, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		pos := fset.Position(callExpr.Pos())
		if err := d.processCallExpr(ctx, callExpr, callerFuncID, filePath, pos.Line); err != nil {
			if firstErr == nil {
				firstErr = err
			}
		}
		if err := d.processHTTPCallExpr(ctx, callExpr, callerFuncID, filePath, pos.Line); err != nil {
			if firstErr == nil {
				firstErr = err
			}
		}
		return true
	})

	return firstErr
}

// processAssignment records gRPC New*Client(...) and HTTP client constructor calls
// into varTypeMap / varPkgMap. Handles both single and multi-return assignments.
func (d *RPCCallDetector) processAssignment(assign *ast.AssignStmt) {
	if len(assign.Lhs) == 0 || len(assign.Rhs) == 0 {
		return
	}
	lhsIdent, ok := assign.Lhs[0].(*ast.Ident)
	if !ok {
		return
	}

	// Handle composite literals: client := &http.Client{} or http.Client{}
	rhs := assign.Rhs[0]
	if unary, ok := rhs.(*ast.UnaryExpr); ok {
		rhs = unary.X
	}
	if compLit, ok := rhs.(*ast.CompositeLit); ok {
		if sel, ok := compLit.Type.(*ast.SelectorExpr); ok {
			if pkgIdent, ok := sel.X.(*ast.Ident); ok {
				if pkgIdent.Name == "http" && sel.Sel.Name == "Client" {
					d.varTypeMap[lhsIdent.Name] = "http.Client"
					return
				}
			}
		}
	}

	callExpr, ok := assign.Rhs[0].(*ast.CallExpr)
	if !ok {
		return
	}
	sel, ok := callExpr.Fun.(*ast.SelectorExpr)
	if !ok {
		return
	}
	funcName := sel.Sel.Name

	// gRPC: New*Client constructor
	if strings.HasPrefix(funcName, "New") && strings.HasSuffix(funcName, "Client") {
		// e.g. "NewPaymentServiceClient" → "PaymentServiceClient"
		clientType := strings.TrimPrefix(funcName, "New")
		d.varTypeMap[lhsIdent.Name] = clientType
		if pkgIdent, ok := sel.X.(*ast.Ident); ok {
			d.varPkgMap[lhsIdent.Name] = pkgIdent.Name
		}
		return
	}

	// HTTP: resty.New() → resty client variable
	if pkgIdent, ok := sel.X.(*ast.Ident); ok {
		if pkgIdent.Name == "resty" && funcName == "New" {
			d.varTypeMap[lhsIdent.Name] = "resty.Client"
			return
		}
	}

	// Tazapay grpcclient: client, err := grpcclient.GetAccountService(ctx)
	// grpcclient.GetOnBoardingBankHTTPService(ctx) → "OnBoardingBankHTTPServiceClient"
	// Stored with "Client" suffix so the existing method-call detection in processCallExpr works.
	if pkgIdent, ok := sel.X.(*ast.Ident); ok {
		if pkgIdent.Name == "grpcclient" && isGRPCClientGetter(funcName) {
			// P0-5 — authoritative binding first. The getter table (ScanGRPCClientGetters)
			// resolves getter name → owning service + proto service via the return type's
			// proto import path, which is correct even when the getter's own name lies
			// (GetPaymentService → payin, GetSOLBalanceService → sol/BalanceService).
			if info, known := d.getterServices[funcName]; known && info.Service != "" {
				protoSvc := info.ProtoService
				if protoSvc == "" {
					protoSvc = strings.TrimPrefix(funcName, "Get")
				}
				d.varTypeMap[lhsIdent.Name] = protoSvc + "Client"
				d.varPkgMap[lhsIdent.Name] = info.Service
				return
			}

			// Fallback (table miss — e.g. no grpcclient package, or return type is not a
			// proto path): fuzzy name-token derivation, as before.
			svcName := strings.TrimPrefix(funcName, "Get") // "BalanceService"
			d.varTypeMap[lhsIdent.Name] = svcName + "Client"
			// Store the bare lowercase service name (not "grpcclient") so resolveTargetServiceFull
			// can match it against Service nodes. e.g. "GetBalanceService" → "balance".
			bareName := strings.TrimSuffix(svcName, "Service")
			if bareName == "" {
				bareName = svcName
			}
			d.varPkgMap[lhsIdent.Name] = strings.ToLower(bareName)
			return
		}
	}
}

// isGRPCClientGetter reports whether a grpcclient package function name is a client
// getter. The Get prefix is optional when the name already ends in a Service*
// suffix, and the accepted suffixes are Service, ServiceServer, ServiceClient.
// A leading "New" is rejected: NewXxxServiceClient is a proto constructor (handled
// by the New*Client branch), not a grpcclient getter — Go's regexp lacks negative
// lookahead, so the exclusion is explicit.
func isGRPCClientGetter(funcName string) bool {
	if strings.HasPrefix(funcName, "New") {
		return false
	}
	return grpcClientGetterName.MatchString(funcName)
}

// processCallExpr checks whether this call expression is a gRPC method invocation on a
// tracked client variable (or a struct field whose name ends in "client") and, if so,
// writes a GRPCCall node plus CALLS_API / CALLS_SERVICE edges.
func (d *RPCCallDetector) processCallExpr(
	ctx context.Context,
	callExpr *ast.CallExpr,
	callerFuncID, filePath string,
	line int,
) error {
	sel, ok := callExpr.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}

	var serviceName, protoPackage string
	detected := false

	// Primary path: receiver is a simple local variable tracked in varTypeMap.
	if recv, ok := sel.X.(*ast.Ident); ok {
		if clientType, tracked := d.varTypeMap[recv.Name]; tracked && strings.HasSuffix(clientType, "Client") {
			serviceName = strings.TrimSuffix(clientType, "Client")
			protoPackage = d.varPkgMap[recv.Name]
			detected = true
		}
	}

	// Secondary path: receiver is a struct field access like h.paymentClient.
	// Detected heuristically by checking if the field name ends in "client" (case-insensitive).
	if !detected {
		if fieldSel, ok := sel.X.(*ast.SelectorExpr); ok {
			fieldName := fieldSel.Sel.Name
			if strings.HasSuffix(strings.ToLower(fieldName), "client") {
				// "paymentClient" → "payment", "PaymentServiceClient" → "PaymentService"
				serviceName = deriveServiceName(fieldName)
				detected = true
			}
		}
	}

	if !detected {
		return nil
	}

	methodName := sel.Sel.Name
	targetMethod := fmt.Sprintf("%s.%s", serviceName, methodName)
	nodeKey := fmt.Sprintf("grpccall:%s:%s:%s:%d", d.serviceName, filePath, targetMethod, line)

	// Derive target service name: protoPackage now stores the bare service name (e.g. "balance")
	// set in processAssignment for the grpcclient pattern. Fall back to stripping "Service" suffix
	// from the proto service name (e.g. "BalanceService" → "balance").
	targetServiceName := protoPackage
	if targetServiceName == "" || targetServiceName == "grpcclient" {
		bare := strings.TrimSuffix(serviceName, "Service")
		if bare == "" {
			bare = serviceName
		}
		targetServiceName = strings.ToLower(bare)
	}

	mergeProps := map[string]any{"nodeKey": nodeKey}
	setProps := map[string]any{
		"nodeKey":       nodeKey,
		"callerService": d.serviceName,
		"targetService": targetServiceName,
		"targetMethod":  targetMethod,
		"protoPackage":  protoPackage,
		"protoService":  serviceName,
		"protoMethod":   methodName,
		"filePath":      filePath,
		"line":          line,
		"createdAt":     time.Now().UTC().Unix(),
		"updatedAt":     time.Now().UTC().Unix(),
	}
	maps.Copy(setProps, d.scopeCtx.Props())

	if d.callBuffer != nil {
		d.callBuffer.addGRPCCall(nodeKey, setProps)
		d.callBuffer.addCallsAPIEdge(callerFuncID, nodeKey, map[string]any{"line": line})

		targetServiceID := d.resolveTargetServiceFull(ctx, serviceName, protoPackage)
		if targetServiceID != "" {
			d.callBuffer.addCallsServiceEdge(nodeKey, targetServiceID, map[string]any{"protocol": "grpc"})
		}
		return nil
	}

	grpcCallID, err := d.client.MergeNode(ctx, []string{"GRPCCall"}, mergeProps, setProps)
	if err != nil {
		return fmt.Errorf("grpc call detector: merge GRPCCall node %q: %w", targetMethod, err)
	}

	// CALLS_API: callerFunction → GRPCCall
	if _, err := d.client.MergeRelationship(ctx,
		callerFuncID, grpcCallID,
		string(models.CallsAPIRel),
		map[string]any{},
		map[string]any{"line": line},
	); err != nil {
		log.Printf("Warning: grpc detector: CALLS_API edge for %s: %v", targetMethod, err)
	}

	// Attempt service resolution and write CALLS_SERVICE if found.
	targetServiceID := d.resolveTargetServiceFull(ctx, serviceName, protoPackage)
	if targetServiceID != "" {
		if _, err := d.client.MergeRelationship(ctx,
			grpcCallID, targetServiceID,
			string(models.CallsServiceRel),
			map[string]any{},
			map[string]any{"protocol": "grpc"},
		); err != nil {
			log.Printf("Warning: grpc detector: CALLS_SERVICE edge for %s: %v", targetMethod, err)
		}
	}

	return nil
}

// resolveTargetService queries Neo4j for a Service whose name fuzzy-matches serviceName.
// Returns the element ID or empty string if not found. Best-effort: callers handle empty.
func (d *RPCCallDetector) resolveTargetService(ctx context.Context, serviceName string) string {
	if d.serviceIdx != nil {
		if id := d.serviceIdx.resolveByName(serviceName); id != "" {
			return id
		}
	}

	// Strict (P0-5): exact name only. Neither the old both-ways CONTAINS nor a prefix
	// match is safe — "settlementpricingservice" STARTS WITH "settlement" would
	// mis-bind a pricing getter to the settlement service. This fallback is dormant
	// when a serviceIndex is set (the pipeline always sets one); exact keeps it safe
	// for the defensive nil-index path.
	cypher := `
		MATCH (s:Service)
		WHERE toLower(s.name) = toLower($name)
		RETURN elementId(s) AS id
		LIMIT 1
	`
	results, err := d.client.ExecuteQuery(ctx, cypher, map[string]any{"name": serviceName})
	if err != nil || len(results) == 0 {
		return ""
	}
	id, _ := results[0].AsMap()["id"].(string)
	return id
}

// resolveTargetServiceFull tries proto-aware resolution first (via serviceIdx.resolveByProto),
// then falls back to resolveTargetService name-based lookup.
func (d *RPCCallDetector) resolveTargetServiceFull(ctx context.Context, serviceName, protoPackage string) string {
	if d.serviceIdx != nil && protoPackage != "" {
		if id := d.serviceIdx.resolveByProto(protoPackage); id != "" {
			return id
		}
	}
	return d.resolveTargetService(ctx, serviceName)
}

// deriveServiceName strips common client suffixes to produce a bare service name.
// "PaymentServiceClient" → "PaymentService", "paymentClient" → "payment"
func deriveServiceName(fieldName string) string {
	if idx := strings.LastIndex(strings.ToLower(fieldName), "client"); idx > 0 {
		return fieldName[:idx]
	}
	return fieldName
}

// processHTTPCallExpr checks whether this call expression is an outbound HTTP call
// (http.Get, http.Post, http.NewRequest, client.Do, resty) and, if so, writes an
// HTTPCall node plus CALLS_API / CALLS_SERVICE edges.
func (d *RPCCallDetector) processHTTPCallExpr(
	ctx context.Context,
	callExpr *ast.CallExpr,
	callerFuncID, filePath string,
	line int,
) error {
	sel, ok := callExpr.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}

	var httpMethod, url string
	detected := false

	// Pattern 1: http.Get(url), http.Post(url,...), http.NewRequest(method, url, body), etc.
	if pkgIdent, ok := sel.X.(*ast.Ident); ok {
		switch pkgIdent.Name {
		case "http":
			switch sel.Sel.Name {
			case "Get":
				httpMethod = "GET"
				url = extractStringArg(callExpr, 0)
				detected = true
			case "Post", "PostForm":
				httpMethod = "POST"
				url = extractStringArg(callExpr, 0)
				detected = true
			case "Head":
				httpMethod = "HEAD"
				url = extractStringArg(callExpr, 0)
				detected = true
			case "NewRequest":
				httpMethod = extractStringArg(callExpr, 0)
				if httpMethod == "" {
					httpMethod = "ANY"
				}
				url = extractStringArg(callExpr, 1)
				detected = true
			case "NewRequestWithContext":
				// signature: NewRequestWithContext(ctx, method, url, body)
				httpMethod = extractStringArg(callExpr, 1)
				if httpMethod == "" {
					httpMethod = "ANY"
				}
				url = extractStringArg(callExpr, 2)
				detected = true
			}
		}
	}

	// Pattern 2: client.Do(req) where client type is tracked as http.Client or resty.Client.
	if !detected {
		if recv, ok := sel.X.(*ast.Ident); ok {
			if clientType, tracked := d.varTypeMap[recv.Name]; tracked {
				if httpClientTypes[clientType] || clientType == "resty.Client" {
					if sel.Sel.Name == "Do" || sel.Sel.Name == "Execute" {
						httpMethod = "ANY"
						url = "dynamic"
						detected = true
					}
				}
			}
		}
	}

	// Pattern 3: struct field access like h.httpClient.Do(req)
	if !detected {
		if fieldSel, ok := sel.X.(*ast.SelectorExpr); ok {
			fieldLower := strings.ToLower(fieldSel.Sel.Name)
			if strings.Contains(fieldLower, "httpclient") || strings.Contains(fieldLower, "http_client") {
				if sel.Sel.Name == "Do" || sel.Sel.Name == "Execute" {
					httpMethod = "ANY"
					url = "dynamic"
					detected = true
				}
			}
		}
	}

	if !detected {
		return nil
	}

	if url == "" {
		url = "dynamic"
	}

	nodeKey := fmt.Sprintf("httpcall:%s:%s:%s:%s:%d", d.serviceName, filePath, httpMethod, url, line)

	mergeProps := map[string]any{"nodeKey": nodeKey}
	setProps := map[string]any{
		"nodeKey":       nodeKey,
		"callerService": d.serviceName,
		"targetService": "",
		"url":           url,
		"method":        httpMethod,
		"filePath":      filePath,
		"line":          line,
		"createdAt":     time.Now().UTC().Unix(),
		"updatedAt":     time.Now().UTC().Unix(),
	}
	maps.Copy(setProps, d.scopeCtx.Props())

	if d.callBuffer != nil {
		d.callBuffer.addHTTPCall(nodeKey, setProps)
		d.callBuffer.addCallsAPIEdge(callerFuncID, nodeKey, map[string]any{"line": line})
		if url != "dynamic" {
			targetServiceID := d.resolveTargetServiceFromURL(ctx, url)
			if targetServiceID != "" {
				d.callBuffer.addCallsServiceEdge(nodeKey, targetServiceID, map[string]any{"protocol": "http"})
			}
		}
		return nil
	}

	httpCallID, err := d.client.MergeNode(ctx, []string{"HTTPCall"}, mergeProps, setProps)
	if err != nil {
		return fmt.Errorf("http call detector: merge HTTPCall node: %w", err)
	}

	// CALLS_API: callerFunction → HTTPCall
	if _, err := d.client.MergeRelationship(ctx,
		callerFuncID, httpCallID,
		string(models.CallsAPIRel),
		map[string]any{},
		map[string]any{"line": line},
	); err != nil {
		log.Printf("Warning: http detector: CALLS_API edge: %v", err)
	}

	// Attempt service resolution from URL (best-effort).
	if url != "dynamic" {
		targetServiceID := d.resolveTargetServiceFromURL(ctx, url)
		if targetServiceID != "" {
			if _, err := d.client.MergeRelationship(ctx,
				httpCallID, targetServiceID,
				string(models.CallsServiceRel),
				map[string]any{},
				map[string]any{"protocol": "http"},
			); err != nil {
				log.Printf("Warning: http detector: CALLS_SERVICE edge: %v", err)
			}
		}
	}

	return nil
}

// resolveTargetServiceFromURL queries Neo4j for a Service whose baseURL or name
// matches the host extracted from the given URL. Returns element ID or "".
func (d *RPCCallDetector) resolveTargetServiceFromURL(ctx context.Context, url string) string {
	if d.serviceIdx != nil {
		if id := d.serviceIdx.resolveFromURL(url); id != "" {
			return id
		}
	}

	host := extractHostFromURL(url)
	if host == "" {
		return ""
	}
	cypher := `
		MATCH (s:Service)
		WHERE (s.baseURL IS NOT NULL AND s.baseURL CONTAINS $host)
		   OR toLower(s.name) CONTAINS toLower($host)
		RETURN elementId(s) AS id
		LIMIT 1
	`
	results, err := d.client.ExecuteQuery(ctx, cypher, map[string]any{"host": host})
	if err != nil || len(results) == 0 {
		return ""
	}
	id, _ := results[0].AsMap()["id"].(string)
	return id
}

// extractStringArg returns the unquoted string literal value of the nth call argument,
// or "" if the argument is absent or not a string literal.
func extractStringArg(callExpr *ast.CallExpr, n int) string {
	if n >= len(callExpr.Args) {
		return ""
	}
	lit, ok := callExpr.Args[n].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return ""
	}
	val := lit.Value
	if len(val) >= 2 && (val[0] == '"' || val[0] == '`') {
		return val[1 : len(val)-1]
	}
	return val
}

// extractHostFromURL extracts the hostname portion from a URL string.
// "https://payments.internal/v1/charge" → "payments.internal"
func extractHostFromURL(rawURL string) string {
	for _, prefix := range []string{"https://", "http://", "grpc://"} {
		if strings.HasPrefix(rawURL, prefix) {
			rawURL = rawURL[len(prefix):]
			break
		}
	}
	for i, c := range rawURL {
		if c == '/' || c == ':' {
			return rawURL[:i]
		}
	}
	return rawURL
}
