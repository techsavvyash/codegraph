// Package census implements the RFC-013 universal recall floor: tree-sitter
// declaration counts per file compared against Function/Method node counts in
// the graph. Language-independent; catches whole-file and whole-construct
// indexing dropouts.
package census

import (
	"context"
	"errors"

	neo4j "github.com/context-maximiser/code-graph/internal/graph"
	"github.com/context-maximiser/code-graph/internal/verify"
)

// Options configures a census run over one indexed project.
type Options struct {
	ProjectRoot string
	ServiceName string
	ScopeID     string
	SampleLimit int
}

// Run walks the project with tree-sitter structure extraction and compares
// per-file declaration counts with the graph's Function/Method nodes.
func Run(ctx context.Context, client *neo4j.Client, opts Options) (*verify.Report, error) {
	return nil, errors.New("verify census: not implemented yet (RFC-013 Layer 2)")
}
