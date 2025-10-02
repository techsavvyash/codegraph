package search

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
)

// ValidationResult represents the result of LLM validation
type ValidationResult struct {
	IsMatch     bool    `json:"isMatch"`
	Confidence  float64 `json:"confidence"`
	Reasoning   string  `json:"reasoning"`
	Explanation string  `json:"explanation"`
}

// FeatureCodeMatch represents a potential match between a feature and code
type FeatureCodeMatch struct {
	FeatureID          string         `json:"featureId"`
	FeatureName        string         `json:"featureName"`
	FeatureDescription string         `json:"featureDescription"`
	CodeSubgraph       *CodeSubgraph  `json:"codeSubgraph"`
	InitialConfidence  float64        `json:"initialConfidence"`
	ValidationResult   *ValidationResult `json:"validationResult,omitempty"`
}

// LLMValidator uses LLM to validate whether code implements a feature
type LLMValidator struct {
	embeddingService EmbeddingService
	llmService       LLMService
}

// NewLLMValidator creates a new LLM validator
func NewLLMValidator(embeddingService EmbeddingService) *LLMValidator {
	return &LLMValidator{
		embeddingService: embeddingService,
		llmService:       nil, // Optional LLM service for text generation
	}
}

// WithLLMService sets the LLM service for validation
func (lv *LLMValidator) WithLLMService(llmService LLMService) *LLMValidator {
	lv.llmService = llmService
	return lv
}

// ValidateFeatureImplementation determines if a code subgraph implements a given feature
func (lv *LLMValidator) ValidateFeatureImplementation(ctx context.Context, match *FeatureCodeMatch) error {
	log.Printf("Validating if code subgraph implements feature: %s", match.FeatureName)

	// Create a validation prompt for the LLM
	prompt := lv.createValidationPrompt(match)

	var validation *ValidationResult

	// Try to use LLM service if available
	if lv.llmService != nil {
		log.Println("Using LLM service for validation")
		llmValidation, err := lv.performLLMValidation(ctx, match, prompt)
		if err != nil {
			log.Printf("Warning: LLM validation failed, falling back to heuristic: %v", err)
			validation = lv.performHeuristicValidation(match)
		} else {
			validation = llmValidation
		}
	} else {
		// Use heuristic-based validation
		log.Println("No LLM service available, using heuristic validation")
		validation = lv.performHeuristicValidation(match)

		// Try to enhance validation using embedding similarity
		enhancedValidation, err := lv.enhanceValidationWithEmbeddings(ctx, match, prompt)
		if err != nil {
			log.Printf("Warning: enhanced validation failed, using heuristic result: %v", err)
		} else {
			validation = enhancedValidation
		}
	}

	match.ValidationResult = validation

	log.Printf("Validation result for %s: %t (confidence: %.3f)",
		match.FeatureName, validation.IsMatch, validation.Confidence)

	return nil
}

// ValidateBatch validates multiple feature-code matches
func (lv *LLMValidator) ValidateBatch(ctx context.Context, matches []*FeatureCodeMatch) error {
	for _, match := range matches {
		if err := lv.ValidateFeatureImplementation(ctx, match); err != nil {
			return fmt.Errorf("validation failed for feature %s: %w", match.FeatureName, err)
		}
	}
	return nil
}

// createValidationPrompt creates a prompt for LLM validation
func (lv *LLMValidator) createValidationPrompt(match *FeatureCodeMatch) string {
	var promptBuilder strings.Builder

	promptBuilder.WriteString("TASK: Determine if the following code logic implements the specified feature requirement.\n\n")

	// Feature information
	promptBuilder.WriteString("FEATURE REQUIREMENT:\n")
	promptBuilder.WriteString(fmt.Sprintf("Name: %s\n", match.FeatureName))
	promptBuilder.WriteString(fmt.Sprintf("Description: %s\n\n", match.FeatureDescription))

	// Code information
	promptBuilder.WriteString("CODE IMPLEMENTATION:\n")
	if match.CodeSubgraph != nil {
		promptBuilder.WriteString(fmt.Sprintf("Entry Point: %s\n", match.CodeSubgraph.EntryPoint.Name))
		if match.CodeSubgraph.Summary != "" {
			promptBuilder.WriteString(fmt.Sprintf("Code Summary: %s\n", match.CodeSubgraph.Summary))
		}

		promptBuilder.WriteString(fmt.Sprintf("Functions Involved: %d\n", len(match.CodeSubgraph.Functions)))

		// Include key function signatures
		promptBuilder.WriteString("Key Functions:\n")
		for i, fn := range match.CodeSubgraph.Functions {
			if i >= 5 { // Limit to first 5 functions
				promptBuilder.WriteString(fmt.Sprintf("... and %d more\n", len(match.CodeSubgraph.Functions)-i))
				break
			}
			promptBuilder.WriteString(fmt.Sprintf("- %s\n", fn.Signature))
			if fn.DocString != "" {
				promptBuilder.WriteString(fmt.Sprintf("  Doc: %s\n", fn.DocString))
			}
		}
	}

	promptBuilder.WriteString("\nQUESTION: Does this code logic accurately implement the specified feature requirement?\n")
	promptBuilder.WriteString("Consider:\n")
	promptBuilder.WriteString("1. Does the code's purpose align with the feature's intent?\n")
	promptBuilder.WriteString("2. Are the key behaviors described in the feature present in the code?\n")
	promptBuilder.WriteString("3. Is this a primary implementation or just tangentially related?\n\n")

	promptBuilder.WriteString("RESPOND: Yes/No with brief reasoning.\n")

	return promptBuilder.String()
}

