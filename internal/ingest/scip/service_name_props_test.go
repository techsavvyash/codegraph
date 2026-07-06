package static

import (
	"testing"

	models "github.com/context-maximiser/code-graph/internal/model"
)

// TestServiceNameOnPerServiceNodes asserts that per-service identity nodes
// (Function, Method, Variable, Parameter) emit the canonical serviceName
// property. This unblocks scoped queries — without it, every "show me X in
// service Y" query has to traverse the Service-CONTAINS chain at runtime.
//
// Class/Interface/Module are intentionally checked NOT to carry serviceName
// because their nodeKeys derive from globally-unique SCIP FQNs and the same
// node is MERGEd from multiple services. Storing one serviceName on a
// shared node would be incoherent (last-writer wins).
func TestServiceNameOnPerServiceNodes(t *testing.T) {
	const svc = "codegraph/apps/cli"
	indexer := NewSCIPIndexer(nil, svc, "v1", "")

	type nodeCase struct {
		name           string
		symbolInfo     *models.SymbolInfo
		expectLabel    string
		expectsService bool // whether this label should carry serviceName
	}
	cases := []nodeCase{
		{
			name: "Function carries serviceName",
			symbolInfo: &models.SymbolInfo{
				Symbol:      &models.SCIPSymbol{Scheme: "scip-go", Manager: "go", Name: "x", Version: "v1", Descriptor: "Run()."},
				Kind:        models.FunctionSymbol,
				DisplayName: "Run",
				Signature:   "Run()",
				FilePath:    "main.go",
				StartLine:   1,
			},
			expectLabel:    "Function",
			expectsService: true,
		},
		{
			name: "Method carries serviceName",
			symbolInfo: &models.SymbolInfo{
				Symbol:      &models.SCIPSymbol{Scheme: "scip-go", Manager: "go", Name: "x", Version: "v1", Descriptor: "Client#Do()."},
				Kind:        models.MethodSymbol,
				DisplayName: "Do",
				Signature:   "(*Client).Do()",
				FilePath:    "client.go",
				StartLine:   10,
			},
			expectLabel:    "Method",
			expectsService: true,
		},
		{
			name: "Variable carries serviceName",
			symbolInfo: &models.SymbolInfo{
				Symbol:      &models.SCIPSymbol{Scheme: "scip-go", Manager: "go", Name: "x", Version: "v1", Descriptor: "rootCmd."},
				Kind:        models.VariableSymbol,
				DisplayName: "rootCmd",
				FilePath:    "main.go",
				StartLine:   3,
			},
			expectLabel:    "Variable",
			expectsService: true,
		},
		{
			name: "Class does NOT carry serviceName (shared identity via FQN)",
			symbolInfo: &models.SymbolInfo{
				Symbol:      &models.SCIPSymbol{Scheme: "scip-go", Manager: "go", Name: "github.com/example/svc", Version: "v1", Descriptor: "User#"},
				Kind:        models.TypeSymbol,
				DisplayName: "User",
				FilePath:    "user.go",
			},
			expectLabel:    "Class",
			expectsService: false,
		},
		{
			name: "Interface does NOT carry serviceName (shared identity via FQN)",
			symbolInfo: &models.SymbolInfo{
				Symbol:      &models.SCIPSymbol{Scheme: "scip-go", Manager: "go", Name: "github.com/example/svc", Version: "v1", Descriptor: "Reader#"},
				Kind:        models.InterfaceSymbol,
				DisplayName: "Reader",
				FilePath:    "io.go",
			},
			expectLabel:    "Interface",
			expectsService: false,
		},
		{
			name: "Module does NOT carry serviceName (shared FQN)",
			symbolInfo: &models.SymbolInfo{
				Symbol:      &models.SCIPSymbol{Scheme: "scip-go", Manager: "go", Name: "github.com/example/svc/pkg", Version: "v1", Descriptor: ""},
				Kind:        models.PackageSymbol,
				DisplayName: "pkg",
				FilePath:    "pkg.go",
			},
			expectLabel:    "Module",
			expectsService: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			label, _, props := indexer.computeDefinitionProps(tc.symbolInfo)
			if label != tc.expectLabel {
				t.Fatalf("label = %q, want %q (test data error)", label, tc.expectLabel)
			}
			got, present := props["serviceName"]
			if tc.expectsService {
				if !present {
					t.Fatalf("%s expected serviceName property, got absent", label)
				}
				if got != svc {
					t.Errorf("%s serviceName = %q, want %q", label, got, svc)
				}
			} else if present {
				t.Errorf("%s should NOT carry serviceName (shared identity), got %v", label, got)
			}
		})
	}
}

// TestServiceNamePropFollowsServiceFlag verifies the property tracks the
// indexer's serviceName field — i.e. two indexers configured for different
// services produce different serviceName values for the same SymbolInfo.
func TestServiceNamePropFollowsServiceFlag(t *testing.T) {
	a := NewSCIPIndexer(nil, "svc-a", "v1", "")
	b := NewSCIPIndexer(nil, "svc-b", "v1", "")

	si := &models.SymbolInfo{
		Symbol:      &models.SCIPSymbol{Scheme: "scip-go", Manager: "go", Name: "x", Version: "v1", Descriptor: "F()."},
		Kind:        models.FunctionSymbol,
		DisplayName: "F",
		Signature:   "F()",
		FilePath:    "main.go",
		StartLine:   1,
	}

	_, _, propsA := a.computeDefinitionProps(si)
	_, _, propsB := b.computeDefinitionProps(si)
	if propsA["serviceName"] != "svc-a" {
		t.Errorf("indexer A: serviceName = %v, want svc-a", propsA["serviceName"])
	}
	if propsB["serviceName"] != "svc-b" {
		t.Errorf("indexer B: serviceName = %v, want svc-b", propsB["serviceName"])
	}
}
