package search

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/context-maximiser/code-graph/pkg/neo4j"
	neo4jdriver "github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// FullTextSearchManager handles BM25-based full-text search operations
type FullTextSearchManager struct {
	client *neo4j.Client
}

// NewFullTextSearchManager creates a new full-text search manager
func NewFullTextSearchManager(client *neo4j.Client) *FullTextSearchManager {
	return &FullTextSearchManager{
		client: client,
	}
}

// FullTextIndexConfig represents configuration for full-text indexes
type FullTextIndexConfig struct {
	Name          string   `json:"name"`
	NodeLabels    []string `json:"nodeLabels"`
	Properties    []string `json:"properties"`
	Analyzer      string   `json:"analyzer"`      // Optional: "standard", "english", etc.
	EventuallyConsistent bool `json:"eventuallyConsistent"` // Optional: for performance
}

// FullTextSearchResult represents a full-text search result with BM25 score
type FullTextSearchResult struct {
	Node   map[string]interface{} `json:"node"`
	Score  float64                `json:"score"`
	Labels []string               `json:"labels"`
}

// FullTextSearchResponse contains full-text search results and metadata
type FullTextSearchResponse struct {
	Results   []FullTextSearchResult `json:"results"`
	Query     string                 `json:"query"`
	IndexUsed string                 `json:"indexUsed"`
	Count     int                    `json:"count"`
}

// CreateFullTextIndexes creates all necessary full-text indexes for CodeGraph
func (ftsm *FullTextSearchManager) CreateFullTextIndexes(ctx context.Context) error {
	indexes := []FullTextIndexConfig{
		{
			Name:       "code_fulltext",
			NodeLabels: []string{"Function", "Method", "Class", "Interface", "Variable"},
			Properties: []string{"name", "signature", "description"},
			Analyzer:   "standard",
		},
		{
			Name:       "document_fulltext",
			NodeLabels: []string{"Document", "Feature"},
			Properties: []string{"title", "name", "description", "content"},
			Analyzer:   "english",
		},
		{
			Name:       "symbol_fulltext",
			NodeLabels: []string{"Symbol"},
			Properties: []string{"symbol", "displayName", "description"},
			Analyzer:   "standard",
		},
		{
			Name:       "file_fulltext",
			NodeLabels: []string{"File"},
			Properties: []string{"path", "name"},
			Analyzer:   "standard",
		},
	}

	for _, index := range indexes {
		if err := ftsm.createFullTextIndex(ctx, index); err != nil {
			log.Printf("Warning: failed to create full-text index %s: %v", index.Name, err)
			// Continue with other indexes even if one fails
		} else {
			log.Printf("✓ Created full-text index: %s", index.Name)
		}
	}

	return nil
}

// createFullTextIndex creates a single full-text index
func (ftsm *FullTextSearchManager) createFullTextIndex(ctx context.Context, config FullTextIndexConfig) error {
	// Build node labels part: (n:Label1|Label2|Label3)
	nodeLabels := strings.Join(config.NodeLabels, "|")

	// Build properties part: [n.prop1, n.prop2, n.prop3]
	var properties []string
	for _, prop := range config.Properties {
		properties = append(properties, fmt.Sprintf("n.%s", prop))
	}
	propertiesPart := fmt.Sprintf("[%s]", strings.Join(properties, ", "))

	// Build the query
	var query string
	if config.Analyzer != "" {
		query = fmt.Sprintf(`
			CREATE FULLTEXT INDEX %s IF NOT EXISTS
			FOR (n:%s)
			ON EACH %s
			OPTIONS {
				indexConfig: {
					` + "`fulltext.analyzer`" + `: '%s',
					` + "`fulltext.eventually_consistent`" + `: %t
				}
			}
		`, config.Name, nodeLabels, propertiesPart, config.Analyzer, config.EventuallyConsistent)
	} else {
		query = fmt.Sprintf(`
			CREATE FULLTEXT INDEX %s IF NOT EXISTS
			FOR (n:%s)
			ON EACH %s
		`, config.Name, nodeLabels, propertiesPart)
	}

	_, err := ftsm.client.ExecuteQuery(ctx, query, nil)
	if err != nil {
		return fmt.Errorf("failed to create full-text index %s: %w", config.Name, err)
	}

	return nil
}

