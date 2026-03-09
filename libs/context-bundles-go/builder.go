package bundles

import (
	"context"
	"sort"

	models "github.com/context-maximiser/code-graph/libs/core-models-go"
	"github.com/context-maximiser/code-graph/libs/intelligence-go/contracts"
)

// GraphExpander provides graph-based expansion of anchors.
// Implementations query the graph database for connected nodes.
type GraphExpander interface {
	// Expand returns nodes connected to the given anchor within budget constraints.
	// depth is the current expansion depth, maxPerAnchor caps how many to return.
	Expand(ctx context.Context, anchorKey string, depth int, maxPerAnchor int, scope models.ScopeContext) ([]ExpansionNode, error)
}

// ExpansionNode represents a node discovered during graph expansion.
type ExpansionNode struct {
	NodeKey      string  `json:"nodeKey"`
	NodeType     string  `json:"nodeType"`
	Name         string  `json:"name"`
	RelationType string  `json:"relationType"` // The edge type that connected it to the anchor
	Depth        int     `json:"depth"`         // How many hops from the anchor
	AnchorKey    string  `json:"anchorKey"`     // Which anchor this was expanded from
	Score        float64 `json:"score"`         // Relevance/importance score
}

// InferenceProvider supplies inference results for the bundle.
type InferenceProvider interface {
	InferForAnchors(ctx context.Context, anchors []contracts.RetrievalCandidate, scope models.ScopeContext) ([]contracts.InferenceResult, error)
}

// TokenEstimator estimates token counts for bundle items.
type TokenEstimator interface {
	EstimateTokens(candidate contracts.RetrievalCandidate) int
}

// defaultTokenEstimator provides a rough estimate based on metadata size.
type defaultTokenEstimator struct{}

func (d *defaultTokenEstimator) EstimateTokens(c contracts.RetrievalCandidate) int {
	// Base cost per node
	tokens := 50
	if sig, ok := c.Metadata["signature"].(string); ok {
		tokens += len(sig) / 4
	}
	if body, ok := c.Metadata["body"].(string); ok {
		tokens += len(body) / 4
	}
	if doc, ok := c.Metadata["docstring"].(string); ok {
		tokens += len(doc) / 4
	}
	return tokens
}

// Builder assembles bounded context bundles from anchors.
// It enforces expansion budgets and produces deterministic output ordering.
type Builder struct {
	budget            ExpansionBudget
	expander          GraphExpander
	inferenceProvider InferenceProvider
	tokenEstimator    TokenEstimator
}

// NewBuilder creates a bundle builder with the default budget.
func NewBuilder() *Builder {
	return &Builder{
		budget:         DefaultExpansionBudget,
		tokenEstimator: &defaultTokenEstimator{},
	}
}

// WithBudget sets a custom expansion budget.
func (b *Builder) WithBudget(budget ExpansionBudget) *Builder {
	b.budget = budget
	return b
}

// WithExpander sets the graph expansion provider.
func (b *Builder) WithExpander(expander GraphExpander) *Builder {
	b.expander = expander
	return b
}

// WithInferenceProvider sets the inference provider.
func (b *Builder) WithInferenceProvider(provider InferenceProvider) *Builder {
	b.inferenceProvider = provider
	return b
}

// WithTokenEstimator sets a custom token estimator.
func (b *Builder) WithTokenEstimator(estimator TokenEstimator) *Builder {
	b.tokenEstimator = estimator
	return b
}

// Build assembles a ContextBundle from the given anchors. Implements contracts.BundleBuilder.
func (b *Builder) Build(ctx context.Context, anchors []contracts.RetrievalCandidate, template string, scope models.ScopeContext) (*contracts.ContextBundle, error) {
	if len(anchors) == 0 {
		return &contracts.ContextBundle{
			Template: template,
			Scope:    scope.Scope,
			ScopeID:  scope.ScopeID,
		}, nil
	}

	// 1. Enforce anchor limit and sort deterministically
	truncatedAnchors := b.truncateAnchors(anchors)

	// 2. Sort anchors deterministically by nodeKey for stable output
	sortCandidatesByKey(truncatedAnchors)

	// 3. Expand anchors within budget
	expansions, err := b.expandAnchors(ctx, truncatedAnchors, scope)
	if err != nil {
		return nil, err
	}

	// 4. Sort expansions deterministically
	sortCandidatesByKey(expansions)

	// 5. Enforce token budget
	truncatedAnchors, expansions = b.enforceTokenBudget(truncatedAnchors, expansions)

	// 6. Collect inference results if provider available
	var inferences []contracts.InferenceResult
	if b.inferenceProvider != nil {
		inferences, err = b.inferenceProvider.InferForAnchors(ctx, truncatedAnchors, scope)
		if err != nil {
			// Graceful degradation: continue without inferences
			inferences = nil
		}
	}

	// 7. Sort inferences deterministically
	sortInferencesByKey(inferences)

	// 8. Compute max tokens from budget
	maxTokens := b.budget.MaxBundleTokens
	if maxTokens == 0 {
		maxTokens = 4000
	}

	return &contracts.ContextBundle{
		Anchors:    truncatedAnchors,
		Expansions: expansions,
		Inferences: inferences,
		Template:   template,
		MaxTokens:  maxTokens,
		Scope:      scope.Scope,
		ScopeID:    scope.ScopeID,
	}, nil
}

