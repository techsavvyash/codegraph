package search

import (
	"context"
	"fmt"
	"log"

	"github.com/context-maximiser/code-graph/pkg/neo4j"
	neo4jdriver "github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// Neo4jVectorStore implements VectorStore using Neo4j's built-in vector indexes.
// This wraps the existing VectorSearchManager behavior behind the VectorStore interface.
type Neo4jVectorStore struct {
	client *neo4j.Client
}

// NewNeo4jVectorStore creates a new Neo4j-backed vector store.
func NewNeo4jVectorStore(client *neo4j.Client) *Neo4jVectorStore {
	return &Neo4jVectorStore{client: client}
}

func (s *Neo4jVectorStore) UpsertVectors(ctx context.Context, vectors []VectorUpsert) error {
	for _, v := range vectors {
		query := `
			MATCH (n)
			WHERE elementId(n) = $id OR n.nodeKey = $id
			SET n.embedding = $vector
		`
		params := map[string]any{
			"id":     v.ID,
			"vector": v.Vector,
		}

		if _, err := s.client.ExecuteQuery(ctx, query, params); err != nil {
			return fmt.Errorf("failed to upsert vector for %s: %w", v.ID, err)
		}
	}
	return nil
}

func (s *Neo4jVectorStore) Query(ctx context.Context, q VectorQuery) ([]VectorResult, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = 10
	}

	// If a specific index is given, use it directly.
	if q.IndexName != "" {
		return s.queryIndex(ctx, q.IndexName, q.Vector, limit)
	}

	// If node labels are specified, try their corresponding indexes.
	if len(q.NodeLabels) > 0 {
		dim := len(q.Vector)
		var allResults []VectorResult
		for _, label := range q.NodeLabels {
			indexName := fmt.Sprintf("%s_embeddings_%d", labelToIndexPrefix(label), dim)
			results, err := s.queryIndex(ctx, indexName, q.Vector, limit)
			if err != nil {
				log.Printf("Warning: query failed for index %s: %v", indexName, err)
				continue
			}
			allResults = append(allResults, results...)
		}
		if len(allResults) > limit {
			allResults = allResults[:limit]
		}
		return allResults, nil
	}

	// Default: search across common indexes.
	dim := len(q.Vector)
	indexes := []string{
		fmt.Sprintf("function_embeddings_%d", dim),
		fmt.Sprintf("class_embeddings_%d", dim),
		fmt.Sprintf("document_embeddings_%d", dim),
	}

	var allResults []VectorResult
	for _, indexName := range indexes {
		results, err := s.queryIndex(ctx, indexName, q.Vector, limit/len(indexes)+1)
		if err != nil {
			log.Printf("Warning: query failed for index %s: %v", indexName, err)
			continue
		}
		allResults = append(allResults, results...)
	}

	if len(allResults) > limit {
		allResults = allResults[:limit]
	}
	return allResults, nil
}

func (s *Neo4jVectorStore) DeleteVectors(ctx context.Context, ids []string) error {
	query := `
		UNWIND $ids AS id
		MATCH (n)
		WHERE elementId(n) = id OR n.nodeKey = id
		REMOVE n.embedding
	`
	_, err := s.client.ExecuteQuery(ctx, query, map[string]any{"ids": ids})
	if err != nil {
		return fmt.Errorf("failed to delete vectors: %w", err)
	}
	return nil
}

func (s *Neo4jVectorStore) CreateIndex(ctx context.Context, name string, dimensions int, similarity string) error {
	// Parse label from index name convention: {label}_embeddings_{dim}
	query := fmt.Sprintf(`
		CREATE VECTOR INDEX %s IF NOT EXISTS
		FOR (n:Function)
		ON n.embedding
		OPTIONS {
			indexConfig: {
				`+"`vector.dimensions`"+`: %d,
				`+"`vector.similarity_function`"+`: '%s'
			}
		}
	`, name, dimensions, similarity)

	_, err := s.client.ExecuteQuery(ctx, query, nil)
	if err != nil {
		return fmt.Errorf("failed to create vector index %s: %w", name, err)
	}
	return nil
}

func (s *Neo4jVectorStore) queryIndex(ctx context.Context, indexName string, vector []float64, limit int) ([]VectorResult, error) {
	query := `
		CALL db.index.vector.queryNodes($indexName, $limit, $queryVector)
		YIELD node, score
		RETURN node, score
		ORDER BY score DESC
	`
	params := map[string]any{
		"indexName":   indexName,
		"limit":       limit,
		"queryVector": vector,
	}

	results, err := s.client.ExecuteQuery(ctx, query, params)
	if err != nil {
		return nil, err
	}

	var vResults []VectorResult
	for _, record := range results {
		rm := record.AsMap()
		score, _ := rm["score"].(float64)

		metadata := make(map[string]any)
		if node, ok := rm["node"]; ok {
			if nodeObj, ok := node.(neo4jdriver.Node); ok {
				metadata = nodeObj.Props
				metadata["_labels"] = nodeObj.Labels
				metadata["_elementId"] = nodeObj.ElementId
			}
		}

		id := ""
		if nk, ok := metadata["nodeKey"].(string); ok {
			id = nk
		} else if eid, ok := metadata["_elementId"].(string); ok {
			id = eid
		}

		vResults = append(vResults, VectorResult{
			ID:       id,
			Score:    score,
			Metadata: metadata,
		})
	}
	return vResults, nil
}

// labelToIndexPrefix converts a Neo4j label to the index naming convention prefix.
func labelToIndexPrefix(label string) string {
	switch label {
	case "Function":
		return "function"
	case "Class":
		return "class"
	case "Method":
		return "method"
	case "Document":
		return "document"
	case "Feature":
		return "feature"
	case "DocumentChunk":
		return "docchunk"
	default:
		return "function"
	}
}

// Compile-time interface check.
var _ VectorStore = (*Neo4jVectorStore)(nil)
