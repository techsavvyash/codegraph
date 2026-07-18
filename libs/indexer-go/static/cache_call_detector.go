package static

import (
	"go/ast"
	"go/token"
	"maps"
	"time"

	models "github.com/context-maximiser/code-graph/libs/core-models-go"
)

// cacheImportPath is the framework Redis client package. Cache detection matches
// by IMPORT PATH (not the local alias token) so that other packages a service may
// alias as "cache" (error-description caches, etc.) are never misclassified.
const cacheImportPath = "github.com/tazapay/grpc-framework/client/cache"

// cacheOp describes one Redis wrapper function at the cache boundary.
type cacheOp struct {
	Operation   string // READ | WRITE | DELETE | INCR | EXPIRE | LOCK | UNLOCK
	KeyArgIndex int    // 0-indexed position of the cache-key arg in call.Args; -1 = none
}

// cacheCallRegistry is the ALLOWLIST of per-key Redis data operations exported by
// grpc-framework/client/cache. All these take (ctx, key, ...), so the key is at
// arg index 1.
//
// DO NOT add SetRedis / GetRedisMutex / NewPipeline / InitRedis / IsInitialized /
// SetMockMutex — their absence is the guardrail: they are client injection, mutex
// and pipeline constructors, and init/test helpers, not per-key cache operations.
var cacheCallRegistry = map[string]cacheOp{
	"Get":         {"READ", 1},
	"HMGet":       {"READ", 1},
	"Set":         {"WRITE", 1},
	"SetNX":       {"WRITE", 1},
	"Del":         {"DELETE", 1},
	"Incr":        {"INCR", 1},
	"Expire":      {"EXPIRE", 1},
	"AcquireLock": {"LOCK", 1},
	"ReleaseLock": {"UNLOCK", 1},
}

// CacheCallDetector detects outbound Redis/cache call sites (cache.Get/Set/Del…)
// and writes CacheCall nodes with CALLS_CACHE edges. It mirrors DBCallDetector:
// one node per call site, linked from the caller function. Stateless across files;
// an import map must be built per file before calling DetectInFunction.
type CacheCallDetector struct {
	serviceName string
	scopeCtx    models.ScopeContext
	callBuffer  *callNodeBuffer
}

// NewCacheCallDetector creates a detector scoped to a single service indexing run.
func NewCacheCallDetector(serviceName string, scopeCtx models.ScopeContext) *CacheCallDetector {
	return &CacheCallDetector{
		serviceName: serviceName,
		scopeCtx:    scopeCtx,
	}
}

// SetCallNodeBuffer configures the shared call node buffer.
func (d *CacheCallDetector) SetCallNodeBuffer(buf *callNodeBuffer) {
	d.callBuffer = buf
}

// DetectInFunction walks funcDecl for cache wrapper calls and writes CacheCall
// nodes plus CALLS_CACHE edges into the shared call buffer.
func (d *CacheCallDetector) DetectInFunction(
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
		if importMap[pkgIdent.Name] != cacheImportPath {
			return true
		}
		op, ok := cacheCallRegistry[sel.Sel.Name]
		if !ok {
			return true
		}

		line := fset.Position(callExpr.Pos()).Line
		ccKey := models.CacheCallNodeKey(d.serviceName, filePath, op.Operation, line)

		// Best-effort cache-key source expression (usually fmt.Sprintf(constants.X, …)
		// or a constant); recorded for readability, not used for logic.
		var keyExpr string
		if op.KeyArgIndex >= 0 && op.KeyArgIndex < len(callExpr.Args) {
			keyExpr = exprString(callExpr.Args[op.KeyArgIndex])
		}

		now := time.Now().UTC().Unix()
		ccProps := map[string]any{
			"nodeKey":     ccKey,
			"name":        sel.Sel.Name,
			"operation":   op.Operation,
			"provider":    "redis",
			"serviceName": d.serviceName,
			"filePath":    filePath,
			"line":        line,
			"createdAt":   now,
			"updatedAt":   now,
		}
		if keyExpr != "" {
			ccProps["keyExpr"] = keyExpr
		}
		maps.Copy(ccProps, d.scopeCtx.Props())

		d.callBuffer.addCacheCall(ccKey, ccProps)
		d.callBuffer.addCallsCacheEdge(callerFuncID, ccKey, map[string]any{"line": line})

		return true
	})
}