// performLLMValidation uses LLM to validate if code implements a feature
func (lv *LLMValidator) performLLMValidation(ctx context.Context, match *FeatureCodeMatch, prompt string) (*ValidationResult, error) {
	// Ask the LLM to validate the match
	systemPrompt := "You are a code analysis expert. Your task is to determine if a given code implementation matches a feature requirement. " +
		"Respond with a JSON object containing: {\"isMatch\": true/false, \"confidence\": 0.0-1.0, \"reasoning\": \"brief explanation\"}. " +
		"Be concise and focus on semantic alignment between the feature intent and code behavior."

	response, err := lv.llmService.GenerateTextWithSystemPrompt(ctx, systemPrompt, prompt)
	if err != nil {
		return nil, fmt.Errorf("LLM validation request failed: %w", err)
	}

	// Parse the LLM response
	validation, err := lv.parseLLMValidationResponse(response)
	if err != nil {
		log.Printf("Warning: failed to parse LLM response, using heuristic fallback: %v", err)
		return lv.performHeuristicValidation(match), nil
	}

	// Add detailed explanation
	validation.Explanation = fmt.Sprintf("LLM validation: %s (confidence: %.3f)", validation.Reasoning, validation.Confidence)

	log.Printf("LLM validation result: isMatch=%t, confidence=%.3f, reasoning=%s",
		validation.IsMatch, validation.Confidence, validation.Reasoning)

	return validation, nil
}

// parseLLMValidationResponse parses the LLM's validation response
func (lv *LLMValidator) parseLLMValidationResponse(response string) (*ValidationResult, error) {
	// Try to extract JSON from the response
	response = strings.TrimSpace(response)

	// Find JSON object in response (LLM might add extra text)
	startIdx := strings.Index(response, "{")
	endIdx := strings.LastIndex(response, "}")

	if startIdx == -1 || endIdx == -1 {
		return nil, fmt.Errorf("no JSON object found in response")
	}

	jsonStr := response[startIdx : endIdx+1]

	// Parse the JSON
	var result struct {
		IsMatch    bool    `json:"isMatch"`
		Confidence float64 `json:"confidence"`
		Reasoning  string  `json:"reasoning"`
	}

	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		// Try a more lenient parsing approach
		return lv.parseTextualResponse(response)
	}

	// Validate confidence bounds
	if result.Confidence < 0.0 {
		result.Confidence = 0.0
	} else if result.Confidence > 1.0 {
		result.Confidence = 1.0
	}

	return &ValidationResult{
		IsMatch:    result.IsMatch,
		Confidence: result.Confidence,
		Reasoning:  result.Reasoning,
	}, nil
}

// parseTextualResponse attempts to parse non-JSON LLM responses
func (lv *LLMValidator) parseTextualResponse(response string) (*ValidationResult, error) {
	responseLower := strings.ToLower(response)

	// Check for affirmative indicators
	isMatch := strings.Contains(responseLower, "yes") ||
		strings.Contains(responseLower, "match") ||
		strings.Contains(responseLower, "implements") ||
		strings.Contains(responseLower, "correct") ||
		strings.Contains(responseLower, "accurate")

	// Check for negative indicators
	isNegative := strings.Contains(responseLower, "no") ||
		strings.Contains(responseLower, "does not") ||
		strings.Contains(responseLower, "doesn't") ||
		strings.Contains(responseLower, "incorrect") ||
		strings.Contains(responseLower, "unrelated")

	if isNegative {
		isMatch = false
	}

	// Extract confidence if mentioned
	confidence := 0.7 // Default moderate confidence for textual responses

	// Look for percentage or decimal confidence
	confidencePattern := regexp.MustCompile(`(\d+)%|confidence[:\s]+([0-9.]+)`)
	matches := confidencePattern.FindStringSubmatch(response)
	if len(matches) > 1 {
		if matches[1] != "" {
			// Percentage format
			if pct, err := strconv.Atoi(matches[1]); err == nil {
				confidence = float64(pct) / 100.0
			}
		} else if matches[2] != "" {
			// Decimal format
			if conf, err := strconv.ParseFloat(matches[2], 64); err == nil {
				confidence = conf
				if confidence > 1.0 {
					confidence = confidence / 100.0
				}
			}
		}
	}

	return &ValidationResult{
		IsMatch:    isMatch,
		Confidence: confidence,
		Reasoning:  strings.TrimSpace(response),
	}, nil
}

