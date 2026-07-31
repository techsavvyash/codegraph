// Package telemetry implements RFC-013 Layer 3: per-index-run quality
// counters stamped as IndexRun nodes, plus drift detection between
// consecutive runs of the same service.
package telemetry

import (
	"context"
	"encoding/json"
	"fmt"

	neo4j "github.com/context-maximiser/code-graph/internal/graph"
)

// runsToKeep is how many most-recent IndexRun nodes are retained per
// service; older ones are pruned after each RecordIndexRun.
const runsToKeep = 10

// RunRecord mirrors the IndexRun node stamped after every index run. Counters
// are computed from the graph itself (what actually landed), not from indexer
// bookkeeping.
type RunRecord struct {
	RunID               string           `json:"runId"`
	ServiceName         string           `json:"serviceName"`
	ScopeID             string           `json:"scopeId"`
	StartedAt           string           `json:"startedAt"`
	FinishedAt          string           `json:"finishedAt"`
	Files               int64            `json:"files"`
	Functions           int64            `json:"functions"`
	Methods             int64            `json:"methods"`
	Symbols             int64            `json:"symbols"`
	CallsEdges          int64            `json:"callsEdges"`
	ImplementsEdges     int64            `json:"implementsEdges"`
	APIRoutes           int64            `json:"apiRoutes"`
	CallsPerFunction    float64          `json:"callsPerFunction"`
	RangeSourceDist     map[string]int64 `json:"rangeSourceDist"`
	DetectionSourceDist map[string]int64 `json:"detectionSourceDist"`
	PromotedFunctions   int64            `json:"promotedFunctions"`
	DecoratedFunctions  int64            `json:"decoratedFunctions"`
}

// Drift is one counter delta between two runs that crossed the warning
// threshold.
type Drift struct {
	Counter  string  `json:"counter"`
	Previous float64 `json:"previous"`
	Current  float64 `json:"current"`
	Detail   string  `json:"detail,omitempty"`
}

// DriftReport compares the two most recent runs of a service.
type DriftReport struct {
	ServiceName string     `json:"serviceName"`
	Previous    *RunRecord `json:"previous,omitempty"`
	Current     *RunRecord `json:"current,omitempty"`
	Drifts      []Drift    `json:"drifts"`
}

// RecordIndexRun computes quality counters from the graph for the given
// service/scope, stamps an IndexRun node linked to the Service, prunes old
// runs (keep last 10), and returns the record. startedAt/finishedAt are
// RFC3339 timestamps supplied by the caller (the index pipeline).
func RecordIndexRun(ctx context.Context, client *neo4j.Client, serviceName, scopeID, startedAt, finishedAt string) (*RunRecord, error) {
	if serviceName == "" {
		return nil, fmt.Errorf("telemetry: serviceName is required")
	}
	if scopeID == "" {
		return nil, fmt.Errorf("telemetry: scopeID is required")
	}

	c, err := computeCounters(ctx, client, serviceName, scopeID)
	if err != nil {
		return nil, fmt.Errorf("telemetry: compute counters: %w", err)
	}

	record := &RunRecord{
		RunID:               serviceName + "@" + finishedAt,
		ServiceName:         serviceName,
		ScopeID:             scopeID,
		StartedAt:           startedAt,
		FinishedAt:          finishedAt,
		Files:               c.Files,
		Functions:           c.Functions,
		Methods:             c.Methods,
		Symbols:             c.Symbols,
		CallsEdges:          c.CallsEdges,
		ImplementsEdges:     c.ImplementsEdges,
		APIRoutes:           c.APIRoutes,
		CallsPerFunction:    callsPerFunction(c.CallsEdges, c.Functions, c.Methods),
		RangeSourceDist:     c.RangeSourceDist,
		DetectionSourceDist: c.DetectionSourceDist,
		PromotedFunctions:   c.PromotedFunctions,
		DecoratedFunctions:  c.DecoratedFunctions,
	}

	if err := persistRun(ctx, client, record); err != nil {
		return nil, fmt.Errorf("telemetry: persist run: %w", err)
	}

	if err := pruneOldRuns(ctx, client, serviceName); err != nil {
		return nil, fmt.Errorf("telemetry: prune old runs: %w", err)
	}

	return record, nil
}

// callsPerFunction divides calls by (functions+methods), guarding against
// division by zero.
func callsPerFunction(calls, functions, methods int64) float64 {
	denom := functions + methods
	if denom == 0 {
		return 0
	}
	return float64(calls) / float64(denom)
}

