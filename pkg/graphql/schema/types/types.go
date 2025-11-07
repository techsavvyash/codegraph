package types

import (
	"github.com/graphql-go/graphql"
)

// ServiceType represents a service/repository in the graph
var ServiceType = graphql.NewObject(graphql.ObjectConfig{
	Name:        "Service",
	Description: "A service or repository that has been indexed",
	Fields: graphql.Fields{
		"name": &graphql.Field{
			Type:        graphql.NewNonNull(graphql.String),
			Description: "Service name",
		},
		"packageName": &graphql.Field{
			Type:        graphql.String,
			Description: "Package name (e.g., npm package, maven artifact)",
		},
		"version": &graphql.Field{
			Type:        graphql.String,
			Description: "Service version",
		},
		"language": &graphql.Field{
			Type:        graphql.NewNonNull(graphql.String),
			Description: "Primary programming language",
		},
		"repositoryURL": &graphql.Field{
			Type:        graphql.String,
			Description: "Git repository URL",
		},
		"indexedAt": &graphql.Field{
			Type:        graphql.String,
			Description: "When the service was last indexed (ISO 8601)",
		},
		"fileCount": &graphql.Field{
			Type:        graphql.Int,
			Description: "Number of files in the service",
		},
		"symbolCount": &graphql.Field{
			Type:        graphql.Int,
			Description: "Number of symbols defined in the service",
		},
	},
})

// FileType represents a source code file
var FileType = graphql.NewObject(graphql.ObjectConfig{
	Name:        "File",
	Description: "A source code file",
	Fields: graphql.Fields{
		"path": &graphql.Field{
			Type:        graphql.NewNonNull(graphql.String),
			Description: "File path relative to service root",
		},
		"serviceName": &graphql.Field{
			Type:        graphql.NewNonNull(graphql.String),
			Description: "Name of the service this file belongs to",
		},
		"language": &graphql.Field{
			Type:        graphql.NewNonNull(graphql.String),
			Description: "Programming language",
		},
		"lines": &graphql.Field{
			Type:        graphql.Int,
			Description: "Number of lines in the file",
		},
		"size": &graphql.Field{
			Type:        graphql.Int,
			Description: "File size in bytes",
		},
		"hash": &graphql.Field{
			Type:        graphql.String,
			Description: "File content hash",
		},
	},
})

// SymbolType represents a code symbol (function, class, etc.)
var SymbolType = graphql.NewObject(graphql.ObjectConfig{
	Name:        "Symbol",
	Description: "A code symbol (function, class, variable, etc.)",
	Fields: graphql.Fields{
		"scipSymbol": &graphql.Field{
			Type:        graphql.NewNonNull(graphql.String),
			Description: "SCIP-formatted symbol identifier",
		},
		"name": &graphql.Field{
			Type:        graphql.NewNonNull(graphql.String),
			Description: "Symbol name",
		},
		"kind": &graphql.Field{
			Type:        graphql.NewNonNull(graphql.String),
			Description: "Symbol kind (function, class, method, etc.)",
		},
		"displayName": &graphql.Field{
			Type:        graphql.String,
			Description: "Human-readable display name",
		},
		"filePath": &graphql.Field{
			Type:        graphql.NewNonNull(graphql.String),
			Description: "File where the symbol is defined",
		},
		"serviceName": &graphql.Field{
			Type:        graphql.String,
			Description: "Service containing this symbol",
		},
		"startLine": &graphql.Field{
			Type:        graphql.Int,
			Description: "Start line number",
		},
		"endLine": &graphql.Field{
			Type:        graphql.Int,
			Description: "End line number",
		},
		"signature": &graphql.Field{
			Type:        graphql.String,
			Description: "Symbol signature",
		},
		"documentation": &graphql.Field{
			Type:        graphql.String,
			Description: "Symbol documentation/comments",
		},
	},
})

// GraphNodeType represents a node in the graph visualization
var GraphNodeType = graphql.NewObject(graphql.ObjectConfig{
	Name:        "GraphNode",
	Description: "A node in the code graph",
	Fields: graphql.Fields{
		"id": &graphql.Field{
			Type:        graphql.NewNonNull(graphql.String),
			Description: "Unique node identifier",
		},
		"type": &graphql.Field{
			Type:        graphql.NewNonNull(graphql.String),
			Description: "Node type (Service, File, Function, etc.)",
		},
		"label": &graphql.Field{
			Type:        graphql.NewNonNull(graphql.String),
			Description: "Display label for the node",
		},
		"properties": &graphql.Field{
			Type:        graphql.String,
			Description: "JSON-encoded node properties",
		},
	},
})

// GraphEdgeType represents an edge in the graph visualization
var GraphEdgeType = graphql.NewObject(graphql.ObjectConfig{
	Name:        "GraphEdge",
	Description: "An edge (relationship) in the code graph",
	Fields: graphql.Fields{
		"id": &graphql.Field{
			Type:        graphql.NewNonNull(graphql.String),
			Description: "Unique edge identifier",
		},
		"source": &graphql.Field{
			Type:        graphql.NewNonNull(graphql.String),
			Description: "Source node ID",
		},
		"target": &graphql.Field{
			Type:        graphql.NewNonNull(graphql.String),
			Description: "Target node ID",
		},
		"type": &graphql.Field{
			Type:        graphql.NewNonNull(graphql.String),
			Description: "Edge type (CONTAINS, CALLS, REFERENCES, etc.)",
		},
		"properties": &graphql.Field{
			Type:        graphql.String,
			Description: "JSON-encoded edge properties",
		},
	},
})

// GraphType represents the complete graph for visualization
var GraphType = graphql.NewObject(graphql.ObjectConfig{
	Name:        "Graph",
	Description: "A code graph for visualization",
	Fields: graphql.Fields{
		"nodes": &graphql.Field{
			Type:        graphql.NewList(GraphNodeType),
			Description: "Graph nodes",
		},
		"edges": &graphql.Field{
			Type:        graphql.NewList(GraphEdgeType),
			Description: "Graph edges (relationships)",
		},
		"totalNodes": &graphql.Field{
			Type:        graphql.Int,
			Description: "Total number of nodes",
		},
		"totalEdges": &graphql.Field{
			Type:        graphql.Int,
			Description: "Total number of edges",
		},
		"hasMore": &graphql.Field{
			Type:        graphql.Boolean,
			Description: "Whether there are more results available",
		},
	},
})

// MetadataType represents custom metadata attached to nodes
var MetadataType = graphql.NewObject(graphql.ObjectConfig{
	Name:        "Metadata",
	Description: "Custom metadata attached to a code entity",
	Fields: graphql.Fields{
		"key": &graphql.Field{
			Type:        graphql.NewNonNull(graphql.String),
			Description: "Metadata key",
		},
		"value": &graphql.Field{
			Type:        graphql.NewNonNull(graphql.String),
			Description: "Metadata value",
		},
		"category": &graphql.Field{
			Type:        graphql.String,
			Description: "Optional category for grouping",
		},
		"createdAt": &graphql.Field{
			Type:        graphql.String,
			Description: "When the metadata was created (ISO 8601)",
		},
		"updatedAt": &graphql.Field{
			Type:        graphql.String,
			Description: "When the metadata was last updated (ISO 8601)",
		},
		"createdBy": &graphql.Field{
			Type:        graphql.String,
			Description: "Who created the metadata",
		},
	},
})
