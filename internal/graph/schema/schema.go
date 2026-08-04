package schema

import (
	"context"
	"fmt"
	"sort"
	"strings"

	neo4j "github.com/context-maximiser/code-graph/internal/graph"
)

// SchemaManager handles Neo4j schema creation and management
type SchemaManager struct {
	client *neo4j.Client
}

// NewSchemaManager creates a new schema manager
func NewSchemaManager(client *neo4j.Client) *SchemaManager {
	return &SchemaManager{client: client}
}

// Constraint represents a Neo4j constraint
type Constraint struct {
	Name      string
	NodeLabel string
	Property  string
	Type      string // "UNIQUE", "EXISTENCE", "NODE_KEY"
}

// Index represents a Neo4j index
type Index struct {
	Name       string
	NodeLabel  string
	Properties []string
	Type       string // "BTREE", "TEXT", "POINT", "LOOKUP"
}

// FulltextIndex represents a Neo4j FULLTEXT index for full-text search
type FulltextIndex struct {
	Name       string
	NodeLabel  string
	Properties []string
}

// GetConstraints returns all constraint definitions for the code graph schema.
// Note: Old UNIQUE constraints (symbol_unique, service_name_unique, file_path_unique,
// class_fqn_unique, interface_fqn_unique, module_fqn_unique) have been removed
// because the same entity can now exist in multiple scopes (main + PR overlays).
// Uniqueness is now enforced via per-label UNIQUE constraints on scopedKey
// (nodeKey|scopeId), which supports multi-scope identity.
func GetConstraints() []Constraint {
	// Extract the set of labels that have nodeKey indexes (the authoritative label list).
	// These labels support scoped identity via scopedKey.
	labelSet := make(map[string]bool)
	for _, idx := range GetIndexes() {
		// Only single-property nodeKey indexes (not composite)
		if len(idx.Properties) == 1 && idx.Properties[0] == "nodeKey" {
			labelSet[idx.NodeLabel] = true
		}
	}

	// Sort labels for deterministic order
	labels := make([]string, 0, len(labelSet))
	for label := range labelSet {
		labels = append(labels, label)
	}
	sort.Strings(labels)

	// Build UNIQUE constraints for scopedKey on each such label
	constraints := []Constraint{}
	for _, label := range labels {
		constraints = append(constraints, Constraint{
			Name:      label + "_scoped_key_unique",
			NodeLabel: label,
			Property:  "scopedKey",
			Type:      "UNIQUE",
		})
	}

	return constraints
}

// GetDroppedConstraints returns the names of old constraints that should be dropped
// during schema migration. These were single-property UNIQUE constraints that conflict
// with the multi-scope model.
func GetDroppedConstraints() []string {
	return []string{
		"symbol_unique",
		"service_name_unique",
		"file_path_unique",
		"class_fqn_unique",
		"interface_fqn_unique",
		"module_fqn_unique",
	}
}

