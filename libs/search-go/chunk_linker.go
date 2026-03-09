package search

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

	"github.com/context-maximiser/code-graph/libs/intelligence-go/provenance"
	"github.com/context-maximiser/code-graph/libs/neo4j-go"
)

// ChunkLinker creates MENTIONS relationships between DocumentChunk nodes and code nodes.
// Each MENTIONS edge carries provenance: scope, scopeId, confidence, reasons,
// strategy, evidenceRefs, createdAt.
type ChunkLinker struct {
	client  *neo4j.Client
	scopeID string // Scope filter for target node lookup
}

// NewChunkLinker creates a new chunk-level linker.
func NewChunkLinker(client *neo4j.Client) *ChunkLinker {
	return &ChunkLinker{client: client, scopeID: "main"}
}

// SetScope sets the scope ID used for target node lookups.
// This ensures chunk linking only finds code nodes visible in the given scope.
func (cl *ChunkLinker) SetScope(scopeID string) {
	if scopeID == "" {
		scopeID = "main"
	}
	cl.scopeID = scopeID
}

// ChunkMentionEdge represents a MENTIONS relationship from a DocumentChunk to a code node.
type ChunkMentionEdge struct {
	ChunkNodeKey  string   // Source DocumentChunk nodeKey.
	TargetNodeKey string   // Target code node nodeKey.
	TargetLabel   string   // Target node's Neo4j label.
	Confidence    float64  // 0.0 to 1.0.
	Reasons       []string // Why this link was created.
	Model         string   // Linking model/method used.
}

// LinkChunksForDocument finds all DocumentChunk nodes for a given document and
// creates MENTIONS relationships to code symbols found in each chunk's content.
func (cl *ChunkLinker) LinkChunksForDocument(ctx context.Context, docNodeKey, scopeID string) (int, error) {
	// Fetch all chunks for this document.
	chunks, err := cl.loadDocumentChunks(ctx, docNodeKey, scopeID)
	if err != nil {
		return 0, fmt.Errorf("failed to load chunks: %w", err)
	}

	var allEdges []ChunkMentionEdge
	for _, chunk := range chunks {
		edges := cl.analyzeChunkForMentions(ctx, chunk)
		allEdges = append(allEdges, edges...)
	}

	if len(allEdges) == 0 {
		return 0, nil
	}

	totalLinks, err := cl.createMentionEdgesBatch(ctx, allEdges, scopeID)
	if err != nil {
		return totalLinks, fmt.Errorf("batch mention edge creation: %w", err)
	}

	return totalLinks, nil
}

// chunkData holds data for a single chunk loaded from Neo4j.
type chunkData struct {
	NodeKey     string
	Content     string
	HeadingPath string
	ElementID   string
}

func (cl *ChunkLinker) loadDocumentChunks(ctx context.Context, docNodeKey, scopeID string) ([]chunkData, error) {
	cypher := `MATCH (c:DocumentChunk {documentKey: $docKey, scopeId: $scopeId})
RETURN c.nodeKey AS nodeKey, c.content AS content, c.headingPath AS headingPath, elementId(c) AS eid`
	params := map[string]any{
		"docKey":  docNodeKey,
		"scopeId": scopeID,
	}
	records, err := cl.client.ExecuteQuery(ctx, cypher, params)
	if err != nil {
		return nil, err
	}

	var chunks []chunkData
	for _, r := range records {
		m := r.AsMap()
		chunks = append(chunks, chunkData{
			NodeKey:     strVal(m, "nodeKey"),
			Content:     strVal(m, "content"),
			HeadingPath: strVal(m, "headingPath"),
			ElementID:   strVal(m, "eid"),
		})
	}
	return chunks, nil
}

// analyzeChunkForMentions extracts code references from a chunk's content
// and tries to match them to code nodes in the graph.
func (cl *ChunkLinker) analyzeChunkForMentions(ctx context.Context, chunk chunkData) []ChunkMentionEdge {
	var edges []ChunkMentionEdge

	// Extract backtick code references.
	codeRefs := extractBacktickRefs(chunk.Content)
	for _, ref := range codeRefs {
		matches := cl.findCodeNodesByName(ctx, ref)
		for _, match := range matches {
			edges = append(edges, ChunkMentionEdge{
				ChunkNodeKey:  chunk.NodeKey,
				TargetNodeKey: match.nodeKey,
				TargetLabel:   match.label,
				Confidence:    0.8,
				Reasons:       []string{"backtick_reference", fmt.Sprintf("mentioned as `%s`", ref)},
				Model:         "backtick_extraction",
			})
		}
	}

	// Extract heading-based context references.
	headingRefs := extractHeadingRefs(chunk.HeadingPath)
	for _, ref := range headingRefs {
		matches := cl.findCodeNodesByName(ctx, ref)
		for _, match := range matches {
			edges = append(edges, ChunkMentionEdge{
				ChunkNodeKey:  chunk.NodeKey,
				TargetNodeKey: match.nodeKey,
				TargetLabel:   match.label,
				Confidence:    0.6,
				Reasons:       []string{"heading_reference", fmt.Sprintf("heading contains '%s'", ref)},
				Model:         "heading_extraction",
			})
		}
	}

	// Deduplicate: keep highest-confidence edge per target.
	return deduplicateEdges(edges)
}

