package search

import (
	"context"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"

	"github.com/context-maximiser/code-graph/libs/neo4j-go"
	textindex "github.com/context-maximiser/code-graph/libs/text-index-client-go"
	neo4jdriver "github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// HybridSearchManager combines BM25 full-text search with graph-based semantic
// search, fusing results with Reciprocal Rank Fusion (RRF).
type HybridSearchManager struct {
	client         *neo4j.Client
	textStore      textindex.TextIndexStore // Optional pluggable text index (e.g. Neo4j, OpenSearch)
	fullTextSearch *FullTextSearchManager
	queryBuilder   *neo4j.QueryBuilder
	scopeID        string // Scope for overlay-aware queries (e.g. "main", "pr-42").
}

// NewHybridSearchManager creates a hybrid search manager combining full-text
// and graph-based semantic search.
func NewHybridSearchManager(client *neo4j.Client) *HybridSearchManager {
	return &HybridSearchManager{
		client:         client,
		fullTextSearch: NewFullTextSearchManager(client),
		queryBuilder:   neo4j.NewQueryBuilder(client),
	}
}

// SetScope configures the scope for overlay-aware retrieval.
func (hsm *HybridSearchManager) SetScope(scopeID string) {
	hsm.scopeID = scopeID
}

// WithTextStore sets an optional TextIndexStore for pluggable fulltext search.
// When non-nil, Search() uses textStore for BM25 results instead of the built-in
// FullTextSearchManager. This enables swapping Neo4j fulltext for OpenSearch or mocks.
func (hsm *HybridSearchManager) WithTextStore(ts textindex.TextIndexStore) *HybridSearchManager {
	hsm.textStore = ts
	return hsm
}

// HybridSearchResult represents a unified search result with multiple scores
type HybridSearchResult struct {
	Node          map[string]interface{} `json:"node"`
	Labels        []string               `json:"labels"`
	FullTextScore float64                `json:"fullTextScore"`
	SemanticScore float64                `json:"semanticScore"`
	CombinedScore float64                `json:"combinedScore"`
	Source        string                 `json:"source"` // "fulltext", "semantic", "hybrid"
	Relevance     string                 `json:"relevance"` // "high", "medium", "low"
}

// HybridSearchResponse contains comprehensive search results
type HybridSearchResponse struct {
	Results      []HybridSearchResult `json:"results"`
	Query        string               `json:"query"`
	SearchTypes  []string             `json:"searchTypes"`
	TotalResults int                  `json:"totalResults"`
	Metadata     SearchMetadata       `json:"metadata"`
}

// SearchMetadata provides information about the search execution
type SearchMetadata struct {
	FullTextResults int     `json:"fullTextResults"`
	SemanticResults int     `json:"semanticResults"`
	SearchDuration  string  `json:"searchDuration"`
	HybridWeight    Weights `json:"hybridWeight"`
}

// Weights for combining different search results
type Weights struct {
	FullText float64 `json:"fullText"`
	Semantic float64 `json:"semantic"`
}

// DefaultWeights provides balanced scoring weights
var DefaultWeights = Weights{
	FullText: 0.7, // BM25 relevance
	Semantic: 0.3, // Graph-based semantic search
}

// InitializeSearchIndexes creates all necessary indexes for hybrid search
func (hsm *HybridSearchManager) InitializeSearchIndexes(ctx context.Context) error {
	log.Println("🚀 Initializing hybrid search indexes...")

	// Create full-text indexes in Neo4j
	if err := hsm.fullTextSearch.CreateFullTextIndexes(ctx); err != nil {
		log.Printf("Warning: failed to create full-text indexes: %v", err)
	}

	log.Println("✓ Hybrid search indexes initialization completed")
	return nil
}

// rrfScore returns the Reciprocal Rank Fusion contribution for rank r (1-indexed).
// k=60 is the standard constant that balances the influence of top vs. lower ranks.
func rrfScore(r int) float64 {
	return 1.0 / (60.0 + float64(r))
}

// UnifiedSearch performs full-text and graph-based semantic search, merging
// results using Reciprocal Rank Fusion (RRF), which correctly combines scores
// from different scales (BM25 2-20, graph heuristic 0-1, etc.).
func (hsm *HybridSearchManager) UnifiedSearch(ctx context.Context, query string, limit int, weights ...Weights) (*HybridSearchResponse, error) {
	if limit <= 0 {
		limit = 20
	}

	// Use provided weights or defaults
	searchWeights := DefaultWeights
	if len(weights) > 0 {
		searchWeights = weights[0]
	}

	// rrfEntry accumulates per-source scores and rank contributions for a node.
	type rrfEntry struct {
		result HybridSearchResult
		rrf    float64
	}
	byKey := make(map[string]*rrfEntry)

	addEntry := func(key string, result HybridSearchResult, rank int) {
		e, ok := byKey[key]
		if !ok {
			e = &rrfEntry{result: result}
			byKey[key] = e
		}
		// Merge node: prefer the one with more properties.
		if len(result.Node) > len(e.result.Node) {
			e.result.Node = result.Node
		}
		// Merge labels.
		e.result.Labels = hsm.mergeLabels(e.result.Labels, result.Labels)
		// Keep highest raw scores per source.
		e.result.FullTextScore = math.Max(e.result.FullTextScore, result.FullTextScore)
		e.result.SemanticScore = math.Max(e.result.SemanticScore, result.SemanticScore)
		// Accumulate RRF score (scale by source weight).
		var w float64
		switch result.Source {
		case "fulltext":
			w = searchWeights.FullText
		case "semantic":
			w = searchWeights.Semantic
		default:
			w = 0.5
		}
		e.rrf += rrfScore(rank) * w
	}

	// 1. Full-Text Search (BM25)
	// When a pluggable TextIndexStore is wired in, prefer it over the built-in
	// FullTextSearchManager. This lets callers swap Neo4j fulltext for OpenSearch
	// or a mock without changing search logic.
	var fullTextResults []FullTextSearchResult
	if hsm.textStore != nil {
		tsResults, tsErr := hsm.textStore.Search(ctx, query, textindex.SearchOpts{Limit: limit, ScopeID: hsm.scopeID})
		if tsErr != nil {
			log.Printf("Warning: text store search failed: %v", tsErr)
		} else {
			fullTextResults = make([]FullTextSearchResult, 0, len(tsResults))
			for i, r := range tsResults {
				node := map[string]interface{}{
					"nodeKey": r.NodeKey,
					"snippet": r.Snippet,
				}
				for k, v := range r.Metadata {
					node[k] = v
				}
				hr := HybridSearchResult{
					Node:          node,
					FullTextScore: r.Score,
					Source:        "fulltext",
					Relevance:     hsm.calculateRelevance(r.Score, "fulltext"),
				}
				key := hsm.getResultKey(node)
				addEntry(key, hr, i+1)
				fullTextResults = append(fullTextResults, FullTextSearchResult{
					Node:  node,
					Score: r.Score,
				})
			}
		}
	} else {
		fullTextResponse, ftErr := hsm.fullTextSearch.HybridFullTextSearch(ctx, query, limit)
		if ftErr != nil {
			log.Printf("Warning: full-text search failed: %v", ftErr)
		} else {
			fullTextResults = fullTextResponse.Results
			for i, result := range fullTextResults {
				hr := HybridSearchResult{
					Node:          result.Node,
					Labels:        result.Labels,
					FullTextScore: result.Score,
					Source:        "fulltext",
					Relevance:     hsm.calculateRelevance(result.Score, "fulltext"),
				}
				key := hsm.getResultKey(result.Node)
				addEntry(key, hr, i+1)
			}
		}
	}

	// 2. Semantic Search (graph-based name/description matching)
	nodeLabels := []string{"Function", "Method", "Class", "Document", "Feature", "Symbol"}
	var semanticResults []*neo4jdriver.Record
	var semanticErr error
	if hsm.scopeID != "" && hsm.scopeID != "main" {
		semanticResults, semanticErr = hsm.queryBuilder.SearchNodesScoped(ctx, query, nodeLabels, limit, hsm.scopeID)
	} else {
		semanticResults, semanticErr = hsm.queryBuilder.SearchNodes(ctx, query, nodeLabels, limit)
	}
	if semanticErr != nil {
		log.Printf("Warning: semantic search failed: %v", semanticErr)
	} else {
		for i, record := range semanticResults {
			recordMap := record.AsMap()
			node, ok := recordMap["n"]
			if !ok {
				continue
			}
			var labels []string
			if labelsList, ok := recordMap["nodeLabels"].([]interface{}); ok {
				for _, label := range labelsList {
					if ls, ok := label.(string); ok {
						labels = append(labels, ls)
					}
				}
			}
			nodeMap := make(map[string]interface{})
			if nodeObj, ok := node.(neo4jdriver.Node); ok {
				nodeMap = nodeObj.Props
				nodeMap["elementId"] = nodeObj.ElementId
			} else if nodeData, ok := node.(map[string]interface{}); ok {
				nodeMap = nodeData
			}
			semanticScore := hsm.calculateSemanticRelevance(nodeMap, query)
			hr := HybridSearchResult{
				Node:          nodeMap,
				Labels:        labels,
				SemanticScore: semanticScore,
				Source:        "semantic",
				Relevance:     hsm.calculateRelevance(semanticScore, "semantic"),
			}
			key := hsm.getResultKey(nodeMap)
			addEntry(key, hr, i+1)
		}
	}

	// 3. Build final result slice from RRF scores.
	mergedResults := make([]HybridSearchResult, 0, len(byKey))
	for _, e := range byKey {
		e.result.CombinedScore = e.rrf
		// Determine source label.
		sourceCount := 0
		for _, s := range []float64{e.result.FullTextScore, e.result.SemanticScore} {
			if s > 0 {
				sourceCount++
			}
		}
		if sourceCount > 1 {
			e.result.Source = "hybrid"
		}
		e.result.Relevance = hsm.calculateRelevance(e.rrf, "rrf")
		mergedResults = append(mergedResults, e.result)
	}

	// 4. Sort by RRF combined score descending.
	sort.Slice(mergedResults, func(i, j int) bool {
		return mergedResults[i].CombinedScore > mergedResults[j].CombinedScore
	})

	// 5. Limit results.
	if len(mergedResults) > limit {
		mergedResults = mergedResults[:limit]
	}

	response := &HybridSearchResponse{
		Results:      mergedResults,
		Query:        query,
		SearchTypes:  buildSearchTypes(len(fullTextResults) > 0, len(semanticResults) > 0),
		TotalResults: len(mergedResults),
		Metadata: SearchMetadata{
			FullTextResults: len(fullTextResults),
			SemanticResults: len(semanticResults),
			HybridWeight:    searchWeights,
		},
	}

	return response, nil
}

// getResultKey generates a stable unique key for result deduplication.
// Priority: nodeKey (most stable) > elementId > name+type fallback.
// Strips any scopeId:: prefix so that overlay and main entries merge.
func (hsm *HybridSearchManager) getResultKey(node map[string]interface{}) string {
	if nk, ok := node["nodeKey"].(string); ok && nk != "" {
		// Strip scopeId:: prefix if present.
		if idx := strings.Index(nk, "::"); idx >= 0 {
			return nk[idx+2:]
		}
		return nk
	}
	if id, ok := node["elementId"].(string); ok && id != "" {
		return id
	}

	name, _ := node["name"].(string)
	nodeType := "unknown"
	if _, ok := node["signature"]; ok {
		nodeType = "function"
	} else if _, ok := node["content"]; ok {
		nodeType = "document"
	} else if _, ok := node["path"]; ok {
		nodeType = "file"
	}
	return fmt.Sprintf("%s_%s", nodeType, name)
}

// mergeLabels combines label arrays, removing duplicates
func (hsm *HybridSearchManager) mergeLabels(labels1, labels2 []string) []string {
	labelSet := make(map[string]bool)
	for _, label := range labels1 {
		labelSet[label] = true
	}
	for _, label := range labels2 {
		labelSet[label] = true
	}

	var merged []string
	for label := range labelSet {
		merged = append(merged, label)
	}

	return merged
}

// calculateRelevance determines relevance level based on score and source.
// RRF scores top out around 1/(60+1) ≈ 0.016 per source; two sources ≈ 0.032.
func (hsm *HybridSearchManager) calculateRelevance(score float64, source string) string {
	switch source {
	case "fulltext":
		if score > 5.0 {
			return "high"
		} else if score > 2.0 {
			return "medium"
		}
		return "low"
	case "semantic":
		if score > 0.7 {
			return "high"
		} else if score > 0.4 {
			return "medium"
		}
		return "low"
	case "rrf", "hybrid":
		// RRF: single-source max ≈ 0.016, dual ≈ 0.032 (unweighted).
		// With weights 0.7/0.3 these scale accordingly.
		if score > 0.010 {
			return "high"
		} else if score > 0.006 {
			return "medium"
		}
		return "low"
	default:
		return "low"
	}
}

// calculateSemanticRelevance calculates a simple relevance score for semantic search
func (hsm *HybridSearchManager) calculateSemanticRelevance(node map[string]interface{}, query string) float64 {
	query = strings.ToLower(query)
	score := 0.0

	// Check name field
	if name, ok := node["name"].(string); ok {
		if strings.Contains(strings.ToLower(name), query) {
			score += 1.0
		}
	}

	// Check description field
	if description, ok := node["description"].(string); ok {
		if strings.Contains(strings.ToLower(description), query) {
			score += 0.5
		}
	}

	// Check signature field
	if signature, ok := node["signature"].(string); ok {
		if strings.Contains(strings.ToLower(signature), query) {
			score += 0.3
		}
	}

	// Check content field (for documents)
	if content, ok := node["content"].(string); ok {
		if strings.Contains(strings.ToLower(content), query) {
			score += 0.4
		}
	}

	return math.Min(score, 1.0) // Cap at 1.0
}

// SmartSearch automatically selects the best search strategy based on query characteristics
func (hsm *HybridSearchManager) SmartSearch(ctx context.Context, query string, limit int) (*HybridSearchResponse, error) {
	// Analyze query to determine optimal search strategy
	strategy := hsm.analyzeQuery(query)

	switch strategy {
	case "code-focused":
		return hsm.UnifiedSearch(ctx, query, limit, Weights{FullText: 0.8, Semantic: 0.2})
	case "concept-focused":
		return hsm.UnifiedSearch(ctx, query, limit, Weights{FullText: 0.4, Semantic: 0.6})
	case "document-focused":
		return hsm.UnifiedSearch(ctx, query, limit, Weights{FullText: 0.9, Semantic: 0.1})
	default:
		return hsm.UnifiedSearch(ctx, query, limit)
	}
}

// analyzeQuery determines the optimal search strategy based on query content
func (hsm *HybridSearchManager) analyzeQuery(query string) string {
	query = strings.ToLower(query)

	// Code-specific keywords
	codeKeywords := []string{
		"function", "method", "class", "variable", "struct", "interface",
		"return", "parameter", "implements", "extends", "private", "public",
	}

	// Conceptual keywords
	conceptKeywords := []string{
		"how to", "what is", "why", "when", "where", "concept", "idea",
		"approach", "strategy", "pattern", "best practice",
	}

	// Document keywords
	docKeywords := []string{
		"documentation", "guide", "tutorial", "example", "readme",
		"specification", "requirements", "design", "architecture",
	}

	codeScore := 0
	conceptScore := 0
	docScore := 0

	for _, keyword := range codeKeywords {
		if strings.Contains(query, keyword) {
			codeScore++
		}
	}

	for _, keyword := range conceptKeywords {
		if strings.Contains(query, keyword) {
			conceptScore++
		}
	}

	for _, keyword := range docKeywords {
		if strings.Contains(query, keyword) {
			docScore++
		}
	}

	if codeScore > conceptScore && codeScore > docScore {
		return "code-focused"
	} else if conceptScore > docScore {
		return "concept-focused"
	} else if docScore > 0 {
		return "document-focused"
	}

	return "balanced"
}

// GetSearchCapabilities returns information about available search capabilities
func (hsm *HybridSearchManager) GetSearchCapabilities(ctx context.Context) (map[string]interface{}, error) {
	capabilities := make(map[string]interface{})

	// Get full-text index info
	fullTextInfo, err := hsm.fullTextSearch.GetFullTextIndexInfo(ctx)
	if err != nil {
		log.Printf("Warning: failed to get full-text index info: %v", err)
	} else {
		capabilities["fullTextSearch"] = fullTextInfo
	}

	capabilities["hybridSearch"] = map[string]interface{}{
		"supportedMethods": []string{"fulltext", "semantic", "hybrid"},
		"defaultWeights":   DefaultWeights,
		"smartSearch":      true,
	}

	return capabilities, nil
}

// buildSearchTypes returns the list of active search type labels based on which
// paths returned results, so the response accurately reflects what ran.
func buildSearchTypes(fulltext, semantic bool) []string {
	types := []string{}
	if fulltext {
		types = append(types, "fulltext")
	}
	if semantic {
		types = append(types, "semantic")
	}
	return types
}
