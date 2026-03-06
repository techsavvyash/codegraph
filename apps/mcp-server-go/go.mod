module github.com/context-maximiser/code-graph/apps/mcp-server-go

go 1.24.3

require (
	github.com/context-maximiser/code-graph/libs/indexer-go v0.0.0-00010101000000-000000000000
	github.com/context-maximiser/code-graph/libs/neo4j-go v0.0.0-00010101000000-000000000000
	github.com/context-maximiser/code-graph/libs/query-go v0.0.0-00010101000000-000000000000
	github.com/context-maximiser/code-graph/libs/search-go v0.0.0-00010101000000-000000000000
	github.com/joho/godotenv v1.5.1
	github.com/neo4j/neo4j-go-driver/v5 v5.28.3
)

require (
	cloud.google.com/go v0.116.0 // indirect
	cloud.google.com/go/auth v0.9.3 // indirect
	cloud.google.com/go/compute/metadata v0.9.0 // indirect
	github.com/context-maximiser/code-graph/libs/core-models-go v0.0.0-00010101000000-000000000000 // indirect
	github.com/context-maximiser/code-graph/libs/inference-go v0.0.0-00010101000000-000000000000 // indirect
	github.com/context-maximiser/code-graph/libs/intelligence-go v0.0.0-00010101000000-000000000000 // indirect
	github.com/context-maximiser/code-graph/libs/text-index-client-go v0.0.0-00010101000000-000000000000 // indirect
	github.com/golang/groupcache v0.0.0-20210331224755-41bb18bfe9da // indirect
	github.com/google/go-cmp v0.7.0 // indirect
	github.com/google/s2a-go v0.1.8 // indirect
	github.com/googleapis/enterprise-certificate-proxy v0.3.4 // indirect
	github.com/gorilla/websocket v1.5.3 // indirect
	github.com/qdrant/go-client v1.17.1 // indirect
	go.opencensus.io v0.24.0 // indirect
	golang.org/x/crypto v0.48.0 // indirect
	golang.org/x/net v0.50.0 // indirect
	golang.org/x/sys v0.41.0 // indirect
	golang.org/x/text v0.34.0 // indirect
	google.golang.org/genai v1.47.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260209200024-4cfbd4190f57 // indirect
	google.golang.org/grpc v1.79.1 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

replace (
	github.com/context-maximiser/code-graph/libs/core-models-go => ../../libs/core-models-go
	github.com/context-maximiser/code-graph/libs/indexer-go => ../../libs/indexer-go
	github.com/context-maximiser/code-graph/libs/inference-go => ../../libs/inference-go
	github.com/context-maximiser/code-graph/libs/intelligence-go => ../../libs/intelligence-go
	github.com/context-maximiser/code-graph/libs/neo4j-go => ../../libs/neo4j-go
	github.com/context-maximiser/code-graph/libs/query-go => ../../libs/query-go
	github.com/context-maximiser/code-graph/libs/search-go => ../../libs/search-go
	github.com/context-maximiser/code-graph/libs/text-index-client-go => ../../libs/text-index-client-go
)
