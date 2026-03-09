package retrieval

import (
	"context"
	"strings"

	models "github.com/context-maximiser/code-graph/libs/core-models-go"
	"github.com/context-maximiser/code-graph/libs/intelligence-go/contracts"
)

// TombstoneChecker checks whether a node has been tombstoned in a given scope.
type TombstoneChecker interface {
	// IsTombstoned returns true if the node identified by nodeKey is tombstoned
	// in the given scopeID.
	IsTombstoned(ctx context.Context, nodeKey string, scopeID string) (bool, error)
}

// DefaultOverlayFilter implements OverlayFilter with deterministic precedence:
// 1. Overlay (PR-scope) candidates take priority over main candidates for the same nodeKey.
// 2. Main candidates whose nodeKey is tombstoned in the overlay scope are hidden.
// 3. Remaining main candidates are included as fallback.
type DefaultOverlayFilter struct {
	tombstones TombstoneChecker
}

// NewDefaultOverlayFilter creates an overlay filter with a tombstone checker.
func NewDefaultOverlayFilter(tc TombstoneChecker) *DefaultOverlayFilter {
	return &DefaultOverlayFilter{tombstones: tc}
}

// FilterCandidates applies overlay precedence rules to a set of candidates.
func (f *DefaultOverlayFilter) FilterCandidates(ctx context.Context, candidates []contracts.RetrievalCandidate, scope models.ScopeContext) ([]contracts.RetrievalCandidate, error) {
	if scope.Scope != models.ScopePR || scope.ScopeID == "" || scope.ScopeID == models.ScopeMain {
		// No overlay filtering needed for main scope
		return candidates, nil
	}

	// Group candidates by normalized nodeKey
	type keyEntry struct {
		overlay *contracts.RetrievalCandidate
		main    *contracts.RetrievalCandidate
	}
	byKey := make(map[string]*keyEntry)
	var order []string // preserve insertion order

	for i := range candidates {
		c := &candidates[i]
		key := stripScopePrefix(c.NodeKey)

		e, ok := byKey[key]
		if !ok {
			e = &keyEntry{}
			byKey[key] = e
			order = append(order, key)
		}

		if c.ScopeID == scope.ScopeID {
			// This candidate is from the overlay scope
			if e.overlay == nil || c.Score > e.overlay.Score {
				e.overlay = c
			}
		} else {
			// Main scope candidate
			if e.main == nil || c.Score > e.main.Score {
				e.main = c
			}
		}
	}

	// Apply precedence rules
	result := make([]contracts.RetrievalCandidate, 0, len(order))
	for _, key := range order {
		e := byKey[key]

		// Rule 1: Overlay wins
		if e.overlay != nil {
			result = append(result, *e.overlay)
			continue
		}

		// Rule 2: Check tombstone for main candidates
		if e.main != nil && f.tombstones != nil {
			tombstoned, err := f.tombstones.IsTombstoned(ctx, key, scope.ScopeID)
			if err != nil {
				return nil, err
			}
			if tombstoned {
				continue // Hidden
			}
		}

		// Rule 3: Main fallback
		if e.main != nil {
			result = append(result, *e.main)
		}
	}

	return result, nil
}

// stripScopePrefix removes a scopeId:: prefix from a nodeKey.
func stripScopePrefix(key string) string {
	if idx := strings.Index(key, "::"); idx >= 0 {
		return key[idx+2:]
	}
	return key
}

// NoopOverlayFilter is an overlay filter that does nothing (pass-through).
// Use when overlay filtering is not needed (e.g., main scope only).
type NoopOverlayFilter struct{}

// FilterCandidates returns candidates unchanged.
func (f *NoopOverlayFilter) FilterCandidates(_ context.Context, candidates []contracts.RetrievalCandidate, _ models.ScopeContext) ([]contracts.RetrievalCandidate, error) {
	return candidates, nil
}
