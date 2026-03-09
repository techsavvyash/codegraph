package retrieval

import (
	"context"

	models "github.com/context-maximiser/code-graph/libs/core-models-go"
	"github.com/context-maximiser/code-graph/libs/intelligence-go/contracts"
)

// GraphAdapter retrieves candidates from the graph store (Neo4j).
// It wraps a GraphStore and normalizes results into RetrievalCandidate.
type GraphAdapter struct {
	store GraphStore
}

// GraphStore abstracts the graph database queries needed by the adapter.
type GraphStore interface {
	// SearchNodes finds nodes matching the query within the given scope.
	// Returns node properties including nodeKey, nodeType, and a relevance score.
	SearchNodes(ctx context.Context, query string, scope models.ScopeContext, limit int) ([]GraphResult, error)
}

// GraphResult is a raw result from the graph store before normalization.
type GraphResult struct {
	NodeKey  string
	NodeType string
	ScopeID  string // Actual scopeID of this result (may differ from request scope).
	Score    float64
	Props    map[string]any
}

// NewGraphAdapter creates a graph retrieval adapter.
func NewGraphAdapter(store GraphStore) *GraphAdapter {
	return &GraphAdapter{store: store}
}

// Retrieve searches the graph store and normalizes results into RetrievalCandidate.
func (a *GraphAdapter) Retrieve(ctx context.Context, query string, scope models.ScopeContext, limit int) ([]contracts.RetrievalCandidate, error) {
	results, err := a.store.SearchNodes(ctx, query, scope, limit)
	if err != nil {
		return nil, err
	}

	candidates := make([]contracts.RetrievalCandidate, 0, len(results))
	for _, r := range results {
		// Use the result's actual scopeID if available, otherwise fall back to request scope.
		scopeID := r.ScopeID
		candidateScope := scope.Scope
		if scopeID == "" {
			scopeID = scope.ScopeID
		} else if scopeID == models.ScopeMain {
			candidateScope = models.ScopeMain
		}
		candidates = append(candidates, contracts.RetrievalCandidate{
			NodeKey:  r.NodeKey,
			NodeType: r.NodeType,
			Scope:    candidateScope,
			ScopeID:  scopeID,
			Score:    r.Score,
			Source:   SourceGraph,
			Metadata: r.Props,
		})
	}
	return candidates, nil
}

// VectorAdapter retrieves candidates from a vector store (Qdrant, etc.).
type VectorAdapter struct {
	store     VectorStore
	embedder  Embedder
}

// VectorStore abstracts vector similarity search.
type VectorStore interface {
	// Query finds the k most similar vectors, with optional scope filtering.
	Query(ctx context.Context, vector []float64, limit int, filters map[string]any) ([]VectorResult, error)
}

// VectorResult is a raw result from the vector store before normalization.
type VectorResult struct {
	ID       string
	NodeKey  string
	NodeType string
	ScopeID  string // Actual scopeID of this result.
	Score    float64
	Metadata map[string]any
}

// Embedder generates embedding vectors from text.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float64, error)
}

// NewVectorAdapter creates a vector retrieval adapter.
func NewVectorAdapter(store VectorStore, embedder Embedder) *VectorAdapter {
	return &VectorAdapter{store: store, embedder: embedder}
}

// Retrieve embeds the query, searches the vector store with scope filters,
// and normalizes results into RetrievalCandidate.
func (a *VectorAdapter) Retrieve(ctx context.Context, query string, scope models.ScopeContext, limit int) ([]contracts.RetrievalCandidate, error) {
	vec, err := a.embedder.Embed(ctx, query)
	if err != nil {
		return nil, err
	}

	// Build scope filter: include results from this scope and main.
	filters := buildScopeFilters(scope)

	results, err := a.store.Query(ctx, vec, limit, filters)
	if err != nil {
		return nil, err
	}

	candidates := make([]contracts.RetrievalCandidate, 0, len(results))
	for _, r := range results {
		scopeID := r.ScopeID
		candidateScope := scope.Scope
		if scopeID == "" {
			scopeID = scope.ScopeID
		} else if scopeID == models.ScopeMain {
			candidateScope = models.ScopeMain
		}
		candidates = append(candidates, contracts.RetrievalCandidate{
			NodeKey:  r.NodeKey,
			NodeType: r.NodeType,
			Scope:    candidateScope,
			ScopeID:  scopeID,
			Score:    r.Score,
			Source:   SourceVector,
			Metadata: r.Metadata,
		})
	}
	return candidates, nil
}

// TextAdapter retrieves candidates from a text/BM25 index (OpenSearch, etc.).
type TextAdapter struct {
	store TextStore
}

// TextStore abstracts keyword/BM25 text search.
type TextStore interface {
	// Search performs a text search with scope filtering.
	Search(ctx context.Context, query string, scope models.ScopeContext, limit int) ([]TextResult, error)
}

// TextResult is a raw result from the text store before normalization.
type TextResult struct {
	NodeKey  string
	NodeType string
	ScopeID  string // Actual scopeID of this result.
	Score    float64
	Snippet  string
	Metadata map[string]any
}

// NewTextAdapter creates a text retrieval adapter.
func NewTextAdapter(store TextStore) *TextAdapter {
	return &TextAdapter{store: store}
}

// Retrieve searches the text store and normalizes results into RetrievalCandidate.
func (a *TextAdapter) Retrieve(ctx context.Context, query string, scope models.ScopeContext, limit int) ([]contracts.RetrievalCandidate, error) {
	results, err := a.store.Search(ctx, query, scope, limit)
	if err != nil {
		return nil, err
	}

	candidates := make([]contracts.RetrievalCandidate, 0, len(results))
	for _, r := range results {
		meta := r.Metadata
		if meta == nil {
			meta = make(map[string]any)
		}
		if r.Snippet != "" {
			meta["snippet"] = r.Snippet
		}
		scopeID := r.ScopeID
		candidateScope := scope.Scope
		if scopeID == "" {
			scopeID = scope.ScopeID
		} else if scopeID == models.ScopeMain {
			candidateScope = models.ScopeMain
		}
		candidates = append(candidates, contracts.RetrievalCandidate{
			NodeKey:  r.NodeKey,
			NodeType: r.NodeType,
			Scope:    candidateScope,
			ScopeID:  scopeID,
			Score:    r.Score,
			Source:   SourceText,
			Metadata: meta,
		})
	}
	return candidates, nil
}

// buildScopeFilters creates scope filter parameters for vector search.
// For PR scopes, we include both the PR scope and "main" to enable overlay merging.
func buildScopeFilters(scope models.ScopeContext) map[string]any {
	if scope.ScopeID == "" || scope.ScopeID == models.ScopeMain {
		return map[string]any{
			"scopeId": []string{models.ScopeMain},
		}
	}
	// PR scope: include both overlay and main for merging
	return map[string]any{
		"scopeId": []string{scope.ScopeID, models.ScopeMain},
	}
}
