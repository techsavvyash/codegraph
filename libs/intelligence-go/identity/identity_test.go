package identity

import "testing"

func TestScopedID(t *testing.T) {
	got := ScopedID("main", "func:path#sig")
	want := "main::func:path#sig"
	if got != want {
		t.Errorf("ScopedID = %q, want %q", got, want)
	}
}

func TestParseRoundTrip(t *testing.T) {
	scope, key := "main", "func:path#sig"
	id := ScopedID(scope, key)
	gotScope, gotKey, ok := Parse(id)
	if !ok {
		t.Fatal("Parse returned ok=false for scoped ID")
	}
	if gotScope != scope {
		t.Errorf("scopeID = %q, want %q", gotScope, scope)
	}
	if gotKey != key {
		t.Errorf("nodeKey = %q, want %q", gotKey, key)
	}
}

func TestParseUnscoped(t *testing.T) {
	scopeID, nodeKey, ok := Parse("func:path#sig")
	if ok {
		t.Error("Parse returned ok=true for unscoped ID")
	}
	if scopeID != "" {
		t.Errorf("scopeID = %q, want empty", scopeID)
	}
	if nodeKey != "func:path#sig" {
		t.Errorf("nodeKey = %q, want %q", nodeKey, "func:path#sig")
	}
}

func TestMustParsePanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("MustParse did not panic on unscoped ID")
		}
	}()
	MustParse("no-scope-here")
}

func TestMustParseValid(t *testing.T) {
	scope, key := MustParse("pr-42::func:main.go#Run")
	if scope != "pr-42" {
		t.Errorf("scopeID = %q, want %q", scope, "pr-42")
	}
	if key != "func:main.go#Run" {
		t.Errorf("nodeKey = %q, want %q", key, "func:main.go#Run")
	}
}

func TestIsScoped(t *testing.T) {
	tests := []struct {
		id   string
		want bool
	}{
		{"main::func:a#b", true},
		{"func:a#b", false},
		{"", false},
		{"::", true},
	}
	for _, tc := range tests {
		if got := IsScoped(tc.id); got != tc.want {
			t.Errorf("IsScoped(%q) = %v, want %v", tc.id, got, tc.want)
		}
	}
}

func TestNodeKeyExtraction(t *testing.T) {
	tests := []struct {
		id   string
		want string
	}{
		{"main::func:a#b", "func:a#b"},
		{"func:a#b", "func:a#b"},
		{"pr-42::file:main.go", "file:main.go"},
	}
	for _, tc := range tests {
		if got := NodeKey(tc.id); got != tc.want {
			t.Errorf("NodeKey(%q) = %q, want %q", tc.id, got, tc.want)
		}
	}
}

func TestScopeIDExtraction(t *testing.T) {
	tests := []struct {
		id   string
		want string
	}{
		{"main::func:a#b", "main"},
		{"func:a#b", ""},
		{"pr-42::file:main.go", "pr-42"},
	}
	for _, tc := range tests {
		if got := ScopeID(tc.id); got != tc.want {
			t.Errorf("ScopeID(%q) = %q, want %q", tc.id, got, tc.want)
		}
	}
}

func TestCollisionDifferentScopes(t *testing.T) {
	key := "func:path#sig"
	id1 := ScopedID("main", key)
	id2 := ScopedID("pr-42", key)
	if id1 == id2 {
		t.Errorf("same nodeKey in different scopes should produce different IDs: %q == %q", id1, id2)
	}
}

func TestEmptyInputs(t *testing.T) {
	// Empty scope, non-empty key
	id := ScopedID("", "func:a#b")
	if id != "::func:a#b" {
		t.Errorf("ScopedID with empty scope = %q, want %q", id, "::func:a#b")
	}
	scope, key, ok := Parse(id)
	if !ok {
		t.Error("Parse returned ok=false for '::func:a#b'")
	}
	if scope != "" {
		t.Errorf("scope = %q, want empty", scope)
	}
	if key != "func:a#b" {
		t.Errorf("key = %q, want %q", key, "func:a#b")
	}

	// Empty input
	_, nodeKey, ok := Parse("")
	if ok {
		t.Error("Parse returned ok=true for empty string")
	}
	if nodeKey != "" {
		t.Errorf("nodeKey = %q, want empty", nodeKey)
	}

	// Both empty
	id2 := ScopedID("", "")
	if id2 != "::" {
		t.Errorf("ScopedID(\"\", \"\") = %q, want %q", id2, "::")
	}
}
