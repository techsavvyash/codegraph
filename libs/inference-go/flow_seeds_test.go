package inference

import (
	"testing"
)

func TestStructuralSeedFinder_ExportedRoot(t *testing.T) {
	finder := NewStructuralSeedFinder()

	nodes := []NodeInfo{
		{NodeKey: "func:a", Name: "BuildGraph", NodeType: "Function", IsExported: true, HasCallers: false},
	}

	seeds := finder.ClassifySeeds(nodes)
	if len(seeds) != 1 {
		t.Fatalf("expected 1 seed, got %d", len(seeds))
	}
	if seeds[0].SeedType != SeedExportedRoot {
		t.Errorf("expected exported_root, got %s", seeds[0].SeedType)
	}
	if seeds[0].Priority < 50 {
		t.Errorf("expected priority >= 50, got %d", seeds[0].Priority)
	}
}

func TestStructuralSeedFinder_HTTPHandler(t *testing.T) {
	finder := NewStructuralSeedFinder()

	nodes := []NodeInfo{
		{NodeKey: "func:b", Name: "HandleUserCreate", NodeType: "Function", IsExported: true, HasCallers: false},
	}

	seeds := finder.ClassifySeeds(nodes)
	if len(seeds) != 1 {
		t.Fatalf("expected 1 seed, got %d", len(seeds))
	}
	if seeds[0].SeedType != SeedHTTPHandler {
		t.Errorf("expected http_handler, got %s", seeds[0].SeedType)
	}
	if seeds[0].Priority < 80 {
		t.Errorf("expected boosted priority >= 80, got %d", seeds[0].Priority)
	}
}

func TestStructuralSeedFinder_MessageHandler(t *testing.T) {
	finder := NewStructuralSeedFinder()

	nodes := []NodeInfo{
		{NodeKey: "func:c", Name: "ProcessOrderConsumer", NodeType: "Function", IsExported: true, HasCallers: false},
	}

	seeds := finder.ClassifySeeds(nodes)
	if len(seeds) != 1 {
		t.Fatalf("expected 1 seed, got %d", len(seeds))
	}
	if seeds[0].SeedType != SeedMessageHandler {
		t.Errorf("expected message_handler, got %s", seeds[0].SeedType)
	}
}

func TestStructuralSeedFinder_TestFunction(t *testing.T) {
	finder := NewStructuralSeedFinder()

	nodes := []NodeInfo{
		{NodeKey: "func:d", Name: "testUserCreation", NodeType: "Function", IsExported: false, HasCallers: false},
	}

	seeds := finder.ClassifySeeds(nodes)
	// Test functions have priority 20, which is below MinSeedScore (30), so they are filtered out
	if len(seeds) != 0 {
		t.Fatalf("expected 0 seeds (test function below MinSeedScore), got %d", len(seeds))
	}
}

func TestStructuralSeedFinder_NonExportedWithCallers(t *testing.T) {
	finder := NewStructuralSeedFinder()

	// Non-exported function with callers should NOT be a seed
	nodes := []NodeInfo{
		{NodeKey: "func:e", Name: "helperFunc", NodeType: "Function", IsExported: false, HasCallers: true},
	}

	seeds := finder.ClassifySeeds(nodes)
	if len(seeds) != 0 {
		t.Errorf("expected 0 seeds for non-exported function with callers, got %d", len(seeds))
	}
}

func TestStructuralSeedFinder_StrongEntrypointNonExported(t *testing.T) {
	finder := NewStructuralSeedFinder()

	nodes := []NodeInfo{
		{NodeKey: "func:f", Name: "main", NodeType: "Function", IsExported: false, HasCallers: false, FilePath: "cmd/codegraph/main.go"},
	}

	seeds := finder.ClassifySeeds(nodes)
	if len(seeds) != 1 {
		t.Fatalf("expected 1 seed for 'main', got %d", len(seeds))
	}
	if seeds[0].SeedType != SeedEntrypoint {
		t.Errorf("expected entrypoint, got %s", seeds[0].SeedType)
	}
}

func TestStructuralSeedFinder_FiltersGenericNoiseEntrypoints(t *testing.T) {
	finder := NewStructuralSeedFinder()

	nodes := []NodeInfo{
		{NodeKey: "func:a", Name: "Run", NodeType: "Function", IsExported: true, HasCallers: false},
		{NodeKey: "func:b", Name: "NewClient", NodeType: "Function", IsExported: true, HasCallers: false},
		{NodeKey: "func:c", Name: "HandleRequest", NodeType: "Function", IsExported: true, HasCallers: false},
	}

	seeds := finder.ClassifySeeds(nodes)
	if len(seeds) != 1 {
		t.Fatalf("expected only high-signal seed to remain, got %d", len(seeds))
	}
	if seeds[0].Name != "HandleRequest" {
		t.Fatalf("expected HandleRequest seed, got %s", seeds[0].Name)
	}
}

