package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	neo4j "github.com/context-maximiser/code-graph/internal/graph"
	"github.com/stretchr/testify/require"
)

// setupDocsTestDB seeds a Document with two chunks, a Function, and a
// provenanced MENTIONS edge (RFC-011) under scopeId "itest-docs-mcp".
func setupDocsTestDB(t *testing.T) (*CodeGraphMCPServer, map[string]string, func()) {
	t.Helper()
	client, err := neo4j.NewClient(neo4j.Config{
		URI:      getEnvOrDefault("NEO4J_URI", "bolt://localhost:7687"),
		Username: getEnvOrDefault("NEO4J_USERNAME", "neo4j"),
		Password: getEnvOrDefault("NEO4J_PASSWORD", "password123"),
		Database: getEnvOrDefault("NEO4J_DATABASE", "neo4j"),
	})
	if err != nil {
		t.Skipf("Neo4j not available: %v", err)
		return nil, nil, nil
	}
	ctx := context.Background()
	if _, err := client.ExecuteQuery(ctx, "RETURN 1", nil); err != nil {
		t.Skipf("Neo4j not responding: %v", err)
		return nil, nil, nil
	}

	cleanup := func() {
		_, _ = client.ExecuteQuery(ctx, `MATCH (n) WHERE n.scopeId = "itest-docs-mcp" DETACH DELETE n`, nil)
	}
	cleanup()

	records, err := client.ExecuteQuery(ctx, `
		CREATE (d:Document {nodeKey: 'doc:docs-mcp/guide.md', scopeId: 'itest-docs-mcp',
			title: 'MCP Docs Guide', sourceUrl: 'docs-mcp/guide.md', serviceName: 'itest-docs-mcp-svc',
			scopedKey: 'doc:docs-mcp/guide.md|itest-docs-mcp'})
		CREATE (c0:DocumentChunk {nodeKey: 'chunk:doc:docs-mcp/guide.md#0', scopeId: 'itest-docs-mcp',
			documentKey: 'doc:docs-mcp/guide.md', chunkIndex: 0, headingPath: 'MCP Docs Guide',
			content: '# MCP Docs Guide\n\nIntro text.', serviceName: 'itest-docs-mcp-svc',
			scopedKey: 'chunk:doc:docs-mcp/guide.md#0|itest-docs-mcp'})
		CREATE (c1:DocumentChunk {nodeKey: 'chunk:doc:docs-mcp/guide.md#1', scopeId: 'itest-docs-mcp',
			documentKey: 'doc:docs-mcp/guide.md', chunkIndex: 1, headingPath: 'MCP Docs Guide > Usage',
			content: 'Call DocsMcpFn to start.', serviceName: 'itest-docs-mcp-svc',
			scopedKey: 'chunk:doc:docs-mcp/guide.md#1|itest-docs-mcp'})
		CREATE (f:Function {nodeKey: 'func:itest-docs-mcp-svc:x.go#DocsMcpFn()', scopeId: 'itest-docs-mcp',
			name: 'DocsMcpFn', serviceName: 'itest-docs-mcp-svc',
			scopedKey: 'func:itest-docs-mcp-svc:x.go#DocsMcpFn()|itest-docs-mcp'})
		CREATE (d)-[:HAS_CHUNK {scopeId: 'itest-docs-mcp'}]->(c0)
		CREATE (d)-[:HAS_CHUNK {scopeId: 'itest-docs-mcp'}]->(c1)
		CREATE (c1)-[:MENTIONS {scopeId: 'itest-docs-mcp', strategy: 'docmine/codespan',
			confidence: 0.9, reasons: ['explicit-identifier'], createdAt: '2026-07-17T00:00:00Z',
			evidenceRefs: ['lit:DocsMcpFn@5'], scope: 'main'}]->(f)
		RETURN elementId(d) AS doc, elementId(c0) AS c0, elementId(c1) AS c1, elementId(f) AS fn
	`, nil)
	require.NoError(t, err)
	require.Len(t, records, 1)

	m := records[0].AsMap()
	ids := map[string]string{
		"doc": m["doc"].(string), "c0": m["c0"].(string),
		"c1": m["c1"].(string), "fn": m["fn"].(string),
	}

	server := &CodeGraphMCPServer{client: client, now: nil}
	return server, ids, func() {
		cleanup()
		client.Close(ctx)
	}
}

