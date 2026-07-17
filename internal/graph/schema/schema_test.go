package schema

import (
	"strings"
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

// TestGetFulltextIndexes verifies that GetFulltextIndexes returns the expected set
// with only real properties for each label.
func TestGetFulltextIndexes(t *testing.T) {
	indexes := GetFulltextIndexes()

	// Expected indexes: 7 hot code labels + 2 docs labels (RFC-011)
	expectedIndexes := map[string][]string{
		"function_fulltext":      {"name", "signature"},
		"method_fulltext":        {"name", "signature"},
		"class_fulltext":         {"name"},
		"interface_fulltext":     {"name"},
		"symbol_fulltext":        {"name", "displayName"},
		"file_fulltext":          {"path"},
		"variable_fulltext":      {"name"},
		"document_fulltext":      {"title", "sourceUrl"},
		"documentchunk_fulltext": {"content", "headingPath"},
	}

	// Verify we have exactly the expected indexes
	if len(indexes) != len(expectedIndexes) {
		t.Errorf("GetFulltextIndexes returned %d indexes, want %d", len(indexes), len(expectedIndexes))
	}

	indexMap := make(map[string][]string)
	for _, idx := range indexes {
		indexMap[idx.Name] = idx.Properties
	}

	// Verify each expected index exists with correct properties
	for expectedName, expectedProps := range expectedIndexes {
		props, ok := indexMap[expectedName]
		if !ok {
			t.Errorf("missing fulltext index: %s", expectedName)
			continue
		}

		// Check properties match exactly
		if len(props) != len(expectedProps) {
			t.Errorf("index %s has %d properties, want %d", expectedName, len(props), len(expectedProps))
			continue
		}

		for i, prop := range props {
			if prop != expectedProps[i] {
				t.Errorf("index %s property %d: got %q, want %q", expectedName, i, prop, expectedProps[i])
			}
		}
	}

	// Verify no unexpected indexes
	for name := range indexMap {
		if _, ok := expectedIndexes[name]; !ok {
			t.Errorf("unexpected fulltext index: %s", name)
		}
	}
}

// TestGetFulltextIndexesNamingConvention verifies index naming follows the pattern.
func TestGetFulltextIndexesNamingConvention(t *testing.T) {
	indexes := GetFulltextIndexes()

	for _, idx := range indexes {
		// Index names follow the pattern {label}_fulltext (labels here are
		// all single-word, so plain lowercasing is the whole rule).
		expectedName := strings.ToLower(idx.NodeLabel) + "_fulltext"
		if idx.Name != expectedName {
			t.Errorf("index name %q does not follow {label}_fulltext pattern (expected %q)", idx.Name, expectedName)
		}
	}
}

// TestGetVectorIndexes verifies the RFC-011 Layer S vector index set: chunk
// embeddings plus one code-summary index per summarizable label.
func TestGetVectorIndexes(t *testing.T) {
	indexes := GetVectorIndexes()

	expected := map[string]string{
		"chunk_embedding":             "DocumentChunk",
		"function_summary_embedding":  "Function",
		"method_summary_embedding":    "Method",
		"class_summary_embedding":     "Class",
		"interface_summary_embedding": "Interface",
		"file_summary_embedding":      "File",
	}

	if len(indexes) != len(expected) {
		t.Errorf("GetVectorIndexes returned %d indexes, want %d", len(indexes), len(expected))
	}

	for _, idx := range indexes {
		wantLabel, ok := expected[idx.Name]
		if !ok {
			t.Errorf("unexpected vector index: %s", idx.Name)
			continue
		}
		if idx.NodeLabel != wantLabel {
			t.Errorf("vector index %s targets label %q, want %q", idx.Name, idx.NodeLabel, wantLabel)
		}
		if idx.Property != "embedding" {
			t.Errorf("vector index %s targets property %q, want embedding", idx.Name, idx.Property)
		}
	}
}

// TestVectorIndexDDL pins the exact Neo4j 5 statement shape, including the
// backtick-quoted option keys and the parameterized dimension.
func TestVectorIndexDDL(t *testing.T) {
	idx := VectorIndex{Name: "chunk_embedding", NodeLabel: "DocumentChunk", Property: "embedding"}
	got := vectorIndexDDL(idx, 1536)
	want := "CREATE VECTOR INDEX chunk_embedding IF NOT EXISTS FOR (n:DocumentChunk) ON n.embedding " +
		"OPTIONS {indexConfig: {`vector.dimensions`: 1536, `vector.similarity_function`: 'cosine'}}"
	if got != want {
		t.Errorf("vectorIndexDDL mismatch:\n got: %s\nwant: %s", got, want)
	}
}

// TestDocsRangeIndexes verifies the RFC-011 serviceName range indexes exist in
// the master index list (scoped doc queries must not scan).
func TestDocsRangeIndexes(t *testing.T) {
	byName := make(map[string]Index)
	for _, idx := range GetIndexes() {
		byName[idx.Name] = idx
	}

	for name, want := range map[string]struct {
		label string
		props []string
	}{
		"document_service_idx": {"Document", []string{"serviceName"}},
		"docchunk_service_idx": {"DocumentChunk", []string{"serviceName"}},
	} {
		idx, ok := byName[name]
		if !ok {
			t.Errorf("missing range index %s", name)
			continue
		}
		if idx.NodeLabel != want.label {
			t.Errorf("index %s targets label %q, want %q", name, idx.NodeLabel, want.label)
		}
		if len(idx.Properties) != len(want.props) || idx.Properties[0] != want.props[0] {
			t.Errorf("index %s properties %v, want %v", name, idx.Properties, want.props)
		}
	}
}
