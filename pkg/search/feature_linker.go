package search

import (
	"context"
	"fmt"
	"log"
	"sort"

	"github.com/context-maximiser/code-graph/pkg/models"
	"github.com/context-maximiser/code-graph/pkg/neo4j"
)

// FeatureLinker implements the RFC-002 specification for semantic linking of features to code
type FeatureLinker struct {
	client              *neo4j.Client
	embeddingService    EmbeddingService
	llmService          LLMService
	codeSummarizer      *CodeSubgraphSummarizer
	llmValidator        *LLMValidator
	vectorSearch        *VectorSearchManager
	minConfidence       float64
	maxCandidates       int
}

// NewFeatureLinker creates a new feature linker according to RFC-002
func NewFeatureLinker(client *neo4j.Client, embeddingService EmbeddingService) *FeatureLinker {
	return &FeatureLinker{
		client:              client,
		embeddingService:    embeddingService,
		llmService:          nil, // Optional LLM service
		codeSummarizer:      NewCodeSubgraphSummarizer(client, embeddingService),
		llmValidator:        NewLLMValidator(embeddingService),
		vectorSearch:        NewVectorSearchManager(client),
		minConfidence:       0.6,  // Minimum confidence to create IMPLEMENTS relationship
		maxCandidates:       10,   // Maximum candidates to validate per feature
	}
}

// WithLLMService sets the LLM service for text generation and validation
func (fl *FeatureLinker) WithLLMService(llmService LLMService) *FeatureLinker {
	fl.llmService = llmService
	fl.codeSummarizer.WithLLMService(llmService)
	fl.llmValidator.WithLLMService(llmService)
	return fl
}

// FeatureLinkingResult contains the results of feature linking process
type FeatureLinkingResult struct {
	FeatureID           string                  `json:"featureId"`
	FeatureName         string                  `json:"featureName"`
	FeatureDescription  string                  `json:"featureDescription"`
	CandidatesFound     int                     `json:"candidatesFound"`
	CandidatesValidated int                     `json:"candidatesValidated"`
	ImplementsLinks     []*ImplementsLink       `json:"implementsLinks"`
	ProcessingTime      string                  `json:"processingTime"`
}

// ImplementsLink represents a validated IMPLEMENTS relationship
type ImplementsLink struct {
	FunctionID       string  `json:"functionId"`
	FunctionName     string  `json:"functionName"`
	Confidence       float64 `json:"confidence"`
	ValidationMethod string  `json:"validationMethod"`
	CodeSummary      string  `json:"codeSummary"`
	SubgraphSize     int     `json:"subgraphSize"`
	RelationshipID   string  `json:"relationshipId"`
}

// LinkAllFeatures processes all features in the database and creates IMPLEMENTS relationships
func (fl *FeatureLinker) LinkAllFeatures(ctx context.Context) ([]*FeatureLinkingResult, error) {
	log.Println("Starting feature linking process for all features...")

	// Step 1: Get all Feature nodes with embeddings
	features, err := fl.getAllFeatures(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get features: %w", err)
	}

	log.Printf("Found %d features to process", len(features))

	var results []*FeatureLinkingResult

	// Step 2: Process each feature
	for _, feature := range features {
		result, err := fl.LinkFeatureToCode(ctx, feature.ID, feature.Name, feature.Description)
		if err != nil {
			log.Printf("Warning: failed to link feature %s: %v", feature.Name, err)
			continue
		}

		results = append(results, result)
		log.Printf("Processed feature %s: %d links created", feature.Name, len(result.ImplementsLinks))
	}

	return results, nil
}

// LinkFeatureToCode implements the full RFC-002 process for a single feature
func (fl *FeatureLinker) LinkFeatureToCode(ctx context.Context, featureID, featureName, featureDescription string) (*FeatureLinkingResult, error) {
	log.Printf("Linking feature to code: %s", featureName)

	result := &FeatureLinkingResult{
		FeatureID:          featureID,
		FeatureName:        featureName,
		FeatureDescription: featureDescription,
	}

	// Step 1: Generate or get feature embedding
	featureEmbedding, err := fl.embeddingService.GenerateEmbedding(ctx, featureDescription)
	if err != nil {
		return nil, fmt.Errorf("failed to generate feature embedding: %w", err)
	}

	// Step 2: Find candidate code entry points using vector search
	candidates, err := fl.findCandidateEntryPoints(ctx, featureEmbedding)
	if err != nil {
		return nil, fmt.Errorf("failed to find candidates: %w", err)
	}

	result.CandidatesFound = len(candidates)
	log.Printf("Found %d candidate entry points for feature %s", len(candidates), featureName)

	// Step 3: For each candidate, extract subgraph and generate summary
	var matches []*FeatureCodeMatch
	for _, candidate := range candidates {
		match, err := fl.analyzeCandidate(ctx, featureID, featureName, featureDescription, candidate)
		if err != nil {
			log.Printf("Warning: failed to analyze candidate %s: %v", candidate.FunctionID, err)
			continue
		}

		matches = append(matches, match)
	}

	result.CandidatesValidated = len(matches)

	// Step 4: LLM validation of matches
	err = fl.llmValidator.ValidateBatch(ctx, matches)
	if err != nil {
		return nil, fmt.Errorf("LLM validation failed: %w", err)
	}

	// Step 5: Create IMPLEMENTS relationships for validated matches
	for _, match := range matches {
		if match.ValidationResult != nil && match.ValidationResult.IsMatch && match.ValidationResult.Confidence >= fl.minConfidence {
			link, err := fl.createImplementsRelationship(ctx, match)
			if err != nil {
				log.Printf("Warning: failed to create IMPLEMENTS relationship: %v", err)
				continue
			}

			result.ImplementsLinks = append(result.ImplementsLinks, link)
		}
	}

	log.Printf("Created %d IMPLEMENTS relationships for feature %s", len(result.ImplementsLinks), featureName)

	return result, nil
}

