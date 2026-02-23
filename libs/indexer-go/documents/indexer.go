package documents

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	textindex "github.com/context-maximiser/code-graph/libs/text-index-client-go"
	"github.com/context-maximiser/code-graph/libs/core-models-go"
	"github.com/context-maximiser/code-graph/libs/neo4j-go"
	"github.com/context-maximiser/code-graph/libs/search-go"
	"github.com/neo4j/neo4j-go-driver/v5/neo4j/dbtype"
)

// DocumentIndexer handles indexing documents into Neo4j
type DocumentIndexer struct {
	client                *neo4j.Client
	parser                *DocumentParser
	intelligentLinker     *search.IntelligentDocumentLinker
	chunkLinker           *search.ChunkLinker
	useIntelligentLinking bool
	scopeCtx              models.ScopeContext
	textStore             textindex.TextIndexStore
	embeddingService      search.EmbeddingService
	vectorStore           search.VectorStore
}

// NewDocumentIndexer creates a new document indexer
func NewDocumentIndexer(client *neo4j.Client) *DocumentIndexer {
	return &DocumentIndexer{
		client:                client,
		parser:                NewDocumentParser(),
		chunkLinker:           search.NewChunkLinker(client),
		useIntelligentLinking: false,
		scopeCtx:              models.DefaultScope(),
	}
}

// SetScope sets the scope context for the document indexer.
func (di *DocumentIndexer) SetScope(scope models.ScopeContext) {
	di.scopeCtx = scope
}

// WithTextStore sets an optional TextIndexStore for pushing chunks to OpenSearch (or any BM25 backend).
func (di *DocumentIndexer) WithTextStore(ts textindex.TextIndexStore) *DocumentIndexer {
	di.textStore = ts
	return di
}

// WithVectorStore sets an embedding service and vector store for pushing
// Document/Feature/DocumentChunk nodes to Qdrant. Also auto-enables
// intelligent linking since both dependencies are satisfied.
func (di *DocumentIndexer) WithVectorStore(es search.EmbeddingService, vs search.VectorStore) *DocumentIndexer {
	di.embeddingService = es
	di.vectorStore = vs
	di.EnableIntelligentLinking(es, vs)
	return di
}

// EnableIntelligentLinking enables semantic analysis and intelligent linking
func (di *DocumentIndexer) EnableIntelligentLinking(embeddingService search.EmbeddingService, vectorStore search.VectorStore) {
	di.intelligentLinker = search.NewIntelligentDocumentLinker(di.client, embeddingService, vectorStore)
	di.useIntelligentLinking = true
}

