package gds

import (
	"context"
	"fmt"
)

// RunWCC runs Weakly Connected Components on the named graph and writes
// componentId to each node. Returns the number of nodes processed.
func (g *GDSClient) RunWCC(ctx context.Context, graphName string) (int, error) {
	cypher := `
		CALL gds.wcc.write($graphName, {
			writeProperty: 'componentId'
		})
		YIELD nodePropertiesWritten
		RETURN nodePropertiesWritten
	`

	records, err := g.client.ExecuteQuery(ctx, cypher, map[string]any{
		"graphName": graphName,
	})
	if err != nil {
		return 0, fmt.Errorf("gds.wcc.write failed: %w", err)
	}

	if len(records) > 0 {
		if n, ok := records[0].AsMap()["nodePropertiesWritten"].(int64); ok {
			return int(n), nil
		}
	}
	return 0, nil
}

// RunLouvain runs the Louvain community detection algorithm on the named graph
// and writes communityId to each node. Returns the number of nodes processed.
func (g *GDSClient) RunLouvain(ctx context.Context, graphName string) (int, error) {
	cypher := `
		CALL gds.louvain.write($graphName, {
			writeProperty: 'communityId'
		})
		YIELD nodePropertiesWritten
		RETURN nodePropertiesWritten
	`

	records, err := g.client.ExecuteQuery(ctx, cypher, map[string]any{
		"graphName": graphName,
	})
	if err != nil {
		return 0, fmt.Errorf("gds.louvain.write failed: %w", err)
	}

	if len(records) > 0 {
		if n, ok := records[0].AsMap()["nodePropertiesWritten"].(int64); ok {
			return int(n), nil
		}
	}
	return 0, nil
}
