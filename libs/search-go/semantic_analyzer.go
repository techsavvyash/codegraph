package search

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/context-maximiser/code-graph/libs/core-models-go"
)

// SemanticDocumentAnalyzer uses LLM to understand document content and extract technical concepts
type SemanticDocumentAnalyzer struct {
	embeddingService EmbeddingService
}

// NewSemanticDocumentAnalyzer creates a new semantic document analyzer
func NewSemanticDocumentAnalyzer(embeddingService EmbeddingService) *SemanticDocumentAnalyzer {
	return &SemanticDocumentAnalyzer{
		embeddingService: embeddingService,
	}
}

// TechnicalConcept represents a technical concept extracted from documentation
type TechnicalConcept struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Keywords    []string `json:"keywords"`
	Confidence  float64  `json:"confidence"`
	Context     string   `json:"context"`
}

// SemanticAnalysisResult contains the results of analyzing a document
type SemanticAnalysisResult struct {
	Document         *models.Document    `json:"document"`
	TechnicalConcepts []TechnicalConcept `json:"technicalConcepts"`
	FunctionalAreas  []string           `json:"functionalAreas"`
	SearchQueries    []string           `json:"searchQueries"`
	Embedding        []float64          `json:"embedding"`
}

// AnalyzeDocument performs semantic analysis on a document
func (sda *SemanticDocumentAnalyzer) AnalyzeDocument(ctx context.Context, doc *models.Document) (*SemanticAnalysisResult, error) {
	log.Printf("Starting semantic analysis for document: %s", doc.Title)

	// Generate embedding for the entire document
	embedding, err := sda.embeddingService.GenerateEmbedding(ctx, doc.Content)
	if err != nil {
		return nil, fmt.Errorf("failed to generate document embedding: %w", err)
	}

	// Extract technical concepts using heuristic analysis
	concepts := sda.extractTechnicalConcepts(doc.Content)

	// Identify functional areas
	functionalAreas := sda.identifyFunctionalAreas(doc.Content, concepts)

	// Generate search queries for finding related code
	searchQueries := sda.generateSearchQueries(concepts, functionalAreas)

	result := &SemanticAnalysisResult{
		Document:         doc,
		TechnicalConcepts: concepts,
		FunctionalAreas:  functionalAreas,
		SearchQueries:    searchQueries,
		Embedding:        embedding,
	}

	log.Printf("Semantic analysis complete: %d concepts, %d functional areas, %d search queries",
		len(concepts), len(functionalAreas), len(searchQueries))

	return result, nil
}

// extractTechnicalConcepts identifies technical concepts from document content
func (sda *SemanticDocumentAnalyzer) extractTechnicalConcepts(content string) []TechnicalConcept {
	var concepts []TechnicalConcept

	// Technical patterns to look for
	patterns := map[string][]string{
		"authentication": {"auth", "login", "token", "jwt", "oauth", "credential", "session", "verify"},
		"database":       {"db", "sql", "query", "schema", "migration", "transaction", "index", "table"},
		"api":           {"endpoint", "rest", "graphql", "http", "request", "response", "route", "handler"},
		"validation":    {"validate", "check", "verify", "sanitize", "format", "rules", "constraint"},
		"caching":       {"cache", "redis", "memcache", "ttl", "expire", "invalidate", "store"},
		"messaging":     {"queue", "pub", "sub", "message", "event", "notification", "broker"},
		"security":      {"encrypt", "decrypt", "hash", "secure", "permission", "access", "role"},
		"monitoring":    {"log", "metric", "trace", "monitor", "alert", "health", "status"},
		"storage":       {"file", "upload", "download", "s3", "blob", "storage", "bucket"},
		"processing":    {"process", "transform", "parse", "convert", "format", "pipeline"},
	}

	contentLower := strings.ToLower(content)

	for concept, keywords := range patterns {
		matches := 0
		foundKeywords := []string{}
		context := ""

		for _, keyword := range keywords {
			if strings.Contains(contentLower, keyword) {
				matches++
				foundKeywords = append(foundKeywords, keyword)

				// Extract context around the keyword
				if context == "" {
					context = sda.extractContextAroundKeyword(content, keyword)
				}
			}
		}

		if matches > 0 {
			confidence := float64(matches) / float64(len(keywords))
			concepts = append(concepts, TechnicalConcept{
				Name:        concept,
				Description: fmt.Sprintf("Technical concept related to %s", concept),
				Keywords:    foundKeywords,
				Confidence:  confidence,
				Context:     context,
			})
		}
	}

	// Sort by confidence
	for i := 0; i < len(concepts); i++ {
		for j := i + 1; j < len(concepts); j++ {
			if concepts[i].Confidence < concepts[j].Confidence {
				concepts[i], concepts[j] = concepts[j], concepts[i]
			}
		}
	}

	return concepts
}

