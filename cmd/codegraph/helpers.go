package main

import (
	neo4j "github.com/context-maximiser/code-graph/internal/graph"
	"github.com/context-maximiser/code-graph/internal/ingest/semlink"
	"github.com/context-maximiser/code-graph/internal/llm"
	"github.com/spf13/viper"
)

// createNeo4jClient creates a new Neo4j client using configuration
func createNeo4jClient() (*neo4j.Client, error) {
	config := neo4j.Config{
		URI:      viper.GetString("neo4j.uri"),
		Username: viper.GetString("neo4j.username"),
		Password: viper.GetString("neo4j.password"),
		Database: viper.GetString("neo4j.database"),
	}

	return neo4j.NewClient(config)
}

// llmConfigFromViper maps the ~/.codegraph.yaml llm section (RFC-011 §5.3):
//
//	llm:
//	  provider: openai-compat
//	  completion: { base_url: ..., model: ..., api_key_env: CODEGRAPH_LLM_KEY }
//	  embedding:  { base_url: ..., model: ..., dimensions: 1536, api_key_env: CODEGRAPH_EMBED_KEY }
func llmConfigFromViper() llm.Config {
	return llm.Config{
		Provider: viper.GetString("llm.provider"),
		Completion: llm.EndpointConfig{
			BaseURL:   viper.GetString("llm.completion.base_url"),
			Model:     viper.GetString("llm.completion.model"),
			APIKeyEnv: viper.GetString("llm.completion.api_key_env"),
			Extra:     viper.GetStringMap("llm.completion.extra"),
		},
		Embedding: llm.EndpointConfig{
			BaseURL:    viper.GetString("llm.embedding.base_url"),
			Model:      viper.GetString("llm.embedding.model"),
			APIKeyEnv:  viper.GetString("llm.embedding.api_key_env"),
			Dimensions: viper.GetInt("llm.embedding.dimensions"),
			Extra:      viper.GetStringMap("llm.embedding.extra"),
		},
	}
}

// semlinkOptionsFromViper maps the semlink section. Unset keys keep the
// package defaults (threshold 0.78, top_k 10, judge on, max_llm_calls 2000,
// concurrency 8).
func semlinkOptionsFromViper() semlink.Options {
	opts := semlink.Options{
		SimilarityThreshold: viper.GetFloat64("semlink.similarity_threshold"),
		TopK:                viper.GetInt("semlink.top_k"),
		MaxLLMCalls:         viper.GetInt("semlink.max_llm_calls"),
		Concurrency:         viper.GetInt("semlink.concurrency"),
	}
	if viper.IsSet("semlink.judge") {
		judge := viper.GetBool("semlink.judge")
		opts.Judge = &judge
	}
	return opts
}
