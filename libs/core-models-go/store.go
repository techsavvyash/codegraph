package models

import "context"

// Node is a generic graph node used by the GraphStore interface.
// It maps directly to BaseNode and is used as the unit of storage for all
// node types in the indexing and retrieval pipelines.
type Node = BaseNode

// Relationship is a generic graph relationship used by the GraphStore interface.
// It maps directly to BaseRelationship and is used as the unit of storage for
// all relationship types in the indexing and retrieval pipelines.
type Relationship = BaseRelationship

// GraphStore abstracts all graph database operations used by the indexing
// and retrieval pipelines. Implementations: Neo4j (production), Mock (tests).
type GraphStore interface {
	// --- Write operations ---

	// UpsertNode creates or updates a node identified by its nodeKey.
	// The node's scope and scopeId determine which overlay it belongs to.
	UpsertNode(ctx context.Context, node *Node) error

	// UpsertNodes batch-upserts a slice of nodes.
	UpsertNodes(ctx context.Context, nodes []*Node) error

	// UpsertRelationship creates or updates a directed relationship.
	UpsertRelationship(ctx context.Context, rel *Relationship) error

	// UpsertRelationships batch-upserts a slice of relationships.
	UpsertRelationships(ctx context.Context, rels []*Relationship) error

	// ApplyTombstone marks a node as deleted in a PR overlay scope.
	// Hides the main-scope node from that PR's view.
	ApplyTombstone(ctx context.Context, tombstone *Tombstone) error

	// --- Read operations ---

	// GetNode retrieves a single node by nodeKey and scope context.
	// Returns nil if not found.
	GetNode(ctx context.Context, nodeKey string, scope ScopeContext) (*Node, error)

	// FindNodes retrieves nodes matching the given filter.
	FindNodes(ctx context.Context, filter NodeFilter) ([]*Node, error)

	// FindRelationships retrieves relationships matching the given filter.
	FindRelationships(ctx context.Context, filter RelFilter) ([]*Relationship, error)

	// GetWithOverlay resolves a node applying overlay precedence:
	// overlay scope wins > tombstone hides > main scope fallback.
	GetWithOverlay(ctx context.Context, nodeKey string, scope ScopeContext) (*Node, error)

	// --- Lifecycle ---

	// Close releases any underlying connections.
	Close(ctx context.Context) error
}

// NodeFilter selects nodes by various criteria.
type NodeFilter struct {
	TenantID  string
	Repo      string
	Service   string
	NodeTypes []NodeType
	Scope     string
	ScopeID   string
	Limit     int
}

// RelFilter selects relationships by various criteria.
type RelFilter struct {
	TenantID    string
	FromNodeKey string
	ToNodeKey   string
	RelTypes    []RelationshipType
	Limit       int
}
