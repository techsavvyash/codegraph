package identity

import "strings"

// Separator is the delimiter between scope ID and node key in a scoped identity.
const Separator = "::"

// ScopedID constructs a scoped identity string: "{scopeId}::{nodeKey}".
func ScopedID(scopeID, nodeKey string) string {
	return scopeID + Separator + nodeKey
}

// Parse splits a scoped identity into (scopeID, nodeKey, ok).
// Returns ("", id, false) if no separator is found.
func Parse(scopedID string) (scopeID, nodeKey string, ok bool) {
	idx := strings.Index(scopedID, Separator)
	if idx < 0 {
		return "", scopedID, false
	}
	return scopedID[:idx], scopedID[idx+len(Separator):], true
}

// MustParse is like Parse but panics if the ID is not scoped.
func MustParse(scopedID string) (scopeID, nodeKey string) {
	s, n, ok := Parse(scopedID)
	if !ok {
		panic("identity: not a scoped ID: " + scopedID)
	}
	return s, n
}

// IsScoped returns true if the ID contains the scope separator.
func IsScoped(id string) bool {
	return strings.Contains(id, Separator)
}

// NodeKey extracts the nodeKey portion, stripping any scope prefix.
func NodeKey(id string) string {
	_, nk, ok := Parse(id)
	if !ok {
		return id
	}
	return nk
}

// ScopeID extracts the scopeID portion. Returns "" if unscoped.
func ScopeID(id string) string {
	sid, _, ok := Parse(id)
	if !ok {
		return ""
	}
	return sid
}