// truncateAnchors enforces the MaxTotalAnchors budget, keeping highest-scored.
func (b *Builder) truncateAnchors(anchors []contracts.RetrievalCandidate) []contracts.RetrievalCandidate {
	if b.budget.MaxTotalAnchors <= 0 || len(anchors) <= b.budget.MaxTotalAnchors {
		result := make([]contracts.RetrievalCandidate, len(anchors))
		copy(result, anchors)
		return result
	}

	// Sort by score descending, take top N
	sorted := make([]contracts.RetrievalCandidate, len(anchors))
	copy(sorted, anchors)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Score > sorted[j].Score
	})
	return sorted[:b.budget.MaxTotalAnchors]
}

// expandAnchors performs graph expansion within budget constraints.
func (b *Builder) expandAnchors(ctx context.Context, anchors []contracts.RetrievalCandidate, scope models.ScopeContext) ([]contracts.RetrievalCandidate, error) {
	if b.expander == nil {
		return nil, nil
	}

	var allExpansions []contracts.RetrievalCandidate
	seen := make(map[string]bool)

	// Mark anchors as seen to avoid duplicates
	for _, a := range anchors {
		seen[a.NodeKey] = true
	}

	totalBudget := b.budget.MaxTotalExpansions
	if totalBudget <= 0 {
		totalBudget = 20
	}

	for _, anchor := range anchors {
		if len(allExpansions) >= totalBudget {
			break
		}

		perAnchor := b.budget.MaxExpansionsPerAnchor
		if perAnchor <= 0 {
			perAnchor = 5
		}
		remaining := totalBudget - len(allExpansions)
		if perAnchor > remaining {
			perAnchor = remaining
		}

		depth := b.budget.MaxExpansionDepth
		if depth <= 0 {
			depth = 2
		}

		nodes, err := b.expander.Expand(ctx, anchor.NodeKey, depth, perAnchor, scope)
		if err != nil {
			continue // Graceful degradation per anchor
		}

		for _, node := range nodes {
			if seen[node.NodeKey] {
				continue
			}
			if !b.budget.IsNodeTypeAllowed(node.NodeType) {
				continue
			}
			if !b.budget.IsRelationAllowed(node.RelationType) {
				continue
			}
			if len(allExpansions) >= totalBudget {
				break
			}

			seen[node.NodeKey] = true
			allExpansions = append(allExpansions, contracts.RetrievalCandidate{
				NodeKey:  node.NodeKey,
				NodeType: node.NodeType,
				Scope:    scope.Scope,
				ScopeID:  scope.ScopeID,
				Score:    node.Score,
				Source:   "expansion",
				Metadata: map[string]any{
					"anchorKey":    node.AnchorKey,
					"relationType": node.RelationType,
					"depth":        node.Depth,
					"name":         node.Name,
				},
			})
		}
	}

	return allExpansions, nil
}

// enforceTokenBudget trims expansions (then anchors) to fit within MaxBundleTokens.
func (b *Builder) enforceTokenBudget(anchors, expansions []contracts.RetrievalCandidate) ([]contracts.RetrievalCandidate, []contracts.RetrievalCandidate) {
	if b.budget.MaxBundleTokens <= 0 {
		return anchors, expansions
	}

	tokensUsed := 0

	// Anchors get priority
	var keptAnchors []contracts.RetrievalCandidate
	for _, a := range anchors {
		est := b.tokenEstimator.EstimateTokens(a)
		if tokensUsed+est > b.budget.MaxBundleTokens {
			break
		}
		tokensUsed += est
		keptAnchors = append(keptAnchors, a)
	}

	// Expansions fill remaining budget
	var keptExpansions []contracts.RetrievalCandidate
	for _, e := range expansions {
		est := b.tokenEstimator.EstimateTokens(e)
		if tokensUsed+est > b.budget.MaxBundleTokens {
			break
		}
		tokensUsed += est
		keptExpansions = append(keptExpansions, e)
	}

	return keptAnchors, keptExpansions
}

// sortCandidatesByKey sorts candidates by NodeKey for deterministic ordering.
func sortCandidatesByKey(candidates []contracts.RetrievalCandidate) {
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].NodeKey < candidates[j].NodeKey
	})
}

// sortInferencesByKey sorts inferences by SourceKey+TargetKey for deterministic ordering.
func sortInferencesByKey(inferences []contracts.InferenceResult) {
	sort.Slice(inferences, func(i, j int) bool {
		if inferences[i].SourceKey != inferences[j].SourceKey {
			return inferences[i].SourceKey < inferences[j].SourceKey
		}
		return inferences[i].TargetKey < inferences[j].TargetKey
	})
}
