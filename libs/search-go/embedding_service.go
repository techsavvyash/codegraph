package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
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

// GenerateBatchEmbeddings generates embeddings for multiple texts using Gemini.
// It uses the batch EmbedContent API (up to 100 texts per call) for efficient
// quota usage, with automatic retry and backoff on rate limit (429) errors.
func (ges *GeminiEmbeddingService) GenerateBatchEmbeddings(ctx context.Context, texts []string) ([][]float64, error) {
	if len(texts) == 0 {
		return nil, fmt.Errorf("no texts provided")
	}

	if ges.APIKey == "" {
		return nil, fmt.Errorf("Google API key is required for Gemini embedding service")
	}

	clientConfig := &genai.ClientConfig{
		Backend: genai.BackendGeminiAPI,
		APIKey:  ges.APIKey,
	}
	client, err := genai.NewClient(ctx, clientConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create Gemini client: %w", err)
	}

	embedConfig := &genai.EmbedContentConfig{
		TaskType:             "SEMANTIC_SIMILARITY",
		OutputDimensionality: genai.Ptr(int32(768)),
	}

	allEmbeddings := make([][]float64, 0, len(texts))

	// Process in chunks of up to 100 texts per API call (Gemini batch limit).
	// Each batch call counts as 1 API request against the daily quota.
	const maxBatchSize = 100
	for batchStart := 0; batchStart < len(texts); batchStart += maxBatchSize {
		batchEnd := batchStart + maxBatchSize
		if batchEnd > len(texts) {
			batchEnd = len(texts)
		}
		batchTexts := texts[batchStart:batchEnd]

		// Build multi-content batch: each text becomes a *genai.Content
		contents := make([]*genai.Content, len(batchTexts))
		for i, text := range batchTexts {
			trimmedText := strings.TrimSpace(text)
			if len(trimmedText) < 3 {
				trimmedText = trimmedText + " code element"
			}
			contents[i] = genai.NewContentFromText(trimmedText, genai.RoleUser)
		}

		var result *genai.EmbedContentResponse
		maxRetries := 5
		for attempt := 0; attempt <= maxRetries; attempt++ {
			result, err = client.Models.EmbedContent(ctx, ges.Model, contents, embedConfig)
			if err == nil {
				break
			}

			// Check for rate limit error (429) and retry with backoff
			errMsg := err.Error()
			if strings.Contains(errMsg, "429") || strings.Contains(errMsg, "RESOURCE_EXHAUSTED") {
				if attempt == maxRetries {
					return nil, fmt.Errorf("Gemini rate limit exceeded after %d retries for batch %d-%d: %w",
						maxRetries, batchStart+1, batchEnd, err)
				}
				// Exponential backoff: 10s, 20s, 40s, 60s, 60s
				backoff := time.Duration(10*(1<<attempt)) * time.Second
				if backoff > 60*time.Second {
					backoff = 60 * time.Second
				}
				log.Printf("⏳ Rate limited, waiting %s before retry (%d/%d)...", backoff, attempt+1, maxRetries)
				select {
				case <-time.After(backoff):
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				continue
			}

			// Non-retryable error
			return nil, fmt.Errorf("Gemini embedding failed for batch %d-%d: %w", batchStart+1, batchEnd, err)
		}

		if result == nil || result.Embeddings == nil {
			return nil, fmt.Errorf("no embeddings returned for batch %d-%d", batchStart+1, batchEnd)
		}

		if len(result.Embeddings) != len(batchTexts) {
			return nil, fmt.Errorf("expected %d embeddings for batch %d-%d, got %d",
				len(batchTexts), batchStart+1, batchEnd, len(result.Embeddings))
		}

		for i, embedding := range result.Embeddings {
			if len(embedding.Values) == 0 {
				return nil, fmt.Errorf("empty embedding values for text %d in batch %d-%d",
					i, batchStart+1, batchEnd)
			}
			embeddingVec := make([]float64, len(embedding.Values))
			for j, v := range embedding.Values {
				embeddingVec[j] = float64(v)
			}
			allEmbeddings = append(allEmbeddings, embeddingVec)
		}

		log.Printf("✓ Batch %d-%d: generated %d embeddings (1 API call)", batchStart+1, batchEnd, len(result.Embeddings))

		// Brief pause between batch calls to respect rate limits (100 RPM)
		if batchEnd < len(texts) {
			select {
			case <-time.After(700 * time.Millisecond):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}

	log.Printf("✓ Generated %d total embeddings using Gemini %s", len(allEmbeddings), ges.Model)
	return allEmbeddings, nil
}

