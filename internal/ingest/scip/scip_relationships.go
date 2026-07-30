package static

import (
	"fmt"

	models "github.com/context-maximiser/code-graph/internal/model"
)

// SCIPRelationship represents a relationship extracted from SCIP
// SymbolInformation.Relationships. Each relationship links a "from" symbol
// (the symbol that declares the relationship) to a "to" symbol (the target),
// with boolean flags matching the SCIP protocol.
//
// For implementation relationships (IsImplementation=true):
//
//	FromSymbol is the concrete type/method that implements
//	ToSymbol is the interface type/method being implemented
type SCIPRelationship struct {
	FromSymbol       string // SCIP symbol string of the declaring symbol
	ToSymbol         string // SCIP symbol string of the target symbol
	IsImplementation bool   // true if FromSymbol implements ToSymbol
	IsReference      bool   // true if FromSymbol references ToSymbol
	IsTypeDefinition bool   // true if FromSymbol is a type definition of ToSymbol

	// DetectionSource records where this relationship came from: "" (or
	// "scip") for relationships parsed directly out of SymbolInformation.Relationships
	// by ExtractRelationships, or "go-types-resolver" for relationships
	// produced by internal/ingest/resolve's structural type resolver
	// (RFC-001 Layer 3). Threaded onto the IMPLEMENTS edge's properties by
	// buildImplementsBatch so resolver-produced edges stay distinguishable
	// from indexer-native ones for debugging/auditing.
	DetectionSource string
}

// detectionSourceOrDefault returns rel.DetectionSource, defaulting to "scip"
// for relationships that didn't set it (i.e. everything produced by
// ExtractRelationships, which parses SCIP-native data).
func detectionSourceOrDefault(rel SCIPRelationship) string {
	if rel.DetectionSource == "" {
		return "scip"
	}
	return rel.DetectionSource
}

// ExtractRelationships extracts all SymbolInformation.Relationships from
// the parsed SCIP index. It iterates over all documents (skipping excluded
// paths) and all symbols within each document, collecting relationships.
//
// This is Phase 1 / Layer 1 of RFC-001: extracting the relationship data
// that SCIP indexers already compute but that CodeGraph previously discarded.
func (sp *SCIPParser) ExtractRelationships() ([]SCIPRelationship, error) {
	if sp.index == nil {
		return nil, fmt.Errorf("no SCIP index loaded")
	}

	var rels []SCIPRelationship

	for _, doc := range sp.index.Documents {
		if shouldExcludePath(doc.RelativePath) {
			continue
		}

		for _, sym := range doc.Symbols {
			for _, rel := range sym.Relationships {
				if !rel.IsImplementation && !rel.IsReference && !rel.IsTypeDefinition {
					continue
				}
				rels = append(rels, SCIPRelationship{
					FromSymbol:       sym.Symbol,
					ToSymbol:         rel.Symbol,
					IsImplementation: rel.IsImplementation,
					IsReference:      rel.IsReference,
					IsTypeDefinition: rel.IsTypeDefinition,
				})
			}
		}
	}

	return rels, nil
}

// buildImplementsBatch constructs the Neo4j relationship batch items for
// IMPLEMENTS edges from extracted SCIPRelationships.
//
// It resolves each relationship's FromSymbol and ToSymbol to graph node IDs
// using the provided maps:
//   - symbolIDs: maps SymbolNodeKey (= SCIP symbol string) → Neo4j elementId
//   - defIDs: maps definition nodeKey → Neo4j elementId
//   - symbolToDefKey: maps SCIP symbol string → definition nodeKey
//
// When both sides have definition nodes (Function/Method/Class/Interface),
// the IMPLEMENTS edge is created between definition nodes for simpler
// call-graph queries. Falls back to Symbol nodes when definitions are absent.
func buildImplementsBatch(
	rels []SCIPRelationship,
	symbolIDs map[string]string,
	defIDs map[string]string,
	symbolToDefKey map[string]string,
	scope models.ScopeContext,
) []map[string]any {
	var batch []map[string]any

	for _, rel := range rels {
		if !rel.IsImplementation {
			continue
		}

		// Resolve fromID: prefer definition node, fall back to symbol node
		fromID := resolveNodeID(rel.FromSymbol, symbolIDs, defIDs, symbolToDefKey)
		toID := resolveNodeID(rel.ToSymbol, symbolIDs, defIDs, symbolToDefKey)

		if fromID == "" || toID == "" {
			continue
		}

		batch = append(batch, map[string]any{
			"fromId": fromID,
			"toId":   toID,
			"props": map[string]any{
				"scope":           scope.Scope,
				"scopeId":         scope.ScopeID,
				"detectionSource": detectionSourceOrDefault(rel),
			},
		})
	}

	return batch
}

// resolveNodeID returns the Neo4j elementId for a SCIP symbol string,
// preferring the definition node ID over the symbol node ID.
func resolveNodeID(
	scipSymbol string,
	symbolIDs map[string]string,
	defIDs map[string]string,
	symbolToDefKey map[string]string,
) string {
	// Try definition node first
	if defKey, ok := symbolToDefKey[scipSymbol]; ok {
		if id, ok := defIDs[defKey]; ok {
			return id
		}
	}
	// Fall back to symbol node
	if id, ok := symbolIDs[scipSymbol]; ok {
		return id
	}
	return ""
}
