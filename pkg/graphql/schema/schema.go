package schema

import (
	"github.com/context-maximiser/code-graph/pkg/graphql/schema/resolvers"
	"github.com/context-maximiser/code-graph/pkg/graphql/schema/types"
	"github.com/context-maximiser/code-graph/pkg/neo4j"
	"github.com/graphql-go/graphql"
)

// NewSchema creates a new GraphQL schema
func NewSchema(client *neo4j.Client) (graphql.Schema, error) {
	// Create query type
	queryType := graphql.NewObject(graphql.ObjectConfig{
		Name: "Query",
		Fields: graphql.Fields{
			"services": &graphql.Field{
				Type:        graphql.NewList(types.ServiceType),
				Description: "Get all indexed services",
				Resolve:     resolvers.NewServiceResolver(client).GetServices,
			},
			"service": &graphql.Field{
				Type:        types.ServiceType,
				Description: "Get a specific service by name",
				Args: graphql.FieldConfigArgument{
					"name": &graphql.ArgumentConfig{
						Type: graphql.NewNonNull(graphql.String),
					},
				},
				Resolve: resolvers.NewServiceResolver(client).GetService,
			},
			"files": &graphql.Field{
				Type:        graphql.NewList(types.FileType),
				Description: "Get files for a service",
				Args: graphql.FieldConfigArgument{
					"serviceName": &graphql.ArgumentConfig{
						Type: graphql.NewNonNull(graphql.String),
					},
					"limit": &graphql.ArgumentConfig{
						Type:         graphql.Int,
						DefaultValue: 100,
					},
					"offset": &graphql.ArgumentConfig{
						Type:         graphql.Int,
						DefaultValue: 0,
					},
				},
				Resolve: resolvers.NewFileResolver(client).GetFiles,
			},
			"file": &graphql.Field{
				Type:        types.FileType,
				Description: "Get a specific file",
				Args: graphql.FieldConfigArgument{
					"path": &graphql.ArgumentConfig{
						Type: graphql.NewNonNull(graphql.String),
					},
					"serviceName": &graphql.ArgumentConfig{
						Type: graphql.NewNonNull(graphql.String),
					},
				},
				Resolve: resolvers.NewFileResolver(client).GetFile,
			},
			"symbols": &graphql.Field{
				Type:        graphql.NewList(types.SymbolType),
				Description: "Search for symbols",
				Args: graphql.FieldConfigArgument{
					"query": &graphql.ArgumentConfig{
						Type: graphql.NewNonNull(graphql.String),
					},
					"limit": &graphql.ArgumentConfig{
						Type:         graphql.Int,
						DefaultValue: 50,
					},
				},
				Resolve: resolvers.NewSymbolResolver(client).SearchSymbols,
			},
			"graph": &graphql.Field{
				Type:        types.GraphType,
				Description: "Get graph data for visualization",
				Args: graphql.FieldConfigArgument{
					"serviceName": &graphql.ArgumentConfig{
						Type: graphql.NewNonNull(graphql.String),
					},
					"limit": &graphql.ArgumentConfig{
						Type:         graphql.Int,
						DefaultValue: 100,
					},
					"offset": &graphql.ArgumentConfig{
						Type:         graphql.Int,
						DefaultValue: 0,
					},
				},
				Resolve: resolvers.NewGraphResolver(client).GetGraph,
			},
		},
	})

	// Create mutation type
	mutationType := graphql.NewObject(graphql.ObjectConfig{
		Name: "Mutation",
		Fields: graphql.Fields{
			"addMetadata": &graphql.Field{
				Type:        types.MetadataType,
				Description: "Add metadata to a node",
				Args: graphql.FieldConfigArgument{
					"targetId": &graphql.ArgumentConfig{
						Type: graphql.NewNonNull(graphql.String),
					},
					"targetType": &graphql.ArgumentConfig{
						Type: graphql.NewNonNull(graphql.String),
					},
					"key": &graphql.ArgumentConfig{
						Type: graphql.NewNonNull(graphql.String),
					},
					"value": &graphql.ArgumentConfig{
						Type: graphql.NewNonNull(graphql.String),
					},
					"category": &graphql.ArgumentConfig{
						Type: graphql.String,
					},
				},
				Resolve: resolvers.NewMetadataResolver(client).AddMetadata,
			},
		},
	})

	// Create schema
	schemaConfig := graphql.SchemaConfig{
		Query:    queryType,
		Mutation: mutationType,
	}

	return graphql.NewSchema(schemaConfig)
}
