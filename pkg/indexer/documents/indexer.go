package documents

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/context-maximiser/code-graph/pkg/models"
	"github.com/context-maximiser/code-graph/pkg/neo4j"
	"github.com/neo4j/neo4j-go-driver/v5/neo4j/dbtype"
)

// DocumentIndexer handles indexing documents into Neo4j
type DocumentIndexer struct {
	client *neo4j.Client
	parser *DocumentParser
}

// NewDocumentIndexer creates a new document indexer
func NewDocumentIndexer(client *neo4j.Client) *DocumentIndexer {
	return &DocumentIndexer{
		client: client,
		parser: NewDocumentParser(),
	}
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
		_, err = di.client.CreateRelationship(ctx, docID, featureID, "DESCRIBES", nil)
		if err != nil {
			fmt.Printf("Warning: failed to create DESCRIBES relationship: %v\n", err)
		}
	}

	// Create relationships to code symbols if they exist
	if err := di.linkToCodeSymbols(ctx, docID, doc.Content); err != nil {
		fmt.Printf("Warning: failed to link to code symbols: %v\n", err)
	}

	fmt.Printf("Successfully indexed document: %s\n", doc.Title)
	return nil
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
	docProps := map[string]any{
		"title":     doc.Title,
		"type":      doc.Type,
		"sourceUrl": doc.SourceURL,
		"content":   doc.Content,
	}

	// Use sourceUrl as the unique identifier for merging
	return di.client.MergeNode(ctx, []string{"Document"}, 
		map[string]any{"sourceUrl": doc.SourceURL}, docProps)
}

// createFeatureNode creates a Feature node in Neo4j
func (di *DocumentIndexer) createFeatureNode(ctx context.Context, feature *models.Feature) (string, error) {
	featureProps := map[string]any{
		"name":        feature.Name,
		"description": feature.Description,
		"status":      feature.Status,
		"priority":    feature.Priority,
		"tags":        feature.Tags,
	}

	// Use name as the unique identifier for merging (features with same name are considered the same)
	return di.client.MergeNode(ctx, []string{"Feature"}, 
		map[string]any{"name": feature.Name}, featureProps)
}

// linkToCodeSymbols creates MENTIONS relationships between documents and code symbols
func (di *DocumentIndexer) linkToCodeSymbols(ctx context.Context, docID string, content string) error {
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
						map[string]any{"context": symbolRef})
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