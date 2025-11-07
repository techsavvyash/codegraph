package resolvers

import (
	"context"
	"fmt"

	"github.com/context-maximiser/code-graph/pkg/neo4j"
	"github.com/graphql-go/graphql"
	dbtype "github.com/neo4j/neo4j-go-driver/v5/neo4j/dbtype"
)

// FileResolver handles file-related queries
type FileResolver struct {
	client *neo4j.Client
}

// NewFileResolver creates a new file resolver
func NewFileResolver(client *neo4j.Client) *FileResolver {
	return &FileResolver{client: client}
}

// GetFiles retrieves files for a service with pagination
func (r *FileResolver) GetFiles(p graphql.ResolveParams) (interface{}, error) {
	ctx := context.Background()
	serviceName := p.Args["serviceName"].(string)
	limit, _ := p.Args["limit"].(int)
	offset, _ := p.Args["offset"].(int)

	cypher := `
		MATCH (s:Service {name: $serviceName})-[:CONTAINS]->(f:File)
		RETURN f
		ORDER BY f.path
		SKIP $offset
		LIMIT $limit
	`

	params := map[string]interface{}{
		"serviceName": serviceName,
		"offset":      offset,
		"limit":       limit,
	}

	result, err := r.client.ExecuteQuery(ctx, cypher, params)
	if err != nil {
		return nil, fmt.Errorf("failed to query files: %w", err)
	}

	var files []map[string]interface{}
	for _, record := range result {
		recordMap := record.AsMap()

		if fileNode, ok := recordMap["f"].(dbtype.Node); ok {
			props := fileNode.Props

			file := map[string]interface{}{
				"path":        props["path"],
				"serviceName": props["serviceName"],
				"language":    props["language"],
				"lines":       props["lines"],
				"size":        props["size"],
				"hash":        props["hash"],
			}

			files = append(files, file)
		}
	}

	return files, nil
}

// GetFile retrieves a specific file
func (r *FileResolver) GetFile(p graphql.ResolveParams) (interface{}, error) {
	ctx := context.Background()
	path := p.Args["path"].(string)
	serviceName := p.Args["serviceName"].(string)

	cypher := `
		MATCH (f:File {path: $path, serviceName: $serviceName})
		RETURN f
	`

	params := map[string]interface{}{
		"path":        path,
		"serviceName": serviceName,
	}

	result, err := r.client.ExecuteQuery(ctx, cypher, params)
	if err != nil {
		return nil, fmt.Errorf("failed to query file: %w", err)
	}

	if len(result) == 0 {
		return nil, nil
	}

	recordMap := result[0].AsMap()
	if fileNode, ok := recordMap["f"].(dbtype.Node); ok {
		props := fileNode.Props

		file := map[string]interface{}{
			"path":        props["path"],
			"serviceName": props["serviceName"],
			"language":    props["language"],
			"lines":       props["lines"],
			"size":        props["size"],
			"hash":        props["hash"],
		}

		return file, nil
	}

	return nil, nil
}