type codeNodeMatch struct {
	nodeKey string
	label   string
}

func (cl *ChunkLinker) findCodeNodesByName(ctx context.Context, name string) []codeNodeMatch {
	cypher := `
		MATCH (n)
		WHERE (n:Function OR n:Method OR n:Class OR n:Interface OR n:Symbol)
		AND (n.name = $name OR n.displayName = $name OR n.signature CONTAINS $name)
		AND (n.scopeId = $scopeId OR n.scopeId = 'main')
		RETURN n.nodeKey AS nodeKey, labels(n)[0] AS label
		LIMIT 3`
	params := map[string]any{"name": name, "scopeId": cl.scopeID}

	records, err := cl.client.ExecuteQuery(ctx, cypher, params)
	if err != nil {
		return nil
	}

	var matches []codeNodeMatch
	for _, r := range records {
		m := r.AsMap()
		nk := strVal(m, "nodeKey")
		label := strVal(m, "label")
		if nk != "" {
			matches = append(matches, codeNodeMatch{nodeKey: nk, label: label})
		}
	}
	return matches
}

func (cl *ChunkLinker) createMentionEdge(ctx context.Context, edge ChunkMentionEdge, scopeID string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	props, err := provenance.BuildMentionEdgeProps(
		edge.Confidence,
		edge.Reasons,
		edge.Model,
		now,
		scopeID,
		[]string{edge.ChunkNodeKey, edge.TargetNodeKey},
	)
	if err != nil {
		return fmt.Errorf("mention edge provenance validation failed: %w", err)
	}

	cypher := `
		MATCH (chunk:DocumentChunk {nodeKey: $chunkKey, scopeId: $scopeId})
		MATCH (target {nodeKey: $targetKey})
		WHERE target.scopeId = $scopeId OR target.scopeId = 'main'
		WITH chunk, target LIMIT 1
		MERGE (chunk)-[r:MENTIONS]->(target)
		SET r.confidence = $confidence,
		    r.reasons = $reasons,
		    r.strategy = $strategy,
		    r.model = $model,
		    r.evidenceRefs = $evidenceRefs,
		    r.createdAt = $createdAt,
		    r.scope = $scope,
		    r.scopeId = $scopeId`
	params := map[string]any{
		"chunkKey":     edge.ChunkNodeKey,
		"targetKey":    edge.TargetNodeKey,
		"scopeId":      scopeID,
		"confidence":   props["confidence"],
		"reasons":      props["reasons"],
		"strategy":     props["strategy"],
		"model":        props["model"],
		"evidenceRefs": props["evidenceRefs"],
		"createdAt":    props["createdAt"],
		"scope":        props["scope"],
	}

	_, err = cl.client.ExecuteQuery(ctx, cypher, params)
	return err
}

