package semlink

import (
	"context"
	"fmt"
)

// embedBatchSize bounds texts per Embed() call.
const embedBatchSize = 64

// codeSummaryLabels are the labels whose summaries participate in matching
// (each has a vector index; Service deliberately has none).
var codeSummaryLabels = []string{"Function", "Method", "Class", "Interface", "File"}

// pendingEmbed is one node whose text needs a vector.
type pendingEmbed struct {
	elementID string
	text      string
}

// embedCodeSummaries embeds every summarized code node whose vector is
// missing or was produced by a different model. Function/Method/File are
// service-scoped via serviceName; Class/Interface via file containment.
func (r *Runner) embedCodeSummaries(ctx context.Context, report *Report) error {
	model := r.embedder.Model()

	for _, label := range codeSummaryLabels {
		var cypher string
		switch label {
		case "Class", "Interface":
			cypher = fmt.Sprintf(`
				MATCH (f:File {serviceName: $svc, scopeId: $scope})-[:CONTAINS]->(n:%s)
				WHERE n.summary IS NOT NULL
				  AND (n.embedding IS NULL OR n.embeddingModel <> $model)
				RETURN DISTINCT elementId(n) AS id, n.summary AS text
				ORDER BY id
			`, label)
		default:
			cypher = fmt.Sprintf(`
				MATCH (n:%s {serviceName: $svc, scopeId: $scope})
				WHERE n.summary IS NOT NULL
				  AND (n.embedding IS NULL OR n.embeddingModel <> $model)
				RETURN elementId(n) AS id, n.summary AS text
				ORDER BY id
			`, label)
		}

		records, err := r.client.ExecuteQuery(ctx, cypher,
			map[string]any{"svc": r.serviceName, "scope": r.scope.ScopeID, "model": model})
		if err != nil {
			return fmt.Errorf("failed to load %s embedding targets: %w", label, err)
		}

		var pending []pendingEmbed
		for _, rec := range records {
			m := rec.AsMap()
			pending = append(pending, pendingEmbed{elementID: str(m, "id"), text: str(m, "text")})
		}
		if err := r.embedAndStore(ctx, pending, report); err != nil {
			return err
		}
	}
	return nil
}

// embedChunks embeds the service's chunks lacking a current-model vector and
// returns the element IDs of every chunk that needs matching: freshly
// embedded ones plus chunks never matched by this model (semlinkModel).
func (r *Runner) embedChunks(ctx context.Context, report *Report) ([]string, error) {
	model := r.embedder.Model()

	records, err := r.client.ExecuteQuery(ctx, `
		MATCH (c:DocumentChunk {serviceName: $svc, scopeId: $scope})
		WHERE c.embedding IS NULL OR c.embeddingModel <> $model
		RETURN elementId(c) AS id, c.content AS text
		ORDER BY c.nodeKey
	`, map[string]any{"svc": r.serviceName, "scope": r.scope.ScopeID, "model": model})
	if err != nil {
		return nil, fmt.Errorf("failed to load chunk embedding targets: %w", err)
	}

	var pending []pendingEmbed
	for _, rec := range records {
		m := rec.AsMap()
		pending = append(pending, pendingEmbed{elementID: str(m, "id"), text: str(m, "text")})
	}
	if err := r.embedAndStore(ctx, pending, report); err != nil {
		return nil, err
	}

	// Match set: embedded-this-run ∪ never-matched-with-this-model.
	records, err = r.client.ExecuteQuery(ctx, `
		MATCH (c:DocumentChunk {serviceName: $svc, scopeId: $scope})
		WHERE c.embedding IS NOT NULL AND c.embeddingModel = $model
		  AND coalesce(c.semlinkModel, '') <> $model
		RETURN elementId(c) AS id
		ORDER BY c.nodeKey
	`, map[string]any{"svc": r.serviceName, "scope": r.scope.ScopeID, "model": model})
	if err != nil {
		return nil, fmt.Errorf("failed to load match set: %w", err)
	}

	var ids []string
	for _, rec := range records {
		ids = append(ids, str(rec.AsMap(), "id"))
	}
	return ids, nil
}

// embedAndStore embeds pending texts in batches and writes vectors via
// db.create.setNodeVectorProperty (the supported write path for vector
// properties; generic SET would store a plain list).
func (r *Runner) embedAndStore(ctx context.Context, pending []pendingEmbed, report *Report) error {
	model := r.embedder.Model()

	for start := 0; start < len(pending); start += embedBatchSize {
		end := min(start+embedBatchSize, len(pending))
		batch := pending[start:end]

		texts := make([]string, len(batch))
		for i, p := range batch {
			texts[i] = p.text
		}
		vectors, err := r.embedder.Embed(ctx, texts)
		if err != nil {
			return fmt.Errorf("embedding batch failed: %w", err)
		}

		rows := make([]map[string]any, len(batch))
		for i, p := range batch {
			// The Go driver does not accept []float32 parameters.
			rows[i] = map[string]any{"id": p.elementID, "vector": toFloat64s(vectors[i])}
		}
		_, err = r.client.ExecuteQuery(ctx, `
			UNWIND $rows AS row
			MATCH (n) WHERE elementId(n) = row.id
			CALL db.create.setNodeVectorProperty(n, 'embedding', row.vector)
			SET n.embeddingModel = $model
		`, map[string]any{"rows": rows, "model": model})
		if err != nil {
			return fmt.Errorf("failed to store embeddings: %w", err)
		}
		report.EmbeddingsWritten += len(batch)
	}
	return nil
}

func toFloat64s(v []float32) []float64 {
	out := make([]float64, len(v))
	for i, x := range v {
		out[i] = float64(x)
	}
	return out
}