// FullTextSearch performs BM25-based full-text search
func (ftsm *FullTextSearchManager) FullTextSearch(ctx context.Context, indexName, queryText string, limit int) (*FullTextSearchResponse, error) {
	if limit <= 0 {
		limit = 10
	}

	query := `
		CALL db.index.fulltext.queryNodes($indexName, $queryText)
		YIELD node, score
		RETURN node, score, labels(node) as nodeLabels
		ORDER BY score DESC
		LIMIT $limit
	`

	params := map[string]any{
		"indexName": indexName,
		"queryText": queryText,
		"limit":     limit,
	}

	results, err := ftsm.client.ExecuteQuery(ctx, query, params)
	if err != nil {
		return nil, fmt.Errorf("failed to perform full-text search: %w", err)
	}

	var searchResults []FullTextSearchResult
	for _, record := range results {
		recordMap := record.AsMap()

		node, ok := recordMap["node"]
		if !ok {
			continue
		}

		score, ok := recordMap["score"].(float64)
		if !ok {
			score = 0.0
		}

		labels := []string{}
		if labelsList, ok := recordMap["nodeLabels"].([]interface{}); ok {
			for _, label := range labelsList {
				if labelStr, ok := label.(string); ok {
					labels = append(labels, labelStr)
				}
			}
		}

		// Convert node to map for JSON serialization
		nodeMap := make(map[string]interface{})
		if nodeObj, ok := node.(neo4jdriver.Node); ok {
			// Extract all properties from the Neo4j Node object
			nodeMap = nodeObj.Props
		} else if nodeData, ok := node.(map[string]interface{}); ok {
			nodeMap = nodeData
		}

		searchResults = append(searchResults, FullTextSearchResult{
			Node:   nodeMap,
			Score:  score,
			Labels: labels,
		})
	}

	return &FullTextSearchResponse{
		Results:   searchResults,
		Query:     queryText,
		IndexUsed: indexName,
		Count:     len(searchResults),
	}, nil
}

// SmartFullTextSearch automatically selects the best index based on query content
func (ftsm *FullTextSearchManager) SmartFullTextSearch(ctx context.Context, queryText string, limit int) (*FullTextSearchResponse, error) {
	// Analyze query to determine best index
	indexName := ftsm.selectBestIndex(queryText)

	return ftsm.FullTextSearch(ctx, indexName, queryText, limit)
}

// selectBestIndex determines the most appropriate index for a query
func (ftsm *FullTextSearchManager) selectBestIndex(queryText string) string {
	query := strings.ToLower(queryText)

	// Code-related keywords
	codeKeywords := []string{
		"function", "method", "class", "interface", "variable",
		"return", "parameter", "argument", "struct", "type",
		"implements", "extends", "override", "private", "public",
		"static", "const", "var", "func", "package",
	}

	// Document-related keywords
	docKeywords := []string{
		"documentation", "guide", "tutorial", "readme", "feature",
		"requirement", "specification", "design", "architecture",
		"overview", "getting started", "how to", "example",
	}

	// Symbol-related keywords
	symbolKeywords := []string{
		"symbol", "reference", "definition", "declaration",
		"import", "export", "namespace", "scope",
	}

	// Check for code-related terms
	for _, keyword := range codeKeywords {
		if strings.Contains(query, keyword) {
			return "code_fulltext"
		}
	}

	// Check for document-related terms
	for _, keyword := range docKeywords {
		if strings.Contains(query, keyword) {
			return "document_fulltext"
		}
	}

	// Check for symbol-related terms
	for _, keyword := range symbolKeywords {
		if strings.Contains(query, keyword) {
			return "symbol_fulltext"
		}
	}

	// Default to document search for general queries
	return "document_fulltext"
}

