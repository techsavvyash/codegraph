package docs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	graph "github.com/context-maximiser/code-graph/internal/graph"
	models "github.com/context-maximiser/code-graph/internal/model"
)

// ChunkRecord describes a chunk that was written (created or updated) by an
// ingest run and therefore needs (re-)linking. Layer D mining consumes these;
// unchanged chunks never appear here, which is what makes re-linking cheap.
type ChunkRecord struct {
	NodeKey     string
	ElementID   string
	DocumentKey string
	FilePath    string // repo-relative path of the owning document
	Content     string
	HeadingPath string
}

// Report summarizes one ingest run.
type Report struct {
	DocsNew             int
	DocsChanged         int
	DocsUnchanged       int
	DocsRemoved         int
	DocsSkippedTooLarge int
	DocsFailed          int
	ChunksWritten       int
	ChunksUnchanged     int
	ChunksRemoved       int

	// Changed lists every chunk written this run, for the mining pass.
	Changed []ChunkRecord
	// Failures carries per-document errors that did not abort the run.
	Failures []string
}

// Ingestor writes Document/DocumentChunk nodes for one service with
// hash-diff incremental sync (RFC-011 §4.2–4.3).
//
// Removal semantics are same-scope only: documents and chunks that disappear
// from the source are DETACH DELETEd within the ingestion scope. Hiding
// main-scope docs inside a PR overlay (tombstones) is not supported — docs
// ingestion runs in main scope in v1.
type Ingestor struct {
	client      *graph.Client
	serviceName string
	scope       models.ScopeContext
	chunker     *Chunker
}

// NewIngestor creates an Ingestor for the given service and scope.
func NewIngestor(client *graph.Client, serviceName string, scope models.ScopeContext) *Ingestor {
	return &Ingestor{
		client:      client,
		serviceName: serviceName,
		scope:       scope,
		chunker:     NewChunker(0), // default word budget
	}
}

const docBatchSize = 500

// Run ingests all documents from src. Per-document read/parse failures are
// recorded in the report and do not abort the run; graph write failures do.
func (ing *Ingestor) Run(ctx context.Context, src Source) (*Report, error) {
	report := &Report{}

	serviceID, err := ing.ensureServiceNode(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to ensure service node: %w", err)
	}

	existing, err := ing.loadExistingDocs(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load existing documents: %w", err)
	}

	refs, err := src.List(ctx)
	if err != nil {
		return nil, err
	}

	for _, ref := range refs {
		if err := ing.ingestDoc(ctx, src, ref, serviceID, existing, report); err != nil {
			return nil, fmt.Errorf("failed to ingest %s: %w", ref.RelPath, err)
		}
	}

	// Anything left in `existing` was not produced by the source this run.
	for docKey := range existing {
		if err := ing.removeDoc(ctx, docKey); err != nil {
			return nil, fmt.Errorf("failed to remove stale document %s: %w", docKey, err)
		}
		report.DocsRemoved++
	}

	return report, nil
}

// existingDoc is the prior-run state used for change detection.
type existingDoc struct {
	contentHash string
}

func (ing *Ingestor) ensureServiceNode(ctx context.Context) (string, error) {
	nodeKey := models.ServiceNodeKey(ing.serviceName)
	// SET += with only these props: an already-indexed Service keeps its
	// packageName/language/version from the SCIP run.
	return ing.client.MergeNode(ctx, []string{"Service"},
		map[string]any{"nodeKey": nodeKey, "scopeId": ing.scope.ScopeID},
		map[string]any{
			"name":    ing.serviceName,
			"nodeKey": nodeKey,
			"scope":   ing.scope.Scope,
			"scopeId": ing.scope.ScopeID,
		})
}

func (ing *Ingestor) loadExistingDocs(ctx context.Context) (map[string]existingDoc, error) {
	records, err := ing.client.ExecuteQuery(ctx, `
		MATCH (d:Document {serviceName: $serviceName, scopeId: $scopeId})
		RETURN d.nodeKey AS nodeKey, coalesce(d.contentHash, '') AS contentHash
	`, map[string]any{"serviceName": ing.serviceName, "scopeId": ing.scope.ScopeID})
	if err != nil {
		return nil, err
	}

	existing := make(map[string]existingDoc, len(records))
	for _, rec := range records {
		m := rec.AsMap()
		nk, _ := m["nodeKey"].(string)
		hash, _ := m["contentHash"].(string)
		if nk != "" {
			existing[nk] = existingDoc{contentHash: hash}
		}
	}
	return existing, nil
}

