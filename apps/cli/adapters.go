package main

import (
	"context"

	models "github.com/context-maximiser/code-graph/libs/core-models-go"
	"github.com/context-maximiser/code-graph/libs/indexer-go/generated"
	"github.com/context-maximiser/code-graph/libs/intelligence-go/contracts"
	"github.com/context-maximiser/code-graph/libs/llm-go"
	"github.com/context-maximiser/code-graph/libs/query-go"
	"github.com/context-maximiser/code-graph/libs/verification-go"
)

// llmClientAdapter adapts llm.LLMProvider to generation.LLMClient.
type llmClientAdapter struct {
	provider llm.LLMProvider
}

func (a *llmClientAdapter) Complete(ctx context.Context, prompt string, _ int) (string, error) {
	return a.provider.GenerateText(ctx, prompt)
}

func (a *llmClientAdapter) ModelName() string {
	return a.provider.Name()
}

// neo4jGraphResolver adapts query.OverlayResolver to verification.GraphResolver.
type neo4jGraphResolver struct {
	overlay *query.OverlayResolver
}

func (r *neo4jGraphResolver) NodeExists(ctx context.Context, nodeKey string, scope models.ScopeContext) (bool, error) {
	result, err := r.overlay.ResolveNode(ctx, nodeKey, scope.ScopeID)
	if err != nil {
		return false, err
	}
	return result != nil, nil
}

// policyGateAdapter adapts verification.PolicyGate to generated.PolicyEvaluator.
type policyGateAdapter struct {
	gate *verification.PolicyGate
}

func (a *policyGateAdapter) Evaluate(gen *contracts.GenerationResult, ver *contracts.VerificationResult) generated.PolicyDecision {
	decision := a.gate.Evaluate(gen, ver)
	var violations []string
	if decision.Diagnostics != nil {
		violations = decision.Diagnostics.PolicyViolations
	}
	return generated.PolicyDecision{
		Allowed:          decision.Allowed,
		Reason:           decision.Reason,
		PolicyViolations: violations,
	}
}
