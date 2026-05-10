package models

import (
	"testing"
)

const (
	svcA = "codegraph/apps/cli"
	svcB = "codegraph/apps/mcp-server-go"
)

func TestServiceNodeKey(t *testing.T) {
	key := ServiceNodeKey("codegraph")
	if key != "svc:codegraph" {
		t.Errorf("expected svc:codegraph, got %s", key)
	}
	// Deterministic
	if ServiceNodeKey("codegraph") != ServiceNodeKey("codegraph") {
		t.Error("ServiceNodeKey is not deterministic")
	}
}

func TestFileNodeKey(t *testing.T) {
	key := FileNodeKey(svcA, "pkg/models/node.go")
	expected := "file:codegraph/apps/cli:pkg/models/node.go"
	if key != expected {
		t.Errorf("expected %s, got %s", expected, key)
	}
}

func TestSymbolNodeKey(t *testing.T) {
	scip := "scip-go go github.com/example/pkg Method#"
	key := SymbolNodeKey(scip)
	if key != scip {
		t.Errorf("expected raw SCIP symbol, got %s", key)
	}
}

func TestFunctionNodeKey(t *testing.T) {
	key := FunctionNodeKey(svcA, "pkg/neo4j/client.go", "MergeNode(...)")
	expected := "func:codegraph/apps/cli:pkg/neo4j/client.go#MergeNode(...)"
	if key != expected {
		t.Errorf("expected %s, got %s", expected, key)
	}
}

func TestMethodNodeKey(t *testing.T) {
	key := MethodNodeKey(svcA, "pkg/neo4j/client.go", "(*Client).Close()")
	expected := "method:codegraph/apps/cli:pkg/neo4j/client.go#(*Client).Close()"
	if key != expected {
		t.Errorf("expected %s, got %s", expected, key)
	}
}

func TestClassNodeKey(t *testing.T) {
	// With FQN — service is ignored.
	key := ClassNodeKey("scip-go...TypeSymbol#", svcA, "", "")
	if key != "class:scip-go...TypeSymbol#" {
		t.Errorf("expected class:scip-go...TypeSymbol#, got %s", key)
	}
	// Without FQN — service is part of the key.
	key = ClassNodeKey("", svcA, "pkg/models/node.go", "BaseNode")
	expected := "class:codegraph/apps/cli:pkg/models/node.go#BaseNode"
	if key != expected {
		t.Errorf("expected %s, got %s", expected, key)
	}
}

func TestInterfaceNodeKey(t *testing.T) {
	key := InterfaceNodeKey("io.Reader", svcA, "", "")
	if key != "iface:io.Reader" {
		t.Errorf("expected iface:io.Reader, got %s", key)
	}
	key = InterfaceNodeKey("", svcA, "pkg/foo.go", "Handler")
	expected := "iface:codegraph/apps/cli:pkg/foo.go#Handler"
	if key != expected {
		t.Errorf("expected %s, got %s", expected, key)
	}
}

func TestModuleNodeKey(t *testing.T) {
	key := ModuleNodeKey("github.com/example/pkg")
	if key != "mod:github.com/example/pkg" {
		t.Errorf("expected mod:github.com/example/pkg, got %s", key)
	}
}

func TestVariableNodeKey(t *testing.T) {
	key := VariableNodeKey(svcA, "pkg/neo4j/client.go", "driver", 21)
	expected := "var:codegraph/apps/cli:pkg/neo4j/client.go#driver:21"
	if key != expected {
		t.Errorf("expected %s, got %s", expected, key)
	}
}

func TestParameterNodeKey(t *testing.T) {
	key := ParameterNodeKey(svcA, "pkg/neo4j/client.go", "MergeNode(...)", "ctx", 0)
	expected := "param:codegraph/apps/cli:pkg/neo4j/client.go#MergeNode(...):ctx:0"
	if key != expected {
		t.Errorf("expected %s, got %s", expected, key)
	}
}

func TestDocumentNodeKey(t *testing.T) {
	key := DocumentNodeKey("docs/README.md")
	if key != "doc:docs/README.md" {
		t.Errorf("expected doc:docs/README.md, got %s", key)
	}
}

func TestFeatureNodeKey(t *testing.T) {
	key := FeatureNodeKey("SCIP Indexing")
	if key != "feat:SCIP Indexing" {
		t.Errorf("expected feat:SCIP Indexing, got %s", key)
	}
}

