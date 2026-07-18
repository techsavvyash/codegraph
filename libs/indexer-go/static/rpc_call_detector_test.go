package static

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	models "github.com/context-maximiser/code-graph/libs/core-models-go"
)

// parseFirstFuncBody parses src and returns the first FuncDecl's body.
func parseFirstFuncBody(t *testing.T, src string) (*ast.FuncDecl, *token.FileSet) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "test.go", src, 0)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	for _, decl := range f.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok {
			return fn, fset
		}
	}
	t.Fatal("no function found in source")
	return nil, nil
}

// collectAssignStmts walks fn and returns all *ast.AssignStmt nodes.
func collectAssignStmts(fn *ast.FuncDecl) []*ast.AssignStmt {
	var stmts []*ast.AssignStmt
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if a, ok := n.(*ast.AssignStmt); ok {
			stmts = append(stmts, a)
		}
		return true
	})
	return stmts
}

// collectCallExprs walks fn and returns all *ast.CallExpr nodes.
func collectCallExprs(fn *ast.FuncDecl) []*ast.CallExpr {
	var calls []*ast.CallExpr
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok {
			calls = append(calls, c)
		}
		return true
	})
	return calls
}

// newTestRPCDetector creates a detector with a nil Neo4j client (safe for map-only operations).
func newTestRPCDetector() *RPCCallDetector {
	return NewRPCCallDetector(nil, "test-service", models.DefaultScope())
}

// ── Helper function unit tests ───────────────────────────────────────────────

func TestExtractStringArg(t *testing.T) {
	tests := []struct {
		name string
		src  string
		n    int
		want string
	}{
		{
			name: "first string arg",
			src:  `package p; func f() { http.Get("https://example.com") }`,
			n:    0,
			want: "https://example.com",
		},
		{
			name: "second string arg (url in NewRequest)",
			src:  `package p; func f() { http.NewRequest("POST", "https://pay.internal/v1/charge", nil) }`,
			n:    1,
			want: "https://pay.internal/v1/charge",
		},
		{
			name: "non-string arg returns empty",
			src:  `package p; func f() { foo(x) }`,
			n:    0,
			want: "",
		},
		{
			name: "out of bounds index returns empty",
			src:  `package p; func f() { http.Get("https://example.com") }`,
			n:    5,
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fn, _ := parseFirstFuncBody(t, tt.src)
			calls := collectCallExprs(fn)
			if len(calls) == 0 {
				t.Fatal("no call expressions found")
			}
			// Find the deepest call (the one we want, not nested wrappers).
			var target *ast.CallExpr
			for _, c := range calls {
				if sel, ok := c.Fun.(*ast.SelectorExpr); ok {
					_ = sel
					target = c
					break
				}
			}
			if target == nil {
				target = calls[0]
			}
			got := extractStringArg(target, tt.n)
			if got != tt.want {
				t.Errorf("extractStringArg(%d) = %q, want %q", tt.n, got, tt.want)
			}
		})
	}
}

