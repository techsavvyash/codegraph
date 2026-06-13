package static

import (
	"testing"

	"github.com/context-maximiser/code-graph/libs/core-models-go"
)

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
			targetNodeKey: models.FileNodeKey("account", "pkg/models/node.go"),
			expected:      "tombstone:pr-42:file:account:pkg/models/node.go",
		},
		{
			name:          "function tombstone",
			scopeID:       "pr-42",
			targetNodeKey: models.FunctionNodeKey("account", "pkg/neo4j/client.go", "MergeNode(...)"),
			expected:      "tombstone:pr-42:func:account:pkg/neo4j/client.go#MergeNode(...)",
		},
		{
			name:          "different PR scope",
			scopeID:       "pr-99",
			targetNodeKey: models.FileNodeKey("account", "pkg/models/node.go"),
			expected:      "tombstone:pr-99:file:account:pkg/models/node.go",
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
	tc := NewTombstoneCreator(nil, scope, "account")
	if tc.scopeCtx.ScopeID != "pr-42" {
		t.Errorf("expected scopeId pr-42, got %s", tc.scopeCtx.ScopeID)
	}
	if tc.scopeCtx.Scope != "pr" {
		t.Errorf("expected scope pr, got %s", tc.scopeCtx.Scope)
	}
	if tc.serviceName != "account" {
		t.Errorf("expected serviceName account, got %s", tc.serviceName)
	}
}
