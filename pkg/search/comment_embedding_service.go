package search

import (
	"context"
	"fmt"
	"strings"

	"github.com/context-maximiser/code-graph/pkg/neo4j"
)

// CommentEmbeddingService handles embedding generation specifically for docstrings and comments
type CommentEmbeddingService struct {
	client           *neo4j.Client
	embeddingService EmbeddingService
	vectorSearch     *VectorSearchManager
}

// NewCommentEmbeddingService creates a new comment-focused embedding service
func NewCommentEmbeddingService(client *neo4j.Client, embeddingService EmbeddingService) *CommentEmbeddingService {
	return &CommentEmbeddingService{
		client:           client,
		embeddingService: embeddingService,
		vectorSearch:     NewVectorSearchManager(client),
	}
}

// CommentEmbeddingUpdate represents a comment embedding update
type CommentEmbeddingUpdate struct {
	CommentId     string
	Text          string
	ParentNodeId  string
	ParentType    string  // Function, Method, Class, etc.
	ParentName    string
	Embedding     []float64
}

// ExtractAndEmbedDocstrings extracts docstrings from functions/classes and creates comment embeddings
func (ces *CommentEmbeddingService) ExtractAndEmbedDocstrings(ctx context.Context, batchSize int, dryRun bool) error {
	// Extract docstrings from Functions
	if err := ces.extractDocstringsForNodeType(ctx, "Function", batchSize, dryRun); err != nil {
		return fmt.Errorf("failed to extract function docstrings: %w", err)
	}

	// Extract docstrings from Methods
	if err := ces.extractDocstringsForNodeType(ctx, "Method", batchSize, dryRun); err != nil {
		return fmt.Errorf("failed to extract method docstrings: %w", err)
	}

	// Extract docstrings from Classes
	if err := ces.extractDocstringsForNodeType(ctx, "Class", batchSize, dryRun); err != nil {
		return fmt.Errorf("failed to extract class docstrings: %w", err)
	}

	return nil
}

// extractDocstringsForNodeType extracts and embeds docstrings for a specific node type
func (ces *CommentEmbeddingService) extractDocstringsForNodeType(ctx context.Context, nodeType string, batchSize int, dryRun bool) error {
	fmt.Printf("📝 Processing %s docstrings...\n", nodeType)

	// Query nodes with docstrings that don't have comment embeddings yet
	query := fmt.Sprintf(`
		MATCH (n:%s)
		WHERE n.docstring IS NOT NULL AND n.docstring <> ""
		AND NOT EXISTS {
			MATCH (n)-[:HAS_COMMENT]->(c:Comment)
			WHERE c.isDocstring = true AND c.embedding IS NOT NULL
		}
		RETURN elementId(n) as nodeId, n.name as name, n.docstring as docstring, n.signature as signature
		LIMIT 1000
	`, nodeType)

	results, err := ces.client.ExecuteQuery(ctx, query, nil)
	if err != nil {
		return fmt.Errorf("failed to query %s nodes: %w", nodeType, err)
	}

	if len(results) == 0 {
		fmt.Printf("   No %s nodes need docstring embeddings\n", nodeType)
		return nil
	}

	fmt.Printf("   Found %d %s nodes with unprocessed docstrings\n", len(results), nodeType)

	// Process in batches
	var commentUpdates []CommentEmbeddingUpdate
	for i, record := range results {
		recordMap := record.AsMap()
		nodeId, _ := recordMap["nodeId"].(string)
		name, _ := recordMap["name"].(string)
		docstring, _ := recordMap["docstring"].(string)
		signature, _ := recordMap["signature"].(string)

		// Create comment text by combining docstring with context
		var textParts []string
		if signature != "" {
			textParts = append(textParts, fmt.Sprintf("Function: %s", signature))
		} else if name != "" {
			textParts = append(textParts, fmt.Sprintf("%s: %s", nodeType, name))
		}
		textParts = append(textParts, docstring)

		commentText := strings.Join(textParts, "\n")

		commentUpdates = append(commentUpdates, CommentEmbeddingUpdate{
			CommentId:    fmt.Sprintf("%s_docstring_%d", nodeId, i),
			Text:         commentText,
			ParentNodeId: nodeId,
			ParentType:   nodeType,
			ParentName:   name,
		})

		// Process batch when full
		if len(commentUpdates) >= batchSize {
			if err := ces.processBatch(ctx, commentUpdates, dryRun); err != nil {
				return err
			}
			commentUpdates = nil
		}
	}

	// Process remaining items
	if len(commentUpdates) > 0 {
		if err := ces.processBatch(ctx, commentUpdates, dryRun); err != nil {
			return err
		}
	}

	return nil
}

// processBatch processes a batch of comment embeddings
func (ces *CommentEmbeddingService) processBatch(ctx context.Context, updates []CommentEmbeddingUpdate, dryRun bool) error {
	if len(updates) == 0 {
		return nil
	}

	// Collect texts for embedding generation
	var texts []string
	for _, update := range updates {
		texts = append(texts, update.Text)
	}

	fmt.Printf("   Generating embeddings for %d comments...\n", len(texts))

	if dryRun {
		fmt.Printf("   [DRY RUN] Would generate embeddings for %d comments\n", len(texts))
		return nil
	}

	// Generate embeddings
	embeddings, err := ces.embeddingService.GenerateBatchEmbeddings(ctx, texts)
	if err != nil {
		return fmt.Errorf("failed to generate embeddings: %w", err)
	}

	// Update embeddings in the updates
	for i, embedding := range embeddings {
		updates[i].Embedding = embedding
	}

	// Create comment nodes and relationships in Neo4j
	return ces.createCommentNodes(ctx, updates)
}

