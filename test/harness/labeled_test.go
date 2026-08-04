// labeled_test.go implements RFC-013 Layer 4: labeled truth fixtures.
//
// Unlike golden_test.go (which detects *change* via snapshot diffing),
// this file hand-verifies exact presence/absence of nodes and edges against
// two small fixture projects (test/fixtures/labeled-go,
// test/fixtures/labeled-ts). Every expectation in test/harness/labeled/*.json
// was derived by reading the fixture source and independently confirming the
// pipeline's actual output via ad hoc Cypher before being written down — see
// the comment field on each expectation for the verification note.
//
// Cleanup is fully self-contained (service names "labeledgo"/"labeledts",
// deliberately disjoint from golden_test.go's fixtureServices) so this file
// can never interact with golden/isolation test cleanup in either direction.
package harness_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	neo4j "github.com/context-maximiser/code-graph/internal/graph"
	"github.com/context-maximiser/code-graph/internal/ingest/resolve"
	static "github.com/context-maximiser/code-graph/internal/ingest/scip"
	"github.com/context-maximiser/code-graph/internal/query/reachability"
	neo4jdrv "github.com/neo4j/neo4j-go-driver/v5/neo4j"
	"github.com/stretchr/testify/require"
)

// labeledServices are the service names the labeled fixtures index under.
// Deliberately disjoint from golden_test.go's fixtureServices so cleanup
// here can never touch (or be touched by) golden/isolation test data.
var labeledServices = []string{"labeledgo", "labeledts"}

// labeledModuleMarkers scope cleanup of shared, FQN-merged node types
// (Symbol/Class/Interface/Module) that carry no serviceName property —
// mirrors golden_test.go's fixtureModuleMarkers but for the labeled
// fixtures' own module identifiers.
var labeledModuleMarkers = []string{"example.com/labeledgo", "labeled-ts"}

// ---- Expectation JSON schema -------------------------------------------

// nodeSpec identifies a node (or a class of nodes) by label/name/file-suffix
// plus an optional property subset. Any zero-value field is a wildcard.
// NodeKeyContains disambiguates same-name/same-file nodes (e.g. two methods
// named "Save" on different receiver types in one file) using a substring of
// the SCIP-derived nodeKey — a signal independent of the Go AST body-range
// enrichment pass, so it stays reliable even when THAT pass mis-attributes
// properties like receiverType (see labeled-go.json's knownGap entries).
type nodeSpec struct {
	Label           string         `json:"label"`
	Name            string         `json:"name"`
	File            string         `json:"file"`
	NodeKeyContains string         `json:"nodeKeyContains"`
	Props           map[string]any `json:"props"`
}

// nodeExpectation is a mustHaveNodes/mustNotHaveNodes entry.
type nodeExpectation struct {
	nodeSpec
	DecoratorContains string `json:"decoratorContains"`
	KnownGap          bool   `json:"knownGap"`
	Comment           string `json:"comment"`
}

// edgeExpectation is a mustHaveEdges/mustNotHaveEdges entry. From/To are
// nodeSpecs identifying the endpoints; either may be a zero-value nodeSpec
// (matches any node) to express "no edge of this type touches this named
// node from/to anywhere," used for the generics and option-bag negative
// checks where no single concrete counterpart node exists.
type edgeExpectation struct {
	Type     string   `json:"type"`
	From     nodeSpec `json:"from"`
	To       nodeSpec `json:"to"`
	KnownGap bool     `json:"knownGap"`
	Comment  string   `json:"comment"`
}

// labeledExpectations is the full contents of one test/harness/labeled/*.json file.
type labeledExpectations struct {
	Fixture          string            `json:"fixture"`
	Service          string            `json:"service"`
	MustHaveNodes    []nodeExpectation `json:"mustHaveNodes"`
	MustNotHaveNodes []nodeExpectation `json:"mustNotHaveNodes"`
	MustHaveEdges    []edgeExpectation `json:"mustHaveEdges"`
	MustNotHaveEdges []edgeExpectation `json:"mustNotHaveEdges"`
}

