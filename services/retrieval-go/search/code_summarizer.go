package search

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/context-maximiser/code-graph/libs/neo4j-client-go"
)

// CodeSubgraph represents a connected set of functions that implement business logic
type CodeSubgraph struct {
	EntryPoint   FunctionNode   `json:"entryPoint"`
	Functions    []FunctionNode `json:"functions"`
	CallDepth    int            `json:"callDepth"`
	TotalLines   int            `json:"totalLines"`
	Summary      string         `json:"summary"`      // LLM-generated summary
	Embedding    []float64      `json:"embedding"`    // Embedding of the summary
	Confidence   float64        `json:"confidence"`   // Confidence in the summary
}

// FunctionNode represents a function in the subgraph
type FunctionNode struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Signature   string `json:"signature"`
	SourceCode  string `json:"sourceCode"`
	FilePath    string `json:"filePath"`
	StartLine   int    `json:"startLine"`
	EndLine     int    `json:"endLine"`
	DocString   string `json:"docString"`
}

// CodeSubgraphSummarizer extracts and summarizes code subgraphs using LLM
type CodeSubgraphSummarizer struct {
	client           *neo4j.Client
	embeddingService EmbeddingService
	llmService       LLMService
	maxDepth         int
	maxFunctions     int
}

// NewCodeSubgraphSummarizer creates a new code subgraph summarizer
func NewCodeSubgraphSummarizer(client *neo4j.Client, embeddingService EmbeddingService) *CodeSubgraphSummarizer {
	return &CodeSubgraphSummarizer{
		client:           client,
		embeddingService: embeddingService,
		llmService:       nil, // Optional LLM service for text generation
		maxDepth:         3,   // Maximum call depth to traverse
		maxFunctions:     15,  // Maximum functions to include in subgraph
	}
}

// WithLLMService sets the LLM service for text generation
func (css *CodeSubgraphSummarizer) WithLLMService(llmService LLMService) *CodeSubgraphSummarizer {
	css.llmService = llmService
	return css
}

// ExtractAndSummarizeSubgraph extracts a code subgraph starting from an entry point and generates an LLM summary
func (css *CodeSubgraphSummarizer) ExtractAndSummarizeSubgraph(ctx context.Context, entryPointID string) (*CodeSubgraph, error) {
	log.Printf("Extracting subgraph from entry point: %s", entryPointID)

	// Step 1: Extract the subgraph using graph traversal
	subgraph, err := css.extractSubgraph(ctx, entryPointID)
	if err != nil {
		return nil, fmt.Errorf("failed to extract subgraph: %w", err)
	}

	if len(subgraph.Functions) == 0 {
		return nil, fmt.Errorf("no functions found in subgraph")
	}

	log.Printf("Extracted subgraph with %d functions", len(subgraph.Functions))

	// Step 2: Generate LLM summary of the code logic
	summary, confidence, err := css.generateCodeSummary(ctx, subgraph)
	if err != nil {
		return nil, fmt.Errorf("failed to generate code summary: %w", err)
	}

	subgraph.Summary = summary
	subgraph.Confidence = confidence

	// Step 3: Generate embedding from the summary (skip if pre-computed embedding exists)
	if subgraph.Embedding == nil || len(subgraph.Embedding) == 0 {
		embedding, err := css.embeddingService.GenerateEmbedding(ctx, summary)
		if err != nil {
			return nil, fmt.Errorf("failed to generate embedding: %w", err)
		}

		subgraph.Embedding = embedding
		log.Printf("Generated summary (%d chars) and embedding (%d dims) for subgraph", len(summary), len(embedding))
	} else {
		log.Printf("Using pre-computed embedding (%d dims) for subgraph with summary (%d chars)",
			len(subgraph.Embedding), len(summary))
	}

	return subgraph, nil
}

