package gds

import (
	"context"
	"fmt"

	"github.com/context-maximiser/code-graph/internal/graph"
)

// GDSClient wraps a Neo4j Client and provides GDS algorithm operations.
type GDSClient struct {
	client *neo4j.Client
}

// NewGDSClient creates a new GDS client wrapping the given Neo4j client.
func NewGDSClient(client *neo4j.Client) *GDSClient {
	return &GDSClient{client: client}
}

// ProjectGraph creates a named GDS graph projection filtered by scope.
// nodeLabels and relTypes specify which labels/relationships to include.
// Uses cypher projection with escaped quotes to handle scope ID values safely.
func (g *GDSClient) ProjectGraph(ctx context.Context, name string, nodeLabels []string, relTypes []string, scopeID string) error {
	// Escape single quotes in scopeID for safe embedding in cypher strings.
	escapedScope := escapeQuotes(scopeID)

	// Build node query with scope filter.
	nodeQuery := "MATCH (n) WHERE ("
	for i, label := range nodeLabels {
		if i > 0 {
			nodeQuery += " OR "
		}
		nodeQuery += fmt.Sprintf("n:%s", label)
	}
	nodeQuery += fmt.Sprintf(") AND (n.scopeId = \\'%s\\' OR n.scopeId = \\'main\\') RETURN id(n) AS id", escapedScope)

	// Build relationship query with scope filter.
	relQuery := "MATCH (s)-[r]->(t) WHERE type(r) IN ["
	for i, rt := range relTypes {
		if i > 0 {
			relQuery += ", "
		}
		relQuery += fmt.Sprintf("\\'%s\\'", rt)
	}
	relQuery += fmt.Sprintf("] AND (s.scopeId = \\'%s\\' OR s.scopeId = \\'main\\') AND (t.scopeId = \\'%s\\' OR t.scopeId = \\'main\\') RETURN id(s) AS source, id(t) AS target", escapedScope, escapedScope)

	cypher := fmt.Sprintf(`
		CALL gds.graph.project.cypher(
			$name,
			'%s',
			'%s'
		)
	`, nodeQuery, relQuery)

	_, err := g.client.ExecuteQuery(ctx, cypher, map[string]any{
		"name": name,
	})
	if err != nil {
		return fmt.Errorf("gds.graph.project.cypher failed: %w", err)
	}
	return nil
}

func escapeQuotes(s string) string {
	result := ""
	for _, c := range s {
		if c == '\'' {
			result += "\\'"
		} else {
			result += string(c)
		}
	}
	return result
}

// DropGraph removes a named GDS graph projection.
func (g *GDSClient) DropGraph(ctx context.Context, name string) error {
	cypher := `CALL gds.graph.drop($name, false)`
	_, err := g.client.ExecuteQuery(ctx, cypher, map[string]any{"name": name})
	if err != nil {
		return fmt.Errorf("gds.graph.drop failed: %w", err)
	}
	return nil
}

// GraphExists checks if a named GDS graph projection exists.
func (g *GDSClient) GraphExists(ctx context.Context, name string) (bool, error) {
	cypher := `CALL gds.graph.exists($name) YIELD exists RETURN exists`
	records, err := g.client.ExecuteQuery(ctx, cypher, map[string]any{"name": name})
	if err != nil {
		return false, fmt.Errorf("gds.graph.exists failed: %w", err)
	}
	if len(records) == 0 {
		return false, nil
	}
	exists, _ := records[0].AsMap()["exists"].(bool)
	return exists, nil
}

// IsGDSAvailable checks whether the GDS plugin is installed.
func (g *GDSClient) IsGDSAvailable(ctx context.Context) bool {
	_, err := g.client.ExecuteQuery(ctx, "RETURN gds.version() AS version", nil)
	return err == nil
}
