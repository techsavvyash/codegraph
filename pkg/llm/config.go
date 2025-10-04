package llm

import (
	"fmt"
	"os"
)

// Config holds the configuration for an LLM provider
type Config struct {
	// Provider type: "gemini", "litellm", "openai"
	Provider ProviderType

	// API credentials
	APIKey  string
	BaseURL string

	// Model configuration
	TextModel      string
	EmbeddingModel string

	// Generation parameters
	Temperature float64
	MaxTokens   int
	TopK        int
	TopP        float64

	// Additional provider-specific options
	Options map[string]interface{}
}

// Validate checks if the configuration is valid
func (c *Config) Validate() error {
	if !c.Provider.IsValid() {
		return fmt.Errorf("invalid provider type: %s", c.Provider)
	}

	if c.APIKey == "" {
		return fmt.Errorf("API key is required")
	}

	// Provider-specific validation
	switch c.Provider {
	case ProviderGemini:
		// Gemini uses fixed Google API base URL
		if c.TextModel == "" {
			c.TextModel = "gemini-1.5-flash"
		}
		if c.EmbeddingModel == "" {
			c.EmbeddingModel = "gemini-embedding-001"
		}

	case ProviderLiteLLM, ProviderOpenAI:
		// LiteLLM and OpenAI require base URL
		if c.BaseURL == "" {
			return fmt.Errorf("%s provider requires base URL", c.Provider)
		}
		if c.TextModel == "" {
			return fmt.Errorf("text model is required for %s provider", c.Provider)
		}
		if c.EmbeddingModel == "" {
			return fmt.Errorf("embedding model is required for %s provider", c.Provider)
		}
	}

	// Set defaults for generation parameters
	if c.Temperature == 0 {
		c.Temperature = 0.2
	}
	if c.MaxTokens == 0 {
		c.MaxTokens = 1024
	}
	if c.TopK == 0 {
		c.TopK = 40
	}
	if c.TopP == 0 {
		c.TopP = 0.95
	}

	return nil
}

// ConfigFromEnv creates a Config from environment variables
func ConfigFromEnv() Config {
	provider := os.Getenv("LLM_PROVIDER")
	if provider == "" {
		// Backward compatibility: check for GEMINI_API_KEY
		if os.Getenv("GEMINI_API_KEY") != "" {
			provider = "gemini"
		} else {
			provider = "litellm" // Default to LiteLLM
		}
	}

	apiKey := os.Getenv("LLM_API_KEY")
	if apiKey == "" {
		// Backward compatibility
		apiKey = os.Getenv("GEMINI_API_KEY")
	}

	config := Config{
		Provider:       ProviderType(provider),
		APIKey:         apiKey,
		BaseURL:        os.Getenv("LLM_BASE_URL"),
		TextModel:      os.Getenv("LLM_TEXT_MODEL"),
		EmbeddingModel: os.Getenv("LLM_EMBEDDING_MODEL"),
	}

	// Parse optional parameters
	if temp := os.Getenv("LLM_TEMPERATURE"); temp != "" {
		fmt.Sscanf(temp, "%f", &config.Temperature)
	}
	if tokens := os.Getenv("LLM_MAX_TOKENS"); tokens != "" {
		fmt.Sscanf(tokens, "%d", &config.MaxTokens)
	}

	return config
}

// DefaultConfig returns a default configuration for the specified provider
func DefaultConfig(provider ProviderType) Config {
	config := Config{
		Provider:    provider,
		Temperature: 0.2,
		MaxTokens:   1024,
		TopK:        40,
		TopP:        0.95,
		Options:     make(map[string]interface{}),
	}

	switch provider {
	case ProviderGemini:
		config.TextModel = "gemini-1.5-flash"
		config.EmbeddingModel = "gemini-embedding-001"
	case ProviderLiteLLM:
		config.TextModel = "openai/gpt-4"
		config.EmbeddingModel = "openai/text-embedding-3-small"
	case ProviderOpenAI:
		config.TextModel = "gpt-4"
		config.EmbeddingModel = "text-embedding-3-small"
		config.BaseURL = "https://api.openai.com/v1"
	}

	return config
}

// WithAPIKey sets the API key
func (c Config) WithAPIKey(apiKey string) Config {
	c.APIKey = apiKey
	return c
}

// WithBaseURL sets the base URL
func (c Config) WithBaseURL(baseURL string) Config {
	c.BaseURL = baseURL
	return c
}

// WithTextModel sets the text generation model
func (c Config) WithTextModel(model string) Config {
	c.TextModel = model
	return c
}

// WithEmbeddingModel sets the embedding model
func (c Config) WithEmbeddingModel(model string) Config {
	c.EmbeddingModel = model
	return c
}

// WithTemperature sets the temperature parameter
func (c Config) WithTemperature(temp float64) Config {
	c.Temperature = temp
	return c
}

// WithMaxTokens sets the maximum tokens parameter
func (c Config) WithMaxTokens(tokens int) Config {
	c.MaxTokens = tokens
	return c
}