func (ing *Ingestor) ingestDoc(ctx context.Context, src Source, ref DocRef, serviceID string, existing map[string]existingDoc, report *Report) error {
	sourceURL := ing.serviceName + "/" + ref.RelPath
	docKey := models.DocumentNodeKey(sourceURL)

	content, err := src.Read(ctx, ref)
	if err != nil {
		// Per-document read failures are reported, not fatal — but the doc's
		// prior graph state (if any) is kept, so drop it from the stale set.
		delete(existing, docKey)
		var tooLarge *ErrDocTooLarge
		if errors.As(err, &tooLarge) {
			report.DocsSkippedTooLarge++
		} else {
			report.DocsFailed++
		}
		report.Failures = append(report.Failures, err.Error())
		return nil
	}

	h := sha256.Sum256(content)
	contentHash := hex.EncodeToString(h[:])

	prior, existed := existing[docKey]
	delete(existing, docKey)

	if existed && prior.contentHash == contentHash {
		report.DocsUnchanged++
		return nil
	}
	if existed {
		report.DocsChanged++
	} else {
		report.DocsNew++
	}

	chunks := ing.chunker.ChunkDocumentWithMeta(string(content))

	priorChunkHashes, err := ing.loadExistingChunkHashes(ctx, docKey)
	if err != nil {
		return err
	}

	title := ref.Title
	if title == "" {
		title = firstH1(string(content))
	}
	if title == "" {
		title = filepath.Base(ref.RelPath)
	}

	docID, err := ing.client.MergeNode(ctx, []string{"Document"},
		map[string]any{"nodeKey": docKey, "scopeId": ing.scope.ScopeID},
		map[string]any{
			"nodeKey":       docKey,
			"title":         title,
			"type":          ref.Format,
			"sourceUrl":     sourceURL,
			"content":       "", // content lives on chunks (RFC-011 §3.1)
			"serviceName":   ing.serviceName,
			"filePath":      ref.RelPath,
			"contentHash":   contentHash,
			"chunkCount":    len(chunks),
			"lastIndexedAt": time.Now().UTC().Format(time.RFC3339),
			"scope":         ing.scope.Scope,
			"scopeId":       ing.scope.ScopeID,
		})
	if err != nil {
		return err
	}

	if _, err := ing.client.MergeRelationship(ctx, serviceID, docID, string(models.ContainsRel), nil,
		map[string]any{"scope": ing.scope.Scope, "scopeId": ing.scope.ScopeID}); err != nil {
		return err
	}

	// Hash-diff: only chunks whose text changed (or that are new) get written.
	var batch []map[string]any
	var written []ChunkMeta
	for _, ch := range chunks {
		if priorHash, ok := priorChunkHashes[ch.ChunkIndex]; ok && priorHash == ch.TextHash {
			report.ChunksUnchanged++
			continue
		}
		chunkKey := models.DocumentChunkNodeKey(docKey, ch.ChunkIndex)
		batch = append(batch, map[string]any{
			"nodeKey": chunkKey,
			"scopeId": ing.scope.ScopeID,
			"props": map[string]any{
				"nodeKey":     chunkKey,
				"documentKey": docKey,
				"chunkIndex":  ch.ChunkIndex,
				"headingPath": ch.HeadingPath,
				"content":     ch.Content,
				"textHash":    ch.TextHash,
				"startOffset": ch.StartOffset,
				"endOffset":   ch.EndOffset,
				"serviceName": ing.serviceName,
				"scope":       ing.scope.Scope,
				"scopeId":     ing.scope.ScopeID,
			},
		})
		written = append(written, ch)
	}

	chunkIDs, err := ing.client.MergeNodesBatch(ctx, "DocumentChunk", batch, docBatchSize)
	if err != nil {
		return err
	}

	// HAS_CHUNK edges for the written chunks (MERGE: idempotent on re-index).
	relItems := make([]map[string]any, 0, len(chunkIDs))
	writtenIDs := make([]string, 0, len(chunkIDs))
	for _, id := range chunkIDs {
		relItems = append(relItems, map[string]any{
			"fromId": docID,
			"toId":   id,
			"props":  map[string]any{"scope": ing.scope.Scope, "scopeId": ing.scope.ScopeID},
		})
		writtenIDs = append(writtenIDs, id)
	}
	if err := ing.client.MergeRelsBatch(ctx, string(models.HasChunkRel), relItems, docBatchSize); err != nil {
		return err
	}

	// A rewritten chunk's derived state is stale: its explicit references are
	// re-mined by Layer D (minedAt cleared), and its embedding and match
	// markers are cleared so Layer S re-embeds and re-matches it. New chunks
	// have none of these; the clear is a no-op for them.
	if len(writtenIDs) > 0 {
		if _, err := ing.client.ExecuteQuery(ctx, `
			MATCH (c:DocumentChunk) WHERE elementId(c) IN $ids
			REMOVE c.embedding, c.embeddingModel, c.semlinkAt, c.semlinkModel, c.minedAt
			WITH c
			OPTIONAL MATCH (c)-[m:MENTIONS]->()
			DELETE m
		`, map[string]any{"ids": writtenIDs}); err != nil {
			return err
		}
	}

	// Chunks past the new count no longer exist in the document.
	removed, err := ing.removeTrailingChunks(ctx, docKey, len(chunks))
	if err != nil {
		return err
	}
	report.ChunksRemoved += removed
	report.ChunksWritten += len(written)

	for _, ch := range written {
		chunkKey := models.DocumentChunkNodeKey(docKey, ch.ChunkIndex)
		report.Changed = append(report.Changed, ChunkRecord{
			NodeKey:     chunkKey,
			ElementID:   chunkIDs[chunkKey],
			DocumentKey: docKey,
			FilePath:    ref.RelPath,
			Content:     ch.Content,
			HeadingPath: ch.HeadingPath,
		})
	}

	return nil
}

