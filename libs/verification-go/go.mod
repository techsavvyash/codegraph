module github.com/context-maximiser/code-graph/libs/verification-go

go 1.24.3

require (
	github.com/context-maximiser/code-graph/libs/core-models-go v0.0.0-00010101000000-000000000000
	github.com/context-maximiser/code-graph/libs/intelligence-go v0.0.0-00010101000000-000000000000
)

replace (
	github.com/context-maximiser/code-graph/libs/core-models-go => ../core-models-go
	github.com/context-maximiser/code-graph/libs/intelligence-go => ../intelligence-go
)
