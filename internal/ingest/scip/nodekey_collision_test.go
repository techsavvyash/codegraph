package static

import (
	"testing"

	models "github.com/context-maximiser/code-graph/internal/model"
)

// TestComputeDefinitionPropsDistinctAcrossServices is the indexer-level
// regression test for bug B1 (File node collision across modules). It
// exercises the actual SCIPIndexer code path that builds nodeKeys from
// SCIP symbol info, with two indexers configured for different services
// but identical relative file paths, and asserts that all path-based
// node kinds produce distinct keys.
//
// Before the fix, scip-go's module-relative paths ("main.go" for both
// apps/cli and apps/mcp-server-go) merged into a single Neo4j node, and
// every Function/Method/Variable defined in those files collapsed too.
func TestComputeDefinitionPropsDistinctAcrossServices(t *testing.T) {
	indexerA := NewSCIPIndexer(nil, "codegraph/apps/cli", "v1", "")
	indexerB := NewSCIPIndexer(nil, "codegraph/apps/mcp-server-go", "v1", "")

	// Each kind below is a (label, *SymbolInfo) pair with identical content
	// across services — only the indexer's serviceName changes.
	cases := []struct {
		name       string
		symbolInfo *models.SymbolInfo
	}{
		{
			name: "Function",
			symbolInfo: &models.SymbolInfo{
				Symbol:      &models.SCIPSymbol{Scheme: "scip-go", Manager: "go", Name: "example", Version: "v1", Descriptor: "Execute()."},
				Kind:        models.FunctionSymbol,
				DisplayName: "Execute",
				Signature:   "Execute()",
				FilePath:    "main.go",
				StartLine:   10, EndLine: 20,
			},
		},
		{
			name: "Method",
			symbolInfo: &models.SymbolInfo{
				Symbol:      &models.SCIPSymbol{Scheme: "scip-go", Manager: "go", Name: "example", Version: "v1", Descriptor: "Client#Close()."},
				Kind:        models.MethodSymbol,
				DisplayName: "Close",
				Signature:   "(*Client).Close()",
				FilePath:    "store.go",
				StartLine:   42, EndLine: 50,
			},
		},
		{
			name: "Variable",
			symbolInfo: &models.SymbolInfo{
				// SCIPSymbol is unused for Variable nodeKey construction — VariableNodeKey
				// uses serviceName+filePath+name+startLine directly.
				Symbol:      &models.SCIPSymbol{Scheme: "scip-go", Manager: "go", Name: "example", Version: "v1", Descriptor: "rootCmd."},
				Kind:        models.VariableSymbol,
				DisplayName: "rootCmd",
				FilePath:    "main.go",
				StartLine:   25,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			labelA, keyA, _ := indexerA.computeDefinitionProps(tc.symbolInfo)
			labelB, keyB, _ := indexerB.computeDefinitionProps(tc.symbolInfo)

			if labelA != labelB {
				t.Fatalf("label mismatch: A=%q B=%q (test data error)", labelA, labelB)
			}
			if keyA == keyB {
				t.Fatalf("%s nodeKey collided across services: both = %q", labelA, keyA)
			}
		})
	}
}

// TestSCIPSymbolBasedClassKeysShared verifies the inverse: when the SCIP
// symbol (FQN) is non-empty for a Class, the same canonical type defined
// in two services (e.g., a shared interface re-declared) produces the
// SAME class nodeKey. This is intentional — SCIP symbols are globally
// unique and cross-service joins go through SymbolNodeKey.
func TestSCIPSymbolBasedClassKeysShared(t *testing.T) {
	indexerA := NewSCIPIndexer(nil, "codegraph/apps/cli", "v1", "")
	indexerB := NewSCIPIndexer(nil, "codegraph/apps/mcp-server-go", "v1", "")

	si := &models.SymbolInfo{
		// Non-empty SCIP symbol — the FQN should win and ignore serviceName.
		Symbol: &models.SCIPSymbol{
			Scheme: "scip-go", Manager: "go",
			Name: "github.com/context-maximiser/code-graph/internal/model", Version: "v1",
			Descriptor: "Service#",
		},
		Kind:        models.TypeSymbol,
		DisplayName: "Service",
		FilePath:    "node.go",
	}
	_, keyA, _ := indexerA.computeDefinitionProps(si)
	_, keyB, _ := indexerB.computeDefinitionProps(si)
	if keyA != keyB {
		t.Fatalf("SCIP-FQN-based class key must be service-independent: %q != %q", keyA, keyB)
	}
}