func TestAPIRouteNodeKey(t *testing.T) {
	key := APIRouteNodeKey("GET", "/api/users")
	if key != "api:GET:/api/users" {
		t.Errorf("expected api:GET:/api/users, got %s", key)
	}
}

func TestCommentNodeKey(t *testing.T) {
	key := CommentNodeKey(svcA, "pkg/models/node.go", 10)
	expected := "comment:codegraph/apps/cli:pkg/models/node.go:10"
	if key != expected {
		t.Errorf("expected %s, got %s", expected, key)
	}
}

func TestReferenceNodeKey(t *testing.T) {
	key := ReferenceNodeKey(svcA, "pkg/models/node.go", 42, 5)
	expected := "ref:codegraph/apps/cli:pkg/models/node.go:42:5"
	if key != expected {
		t.Errorf("expected %s, got %s", expected, key)
	}
}

func TestFlowNodeKey(t *testing.T) {
	key := FlowNodeKey("api", "api:GET:/api/users")
	expected := "flow:api:api:GET:/api/users"
	if key != expected {
		t.Errorf("expected %s, got %s", expected, key)
	}
	// Different flow types produce different keys
	key2 := FlowNodeKey("consumer", "api:GET:/api/users")
	if key == key2 {
		t.Error("FlowNodeKey should differ for different flow types")
	}
}

func TestPullRequestNodeKey(t *testing.T) {
	key := PullRequestNodeKey("123")
	if key != "pr:123" {
		t.Errorf("expected pr:123, got %s", key)
	}
}

func TestGeneratedDocNodeKey(t *testing.T) {
	key := GeneratedDocNodeKey("pr_summary", "pr:123")
	expected := "gendoc:pr_summary:pr:123"
	if key != expected {
		t.Errorf("expected %s, got %s", expected, key)
	}
	key2 := GeneratedDocNodeKey("flow_summary", "flow:api:api:GET:/users")
	if key == key2 {
		t.Error("GeneratedDocNodeKey should differ for different types/sources")
	}
}

func TestSDKCallNodeKey(t *testing.T) {
	key := SDKCallNodeKey("go-http-client.NewRequestWithContext")
	if key != "sdkcall:go-http-client.NewRequestWithContext" {
		t.Errorf("expected sdkcall:go-http-client.NewRequestWithContext, got %s", key)
	}
	// Deterministic
	if SDKCallNodeKey("axios.get") != SDKCallNodeKey("axios.get") {
		t.Error("SDKCallNodeKey is not deterministic")
	}
}

func TestNodeKeysAreUnique(t *testing.T) {
	keys := map[string]string{
		"service":      ServiceNodeKey("codegraph"),
		"file":         FileNodeKey(svcA, "pkg/models/node.go"),
		"function":     FunctionNodeKey(svcA, "pkg/neo4j/client.go", "MergeNode(...)"),
		"method":       MethodNodeKey(svcA, "pkg/neo4j/client.go", "(*Client).Close()"),
		"class":        ClassNodeKey("scip-go...Type#", svcA, "", ""),
		"interface":    InterfaceNodeKey("io.Reader", svcA, "", ""),
		"module":       ModuleNodeKey("github.com/example/pkg"),
		"variable":     VariableNodeKey(svcA, "pkg/neo4j/client.go", "driver", 21),
		"parameter":    ParameterNodeKey(svcA, "pkg/neo4j/client.go", "MergeNode(...)", "ctx", 0),
		"document":     DocumentNodeKey("docs/README.md"),
		"feature":      FeatureNodeKey("SCIP Indexing"),
		"api":          APIRouteNodeKey("GET", "/api/users"),
		"sdkcall":      SDKCallNodeKey("go-http-client.Get"),
		"comment":      CommentNodeKey(svcA, "pkg/models/node.go", 10),
		"reference":    ReferenceNodeKey(svcA, "pkg/models/node.go", 42, 5),
		"flow":         FlowNodeKey("api", "api:GET:/api/users"),
		"pullrequest":  PullRequestNodeKey("123"),
		"generateddoc": GeneratedDocNodeKey("pr_summary", "pr:123"),
	}

	seen := make(map[string]string)
	for label, key := range keys {
		if key == "" {
			t.Errorf("%s: nodeKey is empty", label)
		}
		if prev, exists := seen[key]; exists {
			t.Errorf("duplicate nodeKey %q: %s and %s", key, prev, label)
		}
		seen[key] = label
	}
}