// getAllFeatures retrieves all Feature nodes from the database
func (fl *FeatureLinker) getAllFeatures(ctx context.Context) ([]FeatureNode, error) {
	cypher := `
		MATCH (f:Feature)
		RETURN f.id AS id, f.name AS name, f.description AS description,
			   f.status AS status, f.priority AS priority
		ORDER BY f.priority DESC, f.name
	`

	results, err := fl.client.ExecuteQuery(ctx, cypher, nil)
	if err != nil {
		return nil, err
	}

	var features []FeatureNode
	for _, record := range results {
		recordMap := record.AsMap()
		features = append(features, FeatureNode{
			ID:          getStringValue(recordMap, "id"),
			Name:        getStringValue(recordMap, "name"),
			Description: getStringValue(recordMap, "description"),
			Status:      getStringValue(recordMap, "status"),
			Priority:    getStringValue(recordMap, "priority"),
		})
	}

	return features, nil
}

// FeatureNode represents a feature in the database
type FeatureNode struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Status      string `json:"status"`
	Priority    string `json:"priority"`
}

// CandidateEntryPoint represents a potential code entry point for implementing a feature
type CandidateEntryPoint struct {
	FunctionID  string  `json:"functionId"`
	FunctionName string  `json:"functionName"`
	Signature   string  `json:"signature"`
	FilePath    string  `json:"filePath"`
	Confidence  float64 `json:"confidence"`
	Source      string  `json:"source"` // "vector", "keyword", "hybrid"
}

// findCandidateEntryPoints finds potential entry points using vector search and keyword matching
func (fl *FeatureLinker) findCandidateEntryPoints(ctx context.Context, featureEmbedding []float64) ([]*CandidateEntryPoint, error) {
	var candidates []*CandidateEntryPoint

	// Strategy 1: Vector search using function embeddings
	vectorResults, err := fl.vectorSearch.HybridVectorSearch(ctx, featureEmbedding, fl.maxCandidates)
	if err == nil {
		for _, result := range vectorResults.Results {
			// Only consider functions as entry points
			nodeType := getStringValue(result.Node, "nodeType")
			if nodeType == "Function" || nodeType == "Method" {
				candidates = append(candidates, &CandidateEntryPoint{
					FunctionID:   getStringValue(result.Node, "id"),
					FunctionName: getStringValue(result.Node, "name"),
					Signature:    getStringValue(result.Node, "signature"),
					FilePath:     getStringValue(result.Node, "filePath"),
					Confidence:   result.Score,
					Source:       "vector",
				})
			}
		}
	}

	// Strategy 2: Keyword-based search for functions with meaningful names
	// This bootstraps the search as mentioned in the RFC
	keywordCandidates, err := fl.findKeywordCandidates(ctx)
	if err == nil {
		for _, candidate := range keywordCandidates {
			// Check if not already found by vector search
			found := false
			for _, existing := range candidates {
				if existing.FunctionID == candidate.FunctionID {
					found = true
					break
				}
			}

			if !found {
				candidates = append(candidates, candidate)
			}
		}
	}

	// Sort by confidence and limit results
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Confidence > candidates[j].Confidence
	})

	if len(candidates) > fl.maxCandidates {
		candidates = candidates[:fl.maxCandidates]
	}

	return candidates, nil
}

// findKeywordCandidates finds functions with meaningful names that could be entry points
func (fl *FeatureLinker) findKeywordCandidates(ctx context.Context) ([]*CandidateEntryPoint, error) {
	// Look for functions with names that suggest they are entry points
	cypher := `
		MATCH (f:Function)
		WHERE f.name =~ '(?i).*(handler|controller|service|process|execute|run|handle|create|generate|calculate|compute|validate|authenticate|authorize).*'
		AND f.signature IS NOT NULL
		RETURN f.id AS id, f.name AS name, f.signature AS signature, f.filePath AS filePath
		LIMIT 20
	`

	results, err := fl.client.ExecuteQuery(ctx, cypher, nil)
	if err != nil {
		return nil, err
	}

	var candidates []*CandidateEntryPoint
	for _, record := range results {
		recordMap := record.AsMap()
		candidates = append(candidates, &CandidateEntryPoint{
			FunctionID:   getStringValue(recordMap, "id"),
			FunctionName: getStringValue(recordMap, "name"),
			Signature:    getStringValue(recordMap, "signature"),
			FilePath:     getStringValue(recordMap, "filePath"),
			Confidence:   0.5, // Base confidence for keyword matches
			Source:       "keyword",
		})
	}

	return candidates, nil
}

