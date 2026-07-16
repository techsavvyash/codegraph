package semlink

import (
	"context"
	"fmt"

	"github.com/context-maximiser/code-graph/internal/graph/schema"
)

// ensureVectorIndexes creates the RFC-011 vector indexes at the embedder's
// dimension, recreating them when an existing index was built with a
// different dimension (embeddings from different models are not comparable;
// the index drop does not touch node properties, and stale different-dim
// vectors are simply not indexed until re-embedded). Returns whether a
// recreation happened.
func (r *Runner) ensureVectorIndexes(ctx context.Context) (bool, error) {
	dim := r.embedder.Dimensions()

	existingDim, err := r.currentVectorIndexDimension(ctx)
	if err != nil {
		return false, err
	}

	sm := schema.NewSchemaManager(r.client)
	reset := false
	if existingDim > 0 && existingDim != dim {
		fmt.Printf("semlink: vector indexes are %d-dim but embedder %q needs %d-dim — recreating (node vectors re-embed lazily)\n",
			existingDim, r.embedder.Model(), dim)
		if err := sm.DropVectorIndexes(ctx); err != nil {
			return false, err
		}
		reset = true
	}

	if err := sm.CreateVectorIndexes(ctx, dim); err != nil {
		return reset, err
	}

	// Vector index population is async; matching queries need them ONLINE.
	if _, err := r.client.ExecuteQuery(ctx, "CALL db.awaitIndexes(300)", nil); err != nil {
		return reset, fmt.Errorf("failed waiting for vector indexes: %w", err)
	}
	return reset, nil
}

// currentVectorIndexDimension reads the dimension of the chunk_embedding
// index (all RFC-011 vector indexes are created together with one dimension).
// Returns 0 when the index does not exist.
func (r *Runner) currentVectorIndexDimension(ctx context.Context) (int, error) {
	records, err := r.client.ExecuteQuery(ctx, `
		SHOW INDEXES YIELD name, type, options
		WHERE name = 'chunk_embedding' AND type = 'VECTOR'
		RETURN options
	`, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to inspect vector indexes: %w", err)
	}
	if len(records) == 0 {
		return 0, nil
	}

	options, _ := records[0].AsMap()["options"].(map[string]any)
	indexConfig, _ := options["indexConfig"].(map[string]any)
	switch v := indexConfig["vector.dimensions"].(type) {
	case int64:
		return int(v), nil
	case float64:
		return int(v), nil
	default:
		return 0, nil
	}
}
