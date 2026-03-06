package bundles

import (
	"testing"
)

func TestBuiltinTemplates_AllDefined(t *testing.T) {
	templates := BuiltinTemplates()

	expected := []string{
		TemplateFlowSummary,
		TemplatePRSummary,
		TemplateDocstringSuggest,
		TemplateFeatureToCode,
	}

	for _, name := range expected {
		spec, ok := templates[name]
		if !ok {
			t.Errorf("missing builtin template: %s", name)
			continue
		}
		if spec.Name != name {
			t.Errorf("template %s has wrong name: %s", name, spec.Name)
		}
		if spec.Description == "" {
			t.Errorf("template %s has empty description", name)
		}
		if spec.Budget.MaxBundleTokens == 0 {
			t.Errorf("template %s has zero MaxBundleTokens", name)
		}
		if spec.MaxStatements == 0 {
			t.Errorf("template %s has zero MaxStatements", name)
		}
	}
}

func TestGetTemplateSpec_Known(t *testing.T) {
	spec := GetTemplateSpec(TemplateFlowSummary)
	if spec.Name != TemplateFlowSummary {
		t.Errorf("expected flow_summary, got %s", spec.Name)
	}
	if spec.Budget.MaxExpansionDepth != 3 {
		t.Errorf("expected depth 3 for flow_summary, got %d", spec.Budget.MaxExpansionDepth)
	}
}

func TestGetTemplateSpec_Unknown(t *testing.T) {
	spec := GetTemplateSpec("nonexistent_template")
	if spec.Name != "nonexistent_template" {
		t.Errorf("expected name passthrough, got %s", spec.Name)
	}
	// Should return default budget
	if spec.Budget.MaxBundleTokens != DefaultExpansionBudget.MaxBundleTokens {
		t.Errorf("expected default budget for unknown template")
	}
}

func TestValidateAnchors_NoRequired(t *testing.T) {
	spec := GetTemplateSpec(TemplateFlowSummary) // no required metadata
	anchors := []AnchorValidation{
		{NodeKey: "func:a", Metadata: map[string]any{}},
	}
	violations := ValidateAnchors(anchors, spec)
	if len(violations) != 0 {
		t.Errorf("expected 0 violations for template without required keys, got %d", len(violations))
	}
}

func TestValidateAnchors_MissingRequired(t *testing.T) {
	spec := GetTemplateSpec(TemplateDocstringSuggest) // requires "signature"
	anchors := []AnchorValidation{
		{NodeKey: "func:a", Metadata: map[string]any{"name": "foo"}},
		{NodeKey: "func:b", Metadata: map[string]any{"signature": "func foo()"}},
	}

	violations := ValidateAnchors(anchors, spec)
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation, got %d", len(violations))
	}
	if violations[0].NodeKey != "func:a" {
		t.Errorf("expected violation on func:a, got %s", violations[0].NodeKey)
	}
	if violations[0].MissingKey != "signature" {
		t.Errorf("expected missing key 'signature', got %s", violations[0].MissingKey)
	}
}

func TestValidateAnchors_AllPresent(t *testing.T) {
	spec := GetTemplateSpec(TemplateDocstringSuggest)
	anchors := []AnchorValidation{
		{NodeKey: "func:a", Metadata: map[string]any{"signature": "func foo()"}},
	}

	violations := ValidateAnchors(anchors, spec)
	if len(violations) != 0 {
		t.Errorf("expected 0 violations, got %d", len(violations))
	}
}

func TestTemplateBudgetBuilder(t *testing.T) {
	builder := TemplateBudgetBuilder(TemplateFlowSummary)
	if builder.budget.MaxExpansionDepth != 3 {
		t.Errorf("expected depth 3 from flow_summary template, got %d", builder.budget.MaxExpansionDepth)
	}
	if builder.budget.MaxBundleTokens != 6000 {
		t.Errorf("expected 6000 tokens from flow_summary template, got %d", builder.budget.MaxBundleTokens)
	}
}

func TestTemplateSpecs_BudgetsAreBounded(t *testing.T) {
	templates := BuiltinTemplates()
	for name, spec := range templates {
		if spec.Budget.MaxTotalExpansions > 50 {
			t.Errorf("template %s has overly large MaxTotalExpansions: %d", name, spec.Budget.MaxTotalExpansions)
		}
		if spec.Budget.MaxBundleTokens > 10000 {
			t.Errorf("template %s has overly large MaxBundleTokens: %d", name, spec.Budget.MaxBundleTokens)
		}
		if spec.Budget.MaxExpansionDepth > 5 {
			t.Errorf("template %s has overly large MaxExpansionDepth: %d", name, spec.Budget.MaxExpansionDepth)
		}
	}
}

func TestBudget_IsNodeTypeAllowed(t *testing.T) {
	budget := ExpansionBudget{AllowedExpansionTypes: []string{"Function", "Method"}}
	if !budget.IsNodeTypeAllowed("Function") {
		t.Error("expected Function allowed")
	}
	if budget.IsNodeTypeAllowed("Variable") {
		t.Error("expected Variable not allowed")
	}

	// Empty means all allowed
	empty := ExpansionBudget{}
	if !empty.IsNodeTypeAllowed("anything") {
		t.Error("expected all allowed when empty")
	}
}

func TestBudget_IsRelationAllowed(t *testing.T) {
	budget := ExpansionBudget{AllowedRelationTypes: []string{"CALLS"}}
	if !budget.IsRelationAllowed("CALLS") {
		t.Error("expected CALLS allowed")
	}
	if budget.IsRelationAllowed("CONTAINS") {
		t.Error("expected CONTAINS not allowed")
	}

	empty := ExpansionBudget{}
	if !empty.IsRelationAllowed("anything") {
		t.Error("expected all allowed when empty")
	}
}