// IndexDocument indexes a single document file
func (di *DocumentIndexer) IndexDocument(ctx context.Context, filePath string) error {
	fmt.Printf("Indexing document: %s\n", filePath)

	// Parse the document
	doc, features, err := di.parser.ParseDocument(filePath)
	if err != nil {
		return fmt.Errorf("failed to parse document %s: %w", filePath, err)
	}

	fmt.Printf("Extracted %d features from document\n", len(features))

	// Create document node
	docID, err := di.createDocumentNode(ctx, doc)
	if err != nil {
		return fmt.Errorf("failed to create document node: %w", err)
	}

	// Create feature nodes and relationships
	for _, feature := range features {
		featureID, err := di.createFeatureNode(ctx, feature)
		if err != nil {
			fmt.Printf("Warning: failed to create feature node for %s: %v\n", feature.Name, err)
			continue
		}

		// Create DESCRIBES relationship from document to feature
		_, err = di.client.CreateRelationship(ctx, docID, featureID, "DESCRIBES",
			map[string]any{"scope": di.scopeCtx.Scope, "scopeId": di.scopeCtx.ScopeID})
		if err != nil {
			fmt.Printf("Warning: failed to create DESCRIBES relationship: %v\n", err)
		}
	}

	// Create document chunks
	docNodeKey := models.DocumentNodeKey(doc.SourceURL)
	chunkStats, err := di.createDocumentChunks(ctx, docID, docNodeKey, doc.Content)
	if err != nil {
		fmt.Printf("Warning: failed to create document chunks: %v\n", err)
	} else {
		fmt.Printf("Chunks: %d total, %d created, %d unchanged, %d updated\n",
			chunkStats.Total, chunkStats.Created, chunkStats.Unchanged, chunkStats.Updated)
	}

	// Push Document and Feature nodes to vector store (Qdrant) and text store (OpenSearch).
	docNodeKey2 := models.DocumentNodeKey(doc.SourceURL)
	var embedItems []embedItem
	var textDocs []textindex.IndexDoc

	docText := doc.Title + " " + doc.Content
	if len(docText) > 2000 {
		docText = docText[:2000]
	}
	embedItems = append(embedItems, embedItem{nodeKey: docNodeKey2, text: docText, label: "Document"})
	textDocs = append(textDocs, textindex.IndexDoc{
		NodeKey: docNodeKey2, Content: doc.Title + " " + doc.Content,
		Metadata: map[string]string{"nodeType": "Document"},
	})

	for _, feature := range features {
		fKey := models.FeatureNodeKey(feature.Name)
		fText := feature.Name + " " + feature.Description
		embedItems = append(embedItems, embedItem{nodeKey: fKey, text: fText, label: "Feature"})
		textDocs = append(textDocs, textindex.IndexDoc{
			NodeKey: fKey, Content: fText,
			Metadata: map[string]string{"nodeType": "Feature"},
		})
	}

	if err := di.embedNodes(ctx, embedItems); err != nil {
		fmt.Printf("Warning: vector embedding for doc nodes failed: %v\n", err)
	} else if len(embedItems) > 0 && di.embeddingService != nil {
		fmt.Printf("Embedded %d doc/feature nodes into vector store\n", len(embedItems))
	}
	di.pushToTextStore(ctx, textDocs)

	// Create chunk-level MENTIONS links with provenance.
	if di.chunkLinker != nil {
		di.chunkLinker.SetScope(di.scopeCtx.ScopeID)
		linkCount, err := di.chunkLinker.LinkChunksForDocument(ctx, docNodeKey2, di.scopeCtx.ScopeID)
		if err != nil {
			fmt.Printf("Warning: chunk-level linking failed: %v\n", err)
		} else if linkCount > 0 {
			fmt.Printf("Chunk MENTIONS links: %d\n", linkCount)
		}
	}

	// Create relationships to code symbols using intelligent or simple linking
	if err := di.linkToCodeSymbols(ctx, docID, doc.Content); err != nil {
		fmt.Printf("Warning: failed to link to code symbols: %v\n", err)
	}

	fmt.Printf("Successfully indexed document: %s\n", doc.Title)
	return nil
}

// chunkStats tracks incremental chunk update statistics.
type chunkStats struct {
	Total     int
	Created   int
	Updated   int
	Unchanged int
}

