package gds

import (
	"context"
	"fmt"
)

// BetweennessOpts configures betweenness centrality execution.
type BetweennessOpts struct {
	SamplingSize  int
	WriteProperty string
}

// DefaultBetweennessOpts returns sensible defaults.
func DefaultBetweennessOpts() BetweennessOpts {
	return BetweennessOpts{
		WriteProperty: "betweennessCentrality",
	}
}

// RunBetweennessCentrality runs the betweenness centrality algorithm on the
// named graph and writes results back as node properties. Returns the number
// of nodes processed.
func (g *GDSClient) RunBetweennessCentrality(ctx context.Context, graphName string, opts BetweennessOpts) (int, error) {
	if opts.WriteProperty == "" {
		opts.WriteProperty = "betweennessCentrality"
	}

	params := map[string]any{
		"graphName":     graphName,
		"writeProperty": opts.WriteProperty,
	}

	cypher := `
		CALL gds.betweenness.write($graphName, {
			writeProperty: $writeProperty
		})
		YIELD nodePropertiesWritten
		RETURN nodePropertiesWritten
	`

	if opts.SamplingSize > 0 {
		cypher = `
			CALL gds.betweenness.write($graphName, {
				writeProperty: $writeProperty,
				samplingSize: $samplingSize
			})
			YIELD nodePropertiesWritten
			RETURN nodePropertiesWritten
		`
		params["samplingSize"] = opts.SamplingSize
	}

	records, err := g.client.ExecuteQuery(ctx, cypher, params)
	if err != nil {
		return 0, fmt.Errorf("gds.betweenness.write failed: %w", err)
	}

	if len(records) > 0 {
		if n, ok := records[0].AsMap()["nodePropertiesWritten"].(int64); ok {
			return int(n), nil
		}
	}
	return 0, nil
}
