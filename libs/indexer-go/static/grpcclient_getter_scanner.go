package static

import (
	"go/ast"
	goparser "go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// GetterInfo is the authoritative binding resolved from a single grpcclient getter.
//
//   - Service      canonical owning microservice (e.g. "payin"), from the proto
//     import path of the getter's return type.
//   - ProtoService the proto service name (e.g. "BalanceService"), from the return
//     type name with its Client/Server suffix stripped. Used for handler
//     receiver-type matching in the cross-service resolver.
type GetterInfo struct {
	Service      string
	ProtoService string
}

// GetterServiceMap maps a grpcclient getter function name (e.g. "GetPaymentService")
// to its authoritative binding. This replaces the fuzzy getter-name-token derivation
// (P0-5): a getter's *name* frequently disagrees with the microservice that actually
// owns the proto it wraps —
//
//	GetPaymentService   → payin   (not "payment")
//	GetCollectService   → payin   (not "collect")
//	GetCPNService       → bfi     (not "cpn")
//	GetRBACService      → account (not "rbac")
//	GetOnboardingKYBService → onboarding (not "kyb")
//	GetSOLBalanceService → sol, proto service BalanceService (not "solbalance")
//
// The map is built per-caller-service: each service's own grpcclient package declares
// which proto client every getter wraps, so the table is authoritative for that
// caller's call sites.
type GetterServiceMap map[string]GetterInfo

// protoServiceSegment extracts the owning-service segment from a proto import path.
// Tazapay generated-code convention:
//
//	github.com/tazapay/proto/gen/go/<service>/(grpc|http)/vN
//
// (the workspace CLAUDE.md documents the contract layout as proto/<svc>/grpc/v1;
// gen/go/ is the generated-code root under it). The captured segment is the service
// directory directly under gen/go/.
var protoServiceSegment = regexp.MustCompile(`/proto/gen/go/([a-z0-9-]+)/(?:grpc|http)/v\d+`)

// grpcClientGetterName matches the getter shapes P0-5 requires. The Get prefix is
// optional when the name already ends in a Service* suffix, and the accepted
// suffixes are Service, ServiceServer, ServiceClient — covering the getters the
// old Get*+*Service shape missed (GetSettlementServiceServer,
// GetRuleEngineServiceClient, AccountHolderNameService).
var grpcClientGetterName = regexp.MustCompile(`^(?:Get)?[A-Za-z0-9]*Service(?:Server|Client)?$`)

// ScanGRPCClientGetters parses <projectPath>/grpcclient/*.go and returns a map of
// getter function name → authoritative owning-service binding, resolved from the
// proto import path of each getter's return type.
//
// grpc_ut.go (unit-test scaffolding, see P2-3) and *_test.go files are excluded so
// the table reflects production wiring only. Non-fatal throughout: a missing dir,
// unparseable file, or unresolvable getter is skipped and an empty map is returned
// rather than an error.
func ScanGRPCClientGetters(projectPath string) (GetterServiceMap, error) {
	result := make(GetterServiceMap)

	dir := filepath.Join(projectPath, "grpcclient")
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return result, nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return result, err
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		// grpc_ut.go is unit-test scaffolding (P2-3), not production wiring.
		if entry.Name() == "grpc_ut.go" || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		scanGRPCClientFile(filepath.Join(dir, entry.Name()), result)
	}

	return result, nil
}