// createDocumentChunks creates DocumentChunk nodes linked to the parent Document via HAS_CHUNK.
// Uses textHash for incremental updates — only changed chunks are written.
func (di *DocumentIndexer) createDocumentChunks(ctx context.Context, docID, docNodeKey, content string) (chunkStats, error) {
	chunks := di.parser.ChunkDocumentWithMeta(content)
	var stats chunkStats
	stats.Total = len(chunks)
	var indexDocs []textindex.IndexDoc

	// Load existing chunk hashes for this document to detect changes.
	existingHashes, err := di.loadExistingChunkHashes(ctx, docNodeKey)
	if err != nil {
		// Non-fatal: just index all chunks.
		fmt.Printf("Warning: could not load existing chunk hashes: %v\n", err)
		existingHashes = map[string]string{}
	}

	for _, chunk := range chunks {
		chunkNodeKey := models.DocumentChunkNodeKey(docNodeKey, chunk.ChunkIndex)

		// Check if chunk already exists with the same hash.
		if existingHash, exists := existingHashes[chunkNodeKey]; exists && existingHash == chunk.TextHash {
			stats.Unchanged++
			delete(existingHashes, chunkNodeKey) // Mark as still present.
			continue
		}

		if _, exists := existingHashes[chunkNodeKey]; exists {
			stats.Updated++
		} else {
			stats.Created++
		}
		delete(existingHashes, chunkNodeKey)

		chunkProps := map[string]any{
			"documentKey": docNodeKey,
			"chunkIndex":  chunk.ChunkIndex,
			"headingPath": chunk.HeadingPath,
			"content":     chunk.Content,
			"textHash":    chunk.TextHash,
			"startOffset": chunk.StartOffset,
			"endOffset":   chunk.EndOffset,
			"nodeKey":     chunkNodeKey,
			"scope":       di.scopeCtx.Scope,
			"scopeId":     di.scopeCtx.ScopeID,
		}

		chunkID, err := di.client.MergeNode(ctx, []string{"DocumentChunk"},
			map[string]any{"nodeKey": chunkNodeKey, "scopeId": di.scopeCtx.ScopeID}, chunkProps)
		if err != nil {
			return stats, fmt.Errorf("failed to create chunk %d: %w", chunk.ChunkIndex, err)
		}

		// Create HAS_CHUNK relationship from Document to DocumentChunk.
		_, err = di.client.MergeRelationship(ctx, docID, chunkID, "HAS_CHUNK",
			map[string]any{"chunkIndex": chunk.ChunkIndex},
			map[string]any{"chunkIndex": chunk.ChunkIndex, "scope": di.scopeCtx.Scope, "scopeId": di.scopeCtx.ScopeID})
		if err != nil {
			return stats, fmt.Errorf("failed to create HAS_CHUNK for chunk %d: %w", chunk.ChunkIndex, err)
		}

		// Accumulate for text store bulk push.
		if di.textStore != nil {
			indexDocs = append(indexDocs, textindex.IndexDoc{
				NodeKey: chunkNodeKey,
				Content: chunk.Content,
				Metadata: map[string]string{
					"nodeType":    "DocumentChunk",
					"documentKey": docNodeKey,
				},
			})
		}
	}

	// Remove stale chunks that no longer exist in the document.
	for staleKey := range existingHashes {
		di.deleteStaleChunk(ctx, staleKey)
	}

	// Push new/updated chunks to the text store (e.g. OpenSearch) for BM25 indexing.
	if di.textStore != nil && len(indexDocs) > 0 {
		if err := di.textStore.IndexDocuments(ctx, indexDocs); err != nil {
			log.Printf("Warning: OpenSearch chunk indexing failed: %v", err)
		}
	}

	return stats, nil
}

// loadExistingChunkHashes queries Neo4j for existing DocumentChunk nodes for this document and scope.
func (di *DocumentIndexer) loadExistingChunkHashes(ctx context.Context, docNodeKey string) (map[string]string, error) {
	cypher := `MATCH (c:DocumentChunk {documentKey: $docKey, scopeId: $scopeId})
RETURN c.nodeKey AS nodeKey, c.textHash AS textHash`
	params := map[string]any{
		"docKey":  docNodeKey,
		"scopeId": di.scopeCtx.ScopeID,
	}
	results, err := di.client.ExecuteQuery(ctx, cypher, params)
	if err != nil {
		return nil, err
	}
	hashes := make(map[string]string, len(results))
	for _, record := range results {
		m := record.AsMap()
		nk, _ := m["nodeKey"].(string)
		th, _ := m["textHash"].(string)
		if nk != "" {
			hashes[nk] = th
		}
	}
	return hashes, nil
}

// deleteStaleChunk removes a DocumentChunk node that no longer corresponds to any chunk in the document.
func (di *DocumentIndexer) deleteStaleChunk(ctx context.Context, chunkNodeKey string) {
	cypher := `MATCH (c:DocumentChunk {nodeKey: $nodeKey, scopeId: $scopeId}) DETACH DELETE c`
	params := map[string]any{
		"nodeKey": chunkNodeKey,
		"scopeId": di.scopeCtx.ScopeID,
	}
	if _, err := di.client.ExecuteQuery(ctx, cypher, params); err != nil {
		fmt.Printf("Warning: failed to delete stale chunk %s: %v\n", chunkNodeKey, err)
	}
}