func loadExpectations(t *testing.T, path string) *labeledExpectations {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err, "read expectations %s", path)
	var exp labeledExpectations
	require.NoError(t, json.Unmarshal(data, &exp), "parse expectations %s", path)
	return &exp
}

// ---- Matcher -------------------------------------------------------------

// labeledMatcher runs nodeSpec/edgeExpectation queries against Neo4j,
// scoped to a single fixture's serviceName + module markers.
type labeledMatcher struct {
	t       *testing.T
	ctx     context.Context
	client  *neo4j.Client
	service string
}

// nodeWhereClause renders a Cypher WHERE fragment (using node variable v)
// plus its bound params (prefixed with paramPrefix to avoid collisions when
// both endpoints of an edge query are rendered together) for a nodeSpec.
// Ownership is always applied so fixture queries never match unrelated
// dev-graph nodes that happen to share a name like "Options" or "run". Three
// legs, mirroring harness.Ownership in snapshot.go: serviceName == service,
// nodeKey CONTAINS a labeled marker (shared FQN-merged types), or directly
// connected to a service-owned node — the last leg is required for
// structural APIRoute nodes (decorator-detected routes carry neither
// serviceName nor a module-marker-bearing nodeKey; they key on a bare
// "api:METHOD:/path" string), which are otherwise unreachable through the
// first two legs alone.
func (m *labeledMatcher) nodeWhereClause(v, paramPrefix string, spec nodeSpec) (string, map[string]any) {
	conds := []string{fmt.Sprintf(`(
		(%[1]s.serviceName = $%[2]sservice)
		OR any(mk IN $%[2]smarkers WHERE %[1]s.nodeKey CONTAINS mk)
		OR EXISTS {
		     MATCH (%[1]s)--(nbr)
		     WHERE nbr.serviceName = $%[2]sservice
		   }
	)`, v, paramPrefix)}
	params := map[string]any{
		paramPrefix + "service": m.service,
		paramPrefix + "markers": labeledModuleMarkers,
	}
	if spec.Label != "" {
		conds = append(conds, fmt.Sprintf("%s:`%s`", v, spec.Label))
	}
	if spec.Name != "" {
		conds = append(conds, fmt.Sprintf("%s.name = $%sname", v, paramPrefix))
		params[paramPrefix+"name"] = spec.Name
	}
	if spec.File != "" {
		// File nodes carry `path`; code nodes carry `filePath`. coalesce
		// lets one spec shape address both (needed for File-CALLS module-
		// scope caller assertions).
		conds = append(conds, fmt.Sprintf("coalesce(%[1]s.filePath, %[1]s.path) ENDS WITH $%[2]sfile", v, paramPrefix))
		params[paramPrefix+"file"] = spec.File
	}
	if spec.NodeKeyContains != "" {
		conds = append(conds, fmt.Sprintf("%s.nodeKey CONTAINS $%snkc", v, paramPrefix))
		params[paramPrefix+"nkc"] = spec.NodeKeyContains
	}
	for k, val := range spec.Props {
		pname := paramPrefix + "prop_" + sanitizeParamKey(k)
		conds = append(conds, fmt.Sprintf("%s.%s = $%s", v, backtickIfNeeded(k), pname))
		params[pname] = val
	}
	return strings.Join(conds, " AND "), params
}

func sanitizeParamKey(k string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			return r
		}
		return '_'
	}, k)
}

func backtickIfNeeded(k string) string {
	// Property keys in this codebase are always valid identifiers
	// (camelCase Go/JS field names); no escaping needed in practice, but
	// backtick defensively so an unexpected key never breaks the query.
	return "`" + k + "`"
}

