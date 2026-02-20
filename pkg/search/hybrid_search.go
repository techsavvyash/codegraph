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
	vectorStore         VectorStore              // Vector store backend (e.g. Qdrant)
	fullTextSearch      *FullTextSearchManager
	queryBuilder        *neo4j.QueryBuilder
	embeddingService    EmbeddingService // Interface for generating embeddings
	commentSearch       *CommentEmbeddingService // For comment-based function discovery
}

// NewHybridSearchManager creates a comprehensive hybrid search manager.
// The VectorStore is used for all vector similarity queries.
func NewHybridSearchManager(client *neo4j.Client, embeddingService EmbeddingService, store VectorStore) *HybridSearchManager {
	return &HybridSearchManager{
		client:           client,
		vectorStore:      store,
		fullTextSearch:   NewFullTextSearchManager(client),
		queryBuilder:     neo4j.NewQueryBuilder(client),
		embeddingService: embeddingService,
		commentSearch:    NewCommentEmbeddingService(client, embeddingService),
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
	CommentScore    float64                `json:"commentScore"`    // Score from comment-based search
	CombinedScore   float64                `json:"combinedScore"`
	Source          string                 `json:"source"` // "vector", "fulltext", "semantic", "hybrid", "comment"
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
	CommentResults   int     `json:"commentResults"`
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

// UnifiedSearch performs comprehensive search using all available methods.
// Results are merged using Reciprocal Rank Fusion (RRF), which correctly
// combines scores from different scales (cosine 0-1, BM25 2-20, etc.).
func (hsm *HybridSearchManager) UnifiedSearch(ctx context.Context, query string, limit int, weights ...Weights) (*HybridSearchResponse, error) {
	if limit <= 0 {
		limit = 20
	}

	// Use provided weights or defaults
	searchWeights := DefaultWeights
	if len(weights) > 0 {
		searchWeights = weights[0]
	}

	var queryVector []float64

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
		e.result.VectorScore = math.Max(e.result.VectorScore, result.VectorScore)
		e.result.FullTextScore = math.Max(e.result.FullTextScore, result.FullTextScore)
		e.result.SemanticScore = math.Max(e.result.SemanticScore, result.SemanticScore)
		e.result.CommentScore = math.Max(e.result.CommentScore, result.CommentScore)
		// Accumulate RRF score (scale by source weight).
		var w float64
		switch result.Source {
		case "vector":
			w = searchWeights.Vector
		case "fulltext":
			w = searchWeights.FullText
		case "semantic":
			w = searchWeights.Semantic
		default:
			w = 0.3
		}
		e.rrf += rrfScore(rank) * w
	}

	// 1. Vector Search
	var vectorResultCount int
	if hsm.embeddingService != nil && hsm.vectorStore != nil {
		var err error
		queryVector, err = hsm.embeddingService.GenerateEmbedding(ctx, query)
		if err != nil {
			log.Printf("Warning: failed to generate query embedding: %v", err)
		} else {
			storeResults, err := hsm.vectorStore.Query(ctx, VectorQuery{
				Vector: queryVector,
				Limit:  limit,
			})
			if err != nil {
				log.Printf("Warning: vector store search failed: %v", err)
			} else {
				vectorResultCount = len(storeResults)
				resolvedNodes := hsm.resolveNodeKeys(ctx, storeResults)
				for i, result := range storeResults {
					node := resolvedNodes[i]
					// Extract labels stored by resolveNodeKeys under "_labels".
					var labels []string
					if lv, ok := node["_labels"].([]string); ok {
						labels = lv
					}
					hr := HybridSearchResult{
						Node:        node,
						Labels:      labels,
						VectorScore: result.Score,
						Source:      "vector",
						Relevance:   hsm.calculateRelevance(result.Score, "vector"),
					}
					key := hsm.getResultKey(node)
					addEntry(key, hr, i+1)
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

	// 3. Semantic Search (graph-based name/description matching)
	semanticResults, err := hsm.queryBuilder.SearchNodes(ctx, query, []string{"Function", "Method", "Class", "Document", "Feature", "Symbol"}, limit)
	if err != nil {
		log.Printf("Warning: semantic search failed: %v", err)
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

	// 4. Comment-Based Search
	var commentResults []CommentSearchResult
	if hsm.commentSearch != nil {
		commentResponse, err := hsm.commentSearch.SearchFunctionsByComment(ctx, query, limit)
		if err != nil {
			log.Printf("Warning: comment search failed: %v", err)
		} else {
			commentResults = commentResponse.Results
			for i, result := range commentResults {
				hr := HybridSearchResult{
					Node:         result.ParentNode,
					Labels:       []string{getStringFromMap(result.CommentNode, "parentType")},
					CommentScore: result.Score,
					Source:       "comment",
					Relevance:    hsm.calculateRelevance(result.Score, "comment"),
				}
				key := hsm.getResultKey(result.ParentNode)
				addEntry(key, hr, i+1)
			}
		}
	}

	// 5. Build final result slice from RRF scores.
	mergedResults := make([]HybridSearchResult, 0, len(byKey))
	for _, e := range byKey {
		e.result.CombinedScore = e.rrf
		// Determine source label.
		sourceCount := 0
		for _, s := range []float64{e.result.VectorScore, e.result.FullTextScore, e.result.SemanticScore, e.result.CommentScore} {
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

	// 6. Sort by RRF combined score descending.
	sort.Slice(mergedResults, func(i, j int) bool {
		return mergedResults[i].CombinedScore > mergedResults[j].CombinedScore
	})

	// 7. Limit results.
	if len(mergedResults) > limit {
		mergedResults = mergedResults[:limit]
	}

	response := &HybridSearchResponse{
		Results:      mergedResults,
		Query:        query,
		QueryVector:  queryVector,
		SearchTypes:  []string{"vector", "fulltext", "semantic", "comment"},
		TotalResults: len(mergedResults),
		Metadata: SearchMetadata{
			VectorResults:   vectorResultCount,
			FullTextResults: len(fullTextResults),
			SemanticResults: len(semanticResults),
			CommentResults:  len(commentResults),
			HybridWeight:    searchWeights,
		},
	}

	return response, nil
}

// getResultKey generates a stable unique key for result deduplication.
// Priority: nodeKey (most stable) > elementId > name+type fallback.
func (hsm *HybridSearchManager) getResultKey(node map[string]interface{}) string {
	if nk, ok := node["nodeKey"].(string); ok && nk != "" {
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
// RRF scores top out around 1/(60+1) ≈ 0.016 per source; three sources ≈ 0.048.
func (hsm *HybridSearchManager) calculateRelevance(score float64, source string) string {
	switch source {
	case "vector":
		if score > 0.85 {
			return "high"
		} else if score > 0.65 {
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
	case "rrf", "hybrid":
		// RRF: single-source max ≈ 0.016, dual ≈ 0.032, triple ≈ 0.048 (unweighted).
		// With weights 0.4/0.4/0.2 these scale accordingly.
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

	capabilities["vectorSearch"] = map[string]interface{}{
		"backend":    "qdrant",
		"vectorStore": hsm.vectorStore != nil,
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

// resolveNodeKeys takes Qdrant vector results and resolves their nodeKeys
// back to full Neo4j node properties (filePath, sourceCode, startLine, etc.).
// Returns a slice of node maps in the same order as the input results.
func (hsm *HybridSearchManager) resolveNodeKeys(ctx context.Context, results []VectorResult) []map[string]interface{} {
	nodes := make([]map[string]interface{}, len(results))

	// Collect nodeKeys for batch lookup.
	var nodeKeys []string
	keyIndex := make(map[string][]int) // nodeKey -> indices in results
	for i, r := range results {
		nk, _ := r.Metadata["nodeKey"].(string)
		if nk == "" {
			nk = r.ID
		}
		if nk != "" {
			keyIndex[nk] = append(keyIndex[nk], i)
			nodeKeys = append(nodeKeys, nk)
		}
		// Initialize with Qdrant metadata as fallback.
		node := make(map[string]interface{})
		for k, v := range r.Metadata {
			node[k] = v
		}
		nodes[i] = node
	}

	if len(nodeKeys) == 0 {
		return nodes
	}

	// Batch-resolve from Neo4j.
	resolveQuery := `
		UNWIND $keys AS key
		MATCH (n)
		WHERE n.nodeKey = key
		RETURN n.nodeKey AS nodeKey, n, labels(n) AS labels
	`
	records, err := hsm.client.ExecuteQuery(ctx, resolveQuery, map[string]any{"keys": nodeKeys})
	if err != nil {
		log.Printf("Warning: failed to resolve nodeKeys from Neo4j: %v", err)
		return nodes
	}

	for _, record := range records {
		rm := record.AsMap()
		nk, _ := rm["nodeKey"].(string)
		indices, ok := keyIndex[nk]
		if !ok {
			continue
		}

		// Extract node properties.
		props := make(map[string]interface{})
		if nodeObj, ok := rm["n"].(neo4jdriver.Node); ok {
			for k, v := range nodeObj.Props {
				if k == "embedding" || k == "embeddedAt" {
					continue // Don't copy large/internal fields.
				}
				props[k] = v
			}
			props["elementId"] = nodeObj.ElementId
		}

		// Extract labels.
		var labels []string
		if labelsList, ok := rm["labels"].([]interface{}); ok {
			for _, l := range labelsList {
				if ls, ok := l.(string); ok {
					labels = append(labels, ls)
				}
			}
		}

		// Overwrite the Qdrant-only metadata with the full Neo4j properties.
		for _, idx := range indices {
			enriched := make(map[string]interface{})
			for k, v := range props {
				enriched[k] = v
			}
			enriched["_labels"] = labels
			nodes[idx] = enriched
		}
	}

	return nodes
}

// Helper function to safely extract string values from maps
func getStringFromMap(data map[string]interface{}, key string) string {
	if val, ok := data[key]; ok {
		if str, ok := val.(string); ok {
			return str
		}
	}
	return ""
}