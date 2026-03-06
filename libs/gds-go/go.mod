module github.com/context-maximiser/code-graph/libs/gds-go

go 1.24.3

require github.com/context-maximiser/code-graph/libs/neo4j-go v0.0.0-00010101000000-000000000000

require (
	github.com/context-maximiser/code-graph/libs/core-models-go v0.0.0-00010101000000-000000000000 // indirect
	github.com/neo4j/neo4j-go-driver/v5 v5.28.3 // indirect
)

replace (
	github.com/context-maximiser/code-graph/libs/core-models-go => ../core-models-go
	github.com/context-maximiser/code-graph/libs/neo4j-go => ../neo4j-go
)