// candidateNodes returns up to limit nodes matching spec, for near-miss
// diagnostics.
func (m *labeledMatcher) candidateNodes(spec nodeSpec, limit int) []string {
	where, params := m.nodeWhereClause("n", "c_", spec)
	cypher := fmt.Sprintf("MATCH (n) WHERE %s RETURN labels(n) AS labels, n.nodeKey AS nodeKey LIMIT %d", where, limit)
	records, err := m.client.ExecuteQuery(m.ctx, cypher, params)
	if err != nil {
		return []string{fmt.Sprintf("<candidate query error: %v>", err)}
	}
	out := make([]string, 0, len(records))
	for _, r := range records {
		rm := r.AsMap()
		labels, _ := rm["labels"].([]any)
		var ls []string
		for _, l := range labels {
			if s, ok := l.(string); ok {
				ls = append(ls, s)
			}
		}
		nodeKey, _ := rm["nodeKey"].(string)
		out = append(out, fmt.Sprintf("%s(%s)", strings.Join(ls, "|"), nodeKey))
	}
	return out
}

// nodeExists reports whether at least one node matches spec, plus its count.
func (m *labeledMatcher) nodeCount(spec nodeSpec) int64 {
	where, params := m.nodeWhereClause("n", "n_", spec)
	cypher := fmt.Sprintf("MATCH (n) WHERE %s RETURN count(n) AS c", where)
	records, err := m.client.ExecuteQuery(m.ctx, cypher, params)
	require.NoError(m.t, err, "nodeCount query for %+v", spec)
	if len(records) == 0 {
		return 0
	}
	c, _ := records[0].AsMap()["c"].(int64)
	return c
}

// decoratorListContains reports whether any node matching spec carries want
// in its `decorators` string-list property.
func (m *labeledMatcher) decoratorListContains(spec nodeSpec, want string) bool {
	where, params := m.nodeWhereClause("n", "d_", spec)
	cypher := fmt.Sprintf("MATCH (n) WHERE %s AND $want IN n.decorators RETURN count(n) AS c", where)
	params["want"] = want
	records, err := m.client.ExecuteQuery(m.ctx, cypher, params)
	require.NoError(m.t, err, "decoratorListContains query for %+v", spec)
	if len(records) == 0 {
		return false
	}
	c, _ := records[0].AsMap()["c"].(int64)
	return c > 0
}

// edgeCount reports how many relationships of typ exist between any node
// matching from and any node matching to.
func (m *labeledMatcher) edgeCount(typ string, from, to nodeSpec) int64 {
	fromWhere, fromParams := m.nodeWhereClause("a", "f_", from)
	toWhere, toParams := m.nodeWhereClause("b", "t_", to)
	cypher := fmt.Sprintf("MATCH (a)-[r:`%s`]->(b) WHERE %s AND %s RETURN count(r) AS c", typ, fromWhere, toWhere)
	params := map[string]any{}
	for k, v := range fromParams {
		params[k] = v
	}
	for k, v := range toParams {
		params[k] = v
	}
	records, err := m.client.ExecuteQuery(m.ctx, cypher, params)
	require.NoError(m.t, err, "edgeCount query type=%s from=%+v to=%+v", typ, from, to)
	if len(records) == 0 {
		return 0
	}
	c, _ := records[0].AsMap()["c"].(int64)
	return c
}

func specLabel(spec nodeSpec) string {
	parts := []string{}
	if spec.Label != "" {
		parts = append(parts, spec.Label)
	} else {
		parts = append(parts, "*")
	}
	name := spec.Name
	if name == "" {
		name = "*"
	}
	s := fmt.Sprintf("%s(%s)", parts[0], name)
	if spec.File != "" {
		s += "@" + spec.File
	}
	if spec.NodeKeyContains != "" {
		s += "#" + spec.NodeKeyContains
	}
	if len(spec.Props) > 0 {
		b, _ := json.Marshal(spec.Props)
		s += string(b)
	}
	return s
}

// ---- Assertion entry points ----------------------------------------------

