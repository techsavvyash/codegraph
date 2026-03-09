package bundles

// ExpansionBudget controls how much context is expanded around anchors.
type ExpansionBudget struct {
	// MaxExpansionDepth limits the depth of graph traversal from each anchor.
	MaxExpansionDepth int `json:"maxExpansionDepth"`

	// MaxExpansionsPerAnchor limits how many expansions come from each anchor.
	MaxExpansionsPerAnchor int `json:"maxExpansionsPerAnchor"`

	// MaxTotalExpansions limits total expansions across all anchors.
	MaxTotalExpansions int `json:"maxTotalExpansions"`

	// MaxTotalAnchors limits the number of anchors included.
	MaxTotalAnchors int `json:"maxTotalAnchors"`

	// MaxBundleTokens is the estimated token budget for the entire bundle.
	MaxBundleTokens int `json:"maxBundleTokens"`

	// AllowedExpansionTypes restricts which node types can be expanded into.
	// Empty means all types allowed.
	AllowedExpansionTypes []string `json:"allowedExpansionTypes,omitempty"`

	// AllowedRelationTypes restricts which edge types are traversed.
	// Empty means all types allowed.
	AllowedRelationTypes []string `json:"allowedRelationTypes,omitempty"`
}

// DefaultExpansionBudget returns a conservative budget suitable for most tasks.
var DefaultExpansionBudget = ExpansionBudget{
	MaxExpansionDepth:      2,
	MaxExpansionsPerAnchor: 5,
	MaxTotalExpansions:     20,
	MaxTotalAnchors:        10,
	MaxBundleTokens:        4000,
	AllowedExpansionTypes:  []string{"Function", "Method", "Class", "Interface", "File"},
	AllowedRelationTypes:   []string{"CALLS", "CONTAINS", "HAS_STEP", "IMPLEMENTS", "INHERITS_FROM"},
}

// IsNodeTypeAllowed checks if a node type is permitted by the budget.
func (b ExpansionBudget) IsNodeTypeAllowed(nodeType string) bool {
	if len(b.AllowedExpansionTypes) == 0 {
		return true
	}
	for _, allowed := range b.AllowedExpansionTypes {
		if allowed == nodeType {
			return true
		}
	}
	return false
}

// IsRelationAllowed checks if a relation type is permitted by the budget.
func (b ExpansionBudget) IsRelationAllowed(relType string) bool {
	if len(b.AllowedRelationTypes) == 0 {
		return true
	}
	for _, allowed := range b.AllowedRelationTypes {
		if allowed == relType {
			return true
		}
	}
	return false
}