// performHeuristicValidation uses rule-based validation when LLM is unavailable
func (lv *LLMValidator) performHeuristicValidation(match *FeatureCodeMatch) *ValidationResult {
	// Start with initial confidence from vector similarity
	confidence := match.InitialConfidence

	// Apply heuristic rules to adjust confidence
	var reasons []string

	if match.CodeSubgraph != nil {
		// Rule 1: Check keyword overlap between feature and code
		keywordOverlap := lv.calculateKeywordOverlap(match.FeatureDescription, match.CodeSubgraph)
		if keywordOverlap > 0.3 {
			confidence += 0.1
			reasons = append(reasons, fmt.Sprintf("high keyword overlap (%.2f)", keywordOverlap))
		} else if keywordOverlap < 0.1 {
			confidence -= 0.1
			reasons = append(reasons, "low keyword overlap")
		}

		// Rule 2: Consider subgraph size (too small might be incomplete, too large might be unfocused)
		functionCount := len(match.CodeSubgraph.Functions)
		if functionCount >= 3 && functionCount <= 10 {
			confidence += 0.05
			reasons = append(reasons, "appropriate subgraph size")
		} else if functionCount > 15 {
			confidence -= 0.1
			reasons = append(reasons, "subgraph too large")
		}

		// Rule 3: Check for meaningful function names
		meaningfulNames := lv.countMeaningfulFunctionNames(match.CodeSubgraph)
		if meaningfulNames > 0.5 {
			confidence += 0.05
			reasons = append(reasons, "meaningful function names")
		}

		// Rule 4: Check for documentation alignment
		if lv.hasAlignedDocumentation(match.FeatureDescription, match.CodeSubgraph) {
			confidence += 0.1
			reasons = append(reasons, "documentation alignment")
		}
	}

	// Apply confidence bounds
	if confidence > 1.0 {
		confidence = 1.0
	} else if confidence < 0.0 {
		confidence = 0.0
	}

	// Determine if it's a match based on confidence threshold
	isMatch := confidence >= 0.6

	reasoning := strings.Join(reasons, "; ")
	if reasoning == "" {
		reasoning = "vector similarity analysis"
	}

	explanation := fmt.Sprintf("Heuristic validation based on %s. Confidence: %.3f", reasoning, confidence)

	return &ValidationResult{
		IsMatch:     isMatch,
		Confidence:  confidence,
		Reasoning:   reasoning,
		Explanation: explanation,
	}
}

// enhanceValidationWithEmbeddings uses embedding similarity to improve validation
func (lv *LLMValidator) enhanceValidationWithEmbeddings(ctx context.Context, match *FeatureCodeMatch, prompt string) (*ValidationResult, error) {
	// Generate embeddings for both feature and code summary
	featureEmbedding, err := lv.embeddingService.GenerateEmbedding(ctx, match.FeatureDescription)
	if err != nil {
		return nil, fmt.Errorf("failed to generate feature embedding: %w", err)
	}

	var codeText string
	if match.CodeSubgraph != nil && match.CodeSubgraph.Summary != "" {
		codeText = match.CodeSubgraph.Summary
	} else {
		// Fallback to function names and signatures
		var parts []string
		if match.CodeSubgraph != nil {
			for _, fn := range match.CodeSubgraph.Functions {
				parts = append(parts, fn.Name+" "+fn.Signature)
			}
		}
		codeText = strings.Join(parts, " ")
	}

	codeEmbedding, err := lv.embeddingService.GenerateEmbedding(ctx, codeText)
	if err != nil {
		return nil, fmt.Errorf("failed to generate code embedding: %w", err)
	}

	// Calculate cosine similarity
	similarity := lv.cosineSimilarity(featureEmbedding, codeEmbedding)

	// Use the higher of vector similarity or initial confidence
	enhancedConfidence := similarity
	if match.InitialConfidence > enhancedConfidence {
		enhancedConfidence = match.InitialConfidence
	}

	// Apply enhancement boost
	enhancedConfidence += 0.1

	// Perform heuristic validation with enhanced confidence
	heuristicResult := lv.performHeuristicValidation(match)

	// Combine results
	finalConfidence := (enhancedConfidence + heuristicResult.Confidence) / 2
	isMatch := finalConfidence >= 0.6

	explanation := fmt.Sprintf("Enhanced validation: vector similarity %.3f, heuristic confidence %.3f, final %.3f",
		similarity, heuristicResult.Confidence, finalConfidence)

	return &ValidationResult{
		IsMatch:     isMatch,
		Confidence:  finalConfidence,
		Reasoning:   "enhanced embedding similarity + " + heuristicResult.Reasoning,
		Explanation: explanation,
	}, nil
}

