package litellm

import (
	"context"
	"fmt"
	"log"

	"github.com/context-maximiser/code-graph/libs/llm-go"
	"github.com/sashabaranov/go-openai"
)

// Provider implements LLM operations using LiteLLM proxy
// LiteLLM provides a unified interface to 100+ LLM providers via OpenAI-compatible API
type Provider struct {
	client    *openai.Client
	config    llm.Config
	textModel string
	embModel  string
}

// init registers the LiteLLM provider
func init() {
	llm.NewLiteLLMProvider = newLiteLLMProvider
}

// newLiteLLMProvider creates a new LiteLLM provider instance
func newLiteLLMProvider(config llm.Config) (llm.LLMProvider, error) {
	if config.BaseURL == "" {
		return nil, fmt.Errorf("LiteLLM provider requires base URL (LiteLLM proxy server URL)")
	}

	clientConfig := openai.DefaultConfig(config.APIKey)
	clientConfig.BaseURL = config.BaseURL

	client := openai.NewClientWithConfig(clientConfig)

	log.Printf("🔗 LiteLLM provider initialized: %s", config.BaseURL)
	log.Printf("   Text Model: %s", config.TextModel)
	log.Printf("   Embedding Model: %s", config.EmbeddingModel)

	return &Provider{
		client:    client,
		config:    config,
		textModel: config.TextModel,
		embModel:  config.EmbeddingModel,
	}, nil
}

// Name returns the provider name
func (p *Provider) Name() string {
	return "litellm"
}

// SupportsEmbeddings returns true as LiteLLM supports embeddings
func (p *Provider) SupportsEmbeddings() bool {
	return true
}

// SupportsTextGeneration returns true as LiteLLM supports text generation
func (p *Provider) SupportsTextGeneration() bool {
	return true
}

// Close closes any resources held by the provider
func (p *Provider) Close() error {
	// OpenAI client doesn't require explicit cleanup
	return nil
}

// GenerateText generates text from a prompt using LiteLLM
func (p *Provider) GenerateText(ctx context.Context, prompt string) (string, error) {
	return p.GenerateTextWithSystemPrompt(ctx, "", prompt)
}

// GenerateTextWithSystemPrompt generates text with a system prompt and user prompt
// Note: LiteLLM uses the OpenAI-compatible chat completion format
func (p *Provider) GenerateTextWithSystemPrompt(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	messages := []openai.ChatCompletionMessage{
		{
			Role:    openai.ChatMessageRoleUser,
			Content: userPrompt,
		},
	}

	// Add system prompt if provided
	if systemPrompt != "" {
		messages = []openai.ChatCompletionMessage{
			{
				Role:    openai.ChatMessageRoleSystem,
				Content: systemPrompt,
			},
			{
				Role:    openai.ChatMessageRoleUser,
				Content: userPrompt,
			},
		}
	}

	// LiteLLM expects model names with provider prefix (e.g., "openai/gpt-4", "anthropic/claude-3")
	req := openai.ChatCompletionRequest{
		Model:       p.textModel,
		Messages:    messages,
		Temperature: float32(p.config.Temperature),
		MaxTokens:   p.config.MaxTokens,
		TopP:        float32(p.config.TopP),
	}

	resp, err := p.client.CreateChatCompletion(ctx, req)
	if err != nil {
		return "", fmt.Errorf("LiteLLM chat completion failed: %w", err)
	}

	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("no choices in response")
	}

	return resp.Choices[0].Message.Content, nil
}

// GenerateEmbedding generates a single embedding for the given text
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

// GenerateBatchEmbeddings generates embeddings for multiple texts
// Note: LiteLLM model names should include provider prefix (e.g., "openai/text-embedding-3-small")
func (p *Provider) GenerateBatchEmbeddings(ctx context.Context, texts []string) ([][]float64, error) {
	if len(texts) == 0 {
		return nil, fmt.Errorf("no texts provided")
	}

	req := openai.EmbeddingRequest{
		Model: openai.EmbeddingModel(p.embModel),
		Input: texts,
	}

	resp, err := p.client.CreateEmbeddings(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("LiteLLM embedding failed: %w", err)
	}

	if len(resp.Data) != len(texts) {
		return nil, fmt.Errorf("expected %d embeddings, got %d", len(texts), len(resp.Data))
	}

	// Convert float32 embeddings to float64
	var embeddings [][]float64
	for _, data := range resp.Data {
		embedding := make([]float64, len(data.Embedding))
		for i, v := range data.Embedding {
			embedding[i] = float64(v)
		}
		embeddings = append(embeddings, embedding)
	}

	log.Printf("✓ Generated %d embeddings via LiteLLM (%s)", len(embeddings), p.embModel)
	return embeddings, nil
}
