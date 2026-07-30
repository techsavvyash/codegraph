package inference

import (
	"context"
	"fmt"
	"maps"
	"sort"

	neo4j "github.com/context-maximiser/code-graph/internal/graph"

	models "github.com/context-maximiser/code-graph/internal/model"
)

// GraphSeedType classifies the tier of a graph-structural seed.
type GraphSeedType string

const (
	GraphSeedAPIExposed    GraphSeedType = "api_exposed"      // Tier 1: EXPOSES_API edge
	GraphSeedInterfaceImpl GraphSeedType = "interface_impl"   // Tier 2: IMPLEMENTS with inDegree=0
	GraphSeedTopoRoot      GraphSeedType = "topological_root" // Tier 3: inDegree=0, outDegree>0, exported
	GraphSeedCentrality    GraphSeedType = "centrality_entry" // Tier 4: high betweenness, no higher-centrality caller
)

// graphSeedTierPriority maps seed types to their base priority.
var graphSeedTierPriority = map[GraphSeedType]int{
	GraphSeedAPIExposed:    100,
	GraphSeedInterfaceImpl: 85,
	GraphSeedTopoRoot:      70,
	GraphSeedCentrality:    60,
}

// GraphSeed represents a structurally-detected entry point from the graph.
type GraphSeed struct {
	NodeKey  string        `json:"nodeKey"`
	Name     string        `json:"name"`
	NodeType string        `json:"nodeType"` // "Function" or "Method"
	SeedType GraphSeedType `json:"seedType"`
	Priority int           `json:"priority"`
	Tier     int           `json:"tier"`
}

// GraphSeedFinder detects flow entry points using purely structural graph
// signals — zero name heuristics. It queries the graph for API endpoints,
// interface implementations, topological roots, and high-centrality nodes.
type GraphSeedFinder struct {
	client        *neo4j.Client
	scopeCtx      models.ScopeContext
	budget        TraversalBudget
	serviceNames  []string
	servicePrefix string

	// CentralityThreshold is the minimum betweennessCentrality to qualify
	// as a Tier 4 seed. Zero means use automatic threshold (mean + 1 stddev).
	CentralityThreshold float64
}

// NewGraphSeedFinder creates a new graph-structural seed finder.
func NewGraphSeedFinder(client *neo4j.Client) *GraphSeedFinder {
	return &GraphSeedFinder{
		client:   client,
		scopeCtx: models.DefaultScope(),
		budget:   DefaultTraversalBudget,
	}
}

// SetScope sets the scope context for seed queries.
func (f *GraphSeedFinder) SetScope(scope models.ScopeContext) {
	f.scopeCtx = scope
}

// SetBudget sets the traversal budget.
func (f *GraphSeedFinder) SetBudget(budget TraversalBudget) {
	f.budget = budget
}

// SetServiceFilter restricts seed detection to functions whose serviceName is
// in names (exact) or starts with prefix. Filtering must happen inside the
// tier queries — FindSeeds caps results to budget.MaxSteps, and a global cap
// applied before service filtering starves small services out of the seed set
// entirely (their tier-3 seeds lose the priority sort to the big services'
// tier-1 seeds).
func (f *GraphSeedFinder) SetServiceFilter(names []string, prefix string) {
	f.serviceNames = append([]string{}, names...)
	f.servicePrefix = prefix
}

// serviceParams returns the base query params including service constraints.
func (f *GraphSeedFinder) serviceParams(extra map[string]any) map[string]any {
	params := map[string]any{
		"scopeId":       f.scopeCtx.ScopeID,
		"serviceNames":  f.serviceNames,
		"servicePrefix": f.servicePrefix,
	}
	maps.Copy(params, extra)
	return params
}

// serviceClause is the WHERE-clause fragment enforcing the service filter on fn.
const serviceClause = `
		  AND (size($serviceNames) = 0 OR fn.serviceName IN $serviceNames)
		  AND ($servicePrefix = '' OR fn.serviceName STARTS WITH $servicePrefix)`