// calculateKeywordOverlap calculates overlap between feature description and code
func (lv *LLMValidator) calculateKeywordOverlap(featureDesc string, subgraph *CodeSubgraph) float64 {
	featureWords := lv.extractKeywords(featureDesc)

	var codeWords []string
	for _, fn := range subgraph.Functions {
		codeWords = append(codeWords, lv.extractKeywords(fn.Name)...)
		codeWords = append(codeWords, lv.extractKeywords(fn.DocString)...)
	}

	if len(featureWords) == 0 || len(codeWords) == 0 {
		return 0.0
	}

	overlap := 0
	codeWordSet := make(map[string]bool)
	for _, word := range codeWords {
		codeWordSet[word] = true
	}

	for _, word := range featureWords {
		if codeWordSet[word] {
			overlap++
		}
	}

	return float64(overlap) / float64(len(featureWords))
}

// extractKeywords extracts meaningful keywords from text
func (lv *LLMValidator) extractKeywords(text string) []string {
	text = strings.ToLower(text)
	words := strings.Fields(text)

	var keywords []string
	stopWords := map[string]bool{
		"the": true, "and": true, "or": true, "but": true, "in": true, "on": true,
		"at": true, "to": true, "for": true, "of": true, "with": true, "by": true,
		"is": true, "are": true, "was": true, "were": true, "be": true, "been": true,
		"have": true, "has": true, "had": true, "do": true, "does": true, "did": true,
		"will": true, "would": true, "could": true, "should": true, "can": true,
		"this": true, "that": true, "these": true, "those": true, "a": true, "an": true,
	}

	for _, word := range words {
		// Remove punctuation
		word = strings.Trim(word, ".,!?;:()[]{}\"'")
		if len(word) > 2 && !stopWords[word] {
			keywords = append(keywords, word)
		}
	}

	return keywords
}

// countMeaningfulFunctionNames calculates the ratio of functions with meaningful names
func (lv *LLMValidator) countMeaningfulFunctionNames(subgraph *CodeSubgraph) float64 {
	meaningful := 0
	total := len(subgraph.Functions)

	for _, fn := range subgraph.Functions {
		name := strings.ToLower(fn.Name)
		// Check if name contains meaningful words (not just generic patterns)
		if strings.Contains(name, "get") || strings.Contains(name, "set") ||
		   strings.Contains(name, "create") || strings.Contains(name, "update") ||
		   strings.Contains(name, "delete") || strings.Contains(name, "process") ||
		   strings.Contains(name, "handle") || strings.Contains(name, "validate") ||
		   strings.Contains(name, "generate") || strings.Contains(name, "calculate") {
			meaningful++
		}
	}

	if total == 0 {
		return 0.0
	}

	return float64(meaningful) / float64(total)
}

// hasAlignedDocumentation checks if function documentation aligns with feature description
func (lv *LLMValidator) hasAlignedDocumentation(featureDesc string, subgraph *CodeSubgraph) bool {
	featureKeywords := lv.extractKeywords(featureDesc)

	for _, fn := range subgraph.Functions {
		if fn.DocString != "" {
			docKeywords := lv.extractKeywords(fn.DocString)
			overlap := 0
			for _, fkw := range featureKeywords {
				for _, dkw := range docKeywords {
					if fkw == dkw {
						overlap++
						break
					}
				}
			}

			// If any function has good documentation overlap, consider it aligned
			if len(featureKeywords) > 0 && float64(overlap)/float64(len(featureKeywords)) > 0.2 {
				return true
			}
		}
	}

	return false
}

// cosineSimilarity calculates cosine similarity between two vectors
func (lv *LLMValidator) cosineSimilarity(a, b []float64) float64 {
	if len(a) != len(b) {
		return 0.0
	}

	var dotProduct, normA, normB float64
	for i := 0; i < len(a); i++ {
		dotProduct += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}

	if normA == 0 || normB == 0 {
		return 0.0
	}

	return dotProduct / (normA * normB)
}