package oracle

import (
	"context"
	"errors"

	neo4j "github.com/context-maximiser/code-graph/internal/graph"
)

// GoOracleOptions configures a Go differential run.
type GoOracleOptions struct {
	ProjectRoot string
	ServiceName string
	ScopeID     string
	SampleLimit int // max mismatch samples retained per bucket; 0 → default
}

// RunGoOracle builds independent static and CHA call graphs for the project
// via golang.org/x/tools and compares them against the indexed CALLS edges.
func RunGoOracle(ctx context.Context, client *neo4j.Client, opts GoOracleOptions) (*OracleReport, error) {
	return nil, errors.New("verify oracle --language=go: not implemented yet (RFC-013 Layer 2)")
}