func (m *labeledMatcher) assertMustHaveNode(exp nodeExpectation) {
	m.t.Helper()
	name := fmt.Sprintf("mustHaveNode %s", specLabel(exp.nodeSpec))
	m.t.Run(name, func(t *testing.T) {
		if exp.KnownGap {
			t.Skipf("knownGap: %s", exp.Comment)
			return
		}
		count := m.nodeCount(exp.nodeSpec)
		if count == 0 {
			candidates := m.candidateNodes(nodeSpec{Name: exp.Name}, 10)
			t.Fatalf("expected node %s, found none.\n  comment: %s\n  candidates with name %q: %s",
				specLabel(exp.nodeSpec), exp.Comment, exp.Name, strings.Join(candidates, ", "))
		}
		if exp.DecoratorContains != "" {
			if !m.decoratorListContains(exp.nodeSpec, exp.DecoratorContains) {
				candidates := m.candidateNodes(exp.nodeSpec, 10)
				t.Fatalf("expected node %s to have decorators containing %q, but it did not.\n  comment: %s\n  candidates: %s",
					specLabel(exp.nodeSpec), exp.DecoratorContains, exp.Comment, strings.Join(candidates, ", "))
			}
		}
	})
}

func (m *labeledMatcher) assertMustNotHaveNode(exp nodeExpectation) {
	m.t.Helper()
	name := fmt.Sprintf("mustNotHaveNode %s", specLabel(exp.nodeSpec))
	m.t.Run(name, func(t *testing.T) {
		if exp.KnownGap {
			t.Skipf("knownGap: %s", exp.Comment)
			return
		}
		count := m.nodeCount(exp.nodeSpec)
		if count != 0 {
			candidates := m.candidateNodes(exp.nodeSpec, 10)
			t.Fatalf("expected NO node matching %s, found %d.\n  comment: %s\n  matches: %s",
				specLabel(exp.nodeSpec), count, exp.Comment, strings.Join(candidates, ", "))
		}
	})
}

func (m *labeledMatcher) assertMustHaveEdge(exp edgeExpectation) {
	m.t.Helper()
	name := fmt.Sprintf("mustHaveEdge %s %s->%s", exp.Type, specLabel(exp.From), specLabel(exp.To))
	m.t.Run(name, func(t *testing.T) {
		if exp.KnownGap {
			t.Skipf("knownGap: %s", exp.Comment)
			return
		}
		count := m.edgeCount(exp.Type, exp.From, exp.To)
		if count == 0 {
			fromCandidates := m.candidateNodes(nodeSpec{Name: exp.From.Name}, 10)
			toCandidates := m.candidateNodes(nodeSpec{Name: exp.To.Name}, 10)
			t.Fatalf("expected %s %s->%s, found no such edge.\n  comment: %s\n  candidates with name %q: %s\n  candidates with name %q: %s",
				exp.Type, specLabel(exp.From), specLabel(exp.To), exp.Comment,
				exp.From.Name, strings.Join(fromCandidates, ", "),
				exp.To.Name, strings.Join(toCandidates, ", "))
		}
	})
}

func (m *labeledMatcher) assertMustNotHaveEdge(exp edgeExpectation) {
	m.t.Helper()
	name := fmt.Sprintf("mustNotHaveEdge %s %s->%s", exp.Type, specLabel(exp.From), specLabel(exp.To))
	m.t.Run(name, func(t *testing.T) {
		if exp.KnownGap {
			t.Skipf("knownGap: %s", exp.Comment)
			return
		}
		count := m.edgeCount(exp.Type, exp.From, exp.To)
		if count != 0 {
			fromWhere, fromParams := m.nodeWhereClause("a", "f_", exp.From)
			toWhere, toParams := m.nodeWhereClause("b", "t_", exp.To)
			q := fmt.Sprintf("MATCH (a)-[r:`%s`]->(b) WHERE %s AND %s RETURN labels(a) AS al, a.nodeKey AS ak, labels(b) AS bl, b.nodeKey AS bk LIMIT 10", exp.Type, fromWhere, toWhere)
			params := map[string]any{}
			for k, v := range fromParams {
				params[k] = v
			}
			for k, v := range toParams {
				params[k] = v
			}
			records, _ := m.client.ExecuteQuery(m.ctx, q, params)
			var found []string
			for _, r := range records {
				rm := r.AsMap()
				found = append(found, fmt.Sprintf("%v(%v) -> %v(%v)", rm["al"], rm["ak"], rm["bl"], rm["bk"]))
			}
			t.Fatalf("expected NO %s %s->%s, found %d.\n  comment: %s\n  matches: %s",
				exp.Type, specLabel(exp.From), specLabel(exp.To), count, exp.Comment, strings.Join(found, "; "))
		}
	})
}

