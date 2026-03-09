package verification

import (
	"context"
	"fmt"

	models "github.com/context-maximiser/code-graph/libs/core-models-go"
	"github.com/context-maximiser/code-graph/libs/intelligence-go/contracts"
)

// GraphResolver checks whether a node key exists in the visible scope.
type GraphResolver interface {
	// NodeExists returns true if the node is visible in the given scope.
	NodeExists(ctx context.Context, nodeKey string, scope models.ScopeContext) (bool, error)
}

// Verifier checks that every citation in a GenerationResult resolves to
// existing graph evidence in the visible scope.
type Verifier struct {
	resolver GraphResolver
}

// NewVerifier creates a citation verifier.
func NewVerifier(resolver GraphResolver) *Verifier {
	return &Verifier{resolver: resolver}
}

// Verify checks all citations in the generation result.
// Implements contracts.Verifier.
func (v *Verifier) Verify(ctx context.Context, result *contracts.GenerationResult, scope models.ScopeContext) (*contracts.VerificationResult, error) {
	if result == nil {
		return &contracts.VerificationResult{Passed: true}, nil
	}

	totalStatements := len(result.Citations)
	cited := 0
	var unsupported []int
	var errors []string

	for _, citation := range result.Citations {
		if len(citation.EvidenceRefs) == 0 {
			unsupported = append(unsupported, citation.StatementIndex)
			continue
		}

		// Check each evidence ref resolves in scope
		allValid := true
		for _, ref := range citation.EvidenceRefs {
			if ref.NodeKey == "" {
				continue
			}

			if v.resolver != nil {
				exists, err := v.resolver.NodeExists(ctx, ref.NodeKey, scope)
				if err != nil {
					errors = append(errors, fmt.Sprintf("error checking %s: %v", ref.NodeKey, err))
					allValid = false
					continue
				}
				if !exists {
					errors = append(errors, fmt.Sprintf("citation ref %s not found in scope %s", ref.NodeKey, scope.ScopeID))
					allValid = false
				}
			}
		}

		if allValid {
			cited++
		} else {
			unsupported = append(unsupported, citation.StatementIndex)
		}
	}

	return &contracts.VerificationResult{
		Passed:            len(unsupported) == 0 && len(errors) == 0,
		TotalStatements:   totalStatements,
		CitedStatements:   cited,
		UnsupportedClaims: unsupported,
		Errors:            errors,
	}, nil
}

// UnsupportedClaimRate computes the fraction of statements without valid citations.
func UnsupportedClaimRate(vr *contracts.VerificationResult) float64 {
	if vr == nil || vr.TotalStatements == 0 {
		return 0
	}
	return float64(len(vr.UnsupportedClaims)) / float64(vr.TotalStatements)
}
