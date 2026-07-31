package oracle

import (
	"context"
	"fmt"

	neo4j "github.com/context-maximiser/code-graph/internal/graph"
)

// GoOracleOptions configures a Go differential run.
type GoOracleOptions struct {
	ProjectRoot string
	ServiceName string
	ScopeID     string
	SampleLimit int // max mismatch samples retained per bucket; 0 → default
}

// defaultGoSampleLimit is used when GoOracleOptions.SampleLimit is 0.
const defaultGoSampleLimit = 10

// RunGoOracle builds independent static and CHA call graphs for the project
// via golang.org/x/tools and compares them against the indexed CALLS edges.
//
// The sandwich principle: static(direct calls) ⊆ graph CALLS ⊆ CHA(may-call).
// Edges in static missing from the graph are recall gaps; graph edges
// outside CHA are precision suspects. Both bounds come from go/types +
// go/ssa, sharing no code with the SCIP indexing pipeline.
func RunGoOracle(ctx context.Context, client *neo4j.Client, opts GoOracleOptions) (*OracleReport, error) {
	if opts.ProjectRoot == "" {
		return nil, fmt.Errorf("verify oracle --language=go: --project is required")
	}
	if opts.ServiceName == "" {
		return nil, fmt.Errorf("verify oracle --language=go: --service is required")
	}
	sampleLimit := opts.SampleLimit
	if sampleLimit <= 0 {
		sampleLimit = defaultGoSampleLimit
	}

	extraction, err := extractGoCallGraphs(opts.ProjectRoot)
	if err != nil {
		return nil, fmt.Errorf("extract Go call graphs: %w", err)
	}

	rows, err := fetchGoCallsEdges(ctx, client, opts.ServiceName, opts.ScopeID)
	if err != nil {
		return nil, err
	}
	join := joinGoGraphEdges(rows)

	nodeSymbols, err := fetchGoNodeSymbols(ctx, client, opts.ServiceName, opts.ScopeID)
	if err != nil {
		return nil, err
	}
	knownNodes := knownGoFuncIDs(nodeSymbols)

	cmp := compareGoCallGraphs(join.Edges, extraction.MustEdges, extraction.MayEdges, knownNodes)

	report := &OracleReport{
		Language:    "go",
		ServiceName: opts.ServiceName,
		GraphEdges:  len(join.Edges),
		MustEdges:   len(extraction.MustEdges),
		MayEdges:    len(extraction.MayEdges),
		Recall:      cmp.Recall,
	}

	for i, k := range cmp.Missing {
		if i >= sampleLimit {
			break
		}
		report.MissingFromGraph = append(report.MissingFromGraph, EdgeSample{
			From: funcIDString(k.from),
			To:   funcIDString(k.to),
			Note: "in static call graph, absent from indexed CALLS",
		})
	}
	for i, k := range cmp.PrecisionSuspects {
		if i >= sampleLimit {
			break
		}
		report.PrecisionSuspects = append(report.PrecisionSuspects, EdgeSample{
			From: funcIDString(k.from),
			To:   funcIDString(k.to),
			Note: "indexed CALLS edge outside CHA may-call graph",
		})
	}

	selfLoopMissing := 0
	for _, k := range cmp.Missing {
		if k.from == k.to {
			selfLoopMissing++
		}
	}

	report.Notes = append(report.Notes,
		fmt.Sprintf("module: %s", extraction.ModulePath),
		fmt.Sprintf("missing (recall gaps): %d total, %d shown", len(cmp.Missing), len(report.MissingFromGraph)),
		fmt.Sprintf("precision suspects: %d total, %d shown", len(cmp.PrecisionSuspects), len(report.PrecisionSuspects)),
		fmt.Sprintf("excluded synthetic/init/closure endpoints: %d edges", extraction.SyntheticExcluded),
		fmt.Sprintf("excluded cross-module edges (SSA side): %d", extraction.CrossModuleEdges),
		fmt.Sprintf("unmappable SSA functions (no stable pkg/type identity): %d", extraction.UnmappableFuncs),
		fmt.Sprintf("graph CALLS rows read: %d", len(rows)),
		fmt.Sprintf("graph edges excluded as abstract interface-method symbols: %d", join.Abstract),
		fmt.Sprintf("graph edges excluded as unmappable symbols: %d", join.Unmappable),
		fmt.Sprintf("stale-graph edges excluded (endpoint not yet indexed, both sides checked against graph node symbols): %d", cmp.StaleGraphEdges),
		fmt.Sprintf("self-loop (caller==callee) edges among missing: %d of %d — the indexer's collapseToMinLinePerPair "+
			"(internal/ingest/scip/call_graph_dedup.go:35) drops every self-call by design (`if t.CallerID == t.TargetID { continue }`), "+
			"shared by both the Go and generic call-graph builders, so self-recursion is a systematic, language-wide recall gap, not noise",
			selfLoopMissing, len(cmp.Missing)),
	)

	return report, nil
}
