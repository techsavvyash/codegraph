// Package census implements the RFC-013 universal recall floor: tree-sitter
// declaration counts per file compared against Function/Method node counts in
// the graph. Language-independent; catches whole-file and whole-construct
// indexing dropouts.
package census

import (
	"context"
	"errors"
	"fmt"

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
//
// Go files are skipped entirely (structure.ForFile has no ".go" grammar
// registered in this package's usage — Go recall is the Go oracle's job,
// which type-checks the whole program rather than counting syntax). The
// census is read-only on the graph: one aggregate COUNT query, no writes.
func Run(ctx context.Context, client *neo4j.Client, opts Options) (*verify.Report, error) {
	if opts.ProjectRoot == "" {
		return nil, errors.New("census: ProjectRoot is required")
	}
	if opts.ServiceName == "" {
		return nil, errors.New("census: ServiceName is required")
	}
	scopeID := opts.ScopeID
	if scopeID == "" {
		scopeID = "main"
	}

	declared, err := WalkProject(opts.ProjectRoot)
	if err != nil {
		return nil, fmt.Errorf("census: walk project: %w", err)
	}

	graphCounts, err := fetchGraphDeclarationCounts(ctx, client, opts.ServiceName, scopeID)
	if err != nil {
		return nil, fmt.Errorf("census: fetch graph counts: %w", err)
	}

	knownFiles, err := fetchGraphFilePaths(ctx, client, opts.ServiceName, scopeID)
	if err != nil {
		return nil, fmt.Errorf("census: fetch graph file paths: %w", err)
	}

	statuses := CompareFiles(declared, graphCounts, knownFiles)

	scope := opts.ServiceName
	report := BuildReport(scope, statuses, opts.SampleLimit)

	// Go files are structurally out of scope for this census (no grammar
	// registered — see WalkProject) and reported explicitly so a reader
	// doesn't mistake silence for an unindexed language.
	report.Add(verify.CheckResult{
		Name:   "census: go files",
		Status: verify.StatusPass,
		Detail: "go files skipped (covered by go oracle)",
	})

	return report, nil
}

// fetchGraphDeclarationCounts returns, for the given service/scope, the
// number of Function+Method nodes per filePath — one Cypher query, no
// per-file round trips.
func fetchGraphDeclarationCounts(ctx context.Context, client *neo4j.Client, serviceName, scopeID string) (map[string]int, error) {
	query := `
		MATCH (n)
		WHERE (n:Function OR n:Method) AND n.serviceName = $serviceName AND n.scopeId = $scopeId
		RETURN n.filePath AS filePath, count(n) AS cnt
	`
	records, err := client.ExecuteQuery(ctx, query, map[string]any{
		"serviceName": serviceName,
		"scopeId":     scopeID,
	})
	if err != nil {
		return nil, err
	}

	counts := make(map[string]int, len(records))
	for _, rec := range records {
		m := rec.AsMap()
		filePath, _ := m["filePath"].(string)
		if filePath == "" {
			continue
		}
		switch v := m["cnt"].(type) {
		case int64:
			counts[filePath] = int(v)
		case int:
			counts[filePath] = v
		}
	}
	return counts, nil
}

// fetchGraphFilePaths returns the set of filePaths that exist as File nodes
// for the service — the basis for distinguishing "indexer saw the file and
// produced nothing" (whole-file dropout, fail) from "indexer never saw the
// file" (project exclusion, warn).
func fetchGraphFilePaths(ctx context.Context, client *neo4j.Client, serviceName, scopeID string) (map[string]bool, error) {
	query := `
		MATCH (f:File)
		WHERE f.serviceName = $serviceName AND f.scopeId = $scopeId
		RETURN f.filePath AS filePath
	`
	records, err := client.ExecuteQuery(ctx, query, map[string]any{
		"serviceName": serviceName,
		"scopeId":     scopeID,
	})
	if err != nil {
		return nil, err
	}

	known := make(map[string]bool, len(records))
	for _, rec := range records {
		m := rec.AsMap()
		if filePath, _ := m["filePath"].(string); filePath != "" {
			known[filePath] = true
		}
	}
	return known, nil
}