// FindSeeds queries the graph across all four tiers, deduplicates by nodeKey
// (higher tier wins), and returns seeds sorted by priority descending.
func (f *GraphSeedFinder) FindSeeds(ctx context.Context) ([]GraphSeed, error) {
	seedMap := make(map[string]GraphSeed) // nodeKey -> best seed

	// Tier 1: API-exposed handlers
	tier1, err := f.findAPIExposedSeeds(ctx)
	if err != nil {
		return nil, fmt.Errorf("tier 1 (api_exposed): %w", err)
	}
	for _, s := range tier1 {
		seedMap[s.NodeKey] = s
	}

	// Tier 2: Interface implementations with no callers
	tier2, err := f.findInterfaceImplSeeds(ctx)
	if err != nil {
		return nil, fmt.Errorf("tier 2 (interface_impl): %w", err)
	}
	for _, s := range tier2 {
		if existing, ok := seedMap[s.NodeKey]; !ok || s.Tier < existing.Tier {
			seedMap[s.NodeKey] = s
		}
	}

	// Tier 3: Topological roots
	tier3, err := f.findTopologicalRootSeeds(ctx)
	if err != nil {
		return nil, fmt.Errorf("tier 3 (topological_root): %w", err)
	}
	for _, s := range tier3 {
		if existing, ok := seedMap[s.NodeKey]; !ok || s.Tier < existing.Tier {
			seedMap[s.NodeKey] = s
		}
	}

	// Tier 4: Centrality-based entries
	tier4, err := f.findCentralitySeeds(ctx)
	if err != nil {
		// Non-fatal: centrality data may not exist if GDS wasn't run.
		fmt.Printf("Warning: tier 4 (centrality) query failed: %v\n", err)
	} else {
		for _, s := range tier4 {
			if existing, ok := seedMap[s.NodeKey]; !ok || s.Tier < existing.Tier {
				seedMap[s.NodeKey] = s
			}
		}
	}

	// Collect and sort by priority descending.
	seeds := make([]GraphSeed, 0, len(seedMap))
	for _, s := range seedMap {
		seeds = append(seeds, s)
	}
	sort.Slice(seeds, func(i, j int) bool {
		return seeds[i].Priority > seeds[j].Priority
	})

	// Cap to budget.
	if f.budget.MaxSteps > 0 && len(seeds) > f.budget.MaxSteps {
		seeds = seeds[:f.budget.MaxSteps]
	}

	return seeds, nil
}

// findAPIExposedSeeds returns functions/methods that are connected to APIRoute
// nodes via EXPOSES_API edges. These are the highest-confidence entry points.
// Sub-priority within Tier 1 is based on detection confidence:
//   - Both external-params + cross-pkg: priority 100
//   - External-params only: priority 95
//   - Cross-pkg only: priority 90
//   - Legacy framework-detected: priority 85
func (f *GraphSeedFinder) findAPIExposedSeeds(ctx context.Context) ([]GraphSeed, error) {
	cypher := `
		MATCH (fn)-[:EXPOSES_API]->(route:APIRoute)
		WHERE (fn:Function OR fn:Method)
		  AND (fn.scopeId = $scopeId OR fn.scopeId = 'main')
		  AND (route.scopeId = $scopeId OR route.scopeId = 'main')
		  AND coalesce(fn.isTestFunction, false) = false` + serviceClause + `
		RETURN fn.nodeKey AS nodeKey, fn.name AS name, labels(fn) AS labels,
		       coalesce(fn.hasExternalParams, false) AS hasExternal,
		       coalesce(fn.isCrossPkgTarget, false) AS isCrossPkg,
		       coalesce(route.detectionSource, '') AS detectionSource
	`

	records, err := f.client.ExecuteQuery(ctx, cypher, f.serviceParams(nil))
	if err != nil {
		return nil, err
	}

	var seeds []GraphSeed
	for _, r := range records {
		m := r.AsMap()
		nodeKey := strValMap(m, "nodeKey")
		name := strValMap(m, "name")
		if nodeKey == "" || name == "" {
			continue
		}

		hasExternal, _ := m["hasExternal"].(bool)
		isCrossPkg, _ := m["isCrossPkg"].(bool)

		// Sub-priority based on detection confidence.
		priority := 85 // legacy framework-detected fallback
		switch {
		case hasExternal && isCrossPkg:
			priority = 100
		case hasExternal:
			priority = 95
		case isCrossPkg:
			priority = 90
		}

		seeds = append(seeds, GraphSeed{
			NodeKey:  nodeKey,
			Name:     name,
			NodeType: labelType(m),
			SeedType: GraphSeedAPIExposed,
			Priority: priority,
			Tier:     1,
		})
	}
	return seeds, nil
}

