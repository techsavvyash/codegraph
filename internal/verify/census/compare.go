// compare.go joins tree-sitter per-file declaration counts (walk.go)
// against graph Function/Method node counts per file, and classifies each
// file as pass/warn/fail. Pure functions, no Neo4j — testable in isolation.
package census

import (
	"fmt"
	"sort"

	"github.com/context-maximiser/code-graph/internal/verify"
)

// FileStatus is one file's census outcome.
type FileStatus struct {
	FilePath string
	Declared int // tree-sitter named-declaration count
	Indexed  int // graph Function+Method node count for this file
	Status   verify.Status
}

// CompareFiles joins declared (tree-sitter) counts with indexed (graph)
// counts by file path and classifies each file:
//
//   - pass: graphCount >= tsCount (extra graph nodes are fine — overloads,
//     promoted arrows counted differently, etc.)
//   - warn: 0 < graphCount < tsCount (partial dropout — some but not all
//     declarations made it into the graph)
//   - fail: tsCount > 0 && graphCount == 0 (whole-file dropout — nothing
//     from this file is in the graph at all)
//
// Files with tsCount == 0 (no named declarations found by tree-sitter) are
// never reported: an empty file, a pure-type/interface file, or a file
// tree-sitter parsed with zero named functions is not a recall signal
// regardless of what the graph says about it.
func CompareFiles(declared []FileCensus, graphCounts map[string]int) []FileStatus {
	var out []FileStatus
	for _, fc := range declared {
		if fc.Declared == 0 {
			continue
		}
		graphCount := graphCounts[fc.RelPath]

		status := verify.StatusPass
		switch {
		case graphCount == 0:
			status = verify.StatusFail
		case graphCount < fc.Declared:
			status = verify.StatusWarn
		}

		out = append(out, FileStatus{
			FilePath: fc.RelPath,
			Declared: fc.Declared,
			Indexed:  graphCount,
			Status:   status,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].FilePath < out[j].FilePath })
	return out
}

// BuildReport aggregates per-file FileStatus results into a verify.Report:
// one summary check (files compared, total declared vs indexed), one for
// whole-file dropouts (fail), one for partial dropouts (warn). sampleLimit
// bounds the number of file samples attached to each non-passing check.
func BuildReport(scope string, statuses []FileStatus, sampleLimit int) *verify.Report {
	if sampleLimit <= 0 {
		sampleLimit = 5
	}

	report := &verify.Report{Scope: scope}

	var totalDeclared, totalIndexed int64
	var failCount, warnCount int64
	var failSamples, warnSamples []string

	for _, s := range statuses {
		totalDeclared += int64(s.Declared)
		totalIndexed += int64(s.Indexed)
		switch s.Status {
		case verify.StatusFail:
			failCount++
			if len(failSamples) < sampleLimit {
				failSamples = append(failSamples, fmt.Sprintf("%s: %d declared, 0 indexed", s.FilePath, s.Declared))
			}
		case verify.StatusWarn:
			warnCount++
			if len(warnSamples) < sampleLimit {
				warnSamples = append(warnSamples, fmt.Sprintf("%s: %d declared, %d indexed", s.FilePath, s.Declared, s.Indexed))
			}
		}
	}

	summaryStatus := verify.StatusPass
	if failCount > 0 {
		summaryStatus = verify.StatusFail
	} else if warnCount > 0 {
		summaryStatus = verify.StatusWarn
	}
	report.Add(verify.CheckResult{
		Name:   "census: declared vs indexed",
		Status: summaryStatus,
		Detail: fmt.Sprintf("%d files compared, %d declarations found by tree-sitter, %d Function/Method nodes in the graph", len(statuses), totalDeclared, totalIndexed),
		Count:  int64(len(statuses)),
	})

	whollyDroppedStatus := verify.StatusPass
	if failCount > 0 {
		whollyDroppedStatus = verify.StatusFail
	}
	report.Add(verify.CheckResult{
		Name:    "census: whole-file dropouts",
		Status:  whollyDroppedStatus,
		Detail:  "files with named declarations but zero matching Function/Method nodes in the graph",
		Count:   failCount,
		Samples: failSamples,
	})

	partialStatus := verify.StatusPass
	if warnCount > 0 {
		partialStatus = verify.StatusWarn
	}
	report.Add(verify.CheckResult{
		Name:    "census: partial dropouts",
		Status:  partialStatus,
		Detail:  "files where the graph has fewer Function/Method nodes than tree-sitter found named declarations",
		Count:   warnCount,
		Samples: warnSamples,
	})

	return report
}
