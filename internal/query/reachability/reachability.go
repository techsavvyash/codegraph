// Package reachability implements RFC-014 (RFC-001 phases 7-8): whole-service
// liveness analysis over the union graph CALLS ∪ USES_VALUE, and dead-code
// verdicts stamped onto Function/Method nodes.
//
// The naive check (inDegree = 0 ⇒ dead) is wrong in both directions: entry
// points, module-scope-called functions and address-taken callbacks are alive
// with zero counted callers, while a function whose only callers are
// themselves dead is dead despite inDegree > 0 (a dead cluster). This engine
// does the correct thing: collect roots by tier, BFS, classify what's left.
//
// Graph loads happen in three service-scoped queries (nodes, edges, roots);
// the traversal itself is in-memory — services here are 10³–10⁴ functions,
// far below anything that needs a database-side traversal.
package reachability

import (
	"context"
	"fmt"
	"sort"
	"strings"

	neo4j "github.com/context-maximiser/code-graph/internal/graph"
)

// Verdict is a Function/Method's liveness classification.
type Verdict string

const (
	// VerdictLive: reachable from a non-test root.
	VerdictLive Verdict = "live"
	// VerdictTestOnly: reachable from test-file roots only.
	VerdictTestOnly Verdict = "test_only"
	// VerdictDead: unreachable from every root.
	VerdictDead Verdict = "dead"
	// VerdictPossiblyLive: structurally unreached, but the method matches a
	// stdlib dynamic-dispatch slot (Error, String, MarshalJSON, ServeHTTP,
	// …) — such interfaces live outside the graph, so no IMPLEMENTS fan-out
	// can prove the call. Calling these dead would be wrong for every error
	// type in the codebase; calling them live would be a guess. They are
	// their own category.
	VerdictPossiblyLive Verdict = "possibly_live"
	// VerdictUnknown: the service has no detected entry points at all, so
	// calling anything dead would be dishonest — every non-live function
	// gets unknown instead (RFC-014 root-coverage guard).
	VerdictUnknown Verdict = "unknown"
)

// stdlibDispatchMethods are Go method names that implement well-known stdlib
// interfaces invoked dynamically (error, fmt.Stringer, json.Marshaler,
// http.Handler, io.*, sort.Interface, sql driver hooks). A structurally
// unreached METHOD (never a free function) with one of these names on a .go
// file cannot be proven dead by graph traversal alone.
var stdlibDispatchMethods = map[string]bool{
	"Error": true, "String": true, "GoString": true,
	"MarshalJSON": true, "UnmarshalJSON": true,
	"MarshalText": true, "UnmarshalText": true,
	"MarshalBinary": true, "UnmarshalBinary": true,
	"ServeHTTP": true,
	"Read":      true, "Write": true, "Close": true, "Seek": true,
	"ReadFrom": true, "WriteTo": true,
	"Len": true, "Less": true, "Swap": true,
	"Lock": true, "Unlock": true,
	"Scan": true, "Value": true,
	"Format": true,
}

// Root tiers, stamped as reachabilityTier on live nodes (lowest tier wins
// when a node is reachable from several roots).
const (
	TierAPIExposed  = 1 // EXPOSES_API edge (any detection source)
	TierRuntimeMain = 2 // Go main/init, scheduled tasks, broker consumers
	TierModuleLoad  = 3 // called or address-taken at module scope (File edges)
)

// Options scope one reachability run.
type Options struct {
	ServiceName string
	ScopeID     string // defaults to "main"
}

// FnVerdict is one function's classification, with enough context to render
// a dead-code report without further queries.
type FnVerdict struct {
	ID string `json:"-"` // Neo4j element ID (internal)
	// classKey groups methods of the same class (SCIP symbol prefix up to
	// the member separator); used for the constructor-liveness rule.
	classKey    string
	NodeKey     string  `json:"nodeKey"`
	Name        string  `json:"name"`
	Label       string  `json:"label"` // Function | Method
	FilePath    string  `json:"filePath"`
	StartLine   int64   `json:"startLine"`
	IsExported  bool    `json:"isExported"`
	Verdict     Verdict `json:"verdict"`
	Tier        int     `json:"tier,omitempty"`        // live only
	RootName    string  `json:"rootName,omitempty"`    // live/test_only: nearest root
	DeadCluster bool    `json:"deadCluster,omitempty"` // dead with callers (all dead too)
	InTestFile  bool    `json:"inTestFile"`
}

