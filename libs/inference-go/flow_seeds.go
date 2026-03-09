package inference

import (
	"strings"
)

// FlowSeedType classifies the kind of structural seed found.
type FlowSeedType string

const (
	SeedExportedRoot   FlowSeedType = "exported_root"   // Exported function with no callers
	SeedEntrypoint     FlowSeedType = "entrypoint"      // Name patterns: main, init, handler, etc.
	SeedHTTPHandler    FlowSeedType = "http_handler"    // Framework-detected HTTP handler
	SeedMessageHandler FlowSeedType = "message_handler" // Message consumer/worker
	SeedTestEntry      FlowSeedType = "test_entry"      // Test function
)

// FlowSeed represents a detected entry point for flow derivation.
type FlowSeed struct {
	NodeKey  string       `json:"nodeKey"`
	Name     string       `json:"name"`
	NodeType string       `json:"nodeType"`
	SeedType FlowSeedType `json:"seedType"`
	Priority int          `json:"priority"` // Higher = more likely to be a real entry point
	Reasons  []string     `json:"reasons"`
}

// MinSeedScore is the minimum priority score required for a seed to be included.
const MinSeedScore = 30

// Deprecated: StructuralSeedFinder uses name-based heuristics for seed detection.
// Use GraphSeedFinder instead, which uses purely graph-structural signals.
// StructuralSeedFinder is retained as a fallback when graph metrics are unavailable.
//
// StructuralSeedFinder detects flow entry points from structural signals alone,
// independent of any framework detector. Framework detectors can boost priority
// as optional precision add-ons.
type StructuralSeedFinder struct {
	// FrameworkBoosters are optional functions that can increase seed priority
	// based on framework-specific knowledge.
	FrameworkBoosters []FrameworkBooster

	// Budget controls traversal bounds and blocked patterns for filtering.
	// When set, seeds whose names match BlockedPatterns are excluded.
	Budget *TraversalBudget
}

// FrameworkBooster is an optional function that can adjust seed priority
// based on framework-specific knowledge.
type FrameworkBooster interface {
	// Boost adjusts the priority of a seed if the booster recognizes it.
	// Returns the adjusted priority and any additional reasons.
	Boost(seed *FlowSeed) (priority int, reasons []string)
}

// NewStructuralSeedFinder creates a framework-agnostic seed finder.
func NewStructuralSeedFinder() *StructuralSeedFinder {
	return &StructuralSeedFinder{}
}

// WithFrameworkBoosters adds optional framework-specific boosters.
func (f *StructuralSeedFinder) WithFrameworkBoosters(boosters ...FrameworkBooster) *StructuralSeedFinder {
	f.FrameworkBoosters = boosters
	return f
}

// WithBudget sets the traversal budget for seed filtering.
func (f *StructuralSeedFinder) WithBudget(budget TraversalBudget) *StructuralSeedFinder {
	f.Budget = &budget
	return f
}

// NodeInfo is the minimal information needed for seed detection.
type NodeInfo struct {
	NodeKey       string
	Name          string
	NodeType      string // "Function", "Method"
	IsExported    bool
	HasCallers    bool // Whether any other function calls this one
	IncomingCalls int64
	OutgoingCalls int64
	APILinked     bool
	HasDocstring  bool
	FilePath      string
	Parameters    []string // Parameter type names, for signature analysis
}

// ClassifySeeds determines which nodes are flow entry points using structural signals.
func (f *StructuralSeedFinder) ClassifySeeds(nodes []NodeInfo) []FlowSeed {
	var seeds []FlowSeed

	for _, n := range nodes {
		seed := f.classifyNode(n)
		if seed == nil {
			continue
		}

		// Apply framework boosters
		for _, booster := range f.FrameworkBoosters {
			priority, reasons := booster.Boost(seed)
			if priority > seed.Priority {
				seed.Priority = priority
				seed.Reasons = append(seed.Reasons, reasons...)
			}
		}

		if seed.Priority >= MinSeedScore {
			seeds = append(seeds, *seed)
		}
	}

	return seeds
}

