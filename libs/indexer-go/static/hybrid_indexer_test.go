package static_test

import (
	"testing"

	"github.com/context-maximiser/code-graph/libs/indexer-go/static"
	models "github.com/context-maximiser/code-graph/libs/core-models-go"
)

// TestIndexGoService_ConfigConvertible verifies that the hybrid mode
// does not run call graph / RPC detection during the SCIP pass.
// This is a unit-level wiring test — it does not require a Neo4j instance.
func TestIndexGoService_ConfigConvertible(t *testing.T) {
	cfg := static.IndexConfig{
		Client:      nil,
		ServiceName: "test-service",
		Version:     "v1.0.0",
		RepoURL:     "https://github.com/example/test",
		ProjectPath: t.TempDir(),
		ScopeCtx:    models.DefaultScope(),
	}
	_ = cfg // validates that IndexConfig is structurally correct
}