// HybridFullTextSearch searches across multiple full-text indexes
func (ftsm *FullTextSearchManager) HybridFullTextSearch(ctx context.Context, queryText string, limit int) (*FullTextSearchResponse, error) {
	indexes := []string{
		"code_fulltext",
		"document_fulltext",
		"symbol_fulltext",
		"file_fulltext",
	}

	var allResults []FullTextSearchResult
	limitPerIndex := limit / len(indexes)
	if limitPerIndex < 1 {
		limitPerIndex = 1
	}

	// Search across all full-text indexes
	for _, indexName := range indexes {
		response, err := ftsm.FullTextSearch(ctx, indexName, queryText, limitPerIndex)
		if err != nil {
			log.Printf("Warning: full-text search failed for index %s: %v", indexName, err)
			continue
		}
		allResults = append(allResults, response.Results...)
	}

	// Sort by BM25 score (descending)
	for i := 0; i < len(allResults)-1; i++ {
		for j := i + 1; j < len(allResults); j++ {
			if allResults[i].Score < allResults[j].Score {
				allResults[i], allResults[j] = allResults[j], allResults[i]
			}
		}
	}

	// Limit results
	if len(allResults) > limit {
		allResults = allResults[:limit]
	}

	return &FullTextSearchResponse{
		Results:   allResults,
		Query:     queryText,
		IndexUsed: "hybrid",
		Count:     len(allResults),
	}, nil
}

// AdvancedFullTextSearch supports Lucene query syntax
func (ftsm *FullTextSearchManager) AdvancedFullTextSearch(ctx context.Context, indexName, luceneQuery string, limit int) (*FullTextSearchResponse, error) {
	if limit <= 0 {
		limit = 10
	}

	// Lucene query examples:
	// - "function AND indexing" (boolean AND)
	// - "function OR method" (boolean OR)
	// - "function NOT test" (boolean NOT)
	// - "\"exact phrase\"" (exact phrase)
	// - "name:createIndex" (field-specific search)
	// - "func*" (wildcard)

	query := `
		CALL db.index.fulltext.queryNodes($indexName, $luceneQuery)
		YIELD node, score
		RETURN node, score, labels(node) as nodeLabels
		ORDER BY score DESC
		LIMIT $limit
	`

	params := map[string]any{
		"indexName":   indexName,
		"luceneQuery": luceneQuery,
		"limit":       limit,
	}

	results, err := ftsm.client.ExecuteQuery(ctx, query, params)
	if err != nil {
		return nil, fmt.Errorf("failed to perform advanced full-text search: %w", err)
	}

	var searchResults []FullTextSearchResult
	for _, record := range results {
		recordMap := record.AsMap()

		node, ok := recordMap["node"]
		if !ok {
			continue
		}

		score, ok := recordMap["score"].(float64)
		if !ok {
			score = 0.0
		}

		labels := []string{}
		if labelsList, ok := recordMap["nodeLabels"].([]interface{}); ok {
			for _, label := range labelsList {
				if labelStr, ok := label.(string); ok {
					labels = append(labels, labelStr)
				}
			}
		}

		nodeMap := make(map[string]interface{})
		if nodeData, ok := node.(map[string]interface{}); ok {
			nodeMap = nodeData
		}

		searchResults = append(searchResults, FullTextSearchResult{
			Node:   nodeMap,
			Score:  score,
			Labels: labels,
		})
	}

	return &FullTextSearchResponse{
		Results:   searchResults,
		Query:     luceneQuery,
		IndexUsed: indexName,
		Count:     len(searchResults),
	}, nil
}

// GetFullTextIndexInfo returns information about existing full-text indexes
func (ftsm *FullTextSearchManager) GetFullTextIndexInfo(ctx context.Context) (map[string]interface{}, error) {
	query := `SHOW INDEXES WHERE type = 'FULLTEXT'`

	results, err := ftsm.client.ExecuteQuery(ctx, query, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get full-text index info: %w", err)
	}

	info := make(map[string]interface{})
	info["fullTextIndexes"] = make([]map[string]interface{}, 0)

	for _, record := range results {
		recordMap := record.AsMap()
		indexInfo := make(map[string]interface{})

		for key, value := range recordMap {
			indexInfo[key] = value
		}

		if indexes, ok := info["fullTextIndexes"].([]map[string]interface{}); ok {
			info["fullTextIndexes"] = append(indexes, indexInfo)
		}
	}

	return info, nil
}

// DropFullTextIndex removes a full-text index
func (ftsm *FullTextSearchManager) DropFullTextIndex(ctx context.Context, indexName string) error {
	query := fmt.Sprintf("DROP INDEX %s IF EXISTS", indexName)

	_, err := ftsm.client.ExecuteQuery(ctx, query, nil)
	if err != nil {
		return fmt.Errorf("failed to drop full-text index %s: %w", indexName, err)
	}

	log.Printf("✓ Dropped full-text index: %s", indexName)
	return nil
}