// Result is a full service classification.
type Result struct {
	ServiceName string      `json:"serviceName"`
	ScopeID     string      `json:"scopeId"`
	Total        int `json:"total"`
	Live         int `json:"live"`
	TestOnly     int `json:"testOnly"`
	Dead         int `json:"dead"`
	DeadCluster  int `json:"deadCluster"`
	PossiblyLive int `json:"possiblyLive"`
	Unknown      int `json:"unknown"`
	Roots       int         `json:"roots"`
	TestRoots   int         `json:"testRoots"`
	// AbstractSkipped counts interface-method declarations excluded from
	// the population entirely (contracts, not executable code).
	AbstractSkipped int         `json:"abstractSkipped"`
	Verdicts        []FnVerdict `json:"verdicts"`
}

// isConstructorName matches the constructor member names the SCIP indexers
// emit (scip-typescript uses `<constructor>`). Go has no constructor
// concept, so this never matches Go methods.
func isConstructorName(name string) bool {
	return name == "<constructor>" || name == "constructor"
}

// IsTestFile reports whether a service-relative path is test code, by the
// same conventions the indexed ecosystems use: Go *_test.go, TS/JS
// *.spec.*/*.test.*, and anything under a __tests__/ directory. Census-style
// fixture dirs (testdata/, fixtures/) are excluded from indexing entirely so
// they never reach this check.
func IsTestFile(path string) bool {
	p := strings.ToLower(path)
	base := p
	if i := strings.LastIndex(p, "/"); i >= 0 {
		base = p[i+1:]
	}
	switch {
	case strings.HasSuffix(base, "_test.go"),
		strings.Contains(base, ".spec."),
		strings.Contains(base, ".test."),
		strings.Contains(p, "__tests__/"):
		return true
	}
	return false
}

// Compute classifies every Function/Method in the service and returns the
// result WITHOUT writing anything — Stamp persists a result.
func Compute(ctx context.Context, client *neo4j.Client, opts Options) (*Result, error) {
	if opts.ServiceName == "" {
		return nil, fmt.Errorf("reachability: ServiceName is required")
	}
	scopeID := opts.ScopeID
	if scopeID == "" {
		scopeID = "main"
	}

	nodes, abstractSkipped, err := fetchNodes(ctx, client, opts.ServiceName, scopeID)
	if err != nil {
		return nil, fmt.Errorf("reachability: fetch nodes: %w", err)
	}
	adj, incoming, err := fetchEdges(ctx, client, opts.ServiceName, scopeID)
	if err != nil {
		return nil, fmt.Errorf("reachability: fetch edges: %w", err)
	}
	roots, err := fetchRoots(ctx, client, opts.ServiceName, scopeID)
	if err != nil {
		return nil, fmt.Errorf("reachability: fetch roots: %w", err)
	}

	result := classify(nodes, adj, incoming, roots)
	result.ServiceName = opts.ServiceName
	result.ScopeID = scopeID
	result.AbstractSkipped = abstractSkipped
	return result, nil
}

