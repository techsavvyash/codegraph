package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestOpenAICompatCompleter_RequestShapeAndParse(t *testing.T) {
	var gotAuth, gotPath string
	var gotReq chatRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Errorf("bad request body: %v", err)
		}
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"the summary"}}]}`)
	}))
	defer srv.Close()

	c := newOpenAICompatCompleter(EndpointConfig{BaseURL: srv.URL, Model: "test-model"}, "sk-test")
	out, err := c.Complete(context.Background(), "you are a summarizer", "summarize this")
	if err != nil {
		t.Fatal(err)
	}

	if out != "the summary" {
		t.Errorf("content = %q", out)
	}
	if gotPath != "/chat/completions" {
		t.Errorf("path = %q", gotPath)
	}
	if gotAuth != "Bearer sk-test" {
		t.Errorf("auth = %q", gotAuth)
	}
	if gotReq.Model != "test-model" || len(gotReq.Messages) != 2 ||
		gotReq.Messages[0].Role != "system" || gotReq.Messages[1].Role != "user" {
		t.Errorf("request = %+v", gotReq)
	}
}

func TestOpenAICompatCompleter_NoAuthHeaderWithoutKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Errorf("unexpected auth header %q", r.Header.Get("Authorization"))
		}
		fmt.Fprint(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer srv.Close()

	c := newOpenAICompatCompleter(EndpointConfig{BaseURL: srv.URL, Model: "m"}, "")
	if _, err := c.Complete(context.Background(), "", "hi"); err != nil {
		t.Fatal(err)
	}
}

func TestOpenAICompatEmbedder_BatchOrderByIndex(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req embeddingRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("bad request: %v", err)
		}
		if r.URL.Path != "/embeddings" {
			t.Errorf("path = %q", r.URL.Path)
		}
		// Reply out of order — the client must reorder by index.
		fmt.Fprint(w, `{"data":[
			{"index":1,"embedding":[0,1,0]},
			{"index":0,"embedding":[1,0,0]},
			{"index":2,"embedding":[0,0,1]}
		]}`)
	}))
	defer srv.Close()

	e := newOpenAICompatEmbedder(EndpointConfig{BaseURL: srv.URL, Model: "emb", Dimensions: 3}, "k")
	got, err := e.Embed(context.Background(), []string{"a", "b", "c"})
	if err != nil {
		t.Fatal(err)
	}
	if got[0][0] != 1 || got[1][1] != 1 || got[2][2] != 1 {
		t.Errorf("vectors not reordered by index: %v", got)
	}
}

func TestOpenAICompatEmbedder_DimensionMismatchRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":[{"index":0,"embedding":[1,0]}]}`)
	}))
	defer srv.Close()

	e := newOpenAICompatEmbedder(EndpointConfig{BaseURL: srv.URL, Model: "emb", Dimensions: 3}, "k")
	if _, err := e.Embed(context.Background(), []string{"a"}); err == nil {
		t.Fatal("2-dim vector against 3-dim config must error")
	}
}

func TestOpenAICompatRetries429ThenSucceeds(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		fmt.Fprint(w, `{"choices":[{"message":{"content":"after retry"}}]}`)
	}))
	defer srv.Close()

	c := newOpenAICompatCompleter(EndpointConfig{BaseURL: srv.URL, Model: "m"}, "k")
	out, err := c.Complete(context.Background(), "", "hi")
	if err != nil {
		t.Fatal(err)
	}
	if out != "after retry" || atomic.LoadInt32(&calls) != 2 {
		t.Errorf("out=%q calls=%d", out, calls)
	}
}

func TestOpenAICompatClientErrorNotRetried(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":"bad model"}`)
	}))
	defer srv.Close()

	c := newOpenAICompatCompleter(EndpointConfig{BaseURL: srv.URL, Model: "m"}, "k")
	if _, err := c.Complete(context.Background(), "", "hi"); err == nil {
		t.Fatal("400 must be an error")
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Errorf("400 must not be retried, got %d calls", calls)
	}
}

func TestNewProviderSelection(t *testing.T) {
	t.Setenv("LLM_TEST_KEY", "sk-x")

	// Disabled: hard error with a config hint.
	if _, _, err := New(Config{}); err == nil {
		t.Error("empty provider must error")
	}

	// Unknown provider.
	if _, _, err := New(Config{Provider: "qanything"}); err == nil {
		t.Error("unknown provider must error")
	}

	// Named-but-unset key env is a hard error.
	_, _, err := New(Config{Provider: "openai-compat",
		Completion: EndpointConfig{BaseURL: "http://x", Model: "m", APIKeyEnv: "LLM_TEST_KEY_MISSING"}})
	if err == nil {
		t.Error("unset key env must error")
	}

	// Embedding without dimensions is a hard error.
	_, _, err = New(Config{Provider: "openai-compat",
		Embedding: EndpointConfig{BaseURL: "http://x", Model: "e", APIKeyEnv: "LLM_TEST_KEY"}})
	if err == nil {
		t.Error("embedding without dimensions must error")
	}

	// Valid split setup: completion + embedding on different endpoints.
	comp, emb, err := New(Config{Provider: "openai-compat",
		Completion: EndpointConfig{BaseURL: "http://a", Model: "m", APIKeyEnv: "LLM_TEST_KEY"},
		Embedding:  EndpointConfig{BaseURL: "http://b", Model: "e", APIKeyEnv: "LLM_TEST_KEY", Dimensions: 4}})
	if err != nil || comp == nil || emb == nil {
		t.Fatalf("valid config failed: %v", err)
	}
	if emb.Dimensions() != 4 || emb.Model() != "e" {
		t.Errorf("embedder metadata wrong: %d %q", emb.Dimensions(), emb.Model())
	}
	// The completer must self-describe its model too — semlink stamps it into
	// summaryModel provenance (regression: it once fell back to "completer").
	if named, ok := comp.(interface{ Model() string }); !ok || named.Model() != "m" {
		t.Errorf("completer must expose Model() = %q, got ok=%v", "m", ok)
	}
}
