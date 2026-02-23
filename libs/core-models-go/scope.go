package models

// ScopeContext represents the scope context for indexing operations.
// Scope is either "main" (default branch) or "pr" (pull request overlay).
// ScopeID uniquely identifies the scope instance (e.g., "main", "pr-42").
type ScopeContext struct {
	Scope    string // "main" or "pr"
	ScopeID  string // "main" or "pr-{id}"
	TenantID string // Multi-tenant namespace (optional).
	Repo     string // Repository identifier (optional).
}

const (
	ScopeMain = "main"
	ScopePR   = "pr"
)

// DefaultScope returns the default scope context (main branch).
func DefaultScope() ScopeContext {
	return ScopeContext{
		Scope:   ScopeMain,
		ScopeID: ScopeMain,
	}
}

// NewPRScope creates a scope context for a pull request overlay.
func NewPRScope(prID string) ScopeContext {
	return ScopeContext{
		Scope:   ScopePR,
		ScopeID: "pr-" + prID,
	}
}

// Props returns the scope fields as a map for injection into Neo4j properties.
func (sc ScopeContext) Props() map[string]any {
	m := map[string]any{
		"scope":   sc.Scope,
		"scopeId": sc.ScopeID,
	}
	if sc.TenantID != "" {
		m["tenantId"] = sc.TenantID
	}
	if sc.Repo != "" {
		m["repoId"] = sc.Repo
	}
	return m
}
