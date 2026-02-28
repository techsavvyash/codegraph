package search

import (
	"context"
	"fmt"
	"log"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/context-maximiser/code-graph/libs/intelligence-go/provenance"
	"github.com/context-maximiser/code-graph/libs/neo4j-go"
)

// IntelligentDocumentLinker creates semantic relationships between documents and code
type IntelligentDocumentLinker struct {
	client           *neo4j.Client
	semanticAnalyzer *SemanticDocumentAnalyzer
	vectorStore      VectorStore
	hybridSearch     *HybridSearchManager
	scopeID          string // Scope filter for target node lookup
}

// SetScope sets the scope ID used for target node lookups and relationship creation.
func (idl *IntelligentDocumentLinker) SetScope(scopeID string) {
	if scopeID == "" {
		scopeID = "main"
	}
	idl.scopeID = scopeID
	if idl.hybridSearch != nil {
		idl.hybridSearch.SetScope(scopeID)
	}
}

// NewIntelligentDocumentLinker creates a new intelligent document linker
func NewIntelligentDocumentLinker(client *neo4j.Client, embeddingService EmbeddingService, vectorStore VectorStore) *IntelligentDocumentLinker {
	return &IntelligentDocumentLinker{
		client:           client,
		semanticAnalyzer: NewSemanticDocumentAnalyzer(embeddingService),
		vectorStore:      vectorStore,
		hybridSearch:     NewHybridSearchManager(client, embeddingService, vectorStore),
		scopeID:          "main",
	}
}

// CodeMatch represents a potential match between document and code
type CodeMatch struct {
	NodeKey        string   `json:"nodeKey"`
	NodeType       string   `json:"nodeType"`
	Name           string   `json:"name"`
	Signature      string   `json:"signature,omitempty"`
	FilePath       string   `json:"filePath"`
	Confidence     float64  `json:"confidence"`
	MatchReasons   []string `json:"matchReasons"`
	CallGraphDepth int      `json:"callGraphDepth"`
}

// LinkingResult contains the results of intelligent document linking
type LinkingResult struct {
	DocumentID    string      `json:"documentId"`
	DirectMatches []CodeMatch `json:"directMatches"`
	SemanticMatches []CodeMatch `json:"semanticMatches"`
	CallGraphMatches []CodeMatch `json:"callGraphMatches"`
	CreatedLinks  int         `json:"createdLinks"`
}

// LinkDocumentToCode performs intelligent linking between a document and code
func (idl *IntelligentDocumentLinker) LinkDocumentToCode(ctx context.Context, documentID string, content string) (*LinkingResult, error) {
	log.Printf("Starting intelligent linking for document: %s", documentID)

	result := &LinkingResult{
		DocumentID: documentID,
	}

	// Step 1: Analyze document semantically
	searchQueries, concepts, err := idl.semanticAnalyzer.AnalyzeForCodeMapping(ctx, content)
	if err != nil {
		return nil, fmt.Errorf("semantic analysis failed: %w", err)
	}

	log.Printf("Found %d search queries and %d technical concepts", len(searchQueries), len(concepts))

	// Step 2: Find direct matches (existing backtick references)
	directMatches := idl.findDirectMatches(ctx, content)
	result.DirectMatches = directMatches

	// Step 3: Find semantic matches using embeddings
	semanticMatches, err := idl.findSemanticMatches(ctx, content, searchQueries)
	if err != nil {
		log.Printf("Warning: semantic matching failed: %v", err)
	} else {
		result.SemanticMatches = semanticMatches
	}

	// Step 4: Expand matches using call graph analysis
	callGraphMatches, err := idl.expandWithCallGraph(ctx, append(directMatches, semanticMatches...))
	if err != nil {
		log.Printf("Warning: call graph expansion failed: %v", err)
	} else {
		result.CallGraphMatches = callGraphMatches
	}

	// Step 5: Create relationships in the database
	allMatches := idl.consolidateMatches(directMatches, semanticMatches, callGraphMatches)
	createdLinks, err := idl.createMentionsRelationships(ctx, documentID, allMatches)
	if err != nil {
		return nil, fmt.Errorf("failed to create relationships: %w", err)
	}

	result.CreatedLinks = createdLinks

	log.Printf("Intelligent linking complete: %d direct, %d semantic, %d call graph matches, %d links created",
		len(directMatches), len(semanticMatches), len(callGraphMatches), createdLinks)

	return result, nil
}