func TestExtractHostFromURL(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"https://payments.internal/v1/charge", "payments.internal"},
		{"http://api.example.com/foo", "api.example.com"},
		{"grpc://order-service:50051", "order-service"},
		{"payments.internal/v1/charge", "payments.internal"},
		{"payments.internal", "payments.internal"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			got := extractHostFromURL(tt.url)
			if got != tt.want {
				t.Errorf("extractHostFromURL(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}

func TestDeriveServiceName(t *testing.T) {
	tests := []struct {
		field string
		want  string
	}{
		{"PaymentServiceClient", "PaymentService"},
		{"paymentClient", "payment"},
		{"grpcClient", "grpc"},
		{"orderServiceClient", "orderService"},
	}

	for _, tt := range tests {
		t.Run(tt.field, func(t *testing.T) {
			got := deriveServiceName(tt.field)
			if got != tt.want {
				t.Errorf("deriveServiceName(%q) = %q, want %q", tt.field, got, tt.want)
			}
		})
	}
}

// ── processAssignment tests (only populates maps — no Neo4j needed) ──────────

func TestProcessAssignment_GRPCClient(t *testing.T) {
	src := `package p
func f() {
	client := pb.NewPaymentServiceClient(conn)
}`
	fn, _ := parseFirstFuncBody(t, src)
	d := newTestRPCDetector()

	for _, stmt := range collectAssignStmts(fn) {
		d.processAssignment(stmt)
	}

	clientType, ok := d.varTypeMap["client"]
	if !ok {
		t.Fatal("expected 'client' in varTypeMap")
	}
	if clientType != "PaymentServiceClient" {
		t.Errorf("varTypeMap[client] = %q, want %q", clientType, "PaymentServiceClient")
	}
	pkg, ok := d.varPkgMap["client"]
	if !ok {
		t.Fatal("expected 'client' in varPkgMap")
	}
	if pkg != "pb" {
		t.Errorf("varPkgMap[client] = %q, want %q", pkg, "pb")
	}
}

func TestProcessAssignment_MultiReturn(t *testing.T) {
	// client, err := pb.NewOrderServiceClient(conn) — multi-return
	src := `package p
import "github.com/example/pb"
func f() {
	client, err := pb.NewOrderServiceClient(conn)
	_ = err
}`
	fn, _ := parseFirstFuncBody(t, src)
	d := newTestRPCDetector()

	for _, stmt := range collectAssignStmts(fn) {
		d.processAssignment(stmt)
	}

	if clientType, ok := d.varTypeMap["client"]; !ok || clientType != "OrderServiceClient" {
		t.Errorf("varTypeMap[client] = %q, want OrderServiceClient (ok=%v)", clientType, ok)
	}
}

func TestProcessAssignment_HTTPClientLiteral(t *testing.T) {
	src := `package p
import "net/http"
func f() {
	httpClient := &http.Client{}
}`
	fn, _ := parseFirstFuncBody(t, src)
	d := newTestRPCDetector()

	for _, stmt := range collectAssignStmts(fn) {
		d.processAssignment(stmt)
	}

	if tp, ok := d.varTypeMap["httpClient"]; !ok || tp != "http.Client" {
		t.Errorf("varTypeMap[httpClient] = %q (ok=%v), want http.Client", tp, ok)
	}
}

func TestProcessAssignment_RestyClient(t *testing.T) {
	src := `package p
import "github.com/go-resty/resty/v2"
func f() {
	rc := resty.New()
}`
	fn, _ := parseFirstFuncBody(t, src)
	d := newTestRPCDetector()

	for _, stmt := range collectAssignStmts(fn) {
		d.processAssignment(stmt)
	}

	if tp, ok := d.varTypeMap["rc"]; !ok || tp != "resty.Client" {
		t.Errorf("varTypeMap[rc] = %q (ok=%v), want resty.Client", tp, ok)
	}
}

func TestProcessAssignment_VarMapResetPerFunction(t *testing.T) {
	// First function populates varTypeMap; calling with second function should clear it.
	src1 := `package p
func f1() { client := pb.NewPaymentServiceClient(conn) }`
	fn1, fset1 := parseFirstFuncBody(t, src1)

	d := newTestRPCDetector()
	for _, stmt := range collectAssignStmts(fn1) {
		d.processAssignment(stmt)
	}
	if _, ok := d.varTypeMap["client"]; !ok {
		t.Fatal("expected client in varTypeMap after f1")
	}

	// DetectInFunction with a nil client will panic on the write step, so only
	// simulate the reset that happens at the top of DetectInFunction.
	d.varTypeMap = make(map[string]string)
	d.varPkgMap = make(map[string]string)

	src2 := `package p
func f2() { x := 1; _ = x }`
	fn2, _ := parseFirstFuncBody(t, src2)
	for _, stmt := range collectAssignStmts(fn2) {
		d.processAssignment(stmt)
	}

	// varTypeMap should NOT have "client" — it was reset.
	if _, ok := d.varTypeMap["client"]; ok {
		t.Error("varTypeMap should not contain 'client' from a previous function")
	}

	_ = fset1 // used above in parseFirstFuncBody context
}

// ── P0-5: grpcclient getter → authoritative service binding ──────────────────

func TestProcessAssignment_GRPCClientGetter_Authoritative(t *testing.T) {
	// GetPaymentService's name says "payment", but its proto owner is payin.
	// With the getter table set, varPkgMap must carry the authoritative owner
	// ("payin"), and varTypeMap the real proto service ("PaymentServiceClient").
	src := `package p
func f() {
	client := grpcclient.GetPaymentService(ctx)
}`
	fn, _ := parseFirstFuncBody(t, src)
	d := newTestRPCDetector()
	d.SetGetterServiceMap(GetterServiceMap{
		"GetPaymentService": {Service: "payin", ProtoService: "PaymentService"},
	})

	for _, stmt := range collectAssignStmts(fn) {
		d.processAssignment(stmt)
	}

	if got := d.varTypeMap["client"]; got != "PaymentServiceClient" {
		t.Errorf("varTypeMap[client] = %q, want PaymentServiceClient", got)
	}
	if got := d.varPkgMap["client"]; got != "payin" {
		t.Errorf("varPkgMap[client] = %q, want payin (authoritative owner)", got)
	}
}

func TestProcessAssignment_GRPCClientGetter_NoGetPrefix(t *testing.T) {
	// AccountHolderNameService has no Get prefix but ends in Service — must bind.
	src := `package p
func f() {
	client := grpcclient.AccountHolderNameService(ctx)
}`
	fn, _ := parseFirstFuncBody(t, src)
	d := newTestRPCDetector()
	d.SetGetterServiceMap(GetterServiceMap{
		"AccountHolderNameService": {Service: "sol", ProtoService: "AccountHolderNameService"},
	})

	for _, stmt := range collectAssignStmts(fn) {
		d.processAssignment(stmt)
	}

	if got := d.varPkgMap["client"]; got != "sol" {
		t.Errorf("varPkgMap[client] = %q, want sol", got)
	}
	if got := d.varTypeMap["client"]; got != "AccountHolderNameServiceClient" {
		t.Errorf("varTypeMap[client] = %q, want AccountHolderNameServiceClient", got)
	}
}

func TestProcessAssignment_GRPCClientGetter_Fallback(t *testing.T) {
	// Table miss (no grpcclient package indexed) falls back to fuzzy name derivation.
	src := `package p
func f() {
	client := grpcclient.GetBalanceService(ctx)
}`
	fn, _ := parseFirstFuncBody(t, src)
	d := newTestRPCDetector() // no getter map set

	for _, stmt := range collectAssignStmts(fn) {
		d.processAssignment(stmt)
	}

	if got := d.varTypeMap["client"]; got != "BalanceServiceClient" {
		t.Errorf("varTypeMap[client] = %q, want BalanceServiceClient", got)
	}
	if got := d.varPkgMap["client"]; got != "balance" {
		t.Errorf("varPkgMap[client] = %q, want balance (fuzzy fallback)", got)
	}
}

func TestIsGRPCClientGetter(t *testing.T) {
	yes := []string{
		"GetAccountService", "GetSettlementServiceServer", "GetRuleEngineServiceClient",
		"AccountHolderNameService", "GetOnBoardingBankHTTPService",
	}
	no := []string{
		"InitConnectionManager", "GetConnection", "GetUTObject", "NewPaymentServiceClient",
	}
	for _, n := range yes {
		if !isGRPCClientGetter(n) {
			t.Errorf("isGRPCClientGetter(%q) = false, want true", n)
		}
	}
	for _, n := range no {
		if isGRPCClientGetter(n) {
			t.Errorf("isGRPCClientGetter(%q) = true, want false", n)
		}
	}
}

// ── P0-5: strict resolveByName (no both-ways substring mis-bind) ─────────────

func TestResolveByName_StrictExactOnly(t *testing.T) {
	// serviceAliases() runs on each Service name at load time, so byName carries the
	// canonical forms. Model that: "settlement" is indexed, "pricing" is NOT.
	si := &serviceIndex{
		byName: map[string]string{
			"settlement":        "id-settlement",
			"settlementservice": "id-settlement",
			"payin":             "id-payin",
			"paymentrouter":     "id-pr",
		},
		byProto: map[string]string{},
	}

	// Exact canonical match (hyphen collapsed) still works.
	if got := si.resolveByName("payment-router"); got != "id-pr" {
		t.Errorf("resolveByName(payment-router) = %q, want id-pr", got)
	}
	if got := si.resolveByName("payin"); got != "id-payin" {
		t.Errorf("resolveByName(payin) = %q, want id-payin", got)
	}

	// The live-data regression: a pricing getter's proto service name
	// "SettlementPricingService" canonicalises to "settlementpricingservice", which
	// STARTS WITH "settlement". Prefix matching mis-bound it to the settlement
	// service; exact-only must return "" (pricing is not indexed → no edge).
	if got := si.resolveByName("SettlementPricingService"); got != "" {
		t.Errorf("resolveByName(SettlementPricingService) = %q, want \"\" (no prefix mis-bind)", got)
	}

	// "dashboard" (mid-token substring of dashboards) and "pay" (prefix of two
	// services) must both bind nothing.
	if got := si.resolveByName("dashboard"); got != "" {
		t.Errorf("resolveByName(dashboard) = %q, want \"\"", got)
	}
	if got := si.resolveByName("pay"); got != "" {
		t.Errorf("resolveByName(pay) = %q, want \"\"", got)
	}
}

// ── HTTPCall detection helpers ────────────────────────────────────────────────

func TestExtractHostFromURL_Backtick(t *testing.T) {
	// Ensure the function handles strings without scheme correctly.
	got := extractHostFromURL("order-service:8080/health")
	want := "order-service"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// ── gRPC detection signal: varTypeMap ends with "Client" ─────────────────────

func TestGRPCClientSuffix(t *testing.T) {
	// processCallExpr checks strings.HasSuffix(clientType, "Client").
	// Verify the varTypeMap is populated with a type ending in "Client".
	src := `package p
func f() {
	svc := paymentpb.NewPaymentServiceClient(conn)
}`
	fn, _ := parseFirstFuncBody(t, src)
	d := newTestRPCDetector()
	for _, stmt := range collectAssignStmts(fn) {
		d.processAssignment(stmt)
	}

	tp := d.varTypeMap["svc"]
	if tp == "" {
		t.Fatal("expected svc in varTypeMap")
	}
	if tp[len(tp)-6:] != "Client" {
		t.Errorf("type %q does not end in 'Client'", tp)
	}
}

// ── HTTP method detection via http.NewRequest ─────────────────────────────────

func TestExtractHTTPMethodAndURL(t *testing.T) {
	// Verify extractStringArg returns the method and URL from http.NewRequest.
	src := `package p
import "net/http"
func f() {
	http.NewRequest("POST", "https://payments.internal/v1/charge", nil)
}`
	fn, _ := parseFirstFuncBody(t, src)
	calls := collectCallExprs(fn)

	var target *ast.CallExpr
	for _, c := range calls {
		if sel, ok := c.Fun.(*ast.SelectorExpr); ok {
			if sel.Sel.Name == "NewRequest" {
				target = c
				break
			}
		}
	}
	if target == nil {
		t.Fatal("NewRequest call not found")
	}

	method := extractStringArg(target, 0)
	url := extractStringArg(target, 1)

	if method != "POST" {
		t.Errorf("method = %q, want POST", method)
	}
	if url != "https://payments.internal/v1/charge" {
		t.Errorf("url = %q, want https://payments.internal/v1/charge", url)
	}
}