// GetIndexes returns all index definitions for the code graph schema
func GetIndexes() []Index {
	return []Index{
		// Single property indexes for common lookups
		{
			Name:       "service_name_idx",
			NodeLabel:  "Service",
			Properties: []string{"name"},
			Type:       "BTREE",
		},
		{
			Name:       "file_path_idx",
			NodeLabel:  "File",
			Properties: []string{"path"},
			Type:       "BTREE",
		},
		{
			Name:       "file_hash_idx",
			NodeLabel:  "File",
			Properties: []string{"hash"},
			Type:       "BTREE",
		},
		{
			Name:       "class_name_idx",
			NodeLabel:  "Class",
			Properties: []string{"name"},
			Type:       "BTREE",
		},
		{
			Name:       "class_fqn_idx",
			NodeLabel:  "Class",
			Properties: []string{"fqn"},
			Type:       "BTREE",
		},
		{
			// Interface was the one definition label without a name index —
			// found when RFC-011 codespan resolution timed out scanning it.
			Name:       "interface_name_idx",
			NodeLabel:  "Interface",
			Properties: []string{"name"},
			Type:       "BTREE",
		},
		{
			Name:       "function_name_idx",
			NodeLabel:  "Function",
			Properties: []string{"name"},
			Type:       "BTREE",
		},
		{
			Name:       "function_signature_idx",
			NodeLabel:  "Function",
			Properties: []string{"signature"},
			Type:       "BTREE",
		},
		{
			Name:       "method_name_idx",
			NodeLabel:  "Method",
			Properties: []string{"name"},
			Type:       "BTREE",
		},
		{
			Name:       "variable_name_idx",
			NodeLabel:  "Variable",
			Properties: []string{"name"},
			Type:       "BTREE",
		},
		{
			Name:       "symbol_kind_idx",
			NodeLabel:  "Symbol",
			Properties: []string{"kind"},
			Type:       "BTREE",
		},
		{
			Name:       "api_route_path_idx",
			NodeLabel:  "APIRoute",
			Properties: []string{"path"},
			Type:       "BTREE",
		},
		{
			Name:       "api_route_method_idx",
			NodeLabel:  "APIRoute",
			Properties: []string{"method"},
			Type:       "BTREE",
		},
		{
			Name:       "document_title_idx",
			NodeLabel:  "Document",
			Properties: []string{"title"},
			Type:       "BTREE",
		},
		{
			Name:       "document_type_idx",
			NodeLabel:  "Document",
			Properties: []string{"type"},
			Type:       "BTREE",
		},
		{
			Name:       "feature_name_idx",
			NodeLabel:  "Feature",
			Properties: []string{"name"},
			Type:       "BTREE",
		},
		// Note: Full-text search requires Neo4j Enterprise
		// Using regular BTREE indexes for basic search functionality
		// (search_name_idx removed: duplicate of function_name_idx on Function.name)
		{
			Name:       "search_displayname_idx",
			NodeLabel:  "Symbol",
			Properties: []string{"displayName"},
			Type:       "BTREE",
		},
		// Composite indexes for common query patterns.
		// serviceName is populated by SCIPIndexer on per-service identity nodes
		// (File, Function, Method, Variable, Parameter, Reference) so scoped
		// queries hit indexes directly instead of traversing the Service-CONTAINS
		// chain. Class/Interface/Module/Symbol intentionally omitted — those
		// merge across services on FQN-based nodeKeys, so a single serviceName
		// property would be incoherent.
		{
			Name:       "file_service_path_idx",
			NodeLabel:  "File",
			Properties: []string{"serviceName", "path"},
			Type:       "BTREE",
		},
		{
			Name:       "function_service_idx",
			NodeLabel:  "Function",
			Properties: []string{"serviceName"},
			Type:       "BTREE",
		},
		{
			Name:       "method_service_idx",
			NodeLabel:  "Method",
			Properties: []string{"serviceName"},
			Type:       "BTREE",
		},
		{
			Name:       "variable_service_idx",
			NodeLabel:  "Variable",
			Properties: []string{"serviceName"},
			Type:       "BTREE",
		},
		{
			Name:       "reference_service_idx",
			NodeLabel:  "Reference",
			Properties: []string{"serviceName"},
			Type:       "BTREE",
		},
		// nodeKey indexes for stable identity (Phase 1)
		{
			Name:       "service_nodekey_idx",
			NodeLabel:  "Service",
			Properties: []string{"nodeKey"},
			Type:       "BTREE",
		},
		{
			Name:       "file_nodekey_idx",
			NodeLabel:  "File",
			Properties: []string{"nodeKey"},
			Type:       "BTREE",
		},
		{
			Name:       "symbol_nodekey_idx",
			NodeLabel:  "Symbol",
			Properties: []string{"nodeKey"},
			Type:       "BTREE",
		},
		{
			Name:       "function_nodekey_idx",
			NodeLabel:  "Function",
			Properties: []string{"nodeKey"},
			Type:       "BTREE",
		},
		{
			Name:       "method_nodekey_idx",
			NodeLabel:  "Method",
			Properties: []string{"nodeKey"},
			Type:       "BTREE",
		},
		{
			Name:       "class_nodekey_idx",
			NodeLabel:  "Class",
			Properties: []string{"nodeKey"},
			Type:       "BTREE",
		},
		{
			Name:       "interface_nodekey_idx",
			NodeLabel:  "Interface",
			Properties: []string{"nodeKey"},
			Type:       "BTREE",
		},
		{
			Name:       "module_nodekey_idx",
			NodeLabel:  "Module",
			Properties: []string{"nodeKey"},
			Type:       "BTREE",
		},
		{
			Name:       "variable_nodekey_idx",
			NodeLabel:  "Variable",
			Properties: []string{"nodeKey"},
			Type:       "BTREE",
		},
		{
			Name:       "parameter_nodekey_idx",
			NodeLabel:  "Parameter",
			Properties: []string{"nodeKey"},
			Type:       "BTREE",
		},
		{
			Name:       "apiroute_nodekey_idx",
			NodeLabel:  "APIRoute",
			Properties: []string{"nodeKey"},
			Type:       "BTREE",
		},
		{
			Name:       "document_nodekey_idx",
			NodeLabel:  "Document",
			Properties: []string{"nodeKey"},
			Type:       "BTREE",
		},
		{
			Name:       "feature_nodekey_idx",
			NodeLabel:  "Feature",
			Properties: []string{"nodeKey"},
			Type:       "BTREE",
		},
		{
			Name:       "reference_nodekey_idx",
			NodeLabel:  "Reference",
			Properties: []string{"nodeKey"},
			Type:       "BTREE",
		},
		// Composite (nodeKey, scopeId) indexes for scoped merge lookups (Phase 2)
		{
			Name:       "service_nodekey_scope_idx",
			NodeLabel:  "Service",
			Properties: []string{"nodeKey", "scopeId"},
			Type:       "BTREE",
		},
		{
			Name:       "file_nodekey_scope_idx",
			NodeLabel:  "File",
			Properties: []string{"nodeKey", "scopeId"},
			Type:       "BTREE",
		},
		{
			Name:       "symbol_nodekey_scope_idx",
			NodeLabel:  "Symbol",
			Properties: []string{"nodeKey", "scopeId"},
			Type:       "BTREE",
		},
		{
			Name:       "function_nodekey_scope_idx",
			NodeLabel:  "Function",
			Properties: []string{"nodeKey", "scopeId"},
			Type:       "BTREE",
		},
		{
			Name:       "method_nodekey_scope_idx",
			NodeLabel:  "Method",
			Properties: []string{"nodeKey", "scopeId"},
			Type:       "BTREE",
		},
		{
			Name:       "class_nodekey_scope_idx",
			NodeLabel:  "Class",
			Properties: []string{"nodeKey", "scopeId"},
			Type:       "BTREE",
		},
		{
			Name:       "interface_nodekey_scope_idx",
			NodeLabel:  "Interface",
			Properties: []string{"nodeKey", "scopeId"},
			Type:       "BTREE",
		},
		{
			Name:       "variable_nodekey_scope_idx",
			NodeLabel:  "Variable",
			Properties: []string{"nodeKey", "scopeId"},
			Type:       "BTREE",
		},
		{
			Name:       "reference_nodekey_scope_idx",
			NodeLabel:  "Reference",
			Properties: []string{"nodeKey", "scopeId"},
			Type:       "BTREE",
		},
		{
			Name:       "document_nodekey_scope_idx",
			NodeLabel:  "Document",
			Properties: []string{"nodeKey", "scopeId"},
			Type:       "BTREE",
		},
		{
			Name:       "feature_nodekey_scope_idx",
			NodeLabel:  "Feature",
			Properties: []string{"nodeKey", "scopeId"},
			Type:       "BTREE",
		},
		// Scope filter indexes for querying by scope (Phase 2)
		{
			Name:       "function_scope_idx",
			NodeLabel:  "Function",
			Properties: []string{"scope", "scopeId"},
			Type:       "BTREE",
		},
		{
			Name:       "symbol_scope_idx",
			NodeLabel:  "Symbol",
			Properties: []string{"scope", "scopeId"},
			Type:       "BTREE",
		},
		{
			Name:       "file_scope_idx",
			NodeLabel:  "File",
			Properties: []string{"scope", "scopeId"},
			Type:       "BTREE",
		},
		{
			Name:       "service_scope_idx",
			NodeLabel:  "Service",
			Properties: []string{"scope", "scopeId"},
			Type:       "BTREE",
		},
		// DocumentChunk indexes (Task 4)
		{
			Name:       "docchunk_nodekey_idx",
			NodeLabel:  "DocumentChunk",
			Properties: []string{"nodeKey"},
			Type:       "BTREE",
		},
		{
			Name:       "docchunk_nodekey_scope_idx",
			NodeLabel:  "DocumentChunk",
			Properties: []string{"nodeKey", "scopeId"},
			Type:       "BTREE",
		},
		{
			Name:       "docchunk_dockey_scope_idx",
			NodeLabel:  "DocumentChunk",
			Properties: []string{"documentKey", "scopeId"},
			Type:       "BTREE",
		},
		{
			Name:       "docchunk_texthash_idx",
			NodeLabel:  "DocumentChunk",
			Properties: []string{"textHash"},
			Type:       "BTREE",
		},
		// Docs ingestion indexes (RFC-011)
		{
			Name:       "document_service_idx",
			NodeLabel:  "Document",
			Properties: []string{"serviceName"},
			Type:       "BTREE",
		},
		{
			Name:       "docchunk_service_idx",
			NodeLabel:  "DocumentChunk",
			Properties: []string{"serviceName"},
			Type:       "BTREE",
		},
		// Flow indexes (Task 9)
		{
			Name:       "flow_nodekey_idx",
			NodeLabel:  "Flow",
			Properties: []string{"nodeKey"},
			Type:       "BTREE",
		},
		{
			Name:       "flow_nodekey_scope_idx",
			NodeLabel:  "Flow",
			Properties: []string{"nodeKey", "scopeId"},
			Type:       "BTREE",
		},
		{
			Name:       "flow_entrypoint_idx",
			NodeLabel:  "Flow",
			Properties: []string{"entrypointKey"},
			Type:       "BTREE",
		},
		{
			Name:       "flow_type_idx",
			NodeLabel:  "Flow",
			Properties: []string{"flowType"},
			Type:       "BTREE",
		},
		// PullRequest indexes (Task 10)
		{
			Name:       "pr_nodekey_idx",
			NodeLabel:  "PullRequest",
			Properties: []string{"nodeKey"},
			Type:       "BTREE",
		},
		{
			Name:       "pr_nodekey_scope_idx",
			NodeLabel:  "PullRequest",
			Properties: []string{"nodeKey", "scopeId"},
			Type:       "BTREE",
		},
		{
			Name:       "pr_prid_idx",
			NodeLabel:  "PullRequest",
			Properties: []string{"prId"},
			Type:       "BTREE",
		},
		// GeneratedDoc indexes (Task 10)
		{
			Name:       "gendoc_nodekey_idx",
			NodeLabel:  "GeneratedDoc",
			Properties: []string{"nodeKey"},
			Type:       "BTREE",
		},
		{
			Name:       "gendoc_nodekey_scope_idx",
			NodeLabel:  "GeneratedDoc",
			Properties: []string{"nodeKey", "scopeId"},
			Type:       "BTREE",
		},
		{
			Name:       "gendoc_type_idx",
			NodeLabel:  "GeneratedDoc",
			Properties: []string{"type"},
			Type:       "BTREE",
		},
		{
			Name:       "gendoc_sourcekey_idx",
			NodeLabel:  "GeneratedDoc",
			Properties: []string{"sourceKey"},
			Type:       "BTREE",
		},
		// Tombstone indexes (Phase 3)
		{
			Name:       "tombstone_nodekey_scope_idx",
			NodeLabel:  "Tombstone",
			Properties: []string{"nodeKey", "scopeId"},
			Type:       "BTREE",
		},
		{
			Name:       "tombstone_target_scope_idx",
			NodeLabel:  "Tombstone",
			Properties: []string{"targetNodeKey", "scopeId"},
			Type:       "BTREE",
		},
		// Tenant/repo namespacing indexes (Phase 7)
		{
			Name:       "function_tenant_repo_idx",
			NodeLabel:  "Function",
			Properties: []string{"tenantId", "repoId"},
			Type:       "BTREE",
		},
		{
			Name:       "file_tenant_repo_idx",
			NodeLabel:  "File",
			Properties: []string{"tenantId", "repoId"},
			Type:       "BTREE",
		},
		{
			Name:       "service_tenant_repo_idx",
			NodeLabel:  "Service",
			Properties: []string{"tenantId", "repoId"},
			Type:       "BTREE",
		},
		{
			Name:       "document_tenant_repo_idx",
			NodeLabel:  "Document",
			Properties: []string{"tenantId", "repoId"},
			Type:       "BTREE",
		},
		{
			Name:       "flow_tenant_repo_idx",
			NodeLabel:  "Flow",
			Properties: []string{"tenantId", "repoId"},
			Type:       "BTREE",
		},
		// GDS graph metrics indexes (Phase: Graph-Structural Flow Seeds)
		{
			Name:       "function_indegree_idx",
			NodeLabel:  "Function",
			Properties: []string{"inDegree"},
			Type:       "BTREE",
		},
		{
			Name:       "function_pagerank_idx",
			NodeLabel:  "Function",
			Properties: []string{"pageRank"},
			Type:       "BTREE",
		},
		{
			Name:       "function_betweenness_idx",
			NodeLabel:  "Function",
			Properties: []string{"betweennessCentrality"},
			Type:       "BTREE",
		},
		{
			Name:       "function_community_idx",
			NodeLabel:  "Function",
			Properties: []string{"communityId"},
			Type:       "BTREE",
		},
		{
			Name:       "function_component_idx",
			NodeLabel:  "Function",
			Properties: []string{"componentId"},
			Type:       "BTREE",
		},
		{
			Name:       "function_istest_idx",
			NodeLabel:  "Function",
			Properties: []string{"isTestFunction"},
			Type:       "BTREE",
		},
		{
			Name:       "method_indegree_idx",
			NodeLabel:  "Method",
			Properties: []string{"inDegree"},
			Type:       "BTREE",
		},
		{
			Name:       "method_pagerank_idx",
			NodeLabel:  "Method",
			Properties: []string{"pageRank"},
			Type:       "BTREE",
		},
		{
			Name:       "method_betweenness_idx",
			NodeLabel:  "Method",
			Properties: []string{"betweennessCentrality"},
			Type:       "BTREE",
		},
		{
			Name:       "method_community_idx",
			NodeLabel:  "Method",
			Properties: []string{"communityId"},
			Type:       "BTREE",
		},
		{
			Name:       "method_component_idx",
			NodeLabel:  "Method",
			Properties: []string{"componentId"},
			Type:       "BTREE",
		},
		{
			Name:       "method_istest_idx",
			NodeLabel:  "Method",
			Properties: []string{"isTestFunction"},
			Type:       "BTREE",
		},
	}
}

