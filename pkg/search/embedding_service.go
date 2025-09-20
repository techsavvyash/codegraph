package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"strings"
	"time"

	"google.golang.org/genai"
)

// SimpleEmbeddingService provides embeddings using a REST API or local service
type SimpleEmbeddingService struct {
	BaseURL    string
	APIKey     string
	Model      string
	HTTPClient *http.Client
}

// NewSimpleEmbeddingService creates a new embedding service
func NewSimpleEmbeddingService(baseURL, apiKey, model string) *SimpleEmbeddingService {
	return &SimpleEmbeddingService{
		BaseURL: baseURL,
		APIKey:  apiKey,
		Model:   model,
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// EmbeddingRequest represents a request to generate embeddings
type EmbeddingRequest struct {
	Input []string `json:"input"`
	Model string   `json:"model"`
}

// EmbeddingResponse represents the response from an embedding API
type EmbeddingResponse struct {
	Data []struct {
		Embedding []float64 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
	Model string `json:"model"`
	Usage struct {
		PromptTokens int `json:"prompt_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
}

// GenerateEmbedding generates a single embedding for the given text
func (es *SimpleEmbeddingService) GenerateEmbedding(ctx context.Context, text string) ([]float64, error) {
	embeddings, err := es.GenerateBatchEmbeddings(ctx, []string{text})
	if err != nil {
		return nil, err
	}

	if len(embeddings) == 0 {
		return nil, fmt.Errorf("no embeddings returned")
	}

	return embeddings[0], nil
}

// GenerateBatchEmbeddings generates embeddings for multiple texts
func (es *SimpleEmbeddingService) GenerateBatchEmbeddings(ctx context.Context, texts []string) ([][]float64, error) {
	if len(texts) == 0 {
		return nil, fmt.Errorf("no texts provided")
	}

	// If no API key provided, return error
	if es.APIKey == "" || es.BaseURL == "" {
		return nil, fmt.Errorf("API key and base URL are required for embedding service")
	}

	// Call actual API
	response, err := es.callEmbeddingAPI(ctx, texts)
	if err != nil {
		return nil, fmt.Errorf("embedding API call failed: %w", err)
	}

	// Extract embeddings from API response
	var embeddings [][]float64
	for _, data := range response.Data {
		embeddings = append(embeddings, data.Embedding)
	}

	if len(embeddings) != len(texts) {
		return nil, fmt.Errorf("expected %d embeddings, got %d", len(texts), len(embeddings))
	}

	return embeddings, nil
}

// generateMockEmbeddings creates mock embeddings for testing purposes
func (es *SimpleEmbeddingService) generateMockEmbeddings(texts []string) [][]float64 {
	var embeddings [][]float64

	for _, text := range texts {
		// Generate a mock embedding based on text content and semantic meaning
		embedding := make([]float64, 384) // sentence-transformers/all-MiniLM-L6-v2 dimensions

		// Create hash of the content for consistency
		hash := es.simpleHash(text)

		// Analyze text characteristics for more meaningful embeddings
		wordCount := float64(len(strings.Fields(text)))
		charCount := float64(len(text))

		// Create base pattern based on text content
		for j := 0; j < 384; j++ {
			// Create deterministic values based on text content and position
			baseValue := float64((hash+int64(j*13))%2000)/1000.0 - 1.0 // Range: -1.0 to 1.0

			// Add semantic variations based on text characteristics
			if strings.Contains(strings.ToLower(text), "function") {
				baseValue += 0.1 * float64(j%10) / 10.0
			}
			if strings.Contains(strings.ToLower(text), "class") {
				baseValue += 0.15 * float64(j%7) / 7.0
			}
			if strings.Contains(strings.ToLower(text), "index") {
				baseValue += 0.12 * float64(j%5) / 5.0
			}

			// Incorporate word and character count for diversity
			baseValue += (wordCount / 100.0) * 0.05 * float64(j%3) / 3.0
			baseValue += (charCount / 1000.0) * 0.03 * float64(j%4) / 4.0

			// Keep in valid range
			if baseValue > 1.0 {
				baseValue = 1.0
			} else if baseValue < -1.0 {
				baseValue = -1.0
			}

			embedding[j] = baseValue
		}

		// Normalize the vector for proper cosine similarity
		embedding = es.normalizeVector(embedding)
		embeddings = append(embeddings, embedding)
	}

	return embeddings
}

// simpleHash creates a simple hash of a string for mock embedding generation
func (es *SimpleEmbeddingService) simpleHash(s string) int64 {
	var hash int64 = 5381
	for _, c := range s {
		hash = ((hash << 5) + hash) + int64(c)
	}
	return hash
}

// normalizeVector normalizes a vector to unit length for cosine similarity
func (es *SimpleEmbeddingService) normalizeVector(vec []float64) []float64 {
	var sum float64
	for _, v := range vec {
		sum += v * v
	}

	if sum == 0 {
		return vec
	}

	magnitude := math.Sqrt(sum) // Proper L2 norm: sqrt(sum of squares)
	if magnitude == 0 {
		return vec
	}

	normalized := make([]float64, len(vec))
	for i, v := range vec {
		normalized[i] = v / magnitude
	}

	return normalized
}

// CallEmbeddingAPI makes an actual API call to an embedding service
func (es *SimpleEmbeddingService) callEmbeddingAPI(ctx context.Context, texts []string) (*EmbeddingResponse, error) {
	request := EmbeddingRequest{
		Input: texts,
		Model: es.Model,
	}

	jsonData, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", es.BaseURL+"/embeddings", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if es.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+es.APIKey)
	}

	resp, err := es.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var embeddingResponse EmbeddingResponse
	if err := json.NewDecoder(resp.Body).Decode(&embeddingResponse); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &embeddingResponse, nil
}

// MockEmbeddingService provides mock embeddings for testing
type MockEmbeddingService struct{}

// NewMockEmbeddingService creates a new mock embedding service
func NewMockEmbeddingService() *MockEmbeddingService {
	return &MockEmbeddingService{}
}

// GenerateEmbedding generates a mock embedding
func (mes *MockEmbeddingService) GenerateEmbedding(ctx context.Context, text string) ([]float64, error) {
	// Create a simple mock embedding based on text length and content
	embedding := make([]float64, 384)

	// Simple algorithm to create consistent but varied embeddings
	hash := int64(5381)
	for _, c := range text {
		hash = ((hash << 5) + hash) + int64(c)
	}

	for i := range embedding {
		value := float64((hash+int64(i))%2000)/1000.0 - 1.0 // Range: -1.0 to 1.0
		embedding[i] = value
	}

	return embedding, nil
}

// GenerateBatchEmbeddings generates mock embeddings for multiple texts
func (mes *MockEmbeddingService) GenerateBatchEmbeddings(ctx context.Context, texts []string) ([][]float64, error) {
	var embeddings [][]float64

	for _, text := range texts {
		embedding, err := mes.GenerateEmbedding(ctx, text)
		if err != nil {
			return nil, err
		}
		embeddings = append(embeddings, embedding)
	}

	return embeddings, nil
}

// GeminiEmbeddingService provides embeddings using Google's Gemini API
type GeminiEmbeddingService struct {
	APIKey string
	Model  string
}

// NewGeminiEmbeddingService creates a new Gemini embedding service
func NewGeminiEmbeddingService(apiKey, model string) *GeminiEmbeddingService {
	if model == "" {
		model = "gemini-embedding-001" // Stable Gemini embedding model
	}

	return &GeminiEmbeddingService{
		APIKey: apiKey,
		Model:  model,
	}
}

// GenerateEmbedding generates a single embedding for the given text using Gemini
func (ges *GeminiEmbeddingService) GenerateEmbedding(ctx context.Context, text string) ([]float64, error) {
	embeddings, err := ges.GenerateBatchEmbeddings(ctx, []string{text})
	if err != nil {
		return nil, err
	}

	if len(embeddings) == 0 {
		return nil, fmt.Errorf("no embeddings returned")
	}

	return embeddings[0], nil
}

// GenerateBatchEmbeddings generates embeddings for multiple texts using Gemini
func (ges *GeminiEmbeddingService) GenerateBatchEmbeddings(ctx context.Context, texts []string) ([][]float64, error) {
	if len(texts) == 0 {
		return nil, fmt.Errorf("no texts provided")
	}

	// If no API key provided, return error
	if ges.APIKey == "" {
		return nil, fmt.Errorf("Google API key is required for Gemini embedding service")
	}

	// Create client with API key
	clientConfig := &genai.ClientConfig{
		Backend: genai.BackendGeminiAPI,
		APIKey:  ges.APIKey,
	}
	client, err := genai.NewClient(ctx, clientConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create Gemini client: %w", err)
	}

	var allEmbeddings [][]float64

	// Process texts in batches (Gemini supports batch processing)
	for _, text := range texts {
		// Create content from text
		contents := genai.Text(text)

		// Generate embedding with semantic similarity task type and 768 dimensions
		embedConfig := &genai.EmbedContentConfig{
			TaskType:             "SEMANTIC_SIMILARITY",
			OutputDimensionality: genai.Ptr(int32(768)), // Request 768 dimensions to fit Neo4j limit
		}

		result, err := client.Models.EmbedContent(ctx, ges.Model, contents, embedConfig)
		if err != nil {
			return nil, fmt.Errorf("Gemini embedding failed for text '%s': %w", text[:min(50, len(text))], err)
		}

		if result == nil || result.Embeddings == nil || len(result.Embeddings) == 0 {
			return nil, fmt.Errorf("no embedding returned for text '%s'", text[:min(50, len(text))])
		}

		// Get the first (and only) embedding
		embedding := result.Embeddings[0]
		if len(embedding.Values) == 0 {
			return nil, fmt.Errorf("empty embedding values for text '%s'", text[:min(50, len(text))])
		}

		// Convert float32 to float64
		embeddingVec := make([]float64, len(embedding.Values))
		for i, v := range embedding.Values {
			embeddingVec[i] = float64(v)
		}

		allEmbeddings = append(allEmbeddings, embeddingVec)
	}

	log.Printf("✓ Generated %d embeddings using Gemini %s", len(allEmbeddings), ges.Model)
	return allEmbeddings, nil
}

// Helper function for min
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
