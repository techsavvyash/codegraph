// tsoracle.go implements RFC-013 Layer 2's TypeScript sampled differential
// oracle: it runs tools/ts-oracle/oracle.mjs against the target project
// (the project's own TypeScript compiler resolves every sampled call site),
// then joins each sampled site onto the indexed graph's CALLS edges and
// reports a recall estimate.
//
// This mirrors internal/ingest/resolve/tsresolver.go's design (external
// Node.js process, JSON over a temp file, sentinel errors for
// environment-missing conditions) but the join direction is different: the
// resolver joins onto SCIP symbol strings before indexing; the oracle joins
// onto CALLS edges already committed to the graph, fetched once and
// compared in Go rather than per-site Cypher.
package oracle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	neo4j "github.com/context-maximiser/code-graph/internal/graph"
)

// TSOracleOptions configures a sampled TypeScript differential run.
type TSOracleOptions struct {
	ProjectRoot string
	ServiceName string
	ScopeID     string
	ScriptPath  string // resolved tools/ts-oracle/oracle.mjs; empty → auto-locate
	SampleSize  int    // call sites to sample; 0 → default (matches oracle.mjs's own default)
	SampleLimit int    // max uncovered-site samples retained in the report; 0 → default
}

const (
	defaultTSSampleSize  = 200
	defaultTSSampleLimit = 5
	tsOracleTimeout      = 120 * time.Second
)

// TSCallSite is one sampled, compiler-resolved call site from oracle.mjs's
// "sites" array.
type TSCallSite struct {
	CallerFile      string `json:"callerFile"`
	CallerName      string `json:"callerName"`
	CallerContainer string `json:"callerContainer"`
	CallerLine      int    `json:"callerLine"`
	CalleeFile      string `json:"calleeFile"`
	CalleeName      string `json:"calleeName"`
	CalleeContainer string `json:"calleeContainer"`
	CalleeLine      int    `json:"calleeLine"`
}

// TSOracleStats mirrors oracle.mjs's "stats" object.
type TSOracleStats struct {
	FilesScanned           int `json:"filesScanned"`
	CallSitesSeen          int `json:"callSitesSeen"`
	Qualifying             int `json:"qualifying"`
	Sampled                int `json:"sampled"`
	SkippedExternal        int `json:"skippedExternal"`
	SkippedAnonymousCaller int `json:"skippedAnonymousCaller"`
	SkippedAnonymousCallee int `json:"skippedAnonymousCallee"`
	SkippedUnresolved      int `json:"skippedUnresolved"`
	SkippedNoEnclosure     int `json:"skippedNoEnclosure"`
	SkippedSuperOrDynamic  int `json:"skippedSuperOrDynamic"`
}

// TSOracleOutput is the full JSON document oracle.mjs writes to stdout/--out.
type TSOracleOutput struct {
	Sites []TSCallSite  `json:"sites"`
	Stats TSOracleStats `json:"stats"`
}

// ParseTSOracleOutput unmarshals oracle.mjs's JSON output.
func ParseTSOracleOutput(jsonBytes []byte) (*TSOracleOutput, error) {
	var out TSOracleOutput
	if err := json.Unmarshal(jsonBytes, &out); err != nil {
		return nil, fmt.Errorf("parse ts-oracle output: %w", err)
	}
	return &out, nil
}

// Sentinel errors, mirroring internal/ingest/resolve/tsresolver.go's
// contract: callers treat these as best-effort skips rather than hard
// failures.
var (
	// ErrTSOracleEnvironmentMissing wraps exit code 2 (no `typescript`
	// resolvable in the target project) and the "node not in PATH" /
	// "script not found" cases the runner checks before shelling out.
	ErrTSOracleEnvironmentMissing = errors.New("ts-oracle environment missing")
	// ErrTSOracleVersionTooOld wraps exit code 3 (typescript older than the
	// oracle's floor).
	ErrTSOracleVersionTooOld = errors.New("ts-oracle: typescript version too old")
)