// createCommentNodes creates comment nodes and links them to parent nodes
func (ces *CommentEmbeddingService) createCommentNodes(ctx context.Context, updates []CommentEmbeddingUpdate) error {
	fmt.Printf("   Creating %d comment nodes in Neo4j...\n", len(updates))

	for _, update := range updates {
		// Create comment node with embedding
		createCommentQuery := `
			MATCH (parent) WHERE elementId(parent) = $parentId
			CREATE (c:Comment {
				text: $text,
				type: 'docstring',
				isDocstring: true,
				embedding: $embedding,
				parentType: $parentType,
				parentName: $parentName,
				createdAt: datetime(),
				updatedAt: datetime()
			})
			CREATE (parent)-[:HAS_COMMENT]->(c)
			RETURN elementId(c) as commentId
		`

		params := map[string]any{
			"parentId":    update.ParentNodeId,
			"text":        update.Text,
			"embedding":   update.Embedding,
			"parentType":  update.ParentType,
			"parentName":  update.ParentName,
		}

		results, err := ces.client.ExecuteQuery(ctx, createCommentQuery, params)
		if err != nil {
			return fmt.Errorf("failed to create comment node: %w", err)
		}

		if len(results) > 0 {
			recordMap := results[0].AsMap()
			commentId, _ := recordMap["commentId"].(string)
			fmt.Printf("   Created comment node %s for %s %s\n", commentId, update.ParentType, update.ParentName)
		}
	}

	return nil
}

// SearchFunctionsByComment searches for functions/methods/classes through their comment embeddings
func (ces *CommentEmbeddingService) SearchFunctionsByComment(ctx context.Context, query string, limit int) (*CommentSearchResponse, error) {
	// Generate embedding for the query
	embedding, err := ces.embeddingService.GenerateEmbedding(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to generate query embedding: %w", err)
	}

	// Search comment embeddings
	searchQuery := `
		CALL db.index.vector.queryNodes('comment_embeddings_768', $limit, $embedding)
		YIELD node as comment, score
		WHERE comment:Comment AND comment.isDocstring = true
		MATCH (parent)-[:HAS_COMMENT]->(comment)
		RETURN parent, comment, score
		ORDER BY score DESC
	`

	params := map[string]any{
		"embedding": embedding,
		"limit":     limit,
	}

	results, err := ces.client.ExecuteQuery(ctx, searchQuery, params)
	if err != nil {
		return nil, fmt.Errorf("comment search failed: %w", err)
	}

	var searchResults []CommentSearchResult
	for _, result := range results {
		resultMap := result.AsMap()

		// Extract parent node data
		parentNode := make(map[string]interface{})
		if parent, ok := resultMap["parent"]; ok {
			if parentMap, ok := parent.(map[string]interface{}); ok {
				parentNode = parentMap
			}
		}

		// Extract comment data
		commentNode := make(map[string]interface{})
		if comment, ok := resultMap["comment"]; ok {
			if commentMap, ok := comment.(map[string]interface{}); ok {
				commentNode = commentMap
			}
		}

		score := 0.0
		if s, ok := resultMap["score"].(float64); ok {
			score = s
		}

		searchResults = append(searchResults, CommentSearchResult{
			ParentNode:  parentNode,
			CommentNode: commentNode,
			Score:       score,
		})
	}

	return &CommentSearchResponse{
		Query:        query,
		Results:      searchResults,
		TotalResults: len(searchResults),
		QueryVector:  embedding,
	}, nil
}

// CommentSearchResponse represents search results based on comment embeddings
type CommentSearchResponse struct {
	Query        string               `json:"query"`
	Results      []CommentSearchResult `json:"results"`
	TotalResults int                  `json:"totalResults"`
	QueryVector  []float64            `json:"queryVector,omitempty"`
}

// CommentSearchResult represents a single search result
type CommentSearchResult struct {
	ParentNode  map[string]interface{} `json:"parentNode"`  // The function/class/method
	CommentNode map[string]interface{} `json:"commentNode"` // The comment/docstring
	Score       float64                `json:"score"`       // Similarity score
}

// CreateCommentEmbeddingIndex creates the vector index for comment embeddings
func (ces *CommentEmbeddingService) CreateCommentEmbeddingIndex(ctx context.Context, dimensions int) error {
	indexName := fmt.Sprintf("comment_embeddings_%d", dimensions)

	// Drop existing index if it exists
	dropQuery := fmt.Sprintf("DROP INDEX %s IF EXISTS", indexName)
	_, err := ces.client.ExecuteQuery(ctx, dropQuery, nil)
	if err != nil {
		fmt.Printf("Warning: Could not drop existing index: %v\n", err)
	}

	// Create new index
	createQuery := fmt.Sprintf(`
		CREATE VECTOR INDEX %s IF NOT EXISTS
		FOR (c:Comment)
		ON c.embedding
		OPTIONS {
			indexConfig: {
				`+"`vector.dimensions`"+`: %d,
				`+"`vector.similarity_function`"+`: 'cosine'
			}
		}
	`, indexName, dimensions)

	_, err = ces.client.ExecuteQuery(ctx, createQuery, nil)
	if err != nil {
		return fmt.Errorf("failed to create comment embedding index: %w", err)
	}

	fmt.Printf("✅ Created comment embedding index: %s\n", indexName)
	return nil
}