// IndexDirectory recursively indexes all documents in a directory
func (di *DocumentIndexer) IndexDirectory(ctx context.Context, dirPath string) error {
	fmt.Printf("Indexing documents in directory: %s\n", dirPath)

	return filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories
		if info.IsDir() {
			return nil
		}

		// Only process document files
		if di.isDocumentFile(path) {
			if err := di.IndexDocument(ctx, path); err != nil {
				fmt.Printf("Warning: failed to index %s: %v\n", path, err)
				// Continue processing other files
			}
		}

		return nil
	})
}

// createDocumentNode creates a Document node in Neo4j
func (di *DocumentIndexer) createDocumentNode(ctx context.Context, doc *models.Document) (string, error) {
	nodeKey := models.DocumentNodeKey(doc.SourceURL)
	docProps := map[string]any{
		"title":     doc.Title,
		"type":      doc.Type,
		"sourceUrl": doc.SourceURL,
		"content":   doc.Content,
		"nodeKey":   nodeKey,
		"scope":     di.scopeCtx.Scope,
		"scopeId":   di.scopeCtx.ScopeID,
	}

	return di.client.MergeNode(ctx, []string{"Document"},
		map[string]any{"nodeKey": nodeKey, "scopeId": di.scopeCtx.ScopeID}, docProps)
}

// createFeatureNode creates a Feature node in Neo4j
func (di *DocumentIndexer) createFeatureNode(ctx context.Context, feature *models.Feature) (string, error) {
	nodeKey := models.FeatureNodeKey(feature.Name)
	featureProps := map[string]any{
		"name":        feature.Name,
		"description": feature.Description,
		"status":      feature.Status,
		"priority":    feature.Priority,
		"tags":        feature.Tags,
		"nodeKey":     nodeKey,
		"scope":       di.scopeCtx.Scope,
		"scopeId":     di.scopeCtx.ScopeID,
	}

	return di.client.MergeNode(ctx, []string{"Feature"},
		map[string]any{"nodeKey": nodeKey, "scopeId": di.scopeCtx.ScopeID}, featureProps)
}

// linkToCodeSymbols creates MENTIONS relationships between documents and code symbols
func (di *DocumentIndexer) linkToCodeSymbols(ctx context.Context, docID string, content string) error {
	if di.useIntelligentLinking && di.intelligentLinker != nil {
		// Use intelligent semantic linking
		result, err := di.intelligentLinker.LinkDocumentToCode(ctx, docID, content)
		if err != nil {
			fmt.Printf("Warning: intelligent linking failed, falling back to simple linking: %v\n", err)
			return di.simpleLinkToCodeSymbols(ctx, docID, content)
		}

		fmt.Printf("Intelligent linking created %d relationships (%d direct, %d semantic, %d call graph)\n",
			result.CreatedLinks, len(result.DirectMatches), len(result.SemanticMatches), len(result.CallGraphMatches))
		return nil
	}

	// Use simple backtick-based linking
	return di.simpleLinkToCodeSymbols(ctx, docID, content)
}

// embedItem holds the data needed to embed a single node into the vector store.
type embedItem struct {
	nodeKey string
	text    string
	label   string
}

