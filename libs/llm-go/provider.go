package llm

import (
	"context"
	"fmt"
)

// LLMProvider defines a unified interface for all LLM operations
// This interface abstracts away provider-specific implementations (Gemini, LiteLLM, OpenAI, etc.)
type LLMProvider interface {
	// Text generation methods
	GenerateText(ctx context.Context, prompt string) (string, error)
	GenerateTextWithSystemPrompt(ctx context.Context, systemPrompt, userPrompt string) (string, error)

	// Embedding generation methods
	GenerateEmbedding(ctx context.Context, text string) ([]float64, error)
	GenerateBatchEmbeddings(ctx context.Context, texts []string) ([][]float64, error)

	// Provider metadata
	Name() string
	SupportsEmbeddings() bool
	SupportsTextGeneration() bool
	Close() error
}

// NewProvider creates a new LLM provider based on the configuration
func NewProvider(config Config) (LLMProvider, error) {
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid provider config: %w", err)
	}

	switch config.Provider {
	case ProviderGemini:
		return NewGeminiProvider(config)
	case ProviderLiteLLM:
		return NewLiteLLMProvider(config)
	case ProviderOpenAI:
		return NewOpenAIProvider(config)
	default:
		return nil, fmt.Errorf("unknown provider type: %s (supported: gemini, litellm, openai)", config.Provider)
	}
}

// Provider factory functions (implemented in subpackages)
var (
	NewGeminiProvider   func(Config) (LLMProvider, error)
	NewLiteLLMProvider  func(Config) (LLMProvider, error)
	NewOpenAIProvider   func(Config) (LLMProvider, error)
)

// ProviderType represents supported LLM providers
type ProviderType string

const (
	ProviderGemini   ProviderType = "gemini"
	ProviderLiteLLM  ProviderType = "litellm"
	ProviderOpenAI   ProviderType = "openai"
)

// IsValid checks if the provider type is supported
func (p ProviderType) IsValid() bool {
	switch p {
	case ProviderGemini, ProviderLiteLLM, ProviderOpenAI:
		return true
	default:
		return false
	}
}

// String returns the string representation of the provider type
func (p ProviderType) String() string {
	return string(p)
}
