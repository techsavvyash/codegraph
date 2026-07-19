package static

import (
	"fmt"
	"sort"
)

// IndexStats accumulates coverage counters for a single service's AST-detection run (P3-1).
//
// Its purpose is to make silent gaps LOUD. The failure mode that hid P0-1 for months — an
// entire outbound-HTTP wrapper (406 `apiclient` importers) producing ZERO HTTPCall nodes —
// is invisible in a graph: "no HTTP calls found" and "HTTP calls not detected" look identical.
// This report compares, per wrapper class, how many files IMPORT the wrapper against how many
// nodes were WRITTEN, and emits an explicit WARN when a whole class is present but unwritten.
//
// It also surfaces the DB silent-drop class (recognised query methods dropped for an untracked
// receiver / unresolvable SQL) and the P3-2 const-ambiguity count.
type IndexStats struct {
	serviceName string

	filesScanned     int
	functionsScanned int
	functionsSkipped int // had no Neo4j Function/Method node (funcID lookup miss)

	// importFiles counts, per wrapper class key, how many .go files import that package
	// (counted once per file). A class with importers > 0 but writes == 0 is the alarm.
	importFiles map[string]int

	// written holds node counts by graph label, snapshotted from the call buffer BEFORE
	// it is flushed and reset.
	written map[string]int

	// DB detector counters (P3-1), GENERIC driver path only (pgx/sqlx/gorm/mongo via a
	// tracked var or struct-field receiver). The Tazapay-repo pattern (repo.<Repo>.<Method>)
	// and pgxscan write DBCalls via separate paths, so written["DBCall"] (the buffer total)
	// = dbGenericWritten + repo-pattern + pgxscan. Comparing dbCandidatesSeen to the DBCall
	// total directly is meaningless (different populations) — the report keeps them separate.
	dbCandidatesSeen        int
	dbGenericWritten        int
	dbRejectedUntrackedRecv int
	dbRejectedUnresolvedSQL int

	// constAmbiguous is the P3-2 collision count (bare names with divergent literal values).
	constAmbiguous int
}

// coverageCheck ties a wrapper-import class key to the node label it is expected to produce.
type coverageCheck struct {
	importKey    string // key used in importFiles (also the human label)
	writtenLabel string // label used in written (a callNodeBuffer node type)
}

// coverageChecks drive both the import tally and the "imported but unwritten" WARNs.
var coverageChecks = []coverageCheck{
	{"apiclient (outbound HTTP)", "HTTPCall"},
	{"queue (SQS/outbox)", "OutboxCall"},
	{"cache (redis)", "CacheCall"},
	{"external wrappers", "ExternalCall"},
}

func newIndexStats(serviceName string) *IndexStats {
	return &IndexStats{
		serviceName: serviceName,
		importFiles: make(map[string]int),
		written:     make(map[string]int),
	}
}

// recordFileImports tallies, for the file whose import map is given, each wrapper class the
// file imports (once per file). importMap is alias → import path (from buildImportMap).
func (s *IndexStats) recordFileImports(importMap map[string]string) {
	if s == nil {
		return
	}
	s.filesScanned++
	paths := make(map[string]bool, len(importMap))
	for _, p := range importMap {
		paths[p] = true
	}
	if paths[apiClientImportPath] {
		s.importFiles["apiclient (outbound HTTP)"]++
	}
	if paths[queueClientPkgPath] {
		s.importFiles["queue (SQS/outbox)"]++
	}
	if paths[cacheImportPath] {
		s.importFiles["cache (redis)"]++
	}
	for path := range externalCallRegistry {
		if paths[path] {
			s.importFiles["external wrappers"]++
			break // count the file once even if it imports several external wrappers
		}
	}
}

// captureWritten snapshots node counts from the buffer. Call BEFORE flush (flush resets it).
func (s *IndexStats) captureWritten(b *callNodeBuffer) {
	if s == nil || b == nil {
		return
	}
	s.written["GRPCCall"] = len(b.grpcCalls)
	s.written["HTTPCall"] = len(b.httpCalls)
	s.written["OutboxCall"] = len(b.outboxCalls)
	s.written["EventType"] = len(b.eventTypes)
	s.written["DBCall"] = len(b.dbCalls)
	s.written["CacheCall"] = len(b.cacheCalls)
	s.written["ExternalCall"] = len(b.externalCalls)
}

// coverageWarnings returns one message per wrapper class that is imported by at least one file
// but produced zero nodes — the "a whole wrapper is invisible" alarm. Returned (rather than
// printed) so it is unit-testable; Report prints them.
func (s *IndexStats) coverageWarnings() []string {
	var warns []string
	for _, c := range coverageChecks {
		imported := s.importFiles[c.importKey]
		if imported > 0 && s.written[c.writtenLabel] == 0 {
			warns = append(warns, fmt.Sprintf(
				"%d file(s) import %s but 0 %s nodes were written — a whole wrapper class may be invisible",
				imported, c.importKey, c.writtenLabel))
		}
	}
	return warns
}

// Report prints the full per-run coverage telemetry block. It is verbose-only:
// the block is large, and the load-bearing part — the "a whole wrapper class is
// invisible" alarms — is surfaced separately (and always) via coverageWarnings,
// which the SCIP indexer routes into the summary block's footer.
func (s *IndexStats) Report() {
	if s == nil || !indexVerbose {
		return
	}
	fmt.Printf("\n  === Coverage telemetry: %s ===\n", s.serviceName)
	fmt.Printf("  Files scanned: %d  |  Functions: %d scanned, %d skipped (no graph node)\n",
		s.filesScanned, s.functionsScanned, s.functionsSkipped)

	// Nodes written, sorted for deterministic output.
	labels := make([]string, 0, len(s.written))
	for label := range s.written {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	fmt.Printf("  Nodes written:")
	for _, label := range labels {
		fmt.Printf(" %s=%d", label, s.written[label])
	}
	fmt.Printf("\n")

	// Wrapper imports vs writes.
	for _, c := range coverageChecks {
		fmt.Printf("  %-26s imported by %d file(s) → %d %s node(s)\n",
			c.importKey, s.importFiles[c.importKey], s.written[c.writtenLabel], c.writtenLabel)
	}

	// DB drop breakdown. The rejection counters apply ONLY to the generic driver path;
	// the DBCall TOTAL also includes the Tazapay-repo pattern (repo.<Repo>.<Method>) and
	// pgxscan, so it is reported on its own line to avoid a misleading "written > seen".
	repoAndScan := s.written["DBCall"] - s.dbGenericWritten
	fmt.Printf("  DB raw-driver path: %d candidates → %d written, rejected: %d untracked-receiver "+
		"(incl. non-DB same-named methods), %d unresolved-SQL\n",
		s.dbCandidatesSeen, s.dbGenericWritten, s.dbRejectedUntrackedRecv, s.dbRejectedUnresolvedSQL)
	fmt.Printf("  DB total written: %d (raw-driver %d + repo-pattern/pgxscan %d)\n",
		s.written["DBCall"], s.dbGenericWritten, repoAndScan)

	if s.constAmbiguous > 0 {
		fmt.Printf("  Const ambiguities (P3-2): %d bare name(s) with divergent values → resolved as not-static\n",
			s.constAmbiguous)
	}

	// Coverage WARNs are surfaced (always, non-verbose too) via reportCoverageWarnings
	// into the summary block, so they are intentionally not re-printed here.
	fmt.Printf("  === end telemetry ===\n")
}
