package verify

import (
	"context"
	"errors"

	neo4j "github.com/context-maximiser/code-graph/internal/graph"
)

// IntegrityOptions scopes an integrity run. Empty ServiceName means the whole
// graph; ScopeID defaults to all scopes (non-main scopes are themselves a
// finding of the scope-hygiene check).
type IntegrityOptions struct {
	ServiceName string
	ScopeID     string
	SampleLimit int // max offender samples per check; 0 → default
}

// RunIntegrity executes the RFC-013 Layer-1 invariant suite against the graph.
func RunIntegrity(ctx context.Context, client *neo4j.Client, opts IntegrityOptions) (*Report, error) {
	return nil, errors.New("verify integrity: not implemented yet (RFC-013 Layer 1)")
}