func TestDocumentChunkNodeKey(t *testing.T) {
	key := DocumentChunkNodeKey("doc:docs/README.md", 0)
	expected := "chunk:doc:docs/README.md#0"
	if key != expected {
		t.Errorf("expected %s, got %s", expected, key)
	}
	key2 := DocumentChunkNodeKey("doc:docs/README.md", 3)
	expected2 := "chunk:doc:docs/README.md#3"
	if key2 != expected2 {
		t.Errorf("expected %s, got %s", expected2, key2)
	}
	// Different indices produce different keys.
	if key == key2 {
		t.Error("DocumentChunkNodeKey should differ for different chunk indices")
	}
}

func TestNodeKeyDeterminism(t *testing.T) {
	for i := 0; i < 100; i++ {
		a := FunctionNodeKey(svcA, "pkg/foo.go", "Bar()")
		b := FunctionNodeKey(svcA, "pkg/foo.go", "Bar()")
		if a != b {
			t.Fatal("FunctionNodeKey is not deterministic")
		}
	}
}

// TestFunctionMethodPrefixIsolation ensures that FunctionNodeKey and MethodNodeKey
// with identical arguments produce different keys due to distinct prefixes.
// This prevents node collisions between free functions and methods.
func TestFunctionMethodPrefixIsolation(t *testing.T) {
	filePath := "pkg/neo4j/client.go"
	signature := "Close()"
	funcKey := FunctionNodeKey(svcA, filePath, signature)
	methKey := MethodNodeKey(svcA, filePath, signature)
	if funcKey == methKey {
		t.Errorf("FunctionNodeKey and MethodNodeKey must differ for same args: both = %q", funcKey)
	}
	if funcKey != "func:"+svcA+":"+filePath+"#"+signature {
		t.Errorf("FunctionNodeKey format wrong: got %q", funcKey)
	}
	if methKey != "method:"+svcA+":"+filePath+"#"+signature {
		t.Errorf("MethodNodeKey format wrong: got %q", methKey)
	}
}

// TestClassNodeKeyFallbackFQNPrecedence verifies that when fqn is non-empty the fqn
// is used and the serviceName/filePath/name arguments are ignored entirely.
func TestClassNodeKeyFallbackFQNPrecedence(t *testing.T) {
	// fqn present: serviceName, filePath, and name must be ignored
	key := ClassNodeKey("scip-go...Foo#", svcA, "pkg/foo.go", "Foo")
	if key != "class:scip-go...Foo#" {
		t.Errorf("expected fqn to win, got %q", key)
	}
	// fqn absent: serviceName+filePath+name used
	key2 := ClassNodeKey("", svcA, "pkg/foo.go", "Foo")
	if key2 != "class:codegraph/apps/cli:pkg/foo.go#Foo" {
		t.Errorf("expected fallback format, got %q", key2)
	}
	// They must differ
	if key == key2 {
		t.Error("ClassNodeKey with fqn must differ from fallback key")
	}
}

// TestInterfaceNodeKeyFallbackFQNPrecedence mirrors TestClassNodeKeyFallbackFQNPrecedence
// for InterfaceNodeKey.
func TestInterfaceNodeKeyFallbackFQNPrecedence(t *testing.T) {
	keyFQN := InterfaceNodeKey("io.Writer", svcA, "", "")
	keyFallback := InterfaceNodeKey("", svcA, "pkg/foo.go", "Writer")
	if keyFQN != "iface:io.Writer" {
		t.Errorf("expected iface:io.Writer, got %q", keyFQN)
	}
	if keyFallback != "iface:codegraph/apps/cli:pkg/foo.go#Writer" {
		t.Errorf("expected iface:codegraph/apps/cli:pkg/foo.go#Writer, got %q", keyFallback)
	}
	if keyFQN == keyFallback {
		t.Error("fqn and fallback InterfaceNodeKey must differ")
	}
}

