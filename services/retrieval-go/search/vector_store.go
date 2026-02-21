package search

import "context"

// VectorUpsert represents a single vector to upsert into the store.
type VectorUpsert struct {
	ID         string            // Unique identifier (e.g., nodeKey or elementId).
	Vector     []float64         // The embedding vector.
	Metadata   map[string]any    // Optional metadata to store alongside the vector.
	NodeLabel  string            // The Neo4j label (for stores that support label filtering).
}

// VectorQuery represents a vector similarity query.
type VectorQuery struct {
	Vector     []float64         // The query vector.
	Limit      int               // Maximum number of results.
	Filters    map[string]any    // Optional metadata filters.
	NodeLabels []string          // Optional label filter (for stores that support it).
	IndexName  string            // Optional: specific index to query (store-specific).
}

// VectorResult represents a single result from a vector search.
type VectorResult struct {
	ID       string         // The matched vector's ID.
	Score    float64        // Similarity score (higher = more similar for cosine).
	Metadata map[string]any // Metadata associated with the vector.
}

// VectorStore defines the interface for storing and querying vectors.
// Implementations may use Neo4j vector indexes, Qdrant, Pinecone, etc.
type VectorStore interface {
	// UpsertVectors stores or updates vectors in the store.
	UpsertVectors(ctx context.Context, vectors []VectorUpsert) error

	// Query finds the k most similar vectors to the given query.
	Query(ctx context.Context, query VectorQuery) ([]VectorResult, error)

	// DeleteVectors removes vectors by their IDs.
	DeleteVectors(ctx context.Context, ids []string) error

	// CreateIndex creates a vector index (if the store supports explicit index creation).
	CreateIndex(ctx context.Context, name string, dimensions int, similarity string) error
}
