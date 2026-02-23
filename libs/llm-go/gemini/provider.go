package gemini

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

	"github.com/context-maximiser/code-graph/libs/llm-go"
	"google.golang.org/genai"
)

// Provider implements LLM operations using Google's Gemini API
type Provider struct {
	apiKey     string
	textModel  string
	embModel   string
	config     llm.Config
	httpClient *http.Client
}

// init registers the Gemini provider
func init() {
	llm.NewGeminiProvider = newGeminiProvider
}

// newGeminiProvider creates a new Gemini provider instance
func newGeminiProvider(config llm.Config) (llm.LLMProvider, error) {
	return &Provider{
		apiKey:    config.APIKey,
		textModel: config.TextModel,
		embModel:  config.EmbeddingModel,
		config:    config,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}, nil
}

// Name returns the provider name
func (p *Provider) Name() string {
	return "gemini"
}

// SupportsEmbeddings returns true as Gemini supports embeddings
func (p *Provider) SupportsEmbeddings() bool {
	return true
}

// SupportsTextGeneration returns true as Gemini supports text generation
func (p *Provider) SupportsTextGeneration() bool {
	return true
}

// Close closes any resources held by the provider
func (p *Provider) Close() error {
	p.httpClient.CloseIdleConnections()
	return nil
}

// GenerateText generates text from a prompt using Gemini
func (p *Provider) GenerateText(ctx context.Context, prompt string) (string, error) {
	return p.GenerateTextWithSystemPrompt(ctx, "", prompt)
}

// GenerateTextWithSystemPrompt generates text with a system prompt and user prompt
func (p *Provider) GenerateTextWithSystemPrompt(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	// Construct the full prompt
	fullPrompt := userPrompt
	if systemPrompt != "" {
		fullPrompt = systemPrompt + "\n\n" + userPrompt
	}

	// Build the request
	requestBody := geminiTextRequest{
		Contents: []geminiContent{
			{
				Parts: []geminiPart{
					{Text: fullPrompt},
				},
			},
		},
		GenerationConfig: geminiGenerationConfig{
			Temperature:     p.config.Temperature,
			TopK:            p.config.TopK,
			TopP:            p.config.TopP,
			MaxOutputTokens: p.config.MaxTokens,
		},
		SafetySettings: []geminiSafetySetting{
			{Category: "HARM_CATEGORY_HARASSMENT", Threshold: "BLOCK_MEDIUM_AND_ABOVE"},
			{Category: "HARM_CATEGORY_HATE_SPEECH", Threshold: "BLOCK_MEDIUM_AND_ABOVE"},
			{Category: "HARM_CATEGORY_SEXUALLY_EXPLICIT", Threshold: "BLOCK_MEDIUM_AND_ABOVE"},
			{Category: "HARM_CATEGORY_DANGEROUS_CONTENT", Threshold: "BLOCK_MEDIUM_AND_ABOVE"},
		},
	}

	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	// Make the API request
	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", p.textModel, p.apiKey)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(body))
	}

	// Parse the response
	var response geminiTextResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return "", fmt.Errorf("failed to unmarshal response: %w", err)
	}

	// Extract the generated text
	if len(response.Candidates) == 0 {
		return "", fmt.Errorf("no candidates in response")
	}

	if len(response.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("no parts in response")
	}

	return response.Candidates[0].Content.Parts[0].Text, nil
}

// GenerateEmbedding generates a single embedding for the given text using Gemini
func (p *Provider) GenerateEmbedding(ctx context.Context, text string) ([]float64, error) {
	embeddings, err := p.GenerateBatchEmbeddings(ctx, []string{text})
	if err != nil {
		return nil, err
	}

	if len(embeddings) == 0 {
		return nil, fmt.Errorf("no embeddings returned")
	}

	return embeddings[0], nil
}

// GenerateBatchEmbeddings generates embeddings for multiple texts using Gemini
func (p *Provider) GenerateBatchEmbeddings(ctx context.Context, texts []string) ([][]float64, error) {
	if len(texts) == 0 {
		return nil, fmt.Errorf("no texts provided")
	}

	// Create client with API key
	clientConfig := &genai.ClientConfig{
		Backend: genai.BackendGeminiAPI,
		APIKey:  p.apiKey,
	}
	client, err := genai.NewClient(ctx, clientConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create Gemini client: %w", err)
	}

	var allEmbeddings [][]float64

	// Process texts in batches
	for _, text := range texts {
		// Validate text has minimum content
		trimmedText := strings.TrimSpace(text)
		if len(trimmedText) < 3 {
			trimmedText = trimmedText + " code element"
		}

		// Create content from text
		contents := genai.Text(trimmedText)

		// Generate embedding with semantic similarity task type and 768 dimensions
		embedConfig := &genai.EmbedContentConfig{
			TaskType:             "SEMANTIC_SIMILARITY",
			OutputDimensionality: genai.Ptr(int32(768)), // 768 dimensions for Neo4j compatibility
		}

		result, err := client.Models.EmbedContent(ctx, p.embModel, contents, embedConfig)
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

	log.Printf("✓ Generated %d embeddings using Gemini %s", len(allEmbeddings), p.embModel)
	return allEmbeddings, nil
}

// Helper function for min
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Gemini API request/response structures
type geminiTextRequest struct {
	Contents         []geminiContent        `json:"contents"`
	GenerationConfig geminiGenerationConfig `json:"generationConfig"`
	SafetySettings   []geminiSafetySetting  `json:"safetySettings"`
}

type geminiContent struct {
	Parts []geminiPart `json:"parts"`
	Role  string       `json:"role,omitempty"`
}

type geminiPart struct {
	Text string `json:"text"`
}

type geminiGenerationConfig struct {
	Temperature     float64 `json:"temperature"`
	TopK            int     `json:"topK"`
	TopP            float64 `json:"topP"`
	MaxOutputTokens int     `json:"maxOutputTokens"`
}

type geminiSafetySetting struct {
	Category  string `json:"category"`
	Threshold string `json:"threshold"`
}

type geminiTextResponse struct {
	Candidates []geminiCandidate `json:"candidates"`
}

type geminiCandidate struct {
	Content       geminiContent `json:"content"`
	FinishReason  string        `json:"finishReason"`
	SafetyRatings []struct {
		Category    string `json:"category"`
		Probability string `json:"probability"`
	} `json:"safetyRatings"`
}
