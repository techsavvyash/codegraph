package static

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	models "github.com/context-maximiser/code-graph/internal/model"
	"github.com/sourcegraph/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"
)

// ===========================================================================
// SCIPRelationship struct tests
// ===========================================================================

func TestSCIPRelationship_Fields(t *testing.T) {
	rel := SCIPRelationship{
		FromSymbol:       "scip-go gomod example.com/app v1 `pkg`/MyStruct#Write().",
		ToSymbol:         "scip-go gomod std . `io`/Writer#Write().",
		IsImplementation: true,
		IsReference:      false,
		IsTypeDefinition: false,
	}

	if rel.FromSymbol == "" {
		t.Error("expected non-empty FromSymbol")
	}
	if rel.ToSymbol == "" {
		t.Error("expected non-empty ToSymbol")
	}
	if !rel.IsImplementation {
		t.Error("expected IsImplementation to be true")
	}
	if rel.IsReference {
		t.Error("expected IsReference to be false")
	}
	if rel.IsTypeDefinition {
		t.Error("expected IsTypeDefinition to be false")
	}
}

// ===========================================================================
// ExtractRelationships — synthetic SCIP index (no fixture needed)
// ===========================================================================

// buildSyntheticIndex creates a minimal SCIP index with known relationships
// for deterministic testing without relying on external fixtures.
func buildSyntheticIndex(t *testing.T) *scip.Index {
	t.Helper()
	return &scip.Index{
		Metadata: &scip.Metadata{
			Version: scip.ProtocolVersion_UnspecifiedProtocolVersion,
			ToolInfo: &scip.ToolInfo{
				Name:    "scip-go",
				Version: "0.1.0",
			},
			ProjectRoot: "file:///test/project",
		},
		Documents: []*scip.Document{
			{
				RelativePath: "handler.go",
				Symbols: []*scip.SymbolInformation{
					{
						// Concrete struct method implementing an interface method
						Symbol: "scip-go gomod example.com/app v1 `pkg`/MyHandler#ServeHTTP().",
						Kind:   scip.SymbolInformation_Method,
						Relationships: []*scip.Relationship{
							{
								Symbol:           "scip-go gomod std . `net/http`/Handler#ServeHTTP().",
								IsImplementation: true,
								IsReference:      true,
							},
						},
					},
					{
						// Concrete struct implementing an interface (type-level)
						Symbol: "scip-go gomod example.com/app v1 `pkg`/MyHandler#",
						Kind:   scip.SymbolInformation_Class,
						Relationships: []*scip.Relationship{
							{
								Symbol:           "scip-go gomod std . `net/http`/Handler#",
								IsImplementation: true,
							},
						},
					},
					{
						// Symbol with only IsReference (not implementation)
						Symbol: "scip-go gomod example.com/app v1 `pkg`/helperFunc().",
						Kind:   scip.SymbolInformation_Function,
						Relationships: []*scip.Relationship{
							{
								Symbol:      "scip-go gomod std . `fmt`/Println().",
								IsReference: true,
							},
						},
					},
					{
						// Symbol with IsTypeDefinition
						Symbol: "scip-go gomod example.com/app v1 `pkg`/MyAlias#",
						Kind:   scip.SymbolInformation_Type,
						Relationships: []*scip.Relationship{
							{
								Symbol:           "scip-go gomod std . `io`/Reader#",
								IsTypeDefinition: true,
							},
						},
					},
					{
						// Symbol with no relationships
						Symbol:        "scip-go gomod example.com/app v1 `pkg`/plainFunc().",
						Kind:          scip.SymbolInformation_Function,
						Relationships: nil,
					},
				},
			},
			{
				RelativePath: "store.go",
				Symbols: []*scip.SymbolInformation{
					{
						// Another implementation relationship in a different file
						Symbol: "scip-go gomod example.com/app v1 `pkg`/Neo4jStore#GetNode().",
						Kind:   scip.SymbolInformation_Method,
						Relationships: []*scip.Relationship{
							{
								Symbol:           "scip-go gomod example.com/app v1 `pkg`/GraphStore#GetNode().",
								IsImplementation: true,
								IsReference:      true,
							},
						},
					},
				},
			},
			{
				// Excluded path — should be skipped
				RelativePath: "vendor/github.com/pkg/errors/errors.go",
				Symbols: []*scip.SymbolInformation{
					{
						Symbol: "scip-go gomod github.com/pkg/errors v0.9.1 `errors`/New().",
						Kind:   scip.SymbolInformation_Function,
						Relationships: []*scip.Relationship{
							{
								Symbol:           "scip-go gomod std . `error`/Error().",
								IsImplementation: true,
							},
						},
					},
				},
			},
		},
	}
}