// GetFulltextIndexes returns all FULLTEXT index definitions for Phase 2 find rewrite.
// FULLTEXT indexes enable efficient full-text search across commonly queried properties.
// Property evidence:
// - Function/Method: name and signature both exist (neo4j tags in model/node.go)
// - Class/Interface: name exists; signature does NOT exist on these types
// - Symbol: name and displayName both exist
// - File: path exists
// - Variable: name exists
func GetFulltextIndexes() []FulltextIndex {
	return []FulltextIndex{
		{
			Name:       "function_fulltext",
			NodeLabel:  "Function",
			Properties: []string{"name", "signature"},
		},
		{
			Name:       "method_fulltext",
			NodeLabel:  "Method",
			Properties: []string{"name", "signature"},
		},
		{
			Name:       "class_fulltext",
			NodeLabel:  "Class",
			Properties: []string{"name"},
		},
		{
			Name:       "interface_fulltext",
			NodeLabel:  "Interface",
			Properties: []string{"name"},
		},
		{
			Name:       "symbol_fulltext",
			NodeLabel:  "Symbol",
			Properties: []string{"name", "displayName"},
		},
		{
			Name:       "file_fulltext",
			NodeLabel:  "File",
			Properties: []string{"path"},
		},
		{
			Name:       "variable_fulltext",
			NodeLabel:  "Variable",
			Properties: []string{"name"},
		},
		// RFC-011: docs become findable via the same index-derived allowlists.
		{
			Name:       "document_fulltext",
			NodeLabel:  "Document",
			Properties: []string{"title", "sourceUrl"},
		},
		{
			Name:       "documentchunk_fulltext",
			NodeLabel:  "DocumentChunk",
			Properties: []string{"content", "headingPath"},
		},
	}
}