// TestVariableNodeKeyLineDiscrimination ensures that two variables with the same name
// in the same file at different lines get different keys (line is part of the key).
func TestVariableNodeKeyLineDiscrimination(t *testing.T) {
	k1 := VariableNodeKey(svcA, "pkg/foo.go", "err", 10)
	k2 := VariableNodeKey(svcA, "pkg/foo.go", "err", 20)
	if k1 == k2 {
		t.Error("VariableNodeKey must differ for different start lines")
	}
	if k1 != "var:codegraph/apps/cli:pkg/foo.go#err:10" {
		t.Errorf("unexpected key format: %q", k1)
	}
}

// TestParameterNodeKeyIndexDiscrimination ensures positional parameters with the same
// name at different indices in the same function produce different keys.
func TestParameterNodeKeyIndexDiscrimination(t *testing.T) {
	k0 := ParameterNodeKey(svcA, "pkg/foo.go", "Bar()", "x", 0)
	k1 := ParameterNodeKey(svcA, "pkg/foo.go", "Bar()", "x", 1)
	if k0 == k1 {
		t.Error("ParameterNodeKey must differ for different indices")
	}
	if k0 != "param:codegraph/apps/cli:pkg/foo.go#Bar():x:0" {
		t.Errorf("unexpected key format: %q", k0)
	}
}

// ── Cross-service uniqueness tests (B1 regression suite) ────────────────────
//
// These tests pin the property that a path-based nodeKey produced for one
// service must never equal the same-shape key produced for a different
// service. Before the fix, every pair below collided into a single Neo4j
// node when SCIP-Go emitted module-relative paths.

func TestFileNodeKeyDistinctAcrossServices(t *testing.T) {
	a := FileNodeKey(svcA, "main.go")
	b := FileNodeKey(svcB, "main.go")
	if a == b {
		t.Fatalf("FileNodeKey collided across services: both = %q", a)
	}
}

func TestFunctionNodeKeyDistinctAcrossServices(t *testing.T) {
	a := FunctionNodeKey(svcA, "main.go", "Execute()")
	b := FunctionNodeKey(svcB, "main.go", "Execute()")
	if a == b {
		t.Fatalf("FunctionNodeKey collided across services: both = %q", a)
	}
}

func TestMethodNodeKeyDistinctAcrossServices(t *testing.T) {
	a := MethodNodeKey(svcA, "store.go", "(*Client).Close()")
	b := MethodNodeKey(svcB, "store.go", "(*Client).Close()")
	if a == b {
		t.Fatalf("MethodNodeKey collided across services: both = %q", a)
	}
}

func TestVariableNodeKeyDistinctAcrossServices(t *testing.T) {
	a := VariableNodeKey(svcA, "main.go", "rootCmd", 25)
	b := VariableNodeKey(svcB, "main.go", "rootCmd", 25)
	if a == b {
		t.Fatalf("VariableNodeKey collided across services: both = %q", a)
	}
}

func TestParameterNodeKeyDistinctAcrossServices(t *testing.T) {
	a := ParameterNodeKey(svcA, "main.go", "Execute()", "ctx", 0)
	b := ParameterNodeKey(svcB, "main.go", "Execute()", "ctx", 0)
	if a == b {
		t.Fatalf("ParameterNodeKey collided across services: both = %q", a)
	}
}

func TestCommentNodeKeyDistinctAcrossServices(t *testing.T) {
	a := CommentNodeKey(svcA, "main.go", 12)
	b := CommentNodeKey(svcB, "main.go", 12)
	if a == b {
		t.Fatalf("CommentNodeKey collided across services: both = %q", a)
	}
}

func TestReferenceNodeKeyDistinctAcrossServices(t *testing.T) {
	a := ReferenceNodeKey(svcA, "main.go", 42, 5)
	b := ReferenceNodeKey(svcB, "main.go", 42, 5)
	if a == b {
		t.Fatalf("ReferenceNodeKey collided across services: both = %q", a)
	}
}

func TestClassFallbackNodeKeyDistinctAcrossServices(t *testing.T) {
	a := ClassNodeKey("", svcA, "models.go", "Client")
	b := ClassNodeKey("", svcB, "models.go", "Client")
	if a == b {
		t.Fatalf("ClassNodeKey fallback collided across services: both = %q", a)
	}
}

