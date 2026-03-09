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
// "scope" and "scopeId" for a basic scope. Renaming these would break all
// Cypher property lookups.
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
			t.Errorf("Props() must have exactly 2 keys (no tenant/repo), got %d", len(props))
		}
	}
}

// TestPropsWithTenantID verifies that TenantID is included in Props() when set.
func TestPropsWithTenantID(t *testing.T) {
	sc := DefaultScope()
	sc.TenantID = "acme-corp"
	props := sc.Props()

	if props["tenantId"] != "acme-corp" {
		t.Errorf("expected tenantId=acme-corp, got %v", props["tenantId"])
	}
	if len(props) != 3 {
		t.Errorf("expected 3 props (scope, scopeId, tenantId), got %d", len(props))
	}
}

// TestPropsWithRepo verifies that Repo is included in Props() when set.
func TestPropsWithRepo(t *testing.T) {
	sc := NewPRScope("42")
	sc.Repo = "codegraph"
	props := sc.Props()

	if props["repoId"] != "codegraph" {
		t.Errorf("expected repoId=codegraph, got %v", props["repoId"])
	}
	if len(props) != 3 {
		t.Errorf("expected 3 props (scope, scopeId, repoId), got %d", len(props))
	}
}

// TestPropsWithTenantAndRepo verifies both TenantID and Repo appear in Props().
func TestPropsWithTenantAndRepo(t *testing.T) {
	sc := DefaultScope()
	sc.TenantID = "tenant-1"
	sc.Repo = "repo-1"
	props := sc.Props()

	if props["tenantId"] != "tenant-1" {
		t.Errorf("expected tenantId=tenant-1, got %v", props["tenantId"])
	}
	if props["repoId"] != "repo-1" {
		t.Errorf("expected repoId=repo-1, got %v", props["repoId"])
	}
	if len(props) != 4 {
		t.Errorf("expected 4 props, got %d", len(props))
	}
}

// TestPropsOmitsEmptyTenantAndRepo verifies empty strings are not included.
func TestPropsOmitsEmptyTenantAndRepo(t *testing.T) {
	sc := DefaultScope()
	sc.TenantID = ""
	sc.Repo = ""
	props := sc.Props()

	if _, ok := props["tenantId"]; ok {
		t.Error("empty TenantID should not appear in Props()")
	}
	if _, ok := props["repoId"]; ok {
		t.Error("empty Repo should not appear in Props()")
	}
}

// TestScopeContextFieldsPreserved verifies that all struct fields survive
// assignment and can be read back.
func TestScopeContextFieldsPreserved(t *testing.T) {
	sc := ScopeContext{
		Scope:    ScopePR,
		ScopeID:  "pr-99",
		TenantID: "org-abc",
		Repo:     "my-repo",
	}
	if sc.Scope != "pr" || sc.ScopeID != "pr-99" || sc.TenantID != "org-abc" || sc.Repo != "my-repo" {
		t.Errorf("field values not preserved: %+v", sc)
	}
}

// TestNormalizePRID tests the NormalizePRID function with various input patterns.
func TestNormalizePRID(t *testing.T) {
	cases := []struct {
		name    string
		rawID   string
		wantID  string
	}{
		{"plain number", "42", "42"},
		{"with pr- prefix", "pr-42", "42"},
		{"double prefix", "pr-pr-42", "pr-42"},
		{"empty string", "", ""},
		{"just pr-", "pr-", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := NormalizePRID(c.rawID)
			if got != c.wantID {
				t.Errorf("NormalizePRID(%q) = %q, want %q", c.rawID, got, c.wantID)
			}
		})
	}
}

// TestParseScopeFlags tests ParseScopeFlags with all valid and invalid combinations.
func TestParseScopeFlags(t *testing.T) {
	cases := []struct {
		name      string
		scopeFlag string
		scopeID   string
		wantErr   bool
		wantScope string
		wantID    string
	}{
		// Happy path: PR scope
		{"pr scope with pr-prefixed id", "pr", "pr-42", false, "pr", "pr-42"},
		{"pr scope with plain id", "pr", "42", false, "pr", "pr-42"},
		{"pr scope with double-prefixed id", "pr", "pr-pr-42", false, "pr", "pr-pr-42"},

		// Happy path: main scope (default)
		{"default scope no id", "", "", false, "main", "main"},
		{"main scope no id", "main", "", false, "main", "main"},
		{"main scope with main id", "main", "main", false, "main", "main"},

		// Error cases
		{"pr scope without scope-id", "pr", "", true, "", ""},
		{"non-pr scope with scope-id", "", "pr-42", true, "", ""},
		{"main scope with non-main scope-id", "main", "pr-99", true, "", ""},
		{"unknown scope with scope-id", "unknown", "pr-42", true, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sc, err := ParseScopeFlags(c.scopeFlag, c.scopeID)
			if (err != nil) != c.wantErr {
				t.Errorf("ParseScopeFlags(%q, %q): error = %v, wantErr %v", c.scopeFlag, c.scopeID, err, c.wantErr)
			}
			if !c.wantErr {
				if sc.Scope != c.wantScope {
					t.Errorf("ParseScopeFlags(%q, %q).Scope = %q, want %q", c.scopeFlag, c.scopeID, sc.Scope, c.wantScope)
				}
				if sc.ScopeID != c.wantID {
					t.Errorf("ParseScopeFlags(%q, %q).ScopeID = %q, want %q", c.scopeFlag, c.scopeID, sc.ScopeID, c.wantID)
				}
			}
		})
	}
}
