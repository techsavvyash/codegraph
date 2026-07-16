package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// openai-compat adapter: speaks the /chat/completions and /embeddings wire
// shape used (identically or near-identically) by OpenAI, Ollama, vLLM,
// OpenRouter, and Voyage's embeddings API.

const (
	requestTimeout = 120 * time.Second
	maxAttempts    = 3
	retryBaseDelay = 500 * time.Millisecond
)

type openAICompatClient struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

func newHTTPClient(cfg EndpointConfig, apiKey string) *openAICompatClient {
	return &openAICompatClient{
		baseURL: strings.TrimRight(cfg.BaseURL, "/"),
		apiKey:  apiKey,
		http:    &http.Client{Timeout: requestTimeout},
	}
}

// post sends JSON and decodes JSON, retrying 429/5xx with linear backoff.
func (c *openAICompatClient) post(ctx context.Context, path string, payload any, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to encode request: %w", err)
	}

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(retryBaseDelay * time.Duration(attempt-1)):
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		if c.apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+c.apiKey)
		}

		resp, err := c.http.Do(req)
		if err != nil {
			lastErr = err
			continue
		}

		respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			continue
		}

		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("%s: status %d: %s", path, resp.StatusCode, truncate(respBody, 300))
			continue
		}
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("%s: status %d: %s", path, resp.StatusCode, truncate(respBody, 300))
		}
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("%s: failed to decode response: %w", path, err)
		}
		return nil
	}
	return fmt.Errorf("%s: giving up after %d attempts: %w", path, maxAttempts, lastErr)
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}

// --- Completer ---------------------------------------------------------

type openAICompatCompleter struct {
	client *openAICompatClient
	model  string
}

func newOpenAICompatCompleter(cfg EndpointConfig, apiKey string) *openAICompatCompleter {
	return &openAICompatCompleter{client: newHTTPClient(cfg, apiKey), model: cfg.Model}
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
}

func (c *openAICompatCompleter) Complete(ctx context.Context, system, user string) (string, error) {
	var messages []chatMessage
	if system != "" {
		messages = append(messages, chatMessage{Role: "system", Content: system})
	}
	messages = append(messages, chatMessage{Role: "user", Content: user})

	var resp chatResponse
	if err := c.client.post(ctx, "/chat/completions", chatRequest{Model: c.model, Messages: messages}, &resp); err != nil {
		return "", err
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("/chat/completions: response has no choices")
	}
	return resp.Choices[0].Message.Content, nil
}

// --- Embedder ----------------------------------------------------------

type openAICompatEmbedder struct {
	client     *openAICompatClient
	model      string
	dimensions int
}

func newOpenAICompatEmbedder(cfg EndpointConfig, apiKey string) *openAICompatEmbedder {
	return &openAICompatEmbedder{client: newHTTPClient(cfg, apiKey), model: cfg.Model, dimensions: cfg.Dimensions}
}

type embeddingRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type embeddingResponse struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

func (e *openAICompatEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	var resp embeddingResponse
	if err := e.client.post(ctx, "/embeddings", embeddingRequest{Model: e.model, Input: texts}, &resp); err != nil {
		return nil, err
	}
	if len(resp.Data) != len(texts) {
		return nil, fmt.Errorf("/embeddings: got %d vectors for %d inputs", len(resp.Data), len(texts))
	}

	// Order by the server-reported index — the spec does not promise order.
	out := make([][]float32, len(texts))
	for _, d := range resp.Data {
		if d.Index < 0 || d.Index >= len(texts) {
			return nil, fmt.Errorf("/embeddings: index %d out of range for %d inputs", d.Index, len(texts))
		}
		if len(d.Embedding) != e.dimensions {
			return nil, fmt.Errorf("/embeddings: got %d-dim vector, configured for %d (embedding.dimensions mismatch)", len(d.Embedding), e.dimensions)
		}
		out[d.Index] = d.Embedding
	}
	for i, v := range out {
		if v == nil {
			return nil, fmt.Errorf("/embeddings: missing vector for input %d", i)
		}
	}
	return out, nil
}

func (e *openAICompatEmbedder) Dimensions() int { return e.dimensions }
func (e *openAICompatEmbedder) Model() string   { return e.model }