// findInterfaceImplSeeds returns functions/methods that implement an interface
// and have inDegree = 0 (no callers). These are likely dependency-injected
// entry points.
func (f *GraphSeedFinder) findInterfaceImplSeeds(ctx context.Context) ([]GraphSeed, error) {
	cypher := `
		MATCH (fn)-[:IMPLEMENTS]->(iface:Interface)
		WHERE (fn:Function OR fn:Method)
		  AND (fn.scopeId = $scopeId OR fn.scopeId = 'main')
		  AND coalesce(fn.inDegree, 0) = 0
		  AND coalesce(fn.isTestFunction, false) = false` + serviceClause + `
		RETURN fn.nodeKey AS nodeKey, fn.name AS name, labels(fn) AS labels
	`

	records, err := f.client.ExecuteQuery(ctx, cypher, f.serviceParams(nil))
	if err != nil {
		return nil, err
	}

	var seeds []GraphSeed
	for _, r := range records {
		m := r.AsMap()
		nodeKey := strValMap(m, "nodeKey")
		name := strValMap(m, "name")
		if nodeKey == "" || name == "" {
			continue
		}

		seeds = append(seeds, GraphSeed{
			NodeKey:  nodeKey,
			Name:     name,
			NodeType: labelType(m),
			SeedType: GraphSeedInterfaceImpl,
			Priority: graphSeedTierPriority[GraphSeedInterfaceImpl],
			Tier:     2,
		})
	}
	return seeds, nil
}

// findTopologicalRootSeeds returns exported functions/methods with inDegree=0
// and outDegree>0 that are not test functions. These are structural roots
// of the call graph.
func (f *GraphSeedFinder) findTopologicalRootSeeds(ctx context.Context) ([]GraphSeed, error) {
	cypher := `
		MATCH (fn)
		WHERE (fn:Function OR fn:Method)
		  AND (fn.scopeId = $scopeId OR fn.scopeId = 'main')
		  AND coalesce(fn.isExported, false) = true
		  AND coalesce(fn.inDegree, 0) = 0
		  AND coalesce(fn.outDegree, 0) > 0
		  AND coalesce(fn.isTestFunction, false) = false` + serviceClause + `
		RETURN fn.nodeKey AS nodeKey, fn.name AS name, labels(fn) AS labels,
		       coalesce(fn.outDegree, 0) AS outDegree
	`

	records, err := f.client.ExecuteQuery(ctx, cypher, f.serviceParams(nil))
	if err != nil {
		return nil, err
	}

	var seeds []GraphSeed
	for _, r := range records {
		m := r.AsMap()
		nodeKey := strValMap(m, "nodeKey")
		name := strValMap(m, "name")
		if nodeKey == "" || name == "" {
			continue
		}

		// Boost priority slightly based on outDegree (more downstream = more interesting).
		outDegree := int64Val(m, "outDegree")
		boost := int(min64(outDegree, 10))

		seeds = append(seeds, GraphSeed{
			NodeKey:  nodeKey,
			Name:     name,
			NodeType: labelType(m),
			SeedType: GraphSeedTopoRoot,
			Priority: graphSeedTierPriority[GraphSeedTopoRoot] + boost,
			Tier:     3,
		})
	}
	return seeds, nil
}