// findDirectMatches finds functions/classes explicitly mentioned in backticks
func (idl *IntelligentDocumentLinker) findDirectMatches(ctx context.Context, content string) []CodeMatch {
	var matches []CodeMatch

	// Extract code symbols from content
	symbols := idl.extractCodeSymbols(content)

	for _, symbol := range symbols {
		cypher := `
			MATCH (n)
			WHERE (n:Function OR n:Method OR n:Class)
			AND (n.name = $symbol OR n.displayName = $symbol)
			AND (n.scopeId = $scopeId OR n.scopeId = 'main')
			RETURN n.nodeKey AS nodeKey, labels(n)[0] AS type, n.name AS name,
				   n.signature AS signature, n.filePath AS filePath
			LIMIT 5
		`

		results, err := idl.client.ExecuteQuery(ctx, cypher, map[string]any{
			"symbol":  symbol,
			"scopeId": idl.scopeID,
		})
		if err != nil {
			continue
		}

		for _, record := range results {
			recordMap := record.AsMap()
			nk := getStringValue(recordMap, "nodeKey")
			if nk == "" {
				continue
			}
			matches = append(matches, CodeMatch{
				NodeKey:        nk,
				NodeType:       getStringValue(recordMap, "type"),
				Name:           getStringValue(recordMap, "name"),
				Signature:      getStringValue(recordMap, "signature"),
				FilePath:       getStringValue(recordMap, "filePath"),
				Confidence:     1.0, // Direct matches have highest confidence
				MatchReasons:   []string{"direct_reference"},
				CallGraphDepth: 0,
			})
		}
	}

	return matches
}

// findSemanticMatches finds code using semantic similarity
func (idl *IntelligentDocumentLinker) findSemanticMatches(ctx context.Context, content string, searchQueries []string) ([]CodeMatch, error) {
	var allMatches []CodeMatch

	// Generate embedding for the document content
	docEmbedding, err := idl.semanticAnalyzer.GenerateSemanticEmbedding(ctx, content)
	if err != nil {
		return nil, fmt.Errorf("failed to generate document embedding: %w", err)
	}

	// Search for similar functions using vector store
	if idl.vectorStore != nil {
		vectorResults, err := idl.vectorStore.Query(ctx, VectorQuery{
			Vector: docEmbedding,
			Limit:  20,
			Filters: map[string]any{
				"scopeId": []string{idl.scopeID, "main"},
			},
		})
		if err == nil {
			for _, result := range vectorResults {
				confidence := idl.calculateSemanticConfidence(result.Score)
				if confidence > 0.1 {
					name, _ := result.Metadata["name"].(string)
					sig, _ := result.Metadata["signature"].(string)
					fp, _ := result.Metadata["filePath"].(string)
					nl, _ := result.Metadata["nodeLabel"].(string)
					nk, _ := result.Metadata["nodeKey"].(string)
					if nk == "" {
						nk = result.ID
					}
					allMatches = append(allMatches, CodeMatch{
						NodeKey:        nk,
						NodeType:       nl,
						Name:           name,
						Signature:      sig,
						FilePath:       fp,
						Confidence:     confidence,
						MatchReasons:   []string{"vector_similarity"},
						CallGraphDepth: 0,
					})
				}
			}
		}
	}

	allMatches = idl.enrichMatchesWithGraphEvidence(ctx, allMatches)

	// Also search using hybrid search for each query
	for _, query := range searchQueries[:minInt(5, len(searchQueries))] { // Limit to first 5 queries
		hybridResults, err := idl.hybridSearch.UnifiedSearch(ctx, query, 10)
		if err != nil {
			continue
		}

		for _, result := range hybridResults.Results {
			confidence := idl.calculateHybridConfidence(result.CombinedScore, query, getStringValue(result.Node, "name"))
			if confidence > 0.05 {
				nk := getStringValue(result.Node, "nodeKey")
				if nk == "" {
					continue
				}
				allMatches = append(allMatches, CodeMatch{
					NodeKey:        nk,
					NodeType:       strings.Join(result.Labels, ","),
					Name:           getStringValue(result.Node, "name"),
					Signature:      getStringValue(result.Node, "signature"),
					FilePath:       getStringValue(result.Node, "filePath"),
					Confidence:     confidence,
					MatchReasons:   []string{"hybrid_search", "query:" + query},
					CallGraphDepth: 0,
				})
			}
		}
	}

	// Remove duplicates and sort by confidence
	uniqueMatches := idl.deduplicateMatches(allMatches)
	sort.Slice(uniqueMatches, func(i, j int) bool {
		return uniqueMatches[i].Confidence > uniqueMatches[j].Confidence
	})

	// Return top matches
	maxResults := 15
	if len(uniqueMatches) < maxResults {
		maxResults = len(uniqueMatches)
	}

	return uniqueMatches[:maxResults], nil
}

