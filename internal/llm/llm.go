// Package llm defines the provider contract for RFC-011 Layer S (semantic
// linking): a Completer for summaries/judging and an Embedder for vectors.
// The vendor is a configuration choice, not a design commitment — one
// openai-compat HTTP adapter covers OpenAI, Ollama, vLLM, OpenRouter, and
// Voyage-style embedding APIs. Deterministic test doubles live in the
// test-only llmtest subpackage; they are not a selectable provider.
package llm

import (
	"context"
	"fmt"
	"os"
)

// Completer produces a completion for a (system, user) prompt pair.
type Completer interface {
	Complete(ctx context.Context, system, user string) (string, error)
}

// Embedder embeds a batch of texts into vectors of a fixed dimension.
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	// Dimensions is the vector width; Neo4j vector indexes are created with
	// this value and must be dropped/recreated if it changes.
	Dimensions() int
	// Model identifies the embedding model; stamped into embeddingModel /
	// summaryModel node properties so mixed states are detectable.
	Model() string
}

// EndpointConfig configures one API endpoint (completion or embedding may
// point at different vendors).
type EndpointConfig struct {
	BaseURL    string // e.g. "https://api.openai.com/v1" or "http://localhost:11434/v1"
	Model      string
	APIKeyEnv  string // name of the env var holding the key ("" = no auth, e.g. local Ollama)
	Dimensions int    // embeddings only
	// Extra holds vendor-specific request fields merged verbatim into every
	// request body — e.g. {"reasoning_effort": "minimal"} cuts gpt-5-family
	// latency ~2x for summary-sized outputs. Core fields (model, messages,
	// input) cannot be overridden.
	Extra map[string]any
}

// Config selects and configures the provider.
type Config struct {
	Provider   string // "openai-compat" | "" (disabled)
	Completion EndpointConfig
	Embedding  EndpointConfig
}

// Enabled reports whether any provider is configured.
func (c Config) Enabled() bool { return c.Provider != "" }

// New builds the configured provider pair. Either return value may be nil
// when its endpoint is not configured (e.g. embedding-only setups); callers
// that need both must check.
func New(cfg Config) (Completer, Embedder, error) {
	switch cfg.Provider {
	case "":
		return nil, nil, fmt.Errorf("no LLM provider configured (set llm.provider in ~/.codegraph.yaml)")
	case "openai-compat":
		var completer Completer
		var embedder Embedder
		if cfg.Completion.Model != "" {
			key, err := resolveKey(cfg.Completion.APIKeyEnv)
			if err != nil {
				return nil, nil, fmt.Errorf("completion endpoint: %w", err)
			}
			completer = newOpenAICompatCompleter(cfg.Completion, key)
		}
		if cfg.Embedding.Model != "" {
			if cfg.Embedding.Dimensions <= 0 {
				return nil, nil, fmt.Errorf("embedding endpoint requires positive dimensions, got %d", cfg.Embedding.Dimensions)
			}
			key, err := resolveKey(cfg.Embedding.APIKeyEnv)
			if err != nil {
				return nil, nil, fmt.Errorf("embedding endpoint: %w", err)
			}
			embedder = newOpenAICompatEmbedder(cfg.Embedding, key)
		}
		if completer == nil && embedder == nil {
			return nil, nil, fmt.Errorf("openai-compat provider configured without completion or embedding endpoints")
		}
		return completer, embedder, nil
	default:
		return nil, nil, fmt.Errorf("unknown LLM provider %q (supported: openai-compat)", cfg.Provider)
	}
}

// resolveKey reads the API key from the named env var. An empty APIKeyEnv
// means the endpoint is unauthenticated (local Ollama/vLLM); a named-but-
// unset var is a hard error — silently sending no key produces confusing 401s.
func resolveKey(envName string) (string, error) {
	if envName == "" {
		return "", nil
	}
	key := os.Getenv(envName)
	if key == "" {
		return "", fmt.Errorf("API key env var %s is not set", envName)
	}
	return key, nil
}