func TestStructuralSeedFinder_MultipleCandidates(t *testing.T) {
	finder := NewStructuralSeedFinder()

	nodes := []NodeInfo{
		{NodeKey: "func:1", Name: "HandleRequest", NodeType: "Function", IsExported: true, HasCallers: false},
		{NodeKey: "func:2", Name: "processJob", NodeType: "Function", IsExported: false, HasCallers: true}, // not a seed
		{NodeKey: "func:3", Name: "main", NodeType: "Function", IsExported: false, HasCallers: false},
		{NodeKey: "func:4", Name: "testSomething", NodeType: "Function", IsExported: false, HasCallers: false},
	}

	seeds := finder.ClassifySeeds(nodes)
	// testSomething is filtered; main without main.go is filtered as generic noise.
	if len(seeds) != 1 {
		t.Errorf("expected 1 seed, got %d", len(seeds))
		for _, s := range seeds {
			t.Logf("  seed: %s (%s, priority=%d)", s.Name, s.SeedType, s.Priority)
		}
	}
}

func TestStructuralSeedFinder_WorksWithoutFrameworkDetectors(t *testing.T) {
	// This test validates T3.3: flow derivation works without framework detectors
	finder := NewStructuralSeedFinder() // No framework boosters

	nodes := []NodeInfo{{NodeKey: "func:generic", Name: "HandleEvents", NodeType: "Function", IsExported: true, HasCallers: false}}

	seeds := finder.ClassifySeeds(nodes)
	if len(seeds) == 0 {
		t.Error("expected seeds without framework detectors")
	}
	if seeds[0].SeedType != SeedHTTPHandler {
		t.Errorf("expected http_handler seed type, got %s", seeds[0].SeedType)
	}
}

// --- Framework Booster Test ---

type mockBooster struct {
	matchName   string
	addPriority int
}

func (m *mockBooster) Boost(seed *FlowSeed) (int, []string) {
	if seed.Name == m.matchName {
		return seed.Priority + m.addPriority, []string{"framework_boost"}
	}
	return seed.Priority, nil
}

func TestStructuralSeedFinder_WithFrameworkBooster(t *testing.T) {
	booster := &mockBooster{matchName: "ServeHTTP", addPriority: 50}
	finder := NewStructuralSeedFinder().WithFrameworkBoosters(booster)

	nodes := []NodeInfo{
		{NodeKey: "func:a", Name: "ServeHTTP", NodeType: "Method", IsExported: true, HasCallers: false},
		{NodeKey: "func:b", Name: "ProcessData", NodeType: "Function", IsExported: true, HasCallers: false},
	}

	seeds := finder.ClassifySeeds(nodes)
	if len(seeds) != 2 {
		t.Fatalf("expected 2 seeds, got %d", len(seeds))
	}

	// ServeHTTP should have higher priority due to booster
	var serveHTTP, processData *FlowSeed
	for i := range seeds {
		if seeds[i].Name == "ServeHTTP" {
			serveHTTP = &seeds[i]
		} else {
			processData = &seeds[i]
		}
	}

	if serveHTTP == nil || processData == nil {
		t.Fatal("missing expected seeds")
	}
	if serveHTTP.Priority <= processData.Priority {
		t.Errorf("expected ServeHTTP priority (%d) > ProcessData priority (%d)",
			serveHTTP.Priority, processData.Priority)
	}
}

func TestStructuralSeedFinder_WithBudgetBlockedPatterns(t *testing.T) {
	budget := TraversalBudget{
		BlockedPatterns: []string{"log", "debug"},
	}
	finder := NewStructuralSeedFinder().WithBudget(budget)

	nodes := []NodeInfo{
		{NodeKey: "func:1", Name: "LogRequest", NodeType: "Function", IsExported: true, HasCallers: false},
		{NodeKey: "func:2", Name: "HandleCreate", NodeType: "Function", IsExported: true, HasCallers: false},
		{NodeKey: "func:3", Name: "DebugInfo", NodeType: "Function", IsExported: true, HasCallers: false},
	}

	seeds := finder.ClassifySeeds(nodes)
	if len(seeds) != 1 {
		t.Errorf("expected 1 seed (only HandleCreate), got %d", len(seeds))
		for _, s := range seeds {
			t.Logf("  seed: %s (%s)", s.Name, s.SeedType)
		}
	}
	if len(seeds) > 0 && seeds[0].Name != "HandleCreate" {
		t.Errorf("expected HandleCreate, got %s", seeds[0].Name)
	}
}

// --- Name pattern tests ---

func TestIsEntrypointName(t *testing.T) {
	positives := []string{"main", "init", "run", "start", "serve", "listen"}
	for _, name := range positives {
		if !isEntrypointName(name) {
			t.Errorf("expected %q to be entrypoint name", name)
		}
	}

	negatives := []string{"helper", "utils", "parse", "format"}
	for _, name := range negatives {
		if isEntrypointName(name) {
			t.Errorf("expected %q NOT to be entrypoint name", name)
		}
	}
}

func TestIsHTTPHandlerName(t *testing.T) {
	positives := []string{"handler", "handlerequest", "usercontroller", "authroute", "middleware"}
	for _, name := range positives {
		if !isHTTPHandlerName(name) {
			t.Errorf("expected %q to be HTTP handler name", name)
		}
	}
}

func TestIsMessageHandlerName(t *testing.T) {
	positives := []string{"consumer", "messageworker", "eventlistener", "orderprocessor"}
	for _, name := range positives {
		if !isMessageHandlerName(name) {
			t.Errorf("expected %q to be message handler name", name)
		}
	}
}
