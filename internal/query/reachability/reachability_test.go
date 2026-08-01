package reachability

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mkNodes builds a node map from (id, filePath) pairs.
func mkNodes(entries map[string]string) map[string]*FnVerdict {
	nodes := make(map[string]*FnVerdict, len(entries))
	for id, file := range entries {
		nodes[id] = &FnVerdict{
			ID:         id,
			Name:       id,
			Label:      "Function",
			FilePath:   file,
			InTestFile: IsTestFile(file),
		}
	}
	return nodes
}

// mkGraph builds adjacency + incoming counts from edge pairs.
func mkGraph(edges [][2]string) (map[string][]string, map[string]int) {
	adj := make(map[string][]string)
	incoming := make(map[string]int)
	for _, e := range edges {
		adj[e[0]] = append(adj[e[0]], e[1])
		incoming[e[1]]++
	}
	return adj, incoming
}

func byName(r *Result) map[string]FnVerdict {
	out := make(map[string]FnVerdict, len(r.Verdicts))
	for _, v := range r.Verdicts {
		out[v.Name] = v
	}
	return out
}

func TestIsTestFile(t *testing.T) {
	assert.True(t, IsTestFile("internal/foo/bar_test.go"))
	assert.True(t, IsTestFile("src/users/users.service.spec.ts"))
	assert.True(t, IsTestFile("src/app.test.tsx"))
	assert.True(t, IsTestFile("src/__tests__/helper.ts"))
	assert.False(t, IsTestFile("internal/foo/bar.go"))
	assert.False(t, IsTestFile("src/users/users.service.ts"))
	// "test" as a mere substring of a name must not match.
	assert.False(t, IsTestFile("src/contest/rating.ts"))
	assert.False(t, IsTestFile("src/latest.go"))
}

// TestClassify_DeadClusterDetection is the RFC-014 headline case: a→b→a
// cluster with callers but no path from any root must be dead, flagged as a
// cluster — the naive inDegree check's blind spot.
func TestClassify_DeadClusterDetection(t *testing.T) {
	nodes := mkNodes(map[string]string{
		"handler":  "src/api.go",
		"helper":   "src/api.go",
		"deadA":    "src/old.go",
		"deadB":    "src/old.go",
		"orphan":   "src/old.go",
	})
	adj, incoming := mkGraph([][2]string{
		{"handler", "helper"},
		{"deadA", "deadB"}, // cluster: call each other, nobody live calls in
		{"deadB", "deadA"},
	})
	roots := []rootEntry{{ID: "handler", Tier: TierAPIExposed}}

	r := classify(nodes, adj, incoming, roots)
	v := byName(r)

	assert.Equal(t, VerdictLive, v["handler"].Verdict)
	assert.Equal(t, 1, v["handler"].Tier)
	assert.Equal(t, VerdictLive, v["helper"].Verdict)
	assert.Equal(t, "handler", v["helper"].RootName)

	assert.Equal(t, VerdictDead, v["deadA"].Verdict)
	assert.True(t, v["deadA"].DeadCluster, "deadA has a caller (deadB) — must flag as cluster member")
	assert.Equal(t, VerdictDead, v["deadB"].Verdict)
	assert.True(t, v["deadB"].DeadCluster)

	assert.Equal(t, VerdictDead, v["orphan"].Verdict)
	assert.False(t, v["orphan"].DeadCluster, "orphan has no callers at all")

	assert.Equal(t, 2, r.Live)
	assert.Equal(t, 3, r.Dead)
	assert.Equal(t, 2, r.DeadCluster)
}

func TestClassify_TierPrecedenceAndNearestRoot(t *testing.T) {
	nodes := mkNodes(map[string]string{
		"apiHandler": "src/api.go",
		"moduleInit": "src/boot.go",
		"shared":     "src/shared.go",
	})
	adj, incoming := mkGraph([][2]string{
		{"apiHandler", "shared"},
		{"moduleInit", "shared"},
	})
	// shared reachable from tier 1 AND tier 3 at equal distance: tier 1 wins.
	roots := []rootEntry{
		{ID: "moduleInit", Tier: TierModuleLoad},
		{ID: "apiHandler", Tier: TierAPIExposed},
	}

	r := classify(nodes, adj, incoming, roots)
	v := byName(r)
	assert.Equal(t, 1, v["shared"].Tier, "tier-1 root must claim shared over tier-3")
	assert.Equal(t, "apiHandler", v["shared"].RootName)
	assert.Equal(t, 3, v["moduleInit"].Tier)
}

