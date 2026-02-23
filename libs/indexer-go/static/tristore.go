package static

import (
	"context"
	"fmt"
	"strings"

	"github.com/context-maximiser/code-graph/libs/search-go"
	textindex "github.com/context-maximiser/code-graph/libs/text-index-client-go"
)

// ensureSecondaryStoreIndexes creates Qdrant collections and OpenSearch index
// if they don't already exist. Called once before population.
func (si *SCIPIndexer) ensureSecondaryStoreIndexes(ctx context.Context) {
	// Create Qdrant collections (768-dim for Gemini, 1536-dim for OpenAI).
	type colSpec struct {
		name string
		dim  int
	}
	collections := []colSpec{
		{"function_embeddings_768", 768},
		{"method_embeddings_768", 768},
		{"class_embeddings_768", 768},
		{"symbol_embeddings_768", 768},
		{"document_embeddings_768", 768},
		{"docchunk_embeddings_768", 768},
		{"feature_embeddings_768", 768},
		{"function_embeddings_1536", 1536},
		{"method_embeddings_1536", 1536},
		{"class_embeddings_1536", 1536},
		{"symbol_embeddings_1536", 1536},
		{"document_embeddings_1536", 1536},
		{"docchunk_embeddings_1536", 1536},
		{"feature_embeddings_1536", 1536},
	}
	for _, c := range collections {
		if err := si.vectorStore.CreateIndex(ctx, c.name, c.dim, "cosine"); err != nil {
			fmt.Printf("Warning: failed to create collection %s: %v\n", c.name, err)
		}
	}

	// Ensure OpenSearch index exists.
	if si.textStore != nil {
		if os, ok := si.textStore.(*textindex.OpenSearchStore); ok {
			if err := os.EnsureIndex(ctx); err != nil {
				fmt.Printf("Warning: failed to ensure OpenSearch index: %v\n", err)
			}
		}
	}
}

// populateSecondaryStores pushes Neo4j nodes into Qdrant (vectors) and
// OpenSearch (BM25 text) for the node types used in hybrid search.
func (si *SCIPIndexer) populateSecondaryStores(ctx context.Context) {
	nodeTypes := []string{"Function", "Method", "Class", "Symbol", "Document", "DocumentChunk", "Feature"}
	batchSize := 500

	totalVectors := 0
	totalText := 0
	for _, nt := range nodeTypes {
		n, err := si.populateVectorsForNodeType(ctx, nt, batchSize)
		if err != nil {
			fmt.Printf("Warning: vector population for %s failed: %v\n", nt, err)
		}
		totalVectors += n

		m, err := si.populateTextIndexForNodeType(ctx, nt, batchSize)
		if err != nil {
			fmt.Printf("Warning: text index population for %s failed: %v\n", nt, err)
		}
		totalText += m
	}

	fmt.Printf("Secondary stores populated: %d vectors, %d text docs\n", totalVectors, totalText)
}

// populateVectorsForNodeType queries Neo4j for nodes of the given type that
// have not yet been embedded (embeddedAt IS NULL), generates embeddings, and
// upserts them into the vector store.
func (si *SCIPIndexer) populateVectorsForNodeType(ctx context.Context, nodeType string, batchSize int) (int, error) {
	query := fmt.Sprintf(`
		MATCH (n:%s)
		WHERE n.embeddedAt IS NULL
		RETURN elementId(n) as nodeId, n.name as name, n.nodeKey as nodeKey,
		       n.signature as signature, n.description as description,
		       n.content as content, n.title as title,
		       n.filePath as filePath, n.startLine as startLine, n.endLine as endLine
		LIMIT 1000
	`, nodeType)

	results, err := si.client.ExecuteQuery(ctx, query, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to query %s nodes: %w", nodeType, err)
	}
	if len(results) == 0 {
		return 0, nil
	}

	fmt.Printf("   Embedding %d %s nodes...\n", len(results), nodeType)

	type nodeInfo struct {
		nodeId, nodeKey, name, signature, description, filePath string
		startLine, endLine                                      int64
	}

	processed := 0
	for i := 0; i < len(results); i += batchSize {
		end := i + batchSize
		if end > len(results) {
			end = len(results)
		}
		batch := results[i:end]

		var texts []string
		var infos []nodeInfo

		for _, record := range batch {
			m := record.AsMap()
			nid, _ := m["nodeId"].(string)
			nk, _ := m["nodeKey"].(string)
			name, _ := m["name"].(string)
			sig, _ := m["signature"].(string)
			desc, _ := m["description"].(string)
			fp, _ := m["filePath"].(string)
			sl, _ := m["startLine"].(int64)
			el, _ := m["endLine"].(int64)

			var parts []string
			if name != "" {
				parts = append(parts, name)
			}
			if title, ok := m["title"].(string); ok && title != "" {
				parts = append(parts, title)
			}
			if sig != "" {
				parts = append(parts, sig)
			}
			if desc != "" {
				parts = append(parts, desc)
			}
			if content, ok := m["content"].(string); ok && content != "" {
				if len(content) > 500 {
					content = content[:500] + "..."
				}
				parts = append(parts, content)
			}
			text := strings.Join(parts, " | ")
			if text == "" {
				text = fmt.Sprintf("%s node", nodeType)
			}

			texts = append(texts, text)
			infos = append(infos, nodeInfo{
				nodeId: nid, nodeKey: nk, name: name,
				signature: sig, description: desc, filePath: fp,
				startLine: sl, endLine: el,
			})
		}

		embeddings, err := si.embeddingService.GenerateBatchEmbeddings(ctx, texts)
		if err != nil {
			return processed, fmt.Errorf("failed to generate embeddings for %s: %w", nodeType, err)
		}

		var upserts []search.VectorUpsert
		var embeddedNodeIds []string
		for j, emb := range embeddings {
			info := infos[j]
			id := info.nodeKey
			if id == "" {
				id = info.nodeId
			}
			upserts = append(upserts, search.VectorUpsert{
				ID:        id,
				Vector:    emb,
				NodeLabel: nodeType,
				Metadata: map[string]any{
					"name":      info.name,
					"signature": info.signature,
					"filePath":  info.filePath,
					"startLine": fmt.Sprintf("%d", info.startLine),
					"endLine":   fmt.Sprintf("%d", info.endLine),
				},
			})
			embeddedNodeIds = append(embeddedNodeIds, info.nodeId)
		}

		if err := si.vectorStore.UpsertVectors(ctx, upserts); err != nil {
			return processed, fmt.Errorf("failed to upsert vectors for %s: %w", nodeType, err)
		}

		// Stamp embeddedAt so nodes are skipped on re-runs.
		stampQuery := `
			UNWIND $ids AS id
			MATCH (n) WHERE elementId(n) = id
			SET n.embeddedAt = datetime()
		`
		if _, err := si.client.ExecuteQuery(ctx, stampQuery, map[string]any{"ids": embeddedNodeIds}); err != nil {
			fmt.Printf("   Warning: failed to stamp embeddedAt for %s: %v\n", nodeType, err)
		}

		processed += len(batch)
	}

	return processed, nil
}