// VectorIndex represents a Neo4j native vector index (RFC-011 Layer S).
type VectorIndex struct {
	Name      string
	NodeLabel string
	Property  string
}

// GetVectorIndexes returns the vector index definitions for semantic linking:
// one per label (Neo4j vector indexes are single-label). DocumentChunk holds
// chunk-text embeddings; the code labels hold code-summary embeddings.
func GetVectorIndexes() []VectorIndex {
	return []VectorIndex{
		{Name: "chunk_embedding", NodeLabel: "DocumentChunk", Property: "embedding"},
		{Name: "function_summary_embedding", NodeLabel: "Function", Property: "embedding"},
		{Name: "method_summary_embedding", NodeLabel: "Method", Property: "embedding"},
		{Name: "class_summary_embedding", NodeLabel: "Class", Property: "embedding"},
		{Name: "interface_summary_embedding", NodeLabel: "Interface", Property: "embedding"},
		{Name: "file_summary_embedding", NodeLabel: "File", Property: "embedding"},
	}
}

// vectorIndexDDL renders the Neo4j 5 CREATE VECTOR INDEX statement for idx.
// Dimensions are provider-dependent and therefore a parameter: embeddings from
// different models are not comparable, so switching models means
// DropVectorIndexes + CreateVectorIndexes with the new dimension.
func vectorIndexDDL(idx VectorIndex, dimensions int) string {
	return fmt.Sprintf(
		"CREATE VECTOR INDEX %s IF NOT EXISTS FOR (n:%s) ON n.%s "+
			"OPTIONS {indexConfig: {`vector.dimensions`: %d, `vector.similarity_function`: 'cosine'}}",
		neo4j.Ident(idx.Name), neo4j.Ident(idx.NodeLabel), neo4j.Ident(idx.Property), dimensions,
	)
}