func TestClassify_TestOnlyReach(t *testing.T) {
	nodes := mkNodes(map[string]string{
		"TestFoo":    "pkg/foo_test.go",
		"testHelper": "pkg/helpers.go", // NOT a test file, but only tests call it
		"liveFn":     "pkg/foo.go",
	})
	adj, incoming := mkGraph([][2]string{
		{"TestFoo", "testHelper"},
	})
	roots := []rootEntry{{ID: "liveFn", Tier: TierRuntimeMain}}

	r := classify(nodes, adj, incoming, roots)
	v := byName(r)
	assert.Equal(t, VerdictTestOnly, v["TestFoo"].Verdict, "test-file code is test_only, not dead")
	assert.Equal(t, VerdictTestOnly, v["testHelper"].Verdict, "reached only from test code")
	assert.Equal(t, VerdictLive, v["liveFn"].Verdict)
	assert.Equal(t, 2, r.TestOnly)
}

// TestClassify_AddressTakenIsLive: the USES_VALUE edge (already merged into
// the adjacency by fetchEdges) keeps an address-taken callback alive — the
// cobra.OnInitialize(initConfig) case.
func TestClassify_AddressTakenIsLive(t *testing.T) {
	nodes := mkNodes(map[string]string{
		"main":       "cmd/main.go",
		"initConfig": "cmd/main.go",
	})
	// main -> initConfig via USES_VALUE (fetchEdges unions edge types).
	adj, incoming := mkGraph([][2]string{{"main", "initConfig"}})
	roots := []rootEntry{{ID: "main", Tier: TierRuntimeMain}}

	r := classify(nodes, adj, incoming, roots)
	v := byName(r)
	assert.Equal(t, VerdictLive, v["initConfig"].Verdict)
}

func TestClassify_RootCoverageGuard(t *testing.T) {
	nodes := mkNodes(map[string]string{
		"a": "src/a.ts",
		"b": "src/b.ts",
	})
	adj, incoming := mkGraph([][2]string{{"a", "b"}})

	// No roots at all: nothing may be called dead.
	r := classify(nodes, adj, incoming, nil)
	v := byName(r)
	assert.Equal(t, VerdictUnknown, v["a"].Verdict)
	assert.Equal(t, VerdictUnknown, v["b"].Verdict)
	assert.Equal(t, 2, r.Unknown)
	assert.Zero(t, r.Dead)

	// Only test-file roots: still no application root coverage.
	nodes2 := mkNodes(map[string]string{
		"TestX": "pkg/x_test.go",
		"y":     "pkg/y.go",
	})
	adj2, incoming2 := mkGraph(nil)
	r2 := classify(nodes2, adj2, incoming2, []rootEntry{{ID: "TestX", Tier: TierModuleLoad}})
	v2 := byName(r2)
	assert.Equal(t, VerdictUnknown, v2["y"].Verdict)
}

func TestClassify_SelfLoopDoesNotFakeLiveness(t *testing.T) {
	// A purely self-recursive function with no live caller stays dead even
	// though it has an incoming edge (its own). fetchEdges filters a <> b,
	// so classify never even sees self-loops — this asserts the contract.
	nodes := mkNodes(map[string]string{
		"live":      "src/a.go",
		"recursive": "src/b.go",
	})
	adj, incoming := mkGraph(nil) // self-loop excluded upstream
	roots := []rootEntry{{ID: "live", Tier: TierAPIExposed}}

	r := classify(nodes, adj, incoming, roots)
	v := byName(r)
	require.Equal(t, VerdictDead, v["recursive"].Verdict)
	assert.False(t, v["recursive"].DeadCluster)
}

