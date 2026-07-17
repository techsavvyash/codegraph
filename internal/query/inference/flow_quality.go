package inference

// TraversalBudget controls the bounds of flow derivation traversal.
type TraversalBudget struct {
	// MaxDepth is the maximum call graph depth to traverse.
	MaxDepth int `json:"maxDepth"`

	// MaxFanout is the maximum number of callees to follow at each level.
	MaxFanout int `json:"maxFanout"`

	// MaxSteps is the absolute maximum number of steps in a flow.
	MaxSteps int `json:"maxSteps"`

	// AllowedNodeTypes restricts which node types can be flow steps.
	// Empty means all types are allowed.
	AllowedNodeTypes []string `json:"allowedNodeTypes,omitempty"`

	// BlockedPatterns are node name patterns to exclude from flows.
	// These filter out utility/helper functions that add noise.
	BlockedPatterns []string `json:"blockedPatterns,omitempty"`
}

// DefaultTraversalBudget provides sensible defaults for flow traversal.
var DefaultTraversalBudget = TraversalBudget{
	MaxDepth:  5,
	MaxFanout: 10,
	MaxSteps:  50,
	AllowedNodeTypes: []string{
		"Function", "Method", "APIRoute", "Service",
	},
	BlockedPatterns: []string{
		"log", "print", "debug", "trace",
		"string", "format", "sprintf",
		"error", "panic", "fatal",
		"close", "defer", "cleanup",
		"test", "bench", "init",
		"main", "run",
		"newclient", "defaultconfig", "configfromenv",
		"executequery", "executeread", "executewrite",
		"get", "set", // too generic
	},
}

// IsNodeAllowed checks if a node type is in the allowed list.
func (b *TraversalBudget) IsNodeAllowed(nodeType string) bool {
	if len(b.AllowedNodeTypes) == 0 {
		return true
	}
	for _, t := range b.AllowedNodeTypes {
		if t == nodeType {
			return true
		}
	}
	return false
}

// IsNameBlocked checks if a node name matches any blocked pattern.
func (b *TraversalBudget) IsNameBlocked(name string) bool {
	if len(b.BlockedPatterns) == 0 {
		return false
	}
	nameLower := toLower(name)
	for _, p := range b.BlockedPatterns {
		if nameLower == p || hasPrefix(nameLower, p) || hasSuffix(nameLower, p) {
			return true
		}
	}
	return false
}

// toLower is a simple lowercase helper to avoid importing strings in this file.
func toLower(s string) string {
	b := make([]byte, len(s))
	for i := range s {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		} else {
			b[i] = c
		}
	}
	return string(b)
}

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

func hasSuffix(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}

// FlowDeduplicator removes duplicate and redundant steps from a flow.
type FlowDeduplicator struct{}

// NewFlowDeduplicator creates a new deduplicator.
func NewFlowDeduplicator() *FlowDeduplicator {
	return &FlowDeduplicator{}
}

// FlowStepInfo is a step in a flow for deduplication purposes.
type FlowStepInfo struct {
	NodeKey  string
	Name     string
	NodeType string
	Order    int
}

// Deduplicate removes duplicate steps (same nodeKey) from a flow,
// keeping the first occurrence. Also removes steps that exceed the budget.
func (d *FlowDeduplicator) Deduplicate(steps []FlowStepInfo, budget TraversalBudget) []FlowStepInfo {
	return d.DeduplicateAnchored(steps, budget, 0)
}

// DeduplicateAnchored is Deduplicate with the first `anchors` steps exempt
// from name/type blocking. Anchor steps are what the flow is ABOUT — the
// APIRoute and its handler, or the entrypoint seed — built deliberately by
// the generator; name patterns like "get"/"set" exist to drop noisy traversed
// CALLEES, and applying them to anchors silently strips e.g. every
// "GET /api/users" route and "GetUsers" handler out of its own flow.
// Anchors are still deduplicated by nodeKey and count toward MaxSteps.
func (d *FlowDeduplicator) DeduplicateAnchored(steps []FlowStepInfo, budget TraversalBudget, anchors int) []FlowStepInfo {
	seen := make(map[string]bool)
	var deduped []FlowStepInfo

	for i, s := range steps {
		// Skip duplicates
		if seen[s.NodeKey] {
			continue
		}

		if i >= anchors {
			// Skip blocked names
			if budget.IsNameBlocked(s.Name) {
				continue
			}

			// Skip disallowed node types
			if !budget.IsNodeAllowed(s.NodeType) {
				continue
			}
		}

		// Enforce max steps
		if len(deduped) >= budget.MaxSteps {
			break
		}

		seen[s.NodeKey] = true
		deduped = append(deduped, s)
	}

	// Reorder steps sequentially
	for i := range deduped {
		deduped[i].Order = i
	}

	return deduped
}

// FlowQualityMetrics tracks quality signals for a derived flow.
type FlowQualityMetrics struct {
	TotalSteps       int     `json:"totalSteps"`
	UniqueSteps      int     `json:"uniqueSteps"`
	DuplicateSteps   int     `json:"duplicateSteps"`
	BlockedSteps     int     `json:"blockedSteps"`
	MaxDepthReached  bool    `json:"maxDepthReached"`
	MaxFanoutReached bool    `json:"maxFanoutReached"`
	SpuriousStepRate float64 `json:"spuriousStepRate"`
}

// ComputeFlowQuality computes quality metrics for a flow.
func ComputeFlowQuality(originalSteps, dedupedSteps []FlowStepInfo, budget TraversalBudget) FlowQualityMetrics {
	duplicates := len(originalSteps) - len(dedupedSteps)
	if duplicates < 0 {
		duplicates = 0
	}

	// Count blocked steps
	blocked := 0
	for _, s := range originalSteps {
		if budget.IsNameBlocked(s.Name) || !budget.IsNodeAllowed(s.NodeType) {
			blocked++
		}
	}

	spuriousRate := 0.0
	if len(originalSteps) > 0 {
		spuriousRate = float64(duplicates+blocked) / float64(len(originalSteps))
	}

	return FlowQualityMetrics{
		TotalSteps:       len(originalSteps),
		UniqueSteps:      len(dedupedSteps),
		DuplicateSteps:   duplicates,
		BlockedSteps:     blocked,
		MaxDepthReached:  len(dedupedSteps) >= budget.MaxSteps,
		SpuriousStepRate: spuriousRate,
	}
}