func buildParserFromIndex(t *testing.T, idx *scip.Index) *SCIPParser {
	t.Helper()
	parser := NewSCIPParser()
	parser.index = idx
	return parser
}

// TestExtractRelationships_Synthetic verifies relationship extraction using
// a fully controlled synthetic SCIP index.
func TestExtractRelationships_Synthetic(t *testing.T) {
	idx := buildSyntheticIndex(t)
	parser := buildParserFromIndex(t, idx)

	rels, err := parser.ExtractRelationships()
	if err != nil {
		t.Fatalf("ExtractRelationships failed: %v", err)
	}

	// We expect 3 implementation relationships from non-excluded paths:
	// 1. MyHandler#ServeHTTP -> Handler#ServeHTTP (impl+ref)
	// 2. MyHandler# -> Handler# (impl only)
	// 3. Neo4jStore#GetNode -> GraphStore#GetNode (impl+ref)
	//
	// NOT included:
	// - helperFunc -> Println (IsReference only, not IsImplementation)
	// - MyAlias -> Reader (IsTypeDefinition only, not IsImplementation)
	// - plainFunc (no relationships)
	// - vendor/errors (excluded path)

	implRels := filterImplementationRels(rels)

	if len(implRels) != 3 {
		t.Fatalf("expected 3 implementation relationships, got %d", len(implRels))
		for _, r := range rels {
			t.Logf("  rel: %s -> %s (impl=%v ref=%v typedef=%v)",
				r.FromSymbol, r.ToSymbol, r.IsImplementation, r.IsReference, r.IsTypeDefinition)
		}
	}

	// Verify specific relationships exist
	assertRelExists(t, implRels,
		"scip-go gomod example.com/app v1 `pkg`/MyHandler#ServeHTTP().",
		"scip-go gomod std . `net/http`/Handler#ServeHTTP().",
	)
	assertRelExists(t, implRels,
		"scip-go gomod example.com/app v1 `pkg`/MyHandler#",
		"scip-go gomod std . `net/http`/Handler#",
	)
	assertRelExists(t, implRels,
		"scip-go gomod example.com/app v1 `pkg`/Neo4jStore#GetNode().",
		"scip-go gomod example.com/app v1 `pkg`/GraphStore#GetNode().",
	)
}

// TestExtractRelationships_AllTypes verifies that all relationship types
// (implementation, reference, type_definition) are extracted.
func TestExtractRelationships_AllTypes(t *testing.T) {
	idx := buildSyntheticIndex(t)
	parser := buildParserFromIndex(t, idx)

	rels, err := parser.ExtractRelationships()
	if err != nil {
		t.Fatalf("ExtractRelationships failed: %v", err)
	}

	// Total from non-excluded paths:
	// 1. MyHandler#ServeHTTP -> Handler#ServeHTTP (impl+ref)
	// 2. MyHandler# -> Handler# (impl)
	// 3. helperFunc -> Println (ref)
	// 4. MyAlias -> Reader (typedef)
	// 5. Neo4jStore#GetNode -> GraphStore#GetNode (impl+ref)
	if len(rels) != 5 {
		t.Errorf("expected 5 total relationships, got %d", len(rels))
		for _, r := range rels {
			t.Logf("  rel: %s -> %s (impl=%v ref=%v typedef=%v)",
				r.FromSymbol, r.ToSymbol, r.IsImplementation, r.IsReference, r.IsTypeDefinition)
		}
	}

	// Check that boolean flags are preserved correctly
	for _, r := range rels {
		if r.FromSymbol == "scip-go gomod example.com/app v1 `pkg`/MyHandler#ServeHTTP()." {
			if !r.IsImplementation || !r.IsReference {
				t.Errorf("MyHandler#ServeHTTP rel: expected impl=true ref=true, got impl=%v ref=%v",
					r.IsImplementation, r.IsReference)
			}
		}
		if r.FromSymbol == "scip-go gomod example.com/app v1 `pkg`/MyAlias#" {
			if r.IsImplementation || !r.IsTypeDefinition {
				t.Errorf("MyAlias rel: expected impl=false typedef=true, got impl=%v typedef=%v",
					r.IsImplementation, r.IsTypeDefinition)
			}
		}
	}
}