// createMentionEdgesBatch creates MENTIONS edges in batches using UNWIND.
// This is significantly faster than individual createMentionEdge calls for
// documents with many chunk-to-code references.
func (cl *ChunkLinker) createMentionEdgesBatch(ctx context.Context, edges []ChunkMentionEdge, scopeID string) (int, error) {
	const batchSize = 50
	totalCreated := 0

	for i := 0; i < len(edges); i += batchSize {
		end := i + batchSize
		if end > len(edges) {
			end = len(edges)
		}
		batch := edges[i:end]

		var edgeMaps []map[string]any
		now := time.Now().UTC().Format(time.RFC3339)
		for _, e := range batch {
			props, err := provenance.BuildMentionEdgeProps(
				e.Confidence,
				e.Reasons,
				e.Model,
				now,
				scopeID,
				[]string{e.ChunkNodeKey, e.TargetNodeKey},
			)
			if err != nil {
				log.Printf("Warning: skipping mention edge %s → %s: provenance validation failed: %v",
					e.ChunkNodeKey, e.TargetNodeKey, err)
				continue
			}
			edgeMaps = append(edgeMaps, map[string]any{
				"chunkKey":     e.ChunkNodeKey,
				"targetKey":    e.TargetNodeKey,
				"confidence":   props["confidence"],
				"reasons":      props["reasons"],
				"strategy":     props["strategy"],
				"model":        props["model"],
				"evidenceRefs": props["evidenceRefs"],
				"createdAt":    props["createdAt"],
				"scope":        props["scope"],
			})
		}
		if len(edgeMaps) == 0 {
			continue
		}

		cypher := `
			UNWIND $edges AS edge
			MATCH (chunk:DocumentChunk {nodeKey: edge.chunkKey, scopeId: $scopeId})
			MATCH (target {nodeKey: edge.targetKey})
			WHERE target.scopeId = $scopeId OR target.scopeId = 'main'
			WITH chunk, target, edge ORDER BY CASE WHEN target.scopeId = $scopeId THEN 0 ELSE 1 END
			WITH chunk, head(collect(target)) AS target, edge
			WHERE target IS NOT NULL
			MERGE (chunk)-[r:MENTIONS]->(target)
			SET r.confidence = edge.confidence,
			    r.reasons = edge.reasons,
			    r.strategy = edge.strategy,
			    r.model = edge.model,
			    r.evidenceRefs = edge.evidenceRefs,
				    r.createdAt = edge.createdAt,
				    r.scope = edge.scope,
				    r.scopeId = $scopeId
			RETURN count(r) AS created`
		params := map[string]any{
			"edges":   edgeMaps,
			"scopeId": scopeID,
		}

		records, err := cl.client.ExecuteQuery(ctx, cypher, params)
		if err != nil {
			log.Printf("Warning: batch mention edge creation failed for batch starting at %d: %v", i, err)
			// Fallback to individual writes for this batch.
			for _, edge := range batch {
				if err := cl.createMentionEdge(ctx, edge, scopeID); err != nil {
					log.Printf("Warning: failed to create mention edge from %s to %s: %v",
						edge.ChunkNodeKey, edge.TargetNodeKey, err)
					continue
				}
				totalCreated++
			}
			continue
		}

		if len(records) > 0 {
			m := records[0].AsMap()
			if cnt, ok := m["created"].(int64); ok {
				totalCreated += int(cnt)
			} else {
				totalCreated += len(batch) // Assume all created if count unavailable.
			}
		}
	}

	return totalCreated, nil
}

// extractBacktickRefs extracts code references from backtick expressions.
func extractBacktickRefs(content string) []string {
	pattern := regexp.MustCompile("`([A-Za-z_][A-Za-z0-9_]*(?:\\.[A-Za-z_][A-Za-z0-9_]*)*(?:\\(\\))?)`")
	matches := pattern.FindAllStringSubmatch(content, -1)

	seen := make(map[string]bool)
	var refs []string
	for _, m := range matches {
		if len(m) > 1 {
			ref := m[1]
			if !seen[ref] && isCodeLikeReference(ref) {
				seen[ref] = true
				refs = append(refs, ref)
			}
		}
	}
	return refs
}

// extractHeadingRefs extracts potential code references from a heading path.
func extractHeadingRefs(headingPath string) []string {
	if headingPath == "" {
		return nil
	}

	// Look for CamelCase or PascalCase words in headings.
	pattern := regexp.MustCompile(`([A-Z][a-z]+(?:[A-Z][a-z]+)+)`)
	matches := pattern.FindAllString(headingPath, -1)

	seen := make(map[string]bool)
	var refs []string
	for _, m := range matches {
		if !seen[m] {
			seen[m] = true
			refs = append(refs, m)
		}
	}
	return refs
}

// isCodeLikeReference checks if a backtick reference looks like code.
func isCodeLikeReference(ref string) bool {
	if len(ref) < 2 {
		return false
	}
	// Must contain uppercase, underscore, or dot.
	return regexp.MustCompile(`[A-Z_.]`).MatchString(ref)
}

// deduplicateEdges keeps the highest-confidence edge per target.
func deduplicateEdges(edges []ChunkMentionEdge) []ChunkMentionEdge {
	best := make(map[string]ChunkMentionEdge)
	for _, e := range edges {
		key := e.ChunkNodeKey + "→" + e.TargetNodeKey
		if existing, ok := best[key]; !ok || e.Confidence > existing.Confidence {
			// Merge reasons.
			if ok {
				e.Reasons = append(existing.Reasons, e.Reasons...)
			}
			best[key] = e
		}
	}

	result := make([]ChunkMentionEdge, 0, len(best))
	for _, e := range best {
		result = append(result, e)
	}
	return result
}

func strVal(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// removeDuplicateStringsSearch deduplicates a string slice.
func removeDuplicateStringsSearch(slice []string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, s := range slice {
		lower := strings.ToLower(s)
		if !seen[lower] {
			seen[lower] = true
			result = append(result, s)
		}
	}
	return result
}