// expandWithCallGraph expands matches using call graph relationships
func (idl *IntelligentDocumentLinker) expandWithCallGraph(ctx context.Context, baseMatches []CodeMatch) ([]CodeMatch, error) {
	var expandedMatches []CodeMatch

	for _, match := range baseMatches {
		if match.Confidence < 0.4 { // Only expand high-confidence matches
			continue
		}

		// Find functions called by this function (callees)
		callees, err := idl.findCallees(ctx, match.NodeKey, 2) // Max depth of 2
		if err == nil {
			for _, callee := range callees {
				confidence := match.Confidence * 0.7 * math.Pow(0.8, float64(callee.CallGraphDepth)) // Decay with depth
				if confidence > 0.2 {
					callee.Confidence = confidence
					callee.MatchReasons = append([]string{"call_graph_callee"}, match.MatchReasons...)
					expandedMatches = append(expandedMatches, callee)
				}
			}
		}

		// Find functions that call this function (callers)
		callers, err := idl.findCallers(ctx, match.NodeKey, 2) // Max depth of 2
		if err == nil {
			for _, caller := range callers {
				confidence := match.Confidence * 0.6 * math.Pow(0.8, float64(caller.CallGraphDepth)) // Slightly lower confidence for callers
				if confidence > 0.2 {
					caller.Confidence = confidence
					caller.MatchReasons = append([]string{"call_graph_caller"}, match.MatchReasons...)
					expandedMatches = append(expandedMatches, caller)
				}
			}
		}
	}

	return expandedMatches, nil
}

// findCallees finds functions called by the given function
func (idl *IntelligentDocumentLinker) findCallees(ctx context.Context, functionNodeKey string, maxDepth int) ([]CodeMatch, error) {
	cypher := `
		MATCH (f {nodeKey: $nodeKey})
		WHERE (f:Function OR f:Method)
		  AND (f.scopeId = $scopeId OR f.scopeId = 'main')
		WITH f
		MATCH (f)-[:CALLS*1..` + fmt.Sprintf("%d", maxDepth) + `]->(callee)
		WHERE (callee:Function OR callee:Method)
		  AND (callee.scopeId = $scopeId OR callee.scopeId = 'main')
		RETURN callee.nodeKey AS nodeKey, labels(callee)[0] AS type, callee.name AS name,
		       callee.signature AS signature, callee.filePath AS filePath
		LIMIT 20
	`

	results, err := idl.client.ExecuteQuery(ctx, cypher, map[string]any{
		"nodeKey": functionNodeKey,
		"scopeId": idl.scopeID,
	})
	if err != nil {
		return nil, err
	}

	var matches []CodeMatch
	for _, record := range results {
		recordMap := record.AsMap()
		nk := getStringValue(recordMap, "nodeKey")
		if nk == "" {
			continue
		}
		matches = append(matches, CodeMatch{
			NodeKey:        nk,
			NodeType:       getStringValue(recordMap, "type"),
			Name:           getStringValue(recordMap, "name"),
			Signature:      getStringValue(recordMap, "signature"),
			FilePath:       getStringValue(recordMap, "filePath"),
			CallGraphDepth: 1,
		})
	}

	return matches, nil
}

