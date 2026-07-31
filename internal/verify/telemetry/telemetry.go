// Package telemetry implements RFC-013 Layer 3: per-index-run quality
// counters stamped as IndexRun nodes, plus drift detection between
// consecutive runs of the same service.
package telemetry

import (
	"context"
	"errors"

	neo4j "github.com/context-maximiser/code-graph/internal/graph"
)

// RunRecord mirrors the IndexRun node stamped after every index run. Counters
// are computed from the graph itself (what actually landed), not from indexer
// bookkeeping.
type RunRecord struct {
	RunID               string             `json:"runId"`
	ServiceName         string             `json:"serviceName"`
	ScopeID             string             `json:"scopeId"`
	StartedAt           string             `json:"startedAt"`
	FinishedAt          string             `json:"finishedAt"`
	Files               int64              `json:"files"`
	Functions           int64              `json:"functions"`
	Methods             int64              `json:"methods"`
	Symbols             int64              `json:"symbols"`
	CallsEdges          int64              `json:"callsEdges"`
	ImplementsEdges     int64              `json:"implementsEdges"`
	APIRoutes           int64              `json:"apiRoutes"`
	CallsPerFunction    float64            `json:"callsPerFunction"`
	RangeSourceDist     map[string]int64   `json:"rangeSourceDist"`
	DetectionSourceDist map[string]int64   `json:"detectionSourceDist"`
	PromotedFunctions   int64              `json:"promotedFunctions"`
	DecoratedFunctions  int64              `json:"decoratedFunctions"`
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
	ServiceName string  `json:"serviceName"`
	Previous    *RunRecord `json:"previous,omitempty"`
	Current     *RunRecord `json:"current,omitempty"`
	Drifts      []Drift `json:"drifts"`
}

// RecordIndexRun computes quality counters from the graph for the given
// service/scope, stamps an IndexRun node linked to the Service, prunes old
// runs (keep last 10), and returns the record. startedAt/finishedAt are
// RFC3339 timestamps supplied by the caller (the index pipeline).
func RecordIndexRun(ctx context.Context, client *neo4j.Client, serviceName, scopeID, startedAt, finishedAt string) (*RunRecord, error) {
	return nil, errors.New("telemetry: not implemented yet (RFC-013 Layer 3)")
}

// DiffLastRuns loads the two most recent IndexRun nodes for the service and
// reports drift beyond thresholds (default ±25%, distribution keys
// appearing/disappearing).
func DiffLastRuns(ctx context.Context, client *neo4j.Client, serviceName string) (*DriftReport, error) {
	return nil, errors.New("telemetry: not implemented yet (RFC-013 Layer 3)")
}

// ListRuns returns up to limit most-recent runs for the service, newest first.
func ListRuns(ctx context.Context, client *neo4j.Client, serviceName string, limit int) ([]*RunRecord, error) {
	return nil, errors.New("telemetry: not implemented yet (RFC-013 Layer 3)")
}