// TestExtractRelationships_ExcludedPaths verifies that relationships from
// excluded directories (vendor, node_modules, etc.) are skipped.
func TestExtractRelationships_ExcludedPaths(t *testing.T) {
	idx := buildSyntheticIndex(t)
	parser := buildParserFromIndex(t, idx)

	rels, err := parser.ExtractRelationships()
	if err != nil {
		t.Fatalf("ExtractRelationships failed: %v", err)
	}

	for _, r := range rels {
		if r.FromSymbol == "scip-go gomod github.com/pkg/errors v0.9.1 `errors`/New()." {
			t.Error("found relationship from excluded vendor path — should have been filtered")
		}
	}
}

// TestExtractRelationships_EmptyIndex verifies behavior with an empty SCIP index.
func TestExtractRelationships_EmptyIndex(t *testing.T) {
	idx := &scip.Index{
		Metadata: &scip.Metadata{
			ToolInfo: &scip.ToolInfo{Name: "test", Version: "1.0"},
		},
		Documents: []*scip.Document{},
	}
	parser := buildParserFromIndex(t, idx)

	rels, err := parser.ExtractRelationships()
	if err != nil {
		t.Fatalf("ExtractRelationships failed: %v", err)
	}

	if len(rels) != 0 {
		t.Errorf("expected 0 relationships from empty index, got %d", len(rels))
	}
}

// TestExtractRelationships_NoIndexLoaded verifies error when no index is loaded.
func TestExtractRelationships_NoIndexLoaded(t *testing.T) {
	parser := NewSCIPParser()

	_, err := parser.ExtractRelationships()
	if err == nil {
		t.Error("expected error when no index loaded")
	}
}

// TestExtractRelationships_SymbolsWithNoRelationships verifies that symbols
// without any relationships don't contribute to the output.
func TestExtractRelationships_SymbolsWithNoRelationships(t *testing.T) {
	idx := &scip.Index{
		Metadata: &scip.Metadata{
			ToolInfo: &scip.ToolInfo{Name: "test", Version: "1.0"},
		},
		Documents: []*scip.Document{
			{
				RelativePath: "main.go",
				Symbols: []*scip.SymbolInformation{
					{
						Symbol:        "scip-go gomod example.com v1 `main`/main().",
						Kind:          scip.SymbolInformation_Function,
						Relationships: nil,
					},
					{
						Symbol:        "scip-go gomod example.com v1 `main`/helper().",
						Kind:          scip.SymbolInformation_Function,
						Relationships: []*scip.Relationship{}, // explicitly empty
					},
				},
			},
		},
	}
	parser := buildParserFromIndex(t, idx)

	rels, err := parser.ExtractRelationships()
	if err != nil {
		t.Fatalf("ExtractRelationships failed: %v", err)
	}

	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

// ===========================================================================
// ExtractRelationships — fixture-based tests (real SCIP data)
// ===========================================================================

// TestExtractRelationships_Fixture uses the real tiny_project.scip fixture.
// The Go fixture has ~12 implementation relationships from testify suite embeddings.
func TestExtractRelationships_Fixture(t *testing.T) {
	parser := loadFixtureParser(t)

	rels, err := parser.ExtractRelationships()
	if err != nil {
		t.Fatalf("ExtractRelationships failed: %v", err)
	}

	// The fixture should return some relationships (may be 0 for tiny project,
	// but shouldn't error). Log what we find for visibility.
	t.Logf("Fixture returned %d relationships", len(rels))

	implCount := 0
	for _, r := range rels {
		if r.IsImplementation {
			implCount++
			t.Logf("  IMPL: %s -> %s", r.FromSymbol, r.ToSymbol)
		}
	}
	t.Logf("  of which %d are implementation relationships", implCount)

	// Every relationship must have non-empty FromSymbol and ToSymbol
	for i, r := range rels {
		if r.FromSymbol == "" {
			t.Errorf("rel[%d]: empty FromSymbol", i)
		}
		if r.ToSymbol == "" {
			t.Errorf("rel[%d]: empty ToSymbol", i)
		}
		if !r.IsImplementation && !r.IsReference && !r.IsTypeDefinition {
			t.Errorf("rel[%d]: no relationship type flags set", i)
		}
	}
}

// ===========================================================================
// ExtractRelationships — large synthetic index (performance sanity)
// ===========================================================================

func TestExtractRelationships_LargeIndex(t *testing.T) {
	// Build an index with 1000 symbols, each with 2 relationships
	docs := make([]*scip.Document, 0, 10)
	for d := 0; d < 10; d++ {
		syms := make([]*scip.SymbolInformation, 0, 100)
		for s := 0; s < 100; s++ {
			sym := &scip.SymbolInformation{
				Symbol: fmt.Sprintf("scip-go gomod example.com v1 `pkg%d`/Type%d#Method().", d, s),
				Kind:   scip.SymbolInformation_Method,
				Relationships: []*scip.Relationship{
					{
						Symbol:           fmt.Sprintf("scip-go gomod example.com v1 `iface%d`/Interface%d#Method().", d, s),
						IsImplementation: true,
					},
					{
						Symbol:      fmt.Sprintf("scip-go gomod std . `fmt`/Println%d().", s),
						IsReference: true,
					},
				},
			}
			syms = append(syms, sym)
		}
		docs = append(docs, &scip.Document{
			RelativePath: fmt.Sprintf("pkg%d/types.go", d),
			Symbols:      syms,
		})
	}

	idx := &scip.Index{
		Metadata: &scip.Metadata{
			ToolInfo: &scip.ToolInfo{Name: "test", Version: "1.0"},
		},
		Documents: docs,
	}
	parser := buildParserFromIndex(t, idx)

	rels, err := parser.ExtractRelationships()
	if err != nil {
		t.Fatalf("ExtractRelationships failed: %v", err)
	}

	// 10 docs * 100 symbols * 2 rels = 2000 total relationships
	if len(rels) != 2000 {
		t.Errorf("expected 2000 relationships, got %d", len(rels))
	}

	implRels := filterImplementationRels(rels)
	if len(implRels) != 1000 {
		t.Errorf("expected 1000 implementation relationships, got %d", len(implRels))
	}
}

// ===========================================================================
// Serialization roundtrip — ensures synthetic index survives proto marshal/unmarshal
// ===========================================================================

func TestExtractRelationships_ProtoRoundtrip(t *testing.T) {
	idx := buildSyntheticIndex(t)

	// Marshal to bytes
	data, err := proto.Marshal(idx)
	if err != nil {
		t.Fatalf("proto.Marshal failed: %v", err)
	}

	// Write to temp file
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.scip")
	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	// Parse from file (like real usage)
	parser := NewSCIPParser()
	if err := parser.ParseFile(tmpFile); err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}

	rels, err := parser.ExtractRelationships()
	if err != nil {
		t.Fatalf("ExtractRelationships failed: %v", err)
	}

	implRels := filterImplementationRels(rels)
	if len(implRels) != 3 {
		t.Errorf("expected 3 implementation relationships after roundtrip, got %d", len(implRels))
	}
}

