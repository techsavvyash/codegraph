package resolve

import "fmt"

// Relationship mirrors internal/ingest/scip.SCIPRelationship's shape
// (FromSymbol/ToSymbol/IsImplementation/IsReference/IsTypeDefinition) so
// callers in that package can convert 1:1 with a field-for-field copy. It is
// declared locally, rather than importing internal/ingest/scip, so this
// package has zero dependency on the SCIP layer and stays independently
// testable — internal/ingest/scip is the one importing internal/ingest/resolve,
// not the other way around.
type Relationship struct {
	FromSymbol       string
	ToSymbol         string
	IsImplementation bool
	IsReference      bool
	IsTypeDefinition bool
}

// Stats reports what happened during ResolveImplementations: the go/types
// pass (embedded) plus the SCIP symbol-join outcome.
type Stats struct {
	TypeResolveStats

	// TypeLevelEmitted / MethodLevelEmitted count relationships actually
	// emitted after the symbol join (both endpoints resolved to a known
	// SCIP symbol).
	TypeLevelEmitted   int
	MethodLevelEmitted int

	// DroppedMissingSymbol counts resolver-produced relationships where at
	// least one endpoint did not resolve to any symbol in knownSymbols, and
	// so could not be emitted.
	DroppedMissingSymbol int
}

// ResolveImplementations runs the Go structural type resolver against
// projectRoot and joins its output onto the SCIP symbol strings actually
// present in knownSymbols (i.e. the symbols scip-go already parsed out of
// index.scip for this project). Only relationships where BOTH endpoints
// resolve to a known symbol are returned; everything else is counted in
// Stats.DroppedMissingSymbol rather than emitted, since an IMPLEMENTS edge
// to a symbol that doesn't exist in the graph cannot be created anyway.
//
// This is the only exported entry point callers outside this package
// (internal/ingest/scip/scip_indexer.go) need — it is best-effort by
// design: packages.Load failures (no go.mod, non-Go project, broken
// module) are returned as an error for the caller to WARN-and-continue on,
// never as a panic.
func ResolveImplementations(projectRoot string, knownSymbols []string) ([]Relationship, Stats, error) {
	typeRels, typeStats, err := ResolveGoTypes(projectRoot)
	stats := Stats{TypeResolveStats: typeStats}
	if err != nil {
		return nil, stats, fmt.Errorf("resolve go types: %w", err)
	}

	lookup := buildSymbolLookup(knownSymbols)

	rels := make([]Relationship, 0, len(typeRels))
	for _, tr := range typeRels {
		var fromSym, toSym string
		var ok bool

		if tr.CandidateMethod == "" {
			// Type-level relationship.
			fromSym, ok = lookup.byType[typeKey(tr.CandidatePkgPath, tr.CandidateType)]
			if !ok {
				stats.DroppedMissingSymbol++
				continue
			}
			toSym, ok = lookup.byType[typeKey(tr.InterfacePkgPath, tr.InterfaceType)]
			if !ok {
				stats.DroppedMissingSymbol++
				continue
			}
			rels = append(rels, Relationship{FromSymbol: fromSym, ToSymbol: toSym, IsImplementation: true})
			stats.TypeLevelEmitted++
			continue
		}

		// Method-level relationship.
		fromSym, ok = lookup.byMethod[methodKey(tr.CandidatePkgPath, tr.CandidateType, tr.CandidateMethod)]
		if !ok {
			stats.DroppedMissingSymbol++
			continue
		}
		toSym, ok = lookup.byMethod[methodKey(tr.InterfacePkgPath, tr.InterfaceType, tr.InterfaceMethod)]
		if !ok {
			stats.DroppedMissingSymbol++
			continue
		}
		rels = append(rels, Relationship{FromSymbol: fromSym, ToSymbol: toSym, IsImplementation: true})
		stats.MethodLevelEmitted++
	}

	return rels, stats, nil
}
