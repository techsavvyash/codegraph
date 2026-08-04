package inference

import (
	"testing"
)

func TestGraphSeedTierPriorities(t *testing.T) {
	// Verify tier ordering: higher tiers have higher priority.
	if graphSeedTierPriority[GraphSeedAPIExposed] <= graphSeedTierPriority[GraphSeedInterfaceImpl] {
		t.Error("API exposed should have higher priority than interface impl")
	}
	if graphSeedTierPriority[GraphSeedInterfaceImpl] <= graphSeedTierPriority[GraphSeedTopoRoot] {
		t.Error("Interface impl should have higher priority than topological root")
	}
	if graphSeedTierPriority[GraphSeedTopoRoot] <= graphSeedTierPriority[GraphSeedCentrality] {
		t.Error("Topological root should have higher priority than centrality")
	}
}

func TestGraphSeedDedup(t *testing.T) {
	// Simulate dedup logic: higher tier wins.
	seedMap := make(map[string]GraphSeed)

	// Tier 3 seed added first.
	seedMap["key1"] = GraphSeed{
		NodeKey:  "key1",
		Name:     "HandleRequest",
		SeedType: GraphSeedTopoRoot,
		Priority: 70,
		Tier:     3,
	}

	// Tier 1 seed for same nodeKey should replace.
	s := GraphSeed{
		NodeKey:  "key1",
		Name:     "HandleRequest",
		SeedType: GraphSeedAPIExposed,
		Priority: 100,
		Tier:     1,
	}
	if existing, ok := seedMap[s.NodeKey]; !ok || s.Tier < existing.Tier {
		seedMap[s.NodeKey] = s
	}

	if seedMap["key1"].SeedType != GraphSeedAPIExposed {
		t.Errorf("expected api_exposed, got %s", seedMap["key1"].SeedType)
	}
	if seedMap["key1"].Priority != 100 {
		t.Errorf("expected priority 100, got %d", seedMap["key1"].Priority)
	}

	// Tier 4 seed for same nodeKey should NOT replace tier 1.
	s4 := GraphSeed{
		NodeKey:  "key1",
		Name:     "HandleRequest",
		SeedType: GraphSeedCentrality,
		Priority: 75,
		Tier:     4,
	}
	if existing, ok := seedMap[s4.NodeKey]; !ok || s4.Tier < existing.Tier {
		seedMap[s4.NodeKey] = s4
	}

	if seedMap["key1"].SeedType != GraphSeedAPIExposed {
		t.Errorf("tier 4 should not replace tier 1, got %s", seedMap["key1"].SeedType)
	}
}

// TestApiExposedPriority locks the Tier 1 sub-priority ordering, including
// the RFC-005 decorator-detection case: a decorator-routed handler (NestJS
// @Get/@Post/etc., which SCIP can't see via CALLS edges) must rank at 95,
// the same as external-params detection and above the bare cross-pkg
// heuristic (90), since decorator syntax is an equally definitive framework
// signal.
func TestApiExposedPriority(t *testing.T) {
	tests := []struct {
		name            string
		hasExternal     bool
		isCrossPkg      bool
		detectionSource string
		want            int
	}{
		{"external + cross-pkg", true, true, "", 100},
		{"external only", true, false, "", 95},
		{"decorator detected", false, false, "decorator", 95},
		{"cross-pkg only", false, true, "", 90},
		{"decorator detected but also cross-pkg (cross-pkg case wins, both are structural)", false, true, "decorator", 90},
		{"none (legacy framework fallback)", false, false, "", 85},
		{"unrecognized detectionSource", false, false, "cross_pkg", 85},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := apiExposedPriority(tt.hasExternal, tt.isCrossPkg, tt.detectionSource)
			if got != tt.want {
				t.Errorf("apiExposedPriority(%v, %v, %q) = %d, want %d",
					tt.hasExternal, tt.isCrossPkg, tt.detectionSource, got, tt.want)
			}
		})
	}

	// Decorator detection must never be beaten by the legacy fallback, and
	// must rank strictly above bare cross-pkg detection.
	decoratorPriority := apiExposedPriority(false, false, "decorator")
	crossPkgPriority := apiExposedPriority(false, true, "")
	if decoratorPriority <= crossPkgPriority {
		t.Errorf("decorator priority (%d) must exceed cross-pkg-only priority (%d)", decoratorPriority, crossPkgPriority)
	}
}

func TestLabelType(t *testing.T) {
	tests := []struct {
		name   string
		labels []any
		want   string
	}{
		{"function only", []any{"Function"}, "Function"},
		{"method", []any{"Function", "Method"}, "Method"},
		{"empty", nil, "Function"},
		{"other labels", []any{"Node", "Exported"}, "Function"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := map[string]any{"labels": tt.labels}
			if got := labelType(m); got != tt.want {
				t.Errorf("labelType() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStrValMap(t *testing.T) {
	m := map[string]any{
		"name": "foo",
		"num":  42,
	}
	if got := strValMap(m, "name"); got != "foo" {
		t.Errorf("strValMap(name) = %q, want foo", got)
	}
	if got := strValMap(m, "missing"); got != "" {
		t.Errorf("strValMap(missing) = %q, want empty", got)
	}
	if got := strValMap(m, "num"); got != "" {
		t.Errorf("strValMap(num) = %q, want empty (not a string)", got)
	}
}

func TestMin64(t *testing.T) {
	if min64(3, 5) != 3 {
		t.Error("min64(3,5) should be 3")
	}
	if min64(10, 2) != 2 {
		t.Error("min64(10,2) should be 2")
	}
}
