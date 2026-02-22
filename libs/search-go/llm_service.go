package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// LLMService defines the interface for text generation using LLMs
type LLMService interface {
	GenerateText(ctx context.Context, prompt string) (string, error)
	GenerateTextWithSystemPrompt(ctx context.Context, systemPrompt, userPrompt string) (string, error)
}

// GeminiLLMService implements LLM text generation using Google's Gemini API
type GeminiLLMService struct {
	apiKey     string
	model      string
	httpClient *http.Client
	baseURL    string
}

// NewGeminiLLMService creates a new Gemini LLM service for text generation
func NewGeminiLLMService(apiKey string) *GeminiLLMService {
	return &GeminiLLMService{
		apiKey:  apiKey,
		model:   "gemini-1.5-flash", // Fast model for code analysis
		baseURL: "https://generativelanguage.googleapis.com/v1beta/models",
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

// WithModel sets a custom Gemini model (e.g., gemini-pro, gemini-1.5-flash)
func (g *GeminiLLMService) WithModel(model string) *GeminiLLMService {
	g.model = model
	return g
}

// GenerateText generates text from a prompt using Gemini
func (g *GeminiLLMService) GenerateText(ctx context.Context, prompt string) (string, error) {
	return g.GenerateTextWithSystemPrompt(ctx, "", prompt)
}

// GenerateTextWithSystemPrompt generates text with a system prompt and user prompt
func (g *GeminiLLMService) GenerateTextWithSystemPrompt(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	// Construct the full prompt
	fullPrompt := userPrompt
	if systemPrompt != "" {
		fullPrompt = systemPrompt + "\n\n" + userPrompt
	}

	// Build the request
	requestBody := GeminiTextRequest{
		Contents: []GeminiContent{
			{
				Parts: []GeminiPart{
					{Text: fullPrompt},
				},
			},
		},
		GenerationConfig: GeminiGenerationConfig{
			Temperature:     0.2, // Lower temperature for more deterministic outputs
			TopK:            40,
			TopP:            0.95,
			MaxOutputTokens: 1024,
		},
		SafetySettings: []GeminiSafetySetting{
			{
				Category:  "HARM_CATEGORY_HARASSMENT",
				Threshold: "BLOCK_MEDIUM_AND_ABOVE",
			},
			{
				Category:  "HARM_CATEGORY_HATE_SPEECH",
				Threshold: "BLOCK_MEDIUM_AND_ABOVE",
			},
			{
				Category:  "HARM_CATEGORY_SEXUALLY_EXPLICIT",
				Threshold: "BLOCK_MEDIUM_AND_ABOVE",
			},
			{
				Category:  "HARM_CATEGORY_DANGEROUS_CONTENT",
				Threshold: "BLOCK_MEDIUM_AND_ABOVE",
			},
		},
	}

	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	// Make the API request
	url := fmt.Sprintf("%s/%s:generateContent?key=%s", g.baseURL, g.model, g.apiKey)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := g.httpClient.Do(req)
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
	var response GeminiTextResponse
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

// Gemini API request/response structures

type GeminiTextRequest struct {
	Contents         []GeminiContent          `json:"contents"`
	GenerationConfig GeminiGenerationConfig   `json:"generationConfig"`
	SafetySettings   []GeminiSafetySetting    `json:"safetySettings"`
}

type GeminiContent struct {
	Parts []GeminiPart `json:"parts"`
	Role  string       `json:"role,omitempty"`
}

type GeminiPart struct {
	Text string `json:"text"`
}

type GeminiGenerationConfig struct {
	Temperature     float64 `json:"temperature"`
	TopK            int     `json:"topK"`
	TopP            float64 `json:"topP"`
	MaxOutputTokens int     `json:"maxOutputTokens"`
}

type GeminiSafetySetting struct {
	Category  string `json:"category"`
	Threshold string `json:"threshold"`
}

type GeminiTextResponse struct {
	Candidates []GeminiCandidate `json:"candidates"`
}

type GeminiCandidate struct {
	Content       GeminiContent `json:"content"`
	FinishReason  string        `json:"finishReason"`
	SafetyRatings []struct {
		Category    string `json:"category"`
		Probability string `json:"probability"`
	} `json:"safetyRatings"`
}