func TestClassify_RootsPointingAtUnknownNodesIgnored(t *testing.T) {
	nodes := mkNodes(map[string]string{"a": "src/a.go"})
	adj, incoming := mkGraph(nil)
	roots := []rootEntry{
		{ID: "a", Tier: TierAPIExposed},
		{ID: "cross-service-ghost", Tier: TierAPIExposed},
	}
	r := classify(nodes, adj, incoming, roots)
	assert.Equal(t, 1, r.Roots)
	assert.Equal(t, VerdictLive, byName(r)["a"].Verdict)
}

func TestClassify_StdlibDispatchMethodsArePossiblyLive(t *testing.T) {
	nodes := mkNodes(map[string]string{
		"live": "src/a.go",
	})
	// Error() method on a Go error type: unreached structurally, but the
	// error interface lives outside the graph — must NOT be called dead.
	nodes["errMethod"] = &FnVerdict{ID: "errMethod", Name: "Error", Label: "Method", FilePath: "src/errs.go"}
	// A free FUNCTION named Error gets no such benefit.
	nodes["errFunc"] = &FnVerdict{ID: "errFunc", Name: "Error", Label: "Function", FilePath: "src/errs.go"}
	// A TS method named Close is not subject to Go stdlib dispatch.
	nodes["tsClose"] = &FnVerdict{ID: "tsClose", Name: "Close", Label: "Method", FilePath: "src/conn.ts"}

	adj, incoming := mkGraph(nil)
	roots := []rootEntry{{ID: "live", Tier: TierAPIExposed}}

	r := classify(nodes, adj, incoming, roots)
	byID := make(map[string]FnVerdict, len(r.Verdicts))
	for _, v := range r.Verdicts {
		byID[v.ID] = v
	}
	assert.Equal(t, VerdictPossiblyLive, byID["errMethod"].Verdict)
	assert.Equal(t, VerdictDead, byID["errFunc"].Verdict, "free functions get no stdlib-dispatch benefit")
	assert.Equal(t, VerdictDead, byID["tsClose"].Verdict, "stdlib dispatch is a Go-only concept")
	assert.Equal(t, 1, r.PossiblyLive)
	assert.Equal(t, 2, r.Dead)
}

func TestClassify_LiveClassConstructorBecomesLive(t *testing.T) {
	// A DI-instantiated class: route method live via tier-1 root, the
	// constructor unreached by CALLS — but instantiation is implied, so the
	// constructor AND its callees (a config helper) must become live. A
	// second class whose ONLY member is its constructor stays dead.
	nodes := map[string]*FnVerdict{
		"route": {ID: "route", Name: "findAll", Label: "Method", FilePath: "src/users.controller.ts", classKey: "npm x 1 src/`users.controller.ts`/UsersController"},
		"ctor":  {ID: "ctor", Name: "<constructor>", Label: "Method", FilePath: "src/users.controller.ts", classKey: "npm x 1 src/`users.controller.ts`/UsersController"},
		"cfg":   {ID: "cfg", Name: "buildConfig", Label: "Function", FilePath: "src/config.ts"},
		"deadCtor": {ID: "deadCtor", Name: "<constructor>", Label: "Method", FilePath: "src/unused.service.ts", classKey: "npm x 1 src/`unused.service.ts`/UnusedService"},
	}
	adj, incoming := mkGraph([][2]string{{"ctor", "cfg"}})
	roots := []rootEntry{{ID: "route", Tier: TierAPIExposed}}

	r := classify(nodes, adj, incoming, roots)
	byID := make(map[string]FnVerdict, len(r.Verdicts))
	for _, v := range r.Verdicts {
		byID[v.ID] = v
	}
	assert.Equal(t, VerdictLive, byID["ctor"].Verdict, "constructor of a live class must be live")
	assert.Equal(t, VerdictLive, byID["cfg"].Verdict, "constructor callees become live transitively")
	assert.Equal(t, VerdictDead, byID["deadCtor"].Verdict, "class with no live members stays dead")
}