// CreateVectorIndexes creates all vector indexes with the given dimensions.
// Not part of CreateSchema: vector indexes exist only when an embedding
// provider is configured, and their dimension comes from that provider.
func (sm *SchemaManager) CreateVectorIndexes(ctx context.Context, dimensions int) error {
	if dimensions <= 0 {
		return fmt.Errorf("vector index dimensions must be positive, got %d", dimensions)
	}
	for _, idx := range GetVectorIndexes() {
		if _, err := sm.client.ExecuteQuery(ctx, vectorIndexDDL(idx, dimensions), nil); err != nil {
			return fmt.Errorf("failed to create vector index %s: %w", idx.Name, err)
		}
	}
	return nil
}

// DropVectorIndexes drops all vector indexes (used when the embedding model,
// and therefore the vector dimension, changes).
func (sm *SchemaManager) DropVectorIndexes(ctx context.Context) error {
	for _, idx := range GetVectorIndexes() {
		cypher := fmt.Sprintf("DROP INDEX %s IF EXISTS", neo4j.Ident(idx.Name))
		if _, err := sm.client.ExecuteQuery(ctx, cypher, nil); err != nil {
			return fmt.Errorf("failed to drop vector index %s: %w", idx.Name, err)
		}
	}
	return nil
}

// CreateSchema creates all constraints and indexes for the code graph
func (sm *SchemaManager) CreateSchema(ctx context.Context) error {
	// Drop legacy constraints that conflict with scope model
	for _, name := range GetDroppedConstraints() {
		cypher := fmt.Sprintf("DROP CONSTRAINT %s IF EXISTS", name)
		if _, err := sm.client.ExecuteQuery(ctx, cypher, nil); err != nil {
			// Non-fatal: constraint may not exist
			fmt.Printf("Note: could not drop legacy constraint %s: %v\n", name, err)
		}
	}

	// Heal the built-in LOOKUP indexes first: earlier DropSchema versions
	// swept them away with everything else, leaving every labeled MATCH on an
	// AllNodesScan. IF NOT EXISTS makes this a no-op on healthy databases.
	if err := sm.ensureLookupIndexes(ctx); err != nil {
		return fmt.Errorf("failed to ensure lookup indexes: %w", err)
	}

	// Create constraints
	if err := sm.createConstraints(ctx); err != nil {
		return fmt.Errorf("failed to create constraints: %w", err)
	}

	// Create indexes
	if err := sm.createIndexes(ctx); err != nil {
		return fmt.Errorf("failed to create indexes: %w", err)
	}

	// Create fulltext indexes
	if err := sm.createFulltextIndexes(ctx); err != nil {
		return fmt.Errorf("failed to create fulltext indexes: %w", err)
	}

	return nil
}

