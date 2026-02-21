package models

import (
	"testing"
)

func TestDefaultScope(t *testing.T) {
	sc := DefaultScope()
	if sc.Scope != ScopeMain {
		t.Errorf("expected scope %q, got %q", ScopeMain, sc.Scope)
	}
	if sc.ScopeID != ScopeMain {
		t.Errorf("expected scopeId %q, got %q", ScopeMain, sc.ScopeID)
	}
}

func TestNewPRScope(t *testing.T) {
	sc := NewPRScope("42")
	if sc.Scope != ScopePR {
		t.Errorf("expected scope %q, got %q", ScopePR, sc.Scope)
	}
	if sc.ScopeID != "pr-42" {
		t.Errorf("expected scopeId %q, got %q", "pr-42", sc.ScopeID)
	}
}

func TestScopeProps(t *testing.T) {
	sc := NewPRScope("99")
	props := sc.Props()
	if props["scope"] != "pr" {
		t.Errorf("expected scope pr, got %v", props["scope"])
	}
	if props["scopeId"] != "pr-99" {
		t.Errorf("expected scopeId pr-99, got %v", props["scopeId"])
	}
}

func TestDefaultScopeProps(t *testing.T) {
	sc := DefaultScope()
	props := sc.Props()
	if props["scope"] != "main" {
		t.Errorf("expected scope main, got %v", props["scope"])
	}
	if props["scopeId"] != "main" {
		t.Errorf("expected scopeId main, got %v", props["scopeId"])
	}
}

// TestScopeConstants freezes the string values of ScopeMain and ScopePR.
// Changing these would break all existing Neo4j nodes.
func TestScopeConstants(t *testing.T) {
	if ScopeMain != "main" {
		t.Errorf("ScopeMain must be \"main\", got %q", ScopeMain)
	}
	if ScopePR != "pr" {
		t.Errorf("ScopePR must be \"pr\", got %q", ScopePR)
	}
}

// TestScopeConstantsDistinct ensures ScopeMain and ScopePR are different values
// so they can be used as discriminators in Cypher WHERE clauses.
func TestScopeConstantsDistinct(t *testing.T) {
	if ScopeMain == ScopePR {
		t.Error("ScopeMain and ScopePR must be distinct")
	}
}

// TestDefaultScopeIsMain verifies that DefaultScope produces scope="main" and
// scopeId="main" — the baseline invariant for all non-PR indexed nodes.
func TestDefaultScopeIsMain(t *testing.T) {
	sc := DefaultScope()
	if sc.Scope != "main" {
		t.Errorf("DefaultScope.Scope must be \"main\", got %q", sc.Scope)
	}
	if sc.ScopeID != "main" {
		t.Errorf("DefaultScope.ScopeID must be \"main\", got %q", sc.ScopeID)
	}
}

// TestNewPRScopeIDFormat freezes the exact format "pr-{prID}" for PR scope IDs.
// The format is embedded in Cypher queries and must not change silently.
func TestNewPRScopeIDFormat(t *testing.T) {
	cases := []struct {
		prID       string
		wantScopeID string
	}{
		{"1", "pr-1"},
		{"42", "pr-42"},
		{"999", "pr-999"},
	}
	for _, c := range cases {
		sc := NewPRScope(c.prID)
		if sc.ScopeID != c.wantScopeID {
			t.Errorf("NewPRScope(%q).ScopeID = %q, want %q", c.prID, sc.ScopeID, c.wantScopeID)
		}
		if sc.Scope != ScopePR {
			t.Errorf("NewPRScope(%q).Scope = %q, want %q", c.prID, sc.Scope, ScopePR)
		}
	}
}

// TestDifferentPRIDsProduceDifferentScopeIDs ensures two distinct PR IDs
// yield distinct ScopeIDs to avoid scope cross-contamination.
func TestDifferentPRIDsProduceDifferentScopeIDs(t *testing.T) {
	sc1 := NewPRScope("1")
	sc2 := NewPRScope("2")
	if sc1.ScopeID == sc2.ScopeID {
		t.Errorf("different PR IDs must produce different ScopeIDs: both = %q", sc1.ScopeID)
	}
}

// TestMainScopeAndPRScopeHaveDifferentScopeField ensures that baseline nodes
// (scope="main") and overlay nodes (scope="pr") can always be distinguished
// by the Scope field alone.
func TestMainScopeAndPRScopeHaveDifferentScopeField(t *testing.T) {
	main := DefaultScope()
	pr := NewPRScope("1")
	if main.Scope == pr.Scope {
		t.Errorf("DefaultScope and NewPRScope must have different Scope fields, both = %q", main.Scope)
	}
}

// TestPropsKeysAreStable verifies that Props() always returns exactly the keys
// "scope" and "scopeId". Renaming these would break all Cypher property lookups.
func TestPropsKeysAreStable(t *testing.T) {
	for _, sc := range []ScopeContext{DefaultScope(), NewPRScope("7")} {
		props := sc.Props()
		if _, ok := props["scope"]; !ok {
			t.Error("Props() must contain key \"scope\"")
		}
		if _, ok := props["scopeId"]; !ok {
			t.Error("Props() must contain key \"scopeId\"")
		}
		if len(props) != 2 {
			t.Errorf("Props() must have exactly 2 keys, got %d", len(props))
		}
	}
}
