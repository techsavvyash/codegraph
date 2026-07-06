package gds

import (
	"context"
	"fmt"
)

// PageRankOpts configures PageRank execution.
type PageRankOpts struct {
	MaxIterations int
	DampingFactor float64
	WriteProperty string
	Tolerance     float64
}

// DefaultPageRankOpts returns sensible defaults.
func DefaultPageRankOpts() PageRankOpts {
	return PageRankOpts{
		MaxIterations: 20,
		DampingFactor: 0.85,
		WriteProperty: "pageRank",
		Tolerance:     0.0001,
	}
}

// RunPageRank runs the PageRank algorithm on the named graph and writes results
// back to nodes as the specified property. Returns the number of nodes processed.
func (g *GDSClient) RunPageRank(ctx context.Context, graphName string, opts PageRankOpts) (int, error) {
	if opts.WriteProperty == "" {
		opts.WriteProperty = "pageRank"
	}
	if opts.MaxIterations <= 0 {
		opts.MaxIterations = 20
	}
	if opts.DampingFactor <= 0 {
		opts.DampingFactor = 0.85
	}

	cypher := `
		CALL gds.pageRank.write($graphName, {
			maxIterations: $maxIterations,
			dampingFactor: $dampingFactor,
			tolerance: $tolerance,
			writeProperty: $writeProperty
		})
		YIELD nodePropertiesWritten
		RETURN nodePropertiesWritten
	`

	records, err := g.client.ExecuteQuery(ctx, cypher, map[string]any{
		"graphName":     graphName,
		"maxIterations": opts.MaxIterations,
		"dampingFactor": opts.DampingFactor,
		"tolerance":     opts.Tolerance,
		"writeProperty": opts.WriteProperty,
	})
	if err != nil {
		return 0, fmt.Errorf("gds.pageRank.write failed: %w", err)
	}

	if len(records) > 0 {
		if n, ok := records[0].AsMap()["nodePropertiesWritten"].(int64); ok {
			return int(n), nil
		}
	}
	return 0, nil
}