// populateTextIndexForNodeType queries Neo4j for nodes of the given type and
// indexes them into OpenSearch for BM25 search.
func (si *SCIPIndexer) populateTextIndexForNodeType(ctx context.Context, nodeType string, batchSize int) (int, error) {
	if si.textStore == nil {
		return 0, nil
	}

	var query string
	switch nodeType {
	case "Function", "Method":
		query = fmt.Sprintf(`MATCH (n:%s)
RETURN n.nodeKey AS nodeKey, coalesce(n.name,'') + ' ' + coalesce(n.signature,'') + ' ' + coalesce(n.docstring,'') AS content, labels(n)[0] AS nodeType`, nodeType)
	case "Symbol":
		query = `MATCH (n:Symbol) RETURN n.nodeKey AS nodeKey, coalesce(n.displayName,'') + ' ' + coalesce(n.documentation,'') AS content`
	case "Class":
		query = `MATCH (n:Class) RETURN n.nodeKey AS nodeKey, coalesce(n.name,'') + ' ' + coalesce(n.fqn,'') + ' ' + coalesce(n.docstring,'') AS content`
	case "Document":
		query = `MATCH (n:Document) RETURN n.nodeKey AS nodeKey, coalesce(n.title,'') + ' ' + coalesce(n.content,'') AS content`
	case "DocumentChunk":
		query = `MATCH (n:DocumentChunk) RETURN n.nodeKey AS nodeKey, coalesce(n.headingPath,'') + ' ' + coalesce(n.content,'') AS content`
	case "Feature":
		query = `MATCH (n:Feature) RETURN n.nodeKey AS nodeKey, coalesce(n.name,'') + ' ' + coalesce(n.description,'') AS content`
	default:
		query = fmt.Sprintf(`MATCH (n:%s) RETURN n.nodeKey AS nodeKey, coalesce(n.name,'') AS content`, nodeType)
	}

	rows, err := si.client.ExecuteQuery(ctx, query, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to query %s nodes for text index: %w", nodeType, err)
	}
	if len(rows) == 0 {
		return 0, nil
	}

	fmt.Printf("   Text-indexing %d %s nodes...\n", len(rows), nodeType)

	synced := 0
	var batch []textindex.IndexDoc
	for _, r := range rows {
		m := r.AsMap()
		nk, _ := m["nodeKey"].(string)
		ct, _ := m["content"].(string)
		if nk == "" {
			continue
		}
		batch = append(batch, textindex.IndexDoc{
			NodeKey:  nk,
			Content:  ct,
			Metadata: map[string]string{"nodeType": nodeType},
		})
		if len(batch) >= batchSize {
			if e := si.textStore.IndexDocuments(ctx, batch); e == nil {
				synced += len(batch)
			}
			batch = batch[:0]
		}
	}
	if len(batch) > 0 {
		if e := si.textStore.IndexDocuments(ctx, batch); e == nil {
			synced += len(batch)
		}
	}

	return synced, nil
}