// findCallers finds functions that call the given function
func (idl *IntelligentDocumentLinker) findCallers(ctx context.Context, functionNodeKey string, maxDepth int) ([]CodeMatch, error) {
	cypher := `
		MATCH (f {nodeKey: $nodeKey})
		WHERE (f:Function OR f:Method)
		  AND (f.scopeId = $scopeId OR f.scopeId = 'main')
		WITH f
		MATCH (caller)-[:CALLS*1..` + fmt.Sprintf("%d", maxDepth) + `]->(f)
		WHERE (caller:Function OR caller:Method)
		  AND (caller.scopeId = $scopeId OR caller.scopeId = 'main')
		RETURN caller.nodeKey AS nodeKey, labels(caller)[0] AS type, caller.name AS name,
		       caller.signature AS signature, caller.filePath AS filePath
		LIMIT 20
	`

	results, err := idl.client.ExecuteQuery(ctx, cypher, map[string]any{
		"nodeKey": functionNodeKey,
		"scopeId": idl.scopeID,
	})
	if err != nil {
		return nil, err
	}

	var matches []CodeMatch
	for _, record := range results {
		recordMap := record.AsMap()
		nk := getStringValue(recordMap, "nodeKey")
		if nk == "" {
			continue
		}
		matches = append(matches, CodeMatch{
			NodeKey:        nk,
			NodeType:       getStringValue(recordMap, "type"),
			Name:           getStringValue(recordMap, "name"),
			Signature:      getStringValue(recordMap, "signature"),
			FilePath:       getStringValue(recordMap, "filePath"),
			CallGraphDepth: 1,
		})
	}

	return matches, nil
}

// consolidateMatches removes duplicates and combines matches
func (idl *IntelligentDocumentLinker) consolidateMatches(directMatches, semanticMatches, callGraphMatches []CodeMatch) []CodeMatch {
	seen := make(map[string]*CodeMatch)

	// Add all matches, combining reasons for duplicates
	allMatches := [][]CodeMatch{directMatches, semanticMatches, callGraphMatches}
	for _, matchGroup := range allMatches {
		for _, match := range matchGroup {
			if existing, exists := seen[match.NodeKey]; exists {
				// Combine confidence scores (take the higher one)
				if match.Confidence > existing.Confidence {
					existing.Confidence = match.Confidence
				}
				// Combine match reasons
				existing.MatchReasons = append(existing.MatchReasons, match.MatchReasons...)
			} else {
				// Create copy to avoid modifying original
				matchCopy := match
				seen[match.NodeKey] = &matchCopy
			}
		}
	}

	// Convert back to slice
	var result []CodeMatch
	for _, match := range seen {
		result = append(result, *match)
	}

	// Sort by confidence
	sort.Slice(result, func(i, j int) bool {
		return result[i].Confidence > result[j].Confidence
	})

	return result
}

// createMentionsRelationships creates MENTIONS relationships in the database using batch UNWIND.
func (idl *IntelligentDocumentLinker) createMentionsRelationships(ctx context.Context, documentID string, matches []CodeMatch) (int, error) {
	// Filter out low-confidence matches.
	var filtered []CodeMatch
	for _, match := range matches {
		if match.Confidence >= 0.15 {
			filtered = append(filtered, match)
		}
	}
	if len(filtered) == 0 {
		return 0, nil
	}

	log.Printf("Creating MENTIONS edges: docID=%s, %d matches after confidence filter (>= 0.15)", documentID, len(filtered))

	// Build batch parameter list with provenance fields.
	now := time.Now().UTC().Format(time.RFC3339)
	var edgeMaps []map[string]any
	for _, match := range filtered {
		props := map[string]any{
			"targetKey":      match.NodeKey,
			"confidence":     match.Confidence,
			"reasons":        match.MatchReasons,
			"callGraphDepth": match.CallGraphDepth,
			"model":          "intelligent_linking",
			"createdAt":      now,
			"scopeId":        idl.scopeID,
		}
		if err := provenance.ValidateMentionEdgeProps(props); err != nil {
			log.Printf("Warning: skipping MENTIONS edge to %s: provenance validation failed: %v", match.NodeKey, err)
			continue
		}
		edgeMaps = append(edgeMaps, props)
	}
	if len(edgeMaps) == 0 {
		return 0, nil
	}

	// documentID may be a Neo4j element ID (e.g. "4:abc:123") or a nodeKey.
	// Try matching by elementId first, fall back to nodeKey.
	cypher := `
		MATCH (doc)
		WHERE elementId(doc) = $docKey OR doc.nodeKey = $docKey
		WITH doc LIMIT 1
		UNWIND $edges AS edge
		MATCH (target {nodeKey: edge.targetKey})
		WHERE target.scopeId = $scopeId OR target.scopeId = 'main'
		WITH doc, target, edge ORDER BY CASE WHEN target.scopeId = $scopeId THEN 0 ELSE 1 END
		WITH doc, head(collect(target)) AS target, edge
		WHERE target IS NOT NULL
		MERGE (doc)-[r:MENTIONS]->(target)
		SET r.confidence = edge.confidence,
		    r.reasons = edge.reasons,
		    r.contextType = 'intelligent_linking',
		    r.callGraphDepth = edge.callGraphDepth,
		    r.model = edge.model,
		    r.createdAt = edge.createdAt,
		    r.scopeId = edge.scopeId
		RETURN count(r) AS created
	`
	records, err := idl.client.ExecuteQuery(ctx, cypher, map[string]any{
		"docKey":  documentID,
		"scopeId": idl.scopeID,
		"edges":   edgeMaps,
	})
	if err != nil {
		return 0, fmt.Errorf("batch MENTIONS creation failed: %w", err)
	}

	if len(records) > 0 {
		m := records[0].AsMap()
		if cnt, ok := m["created"].(int64); ok {
			return int(cnt), nil
		}
	}
	return len(filtered), nil
}