func (ing *Ingestor) loadExistingChunkHashes(ctx context.Context, docKey string) (map[int]string, error) {
	records, err := ing.client.ExecuteQuery(ctx, `
		MATCH (c:DocumentChunk {documentKey: $documentKey, scopeId: $scopeId})
		RETURN c.chunkIndex AS chunkIndex, c.textHash AS textHash
	`, map[string]any{"documentKey": docKey, "scopeId": ing.scope.ScopeID})
	if err != nil {
		return nil, err
	}

	hashes := make(map[int]string, len(records))
	for _, rec := range records {
		m := rec.AsMap()
		idx, ok := m["chunkIndex"].(int64)
		hash, _ := m["textHash"].(string)
		if ok && hash != "" {
			hashes[int(idx)] = hash
		}
	}
	return hashes, nil
}

func (ing *Ingestor) removeTrailingChunks(ctx context.Context, docKey string, newCount int) (int, error) {
	records, err := ing.client.ExecuteQuery(ctx, `
		MATCH (c:DocumentChunk {documentKey: $documentKey, scopeId: $scopeId})
		WHERE c.chunkIndex >= $newCount
		DETACH DELETE c
		RETURN count(c) AS removed
	`, map[string]any{"documentKey": docKey, "scopeId": ing.scope.ScopeID, "newCount": newCount})
	if err != nil {
		return 0, err
	}
	if len(records) > 0 {
		if n, ok := records[0].AsMap()["removed"].(int64); ok {
			return int(n), nil
		}
	}
	return 0, nil
}

func (ing *Ingestor) removeDoc(ctx context.Context, docKey string) error {
	_, err := ing.client.ExecuteQuery(ctx, `
		MATCH (d:Document {nodeKey: $nodeKey, scopeId: $scopeId})
		OPTIONAL MATCH (d)-[:HAS_CHUNK]->(c:DocumentChunk)
		DETACH DELETE d, c
	`, map[string]any{"nodeKey": docKey, "scopeId": ing.scope.ScopeID})
	return err
}

// firstH1 returns the text of the document's first H1 heading, or "".
func firstH1(content string) string {
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# ") {
			return strings.TrimSpace(trimmed[2:])
		}
	}
	return ""
}
