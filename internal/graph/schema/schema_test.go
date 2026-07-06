package schema

import (
	"testing"
)

// TestGetConstraints verifies that constraints are populated for identity (RFC-005 I2).
func TestGetConstraints(t *testing.T) {
	constraints := GetConstraints()

	// Constraints must be non-empty (invariant I2)
	if len(constraints) == 0 {
		t.Error("GetConstraints() returned empty list; identity constraints must be defined")
	}

	// Every constraint must be UNIQUE on scopedKey
	for _, c := range constraints {
		if c.Type != "UNIQUE" {
			t.Errorf("constraint %s has type %q, want UNIQUE", c.Name, c.Type)
		}
		if c.Property != "scopedKey" {
			t.Errorf("constraint %s is on property %q, want scopedKey", c.Name, c.Property)
		}
		if c.NodeLabel == "" {
			t.Errorf("constraint %s has empty NodeLabel", c.Name)
		}
	}

	// Verify we have constraints for key labels with nodeKey indexes
	labelSet := make(map[string]bool)
	for _, c := range constraints {
		labelSet[c.NodeLabel] = true
	}

	// Expected labels with nodeKey indexes (from GetIndexes)
	expectedLabels := map[string]bool{
		"Service":       true,
		"File":          true,
		"Symbol":        true,
		"Function":      true,
		"Method":        true,
		"Class":         true,
		"Interface":     true,
		"Module":        true,
		"Variable":      true,
		"Parameter":     true,
		"APIRoute":      true,
		"Document":      true,
		"Feature":       true,
		"Reference":     true,
		"DocumentChunk": true,
		"Flow":          true,
		"PullRequest":   true,
	}

	for label := range expectedLabels {
		if !labelSet[label] {
			t.Errorf("missing constraint for label %q (has nodeKey index)", label)
		}
	}
}

// TestGetConstraintsNamingConvention verifies constraint naming follows the pattern.
func TestGetConstraintsNamingConvention(t *testing.T) {
	constraints := GetConstraints()

	for _, c := range constraints {
		// Constraint names should follow pattern: {Label}_scoped_key_unique
		expectedName := c.NodeLabel + "_scoped_key_unique"
		if c.Name != expectedName {
			t.Errorf("constraint name %q does not match expected pattern %q", c.Name, expectedName)
		}
	}
}
