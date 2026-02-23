module github.com/context-maximiser/code-graph/libs/evals-go

go 1.24.3

require (
	github.com/context-maximiser/code-graph/libs/benchmarks-go v0.0.0-00010101000000-000000000000
	github.com/context-maximiser/code-graph/libs/search-go v0.0.0-00010101000000-000000000000
	github.com/context-maximiser/code-graph/libs/text-index-client-go v0.0.0-00010101000000-000000000000
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/context-maximiser/code-graph/libs/core-models-go v0.0.0-00010101000000-000000000000 // indirect
	github.com/context-maximiser/code-graph/libs/neo4j-go v0.0.0-00010101000000-000000000000 // indirect
)

replace (
	github.com/context-maximiser/code-graph/libs/benchmarks-go => ../benchmarks-go
	github.com/context-maximiser/code-graph/libs/core-models-go => ../core-models-go
	github.com/context-maximiser/code-graph/libs/neo4j-go => ../neo4j-go
	github.com/context-maximiser/code-graph/libs/search-go => ../search-go
	github.com/context-maximiser/code-graph/libs/text-index-client-go => ../text-index-client-go
)