// Helper functions

func (idl *IntelligentDocumentLinker) calculateSemanticConfidence(similarity float64) float64 {
	// Convert cosine similarity to confidence.
	// OpenAI text-embedding-3-small typically returns 0.2-0.6 for related content.
	// Gemini returns 0.5-0.9.  Use a floor of 0.15 so both providers work.
	if similarity < 0.15 {
		return 0
	}
	return math.Min(1.0, math.Max(0, (similarity-0.15)/0.65)) // 0.15→0, 0.80→1.0
}

func (idl *IntelligentDocumentLinker) calculateHybridConfidence(score float64, query, functionName string) float64 {
	// CombinedScore is an RRF score with k=60, so the top result ≈ 1/61 ≈ 0.016.
	// Multi-source results can sum to ~0.05.  Normalize to [0, 1].
	confidence := math.Min(score/0.05, 1.0)

	// Boost if query appears in function name
	if len(query) > 2 && contains(functionName, query) {
		confidence = math.Min(confidence*1.5, 1.0)
	}

	return confidence
}

func (idl *IntelligentDocumentLinker) deduplicateMatches(matches []CodeMatch) []CodeMatch {
	seen := make(map[string]bool)
	var unique []CodeMatch

	for _, match := range matches {
		if !seen[match.NodeKey] {
			seen[match.NodeKey] = true
			unique = append(unique, match)
		}
	}

	return unique
}

// enrichMatchesWithGraphEvidence adjusts confidence using graph-grounded signals
// rather than only lexical/vector heuristics.
func (idl *IntelligentDocumentLinker) enrichMatchesWithGraphEvidence(ctx context.Context, matches []CodeMatch) []CodeMatch {
	if len(matches) == 0 {
		return matches
	}

	nodeKeys := make([]string, 0, len(matches))
	seen := make(map[string]bool)
	for _, m := range matches {
		if m.NodeKey == "" || seen[m.NodeKey] {
			continue
		}
		seen[m.NodeKey] = true
		nodeKeys = append(nodeKeys, m.NodeKey)
	}
	if len(nodeKeys) == 0 {
		return matches
	}

	cypher := `
		UNWIND $nodeKeys AS nk
		MATCH (n {nodeKey: nk})
		WHERE n.scopeId = $scopeId OR n.scopeId = 'main'
		WITH n ORDER BY CASE WHEN n.scopeId = $scopeId THEN 0 ELSE 1 END
		WITH head(collect(n)) AS n
		WHERE n IS NOT NULL
		OPTIONAL MATCH (n)<-[:HAS_STEP]-(:Flow)
		WITH n, count(*) AS flowRefs
		OPTIONAL MATCH (n)<-[:CALLS]-(:Function)
		WITH n, flowRefs, count(*) AS incomingCalls
		OPTIONAL MATCH (n)-[:CALLS]->(:Function)
		RETURN n.nodeKey AS nodeKey,
		       flowRefs AS flowRefs,
		       incomingCalls AS incomingCalls,
		       count(*) AS outgoingCalls,
		       coalesce(n.isExported, false) AS isExported,
		       size(coalesce(n.docstring, '')) > 0 AS hasDocstring`

	rows, err := idl.client.ExecuteQuery(ctx, cypher, map[string]any{
		"nodeKeys": nodeKeys,
		"scopeId":  idl.scopeID,
	})
	if err != nil {
		return matches
	}

	type evidence struct {
		flowRefs      int
		incomingCalls int
		outgoingCalls int
		isExported    bool
		hasDocstring  bool
	}
	evByKey := make(map[string]evidence, len(rows))
	for _, r := range rows {
		m := r.AsMap()
		nk := getStringValue(m, "nodeKey")
		if nk == "" {
			continue
		}
		evByKey[nk] = evidence{
			flowRefs:      int(getInt64(m, "flowRefs")),
			incomingCalls: int(getInt64(m, "incomingCalls")),
			outgoingCalls: int(getInt64(m, "outgoingCalls")),
			isExported:    getBool(m, "isExported"),
			hasDocstring:  getBool(m, "hasDocstring"),
		}
	}

	for i := range matches {
		ev, ok := evByKey[matches[i].NodeKey]
		if !ok {
			continue
		}
		boost := 0.0
		if ev.flowRefs > 0 {
			boost += 0.12
		}
		if ev.incomingCalls > 0 {
			boost += 0.07
		}
		if ev.outgoingCalls > 0 {
			boost += 0.05
		}
		if ev.isExported {
			boost += 0.05
		}
		if ev.hasDocstring {
			boost += 0.03
		}
		if boost > 0 {
			matches[i].Confidence = math.Min(1.0, matches[i].Confidence+boost)
			matches[i].MatchReasons = append(matches[i].MatchReasons, "graph_evidence")
		}
	}

	return matches
}