// MigrationResult holds the result of a schema migration for reporting
type MigrationResult struct {
	Label           string
	BackfilledCount int
	DuplicatesFound int
	DuplicateKeys   []string // Keys with duplicates (first 20, then "+N more" if more than 20)
	ConstraintOK    bool
	Error           string
}

// Migrate performs a safe migration to scopedKey identity constraints.
// It backfills scopedKey for existing nodes, detects duplicates, and creates constraints.
// Returns non-zero exit if duplicates are found (user must manually resolve).
func (sm *SchemaManager) Migrate(ctx context.Context) ([]MigrationResult, error) {
	// Get the set of labels that need scopedKey constraints
	labelSet := make(map[string]bool)
	for _, idx := range GetIndexes() {
		if len(idx.Properties) == 1 && idx.Properties[0] == "nodeKey" {
			labelSet[idx.NodeLabel] = true
		}
	}

	// Sort labels for deterministic output
	labels := make([]string, 0, len(labelSet))
	for label := range labelSet {
		labels = append(labels, label)
	}
	sort.Strings(labels)

	results := []MigrationResult{}
	hasDuplicates := false

	// For each label with nodeKey index: backfill, check for duplicates, then create constraint
	for _, label := range labels {
		result := MigrationResult{Label: label}

		// Step 1: BACKFILL scopedKey for nodes that have nodeKey but no scopedKey
		// Drive CALL from a MATCH stream for proper batching
		backfillCypher := fmt.Sprintf(`
			MATCH (n:%s)
			WHERE n.scopedKey IS NULL AND n.nodeKey IS NOT NULL
			CALL { WITH n SET n.scopedKey = n.nodeKey + '|' + coalesce(n.scopeId, 'main') } IN TRANSACTIONS OF 1000 ROWS
			RETURN count(n) AS total
		`, neo4j.Ident(label))

		backfillRecs, err := sm.client.ExecuteQuery(ctx, backfillCypher, nil)
		if err != nil {
			result.Error = fmt.Sprintf("backfill failed: %v", err)
			results = append(results, result)
			continue
		}
		if len(backfillRecs) > 0 {
			if total, ok := backfillRecs[0].AsMap()["total"].(int64); ok {
				result.BackfilledCount = int(total)
			}
		}

		// Step 2: DUPLICATE DETECTION
		dupCypher := fmt.Sprintf(`
			MATCH (n:%s)
			WHERE n.scopedKey IS NOT NULL
			WITH n.scopedKey AS k, count(*) AS c
			WHERE c > 1
			RETURN k, c
			ORDER BY c DESC
		`, neo4j.Ident(label))

		dupRecs, err := sm.client.ExecuteQuery(ctx, dupCypher, nil)
		if err != nil {
			result.Error = fmt.Sprintf("duplicate detection failed: %v", err)
			results = append(results, result)
			continue
		}

		if len(dupRecs) > 0 {
			// Duplicates found — collect keys and do NOT create constraint
			result.DuplicatesFound = len(dupRecs)
			hasDuplicates = true

			// Collect duplicate keys (cap at 20, then "+N more" if needed)
			dupKeys := []string{}
			for i, rec := range dupRecs {
				if i >= 20 {
					break
				}
				if k, ok := rec.AsMap()["k"].(string); ok {
					dupKeys = append(dupKeys, k)
				}
			}
			result.DuplicateKeys = dupKeys

			results = append(results, result)
			continue
		}

		// Step 3: CREATE constraint (no duplicates)
		constraintName := label + "_scoped_key_unique"
		constraintCypher := fmt.Sprintf(
			"CREATE CONSTRAINT %s IF NOT EXISTS FOR (n:%s) REQUIRE n.scopedKey IS UNIQUE",
			neo4j.Ident(constraintName), neo4j.Ident(label),
		)
		if _, err := sm.client.ExecuteQuery(ctx, constraintCypher, nil); err != nil {
			result.Error = fmt.Sprintf("constraint creation failed: %v", err)
			results = append(results, result)
			continue
		}

		result.ConstraintOK = true
		results = append(results, result)
	}

	// If duplicates were found, return non-zero signal but include results
	if hasDuplicates {
		return results, fmt.Errorf("duplicate scopedKeys found in one or more labels — graph is derived state, delete the affected scope and re-index")
	}

	return results, nil
}

// createConstraints creates all constraint definitions
func (sm *SchemaManager) createConstraints(ctx context.Context) error {
	constraints := GetConstraints()

	for _, constraint := range constraints {
		if err := sm.createConstraint(ctx, constraint); err != nil {
			return fmt.Errorf("failed to create constraint %s: %w", constraint.Name, err)
		}
	}

	return nil
}

