package models

import (
	"testing"
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
	key := FileNodeKey("pkg/models/node.go")
	if key != "file:pkg/models/node.go" {
		t.Errorf("expected file:pkg/models/node.go, got %s", key)
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
	key := FunctionNodeKey("pkg/neo4j/client.go", "MergeNode(...)")
	expected := "func:pkg/neo4j/client.go#MergeNode(...)"
	if key != expected {
		t.Errorf("expected %s, got %s", expected, key)
	}
}

func TestMethodNodeKey(t *testing.T) {
	key := MethodNodeKey("pkg/neo4j/client.go", "(*Client).Close()")
	expected := "method:pkg/neo4j/client.go#(*Client).Close()"
	if key != expected {
		t.Errorf("expected %s, got %s", expected, key)
	}
}

func TestClassNodeKey(t *testing.T) {
	// With FQN
	key := ClassNodeKey("scip-go...TypeSymbol#", "", "")
	if key != "class:scip-go...TypeSymbol#" {
		t.Errorf("expected class:scip-go...TypeSymbol#, got %s", key)
	}
	// Without FQN
	key = ClassNodeKey("", "pkg/models/node.go", "BaseNode")
	if key != "class:pkg/models/node.go#BaseNode" {
		t.Errorf("expected class:pkg/models/node.go#BaseNode, got %s", key)
	}
}

func TestInterfaceNodeKey(t *testing.T) {
	key := InterfaceNodeKey("io.Reader", "", "")
	if key != "iface:io.Reader" {
		t.Errorf("expected iface:io.Reader, got %s", key)
	}
	key = InterfaceNodeKey("", "pkg/foo.go", "Handler")
	if key != "iface:pkg/foo.go#Handler" {
		t.Errorf("expected iface:pkg/foo.go#Handler, got %s", key)
	}
}

func TestModuleNodeKey(t *testing.T) {
	key := ModuleNodeKey("github.com/example/pkg")
	if key != "mod:github.com/example/pkg" {
		t.Errorf("expected mod:github.com/example/pkg, got %s", key)
	}
}

func TestVariableNodeKey(t *testing.T) {
	key := VariableNodeKey("pkg/neo4j/client.go", "driver", 21)
	expected := "var:pkg/neo4j/client.go#driver:21"
	if key != expected {
		t.Errorf("expected %s, got %s", expected, key)
	}
}

func TestParameterNodeKey(t *testing.T) {
	key := ParameterNodeKey("pkg/neo4j/client.go", "MergeNode(...)", "ctx", 0)
	expected := "param:pkg/neo4j/client.go#MergeNode(...):ctx:0"
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
	key := CommentNodeKey("pkg/models/node.go", 10)
	if key != "comment:pkg/models/node.go:10" {
		t.Errorf("expected comment:pkg/models/node.go:10, got %s", key)
	}
}

func TestReferenceNodeKey(t *testing.T) {
	key := ReferenceNodeKey("pkg/models/node.go", 42, 5)
	if key != "ref:pkg/models/node.go:42:5" {
		t.Errorf("expected ref:pkg/models/node.go:42:5, got %s", key)
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
		"service":   ServiceNodeKey("codegraph"),
		"file":      FileNodeKey("pkg/models/node.go"),
		"function":  FunctionNodeKey("pkg/neo4j/client.go", "MergeNode(...)"),
		"method":    MethodNodeKey("pkg/neo4j/client.go", "(*Client).Close()"),
		"class":     ClassNodeKey("scip-go...Type#", "", ""),
		"interface": InterfaceNodeKey("io.Reader", "", ""),
		"module":    ModuleNodeKey("github.com/example/pkg"),
		"variable":  VariableNodeKey("pkg/neo4j/client.go", "driver", 21),
		"parameter": ParameterNodeKey("pkg/neo4j/client.go", "MergeNode(...)", "ctx", 0),
		"document":  DocumentNodeKey("docs/README.md"),
		"feature":   FeatureNodeKey("SCIP Indexing"),
		"api":       APIRouteNodeKey("GET", "/api/users"),
		"sdkcall":   SDKCallNodeKey("go-http-client.Get"),
		"comment":   CommentNodeKey("pkg/models/node.go", 10),
		"reference": ReferenceNodeKey("pkg/models/node.go", 42, 5),
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
		a := FunctionNodeKey("pkg/foo.go", "Bar()")
		b := FunctionNodeKey("pkg/foo.go", "Bar()")
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
	funcKey := FunctionNodeKey(filePath, signature)
	methKey := MethodNodeKey(filePath, signature)
	if funcKey == methKey {
		t.Errorf("FunctionNodeKey and MethodNodeKey must differ for same args: both = %q", funcKey)
	}
	if funcKey != "func:"+filePath+"#"+signature {
		t.Errorf("FunctionNodeKey format wrong: got %q", funcKey)
	}
	if methKey != "method:"+filePath+"#"+signature {
		t.Errorf("MethodNodeKey format wrong: got %q", methKey)
	}
}

// TestClassNodeKeyFallbackFQNPrecedence verifies that when fqn is non-empty the fqn
// is used and the filePath/name arguments are ignored entirely.
func TestClassNodeKeyFallbackFQNPrecedence(t *testing.T) {
	// fqn present: filePath and name must be ignored
	key := ClassNodeKey("scip-go...Foo#", "pkg/foo.go", "Foo")
	if key != "class:scip-go...Foo#" {
		t.Errorf("expected fqn to win, got %q", key)
	}
	// fqn absent: filePath+name used
	key2 := ClassNodeKey("", "pkg/foo.go", "Foo")
	if key2 != "class:pkg/foo.go#Foo" {
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
	keyFQN := InterfaceNodeKey("io.Writer", "", "")
	keyFallback := InterfaceNodeKey("", "pkg/foo.go", "Writer")
	if keyFQN != "iface:io.Writer" {
		t.Errorf("expected iface:io.Writer, got %q", keyFQN)
	}
	if keyFallback != "iface:pkg/foo.go#Writer" {
		t.Errorf("expected iface:pkg/foo.go#Writer, got %q", keyFallback)
	}
	if keyFQN == keyFallback {
		t.Error("fqn and fallback InterfaceNodeKey must differ")
	}
}

// TestVariableNodeKeyLineDiscrimination ensures that two variables with the same name
// in the same file at different lines get different keys (line is part of the key).
func TestVariableNodeKeyLineDiscrimination(t *testing.T) {
	k1 := VariableNodeKey("pkg/foo.go", "err", 10)
	k2 := VariableNodeKey("pkg/foo.go", "err", 20)
	if k1 == k2 {
		t.Error("VariableNodeKey must differ for different start lines")
	}
	if k1 != "var:pkg/foo.go#err:10" {
		t.Errorf("unexpected key format: %q", k1)
	}
}

// TestParameterNodeKeyIndexDiscrimination ensures positional parameters with the same
// name at different indices in the same function produce different keys.
func TestParameterNodeKeyIndexDiscrimination(t *testing.T) {
	k0 := ParameterNodeKey("pkg/foo.go", "Bar()", "x", 0)
	k1 := ParameterNodeKey("pkg/foo.go", "Bar()", "x", 1)
	if k0 == k1 {
		t.Error("ParameterNodeKey must differ for different indices")
	}
	if k0 != "param:pkg/foo.go#Bar():x:0" {
		t.Errorf("unexpected key format: %q", k0)
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
		{"FileNodeKey", func() string { return FileNodeKey("pkg/a.go") }},
		{"SymbolNodeKey", func() string { return SymbolNodeKey("scip-go go example/pkg Fn#") }},
		{"FunctionNodeKey", func() string { return FunctionNodeKey("pkg/a.go", "Fn()") }},
		{"MethodNodeKey", func() string { return MethodNodeKey("pkg/a.go", "(*T).Fn()") }},
		{"ClassNodeKey-fqn", func() string { return ClassNodeKey("scip-go...T#", "", "") }},
		{"ClassNodeKey-fallback", func() string { return ClassNodeKey("", "pkg/a.go", "T") }},
		{"InterfaceNodeKey-fqn", func() string { return InterfaceNodeKey("io.Reader", "", "") }},
		{"InterfaceNodeKey-fallback", func() string { return InterfaceNodeKey("", "pkg/a.go", "R") }},
		{"ModuleNodeKey", func() string { return ModuleNodeKey("github.com/example/pkg") }},
		{"VariableNodeKey", func() string { return VariableNodeKey("pkg/a.go", "v", 5) }},
		{"ParameterNodeKey", func() string { return ParameterNodeKey("pkg/a.go", "Fn()", "p", 0) }},
		{"DocumentNodeKey", func() string { return DocumentNodeKey("docs/x.md") }},
		{"DocumentChunkNodeKey", func() string { return DocumentChunkNodeKey("doc:docs/x.md", 2) }},
		{"FeatureNodeKey", func() string { return FeatureNodeKey("auth") }},
		{"APIRouteNodeKey", func() string { return APIRouteNodeKey("POST", "/login") }},
		{"CommentNodeKey", func() string { return CommentNodeKey("pkg/a.go", 7) }},
		{"ReferenceNodeKey", func() string { return ReferenceNodeKey("pkg/a.go", 7, 3) }},
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