// extractContextAroundKeyword extracts surrounding text around a keyword
func (sda *SemanticDocumentAnalyzer) extractContextAroundKeyword(content, keyword string) string {
	contentLower := strings.ToLower(content)
	index := strings.Index(contentLower, keyword)
	if index == -1 {
		return ""
	}

	// Extract 100 characters before and after
	start := index - 50
	if start < 0 {
		start = 0
	}
	end := index + len(keyword) + 50
	if end > len(content) {
		end = len(content)
	}

	context := content[start:end]
	// Clean up and return
	return strings.TrimSpace(strings.ReplaceAll(context, "\n", " "))
}

// identifyFunctionalAreas categorizes the document into functional areas
func (sda *SemanticDocumentAnalyzer) identifyFunctionalAreas(content string, concepts []TechnicalConcept) []string {
	areas := make(map[string]bool)

	// Map concepts to functional areas
	conceptToArea := map[string]string{
		"authentication": "security",
		"security":       "security",
		"database":       "data",
		"storage":        "data",
		"api":           "interface",
		"messaging":     "communication",
		"monitoring":    "observability",
		"validation":    "business_logic",
		"processing":    "business_logic",
		"caching":       "performance",
	}

	for _, concept := range concepts {
		if area, exists := conceptToArea[concept.Name]; exists {
			areas[area] = true
		}
	}

	// Convert to slice
	var result []string
	for area := range areas {
		result = append(result, area)
	}

	// Add additional areas based on content analysis
	contentLower := strings.ToLower(content)
	if strings.Contains(contentLower, "user") || strings.Contains(contentLower, "customer") {
		result = append(result, "user_management")
	}
	if strings.Contains(contentLower, "report") || strings.Contains(contentLower, "analytics") {
		result = append(result, "reporting")
	}
	if strings.Contains(contentLower, "config") || strings.Contains(contentLower, "setting") {
		result = append(result, "configuration")
	}

	return result
}

// generateSearchQueries creates search queries for finding related code
func (sda *SemanticDocumentAnalyzer) generateSearchQueries(concepts []TechnicalConcept, functionalAreas []string) []string {
	var queries []string

	// Generate queries from concepts
	for _, concept := range concepts {
		if concept.Confidence > 0.3 { // Only use high-confidence concepts
			queries = append(queries, concept.Name)

			// Add specific keywords as separate queries
			for _, keyword := range concept.Keywords {
				if len(keyword) > 3 { // Skip very short keywords
					queries = append(queries, keyword)
				}
			}
		}
	}

	// Generate queries from functional areas
	for _, area := range functionalAreas {
		queries = append(queries, area)
	}

	// Add common programming patterns
	commonPatterns := []string{"handler", "service", "manager", "client", "controller", "processor"}
	for _, pattern := range commonPatterns {
		queries = append(queries, pattern)
	}

	// Remove duplicates and return
	seen := make(map[string]bool)
	var uniqueQueries []string
	for _, query := range queries {
		if !seen[query] {
			seen[query] = true
			uniqueQueries = append(uniqueQueries, query)
		}
	}

	return uniqueQueries
}

// AnalyzeForCodeMapping specifically analyzes a document for mapping to code symbols
func (sda *SemanticDocumentAnalyzer) AnalyzeForCodeMapping(ctx context.Context, content string) ([]string, []TechnicalConcept, error) {
	// Create a temporary document for analysis
	doc := &models.Document{
		Title:   "Analysis Target",
		Content: content,
	}

	result, err := sda.AnalyzeDocument(ctx, doc)
	if err != nil {
		return nil, nil, err
	}

	return result.SearchQueries, result.TechnicalConcepts, nil
}

// GenerateSemanticEmbedding creates an embedding specifically for semantic search
func (sda *SemanticDocumentAnalyzer) GenerateSemanticEmbedding(ctx context.Context, content string) ([]float64, error) {
	// Preprocess content to focus on technical terms
	processedContent := sda.preprocessForEmbedding(content)

	return sda.embeddingService.GenerateEmbedding(ctx, processedContent)
}

// preprocessForEmbedding preprocesses text to focus on technical concepts
func (sda *SemanticDocumentAnalyzer) preprocessForEmbedding(content string) string {
	// Extract key technical sentences and combine them
	sentences := strings.Split(content, ".")
	var technicalSentences []string

	technicalKeywords := []string{
		"function", "method", "class", "interface", "service", "api", "endpoint",
		"database", "query", "auth", "validate", "process", "handle", "manage",
		"create", "update", "delete", "get", "post", "put", "request", "response",
	}

	for _, sentence := range sentences {
		sentenceLower := strings.ToLower(sentence)
		for _, keyword := range technicalKeywords {
			if strings.Contains(sentenceLower, keyword) {
				technicalSentences = append(technicalSentences, strings.TrimSpace(sentence))
				break
			}
		}
	}

	if len(technicalSentences) == 0 {
		// Fallback to original content if no technical sentences found
		return content
	}

	return strings.Join(technicalSentences, ". ")
}