// createConstraint creates a single constraint
func (sm *SchemaManager) createConstraint(ctx context.Context, constraint Constraint) error {
	var cypher string

	// Use Ident() to safely interpolate label, property, and constraint names
	label := neo4j.Ident(constraint.NodeLabel)
	property := neo4j.Ident(constraint.Property)
	name := neo4j.Ident(constraint.Name)

	switch constraint.Type {
	case "UNIQUE":
		cypher = fmt.Sprintf(
			"CREATE CONSTRAINT %s IF NOT EXISTS FOR (n:%s) REQUIRE n.%s IS UNIQUE",
			name, label, property,
		)
	case "EXISTENCE":
		cypher = fmt.Sprintf(
			"CREATE CONSTRAINT %s IF NOT EXISTS FOR (n:%s) REQUIRE n.%s IS NOT NULL",
			name, label, property,
		)
	case "NODE_KEY":
		cypher = fmt.Sprintf(
			"CREATE CONSTRAINT %s IF NOT EXISTS FOR (n:%s) REQUIRE (n.%s) IS NODE KEY",
			name, label, property,
		)
	default:
		return fmt.Errorf("unsupported constraint type: %s", constraint.Type)
	}

	_, err := sm.client.ExecuteQuery(ctx, cypher, nil)
	if err != nil {
		return fmt.Errorf("failed to execute constraint creation: %w", err)
	}

	return nil
}

// createIndexes creates all index definitions
func (sm *SchemaManager) createIndexes(ctx context.Context) error {
	indexes := GetIndexes()

	for _, index := range indexes {
		if err := sm.createIndex(ctx, index); err != nil {
			return fmt.Errorf("failed to create index %s: %w", index.Name, err)
		}
	}

	return nil
}

// createIndex creates a single index
func (sm *SchemaManager) createIndex(ctx context.Context, index Index) error {
	var cypher string

	propertiesStr := strings.Join(index.Properties, ", ")

	switch index.Type {
	case "BTREE":
		if index.NodeLabel == "" {
			// Create index on all nodes
			cypher = fmt.Sprintf(
				"CREATE INDEX %s IF NOT EXISTS FOR (n) ON (%s)",
				index.Name, propertiesStr,
			)
		} else {
			cypher = fmt.Sprintf(
				"CREATE INDEX %s IF NOT EXISTS FOR (n:%s) ON (n.%s)",
				index.Name, index.NodeLabel, strings.Join(index.Properties, ", n."),
			)
		}
	case "FULLTEXT":
		if index.NodeLabel == "" {
			// Full-text index on all nodes using APOC
			cypher = fmt.Sprintf(
				"CALL apoc.index.fulltext.nodes.create('%s', ['Service', 'File', 'Class', 'Function', 'Method', 'Variable', 'Symbol', 'Document', 'Feature'], [%s])",
				index.Name, quoteProperties(index.Properties),
			)
		} else {
			cypher = fmt.Sprintf(
				"CALL apoc.index.fulltext.nodes.create('%s', ['%s'], [%s])",
				index.Name, index.NodeLabel, quoteProperties(index.Properties),
			)
		}
	case "TEXT":
		// Legacy TEXT support - try built-in first, fallback to APOC
		if index.NodeLabel == "" {
			// Full-text index on all nodes
			cypher = fmt.Sprintf(
				"CALL db.index.fulltext.createNodeIndex('%s', ['Service', 'File', 'Class', 'Function', 'Method', 'Variable', 'Symbol', 'Document', 'Feature'], [%s])",
				index.Name, quoteProperties(index.Properties),
			)
		} else {
			cypher = fmt.Sprintf(
				"CALL db.index.fulltext.createNodeIndex('%s', ['%s'], [%s])",
				index.Name, index.NodeLabel, quoteProperties(index.Properties),
			)
		}
	case "LOOKUP":
		cypher = fmt.Sprintf(
			"CREATE LOOKUP INDEX %s IF NOT EXISTS FOR (n) ON EACH labels(n)",
			index.Name,
		)
	default:
		return fmt.Errorf("unsupported index type: %s", index.Type)
	}

	_, err := sm.client.ExecuteQuery(ctx, cypher, nil)
	if err != nil {
		return fmt.Errorf("failed to execute index creation: %w", err)
	}

	return nil
}

// createFulltextIndexes creates all FULLTEXT index definitions using Neo4j 5 syntax
func (sm *SchemaManager) createFulltextIndexes(ctx context.Context) error {
	indexes := GetFulltextIndexes()

	for _, index := range indexes {
		// Neo4j 5 syntax: CREATE FULLTEXT INDEX name IF NOT EXISTS FOR (n:Label) ON EACH [n.prop1, n.prop2]
		props := make([]string, len(index.Properties))
		for i, prop := range index.Properties {
			props[i] = "n." + prop
		}
		propertiesStr := strings.Join(props, ", ")

		cypher := fmt.Sprintf(
			"CREATE FULLTEXT INDEX %s IF NOT EXISTS FOR (n:%s) ON EACH [%s]",
			index.Name, index.NodeLabel, propertiesStr,
		)

		if _, err := sm.client.ExecuteQuery(ctx, cypher, nil); err != nil {
			return fmt.Errorf("failed to create fulltext index %s: %w", index.Name, err)
		}
	}

	return nil
}

// DropSchema drops all constraints and indexes
func (sm *SchemaManager) DropSchema(ctx context.Context) error {
	// Drop constraints first (constraint-backed indexes can't be dropped separately)
	if err := sm.dropAllConstraints(ctx); err != nil {
		return fmt.Errorf("failed to drop constraints: %w", err)
	}

	// Drop remaining indexes. SHOW INDEXES lists FULLTEXT indexes too, so
	// this sweep covers them — no separate fulltext drop pass is needed.
	if err := sm.dropAllIndexes(ctx); err != nil {
		return fmt.Errorf("failed to drop indexes: %w", err)
	}

	return nil
}