// findCentralitySeeds returns nodes with betweennessCentrality above threshold
// that have no higher-centrality caller in the same component. These are
// structurally important "gateway" functions.
func (f *GraphSeedFinder) findCentralitySeeds(ctx context.Context) ([]GraphSeed, error) {
	// Determine threshold: an explicitly configured value wins; otherwise use
	// THE shared centrality definition (see ComputeCentralityThreshold) so
	// this finder and the entry_points tool cannot drift apart.
	threshold := f.CentralityThreshold
	if threshold <= 0 {
		t, hasData, err := ComputeCentralityThreshold(ctx, f.client, f.scopeCtx.ScopeID)
		if err != nil {
			return nil, err
		}
		if !hasData {
			return nil, nil // No centrality data available (GDS not run yet).
		}
		threshold = t
	}

	// Find high-centrality nodes that have no caller with higher centrality
	// in the same componentId.
	cypher := `
		MATCH (fn)
		WHERE (fn:Function OR fn:Method)
		  AND (fn.scopeId = $scopeId OR fn.scopeId = 'main')
		  AND fn.betweennessCentrality > $threshold
		  AND coalesce(fn.isTestFunction, false) = false` + serviceClause + `
		OPTIONAL MATCH (caller)-[:CALLS]->(fn)
		WHERE (caller:Function OR caller:Method)
		  AND caller.betweennessCentrality > fn.betweennessCentrality
		  AND caller.componentId = fn.componentId
		WITH fn, count(caller) AS higherCallers
		WHERE higherCallers = 0
		RETURN fn.nodeKey AS nodeKey, fn.name AS name, labels(fn) AS labels,
		       fn.betweennessCentrality AS bc
		ORDER BY bc DESC
	`

	records, err := f.client.ExecuteQuery(ctx, cypher, f.serviceParams(map[string]any{
		"threshold": threshold,
	}))
	if err != nil {
		return nil, err
	}

	var seeds []GraphSeed
	// Find max BC for scaling.
	maxBC := threshold
	for _, r := range records {
		if bc, ok := r.AsMap()["bc"].(float64); ok && bc > maxBC {
			maxBC = bc
		}
	}

	for _, r := range records {
		m := r.AsMap()
		nodeKey := strValMap(m, "nodeKey")
		name := strValMap(m, "name")
		if nodeKey == "" || name == "" {
			continue
		}

		bc, _ := m["bc"].(float64)
		// Scale priority 60-80 based on relative centrality.
		scaled := 0
		if maxBC > threshold {
			scaled = int(20 * (bc - threshold) / (maxBC - threshold))
		}

		seeds = append(seeds, GraphSeed{
			NodeKey:  nodeKey,
			Name:     name,
			NodeType: labelType(m),
			SeedType: GraphSeedCentrality,
			Priority: graphSeedTierPriority[GraphSeedCentrality] + scaled,
			Tier:     4,
		})
	}
	return seeds, nil
}

// ComputeCentralityThreshold is THE high-centrality definition, shared by
// GraphSeedFinder's Tier 4 seeds and the entry_points MCP tool: a node is
// highly central when betweennessCentrality > mean + 1 stddev over the scope.
// hasData is false when no node in the scope carries betweennessCentrality
// (the GDS metrics stage hasn't run) — callers should treat the tier as empty
// rather than comparing against the returned fallback.
func ComputeCentralityThreshold(ctx context.Context, client *neo4j.Client, scopeId string) (threshold float64, hasData bool, err error) {
	statsCypher := `
		MATCH (fn)
		WHERE (fn:Function OR fn:Method)
		  AND fn.betweennessCentrality IS NOT NULL
		  AND (fn.scopeId = $scopeId OR fn.scopeId = 'main')
		WITH count(fn) AS cnt,
		     avg(fn.betweennessCentrality) AS mean,
		     stDev(fn.betweennessCentrality) AS sd
		RETURN cnt, mean + sd AS threshold
	`
	records, err := client.ExecuteQuery(ctx, statsCypher, map[string]any{"scopeId": scopeId})
	if err != nil {
		return 0, false, err
	}
	if len(records) == 0 {
		return 0, false, nil
	}
	m := records[0].AsMap()
	if int64Val(m, "cnt") == 0 {
		return 0, false, nil
	}
	if t, ok := m["threshold"].(float64); ok && t > 0 {
		return t, true, nil
	}
	// Degenerate but present data (e.g. all zeros): a tiny positive floor
	// keeps the > comparison meaningful.
	return 1.0, true, nil
}

// labelType extracts "Function" or "Method" from a labels array in a record map.
func labelType(m map[string]any) string {
	if labels, ok := m["labels"].([]any); ok {
		for _, l := range labels {
			if s, ok := l.(string); ok && s == "Method" {
				return "Method"
			}
		}
	}
	return "Function"
}

// strValMap extracts a string value from a record map.
func strValMap(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// int64Val extracts an int64 value from a record map.
func int64Val(m map[string]any, key string) int64 {
	if v, ok := m[key].(int64); ok {
		return v
	}
	return 0
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