// persistRun stamps the IndexRun node and, when a Service node with this
// name and scopeId exists, links it via HAS_RUN. The IndexRun is created
// unconditionally either way, per RFC-013 ("create the IndexRun regardless").
func persistRun(ctx context.Context, client *neo4j.Client, r *RunRecord) error {
	rangeDistJSON, err := json.Marshal(r.RangeSourceDist)
	if err != nil {
		return fmt.Errorf("marshal rangeSourceDist: %w", err)
	}
	detectionDistJSON, err := json.Marshal(r.DetectionSourceDist)
	if err != nil {
		return fmt.Errorf("marshal detectionSourceDist: %w", err)
	}

	params := map[string]any{
		"runId":               r.RunID,
		"serviceName":         r.ServiceName,
		"scopeId":             r.ScopeID,
		"startedAt":           r.StartedAt,
		"finishedAt":          r.FinishedAt,
		"files":               r.Files,
		"functions":           r.Functions,
		"methods":             r.Methods,
		"symbols":             r.Symbols,
		"callsEdges":          r.CallsEdges,
		"implementsEdges":     r.ImplementsEdges,
		"apiRoutes":           r.APIRoutes,
		"callsPerFunction":    r.CallsPerFunction,
		"rangeSourceDist":     string(rangeDistJSON),
		"detectionSourceDist": string(detectionDistJSON),
		"promotedFunctions":   r.PromotedFunctions,
		"decoratedFunctions":  r.DecoratedFunctions,
	}

	err = client.ExecuteQueryWithoutRecords(ctx, `
		CREATE (run:IndexRun {
			runId: $runId,
			serviceName: $serviceName,
			scopeId: $scopeId,
			startedAt: $startedAt,
			finishedAt: $finishedAt,
			files: $files,
			functions: $functions,
			methods: $methods,
			symbols: $symbols,
			callsEdges: $callsEdges,
			implementsEdges: $implementsEdges,
			apiRoutes: $apiRoutes,
			callsPerFunction: $callsPerFunction,
			rangeSourceDist: $rangeSourceDist,
			detectionSourceDist: $detectionSourceDist,
			promotedFunctions: $promotedFunctions,
			decoratedFunctions: $decoratedFunctions
		})
		WITH run
		OPTIONAL MATCH (svc:Service {name: $serviceName, scopeId: $scopeId})
		FOREACH (_ IN CASE WHEN svc IS NOT NULL THEN [1] ELSE [] END |
			MERGE (svc)-[:HAS_RUN]->(run)
		)
	`, params)
	if err != nil {
		return err
	}
	return nil
}

// pruneOldRuns keeps only the runsToKeep most-recent IndexRun nodes for a
// service (ordered by finishedAt desc) and DETACH DELETEs the rest.
func pruneOldRuns(ctx context.Context, client *neo4j.Client, serviceName string) error {
	return client.ExecuteQueryWithoutRecords(ctx, `
		MATCH (run:IndexRun {serviceName: $serviceName})
		WITH run ORDER BY run.finishedAt DESC
		SKIP $keep
		DETACH DELETE run
	`, map[string]any{"serviceName": serviceName, "keep": int64(runsToKeep)})
}

// DiffLastRuns loads the two most recent IndexRun nodes for the service and
// reports drift beyond thresholds (default ±25%, distribution keys
// appearing/disappearing).
func DiffLastRuns(ctx context.Context, client *neo4j.Client, serviceName string) (*DriftReport, error) {
	runs, err := ListRuns(ctx, client, serviceName, 2)
	if err != nil {
		return nil, err
	}

	report := &DriftReport{ServiceName: serviceName}
	switch len(runs) {
	case 0:
		return report, nil
	case 1:
		report.Current = runs[0]
		return report, nil
	default:
		report.Current = runs[0]
		report.Previous = runs[1]
		report.Drifts = diffRuns(report.Previous, report.Current)
		return report, nil
	}
}

// ListRuns returns up to limit most-recent runs for the service, newest first.
func ListRuns(ctx context.Context, client *neo4j.Client, serviceName string, limit int) ([]*RunRecord, error) {
	if limit <= 0 {
		limit = 10
	}

	records, err := client.ExecuteQuery(ctx, `
		MATCH (run:IndexRun {serviceName: $serviceName})
		RETURN run
		ORDER BY run.finishedAt DESC
		LIMIT $limit
	`, map[string]any{"serviceName": serviceName, "limit": int64(limit)})
	if err != nil {
		return nil, fmt.Errorf("telemetry: list runs: %w", err)
	}

	runs := make([]*RunRecord, 0, len(records))
	for _, rec := range records {
		node, ok := rec.AsMap()["run"]
		if !ok {
			continue
		}
		props, err := nodeProps(node)
		if err != nil {
			return nil, fmt.Errorf("telemetry: decode IndexRun node: %w", err)
		}
		rr, err := recordFromProps(props)
		if err != nil {
			return nil, fmt.Errorf("telemetry: decode IndexRun node: %w", err)
		}
		runs = append(runs, rr)
	}
	return runs, nil
}