// ensureLookupIndexes recreates Neo4j's built-in token lookup indexes (node
// label + relationship type) if they are missing. Without them the planner
// cannot do NodeByLabelScan and every labeled MATCH degrades to AllNodesScan.
func (sm *SchemaManager) ensureLookupIndexes(ctx context.Context) error {
	stmts := []string{
		"CREATE LOOKUP INDEX node_label_lookup_index IF NOT EXISTS FOR (n) ON EACH labels(n)",
		"CREATE LOOKUP INDEX rel_type_lookup_index IF NOT EXISTS FOR ()-[r]-() ON EACH type(r)",
	}
	for _, cypher := range stmts {
		if _, err := sm.client.ExecuteQuery(ctx, cypher, nil); err != nil {
			return fmt.Errorf("lookup index creation failed (%s): %w", cypher, err)
		}
	}
	return nil
}

// dropAllConstraints drops all constraints in the database
func (sm *SchemaManager) dropAllConstraints(ctx context.Context) error {
	// Get all constraint names
	cypher := "SHOW CONSTRAINTS YIELD name"
	result, err := sm.client.ExecuteQuery(ctx, cypher, nil)
	if err != nil {
		return fmt.Errorf("failed to list constraints: %w", err)
	}

	// Drop each constraint
	for _, record := range result {
		constraintName, ok := record.AsMap()["name"].(string)
		if !ok {
			continue
		}

		dropCypher := fmt.Sprintf("DROP CONSTRAINT %s IF EXISTS", constraintName)
		_, err := sm.client.ExecuteQuery(ctx, dropCypher, nil)
		if err != nil {
			return fmt.Errorf("failed to drop constraint %s: %w", constraintName, err)
		}
	}

	return nil
}

// dropAllIndexes drops all indexes in the database — except the built-in
// LOOKUP indexes (node label / relationship type). Dropping those makes the
// planner fall back to AllNodesScan for every labeled MATCH database-wide;
// they are Neo4j infrastructure, not part of our schema.
func (sm *SchemaManager) dropAllIndexes(ctx context.Context) error {
	// Get all index names
	cypher := "SHOW INDEXES YIELD name, type WHERE type <> 'LOOKUP' RETURN name"
	result, err := sm.client.ExecuteQuery(ctx, cypher, nil)
	if err != nil {
		return fmt.Errorf("failed to list indexes: %w", err)
	}

	// Drop each index
	for _, record := range result {
		indexName, ok := record.AsMap()["name"].(string)
		if !ok {
			continue
		}

		dropCypher := fmt.Sprintf("DROP INDEX %s IF EXISTS", indexName)
		_, err := sm.client.ExecuteQuery(ctx, dropCypher, nil)
		if err != nil {
			return fmt.Errorf("failed to drop index %s: %w", indexName, err)
		}
	}

	return nil
}

// GetSchemaInfo returns information about current schema
func (sm *SchemaManager) GetSchemaInfo(ctx context.Context) (map[string]any, error) {
	info := make(map[string]any)

	// Get constraints
	constraintsCypher := "SHOW CONSTRAINTS"
	constraintsResult, err := sm.client.ExecuteQuery(ctx, constraintsCypher, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get constraints info: %w", err)
	}

	var constraints []map[string]any
	for _, record := range constraintsResult {
		constraints = append(constraints, record.AsMap())
	}
	info["constraints"] = constraints

	// Get indexes
	indexesCypher := "SHOW INDEXES"
	indexesResult, err := sm.client.ExecuteQuery(ctx, indexesCypher, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get indexes info: %w", err)
	}

	var indexes []map[string]any
	for _, record := range indexesResult {
		indexes = append(indexes, record.AsMap())
	}
	info["indexes"] = indexes

	return info, nil
}

// ValidateSchema checks if the required schema elements exist
func (sm *SchemaManager) ValidateSchema(ctx context.Context) error {
	requiredIndexes := GetIndexes()

	// Check indexes
	indexesCypher := "SHOW INDEXES YIELD name"
	indexesResult, err := sm.client.ExecuteQuery(ctx, indexesCypher, nil)
	if err != nil {
		return fmt.Errorf("failed to check indexes: %w", err)
	}

	existingIndexes := make(map[string]bool)
	for _, record := range indexesResult {
		if name, ok := record.AsMap()["name"].(string); ok {
			existingIndexes[name] = true
		}
	}

	for _, index := range requiredIndexes {
		if !existingIndexes[index.Name] {
			return fmt.Errorf("missing index: %s", index.Name)
		}
	}

	return nil
}

// quoteProperties wraps property names in quotes for full-text index creation
func quoteProperties(properties []string) string {
	quoted := make([]string, len(properties))
	for i, prop := range properties {
		quoted[i] = fmt.Sprintf("'%s'", prop)
	}
	return strings.Join(quoted, ", ")
}
