package oracle

import (
	"context"
	"errors"

	neo4j "github.com/context-maximiser/code-graph/internal/graph"
)

// TSOracleOptions configures a sampled TypeScript differential run.
type TSOracleOptions struct {
	ProjectRoot string
	ServiceName string
	ScopeID     string
	ScriptPath  string // resolved tools/ts-oracle/oracle.mjs; empty → auto-locate
	SampleSize  int    // call sites to sample; 0 → default
	SampleLimit int
}

// RunTSOracle samples compiler-resolved call sites from the target project and
// checks each against the indexed CALLS edges.
func RunTSOracle(ctx context.Context, client *neo4j.Client, opts TSOracleOptions) (*OracleReport, error) {
	return nil, errors.New("verify oracle --language=typescript: not implemented yet (RFC-013 Layer 2)")
}