// locateTSOracleScript finds tools/ts-oracle/oracle.mjs, checked in this
// order:
//  1. CODEGRAPH_TS_ORACLE env var, if set (explicit override).
//  2. <dir of the running executable>/../tools/ts-oracle/oracle.mjs (the
//     layout produced when the repo's tools/ directory is shipped alongside
//     the built binary — matches locateTSResolverScript's convention).
//  3. A dev-tree fallback: walk up from the current working directory
//     looking for go.mod (repo root), then tools/ts-oracle/oracle.mjs
//     beneath it — covers `go run`/`go test`/`codegraph` invoked straight
//     from a repo checkout, where step 2's exe-relative path doesn't apply
//     (the binary lives under a module cache or a temp build dir, not next
//     to a shipped tools/ directory).
//
// Returns "" (never an error) when nothing resolves.
func locateTSOracleScript() string {
	if envPath := os.Getenv("CODEGRAPH_TS_ORACLE"); envPath != "" {
		if _, err := os.Stat(envPath); err == nil {
			return envPath
		}
		return ""
	}

	if exePath, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(exePath), "..", "tools", "ts-oracle", "oracle.mjs")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}

	if wd, err := os.Getwd(); err == nil {
		dir := wd
		for {
			if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
				candidate := filepath.Join(dir, "tools", "ts-oracle", "oracle.mjs")
				if _, err := os.Stat(candidate); err == nil {
					return candidate
				}
				break
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}

	return ""
}

// RunTSOracleScript executes `node <scriptPath> --project <projectRoot>
// --sample-size <n> --out <tmpfile>` with the given timeout, then parses
// and returns its output.
//
// Exit code contract (see tools/ts-oracle/oracle.mjs's header comment):
//
//	0  success -> parse --out and return it
//	1  usage error -> real failure (should not happen; args are controlled here)
//	2  typescript module not found -> ErrTSOracleEnvironmentMissing (skip, warn)
//	3  typescript version too old -> ErrTSOracleVersionTooOld (skip, warn)
//	4  unexpected internal failure -> real failure, returned as-is
//
// This function does not itself check for `node` in PATH or resolve the
// script path — that is the caller's responsibility, mirroring
// RunTSResolver's split of concerns.
func RunTSOracleScript(ctx context.Context, scriptPath, projectRoot string, sampleSize int, timeout time.Duration) (*TSOracleOutput, error) {
	runCtx := ctx
	var cancel context.CancelFunc
	if timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	outFile, err := os.CreateTemp("", "ts-oracle-*.json")
	if err != nil {
		return nil, fmt.Errorf("create temp output file: %w", err)
	}
	outPath := outFile.Name()
	outFile.Close()
	defer os.Remove(outPath)

	absProjectRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		absProjectRoot = projectRoot
	}

	if sampleSize <= 0 {
		sampleSize = defaultTSSampleSize
	}

	cmd := exec.CommandContext(runCtx, "node", scriptPath,
		"--project", absProjectRoot,
		"--sample-size", fmt.Sprintf("%d", sampleSize),
		"--out", outPath,
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	runErr := cmd.Run()

	if runCtx.Err() != nil && errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return nil, fmt.Errorf("ts-oracle timed out after %s running %s: %w", timeout, scriptPath, runCtx.Err())
	}

	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			code := exitErr.ExitCode()
			switch code {
			case 2:
				return nil, fmt.Errorf("%w: %s", ErrTSOracleEnvironmentMissing, strings.TrimSpace(stderr.String()))
			case 3:
				return nil, fmt.Errorf("%w: %s", ErrTSOracleVersionTooOld, strings.TrimSpace(stderr.String()))
			default:
				return nil, fmt.Errorf("ts-oracle exited %d: %s", code, strings.TrimSpace(stderr.String()))
			}
		}
		return nil, fmt.Errorf("ts-oracle failed to run: %w (stderr: %s)", runErr, strings.TrimSpace(stderr.String()))
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		return nil, fmt.Errorf("read ts-oracle output %s: %w", outPath, err)
	}
	return ParseTSOracleOutput(data)
}

// tsClassFromSignature extracts the enclosing class name from a
// scip-typescript symbol string of the form
// `<dir>/`<file>`/ClassName#methodName().` — the only place class identity
// is recorded for Method nodes (there is no separate container property and
// no Class→Method CONTAINS edge; confirmed empirically against the live
// khaata/backend graph). Returns "" when the signature has no `/Type#`
// segment (Function nodes, or a signature shape we don't recognize) —
// callers then treat the node as a top-level function, matching how
// oracle.mjs reports callerContainer/calleeContainer "" for non-methods.
//
// This is a narrower, purpose-built cousin of
// internal/ingest/resolve/parseTSSymbolDescriptor: that parser rejects
// nested containers and property-vs-method trailing shapes to build an
// unambiguous join key for IMPLEMENTS edges; this one only needs the class
// name segment between the closing backtick and the LAST '#' (nested
// containers still yield a usable outer class name for our purposes, since
// the join is keyed on file+name+container, not on precise SCIP symbol
// identity).
func tsClassFromSignature(signature string) string {
	backtickEnd := strings.LastIndex(signature, "`")
	if backtickEnd < 0 {
		return ""
	}
	rest := signature[backtickEnd+1:]
	rest = strings.TrimPrefix(rest, "/")
	hashIdx := strings.Index(rest, "#")
	if hashIdx <= 0 {
		return ""
	}
	return rest[:hashIdx]
}

