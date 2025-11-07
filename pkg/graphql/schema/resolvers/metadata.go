package resolvers

import (
	"context"
	"fmt"
	"time"

	"github.com/context-maximiser/code-graph/pkg/neo4j"
	"github.com/graphql-go/graphql"
)

// MetadataResolver handles metadata-related mutations
type MetadataResolver struct {
	client *neo4j.Client
}

// NewMetadataResolver creates a new metadata resolver
func NewMetadataResolver(client *neo4j.Client) *MetadataResolver {
	return &MetadataResolver{client: client}
}

// AddMetadata adds custom metadata to a node
func (r *MetadataResolver) AddMetadata(p graphql.ResolveParams) (interface{}, error) {
	ctx := context.Background()

	targetID := p.Args["targetId"].(string)
	targetType := p.Args["targetType"].(string)
	key := p.Args["key"].(string)
	value := p.Args["value"].(string)

	category := ""
	if cat, ok := p.Args["category"].(string); ok {
		category = cat
	}

	// Add metadata as a property on the node
	// We use a map to store all metadata with a "meta_" prefix
	cypher := fmt.Sprintf(`
		MATCH (n:%s)
		WHERE id(n) = $targetId OR n.name = $targetId OR n.path = $targetId OR n.symbol = $targetId
		SET n.meta_%s = $value,
			n.meta_%s_category = $category,
			n.meta_%s_created_at = $createdAt
		RETURN n
	`, targetType, key, key, key)

	params := map[string]interface{}{
		"targetId":  targetID,
		"value":     value,
		"category":  category,
		"createdAt": time.Now().Format(time.RFC3339),
	}

	result, err := r.client.ExecuteQuery(ctx, cypher, params)
	if err != nil {
		return nil, fmt.Errorf("failed to add metadata: %w", err)
	}

	if len(result) == 0 {
		return nil, fmt.Errorf("node not found: %s", targetID)
	}

	metadata := map[string]interface{}{
		"key":       key,
		"value":     value,
		"category":  category,
		"createdAt": time.Now().Format(time.RFC3339),
	}

	return metadata, nil
}
