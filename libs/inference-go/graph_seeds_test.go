package inference

import (
	"testing"
)

func TestGraphSeedTierPriorities(t *testing.T) {
	// Verify tier ordering: higher tiers have higher priority.
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

	// Tier 2 seed added first.
	seedMap["key1"] = GraphSeed{
		NodeKey:  "key1",
		Name:     "HandleRequest",
		SeedType: GraphSeedTopoRoot,
		Priority: 70,
		Tier:     2,
	}

	// Tier 1 seed for same nodeKey should replace.
	s := GraphSeed{
		NodeKey:  "key1",
		Name:     "HandleRequest",
		SeedType: GraphSeedInterfaceImpl,
		Priority: 85,
		Tier:     1,
	}
	if existing, ok := seedMap[s.NodeKey]; !ok || s.Tier < existing.Tier {
		seedMap[s.NodeKey] = s
	}

	if seedMap["key1"].SeedType != GraphSeedInterfaceImpl {
		t.Errorf("expected interface_impl, got %s", seedMap["key1"].SeedType)
	}
	if seedMap["key1"].Priority != 85 {
		t.Errorf("expected priority 85, got %d", seedMap["key1"].Priority)
	}

	// Tier 3 seed for same nodeKey should NOT replace tier 1.
	s3 := GraphSeed{
		NodeKey:  "key1",
		Name:     "HandleRequest",
		SeedType: GraphSeedCentrality,
		Priority: 75,
		Tier:     3,
	}
	if existing, ok := seedMap[s3.NodeKey]; !ok || s3.Tier < existing.Tier {
		seedMap[s3.NodeKey] = s3
	}

	if seedMap["key1"].SeedType != GraphSeedInterfaceImpl {
		t.Errorf("tier 3 should not replace tier 1, got %s", seedMap["key1"].SeedType)
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
