package static

import (
	"testing"

	"github.com/context-maximiser/code-graph/libs/core-models-go"
)

const tombSvc = "codegraph/apps/cli"

func TestTombstoneNodeKeyDerivation(t *testing.T) {
	tests := []struct {
		name          string
		scopeID       string
		targetNodeKey string
		expected      string
	}{
		{
			name:          "file tombstone",
			scopeID:       "pr-42",
			targetNodeKey: models.FileNodeKey(tombSvc, "pkg/models/node.go"),
			expected:      "tombstone:pr-42:file:codegraph/apps/cli:pkg/models/node.go",
		},
		{
			name:          "function tombstone",
			scopeID:       "pr-42",
			targetNodeKey: models.FunctionNodeKey(tombSvc, "pkg/neo4j/client.go", "MergeNode(...)"),
			expected:      "tombstone:pr-42:func:codegraph/apps/cli:pkg/neo4j/client.go#MergeNode(...)",
		},
		{
			name:          "different PR scope",
			scopeID:       "pr-99",
			targetNodeKey: models.FileNodeKey(tombSvc, "pkg/models/node.go"),
			expected:      "tombstone:pr-99:file:codegraph/apps/cli:pkg/models/node.go",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := models.TombstoneNodeKey(tt.scopeID, tt.targetNodeKey)
			if got != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, got)
			}
		})
	}
}

func TestTombstoneCreatorFieldSetup(t *testing.T) {
	scope := models.NewPRScope("42")
	tc := NewTombstoneCreator(nil, scope, tombSvc)
	if tc.scopeCtx.ScopeID != "pr-42" {
		t.Errorf("expected scopeId pr-42, got %s", tc.scopeCtx.ScopeID)
	}
	if tc.scopeCtx.Scope != "pr" {
		t.Errorf("expected scope pr, got %s", tc.scopeCtx.Scope)
	}
	if tc.serviceName != tombSvc {
		t.Errorf("expected serviceName %s, got %s", tombSvc, tc.serviceName)
	}
}

// TestTombstoneFileNodeKeyDistinctAcrossServices verifies that the tombstone
// flow produces distinct target keys for the same relative path in two
// services — the bug B1 directly fixes for the tombstone code path.
func TestTombstoneFileNodeKeyDistinctAcrossServices(t *testing.T) {
	a := models.FileNodeKey("codegraph/apps/cli", "main.go")
	b := models.FileNodeKey("codegraph/apps/mcp-server-go", "main.go")
	if a == b {
		t.Fatalf("File nodeKey collided across services: both = %q", a)
	}
	tombA := models.TombstoneNodeKey("pr-1", a)
	tombB := models.TombstoneNodeKey("pr-1", b)
	if tombA == tombB {
		t.Fatalf("Tombstone nodeKey collided across services: both = %q", tombA)
	}
}
