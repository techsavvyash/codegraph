package neo4j

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// TestIdent
// ---------------------------------------------------------------------------

func TestIdent(t *testing.T) {
	tests := []struct {
		name string
		input string
		want string
	}{
		{
			name:  "plain identifier passes through",
			input: "Function",
			want:  "Function",
		},
		{
			name:  "identifier with underscore and digits",
			input: "MyFunction_v2",
			want:  "MyFunction_v2",
		},
		{
			name:  "identifier starting with underscore",
			input: "_privateName",
			want:  "_privateName",
		},
		{
			name:  "hyphenated name gets backticks",
			input: "my-label",
			want:  "`my-label`",
		},
		{
			name:  "name with space gets backticks",
			input: "my label",
			want:  "`my label`",
		},
		{
			name:  "name with embedded backtick gets doubled",
			input: "my`label",
			want:  "`my``label`",
		},
		{
			name:  "multiple embedded backticks",
			input: "a`b`c",
			want:  "`a``b``c`",
		},
		{
			name:  "empty string gets backticks",
			input: "",
			want:  "``",
		},
		{
			name:  "name starting with digit gets backticks",
			input: "1Function",
			want:  "`1Function`",
		},
		{
			name:  "special characters",
			input: "my-special.label",
			want:  "`my-special.label`",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Ident(tt.input)
			if got != tt.want {
				t.Errorf("Ident(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestTombstoneFilter
// ---------------------------------------------------------------------------

func TestTombstoneFilter(t *testing.T) {
	tests := []struct {
		name     string
		nodeVar  string
		scopeID  string
		wantEmpty bool
		wantContains string
		wantNotContains string
	}{
		{
			name:      "empty scopeID returns empty",
			nodeVar:   "n",
			scopeID:   "",
			wantEmpty: true,
		},
		{
			name:      "main scopeID returns empty",
			nodeVar:   "n",
			scopeID:   "main",
			wantEmpty: true,
		},
		{
			name:         "PR scopeID returns filter",
			nodeVar:      "n",
			scopeID:      "pr-42",
			wantEmpty:    false,
			wantContains: "Tombstone",
		},
		{
			name:         "uses correct nodeVar",
			nodeVar:      "node",
			scopeID:      "pr-99",
			wantEmpty:    false,
			wantContains: "node.nodeKey",
		},
		{
			name:         "uses bound parameter $scopeId",
			nodeVar:      "n",
			scopeID:      "pr-42",
			wantEmpty:    false,
			wantContains: "$scopeId",
		},
		{
			name:            "does not contain raw scope value",
			nodeVar:         "n",
			scopeID:         "pr-42",
			wantEmpty:       false,
			wantNotContains: `"pr-42"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TombstoneFilter(tt.nodeVar, tt.scopeID)
			if tt.wantEmpty && got != "" {
				t.Errorf("expected empty string, got %q", got)
			}
			if !tt.wantEmpty && got == "" {
				t.Error("expected non-empty filter string")
			}
			if tt.wantContains != "" && !strings.Contains(got, tt.wantContains) {
				t.Errorf("expected filter to contain %q, got %q", tt.wantContains, got)
			}
			if tt.wantNotContains != "" && strings.Contains(got, tt.wantNotContains) {
				t.Errorf("expected filter to NOT contain %q, got %q", tt.wantNotContains, got)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestGetString / TestGetInt — helper functions
// ---------------------------------------------------------------------------

func TestGetString(t *testing.T) {
	m := map[string]any{
		"name":  "test",
		"count": 42,
	}

	if getString(m, "name") != "test" {
		t.Error("expected 'test'")
	}
	if getString(m, "count") != "" {
		t.Error("expected empty string for non-string value")
	}
	if getString(m, "missing") != "" {
		t.Error("expected empty string for missing key")
	}
}

func TestGetInt(t *testing.T) {
	m := map[string]any{
		"count64": int64(42),
		"count":   10,
		"name":    "test",
	}

	if getInt(m, "count64") != 42 {
		t.Errorf("expected 42 for int64 value")
	}
	if getInt(m, "count") != 10 {
		t.Errorf("expected 10 for int value")
	}
	if getInt(m, "name") != 0 {
		t.Error("expected 0 for non-int value")
	}
	if getInt(m, "missing") != 0 {
		t.Error("expected 0 for missing key")
	}
}

// ---------------------------------------------------------------------------
// TestScopedKey / TestApplyScopedKey
// ---------------------------------------------------------------------------

func TestScopedKey(t *testing.T) {
	tests := []struct {
		name    string
		nodeKey string
		scopeId string
		want    string
	}{
		{
			name:    "nodeKey and scopeId",
			nodeKey: "my-node",
			scopeId: "pr-42",
			want:    "my-node|pr-42",
		},
		{
			name:    "nodeKey with empty scopeId defaults to main",
			nodeKey: "my-node",
			scopeId: "",
			want:    "my-node|main",
		},
		{
			name:    "nodeKey with main scopeId",
			nodeKey: "my-node",
			scopeId: "main",
			want:    "my-node|main",
		},
		{
			name:    "complex nodeKey with special chars",
			nodeKey: "module/package/Class",
			scopeId: "feature/branch",
			want:    "module/package/Class|feature/branch",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ScopedKey(tt.nodeKey, tt.scopeId)
			if got != tt.want {
				t.Errorf("ScopedKey(%q, %q) = %q, want %q", tt.nodeKey, tt.scopeId, got, tt.want)
			}
		})
	}
}

func TestApplyScopedKey(t *testing.T) {
	tests := []struct {
		name      string
		props     map[string]any
		wantKey   string
		wantExists bool
	}{
		{
			name: "adds scopedKey when nodeKey exists",
			props: map[string]any{
				"nodeKey": "my-node",
				"scopeId": "pr-42",
			},
			wantKey:    "my-node|pr-42",
			wantExists: true,
		},
		{
			name: "defaults scopeId to main when absent",
			props: map[string]any{
				"nodeKey": "my-node",
				"name":    "test",
			},
			wantKey:    "my-node|main",
			wantExists: true,
		},
		{
			name: "does not add scopedKey when nodeKey is missing",
			props: map[string]any{
				"scopeId": "pr-42",
				"name":    "test",
			},
			wantExists: false,
		},
		{
			name: "does not add scopedKey when nodeKey is empty string",
			props: map[string]any{
				"nodeKey": "",
				"scopeId": "pr-42",
			},
			wantExists: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			applyScopedKey(tt.props)
			key, exists := tt.props["scopedKey"]

			if exists != tt.wantExists {
				t.Errorf("scopedKey exists = %v, want %v", exists, tt.wantExists)
			}
			if exists && key != tt.wantKey {
				t.Errorf("scopedKey = %q, want %q", key, tt.wantKey)
			}
		})
	}
}