// TestSourceDocumentChunk verifies source returns chunk content from the
// graph without any filesystem access.
func TestSourceDocumentChunk(t *testing.T) {
	server, ids, cleanup := setupDocsTestDB(t)
	defer cleanup()

	resp := server.handleSourceToolV2(context.Background(), map[string]interface{}{"node_id": ids["c1"]})
	require.False(t, resp.IsError, "chunk source failed: %+v", resp)
	text := resp.Content[0].Text
	require.Contains(t, text, "Call DocsMcpFn to start.")
	require.Contains(t, text, "section: MCP Docs Guide > Usage")
	require.Contains(t, text, "service: `itest-docs-mcp-svc`")
}

// TestSourceDocumentReassemblesChunks verifies a Document source concatenates
// its chunks in order.
func TestSourceDocumentReassemblesChunks(t *testing.T) {
	server, ids, cleanup := setupDocsTestDB(t)
	defer cleanup()

	resp := server.handleSourceToolV2(context.Background(), map[string]interface{}{"node_id": ids["doc"]})
	require.False(t, resp.IsError, "document source failed: %+v", resp)
	text := resp.Content[0].Text
	require.Contains(t, text, "MCP Docs Guide")
	require.Contains(t, text, "docs-mcp/guide.md")
	intro := strings.Index(text, "Intro text.")
	usage := strings.Index(text, "Call DocsMcpFn")
	require.Greater(t, intro, 0, "chunk 0 content present")
	require.Greater(t, usage, intro, "chunks concatenated in chunkIndex order")
}

// TestExpandRendersEdgeProvenance verifies MENTIONS edges surface strategy
// and confidence in both JSON and mermaid output (RFC-011 I4).
func TestExpandRendersEdgeProvenance(t *testing.T) {
	server, ids, cleanup := setupDocsTestDB(t)
	defer cleanup()

	args := map[string]interface{}{
		"node_id":   ids["c1"],
		"rel_types": []interface{}{"MENTIONS"},
		"direction": "out",
	}

	resp := server.handleExpandTool(context.Background(), args)
	require.False(t, resp.IsError, "expand failed: %+v", resp)

	var out struct {
		Edges []expandEdge `json:"edges"`
	}
	require.NoError(t, json.Unmarshal([]byte(resp.Content[0].Text), &out))
	require.Len(t, out.Edges, 1)
	require.Equal(t, "MENTIONS", out.Edges[0].Type)
	require.Equal(t, "docmine/codespan", out.Edges[0].Strategy)
	require.InDelta(t, 0.9, out.Edges[0].Confidence, 1e-9)

	// Mermaid output annotates the inferred edge.
	args["format"] = "mermaid"
	resp = server.handleExpandTool(context.Background(), args)
	require.False(t, resp.IsError)
	require.Contains(t, resp.Content[0].Text, "MENTIONS (docmine/codespan 0.90)")

	// A structural edge (HAS_CHUNK) stays unannotated.
	args2 := map[string]interface{}{
		"node_id":   ids["doc"],
		"rel_types": []interface{}{"HAS_CHUNK"},
		"direction": "out",
	}
	resp = server.handleExpandTool(context.Background(), args2)
	require.False(t, resp.IsError)
	var out2 struct {
		Edges []expandEdge `json:"edges"`
	}
	require.NoError(t, json.Unmarshal([]byte(resp.Content[0].Text), &out2))
	require.Len(t, out2.Edges, 2)
	for _, e := range out2.Edges {
		require.Empty(t, e.Strategy, "structural edges must not carry provenance annotations")
		require.Zero(t, e.Confidence)
	}

	// Doc nodes have no name/path property: text rendering must fall back to
	// title (Document) / headingPath (DocumentChunk), not show blanks.
	args2["format"] = "text"
	resp = server.handleExpandTool(context.Background(), args2)
	require.False(t, resp.IsError)
	require.Contains(t, resp.Content[0].Text, "MCP Docs Guide > Usage",
		"DocumentChunk display name must fall back to headingPath")
}
