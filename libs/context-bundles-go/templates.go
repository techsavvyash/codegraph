package bundles

// Template name constants.
const (
	TemplateFlowSummary       = "flow_summary"
	TemplatePRSummary         = "pr_summary"
	TemplateDocstringSuggest  = "docstring_suggestion"
	TemplateFeatureToCode     = "feature_to_code"
)

// TemplateSpec defines the schema and constraints for a bundle template.
type TemplateSpec struct {
	// Name is the template identifier.
	Name string `json:"name"`

	// Description explains when this template is used.
	Description string `json:"description"`

	// Budget overrides the default expansion budget for this template.
	Budget ExpansionBudget `json:"budget"`

	// RequiredMetadataKeys lists metadata keys that must be present on anchors.
	RequiredMetadataKeys []string `json:"requiredMetadataKeys,omitempty"`

	// PreferredNodeTypes biases expansion toward these node types.
	PreferredNodeTypes []string `json:"preferredNodeTypes,omitempty"`

	// MaxStatements limits the number of generated statements (for generation stage).
	MaxStatements int `json:"maxStatements"`
}

// BuiltinTemplates returns the predefined template specs for each task type.
func BuiltinTemplates() map[string]TemplateSpec {
	return map[string]TemplateSpec{
		TemplateFlowSummary: {
			Name:        TemplateFlowSummary,
			Description: "Summarizes an execution flow from entry point through call chain",
			Budget: ExpansionBudget{
				MaxExpansionDepth:      3,
				MaxExpansionsPerAnchor: 8,
				MaxTotalExpansions:     30,
				MaxTotalAnchors:        5,
				MaxBundleTokens:        6000,
				AllowedExpansionTypes:  []string{"Function", "Method", "Class", "Interface"},
				AllowedRelationTypes:   []string{"CALLS", "HAS_STEP", "CONTAINS"},
			},
			PreferredNodeTypes: []string{"Function", "Method"},
			MaxStatements:      20,
		},

		TemplatePRSummary: {
			Name:        TemplatePRSummary,
			Description: "Summarizes changes in a pull request with impact analysis",
			Budget: ExpansionBudget{
				MaxExpansionDepth:      2,
				MaxExpansionsPerAnchor: 5,
				MaxTotalExpansions:     25,
				MaxTotalAnchors:        15,
				MaxBundleTokens:        5000,
				AllowedExpansionTypes:  []string{"Function", "Method", "Class", "Interface", "File"},
				AllowedRelationTypes:   []string{"CALLS", "CONTAINS", "IMPLEMENTS", "INHERITS_FROM"},
			},
			PreferredNodeTypes: []string{"Function", "Method", "File"},
			MaxStatements:      15,
		},

		TemplateDocstringSuggest: {
			Name:        TemplateDocstringSuggest,
			Description: "Generates docstring suggestions for functions/methods",
			Budget: ExpansionBudget{
				MaxExpansionDepth:      1,
				MaxExpansionsPerAnchor: 3,
				MaxTotalExpansions:     10,
				MaxTotalAnchors:        3,
				MaxBundleTokens:        2000,
				AllowedExpansionTypes:  []string{"Function", "Method", "Class", "Interface"},
				AllowedRelationTypes:   []string{"CALLS", "CONTAINS"},
			},
			RequiredMetadataKeys: []string{"signature"},
			PreferredNodeTypes:   []string{"Function", "Method"},
			MaxStatements:        5,
		},

		TemplateFeatureToCode: {
			Name:        TemplateFeatureToCode,
			Description: "Maps business features to implementing code locations",
			Budget: ExpansionBudget{
				MaxExpansionDepth:      2,
				MaxExpansionsPerAnchor: 6,
				MaxTotalExpansions:     20,
				MaxTotalAnchors:        8,
				MaxBundleTokens:        4000,
				AllowedExpansionTypes:  []string{"Function", "Method", "Class", "File", "Feature", "Document"},
				AllowedRelationTypes:   []string{"CALLS", "CONTAINS", "MENTIONS", "HAS_STEP"},
			},
			PreferredNodeTypes: []string{"Function", "Class", "Feature"},
			MaxStatements:      10,
		},
	}
}

// GetTemplateSpec returns the spec for a named template.
// Returns the default spec if the template is not found.
func GetTemplateSpec(name string) TemplateSpec {
	templates := BuiltinTemplates()
	if spec, ok := templates[name]; ok {
		return spec
	}
	return TemplateSpec{
		Name:          name,
		Description:   "Custom template",
		Budget:        DefaultExpansionBudget,
		MaxStatements: 10,
	}
}

// ValidateAnchors checks that anchors satisfy the template's required metadata keys.
func ValidateAnchors(anchors []AnchorValidation, spec TemplateSpec) []AnchorViolation {
	if len(spec.RequiredMetadataKeys) == 0 {
		return nil
	}

	var violations []AnchorViolation
	for _, anchor := range anchors {
		for _, key := range spec.RequiredMetadataKeys {
			if _, ok := anchor.Metadata[key]; !ok {
				violations = append(violations, AnchorViolation{
					NodeKey:    anchor.NodeKey,
					MissingKey: key,
				})
			}
		}
	}
	return violations
}

// AnchorValidation is a lightweight struct for validation input.
type AnchorValidation struct {
	NodeKey  string
	Metadata map[string]any
}

// AnchorViolation reports a missing required metadata key on an anchor.
type AnchorViolation struct {
	NodeKey    string `json:"nodeKey"`
	MissingKey string `json:"missingKey"`
}

// TemplateBudgetBuilder creates a Builder pre-configured for a specific template.
func TemplateBudgetBuilder(templateName string) *Builder {
	spec := GetTemplateSpec(templateName)
	return NewBuilder().WithBudget(spec.Budget)
}
