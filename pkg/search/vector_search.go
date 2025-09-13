package search

import (
	"context"
	"fmt"
	"log"

	"github.com/context-maximiser/code-graph/pkg/neo4j"
	neo4jdriver "github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// VectorSearchManager handles vector-based semantic search operations
type VectorSearchManager struct {
	client *neo4j.Client
}

// NewVectorSearchManager creates a new vector search manager
func NewVectorSearchManager(client *neo4j.Client) *VectorSearchManager {
	return &VectorSearchManager{
		client: client,
	}
}

// VectorIndexConfig represents configuration for vector indexes
type VectorIndexConfig struct {
	Name               string `json:"name"`
	NodeLabel          string `json:"nodeLabel"`
	Property           string `json:"property"`
	Dimensions         int    `json:"dimensions"`
	SimilarityFunction string `json:"similarityFunction"` // "cosine" or "euclidean"
}

// VectorSearchResult represents a vector search result with similarity score
type VectorSearchResult struct {
	Node  map[string]interface{} `json:"node"`
	Score float64                `json:"score"`
}

// VectorSearchResponse contains vector search results and metadata
type VectorSearchResponse struct {
	Results     []VectorSearchResult `json:"results"`
	QueryVector []float64            `json:"queryVector"`
	IndexUsed   string               `json:"indexUsed"`
	Count       int                  `json:"count"`
}

// CreateVectorIndexes creates all necessary vector indexes for CodeGraph
func (vsm *VectorSearchManager) CreateVectorIndexes(ctx context.Context) error {
	indexes := []VectorIndexConfig{
		{
			Name:               "function_embeddings",
			NodeLabel:          "Function",
			Property:           "embedding",
			Dimensions:         384, // sentence-transformers/all-MiniLM-L6-v2
			SimilarityFunction: "cosine",
		},
		{
			Name:               "document_embeddings",
			NodeLabel:          "Document",
			Property:           "embedding",
			Dimensions:         384,
			SimilarityFunction: "cosine",
		},
		{
			Name:               "feature_embeddings",
			NodeLabel:          "Feature",
			Property:           "embedding",
			Dimensions:         384,
			SimilarityFunction: "cosine",
		},
		{
			Name:               "class_embeddings",
			NodeLabel:          "Class",
			Property:           "embedding",
			Dimensions:         384,
			SimilarityFunction: "cosine",
		},
	}

	for _, index := range indexes {
		if err := vsm.createVectorIndex(ctx, index); err != nil {
			log.Printf("Warning: failed to create vector index %s: %v", index.Name, err)
			// Continue with other indexes even if one fails
		} else {
			log.Printf("✓ Created vector index: %s", index.Name)
		}
	}

	return nil
}

// createVectorIndex creates a single vector index
func (vsm *VectorSearchManager) createVectorIndex(ctx context.Context, config VectorIndexConfig) error {
	query := fmt.Sprintf(`
		CREATE VECTOR INDEX %s IF NOT EXISTS
		FOR (n:%s)
		ON n.%s
		OPTIONS {
			indexConfig: {
				` + "`vector.dimensions`" + `: %d,
				` + "`vector.similarity_function`" + `: '%s'
			}
		}
	`, config.Name, config.NodeLabel, config.Property, config.Dimensions, config.SimilarityFunction)

	_, err := vsm.client.ExecuteQuery(ctx, query, nil)
	if err != nil {
		return fmt.Errorf("failed to create vector index %s: %w", config.Name, err)
	}

	return nil
}

// VectorSearch performs semantic search using vector similarity
func (vsm *VectorSearchManager) VectorSearch(ctx context.Context, indexName string, queryVector []float64, limit int) (*VectorSearchResponse, error) {
	if limit <= 0 {
		limit = 10
	}

	query := fmt.Sprintf(`
		CALL db.index.vector.queryNodes($indexName, $limit, $queryVector)
		YIELD node, score
		RETURN node, score
		ORDER BY score DESC
	`)

	params := map[string]any{
		"indexName":   indexName,
		"limit":       limit,
		"queryVector": queryVector,
	}

	results, err := vsm.client.ExecuteQuery(ctx, query, params)
	if err != nil {
		return nil, fmt.Errorf("failed to perform vector search: %w", err)
	}

	var searchResults []VectorSearchResult
	for _, record := range results {
		recordMap := record.AsMap()

		node, ok := recordMap["node"]
		if !ok {
			continue
		}

		score, ok := recordMap["score"].(float64)
		if !ok {
			score = 0.0
		}

		// Convert node to map for JSON serialization
		nodeMap := make(map[string]interface{})
		if nodeObj, ok := node.(neo4jdriver.Node); ok {
			// Extract all properties from the Neo4j Node object
			nodeMap = nodeObj.Props
		} else if nodeData, ok := node.(map[string]interface{}); ok {
			nodeMap = nodeData
		}

		searchResults = append(searchResults, VectorSearchResult{
			Node:  nodeMap,
			Score: score,
		})
	}

	return &VectorSearchResponse{
		Results:     searchResults,
		QueryVector: queryVector,
		IndexUsed:   indexName,
		Count:       len(searchResults),
	}, nil
}

// HybridVectorSearch combines multiple vector indexes for comprehensive search
func (vsm *VectorSearchManager) HybridVectorSearch(ctx context.Context, queryVector []float64, limit int) (*VectorSearchResponse, error) {
	indexes := []string{
		"function_embeddings",
		"document_embeddings",
		"feature_embeddings",
		"class_embeddings",
	}

	var allResults []VectorSearchResult

	// Search across all vector indexes
	for _, indexName := range indexes {
		response, err := vsm.VectorSearch(ctx, indexName, queryVector, limit/len(indexes)+1)
		if err != nil {
			log.Printf("Warning: vector search failed for index %s: %v", indexName, err)
			continue
		}
		allResults = append(allResults, response.Results...)
	}

	// Sort by score and limit results
	if len(allResults) > limit {
		allResults = allResults[:limit]
	}

	return &VectorSearchResponse{
		Results:     allResults,
		QueryVector: queryVector,
		IndexUsed:   "hybrid",
		Count:       len(allResults),
	}, nil
}

// GetVectorIndexInfo returns information about existing vector indexes
func (vsm *VectorSearchManager) GetVectorIndexInfo(ctx context.Context) (map[string]interface{}, error) {
	query := `SHOW INDEXES WHERE type = 'VECTOR'`

	results, err := vsm.client.ExecuteQuery(ctx, query, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get vector index info: %w", err)
	}

	info := make(map[string]interface{})
	info["vectorIndexes"] = make([]map[string]interface{}, 0)

	for _, record := range results {
		recordMap := record.AsMap()
		indexInfo := make(map[string]interface{})

		for key, value := range recordMap {
			indexInfo[key] = value
		}

		if indexes, ok := info["vectorIndexes"].([]map[string]interface{}); ok {
			info["vectorIndexes"] = append(indexes, indexInfo)
		}
	}

	return info, nil
}

// UpdateNodeEmbedding updates the embedding for a specific node
func (vsm *VectorSearchManager) UpdateNodeEmbedding(ctx context.Context, nodeId string, embedding []float64) error {
	query := `
		MATCH (n)
		WHERE elementId(n) = $nodeId
		SET n.embedding = $embedding
		RETURN elementId(n) as id
	`

	params := map[string]any{
		"nodeId":    nodeId,
		"embedding": embedding,
	}

	results, err := vsm.client.ExecuteQuery(ctx, query, params)
	if err != nil {
		return fmt.Errorf("failed to update node embedding: %w", err)
	}

	if len(results) == 0 {
		return fmt.Errorf("node not found: %s", nodeId)
	}

	return nil
}

// BatchUpdateEmbeddings updates embeddings for multiple nodes efficiently
func (vsm *VectorSearchManager) BatchUpdateEmbeddings(ctx context.Context, updates []NodeEmbeddingUpdate) error {
	if len(updates) == 0 {
		return nil
	}

	// Convert to format Neo4j can handle
	var updateMaps []map[string]interface{}
	for _, update := range updates {
		updateMaps = append(updateMaps, map[string]interface{}{
			"nodeId":    update.NodeId,
			"embedding": update.Embedding,
		})
	}

	query := `
		UNWIND $updates as update
		MATCH (n)
		WHERE elementId(n) = update.nodeId
		SET n.embedding = update.embedding
		RETURN count(n) as updated
	`

	params := map[string]any{
		"updates": updateMaps,
	}

	results, err := vsm.client.ExecuteQuery(ctx, query, params)
	if err != nil {
		return fmt.Errorf("failed to batch update embeddings: %w", err)
	}

	if len(results) > 0 {
		recordMap := results[0].AsMap()
		if updated, ok := recordMap["updated"].(int64); ok {
			log.Printf("✓ Updated embeddings for %d nodes", updated)
		}
	}

	return nil
}

// NodeEmbeddingUpdate represents a node embedding update operation
type NodeEmbeddingUpdate struct {
	NodeId    string    `json:"nodeId"`
	Embedding []float64 `json:"embedding"`
}

// DropVectorIndex removes a vector index
func (vsm *VectorSearchManager) DropVectorIndex(ctx context.Context, indexName string) error {
	query := fmt.Sprintf("DROP INDEX %s IF EXISTS", indexName)

	_, err := vsm.client.ExecuteQuery(ctx, query, nil)
	if err != nil {
		return fmt.Errorf("failed to drop vector index %s: %w", indexName, err)
	}

	log.Printf("✓ Dropped vector index: %s", indexName)
	return nil
}