// ===========================================================================
// buildImplementsBatch — unit test for the batch builder
// ===========================================================================

// TestBuildImplementsBatch verifies that the batch builder correctly maps
// SCIP relationships to Neo4j relationship batch items using symbolIDs and defIDs.
func TestBuildImplementsBatch(t *testing.T) {
	rels := []SCIPRelationship{
		{
			// Both symbols have definitions — should create IMPLEMENTS edge between defs
			FromSymbol:       "scip-go gomod example.com v1 `pkg`/Neo4jStore#GetNode().",
			ToSymbol:         "scip-go gomod example.com v1 `pkg`/GraphStore#GetNode().",
			IsImplementation: true,
		},
		{
			// FromSymbol has no definition node — should fall back to symbol-level edge
			FromSymbol:       "scip-go gomod example.com v1 `pkg`/Orphan#Method().",
			ToSymbol:         "scip-go gomod example.com v1 `pkg`/SomeInterface#Method().",
			IsImplementation: true,
		},
		{
			// Neither symbol exists — should be skipped
			FromSymbol:       "scip-go gomod example.com v1 `pkg`/Ghost#X().",
			ToSymbol:         "scip-go gomod example.com v1 `pkg`/Phantom#X().",
			IsImplementation: true,
		},
		{
			// Not an implementation relationship — should be skipped
			FromSymbol:       "scip-go gomod example.com v1 `pkg`/Func().",
			ToSymbol:         "scip-go gomod std . `fmt`/Println().",
			IsImplementation: false,
			IsReference:      true,
		},
	}

	// Simulate the symbolIDs map (symbolNodeKey → elementId)
	// SymbolNodeKey returns the SCIP symbol string directly
	symbolIDs := map[string]string{
		"scip-go gomod example.com v1 `pkg`/Neo4jStore#GetNode().":   "sym:1",
		"scip-go gomod example.com v1 `pkg`/GraphStore#GetNode().":   "sym:2",
		"scip-go gomod example.com v1 `pkg`/Orphan#Method().":        "sym:3",
		"scip-go gomod example.com v1 `pkg`/SomeInterface#Method().": "sym:4",
		"scip-go gomod example.com v1 `pkg`/Func().":                 "sym:5",
		"scip-go gomod std . `fmt`/Println().":                       "sym:6",
	}

	// Simulate the defIDs map (defNodeKey → elementId)
	// Only Neo4jStore and GraphStore have definition nodes
	defIDs := map[string]string{
		"method:store.go#scip-go gomod example.com v1 `pkg`/Neo4jStore#GetNode().":   "def:1",
		"method:store.go#scip-go gomod example.com v1 `pkg`/GraphStore#GetNode().":   "def:2",
		"method:iface.go#scip-go gomod example.com v1 `pkg`/SomeInterface#Method().": "def:4",
	}

	// symbolToDefKey maps symbol strings to their definition nodeKeys
	// (normally derived from computeDefinitionProps during indexing)
	symbolToDefKey := map[string]string{
		"scip-go gomod example.com v1 `pkg`/Neo4jStore#GetNode().":   "method:store.go#scip-go gomod example.com v1 `pkg`/Neo4jStore#GetNode().",
		"scip-go gomod example.com v1 `pkg`/GraphStore#GetNode().":   "method:store.go#scip-go gomod example.com v1 `pkg`/GraphStore#GetNode().",
		"scip-go gomod example.com v1 `pkg`/SomeInterface#Method().": "method:iface.go#scip-go gomod example.com v1 `pkg`/SomeInterface#Method().",
	}

	batch := buildImplementsBatch(rels, symbolIDs, defIDs, symbolToDefKey, models.DefaultScope())

	// Expected:
	// 1. Neo4jStore#GetNode -> GraphStore#GetNode: both have defs, so def:1 -> def:2
	// 2. Orphan#Method -> SomeInterface#Method: Orphan has no def, so sym:3 -> def:4 (or sym:4)
	// 3. Ghost — skipped (neither symbol exists)
	// 4. Func -> Println — skipped (not IsImplementation)

	if len(batch) < 1 {
		t.Fatalf("expected at least 1 batch item, got %d", len(batch))
	}

	// First batch item: both have defs
	found := false
	for _, item := range batch {
		fromID := item["fromId"].(string)
		toID := item["toId"].(string)
		if fromID == "def:1" && toID == "def:2" {
			found = true
		}
	}
	if !found {
		t.Error("expected IMPLEMENTS edge from def:1 (Neo4jStore#GetNode) to def:2 (GraphStore#GetNode)")
		for _, item := range batch {
			t.Logf("  batch item: fromId=%s toId=%s", item["fromId"], item["toId"])
		}
	}

	// No batch items should have empty fromId or toId
	for i, item := range batch {
		if item["fromId"] == "" || item["toId"] == "" {
			t.Errorf("batch[%d]: empty fromId or toId: %v", i, item)
		}
	}
}