// extractSubgraph traverses the call graph to extract a connected subgraph
func (css *CodeSubgraphSummarizer) extractSubgraph(ctx context.Context, entryPointID string) (*CodeSubgraph, error) {
	// Query to get entry point function details including pre-computed embedding
	entryPointCypher := `
		MATCH (f:Function {id: $functionId})
		RETURN f.id AS id, f.name AS name, f.signature AS signature,
			   f.sourceCode AS sourceCode, f.filePath AS filePath,
			   f.startLine AS startLine, f.endLine AS endLine,
			   f.docstring AS docstring, f.embedding AS embedding
	`

	results, err := css.client.ExecuteQuery(ctx, entryPointCypher, map[string]any{
		"functionId": entryPointID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get entry point function: %w", err)
	}

	if len(results) == 0 {
		return nil, fmt.Errorf("entry point function not found: %s", entryPointID)
	}

	// Create entry point function node
	entryRecord := results[0].AsMap()
	entryPoint := FunctionNode{
		ID:         entryRecord["id"].(string),
		Name:       getStringValue(entryRecord, "name"),
		Signature:  getStringValue(entryRecord, "signature"),
		SourceCode: getStringValue(entryRecord, "sourceCode"),
		FilePath:   getStringValue(entryRecord, "filePath"),
		StartLine:  getIntValue(entryRecord, "startLine"),
		EndLine:    getIntValue(entryRecord, "endLine"),
		DocString:  getStringValue(entryRecord, "docstring"),
	}

	// Check if pre-computed embedding exists
	var precomputedEmbedding []float64
	if embeddingVal, ok := entryRecord["embedding"]; ok && embeddingVal != nil {
		// Neo4j returns embeddings as []interface{}, need to convert to []float64
		if embeddingSlice, ok := embeddingVal.([]interface{}); ok {
			precomputedEmbedding = make([]float64, len(embeddingSlice))
			for i, val := range embeddingSlice {
				if floatVal, ok := val.(float64); ok {
					precomputedEmbedding[i] = floatVal
				}
			}
			log.Printf("Found pre-computed embedding (%d dims) for function %s", len(precomputedEmbedding), entryPoint.Name)
		}
	}

	// Query to get connected functions via CALLS and FLOWS_TO relationships
	subgraphCypher := fmt.Sprintf(`
		MATCH (entry:Function {id: $entryId})
		MATCH path = (entry)-[:CALLS|FLOWS_TO*1..%d]->(connected:Function)
		WHERE connected.id <> $entryId
		WITH connected, min(length(path)) as depth
		RETURN DISTINCT connected.id AS id, connected.name AS name,
			   connected.signature AS signature, connected.sourceCode AS sourceCode,
			   connected.filePath AS filePath, connected.startLine AS startLine,
			   connected.endLine AS endLine, connected.docstring AS docstring,
			   depth
		ORDER BY depth, connected.name
		LIMIT %d
	`, css.maxDepth, css.maxFunctions)

	subgraphResults, err := css.client.ExecuteQuery(ctx, subgraphCypher, map[string]any{
		"entryId": entryPointID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get subgraph functions: %w", err)
	}

	// Create function nodes for the subgraph
	var functions []FunctionNode
	functions = append(functions, entryPoint) // Add entry point first

	maxDepth := 0
	totalLines := entryPoint.EndLine - entryPoint.StartLine + 1

	for _, record := range subgraphResults {
		recordMap := record.AsMap()
		depth := getIntValue(recordMap, "depth")
		if depth > maxDepth {
			maxDepth = depth
		}

		functionNode := FunctionNode{
			ID:         recordMap["id"].(string),
			Name:       getStringValue(recordMap, "name"),
			Signature:  getStringValue(recordMap, "signature"),
			SourceCode: getStringValue(recordMap, "sourceCode"),
			FilePath:   getStringValue(recordMap, "filePath"),
			StartLine:  getIntValue(recordMap, "startLine"),
			EndLine:    getIntValue(recordMap, "endLine"),
			DocString:  getStringValue(recordMap, "docstring"),
		}

		totalLines += functionNode.EndLine - functionNode.StartLine + 1
		functions = append(functions, functionNode)
	}

	subgraph := &CodeSubgraph{
		EntryPoint:   entryPoint,
		Functions:    functions,
		CallDepth:    maxDepth,
		TotalLines:   totalLines,
	}

	// If we found a pre-computed embedding, include it in the subgraph
	if precomputedEmbedding != nil && len(precomputedEmbedding) > 0 {
		subgraph.Embedding = precomputedEmbedding
	}

	return subgraph, nil
}

// generateCodeSummary uses LLM to create a natural language summary of the code subgraph
func (css *CodeSubgraphSummarizer) generateCodeSummary(ctx context.Context, subgraph *CodeSubgraph) (string, float64, error) {
	// Construct a prompt for the LLM
	prompt := css.buildSummaryPrompt(subgraph)

	// Try to use LLM service if available
	if css.llmService != nil {
		log.Println("Using LLM service for code summarization")
		summary, err := css.llmService.GenerateText(ctx, prompt)
		if err != nil {
			log.Printf("Warning: LLM summarization failed, falling back to heuristic: %v", err)
			return css.generateFallbackSummary(subgraph), 0.6, nil
		}

		// Clean up the summary
		summary = strings.TrimSpace(summary)

		// Higher confidence for LLM-generated summaries
		confidence := 0.9

		log.Printf("Generated LLM code summary: %s (confidence: %.2f)", summary, confidence)
		return summary, confidence, nil
	}

	// Fallback: Use heuristic-based summary generation
	log.Println("No LLM service available, using heuristic summarization")
	summary := css.generateFallbackSummary(subgraph)
	confidence := 0.7

	// Enhance summary based on function patterns
	if css.containsPattern(subgraph, []string{"validate", "check", "verify"}) {
		summary = "This code implements validation and verification logic. " + summary
	}
	if css.containsPattern(subgraph, []string{"create", "generate", "build"}) {
		summary = "This code implements creation and generation logic. " + summary
	}
	if css.containsPattern(subgraph, []string{"auth", "login", "token"}) {
		summary = "This code implements authentication and authorization logic. " + summary
	}

	log.Printf("Generated heuristic code summary: %s (confidence: %.2f)", summary, confidence)
	return summary, confidence, nil
}

// buildSummaryPrompt constructs the prompt for LLM code summarization
func (css *CodeSubgraphSummarizer) buildSummaryPrompt(subgraph *CodeSubgraph) string {
	var promptBuilder strings.Builder

	promptBuilder.WriteString("Analyze the following code subgraph and provide a concise, natural language summary ")
	promptBuilder.WriteString("that describes the PURPOSE and BEHAVIOR of this code logic. ")
	promptBuilder.WriteString("Focus on WHAT the code accomplishes from a business/functional perspective, not HOW it's implemented.\n\n")

	promptBuilder.WriteString(fmt.Sprintf("ENTRY POINT: %s\n", subgraph.EntryPoint.Name))
	if subgraph.EntryPoint.DocString != "" {
		promptBuilder.WriteString(fmt.Sprintf("Documentation: %s\n", subgraph.EntryPoint.DocString))
	}
	promptBuilder.WriteString("\n")

	// Add function signatures and documentation
	promptBuilder.WriteString("FUNCTIONS IN SUBGRAPH:\n")
	for i, function := range subgraph.Functions {
		if i >= 10 { // Limit to first 10 functions for prompt efficiency
			promptBuilder.WriteString(fmt.Sprintf("... and %d more functions\n", len(subgraph.Functions)-i))
			break
		}

		promptBuilder.WriteString(fmt.Sprintf("%d. %s\n", i+1, function.Signature))
		if function.DocString != "" {
			promptBuilder.WriteString(fmt.Sprintf("   Doc: %s\n", function.DocString))
		}

		// Include limited source code for context (first few lines)
		if function.SourceCode != "" {
			lines := strings.Split(function.SourceCode, "\n")
			maxLines := 5
			if len(lines) > maxLines {
				promptBuilder.WriteString(fmt.Sprintf("   Code preview:\n   %s\n   ... (%d more lines)\n",
					strings.Join(lines[:maxLines], "\n   "), len(lines)-maxLines))
			} else {
				promptBuilder.WriteString(fmt.Sprintf("   Code:\n   %s\n", strings.Join(lines, "\n   ")))
			}
		}
		promptBuilder.WriteString("\n")
	}

	promptBuilder.WriteString("\nProvide a 2-3 sentence summary that captures the core business logic and purpose of this code subgraph. ")
	promptBuilder.WriteString("Focus on the high-level business value, not implementation details.")

	return promptBuilder.String()
}

// generateFallbackSummary creates a basic summary when LLM is unavailable
func (css *CodeSubgraphSummarizer) generateFallbackSummary(subgraph *CodeSubgraph) string {
	var summary strings.Builder

	summary.WriteString(fmt.Sprintf("Code subgraph starting from %s", subgraph.EntryPoint.Name))

	if len(subgraph.Functions) > 1 {
		summary.WriteString(fmt.Sprintf(" involving %d connected functions", len(subgraph.Functions)))
	}

	// Add function name patterns
	var functionNames []string
	for _, fn := range subgraph.Functions {
		if len(functionNames) < 5 { // Limit for readability
			functionNames = append(functionNames, fn.Name)
		}
	}

	if len(functionNames) > 0 {
		summary.WriteString(fmt.Sprintf(": %s", strings.Join(functionNames, ", ")))
		if len(subgraph.Functions) > len(functionNames) {
			summary.WriteString(fmt.Sprintf(" and %d others", len(subgraph.Functions)-len(functionNames)))
		}
	}

	summary.WriteString(".")

	return summary.String()
}

// containsPattern checks if the subgraph contains functions matching certain patterns
func (css *CodeSubgraphSummarizer) containsPattern(subgraph *CodeSubgraph, patterns []string) bool {
	for _, function := range subgraph.Functions {
		functionNameLower := strings.ToLower(function.Name)
		for _, pattern := range patterns {
			if strings.Contains(functionNameLower, pattern) {
				return true
			}
		}
	}
	return false
}

// Helper functions for type conversion
func getIntValue(m map[string]any, key string) int {
	if val, ok := m[key]; ok {
		if i64, ok := val.(int64); ok {
			return int(i64)
		}
		if i, ok := val.(int); ok {
			return i
		}
	}
	return 0
}