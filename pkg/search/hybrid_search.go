package search

import (
	"context"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"

	"github.com/context-maximiser/code-graph/pkg/neo4j"
	neo4jdriver "github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// HybridSearchManager combines vector search, BM25 full-text search, and semantic search
type HybridSearchManager struct {
	client              *neo4j.Client
	vectorSearch        *VectorSearchManager
	fullTextSearch      *FullTextSearchManager
	queryBuilder        *neo4j.QueryBuilder
	embeddingService    EmbeddingService // Interface for generating embeddings
}

// NewHybridSearchManager creates a comprehensive hybrid search manager
func NewHybridSearchManager(client *neo4j.Client, embeddingService EmbeddingService) *HybridSearchManager {
	return &HybridSearchManager{
		client:           client,
		vectorSearch:     NewVectorSearchManager(client),
		fullTextSearch:   NewFullTextSearchManager(client),
		queryBuilder:     neo4j.NewQueryBuilder(client),
		embeddingService: embeddingService,
	}
}

// EmbeddingService interface for generating text embeddings
type EmbeddingService interface {
	GenerateEmbedding(ctx context.Context, text string) ([]float64, error)
	GenerateBatchEmbeddings(ctx context.Context, texts []string) ([][]float64, error)
}

// HybridSearchResult represents a unified search result with multiple scores
type HybridSearchResult struct {
	Node            map[string]interface{} `json:"node"`
	Labels          []string               `json:"labels"`
	VectorScore     float64                `json:"vectorScore"`
	FullTextScore   float64                `json:"fullTextScore"`
	SemanticScore   float64                `json:"semanticScore"`
	CombinedScore   float64                `json:"combinedScore"`
	Source          string                 `json:"source"` // "vector", "fulltext", "semantic", "hybrid"
	Relevance       string                 `json:"relevance"` // "high", "medium", "low"
}

// HybridSearchResponse contains comprehensive search results
type HybridSearchResponse struct {
	Results      []HybridSearchResult `json:"results"`
	Query        string               `json:"query"`
	QueryVector  []float64            `json:"queryVector,omitempty"`
	SearchTypes  []string             `json:"searchTypes"`
	TotalResults int                  `json:"totalResults"`
	Metadata     SearchMetadata       `json:"metadata"`
}

// SearchMetadata provides information about the search execution
type SearchMetadata struct {
	VectorResults    int     `json:"vectorResults"`
	FullTextResults  int     `json:"fullTextResults"`
	SemanticResults  int     `json:"semanticResults"`
	SearchDuration   string  `json:"searchDuration"`
	HybridWeight     Weights `json:"hybridWeight"`
}

// Weights for combining different search results
type Weights struct {
	Vector    float64 `json:"vector"`
	FullText  float64 `json:"fullText"`
	Semantic  float64 `json:"semantic"`
}

// DefaultWeights provides balanced scoring weights
var DefaultWeights = Weights{
	Vector:   0.4, // Semantic similarity
	FullText: 0.4, // BM25 relevance
	Semantic: 0.2, // Graph-based semantic search
}

// InitializeSearchIndexes creates all necessary indexes for hybrid search
func (hsm *HybridSearchManager) InitializeSearchIndexes(ctx context.Context) error {
	log.Println("🚀 Initializing hybrid search indexes...")

	// Create vector indexes
	if err := hsm.vectorSearch.CreateVectorIndexes(ctx); err != nil {
		log.Printf("Warning: failed to create vector indexes: %v", err)
	}

	// Create full-text indexes
	if err := hsm.fullTextSearch.CreateFullTextIndexes(ctx); err != nil {
		log.Printf("Warning: failed to create full-text indexes: %v", err)
	}

	log.Println("✓ Hybrid search indexes initialization completed")
	return nil
}

// UnifiedSearch performs comprehensive search using all available methods
func (hsm *HybridSearchManager) UnifiedSearch(ctx context.Context, query string, limit int, weights ...Weights) (*HybridSearchResponse, error) {
	if limit <= 0 {
		limit = 20
	}

	// Use provided weights or defaults
	searchWeights := DefaultWeights
	if len(weights) > 0 {
		searchWeights = weights[0]
	}

	var allResults []HybridSearchResult
	var queryVector []float64

	// 1. Vector Search (if embedding service available)
	var vectorResults []VectorSearchResult
	if hsm.embeddingService != nil {
		var err error
		queryVector, err = hsm.embeddingService.GenerateEmbedding(ctx, query)
		if err != nil {
			log.Printf("Warning: failed to generate query embedding: %v", err)
		} else {
			vectorResponse, err := hsm.vectorSearch.HybridVectorSearch(ctx, queryVector, limit)
			if err != nil {
				log.Printf("Warning: vector search failed: %v", err)
			} else {
				vectorResults = vectorResponse.Results
				for _, result := range vectorResults {
					allResults = append(allResults, HybridSearchResult{
						Node:          result.Node,
						VectorScore:   result.Score,
						CombinedScore: result.Score * searchWeights.Vector,
						Source:        "vector",
						Relevance:     hsm.calculateRelevance(result.Score, "vector"),
					})
				}
			}
		}
	}

	// 2. Full-Text Search (BM25)
	var fullTextResults []FullTextSearchResult
	fullTextResponse, err := hsm.fullTextSearch.HybridFullTextSearch(ctx, query, limit)
	if err != nil {
		log.Printf("Warning: full-text search failed: %v", err)
	} else {
		fullTextResults = fullTextResponse.Results
		for _, result := range fullTextResults {
			allResults = append(allResults, HybridSearchResult{
				Node:          result.Node,
				Labels:        result.Labels,
				FullTextScore: result.Score,
				CombinedScore: result.Score * searchWeights.FullText,
				Source:        "fulltext",
				Relevance:     hsm.calculateRelevance(result.Score, "fulltext"),
			})
		}
	}

	// 3. Semantic Search (existing graph-based search)
	semanticResults, err := hsm.queryBuilder.SearchNodes(ctx, query, []string{"Function", "Method", "Class", "Document", "Feature", "Symbol"}, limit)
	if err != nil {
		log.Printf("Warning: semantic search failed: %v", err)
	} else {
		for _, record := range semanticResults {
			recordMap := record.AsMap()
			if node, ok := recordMap["n"]; ok {
				labels := []string{}
				if labelsList, ok := recordMap["nodeLabels"].([]interface{}); ok {
					for _, label := range labelsList {
						if labelStr, ok := label.(string); ok {
							labels = append(labels, labelStr)
						}
					}
				}

				nodeMap := make(map[string]interface{})
				if nodeObj, ok := node.(neo4jdriver.Node); ok {
					// Extract all properties from the Neo4j Node object
					nodeMap = nodeObj.Props
				} else if nodeData, ok := node.(map[string]interface{}); ok {
					nodeMap = nodeData
				}

				// Simple relevance scoring for semantic search
				semanticScore := hsm.calculateSemanticRelevance(nodeMap, query)

				allResults = append(allResults, HybridSearchResult{
					Node:          nodeMap,
					Labels:        labels,
					SemanticScore: semanticScore,
					CombinedScore: semanticScore * searchWeights.Semantic,
					Source:        "semantic",
					Relevance:     hsm.calculateRelevance(semanticScore, "semantic"),
				})
			}
		}
	}

	// 4. Merge and deduplicate results
	mergedResults := hsm.mergeResults(allResults)

	// 5. Sort by combined score
	sort.Slice(mergedResults, func(i, j int) bool {
		return mergedResults[i].CombinedScore > mergedResults[j].CombinedScore
	})

	// 6. Limit results
	if len(mergedResults) > limit {
		mergedResults = mergedResults[:limit]
	}

	// 7. Build response
	response := &HybridSearchResponse{
		Results:     mergedResults,
		Query:       query,
		QueryVector: queryVector,
		SearchTypes: []string{"vector", "fulltext", "semantic"},
		TotalResults: len(mergedResults),
		Metadata: SearchMetadata{
			VectorResults:   len(vectorResults),
			FullTextResults: len(fullTextResults),
			SemanticResults: len(semanticResults),
			HybridWeight:    searchWeights,
		},
	}

	return response, nil
}

// mergeResults combines results from different search methods, handling duplicates
func (hsm *HybridSearchManager) mergeResults(results []HybridSearchResult) []HybridSearchResult {
	resultMap := make(map[string]*HybridSearchResult)

	for _, result := range results {
		// Use node ID or name as key for deduplication
		key := hsm.getResultKey(result.Node)

		if existing, exists := resultMap[key]; exists {
			// Merge scores from different sources
			existing.VectorScore = math.Max(existing.VectorScore, result.VectorScore)
			existing.FullTextScore = math.Max(existing.FullTextScore, result.FullTextScore)
			existing.SemanticScore = math.Max(existing.SemanticScore, result.SemanticScore)

			// Recalculate combined score
			existing.CombinedScore = existing.VectorScore*DefaultWeights.Vector +
				existing.FullTextScore*DefaultWeights.FullText +
				existing.SemanticScore*DefaultWeights.Semantic

			// Update source to indicate hybrid
			existing.Source = "hybrid"
			existing.Relevance = hsm.calculateRelevance(existing.CombinedScore, "hybrid")

			// Merge labels
			existing.Labels = hsm.mergeLabels(existing.Labels, result.Labels)
		} else {
			resultMap[key] = &result
		}
	}

	// Convert map back to slice
	var merged []HybridSearchResult
	for _, result := range resultMap {
		merged = append(merged, *result)
	}

	return merged
}

// getResultKey generates a unique key for result deduplication
func (hsm *HybridSearchManager) getResultKey(node map[string]interface{}) string {
	// Try to use element ID first
	if id, ok := node["elementId"].(string); ok {
		return id
	}

	// Fallback to name + type
	name, _ := node["name"].(string)
	nodeType := "unknown"

	// Try to infer type from properties
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

// calculateRelevance determines relevance level based on score and source
func (hsm *HybridSearchManager) calculateRelevance(score float64, source string) string {
	switch source {
	case "vector":
		if score > 0.8 {
			return "high"
		} else if score > 0.6 {
			return "medium"
		}
		return "low"
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
	case "hybrid":
		if score > 2.0 {
			return "high"
		} else if score > 1.0 {
			return "medium"
		}
		return "low"
	default:
		return "unknown"
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
		return hsm.UnifiedSearch(ctx, query, limit, Weights{Vector: 0.3, FullText: 0.5, Semantic: 0.2})
	case "concept-focused":
		return hsm.UnifiedSearch(ctx, query, limit, Weights{Vector: 0.6, FullText: 0.3, Semantic: 0.1})
	case "document-focused":
		return hsm.UnifiedSearch(ctx, query, limit, Weights{Vector: 0.5, FullText: 0.4, Semantic: 0.1})
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

	// Get vector index info
	vectorInfo, err := hsm.vectorSearch.GetVectorIndexInfo(ctx)
	if err != nil {
		log.Printf("Warning: failed to get vector index info: %v", err)
	} else {
		capabilities["vectorSearch"] = vectorInfo
	}

	// Get full-text index info
	fullTextInfo, err := hsm.fullTextSearch.GetFullTextIndexInfo(ctx)
	if err != nil {
		log.Printf("Warning: failed to get full-text index info: %v", err)
	} else {
		capabilities["fullTextSearch"] = fullTextInfo
	}

	capabilities["hybridSearch"] = map[string]interface{}{
		"supportedMethods": []string{"vector", "fulltext", "semantic", "hybrid"},
		"defaultWeights":   DefaultWeights,
		"smartSearch":      true,
		"embeddingService": hsm.embeddingService != nil,
	}

	return capabilities, nil
}