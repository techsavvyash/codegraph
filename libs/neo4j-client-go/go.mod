module github.com/context-maximiser/code-graph/libs/neo4j-client-go

go 1.24.3

require (
	github.com/context-maximiser/code-graph/libs/core-models-go v0.0.0-00010101000000-000000000000
	github.com/neo4j/neo4j-go-driver/v5 v5.28.3
)

replace github.com/context-maximiser/code-graph/libs/core-models-go => ../core-models-go