// embedNodes generates embeddings for a batch of nodes and upserts them into
// Qdrant. It also stamps embeddedAt in Neo4j so nodes are skipped on re-runs.
func (di *DocumentIndexer) embedNodes(ctx context.Context, items []embedItem) error {
	if di.embeddingService == nil || di.vectorStore == nil || len(items) == 0 {
		return nil
	}

	texts := make([]string, len(items))
	for i, it := range items {
		texts[i] = it.text
	}

	embeddings, err := di.embeddingService.GenerateBatchEmbeddings(ctx, texts)
	if err != nil {
		return fmt.Errorf("failed to generate embeddings: %w", err)
	}

	scopeId := di.scopeCtx.ScopeID
	if scopeId == "" {
		scopeId = "main"
	}

	var upserts []search.VectorUpsert
	for i, emb := range embeddings {
		// Use scopeId::nodeKey as vector ID to prevent cross-scope collisions.
		vectorID := scopeId + "::" + items[i].nodeKey
		upserts = append(upserts, search.VectorUpsert{
			ID:        vectorID,
			Vector:    emb,
			NodeLabel: items[i].label,
			Metadata: map[string]any{
				"nodeKey": items[i].nodeKey,
				"scopeId": scopeId,
			},
		})
	}

	if err := di.vectorStore.UpsertVectors(ctx, upserts); err != nil {
		return fmt.Errorf("failed to upsert vectors: %w", err)
	}

	// Stamp embeddedAt in Neo4j.
	nodeKeys := make([]string, len(items))
	for i, it := range items {
		nodeKeys[i] = it.nodeKey
	}
	stampQuery := `
		UNWIND $keys AS key
		MATCH (n {nodeKey: key})
		SET n.embeddedAt = datetime()
	`
	if _, err := di.client.ExecuteQuery(ctx, stampQuery, map[string]any{"keys": nodeKeys}); err != nil {
		fmt.Printf("Warning: failed to stamp embeddedAt: %v\n", err)
	}

	return nil
}

// pushToTextStore indexes Document/Feature nodes into the text store (OpenSearch).
func (di *DocumentIndexer) pushToTextStore(ctx context.Context, docs []textindex.IndexDoc) {
	if di.textStore == nil || len(docs) == 0 {
		return
	}
	if err := di.textStore.IndexDocuments(ctx, docs); err != nil {
		fmt.Printf("Warning: text store indexing failed: %v\n", err)
	}
}

// simpleLinkToCodeSymbols creates MENTIONS relationships using simple backtick extraction
func (di *DocumentIndexer) simpleLinkToCodeSymbols(ctx context.Context, docID string, content string) error {
	symbols := extractCodeSymbols(content)

	for _, symbolRef := range symbols {
		// Try to find matching Symbol nodes in the database
		cypher := `
			MATCH (s:Symbol)
			WHERE s.symbol CONTAINS $symbolRef OR s.displayName CONTAINS $symbolRef
			RETURN s
			LIMIT 5
		`

		results, err := di.client.ExecuteQuery(ctx, cypher, map[string]any{
			"symbolRef": symbolRef,
		})
		if err != nil {
			continue // Skip if query fails
		}

		// Create MENTIONS relationships to found symbols
		for _, record := range results {
			recordMap := record.AsMap()
			if symbolObj, ok := recordMap["s"]; ok {
				if symbolNode, ok := symbolObj.(dbtype.Node); ok {
					_, err = di.client.CreateRelationship(ctx, docID, symbolNode.ElementId, "MENTIONS",
						map[string]any{"context": symbolRef, "scope": di.scopeCtx.Scope, "scopeId": di.scopeCtx.ScopeID})
					if err != nil {
						continue // Skip failed relationships
					}
				}
			}
		}
	}

	return nil
}

// isDocumentFile checks if a file should be processed as a document
func (di *DocumentIndexer) isDocumentFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	documentExts := map[string]bool{
		".md":  true,
		".txt": true,
		".rst": true,
		".adoc": true,
	}
	
	return documentExts[ext]
}

// GetDocumentStats returns statistics about indexed documents
func (di *DocumentIndexer) GetDocumentStats(ctx context.Context) (map[string]any, error) {
	cypher := `
		MATCH (d:Document)
		OPTIONAL MATCH (d)-[:DESCRIBES]->(f:Feature)
		OPTIONAL MATCH (d)-[:MENTIONS]->(s:Symbol)
		RETURN 
			count(DISTINCT d) as documentCount,
			count(DISTINCT f) as featureCount,
			count(DISTINCT s) as mentionedSymbolCount,
			collect(DISTINCT d.type) as documentTypes
	`
	
	results, err := di.client.ExecuteQuery(ctx, cypher, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get document stats: %w", err)
	}
	
	if len(results) > 0 {
		return results[0].AsMap(), nil
	}
	
	return map[string]any{}, nil
}

