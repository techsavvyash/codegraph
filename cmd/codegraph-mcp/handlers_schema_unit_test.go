package main

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestSchemaCacheHitServesWithoutDB exercises the REAL handler's cache-hit
// path: with a warm cache inside TTL, handleSchemaTool must answer from the
// cache without touching the database. The nil client makes that property
// enforceable — any DB access would panic and fail the test.
func TestSchemaCacheHitServesWithoutDB(t *testing.T) {
	base := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)

	server := &CodeGraphMCPServer{
		client: nil, // cache hits must not touch the DB
		schemaCache: map[string]any{
			"nodes":       []any{},
			"computed_at": "cached-sentinel",
		},
		schemaCacheTime: base,
		schemaCacheTTL:  300 * time.Second,
		now:             func() time.Time { return base.Add(2 * time.Second) },
	}

	resp := server.handleSchemaTool(context.Background(), map[string]interface{}{
		"include_examples": false,
	})
	require.False(t, resp.IsError, "cache hit must succeed")
	require.NotEmpty(t, resp.Content)
	require.Contains(t, resp.Content[0].Text, "cached-sentinel",
		"response must be served from the cached payload")
}

// TestSchemaCacheExamplesPerRequest verifies the cache stores only the base
// payload: a caller with include_examples=true gets examples appended even
// when served from a cache warmed by an examples-less call, and vice versa.
func TestSchemaCacheExamplesPerRequest(t *testing.T) {
	base := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)

	server := &CodeGraphMCPServer{
		client:          nil,
		schemaCache:     map[string]any{"nodes": []any{}},
		schemaCacheTime: base,
		schemaCacheTTL:  300 * time.Second,
		now:             func() time.Time { return base.Add(time.Second) },
	}

	withExamples := server.handleSchemaTool(context.Background(), map[string]interface{}{
		"include_examples": true,
	})
	require.False(t, withExamples.IsError)
	require.Contains(t, withExamples.Content[0].Text, `"examples"`,
		"include_examples=true must add examples even on a cache hit")

	withoutExamples := server.handleSchemaTool(context.Background(), map[string]interface{}{
		"include_examples": false,
	})
	require.False(t, withoutExamples.IsError)
	require.NotContains(t, withoutExamples.Content[0].Text, `"examples"`,
		"include_examples=false must not leak examples from a prior caller")
}

// TestSchemaCacheExpiryAndRefreshRecompute verifies both recompute triggers
// through the real handler: TTL expiry and refresh=true. Recompute with a
// nil client panics — the deferred recover converts that into the positive
// signal that the handler LEFT the cache path.
func TestSchemaCacheExpiryAndRefreshRecompute(t *testing.T) {
	base := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)

	attemptedRecompute := func(nowFn func() time.Time, args map[string]interface{}) (recomputed bool) {
		server := &CodeGraphMCPServer{
			client:          nil, // recompute path panics on DB access
			schemaCache:     map[string]any{"nodes": []any{}},
			schemaCacheTime: base,
			schemaCacheTTL:  300 * time.Second,
			now:             nowFn,
		}
		defer func() {
			if r := recover(); r != nil {
				recomputed = true
			}
		}()
		_ = server.handleSchemaTool(context.Background(), args)
		// No panic: the handler served from cache.
		return false
	}

	require.False(t,
		attemptedRecompute(func() time.Time { return base.Add(2 * time.Second) }, map[string]interface{}{}),
		"within TTL and no refresh: must serve from cache")

	require.True(t,
		attemptedRecompute(func() time.Time { return base.Add(301 * time.Second) }, map[string]interface{}{}),
		"past TTL: must attempt recompute")

	require.True(t,
		attemptedRecompute(func() time.Time { return base.Add(2 * time.Second) }, map[string]interface{}{"refresh": true}),
		"refresh=true within TTL: must attempt recompute")
}