// TestBuildImplementsBatch_Empty verifies no batch items for empty input.
func TestBuildImplementsBatch_Empty(t *testing.T) {
	batch := buildImplementsBatch(nil, nil, nil, nil, models.DefaultScope())
	if len(batch) != 0 {
		t.Errorf("expected 0 batch items for nil input, got %d", len(batch))
	}

	batch = buildImplementsBatch([]SCIPRelationship{}, map[string]string{}, map[string]string{}, map[string]string{}, models.DefaultScope())
	if len(batch) != 0 {
		t.Errorf("expected 0 batch items for empty input, got %d", len(batch))
	}
}

// ===========================================================================
// Helpers
// ===========================================================================

func filterImplementationRels(rels []SCIPRelationship) []SCIPRelationship {
	var impl []SCIPRelationship
	for _, r := range rels {
		if r.IsImplementation {
			impl = append(impl, r)
		}
	}
	return impl
}

func assertRelExists(t *testing.T, rels []SCIPRelationship, fromSymbol, toSymbol string) {
	t.Helper()
	for _, r := range rels {
		if r.FromSymbol == fromSymbol && r.ToSymbol == toSymbol {
			return
		}
	}
	t.Errorf("expected relationship %s -> %s not found", fromSymbol, toSymbol)
}

// Ensure fmt is referenced (used in TestExtractRelationships_LargeIndex).
var _ = fmt.Sprintf