// runExpectations executes every expectation in exp against the graph,
// scoped to service.
func runExpectations(t *testing.T, ctx context.Context, client *neo4j.Client, service string, exp *labeledExpectations) {
	t.Helper()
	m := &labeledMatcher{t: t, ctx: ctx, client: client, service: service}
	for _, n := range exp.MustHaveNodes {
		m.assertMustHaveNode(n)
	}
	for _, n := range exp.MustNotHaveNodes {
		m.assertMustNotHaveNode(n)
	}
	for _, e := range exp.MustHaveEdges {
		m.assertMustHaveEdge(e)
	}
	for _, e := range exp.MustNotHaveEdges {
		m.assertMustNotHaveEdge(e)
	}
}

// ---- Cleanup ---------------------------------------------------------------

// deleteLabeledFixtureData removes every node the labeled fixtures create.
// Self-contained: does not call or reuse golden_test.go's deleteFixtureData,
// and uses its own service/marker lists so the two cleanup paths can never
// interact.
func deleteLabeledFixtureData(ctx context.Context, client *neo4j.Client) error {
	queries := []string{
		`MATCH (n)
		 WHERE n.serviceName IS NOT NULL AND n.serviceName IN $services
		 CALL { WITH n DETACH DELETE n } IN TRANSACTIONS OF 1000 ROWS`,
		`MATCH (svc:Service)
		 WHERE svc.name IN $services
		 CALL { WITH svc DETACH DELETE svc } IN TRANSACTIONS OF 1000 ROWS`,
		`MATCH (n)
		 WHERE (n:Symbol OR n:Class OR n:Interface OR n:Module OR n:APIRoute OR n:SDKCall)
		   AND any(m IN $markers WHERE n.nodeKey CONTAINS m)
		 CALL { WITH n DETACH DELETE n } IN TRANSACTIONS OF 1000 ROWS`,
		// Structural APIRoute/SDKCall nodes carry no serviceName; once the
		// fixture functions are gone, fully-disconnected ones are garbage.
		`MATCH (n)
		 WHERE (n:APIRoute OR n:SDKCall) AND NOT (n)--()
		   AND (any(m IN $markers WHERE n.nodeKey CONTAINS m) OR n.nodeKey STARTS WITH 'api:GET:/items/')
		 CALL { WITH n DETACH DELETE n } IN TRANSACTIONS OF 1000 ROWS`,
	}
	params := map[string]any{"services": labeledServices, "markers": labeledModuleMarkers}
	for _, q := range queries {
		var err error
		for attempt := 1; attempt <= 3; attempt++ {
			if _, err = client.ExecuteQuery(ctx, q, params); err == nil || !neo4jdrv.IsRetryable(err) {
				break
			}
			time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// countLabeledFixtureLeftovers mirrors deleteLabeledFixtureData's predicates
// so leak detection cannot drift from the cleanup itself.
func countLabeledFixtureLeftovers(ctx context.Context, client *neo4j.Client) (int64, []string, error) {
	cypher := `
		MATCH (n)
		WHERE (n.serviceName IS NOT NULL AND n.serviceName IN $services)
		   OR (n:Service AND n.name IN $services)
		   OR ((n:Symbol OR n:Class OR n:Interface OR n:Module OR n:APIRoute OR n:SDKCall)
		       AND any(m IN $markers WHERE n.nodeKey CONTAINS m))
		   OR ((n:APIRoute OR n:SDKCall) AND NOT (n)--() AND n.nodeKey STARTS WITH 'api:GET:/items/')
		RETURN count(n) AS c, collect(coalesce(n.nodeKey, n.name))[..10] AS sample`
	params := map[string]any{"services": labeledServices, "markers": labeledModuleMarkers}
	records, err := client.ExecuteQuery(ctx, cypher, params)
	if err != nil || len(records) == 0 {
		return 0, nil, err
	}
	m := records[0].AsMap()
	count, _ := m["c"].(int64)
	var sample []string
	if raw, ok := m["sample"].([]any); ok {
		for _, v := range raw {
			if s, ok := v.(string); ok {
				sample = append(sample, s)
			}
		}
	}
	return count, sample, nil
}

// registerLabeledCleanup wipes any pre-existing labeled-fixture residue
// before the test runs and registers a t.Cleanup that sweeps again on the
// way out (both directions, mirroring resetGraph's rationale in
// golden_test.go: whichever test runs last must not leak into the shared
// dev database).
func registerLabeledCleanup(t *testing.T, ctx context.Context, client *neo4j.Client) {
	t.Helper()
	if err := deleteLabeledFixtureData(ctx, client); err != nil {
		t.Fatalf("pre-test labeled fixture cleanup: %v", err)
	}
	t.Cleanup(func() {
		cctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		cleanupClient, err := neo4j.NewClient(neo4jTestConfig())
		if err != nil {
			t.Errorf("labeled fixture cleanup: reconnect failed: %v", err)
			return
		}
		defer cleanupClient.Close(cctx)
		if err := deleteLabeledFixtureData(cctx, cleanupClient); err != nil {
			t.Errorf("labeled fixture cleanup failed: %v", err)
		}
	})
}

// ---- Tests -----------------------------------------------------------------

// stampReachability runs the RFC-014 classifier for a fixture service and
// stamps verdicts, so labeled expectations can assert reachability props
// (live/dead/tier) exactly like any other node property. cmd_index does this
// after every real index run; the harness indexes via the library API, which
// deliberately does not stamp.
func stampReachability(t *testing.T, ctx context.Context, client *neo4j.Client, service string) {
	t.Helper()
	result, err := reachability.Compute(ctx, client, reachability.Options{ServiceName: service})
	require.NoError(t, err, "reachability compute for %s", service)
	require.NoError(t, reachability.Stamp(ctx, client, result), "reachability stamp for %s", service)
}

// TestLabeledGo indexes test/fixtures/labeled-go and checks every hand-derived
// expectation in test/harness/labeled/labeled-go.json.
func TestLabeledGo(t *testing.T) {
	ctx := context.Background()
	client := connectNeo4j(t, ctx)
	defer client.Close(ctx)

	if _, err := exec.LookPath("scip-go"); err != nil {
		t.Skip("scip-go not installed; install: go install github.com/sourcegraph/scip-go/cmd/scip-go@latest")
	}

	registerLabeledCleanup(t, ctx, client)

	repoRoot := findRepoRoot(t)
	fixturePath := filepath.Join(repoRoot, "test", "fixtures", "labeled-go")

	t.Setenv("GOWORK", "off")

	indexer := static.NewSCIPIndexerWithLanguage(client, "labeledgo", "v0.0.0", "https://example.com/labeledgo", static.LanguageGo)
	if err := indexer.IndexProject(ctx, fixturePath); err != nil {
		t.Fatalf("IndexProject: %v", err)
	}
	defer os.Remove(filepath.Join(fixturePath, "index.scip"))

	stampReachability(t, ctx, client, "labeledgo")

	exp := loadExpectations(t, filepath.Join(repoRoot, "test", "harness", "labeled", "labeled-go.json"))
	require.Equal(t, "labeledgo", exp.Service, "expectations file service mismatch")

	runExpectations(t, ctx, client, "labeledgo", exp)

	// Verify no leak from THIS test alone (belt-and-braces; the top-level
	// TestLabeledNoLeaks below checks after the whole file runs).
	count, sample, err := countLabeledFixtureLeftoversForService(ctx, client, "labeledgo")
	require.NoError(t, err)
	for _, s := range sample {
		t.Logf("labeledgo residue sample: %s", s)
	}
	_ = count // informational only here; real leak assertion happens post-cleanup in TestLabeledNoLeaks
}

// TestLabeledTS indexes test/fixtures/labeled-ts and checks every hand-derived
// expectation in test/harness/labeled/labeled-ts.json, then separately
// exercises the CODEGRAPH_TS_RESOLVER-guarded structural resolver path for
// the option-bag negative assertion.
func TestLabeledTS(t *testing.T) {
	ctx := context.Background()
	client := connectNeo4j(t, ctx)
	defer client.Close(ctx)

	if _, err := exec.LookPath("scip-typescript"); err != nil {
		t.Skip("scip-typescript not installed; install: npm install -g @sourcegraph/scip-typescript")
	}

	registerLabeledCleanup(t, ctx, client)

	repoRoot := findRepoRoot(t)
	fixturePath := filepath.Join(repoRoot, "test", "fixtures", "labeled-ts")
	ensureNodeModules(t, fixturePath)

	indexer := static.NewSCIPIndexerWithLanguage(client, "labeledts", "v0.0.0", "https://example.com/labeledts", static.LanguageTypeScript)
	if err := indexer.IndexProject(ctx, fixturePath); err != nil {
		t.Fatalf("IndexProject: %v", err)
	}
	defer os.Remove(filepath.Join(fixturePath, "index.scip"))

	stampReachability(t, ctx, client, "labeledts")

	exp := loadExpectations(t, filepath.Join(repoRoot, "test", "harness", "labeled", "labeled-ts.json"))
	require.Equal(t, "labeledts", exp.Service, "expectations file service mismatch")

	runExpectations(t, ctx, client, "labeledts", exp)

	t.Run("resolver path: Options remains unlinked when CODEGRAPH_TS_RESOLVER runs", func(t *testing.T) {
		testTSResolverPath(t, ctx, client, repoRoot, fixturePath)
	})
}

// testTSResolverPath re-runs the TS structural resolver directly (the same
// call scip_indexer.go's runTSStructuralResolver makes) against the
// labeled-ts fixture, pointed at the repo's real tools/ts-resolver/resolve.mjs
// via CODEGRAPH_TS_RESOLVER (required under `go test`, since os.Executable()
// resolves to a temp test binary and the relative-path fallback in
// locateTSResolverScript cannot find the script). If the resolver
// environment isn't viable here (no `node`, script missing, or the resolver
// itself errors/reports an unusable TypeScript version), this SKIPs with a
// clear message — it never silently no-ops as a pass.
func testTSResolverPath(t *testing.T, ctx context.Context, client *neo4j.Client, repoRoot, fixturePath string) {
	t.Helper()

	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not on PATH; cannot exercise CODEGRAPH_TS_RESOLVER path")
	}

	scriptPath := filepath.Join(repoRoot, "tools", "ts-resolver", "resolve.mjs")
	if _, err := os.Stat(scriptPath); err != nil {
		t.Skipf("resolver script not found at %s: %v", scriptPath, err)
	}
	t.Setenv("CODEGRAPH_TS_RESOLVER", scriptPath)

	// Collect the known SCIP symbol strings scip-typescript emitted for this
	// project, exactly as JoinTSRelationships expects (see
	// tsresolver.go/scip_indexer.go's own construction of knownSymbols from
	// symIdx.symbolIDs) — here reconstructed from the Symbol nodes already
	// merged into the graph by the TestLabeledTS run above (same process,
	// same graph state, so the symbol set matches).
	records, err := client.ExecuteQuery(ctx, `
		MATCH (s:Symbol) WHERE s.symbol CONTAINS 'labeled-ts'
		RETURN s.symbol AS symbol`, nil)
	require.NoError(t, err, "collect known symbols")
	var knownSymbols []string
	for _, r := range records {
		if sym, ok := r.AsMap()["symbol"].(string); ok {
			knownSymbols = append(knownSymbols, sym)
		}
	}
	require.NotEmpty(t, knownSymbols, "no labeled-ts Symbol nodes found — did TestLabeledTS index correctly?")

	output, err := resolve.RunTSResolver(ctx, scriptPath, fixturePath, 60*time.Second)
	if err != nil {
		t.Skipf("CODEGRAPH_TS_RESOLVER path not usable in this environment: %v", err)
		return
	}

	rels, stats := resolve.JoinTSRelationships(output, knownSymbols)
	t.Logf("ts-resolver stats: interfaces=%d classes=%d pairsChecked=%d typeLevel=%d methodLevel=%d skippedNoRequiredCallable=%d joinTypeLevel=%d joinMethodLevel=%d droppedMissingSymbol=%d",
		output.Stats.Interfaces, output.Stats.Classes, output.Stats.PairsChecked,
		output.Stats.TypeLevel, output.Stats.MethodLevel, output.Stats.SkippedNoRequiredCallable,
		stats.TypeLevelEmitted, stats.MethodLevelEmitted, stats.DroppedMissingSymbol)

	// The critical assertion: no relationship the resolver itself proposes
	// may touch Options — this is the resolver's OWN output, independent of
	// what's already in the graph, so it directly exercises
	// SkippedNoRequiredCallable rather than just re-checking the DB.
	for _, rel := range rels {
		if strings.Contains(rel.FromSymbol, "/Options#") || strings.Contains(rel.ToSymbol, "/Options#") {
			t.Fatalf("ts-resolver proposed a relationship touching Options (the all-optional option-bag interface), which must be skipped via SkippedNoRequiredCallable: %+v", rel)
		}
	}
	if output.Stats.SkippedNoRequiredCallable < 1 {
		t.Errorf("expected resolver to report skippedNoRequiredCallable >= 1 (Options should be skipped as having no required function-typed member), got %d", output.Stats.SkippedNoRequiredCallable)
	}
}

// countLabeledFixtureLeftoversForService is a narrower version of
// countLabeledFixtureLeftovers scoped to one service, used for informational
// per-test logging (not the authoritative leak check, which runs once after
// both fixtures via TestLabeledNoLeaks).
func countLabeledFixtureLeftoversForService(ctx context.Context, client *neo4j.Client, service string) (int64, []string, error) {
	cypher := `MATCH (n) WHERE n.serviceName = $svc RETURN count(n) AS c, collect(n.nodeKey)[..5] AS sample`
	records, err := client.ExecuteQuery(ctx, cypher, map[string]any{"svc": service})
	if err != nil || len(records) == 0 {
		return 0, nil, err
	}
	m := records[0].AsMap()
	count, _ := m["c"].(int64)
	var sample []string
	if raw, ok := m["sample"].([]any); ok {
		for _, v := range raw {
			if s, ok := v.(string); ok {
				sample = append(sample, s)
			}
		}
	}
	return count, sample, nil
}

// TestLabeledNoLeaks asserts that TestLabeledGo/TestLabeledTS cleaned up
// after themselves. Named so it sorts after them alphabetically is NOT
// relied upon — Go test execution order is source order within a file, and
// this test is declared last in this file, but to be robust against
// reordering it also tolerates running when the other two were skipped
// (skipped tests never created data, so the leftover count is legitimately
// zero either way).
func TestLabeledNoLeaks(t *testing.T) {
	ctx := context.Background()
	client := connectNeo4j(t, ctx)
	defer client.Close(ctx)

	count, sample, err := countLabeledFixtureLeftovers(ctx, client)
	require.NoError(t, err, "leftover query failed")
	sort.Strings(sample)
	for _, s := range sample {
		t.Logf("leaked labeled fixture node: %s", s)
	}
	require.Zero(t, count, "labeled fixture nodes leaked into the shared database")
}
