package llm

import (
	"context"
)

// EmbeddingServiceAdapter adapts an LLMProvider to the legacy EmbeddingService interface
// This ensures backward compatibility with existing code that uses EmbeddingService
type EmbeddingServiceAdapter struct {
	provider LLMProvider
}

// NewEmbeddingServiceAdapter creates an adapter from an LLMProvider
func NewEmbeddingServiceAdapter(provider LLMProvider) *EmbeddingServiceAdapter {
	return &EmbeddingServiceAdapter{
		provider: provider,
	}
}

// GenerateEmbedding implements the EmbeddingService interface
func (a *EmbeddingServiceAdapter) GenerateEmbedding(ctx context.Context, text string) ([]float64, error) {
	return a.provider.GenerateEmbedding(ctx, text)
}

// GenerateBatchEmbeddings implements the EmbeddingService interface
func (a *EmbeddingServiceAdapter) GenerateBatchEmbeddings(ctx context.Context, texts []string) ([][]float64, error) {
	return a.provider.GenerateBatchEmbeddings(ctx, texts)
}

// LLMServiceAdapter adapts an LLMProvider to the legacy LLMService interface
// This ensures backward compatibility with existing code that uses LLMService
type LLMServiceAdapter struct {
	provider LLMProvider
}

// NewLLMServiceAdapter creates an adapter from an LLMProvider
func NewLLMServiceAdapter(provider LLMProvider) *LLMServiceAdapter {
	return &LLMServiceAdapter{
		provider: provider,
	}
}

// GenerateText implements the LLMService interface
func (a *LLMServiceAdapter) GenerateText(ctx context.Context, prompt string) (string, error) {
	return a.provider.GenerateText(ctx, prompt)
}

// GenerateTextWithSystemPrompt implements the LLMService interface
func (a *LLMServiceAdapter) GenerateTextWithSystemPrompt(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	return a.provider.GenerateTextWithSystemPrompt(ctx, systemPrompt, userPrompt)
}

// ProviderWrapper wraps both adapters for convenience
type ProviderWrapper struct {
	Provider         LLMProvider
	EmbeddingService *EmbeddingServiceAdapter
	LLMService       *LLMServiceAdapter
}

// WrapProvider creates both adapters from a single provider
func WrapProvider(provider LLMProvider) *ProviderWrapper {
	return &ProviderWrapper{
		Provider:         provider,
		EmbeddingService: NewEmbeddingServiceAdapter(provider),
		LLMService:       NewLLMServiceAdapter(provider),
	}
}
