package static

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	models "github.com/context-maximiser/code-graph/libs/core-models-go"
)

// parseFileForCache parses src into (file, firstFuncDecl, fset) for cache-detector tests.
func parseFileForCache(t *testing.T, src string) (*ast.File, *ast.FuncDecl, *token.FileSet) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "test.go", src, 0)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	for _, decl := range f.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok {
			return f, fn, fset
		}
	}
	t.Fatal("no function found in source")
	return nil, nil, nil
}

// runCacheDetector wires a detector to a fresh buffer and runs it over the first func.
func runCacheDetector(t *testing.T, src string) *callNodeBuffer {
	t.Helper()
	f, fn, fset := parseFileForCache(t, src)
	buf := newCallNodeBuffer(models.DefaultScope().ScopeID)
	d := NewCacheCallDetector("test-service", models.DefaultScope())
	d.SetCallNodeBuffer(buf)
	d.DetectInFunction(fn, "caller-elem-id", buildImportMap(f), "utils/mfa.go", fset)
	return buf
}

func TestCacheDetector_DetectsRedisOps(t *testing.T) {
	src := `package p
import (
	"context"
	"github.com/tazapay/grpc-framework/client/cache"
)
func CheckMFASession(ctx context.Context, key string) {
	_ = cache.Get(ctx, key)
	_ = cache.Set(ctx, key, "v", 0)
	_ = cache.Del(ctx, key)
	_ = cache.Incr(ctx, key)
	_ = cache.Expire(ctx, key, 0)
}`
	buf := runCacheDetector(t, src)

	if len(buf.cacheCalls) != 5 {
		t.Fatalf("expected 5 CacheCall nodes, got %d", len(buf.cacheCalls))
	}
	if len(buf.callsCache) != 5 {
		t.Fatalf("expected 5 CALLS_CACHE edges, got %d", len(buf.callsCache))
	}

	// Verify operations are classified correctly, keyed by wrapper name.
	wantOps := map[string]string{
		"Get": "READ", "Set": "WRITE", "Del": "DELETE", "Incr": "INCR", "Expire": "EXPIRE",
	}
	seen := map[string]string{}
	for _, props := range buf.cacheCalls {
		seen[props["name"].(string)] = props["operation"].(string)
		if props["provider"] != "redis" {
			t.Errorf("expected provider=redis, got %v", props["provider"])
		}
		if props["serviceName"] != "test-service" {
			t.Errorf("expected serviceName=test-service, got %v", props["serviceName"])
		}
	}
	for name, op := range wantOps {
		if seen[name] != op {
			t.Errorf("op for %s = %q, want %q", name, seen[name], op)
		}
	}

	// Every edge must originate from the caller element ID.
	for _, edge := range buf.callsCache {
		if edge.fromID != "caller-elem-id" {
			t.Errorf("edge fromID = %q, want caller-elem-id", edge.fromID)
		}
	}
}

func TestCacheDetector_CapturesKeyExpr(t *testing.T) {
	src := `package p
import (
	"context"
	"github.com/tazapay/grpc-framework/client/cache"
)
func f(ctx context.Context) {
	_ = cache.Get(ctx, buildKey)
}`
	buf := runCacheDetector(t, src)
	if len(buf.cacheCalls) != 1 {
		t.Fatalf("expected 1 CacheCall node, got %d", len(buf.cacheCalls))
	}
	for _, props := range buf.cacheCalls {
		if props["keyExpr"] != "buildKey" {
			t.Errorf("keyExpr = %v, want buildKey", props["keyExpr"])
		}
	}
}

func TestCacheDetector_IgnoresNonDataOps(t *testing.T) {
	// SetRedis / NewPipeline / GetRedisMutex are in the cache package but are NOT
	// per-key data ops — the allowlist must exclude them.
	src := `package p
import (
	"context"
	"github.com/tazapay/grpc-framework/client/cache"
)
func f(ctx context.Context) {
	cache.SetRedis(nil)
	_, _ = cache.NewPipeline(ctx)
	_ = cache.GetRedisMutex("k", 0)
}`
	buf := runCacheDetector(t, src)
	if len(buf.cacheCalls) != 0 {
		t.Fatalf("expected 0 CacheCall nodes for non-data ops, got %d", len(buf.cacheCalls))
	}
}

func TestCacheDetector_ImportPathGuard(t *testing.T) {
	// A DIFFERENT package aliased as "cache" must never be classified as Redis.
	src := `package p
import (
	"context"
	cache "github.com/tazapay/some-service/internal/errorcache"
)
func f(ctx context.Context) {
	_ = cache.Get(ctx, "k")
}`
	buf := runCacheDetector(t, src)
	if len(buf.cacheCalls) != 0 {
		t.Fatalf("expected 0 CacheCall nodes for non-framework cache pkg, got %d", len(buf.cacheCalls))
	}
}

func TestCacheCallRegistry_Allowlist(t *testing.T) {
	must := []string{"Get", "Set", "Del", "Incr", "Expire", "SetNX", "HMGet", "AcquireLock", "ReleaseLock"}
	for _, m := range must {
		if _, ok := cacheCallRegistry[m]; !ok {
			t.Errorf("cacheCallRegistry missing expected op %q", m)
		}
	}
	mustNot := []string{"SetRedis", "NewPipeline", "GetRedisMutex", "InitRedis", "IsInitialized", "SetMockMutex"}
	for _, m := range mustNot {
		if _, ok := cacheCallRegistry[m]; ok {
			t.Errorf("cacheCallRegistry must NOT contain non-data op %q", m)
		}
	}
}
