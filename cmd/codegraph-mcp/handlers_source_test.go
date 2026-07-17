package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	neo4j "github.com/context-maximiser/code-graph/internal/graph"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sourceTestFile is written to a temp dir; byte offsets are computed from it,
// never hardcoded. The target arrow shares its line with other code so that
// byte-exact extraction is distinguishable from whole-line extraction.
const sourceTestFile = `export const other = 1;
export const srcMcpArrow = (x: number): number => x + 1; export const trailing = 2;
export interface SrcMcpApi {
  srcMcpDecl(payload: string): void;
}
`

// setupSourceTestDB seeds two Function nodes under scopeId "itest-source-mcp"
// pointing (by absolute path) at a real temp file:
//   - srcMcpArrow: rangeSource=treesitter with exact byte anchors covering
//     the declarator-widened arrow only (mid-line span);
//   - srcMcpDecl: rangeSource=scip-declaration whose byte props are just the
//     identifier — the trap the extraction must NOT fall into.
func setupSourceTestDB(t *testing.T) (*CodeGraphMCPServer, map[string]string, func()) {
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

	_, _ = client.ExecuteQuery(ctx, `MATCH (n) WHERE n.scopeId = "itest-source-mcp" DETACH DELETE n`, nil)

	dir := t.TempDir()
	filePath := filepath.Join(dir, "src.ts")
	require.NoError(t, os.WriteFile(filePath, []byte(sourceTestFile), 0o644))

	arrowSpan := "srcMcpArrow = (x: number): number => x + 1"
	arrowStart := strings.Index(sourceTestFile, arrowSpan)
	require.GreaterOrEqual(t, arrowStart, 0, "fixture must contain the arrow span")
	declIdentStart := strings.Index(sourceTestFile, "srcMcpDecl")
	require.GreaterOrEqual(t, declIdentStart, 0, "fixture must contain the decl identifier")

	createQuery := fmt.Sprintf(`
		CREATE (a:Function {
			name: 'srcMcpArrow', scopeId: 'itest-source-mcp',
			nodeKey: 'itest-source-mcp/srcMcpArrow',
			serviceName: 'source-mcp-svc', filePath: $filePath,
			startLine: 2, endLine: 2,
			startByte: %d, endByte: %d,
			rangeSource: 'treesitter'
		})
		CREATE (b:Method {
			name: 'srcMcpDecl', scopeId: 'itest-source-mcp',
			nodeKey: 'itest-source-mcp/srcMcpDecl',
			serviceName: 'source-mcp-svc', filePath: $filePath,
			startLine: 4, endLine: 4,
			startByte: %d, endByte: %d,
			rangeSource: 'scip-declaration'
		})
	`, arrowStart, arrowStart+len(arrowSpan), declIdentStart, declIdentStart+len("srcMcpDecl"))
	_, err = client.ExecuteQuery(ctx, createQuery, map[string]any{"filePath": filePath})
	require.NoError(t, err, "failed to seed source test nodes")

	records, err := client.ExecuteQuery(ctx, `
		MATCH (n) WHERE n.scopeId = 'itest-source-mcp'
		RETURN n.name AS name, elementId(n) AS node_id
	`, nil)
	require.NoError(t, err)
	require.Len(t, records, 2)
	nodeIDs := make(map[string]string)
	for _, rec := range records {
		m := rec.AsMap()
		nodeIDs[getStringFromRecord(m, "name")] = getStringFromRecord(m, "node_id")
	}

	server := &CodeGraphMCPServer{
		client:       client,
		queryBuilder: neo4j.NewQueryBuilder(client),
	}
	cleanup := func() {
		_, _ = client.ExecuteQuery(context.Background(), `MATCH (n) WHERE n.scopeId = "itest-source-mcp" DETACH DELETE n`, nil)
		client.Close(context.Background())
	}
	return server, nodeIDs, cleanup
}

// TestSourceByteExactExtraction locks the RFC-010 switch: a treesitter-ranged
// node is extracted by its byte span, not by whole lines — the arrow's line
// also holds `export const other`-style neighbors that must NOT appear.
func TestSourceByteExactExtraction(t *testing.T) {
	server, nodeIDs, cleanup := setupSourceTestDB(t)
	if server == nil {
		t.Skip("Neo4j not available")
	}
	defer cleanup()

	response := server.handleSourceToolV2(context.Background(), map[string]interface{}{
		"node_id": nodeIDs["srcMcpArrow"],
	})
	require.False(t, response.IsError, "source should succeed: %+v", response.Content)
	text := response.Content[0].Text

	assert.Contains(t, text, "srcMcpArrow = (x: number): number => x + 1",
		"must contain the exact byte span")
	assert.NotContains(t, text, "export const trailing",
		"byte-exact extraction must exclude the rest of the shared line")
	assert.NotContains(t, text, "export const other",
		"byte-exact extraction must exclude preceding lines")
	assert.Contains(t, text, "range: treesitter", "provenance must be reported")
}

// TestSourceDeclarationStubFallsBackToLines locks the fallback: a
// scip-declaration stub's byte props are only the identifier, so extraction
// must use the declaration LINE — returning just "srcMcpDecl" would be a
// regression to identifier-only output.
func TestSourceDeclarationStubFallsBackToLines(t *testing.T) {
	server, nodeIDs, cleanup := setupSourceTestDB(t)
	if server == nil {
		t.Skip("Neo4j not available")
	}
	defer cleanup()

	response := server.handleSourceToolV2(context.Background(), map[string]interface{}{
		"node_id": nodeIDs["srcMcpDecl"],
	})
	require.False(t, response.IsError, "source should succeed: %+v", response.Content)
	text := response.Content[0].Text

	assert.Contains(t, text, "srcMcpDecl(payload: string): void;",
		"stub must return the whole declaration line, not just the identifier bytes")
	assert.Contains(t, text, "declaration line only",
		"stub output must warn that the body span is unavailable")
	assert.NotContains(t, text, "srcMcpArrow", "must not bleed into other lines")
}