// fetchCallsIndex loads every CALLS edge for the service once and builds a
// set of (callerKey -> calleeKey) pairs, so per-site lookups in Go never
// touch Neo4j again. Function/Method nodes are fetched together (CALLS
// connects both labels interchangeably in the live graph) with their
// filePath, name, and signature (from which the container is derived).
func fetchCallsIndex(ctx context.Context, client *neo4j.Client, serviceName, scopeID string) (edges map[string]map[string]bool, nodeExists map[string]bool, err error) {
	query := `
		MATCH (caller)-[:CALLS]->(callee)
		WHERE (caller:Function OR caller:Method) AND (callee:Function OR callee:Method)
		  AND caller.serviceName = $serviceName AND callee.serviceName = $serviceName
		  AND caller.scopeId = $scopeId AND callee.scopeId = $scopeId
		RETURN caller.filePath AS callerFile, caller.name AS callerName, caller.signature AS callerSig,
		       callee.filePath AS calleeFile, callee.name AS calleeName, callee.signature AS calleeSig
	`
	records, err := client.ExecuteQuery(ctx, query, map[string]any{
		"serviceName": serviceName,
		"scopeId":     scopeID,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("fetch CALLS edges: %w", err)
	}

	edges = make(map[string]map[string]bool)
	nodeExists = make(map[string]bool)

	for _, rec := range records {
		m := rec.AsMap()
		callerFile, _ := m["callerFile"].(string)
		callerName, _ := m["callerName"].(string)
		callerSig, _ := m["callerSig"].(string)
		calleeFile, _ := m["calleeFile"].(string)
		calleeName, _ := m["calleeName"].(string)
		calleeSig, _ := m["calleeSig"].(string)

		callerKey := tsCallEndpointKey(callerFile, callerName, tsClassFromSignature(callerSig))
		calleeKey := tsCallEndpointKey(calleeFile, calleeName, tsClassFromSignature(calleeSig))

		nodeExists[callerKey] = true
		nodeExists[calleeKey] = true

		if edges[callerKey] == nil {
			edges[callerKey] = make(map[string]bool)
		}
		edges[callerKey][calleeKey] = true
	}

	// Also index every known Function/Method node's identity independently
	// of CALLS participation, so "node missing entirely" (census territory)
	// is distinguishable from "node exists but has no matching edge"
	// (precision/recall territory). A node with zero in/out CALLS edges
	// never appears in the query above.
	nodesQuery := `
		MATCH (n)
		WHERE (n:Function OR n:Method) AND n.serviceName = $serviceName AND n.scopeId = $scopeId
		RETURN n.filePath AS filePath, n.name AS name, n.signature AS signature
	`
	nodeRecords, err := client.ExecuteQuery(ctx, nodesQuery, map[string]any{
		"serviceName": serviceName,
		"scopeId":     scopeID,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("fetch Function/Method nodes: %w", err)
	}
	for _, rec := range nodeRecords {
		m := rec.AsMap()
		filePath, _ := m["filePath"].(string)
		name, _ := m["name"].(string)
		signature, _ := m["signature"].(string)
		key := tsCallEndpointKey(filePath, name, tsClassFromSignature(signature))
		nodeExists[key] = true
	}

	return edges, nodeExists, nil
}

// tsCallEndpointKey normalizes a (filePath, name, container) triple into
// the join key used on both sides: the sampled site (from oracle.mjs, whose
// container is "" for top-level functions/const-arrows) and the graph node
// (whose container is derived from its SCIP signature by
// tsClassFromSignature, likewise "" for Function nodes).
func tsCallEndpointKey(filePath, name, container string) string {
	return filePath + "\x00" + container + "\x00" + name
}

// RunTSOracle samples compiler-resolved call sites from the target project
// (via tools/ts-oracle/oracle.mjs) and checks each against the indexed
// CALLS edges.
//
// Recall is computed as covered / (sampled - missingNodes), per the brief:
// sites whose caller or callee node doesn't exist in the graph at all are a
// census concern (whole-file/whole-construct dropout), not a precision or
// recall signal for THIS oracle — including them in the denominator would
// conflate "the call resolution is wrong" with "the node was never
// indexed".
func RunTSOracle(ctx context.Context, client *neo4j.Client, opts TSOracleOptions) (*OracleReport, error) {
	if opts.ProjectRoot == "" {
		return nil, errors.New("ts oracle: ProjectRoot is required")
	}
	if opts.ServiceName == "" {
		return nil, errors.New("ts oracle: ServiceName is required")
	}

	scopeID := opts.ScopeID
	if scopeID == "" {
		scopeID = "main"
	}
	sampleLimit := opts.SampleLimit
	if sampleLimit <= 0 {
		sampleLimit = defaultTSSampleLimit
	}

	report := &OracleReport{
		Language:    "typescript",
		ServiceName: opts.ServiceName,
	}

	if _, err := exec.LookPath("node"); err != nil {
		return nil, fmt.Errorf("%w: 'node' not found in PATH", ErrTSOracleEnvironmentMissing)
	}

	scriptPath := opts.ScriptPath
	if scriptPath == "" {
		scriptPath = locateTSOracleScript()
	}
	if scriptPath == "" {
		return nil, fmt.Errorf("%w: tools/ts-oracle/oracle.mjs not found (set CODEGRAPH_TS_ORACLE or ship tools/ts-oracle alongside the binary)", ErrTSOracleEnvironmentMissing)
	}

	output, err := RunTSOracleScript(ctx, scriptPath, opts.ProjectRoot, opts.SampleSize, tsOracleTimeout)
	if err != nil {
		return nil, err
	}

	edges, nodeExists, err := fetchCallsIndex(ctx, client, opts.ServiceName, scopeID)
	if err != nil {
		return nil, err
	}

	graphEdgeCount := 0
	for _, callees := range edges {
		graphEdgeCount += len(callees)
	}
	report.GraphEdges = graphEdgeCount
	report.SampledSites = len(output.Sites)

	var covered, missingNodes int
	var missingSamples []EdgeSample

	for _, site := range output.Sites {
		callerKey := tsCallEndpointKey(site.CallerFile, site.CallerName, site.CallerContainer)
		calleeKey := tsCallEndpointKey(site.CalleeFile, site.CalleeName, site.CalleeContainer)

		callerExists := nodeExists[callerKey]
		calleeExists := nodeExists[calleeKey]
		if !callerExists || !calleeExists {
			missingNodes++
			continue
		}

		if edges[callerKey] != nil && edges[callerKey][calleeKey] {
			covered++
			continue
		}

		if len(missingSamples) < sampleLimit {
			missingSamples = append(missingSamples, EdgeSample{
				From: formatTSEndpoint(site.CallerFile, site.CallerContainer, site.CallerName, site.CallerLine),
				To:   formatTSEndpoint(site.CalleeFile, site.CalleeContainer, site.CalleeName, site.CalleeLine),
				Note: "sampled call site resolved by the TypeScript compiler; no CALLS edge found in the graph",
			})
		}
	}
	report.ResolvedSites = covered
	report.MissingFromGraph = missingSamples

	denominator := len(output.Sites) - missingNodes
	if denominator > 0 {
		report.Recall = float64(covered) / float64(denominator)
	}

	report.Notes = append(report.Notes,
		fmt.Sprintf("sample size requested=%d, qualifying call sites in project=%d, sampled=%d",
			opts.SampleSize, output.Stats.Qualifying, output.Stats.Sampled),
		fmt.Sprintf("skip taxonomy: external=%d, anonymousCaller=%d, anonymousCallee=%d, unresolved=%d, noEnclosure=%d, superOrDynamic=%d",
			output.Stats.SkippedExternal, output.Stats.SkippedAnonymousCaller, output.Stats.SkippedAnonymousCallee,
			output.Stats.SkippedUnresolved, output.Stats.SkippedNoEnclosure, output.Stats.SkippedSuperOrDynamic),
		fmt.Sprintf("%d/%d sampled sites excluded from the recall denominator: caller or callee node missing from the graph entirely (census territory, not a recall/precision signal for this oracle)",
			missingNodes, len(output.Sites)),
	)

	return report, nil
}

func formatTSEndpoint(file, container, name string, line int) string {
	if container != "" {
		return fmt.Sprintf("%s:%s#%s:%d", file, container, name, line)
	}
	return fmt.Sprintf("%s#%s:%d", file, name, line)
}
