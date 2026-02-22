package search

import (
	"context"
	"fmt"
	"log"
	"math"
	"regexp"
	"sort"
	"strings"

	"github.com/context-maximiser/code-graph/libs/core-models-go"
	"github.com/context-maximiser/code-graph/libs/neo4j-go"
)

// IntelligentDocumentLinker creates semantic relationships between documents and code
type IntelligentDocumentLinker struct {
	client           *neo4j.Client
	semanticAnalyzer *SemanticDocumentAnalyzer
	vectorStore      VectorStore
	hybridSearch     *HybridSearchManager
}

// NewIntelligentDocumentLinker creates a new intelligent document linker
func NewIntelligentDocumentLinker(client *neo4j.Client, embeddingService EmbeddingService, vectorStore VectorStore) *IntelligentDocumentLinker {
	return &IntelligentDocumentLinker{
		client:           client,
		semanticAnalyzer: NewSemanticDocumentAnalyzer(embeddingService),
		vectorStore:      vectorStore,
		hybridSearch:     NewHybridSearchManager(client, embeddingService, vectorStore),
	}
}

// CodeMatch represents a potential match between document and code
type CodeMatch struct {
	NodeID         string   `json:"nodeId"`
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
			MATCH (n:Function|Method|Class)
			WHERE n.name = $symbol OR n.displayName = $symbol
			RETURN n.id AS id, labels(n)[0] AS type, n.name AS name,
				   n.signature AS signature, n.filePath AS filePath
			LIMIT 5
		`

		results, err := idl.client.ExecuteQuery(ctx, cypher, map[string]any{
			"symbol": symbol,
		})
		if err != nil {
			continue
		}

		for _, record := range results {
			recordMap := record.AsMap()
			matches = append(matches, CodeMatch{
				NodeID:       recordMap["id"].(string),
				NodeType:     recordMap["type"].(string),
				Name:         recordMap["name"].(string),
				Signature:    getStringValue(recordMap, "signature"),
				FilePath:     getStringValue(recordMap, "filePath"),
				Confidence:   1.0, // Direct matches have highest confidence
				MatchReasons: []string{"direct_reference"},
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
		})
		if err == nil {
			for _, result := range vectorResults {
				confidence := idl.calculateSemanticConfidence(result.Score)
				if confidence > 0.3 {
					name, _ := result.Metadata["name"].(string)
					sig, _ := result.Metadata["signature"].(string)
					fp, _ := result.Metadata["filePath"].(string)
					nl, _ := result.Metadata["nodeLabel"].(string)
					allMatches = append(allMatches, CodeMatch{
						NodeID:         result.ID,
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

	// Also search using hybrid search for each query
	for _, query := range searchQueries[:minInt(5, len(searchQueries))] { // Limit to first 5 queries
		hybridResults, err := idl.hybridSearch.UnifiedSearch(ctx, query, 10)
		if err != nil {
			continue
		}

		for _, result := range hybridResults.Results {
			confidence := idl.calculateHybridConfidence(result.CombinedScore, query, getStringValue(result.Node, "name"))
			if confidence > 0.2 {
				allMatches = append(allMatches, CodeMatch{
					NodeID:       getStringValue(result.Node, "id"),
					NodeType:     strings.Join(result.Labels, ","),
					Name:         getStringValue(result.Node, "name"),
					Signature:    getStringValue(result.Node, "signature"),
					FilePath:     getStringValue(result.Node, "filePath"),
					Confidence:   confidence,
					MatchReasons: []string{"hybrid_search", "query:" + query},
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
		callees, err := idl.findCallees(ctx, match.NodeID, 2) // Max depth of 2
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
		callers, err := idl.findCallers(ctx, match.NodeID, 2) // Max depth of 2
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
func (idl *IntelligentDocumentLinker) findCallees(ctx context.Context, functionID string, maxDepth int) ([]CodeMatch, error) {
	cypher := `
		MATCH (f:Function {id: $functionId})-[:CALLS*1..` + fmt.Sprintf("%d", maxDepth) + `]->(callee:Function)
		RETURN callee.id AS id, labels(callee)[0] AS type, callee.name AS name,
			   callee.signature AS signature, callee.filePath AS filePath,
			   length(()-[:CALLS*]->(callee)) AS depth
		LIMIT 20
	`

	results, err := idl.client.ExecuteQuery(ctx, cypher, map[string]any{
		"functionId": functionID,
	})
	if err != nil {
		return nil, err
	}

	var matches []CodeMatch
	for _, record := range results {
		recordMap := record.AsMap()
		depth := 0
		if d, ok := recordMap["depth"].(int64); ok {
			depth = int(d)
		}

		matches = append(matches, CodeMatch{
			NodeID:         recordMap["id"].(string),
			NodeType:       recordMap["type"].(string),
			Name:           recordMap["name"].(string),
			Signature:      getStringValue(recordMap, "signature"),
			FilePath:       getStringValue(recordMap, "filePath"),
			CallGraphDepth: depth,
		})
	}

	return matches, nil
}

// findCallers finds functions that call the given function
func (idl *IntelligentDocumentLinker) findCallers(ctx context.Context, functionID string, maxDepth int) ([]CodeMatch, error) {
	cypher := `
		MATCH (caller:Function)-[:CALLS*1..` + fmt.Sprintf("%d", maxDepth) + `]->(f:Function {id: $functionId})
		RETURN caller.id AS id, labels(caller)[0] AS type, caller.name AS name,
			   caller.signature AS signature, caller.filePath AS filePath,
			   length((caller)-[:CALLS*]->()) AS depth
		LIMIT 20
	`

	results, err := idl.client.ExecuteQuery(ctx, cypher, map[string]any{
		"functionId": functionID,
	})
	if err != nil {
		return nil, err
	}

	var matches []CodeMatch
	for _, record := range results {
		recordMap := record.AsMap()
		depth := 0
		if d, ok := recordMap["depth"].(int64); ok {
			depth = int(d)
		}

		matches = append(matches, CodeMatch{
			NodeID:         recordMap["id"].(string),
			NodeType:       recordMap["type"].(string),
			Name:           recordMap["name"].(string),
			Signature:      getStringValue(recordMap, "signature"),
			FilePath:       getStringValue(recordMap, "filePath"),
			CallGraphDepth: depth,
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
			if existing, exists := seen[match.NodeID]; exists {
				// Combine confidence scores (take the higher one)
				if match.Confidence > existing.Confidence {
					existing.Confidence = match.Confidence
				}
				// Combine match reasons
				existing.MatchReasons = append(existing.MatchReasons, match.MatchReasons...)
			} else {
				// Create copy to avoid modifying original
				matchCopy := match
				seen[match.NodeID] = &matchCopy
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

// createMentionsRelationships creates MENTIONS relationships in the database
func (idl *IntelligentDocumentLinker) createMentionsRelationships(ctx context.Context, documentID string, matches []CodeMatch) (int, error) {
	createdLinks := 0

	for _, match := range matches {
		if match.Confidence < 0.15 { // Skip very low confidence matches
			continue
		}

		relProps := map[string]any{
			"confidence":   match.Confidence,
			"reasons":      match.MatchReasons,
			"contextType":  "intelligent_linking",
			"callGraphDepth": match.CallGraphDepth,
		}

		_, err := idl.client.CreateRelationship(ctx, documentID, match.NodeID, string(models.MentionsRel), relProps)
		if err != nil {
			log.Printf("Warning: failed to create MENTIONS relationship: %v", err)
			continue
		}

		createdLinks++
	}

	return createdLinks, nil
}

// Helper functions

func (idl *IntelligentDocumentLinker) calculateSemanticConfidence(similarity float64) float64 {
	// Convert similarity score to confidence (normalize and apply threshold)
	return math.Max(0, (similarity-0.5)*2) // Convert 0.5-1.0 to 0-1.0
}

func (idl *IntelligentDocumentLinker) calculateHybridConfidence(score float64, query, functionName string) float64 {
	// Base confidence from search score
	confidence := math.Min(score/100.0, 1.0) // Normalize score

	// Boost if query appears in function name
	if len(query) > 2 && contains(functionName, query) {
		confidence *= 1.5
	}

	return math.Min(confidence, 1.0)
}

func (idl *IntelligentDocumentLinker) deduplicateMatches(matches []CodeMatch) []CodeMatch {
	seen := make(map[string]bool)
	var unique []CodeMatch

	for _, match := range matches {
		if !seen[match.NodeID] {
			seen[match.NodeID] = true
			unique = append(unique, match)
		}
	}

	return unique
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