func TestInterfaceFallbackNodeKeyDistinctAcrossServices(t *testing.T) {
	a := InterfaceNodeKey("", svcA, "models.go", "Store")
	b := InterfaceNodeKey("", svcB, "models.go", "Store")
	if a == b {
		t.Fatalf("InterfaceNodeKey fallback collided across services: both = %q", a)
	}
}

// TestSCIPSymbolBasedKeysIgnoreService confirms that when a SCIP symbol (FQN)
// is provided for Class/Interface, the service argument is NOT mixed in —
// SCIP symbols are already globally unique and re-prefixing them would break
// cross-language joins through SymbolNodeKey.
func TestSCIPSymbolBasedKeysIgnoreService(t *testing.T) {
	a := ClassNodeKey("scip-go go example/pkg Foo#", svcA, "x.go", "X")
	b := ClassNodeKey("scip-go go example/pkg Foo#", svcB, "y.go", "Y")
	if a != b {
		t.Errorf("ClassNodeKey with FQN must be service-independent: %q != %q", a, b)
	}
	a = InterfaceNodeKey("io.Reader", svcA, "x.go", "X")
	b = InterfaceNodeKey("io.Reader", svcB, "y.go", "Y")
	if a != b {
		t.Errorf("InterfaceNodeKey with FQN must be service-independent: %q != %q", a, b)
	}
}

// TestAllNodeKeysDeterministicAcrossReindex verifies that every nodeKey constructor
// produces the same result on repeated calls — simulating re-index stability.
func TestAllNodeKeysDeterministicAcrossReindex(t *testing.T) {
	type call struct {
		name string
		fn   func() string
	}
	calls := []call{
		{"ServiceNodeKey", func() string { return ServiceNodeKey("svc") }},
		{"FileNodeKey", func() string { return FileNodeKey(svcA, "pkg/a.go") }},
		{"SymbolNodeKey", func() string { return SymbolNodeKey("scip-go go example/pkg Fn#") }},
		{"FunctionNodeKey", func() string { return FunctionNodeKey(svcA, "pkg/a.go", "Fn()") }},
		{"MethodNodeKey", func() string { return MethodNodeKey(svcA, "pkg/a.go", "(*T).Fn()") }},
		{"ClassNodeKey-fqn", func() string { return ClassNodeKey("scip-go...T#", svcA, "", "") }},
		{"ClassNodeKey-fallback", func() string { return ClassNodeKey("", svcA, "pkg/a.go", "T") }},
		{"InterfaceNodeKey-fqn", func() string { return InterfaceNodeKey("io.Reader", svcA, "", "") }},
		{"InterfaceNodeKey-fallback", func() string { return InterfaceNodeKey("", svcA, "pkg/a.go", "R") }},
		{"ModuleNodeKey", func() string { return ModuleNodeKey("github.com/example/pkg") }},
		{"VariableNodeKey", func() string { return VariableNodeKey(svcA, "pkg/a.go", "v", 5) }},
		{"ParameterNodeKey", func() string { return ParameterNodeKey(svcA, "pkg/a.go", "Fn()", "p", 0) }},
		{"DocumentNodeKey", func() string { return DocumentNodeKey("docs/x.md") }},
		{"DocumentChunkNodeKey", func() string { return DocumentChunkNodeKey("doc:docs/x.md", 2) }},
		{"FeatureNodeKey", func() string { return FeatureNodeKey("auth") }},
		{"APIRouteNodeKey", func() string { return APIRouteNodeKey("POST", "/login") }},
		{"CommentNodeKey", func() string { return CommentNodeKey(svcA, "pkg/a.go", 7) }},
		{"ReferenceNodeKey", func() string { return ReferenceNodeKey(svcA, "pkg/a.go", 7, 3) }},
		{"FlowNodeKey", func() string { return FlowNodeKey("api", "api:GET:/x") }},
		{"PullRequestNodeKey", func() string { return PullRequestNodeKey("7") }},
		{"GeneratedDocNodeKey", func() string { return GeneratedDocNodeKey("summary", "pr:7") }},
		{"SDKCallNodeKey", func() string { return SDKCallNodeKey("http.Get") }},
	}
	for _, c := range calls {
		first := c.fn()
		second := c.fn()
		if first != second {
			t.Errorf("%s: not deterministic across two calls (%q != %q)", c.name, first, second)
		}
	}
}
