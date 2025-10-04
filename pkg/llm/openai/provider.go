package openai

import (
	"context"
	"fmt"

	"github.com/context-maximiser/code-graph/pkg/llm"
	"github.com/sashabaranov/go-openai"
)

// Provider implements LLM operations using OpenAI-compatible APIs
// This provider works with OpenAI, Azure OpenAI, and other compatible services
type Provider struct {
	client    *openai.Client
	config    llm.Config
	textModel string
	embModel  string
}

// init registers the OpenAI provider
func init() {
	llm.NewOpenAIProvider = newOpenAIProvider
}

// newOpenAIProvider creates a new OpenAI-compatible provider instance
func newOpenAIProvider(config llm.Config) (llm.LLMProvider, error) {
	clientConfig := openai.DefaultConfig(config.APIKey)

	// Set custom base URL if provided (for Azure, local deployments, etc.)
	if config.BaseURL != "" {
		clientConfig.BaseURL = config.BaseURL
	}

	client := openai.NewClientWithConfig(clientConfig)

	return &Provider{
		client:    client,
		config:    config,
		textModel: config.TextModel,
		embModel:  config.EmbeddingModel,
	}, nil
}

// Name returns the provider name
func (p *Provider) Name() string {
	return "openai"
}

// SupportsEmbeddings returns true as OpenAI supports embeddings
func (p *Provider) SupportsEmbeddings() bool {
	return true
}

// SupportsTextGeneration returns true as OpenAI supports text generation
func (p *Provider) SupportsTextGeneration() bool {
	return true
}

// Close closes any resources held by the provider
func (p *Provider) Close() error {
	// OpenAI client doesn't require explicit cleanup
	return nil
}

// GenerateText generates text from a prompt using OpenAI chat completion
func (p *Provider) GenerateText(ctx context.Context, prompt string) (string, error) {
	return p.GenerateTextWithSystemPrompt(ctx, "", prompt)
}

// GenerateTextWithSystemPrompt generates text with a system prompt and user prompt
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

	req := openai.ChatCompletionRequest{
		Model:       p.textModel,
		Messages:    messages,
		Temperature: float32(p.config.Temperature),
		MaxTokens:   p.config.MaxTokens,
		TopP:        float32(p.config.TopP),
	}

	resp, err := p.client.CreateChatCompletion(ctx, req)
	if err != nil {
		return "", fmt.Errorf("OpenAI chat completion failed: %w", err)
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
		return nil, fmt.Errorf("OpenAI embedding failed: %w", err)
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

	return embeddings, nil
}
