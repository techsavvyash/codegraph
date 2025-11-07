package resolvers

import (
	"context"
	"fmt"

	"github.com/context-maximiser/code-graph/pkg/neo4j"
	"github.com/graphql-go/graphql"
	dbtype "github.com/neo4j/neo4j-go-driver/v5/neo4j/dbtype"
)

// ServiceResolver handles service-related queries
type ServiceResolver struct {
	client *neo4j.Client
}

// NewServiceResolver creates a new service resolver
func NewServiceResolver(client *neo4j.Client) *ServiceResolver {
	return &ServiceResolver{client: client}
}

// GetServices retrieves all indexed services
func (r *ServiceResolver) GetServices(p graphql.ResolveParams) (interface{}, error) {
	ctx := context.Background()

	cypher := `
		MATCH (s:Service)
		OPTIONAL MATCH (s)-[:CONTAINS]->(f:File)
		OPTIONAL MATCH (f)-[:CONTAINS]->(sym:Symbol)
		RETURN s,
			   COUNT(DISTINCT f) as fileCount,
			   COUNT(DISTINCT sym) as symbolCount
		ORDER BY s.indexed_at DESC
	`

	result, err := r.client.ExecuteQuery(ctx, cypher, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to query services: %w", err)
	}

	var services []map[string]interface{}
	for _, record := range result {
		recordMap := record.AsMap()

		if serviceNode, ok := recordMap["s"].(dbtype.Node); ok {
			props := serviceNode.Props

			service := map[string]interface{}{
				"name":          props["name"],
				"packageName":   props["packageName"],
				"version":       props["version"],
				"language":      props["language"],
				"repositoryURL": props["repositoryURL"],
				"indexedAt":     formatDateTime(props["indexed_at"]),
				"fileCount":     recordMap["fileCount"],
				"symbolCount":   recordMap["symbolCount"],
			}

			services = append(services, service)
		}
	}

	return services, nil
}

// GetService retrieves a specific service by name
func (r *ServiceResolver) GetService(p graphql.ResolveParams) (interface{}, error) {
	ctx := context.Background()
	name := p.Args["name"].(string)

	cypher := `
		MATCH (s:Service {name: $name})
		OPTIONAL MATCH (s)-[:CONTAINS]->(f:File)
		OPTIONAL MATCH (f)-[:CONTAINS]->(sym:Symbol)
		RETURN s,
			   COUNT(DISTINCT f) as fileCount,
			   COUNT(DISTINCT sym) as symbolCount
	`

	params := map[string]interface{}{
		"name": name,
	}

	result, err := r.client.ExecuteQuery(ctx, cypher, params)
	if err != nil {
		return nil, fmt.Errorf("failed to query service: %w", err)
	}

	if len(result) == 0 {
		return nil, nil
	}

	recordMap := result[0].AsMap()
	if serviceNode, ok := recordMap["s"].(dbtype.Node); ok {
		props := serviceNode.Props

		service := map[string]interface{}{
			"name":          props["name"],
			"packageName":   props["packageName"],
			"version":       props["version"],
			"language":      props["language"],
			"repositoryURL": props["repositoryURL"],
			"indexedAt":     formatDateTime(props["indexed_at"]),
			"fileCount":     recordMap["fileCount"],
			"symbolCount":   recordMap["symbolCount"],
		}

		return service, nil
	}

	return nil, nil
}

// formatDateTime converts Neo4j datetime to ISO 8601 string
func formatDateTime(value interface{}) string {
	if value == nil {
		return ""
	}
	// Handle different possible types from Neo4j
	switch v := value.(type) {
	case string:
		return v
	default:
		return fmt.Sprintf("%v", v)
	}
}
