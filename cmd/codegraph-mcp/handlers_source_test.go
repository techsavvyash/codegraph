package main

import (
	"context"
	"encoding/json"
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

// TestSourceJSONFormat locks the RFC-012 R2 format=json contract: the payload
// must match web/studio/src/lib/types/graph.ts SourceResponse field-for-field
// (snake_case keys), with the exact extracted body as `source`.
func TestSourceJSONFormat(t *testing.T) {
	server, nodeIDs, cleanup := setupSourceTestDB(t)
	if server == nil {
		t.Skip("Neo4j not available")
	}
	defer cleanup()

	response := server.handleSourceToolV2(context.Background(), map[string]interface{}{
		"node_id": nodeIDs["srcMcpArrow"],
		"format":  "json",
	})
	require.False(t, response.IsError, "source should succeed: %+v", response.Content)
	require.Len(t, response.Content, 1)

	var got sourceResponse
	require.NoError(t, json.Unmarshal([]byte(response.Content[0].Text), &got))

	assert.Equal(t, "code", got.Kind)
	assert.Equal(t, "srcMcpArrow", got.Name)
	assert.Equal(t, "source-mcp-svc", got.Service)
	assert.Equal(t, "treesitter", got.RangeSource)
	assert.Equal(t, "typescript", got.Lang)
	assert.Equal(t, 2, got.StartLine)
	assert.Equal(t, 2, got.EndLine)
	assert.Equal(t, "srcMcpArrow = (x: number): number => x + 1", got.Source,
		"json format must carry the same byte-exact extraction as markdown format")
	assert.Empty(t, got.HeadingPath, "code responses must not carry chunk-only fields")
	assert.Empty(t, got.Title, "code responses must not carry document-only fields")
}

// TestSourceJSONFormatRejectsInvalidValue verifies the format arg is validated
// against its enum rather than silently defaulting.
func TestSourceJSONFormatRejectsInvalidValue(t *testing.T) {
	server, nodeIDs, cleanup := setupSourceTestDB(t)
	if server == nil {
		t.Skip("Neo4j not available")
	}
	defer cleanup()

	response := server.handleSourceToolV2(context.Background(), map[string]interface{}{
		"node_id": nodeIDs["srcMcpArrow"],
		"format":  "yaml",
	})
	require.True(t, response.IsError, "an unsupported format must error, not silently fall back")
}

// setupSourceRootPathTestDB seeds a Function whose filePath is
// SERVICE-RELATIVE (unlike setupSourceTestDB's absolute fixture), plus a
// Service node carrying rootPath, so codegraph_source's rootPath-first
// resolution (RFC-012 R2) is exercised end-to-end: the graph never stores an
// absolute path, only the owning Service does.
func setupSourceRootPathTestDB(t *testing.T) (*CodeGraphMCPServer, string, func()) {
	t.Helper()
	client, err := neo4j.NewClient(neo4j.Config{
		URI:      getEnvOrDefault("NEO4J_URI", "bolt://localhost:7687"),
		Username: getEnvOrDefault("NEO4J_USERNAME", "neo4j"),
		Password: getEnvOrDefault("NEO4J_PASSWORD", "password123"),
		Database: getEnvOrDefault("NEO4J_DATABASE", "neo4j"),
	})
	if err != nil {
		t.Skipf("Neo4j not available: %v", err)
		return nil, "", nil
	}
	ctx := context.Background()
	if _, err := client.ExecuteQuery(ctx, "RETURN 1", nil); err != nil {
		t.Skipf("Neo4j not responding: %v", err)
		return nil, "", nil
	}

	_, _ = client.ExecuteQuery(ctx, `MATCH (n) WHERE n.scopeId = "itest-source-rootpath-mcp" DETACH DELETE n`, nil)

	root := t.TempDir()
	relPath := "pkg/root_path_target.go"
	body := "func RootPathTarget() int {\n\treturn 42\n}\n"
	require.NoError(t, os.MkdirAll(filepath.Join(root, "pkg"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, relPath), []byte(body), 0o644))

	_, err = client.ExecuteQuery(ctx, `
		CREATE (:Service {
			name: 'source-rootpath-mcp-svc', scopeId: 'itest-source-rootpath-mcp',
			nodeKey: 'svc:source-rootpath-mcp-svc', rootPath: $rootPath
		})
		CREATE (:Function {
			name: 'RootPathTarget', scopeId: 'itest-source-rootpath-mcp',
			nodeKey: 'itest-source-rootpath-mcp/RootPathTarget',
			serviceName: 'source-rootpath-mcp-svc', filePath: $relPath,
			startLine: 1, endLine: 3
		})
	`, map[string]any{"rootPath": root, "relPath": relPath})
	require.NoError(t, err, "failed to seed rootPath test fixture")

	records, err := client.ExecuteQuery(ctx, `
		MATCH (n:Function) WHERE n.scopeId = 'itest-source-rootpath-mcp'
		RETURN elementId(n) AS node_id
	`, nil)
	require.NoError(t, err)
	require.Len(t, records, 1)
	nodeID := getStringFromRecord(records[0].AsMap(), "node_id")

	server := &CodeGraphMCPServer{
		client:       client,
		queryBuilder: neo4j.NewQueryBuilder(client),
		// workspaceRoot deliberately points somewhere that does NOT contain
		// relPath — proves resolution used rootPath, not a workspaceRoot
		// coincidence.
		workspaceRoot: t.TempDir(),
	}
	cleanup := func() {
		_, _ = client.ExecuteQuery(context.Background(), `MATCH (n) WHERE n.scopeId = "itest-source-rootpath-mcp" DETACH DELETE n`, nil)
		client.Close(context.Background())
	}
	return server, nodeID, cleanup
}

// TestSourceResolvesViaServiceRootPath is the end-to-end proof for RFC-012 R2
// Change 3: a Function whose filePath is service-relative (the only form
// SCIP ever writes) must resolve through its owning Service's rootPath, even
// though the MCP server's own workspaceRoot points elsewhere.
func TestSourceResolvesViaServiceRootPath(t *testing.T) {
	server, nodeID, cleanup := setupSourceRootPathTestDB(t)
	if server == nil {
		t.Skip("Neo4j not available")
	}
	defer cleanup()

	response := server.handleSourceToolV2(context.Background(), map[string]interface{}{
		"node_id": nodeID,
		"format":  "json",
	})
	require.False(t, response.IsError, "source should succeed via rootPath: %+v", response.Content)

	var got sourceResponse
	require.NoError(t, json.Unmarshal([]byte(response.Content[0].Text), &got))
	assert.Contains(t, got.Source, "func RootPathTarget() int {")
	assert.Contains(t, got.Source, "return 42")
}