func getInt64(m map[string]any, key string) int64 {
	v, ok := m[key]
	if !ok {
		return 0
	}
	switch t := v.(type) {
	case int64:
		return t
	case int:
		return int64(t)
	case float64:
		return int64(t)
	default:
		return 0
	}
}

func getBool(m map[string]any, key string) bool {
	v, ok := m[key]
	if !ok {
		return false
	}
	b, ok := v.(bool)
	return ok && b
}

func getStringValue(m map[string]any, key string) string {
	if val, ok := m[key]; ok {
		if str, ok := val.(string); ok {
			return str
		}
	}
	return ""
}

func contains(s, substr string) bool {
	return len(substr) > 0 && len(s) >= len(substr) &&
		(s == substr ||
		 (len(s) > len(substr) &&
		  (s[:len(substr)] == substr ||
		   s[len(s)-len(substr):] == substr ||
		   findSubstring(s, substr))))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// extractCodeSymbols finds references to code symbols in the document
func (idl *IntelligentDocumentLinker) extractCodeSymbols(content string) []string {
	var symbols []string

	// Pattern for code references in backticks
	codePattern := regexp.MustCompile("`([A-Za-z_][A-Za-z0-9_]*(?:\\.[A-Za-z_][A-Za-z0-9_]*)*(?:\\(\\))?)`")
	matches := codePattern.FindAllStringSubmatch(content, -1)

	for _, match := range matches {
		if len(match) > 1 {
			symbol := match[1]
			// Filter out common words that aren't likely to be code symbols
			if idl.isLikelyCodeSymbol(symbol) {
				symbols = append(symbols, symbol)
			}
		}
	}

	return symbols
}

// isLikelyCodeSymbol determines if a string is likely a code symbol
func (idl *IntelligentDocumentLinker) isLikelyCodeSymbol(symbol string) bool {
	// Skip common English words
	commonWords := []string{"the", "and", "or", "but", "if", "then", "else", "for", "while", "when", "where", "what", "how", "why", "who", "which"}
	lowerSymbol := strings.ToLower(symbol)

	for _, word := range commonWords {
		if word == lowerSymbol {
			return false
		}
	}

	// Must start with letter or underscore
	if len(symbol) == 0 || (!((symbol[0] >= 'A' && symbol[0] <= 'Z') || (symbol[0] >= 'a' && symbol[0] <= 'z') || symbol[0] == '_')) {
		return false
	}

	// Should have at least one capital letter (camelCase/PascalCase) or underscore
	hasCapital := false
	hasUnderscore := false
	for _, char := range symbol {
		if char >= 'A' && char <= 'Z' {
			hasCapital = true
		}
		if char == '_' {
			hasUnderscore = true
		}
	}

	return hasCapital || hasUnderscore || strings.Contains(symbol, ".")
}