// scanGRPCClientFile parses a single grpcclient/*.go file and adds its getter
// bindings to m. Best-effort: parse errors and unresolvable getters are skipped.
func scanGRPCClientFile(path string, m GetterServiceMap) {
	fset := token.NewFileSet()
	f, err := goparser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return
	}

	// Build import alias → import path. Proto packages here are always aliased, but
	// fall back to the Go default alias (last path segment) for unaliased imports.
	importPaths := make(map[string]string)
	for _, imp := range f.Imports {
		p := strings.Trim(imp.Path.Value, `"`)
		alias := ""
		if imp.Name != nil {
			alias = imp.Name.Name
		} else if idx := strings.LastIndex(p, "/"); idx >= 0 {
			alias = p[idx+1:]
		} else {
			alias = p
		}
		if alias != "" {
			importPaths[alias] = p
		}
	}

	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil { // package-level funcs only
			continue
		}
		if !isGRPCClientGetter(fn.Name.Name) {
			continue
		}

		// The declared return type is only an interface the concrete client satisfies.
		// In the proxy idiom the two disagree —
		//
		//	func GetPayoutServiceClient(ctx) (mpdashboardhttpv3.PayoutProxyServiceClient, error) {
		//	    return settlementhttpv3.NewPayoutServiceClient(conn), nil
		//	}
		//
		// — and the constructor names the generated client actually put on the wire,
		// so the call targets settlement, not the proxy's own proto. Prefer the body's
		// New<X>Client constructor; fall back to the declared type when the body has
		// none (or it isn't a proto client).
		alias, typeName := constructedClientPkgType(fn)
		if alias == "" || !resolvesToProto(alias, importPaths) {
			alias, typeName = firstResultPkgType(fn)
		}
		if alias == "" {
			continue
		}
		importPath, ok := importPaths[alias]
		if !ok {
			continue
		}
		mm := protoServiceSegment.FindStringSubmatch(importPath)
		if len(mm) < 2 {
			continue
		}

		m[fn.Name.Name] = GetterInfo{
			Service:      canonicalServiceKey(mm[1]), // "payment-router" → "paymentrouter"
			ProtoService: protoServiceFromType(typeName),
		}
	}
}

// resolvesToProto reports whether alias maps to a generated-proto import path.
func resolvesToProto(alias string, importPaths map[string]string) bool {
	p, ok := importPaths[alias]
	return ok && protoServiceSegment.MatchString(p)
}

// constructedClientPkgType returns the package alias and client type of the proto
// client the getter's body actually constructs, from a `return pkg.New<X>Client(…)`
// statement (the value may also be assigned and returned via a variable-free single
// return — only direct constructor returns are recognised, which is the Tazapay
// grpcclient house style). Returns ("","") when no such constructor is found.
func constructedClientPkgType(fn *ast.FuncDecl) (alias, typeName string) {
	if fn.Body == nil {
		return "", ""
	}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if alias != "" {
			return false
		}
		ret, ok := n.(*ast.ReturnStmt)
		if !ok || len(ret.Results) == 0 {
			return true
		}
		call, ok := ret.Results[0].(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkgIdent, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		name := sel.Sel.Name
		if !strings.HasPrefix(name, "New") || !strings.HasSuffix(name, "Client") {
			return true
		}
		alias = pkgIdent.Name
		typeName = strings.TrimPrefix(name, "New")
		return false
	})
	return alias, typeName
}

// firstResultPkgType returns the package alias and type name of a getter's first
// return value, for a return type of shape `pkg.TypeName` (optionally a pointer).
// Returns ("", "") when the first result is not a selector expression (e.g. a bare
// or builtin type).
func firstResultPkgType(fn *ast.FuncDecl) (alias, typeName string) {
	if fn.Type.Results == nil || len(fn.Type.Results.List) == 0 {
		return "", ""
	}
	expr := fn.Type.Results.List[0].Type
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return "", ""
	}
	pkgIdent, ok := sel.X.(*ast.Ident)
	if !ok {
		return "", ""
	}
	return pkgIdent.Name, sel.Sel.Name
}

// protoServiceFromType strips the client/server suffix from a proto client type name
// to recover the proto service name: "BalanceServiceClient" → "BalanceService".
func protoServiceFromType(typeName string) string {
	typeName = strings.TrimSuffix(typeName, "Client")
	typeName = strings.TrimSuffix(typeName, "Server")
	return typeName
}