// classify runs the BFS passes and assigns verdicts. Pure function over the
// loaded graph — testable without Neo4j.
func classify(nodes map[string]*FnVerdict, adj map[string][]string, incoming map[string]int, roots []rootEntry) *Result {
	result := &Result{}

	// Partition roots: a root inside a test file seeds the test-only pass,
	// everything else seeds liveness. Roots pointing at nodes we didn't load
	// (cross-service targets) are ignored.
	type seed struct {
		id   string
		tier int
		name string
	}
	var liveSeeds, testSeeds []seed
	for _, r := range roots {
		n, ok := nodes[r.ID]
		if !ok {
			continue
		}
		s := seed{id: r.ID, tier: r.Tier, name: n.Name}
		if n.InTestFile {
			testSeeds = append(testSeeds, s)
		} else {
			liveSeeds = append(liveSeeds, s)
		}
	}
	result.Roots = len(liveSeeds)
	result.TestRoots = len(testSeeds)

	// Root-coverage guard: with zero non-test roots the graph cannot honestly
	// distinguish dead from "we found no entry points" — everything non-live
	// becomes unknown rather than dead.
	noRootCoverage := len(liveSeeds) == 0

	// BFS 1: liveness. Track nearest root (BFS order = fewest hops) and the
	// lowest tier that reaches each node. Seeds sorted tier-first so a
	// tier-1 root claims a node over a tier-3 root at equal distance.
	sort.SliceStable(liveSeeds, func(i, j int) bool { return liveSeeds[i].tier < liveSeeds[j].tier })
	type mark struct {
		tier int
		root string
	}
	liveMarks := make(map[string]mark, len(nodes))
	queue := make([]string, 0, len(liveSeeds))
	for _, s := range liveSeeds {
		if _, seen := liveMarks[s.id]; seen {
			continue
		}
		liveMarks[s.id] = mark{tier: s.tier, root: s.name}
		queue = append(queue, s.id)
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		m := liveMarks[cur]
		for _, next := range adj[cur] {
			if _, seen := liveMarks[next]; seen {
				continue
			}
			liveMarks[next] = mark{tier: m.tier, root: m.root}
			queue = append(queue, next)
		}
	}

	// Constructor-liveness fixpoint: a class with any live method is
	// instantiated (DI containers, `new` in framework code), so its
	// constructor runs — and everything the constructor calls becomes live
	// in turn, which can make further classes live. Iterate until stable.
	for {
		var newSeeds []string
		liveClasses := make(map[string]mark)
		for id, n := range nodes {
			if m, ok := liveMarks[id]; ok && n.classKey != "" {
				if _, seen := liveClasses[n.classKey]; !seen {
					liveClasses[n.classKey] = m
				}
			}
		}
		for id, n := range nodes {
			if _, alreadyLive := liveMarks[id]; alreadyLive || !isConstructorName(n.Name) || n.classKey == "" {
				continue
			}
			if m, ok := liveClasses[n.classKey]; ok {
				liveMarks[id] = mark{tier: m.tier, root: m.root}
				newSeeds = append(newSeeds, id)
			}
		}
		if len(newSeeds) == 0 {
			break
		}
		queue = append(queue[:0], newSeeds...)
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			m := liveMarks[cur]
			for _, next := range adj[cur] {
				if _, seen := liveMarks[next]; seen {
					continue
				}
				liveMarks[next] = mark{tier: m.tier, root: m.root}
				queue = append(queue, next)
			}
		}
	}

	// BFS 2: test reach — everything reachable from test-file roots AND from
	// test-file functions themselves (a helper called by a test function is
	// test-reached even though the test fn isn't a formal root).
	testMarks := make(map[string]string, len(nodes))
	queue = queue[:0]
	for _, s := range testSeeds {
		if _, seen := testMarks[s.id]; !seen {
			testMarks[s.id] = s.name
			queue = append(queue, s.id)
		}
	}
	for id, n := range nodes {
		if n.InTestFile {
			if _, seen := testMarks[id]; !seen {
				testMarks[id] = n.Name
				queue = append(queue, id)
			}
		}
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		root := testMarks[cur]
		for _, next := range adj[cur] {
			if _, seen := testMarks[next]; seen {
				continue
			}
			testMarks[next] = root
			queue = append(queue, next)
		}
	}

	// Assign verdicts.
	for id, n := range nodes {
		m, isLive := liveMarks[id]
		_, isTestReached := testMarks[id]
		switch {
		case isLive:
			n.Verdict = VerdictLive
			n.Tier = m.tier
			n.RootName = m.root
			result.Live++
		case n.InTestFile || isTestReached:
			// Test code itself, and anything only tests reach, is neither
			// live nor dead in application terms.
			n.Verdict = VerdictTestOnly
			n.RootName = testMarks[id]
			result.TestOnly++
		case noRootCoverage:
			n.Verdict = VerdictUnknown
			result.Unknown++
		case n.Label == "Method" && strings.HasSuffix(n.FilePath, ".go") && stdlibDispatchMethods[n.Name]:
			n.Verdict = VerdictPossiblyLive
			result.PossiblyLive++
		default:
			n.Verdict = VerdictDead
			if incoming[id] > 0 {
				n.DeadCluster = true
				result.DeadCluster++
			}
			result.Dead++
		}
	}

	result.Total = len(nodes)
	result.Verdicts = make([]FnVerdict, 0, len(nodes))
	for _, n := range nodes {
		result.Verdicts = append(result.Verdicts, *n)
	}
	sort.Slice(result.Verdicts, func(i, j int) bool {
		a, b := result.Verdicts[i], result.Verdicts[j]
		if a.FilePath != b.FilePath {
			return a.FilePath < b.FilePath
		}
		return a.StartLine < b.StartLine
	})
	return result
}