// analyzeCandidate performs subgraph extraction and summarization for a candidate
func (fl *FeatureLinker) analyzeCandidate(ctx context.Context, featureID, featureName, featureDescription string, candidate *CandidateEntryPoint) (*FeatureCodeMatch, error) {
	// Extract and summarize the code subgraph
	subgraph, err := fl.codeSummarizer.ExtractAndSummarizeSubgraph(ctx, candidate.FunctionID)
	if err != nil {
		return nil, fmt.Errorf("failed to extract subgraph: %w", err)
	}

	// Persist embedding to the entry point Function node (RFC-002 requirement)
	if subgraph.Embedding != nil && len(subgraph.Embedding) > 0 {
		err = fl.client.SetNodeProperty(ctx, subgraph.EntryPoint.ID, "embedding", subgraph.Embedding)
		if err != nil {
			log.Printf("Warning: failed to persist embedding to function node %s: %v", subgraph.EntryPoint.ID, err)
			// Don't fail the entire operation if embedding persistence fails
		} else {
			log.Printf("Persisted embedding (%d dims) to function node %s", len(subgraph.Embedding), subgraph.EntryPoint.Name)
		}
	}

	return &FeatureCodeMatch{
		FeatureID:          featureID,
		FeatureName:        featureName,
		FeatureDescription: featureDescription,
		CodeSubgraph:       subgraph,
		InitialConfidence:  candidate.Confidence,
	}, nil
}

// createImplementsRelationship creates an IMPLEMENTS relationship in the database
func (fl *FeatureLinker) createImplementsRelationship(ctx context.Context, match *FeatureCodeMatch) (*ImplementsLink, error) {
	// Prepare relationship properties according to enhanced ImplementsRelationship model
	relProps := map[string]any{
		"confidence":       match.ValidationResult.Confidence,
		"validationMethod": match.ValidationResult.Explanation,
		"codeSummary":      match.CodeSubgraph.Summary,
		"subgraphSize":     len(match.CodeSubgraph.Functions),
	}

	// Create the IMPLEMENTS relationship from function to feature
	relationshipID, err := fl.client.CreateRelationship(
		ctx,
		match.CodeSubgraph.EntryPoint.ID, // Function implements Feature
		match.FeatureID,
		string(models.ImplementsRel),
		relProps,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create IMPLEMENTS relationship: %w", err)
	}

	return &ImplementsLink{
		FunctionID:       match.CodeSubgraph.EntryPoint.ID,
		FunctionName:     match.CodeSubgraph.EntryPoint.Name,
		Confidence:       match.ValidationResult.Confidence,
		ValidationMethod: match.ValidationResult.Explanation,
		CodeSummary:      match.CodeSubgraph.Summary,
		SubgraphSize:     len(match.CodeSubgraph.Functions),
		RelationshipID:   relationshipID,
	}, nil
}

// GetFeatureImplementations retrieves all code that implements a specific feature
func (fl *FeatureLinker) GetFeatureImplementations(ctx context.Context, featureID string) ([]*ImplementsLink, error) {
	cypher := `
		MATCH (f:Function)-[r:IMPLEMENTS]->(feature:Feature {id: $featureId})
		RETURN f.id AS functionId, f.name AS functionName,
			   r.confidence AS confidence, r.validationMethod AS validationMethod,
			   r.codeSummary AS codeSummary, r.subgraphSize AS subgraphSize,
			   r.id AS relationshipId
		ORDER BY r.confidence DESC
	`

	results, err := fl.client.ExecuteQuery(ctx, cypher, map[string]any{
		"featureId": featureID,
	})
	if err != nil {
		return nil, err
	}

	var implementations []*ImplementsLink
	for _, record := range results {
		recordMap := record.AsMap()
		implementations = append(implementations, &ImplementsLink{
			FunctionID:       getStringValue(recordMap, "functionId"),
			FunctionName:     getStringValue(recordMap, "functionName"),
			Confidence:       getFloatValue(recordMap, "confidence"),
			ValidationMethod: getStringValue(recordMap, "validationMethod"),
			CodeSummary:      getStringValue(recordMap, "codeSummary"),
			SubgraphSize:     getIntValue(recordMap, "subgraphSize"),
			RelationshipID:   getStringValue(recordMap, "relationshipId"),
		})
	}

	return implementations, nil
}

// Helper function for float conversion
func getFloatValue(m map[string]any, key string) float64 {
	if val, ok := m[key]; ok {
		if f64, ok := val.(float64); ok {
			return f64
		}
		if f32, ok := val.(float32); ok {
			return float64(f32)
		}
	}
	return 0.0
}