package resolvers

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/context-maximiser/code-graph/pkg/neo4j"
	"github.com/graphql-go/graphql"
	dbtype "github.com/neo4j/neo4j-go-driver/v5/neo4j/dbtype"
)

// GraphResolver handles graph visualization queries
type GraphResolver struct {
	client *neo4j.Client
}

// NewGraphResolver creates a new graph resolver
func NewGraphResolver(client *neo4j.Client) *GraphResolver {
	return &GraphResolver{client: client}
}

// GetGraph retrieves graph data for visualization
func (r *GraphResolver) GetGraph(p graphql.ResolveParams) (interface{}, error) {
	ctx := context.Background()
	serviceName := p.Args["serviceName"].(string)
	limit, _ := p.Args["limit"].(int)
	offset, _ := p.Args["offset"].(int)

	// Query for nodes and their relationships
	cypher := `
		MATCH (s:Service {name: $serviceName})
		OPTIONAL MATCH (s)-[r1:CONTAINS]->(f:File)
		OPTIONAL MATCH (f)-[r2:CONTAINS]->(sym:Symbol)
		WITH s, f, sym, r1, r2
		LIMIT $limit
		SKIP $offset
		RETURN s, f, sym, r1, r2
	`

	params := map[string]interface{}{
		"serviceName": serviceName,
		"limit":       limit,
		"offset":      offset,
	}

	result, err := r.client.ExecuteQuery(ctx, cypher, params)
	if err != nil {
		return nil, fmt.Errorf("failed to query graph: %w", err)
	}

	// Process results into nodes and edges
	nodesMap := make(map[string]map[string]interface{})
	edgesMap := make(map[string]map[string]interface{})

	for _, record := range result {
		recordMap := record.AsMap()

		// Process service node
		if serviceNode, ok := recordMap["s"].(dbtype.Node); ok {
			nodeID := fmt.Sprintf("service-%v", serviceNode.Props["name"])
			if _, exists := nodesMap[nodeID]; !exists {
				props, _ := json.Marshal(serviceNode.Props)
				nodesMap[nodeID] = map[string]interface{}{
					"id":         nodeID,
					"type":       "Service",
					"label":      serviceNode.Props["name"],
					"properties": string(props),
				}
			}
		}

		// Process file node
		if fileNode, ok := recordMap["f"].(dbtype.Node); ok {
			nodeID := fmt.Sprintf("file-%v", fileNode.Props["path"])
			if _, exists := nodesMap[nodeID]; !exists {
				props, _ := json.Marshal(fileNode.Props)
				nodesMap[nodeID] = map[string]interface{}{
					"id":         nodeID,
					"type":       "File",
					"label":      fileNode.Props["path"],
					"properties": string(props),
				}
			}

			// Process CONTAINS relationship (Service -> File)
			if rel, ok := recordMap["r1"].(dbtype.Relationship); ok {
				edgeID := fmt.Sprintf("edge-%d", rel.Id)
				if _, exists := edgesMap[edgeID]; !exists {
					edgesMap[edgeID] = map[string]interface{}{
						"id":     edgeID,
						"source": fmt.Sprintf("service-%v", serviceName),
						"target": nodeID,
						"type":   rel.Type,
					}
				}
			}
		}

		// Process symbol node
		if symNode, ok := recordMap["sym"].(dbtype.Node); ok {
			nodeID := fmt.Sprintf("symbol-%v", symNode.Props["symbol"])
			if _, exists := nodesMap[nodeID]; !exists {
				props, _ := json.Marshal(symNode.Props)
				nodesMap[nodeID] = map[string]interface{}{
					"id":         nodeID,
					"type":       "Symbol",
					"label":      symNode.Props["name"],
					"properties": string(props),
				}
			}

			// Process CONTAINS relationship (File -> Symbol)
			if rel, ok := recordMap["r2"].(dbtype.Relationship); ok {
				if fileNode, ok := recordMap["f"].(dbtype.Node); ok {
					edgeID := fmt.Sprintf("edge-%d", rel.Id)
					if _, exists := edgesMap[edgeID]; !exists {
						edgesMap[edgeID] = map[string]interface{}{
							"id":     edgeID,
							"source": fmt.Sprintf("file-%v", fileNode.Props["path"]),
							"target": nodeID,
							"type":   rel.Type,
						}
					}
				}
			}
		}
	}

	// Convert maps to slices
	nodes := make([]map[string]interface{}, 0, len(nodesMap))
	for _, node := range nodesMap {
		nodes = append(nodes, node)
	}

	edges := make([]map[string]interface{}, 0, len(edgesMap))
	for _, edge := range edgesMap {
		edges = append(edges, edge)
	}

	// Check if there are more results
	hasMore := len(result) >= limit

	graph := map[string]interface{}{
		"nodes":      nodes,
		"edges":      edges,
		"totalNodes": len(nodes),
		"totalEdges": len(edges),
		"hasMore":    hasMore,
	}

	return graph, nil
}