// SyncExternalDocument fetches a document from an external connector and indexes it.
func (di *DocumentIndexer) SyncExternalDocument(ctx context.Context, connector DocConnector, docID string) error {
	extDoc, err := connector.FetchDocument(ctx, docID)
	if err != nil {
		return fmt.Errorf("failed to fetch document %s: %w", docID, err)
	}

	return di.indexExternalDocument(ctx, extDoc)
}

// SyncExternalSpace fetches all documents from a space and indexes them.
func (di *DocumentIndexer) SyncExternalSpace(ctx context.Context, connector DocConnector, space string) (int, error) {
	docs, err := connector.ListDocuments(ctx, space)
	if err != nil {
		return 0, fmt.Errorf("failed to list documents in space %s: %w", space, err)
	}

	synced := 0
	for _, doc := range docs {
		fmt.Printf("Syncing: %s (%s)\n", doc.Title, doc.ID)
		extDoc, err := connector.FetchDocument(ctx, doc.ID)
		if err != nil {
			fmt.Printf("Warning: failed to fetch %s: %v\n", doc.ID, err)
			continue
		}
		if err := di.indexExternalDocument(ctx, extDoc); err != nil {
			fmt.Printf("Warning: failed to index %s: %v\n", doc.ID, err)
			continue
		}
		synced++
	}

	return synced, nil
}

// indexExternalDocument indexes a single external document with chunks.
func (di *DocumentIndexer) indexExternalDocument(ctx context.Context, extDoc *ExternalDocument) error {
	doc := &models.Document{
		Title:     extDoc.Title,
		Type:      fmt.Sprintf("External/%s", extDoc.Source),
		SourceURL: extDoc.SourceURL,
		Content:   extDoc.Content,
	}

	docID, err := di.createDocumentNode(ctx, doc)
	if err != nil {
		return fmt.Errorf("failed to create document node: %w", err)
	}

	// Create chunks.
	docNodeKey := models.DocumentNodeKey(extDoc.SourceURL)
	chunkStats, err := di.createDocumentChunks(ctx, docID, docNodeKey, extDoc.Content)
	if err != nil {
		return fmt.Errorf("failed to create chunks: %w", err)
	}

	fmt.Printf("  Chunks: %d total, %d created, %d unchanged, %d updated\n",
		chunkStats.Total, chunkStats.Created, chunkStats.Unchanged, chunkStats.Updated)

	// Push Document node to vector store and text store.
	docText := extDoc.Title + " " + extDoc.Content
	if len(docText) > 2000 {
		docText = docText[:2000]
	}
	if err := di.embedNodes(ctx, []embedItem{{nodeKey: docNodeKey, text: docText, label: "Document"}}); err != nil {
		fmt.Printf("  Warning: vector embedding for external doc failed: %v\n", err)
	}
	di.pushToTextStore(ctx, []textindex.IndexDoc{{
		NodeKey: docNodeKey, Content: extDoc.Title + " " + extDoc.Content,
		Metadata: map[string]string{"nodeType": "Document"},
	}})

	// Create chunk-level MENTIONS links with provenance (parity with IndexDocument).
	if di.chunkLinker != nil {
		di.chunkLinker.SetScope(di.scopeCtx.ScopeID)
		linkCount, linkErr := di.chunkLinker.LinkChunksForDocument(ctx, docNodeKey, di.scopeCtx.ScopeID)
		if linkErr != nil {
			fmt.Printf("  Warning: chunk-level linking failed: %v\n", linkErr)
		} else if linkCount > 0 {
			fmt.Printf("  Chunk MENTIONS links: %d\n", linkCount)
		}
	}

	// Link to code symbols.
	if err := di.linkToCodeSymbols(ctx, docID, extDoc.Content); err != nil {
		fmt.Printf("  Warning: failed to link to code symbols: %v\n", err)
	}

	return nil
}