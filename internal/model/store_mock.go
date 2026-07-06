package models

import (
	"context"
	"sync"
)

// MockGraphStore is an in-memory GraphStore implementation for testing.
// It does NOT require a live Neo4j instance.
//
// Error injection: set Errors["MethodName"] = someErr to make that method
// return the given error on the next call.
type MockGraphStore struct {
	mu            sync.RWMutex
	nodes         map[string]*Node       // nodeKey → Node (all scopes)
	relationships []*Relationship
	tombstones    map[string]*Tombstone  // tombstone nodeKey → Tombstone
	Errors        map[string]error       // method name → error to return
}

// NewMockGraphStore returns an initialised MockGraphStore.
func NewMockGraphStore() *MockGraphStore {
	return &MockGraphStore{
		nodes:      make(map[string]*Node),
		tombstones: make(map[string]*Tombstone),
		Errors:     make(map[string]error),
	}
}

// storageKey returns a composite key used to store a node by (nodeKey, scopeId).
func storageKey(nodeKey, scopeID string) string {
	return nodeKey + "\x00" + scopeID
}

// UpsertNode stores or updates a node keyed by (NodeKey, ScopeID).
func (m *MockGraphStore) UpsertNode(ctx context.Context, node *Node) error {
	if err := m.Errors["UpsertNode"]; err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := storageKey(node.NodeKey, node.ScopeID)
	m.nodes[key] = node
	return nil
}

// UpsertNodes calls UpsertNode for each node in the slice.
func (m *MockGraphStore) UpsertNodes(ctx context.Context, nodes []*Node) error {
	if err := m.Errors["UpsertNodes"]; err != nil {
		return err
	}
	for _, n := range nodes {
		if err := m.UpsertNode(ctx, n); err != nil {
			return err
		}
	}
	return nil
}

// UpsertRelationship appends a relationship to the in-memory store.
// Duplicate detection is deliberately omitted for simplicity.
func (m *MockGraphStore) UpsertRelationship(ctx context.Context, rel *Relationship) error {
	if err := m.Errors["UpsertRelationship"]; err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	// Replace existing relationship with the same (StartID, EndID, Type) if present.
	for i, existing := range m.relationships {
		if existing.StartID == rel.StartID &&
			existing.EndID == rel.EndID &&
			existing.Type == rel.Type {
			m.relationships[i] = rel
			return nil
		}
	}
	m.relationships = append(m.relationships, rel)
	return nil
}

// UpsertRelationships calls UpsertRelationship for each relationship.
func (m *MockGraphStore) UpsertRelationships(ctx context.Context, rels []*Relationship) error {
	if err := m.Errors["UpsertRelationships"]; err != nil {
		return err
	}
	for _, rel := range rels {
		if err := m.UpsertRelationship(ctx, rel); err != nil {
			return err
		}
	}
	return nil
}

// ApplyTombstone stores a tombstone keyed by its NodeKey.
func (m *MockGraphStore) ApplyTombstone(ctx context.Context, tombstone *Tombstone) error {
	if err := m.Errors["ApplyTombstone"]; err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tombstones[tombstone.NodeKey] = tombstone
	return nil
}

// GetNode retrieves a node by nodeKey and scope. Returns nil if not found.
func (m *MockGraphStore) GetNode(ctx context.Context, nodeKey string, scope ScopeContext) (*Node, error) {
	if err := m.Errors["GetNode"]; err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	key := storageKey(nodeKey, scope.ScopeID)
	node, ok := m.nodes[key]
	if !ok {
		return nil, nil
	}
	return node, nil
}

// FindNodes retrieves nodes that match all non-zero fields in the filter.
func (m *MockGraphStore) FindNodes(ctx context.Context, filter NodeFilter) ([]*Node, error) {
	if err := m.Errors["FindNodes"]; err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()

	var results []*Node
	for _, node := range m.nodes {
		if !nodeMatchesFilter(node, filter) {
			continue
		}
		results = append(results, node)
		if filter.Limit > 0 && len(results) >= filter.Limit {
			break
		}
	}
	return results, nil
}

// FindRelationships retrieves relationships matching the filter.
func (m *MockGraphStore) FindRelationships(ctx context.Context, filter RelFilter) ([]*Relationship, error) {
	if err := m.Errors["FindRelationships"]; err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()

	var results []*Relationship
	for _, rel := range m.relationships {
		if filter.FromNodeKey != "" && rel.StartID != filter.FromNodeKey {
			continue
		}
		if filter.ToNodeKey != "" && rel.EndID != filter.ToNodeKey {
			continue
		}
		if len(filter.RelTypes) > 0 && !containsRelType(filter.RelTypes, rel.Type) {
			continue
		}
		results = append(results, rel)
		if filter.Limit > 0 && len(results) >= filter.Limit {
			break
		}
	}
	return results, nil
}

// GetWithOverlay resolves a node applying overlay precedence:
//  1. If an overlay-scope node exists for nodeKey → return it.
//  2. If a tombstone exists for nodeKey in this scope → return nil (hidden).
//  3. If a main-scope node exists → return it.
//  4. Otherwise → return nil.
func (m *MockGraphStore) GetWithOverlay(ctx context.Context, nodeKey string, scope ScopeContext) (*Node, error) {
	if err := m.Errors["GetWithOverlay"]; err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Step 1: overlay node.
	if scope.ScopeID != "" && scope.ScopeID != ScopeMain {
		overlayKey := storageKey(nodeKey, scope.ScopeID)
		if node, ok := m.nodes[overlayKey]; ok {
			return node, nil
		}
	}

	// Step 2: tombstone check.
	tombstoneKey := TombstoneNodeKey(scope.ScopeID, nodeKey)
	if _, found := m.tombstones[tombstoneKey]; found {
		return nil, nil
	}

	// Step 3: main-scope fallback.
	mainKey := storageKey(nodeKey, ScopeMain)
	if node, ok := m.nodes[mainKey]; ok {
		return node, nil
	}

	return nil, nil
}

// Close is a no-op for the mock store.
func (m *MockGraphStore) Close(ctx context.Context) error {
	return nil
}

// --- Inspection helpers (not part of the interface, useful in tests) ---

// AllNodes returns a copy of all stored nodes.
func (m *MockGraphStore) AllNodes() []*Node {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Node, 0, len(m.nodes))
	for _, n := range m.nodes {
		out = append(out, n)
	}
	return out
}

// AllRelationships returns a copy of all stored relationships.
func (m *MockGraphStore) AllRelationships() []*Relationship {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Relationship, len(m.relationships))
	copy(out, m.relationships)
	return out
}

// --- private helpers ---

func nodeMatchesFilter(node *Node, filter NodeFilter) bool {
	if filter.ScopeID != "" && node.ScopeID != filter.ScopeID {
		return false
	}
	if filter.Scope != "" && node.Scope != filter.Scope {
		return false
	}
	if filter.TenantID != "" && node.TenantID != filter.TenantID {
		return false
	}
	if filter.Repo != "" && node.Repo != filter.Repo {
		return false
	}
	if len(filter.NodeTypes) > 0 && !nodeHasAnyLabel(node, filter.NodeTypes) {
		return false
	}
	return true
}

func nodeHasAnyLabel(node *Node, types []NodeType) bool {
	for _, nt := range types {
		label := string(nt)
		for _, l := range node.Labels {
			if l == label {
				return true
			}
		}
	}
	return false
}

func containsRelType(types []RelationshipType, t RelationshipType) bool {
	for _, rt := range types {
		if rt == t {
			return true
		}
	}
	return false
}