// classifyNode determines if a single node is a flow seed.
func (f *StructuralSeedFinder) classifyNode(n NodeInfo) *FlowSeed {
	nameLower := strings.ToLower(n.Name)

	// Test and benchmark functions are intentionally excluded from production
	// flow seeds to keep generated flows domain-focused.
	if isTestName(nameLower) || strings.HasSuffix(strings.ToLower(n.FilePath), "_test.go") {
		return nil
	}

	// Use budget's blocked patterns to exclude utility/generic symbols.
	if f.Budget != nil && f.Budget.IsNameBlocked(n.Name) {
		return nil
	}

	if isLowSignalEntrypointName(nameLower) &&
		!isHTTPHandlerName(nameLower) &&
		!isMessageHandlerName(nameLower) &&
		!strings.Contains(nameLower, "api") &&
		!strings.Contains(nameLower, "route") {
		return nil
	}

	if isGenericNoiseEntrypoint(nameLower, n.FilePath) &&
		!isHTTPHandlerName(nameLower) &&
		!isMessageHandlerName(nameLower) &&
		!strings.Contains(nameLower, "api") &&
		!strings.Contains(nameLower, "route") {
		return nil
	}

	// Signal 1: Exported function with no callers = likely entry point
	if n.IsExported && !n.HasCallers {
		seed := &FlowSeed{
			NodeKey:  n.NodeKey,
			Name:     n.Name,
			NodeType: n.NodeType,
			SeedType: SeedExportedRoot,
			Priority: 48,
			Reasons:  []string{"exported", "no_callers"},
		}

		if n.APILinked {
			seed.Priority += 28
			seed.Reasons = append(seed.Reasons, "api_linked")
		}
		if n.OutgoingCalls > 0 {
			seed.Priority += minInt(int(n.OutgoingCalls), 8)
			seed.Reasons = append(seed.Reasons, "has_downstream_calls")
		}
		if hasBusinessFileSignal(n.FilePath) {
			seed.Priority += 8
			seed.Reasons = append(seed.Reasons, "business_path_signal")
		}
		if hasUtilityPathSignal(n.FilePath) {
			seed.Priority -= 12
			seed.Reasons = append(seed.Reasons, "utility_path_penalty")
		}

		// Boost if name suggests entry point
		if isEntrypointName(nameLower) {
			seed.Priority += 8
			seed.SeedType = SeedEntrypoint
			seed.Reasons = append(seed.Reasons, "entrypoint_name_pattern")
		}

		// Boost if it has an HTTP handler-like signature
		if isHTTPHandlerName(nameLower) {
			seed.Priority += 30
			seed.SeedType = SeedHTTPHandler
			seed.Reasons = append(seed.Reasons, "http_handler_pattern")
		}

		// Boost if message/event handler
		if isMessageHandlerName(nameLower) {
			seed.Priority += 25
			seed.SeedType = SeedMessageHandler
			seed.Reasons = append(seed.Reasons, "message_handler_pattern")
		}

		return seed
	}

	// Signal 2: Non-exported but matches strong entry point patterns and has no callers
	if !n.HasCallers && isStrongEntrypointName(nameLower) {
		priority := 40
		if n.APILinked {
			priority += 15
		}
		if hasUtilityPathSignal(n.FilePath) {
			priority -= 8
		}
		return &FlowSeed{
			NodeKey:  n.NodeKey,
			Name:     n.Name,
			NodeType: n.NodeType,
			SeedType: SeedEntrypoint,
			Priority: priority,
			Reasons:  []string{"strong_entrypoint_pattern", "no_callers"},
		}
	}

	return nil
}

// isEntrypointName checks for names that commonly indicate entry points.
func isEntrypointName(name string) bool {
	patterns := []string{
		"main", "init", "run", "start", "serve", "listen",
		"execute", "process", "dispatch",
	}
	for _, p := range patterns {
		if name == p || strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// isHTTPHandlerName checks for HTTP handler name patterns.
func isHTTPHandlerName(name string) bool {
	patterns := []string{
		"handler", "handle", "endpoint", "controller",
		"route", "middleware", "interceptor",
	}
	for _, p := range patterns {
		if strings.Contains(name, p) {
			return true
		}
	}
	return false
}

// isMessageHandlerName checks for message/event handler patterns.
func isMessageHandlerName(name string) bool {
	patterns := []string{
		"consumer", "subscriber", "listener", "worker",
		"processor", "receiver", "onmessage", "onevent",
		"job", "task", "cron",
	}
	for _, p := range patterns {
		if strings.Contains(name, p) {
			return true
		}
	}
	return false
}

// isStrongEntrypointName checks for names that are almost certainly entry points.
func isStrongEntrypointName(name string) bool {
	return name == "main" || name == "init" ||
		strings.HasPrefix(name, "handle") ||
		strings.HasPrefix(name, "serve") ||
		strings.HasSuffix(name, "handler") ||
		strings.HasSuffix(name, "controller")
}

// isTestName checks if the function is a test function.
func isTestName(name string) bool {
	return strings.HasPrefix(name, "test") || strings.HasPrefix(name, "bench")
}

func isLowSignalEntrypointName(name string) bool {
	switch name {
	case "execute", "start", "init", "do":
		return true
	default:
		return false
	}
}

func isGenericNoiseEntrypoint(name, filePath string) bool {
	if name == "main" {
		// Allow canonical command entrypoints in main.go, suppress other generic main symbols.
		return !(strings.HasSuffix(filePath, "/main.go") || strings.HasSuffix(filePath, "main.go"))
	}
	for _, exact := range []string{"run", "start", "execute", "newclient", "defaultconfig", "configfromenv", "newdriverwithcontext"} {
		if name == exact {
			return true
		}
	}
	for _, prefix := range []string{"new", "get", "set"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func hasBusinessFileSignal(filePath string) bool {
	lower := strings.ToLower(filePath)
	for _, token := range []string{"/api/", "/handler", "/service", "/workflow", "/domain", "/usecase"} {
		if strings.Contains(lower, token) {
			return true
		}
	}
	return false
}

func hasUtilityPathSignal(filePath string) bool {
	lower := strings.ToLower(filePath)
	for _, token := range []string{"/internal/", "/util", "/utils", "/vendor/", "/generated/"} {
		if strings.Contains(lower, token) {
			return true
		}
	}
	return false
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
