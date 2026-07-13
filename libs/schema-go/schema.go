package schema

import (
	"context"
	"fmt"
	"strings"

	"github.com/context-maximiser/code-graph/libs/neo4j-go"
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

// GetConstraints returns all constraint definitions for the code graph schema.
// Note: Old UNIQUE constraints (symbol_unique, service_name_unique, file_path_unique,
// class_fqn_unique, interface_fqn_unique, module_fqn_unique) have been removed
// because the same entity can now exist in multiple scopes (main + PR overlays).
// Uniqueness is enforced via composite (nodeKey, scopeId) indexes instead.
func GetConstraints() []Constraint {
	return []Constraint{
		{
			Name:      "grpccall_nodekey_unique",
			NodeLabel: "GRPCCall",
			Property:  "nodeKey",
			Type:      "UNIQUE",
		},
		{
			Name:      "httpcall_nodekey_unique",
			NodeLabel: "HTTPCall",
			Property:  "nodeKey",
			Type:      "UNIQUE",
		},
		{
			Name:      "outboxcall_nodekey_unique",
			NodeLabel: "OutboxCall",
			Property:  "nodeKey",
			Type:      "UNIQUE",
		},
		{
			Name:      "eventtype_nodekey_unique",
			NodeLabel: "EventType",
			Property:  "nodeKey",
			Type:      "UNIQUE",
		},
		{
			Name:      "dbcall_nodekey_unique",
			NodeLabel: "DBCall",
			Property:  "nodeKey",
			Type:      "UNIQUE",
		},
		{
			Name:      "externalcall_nodekey_unique",
			NodeLabel: "ExternalCall",
			Property:  "nodeKey",
			Type:      "UNIQUE",
		},
		{
			Name:      "externalservice_nodekey_unique",
			NodeLabel: "ExternalService",
			Property:  "nodeKey",
			Type:      "UNIQUE",
		},
	}
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
		// Note: Full-text search requires Neo4j Enterprise
		// Using regular BTREE indexes for basic search functionality
		// (search_name_idx removed: duplicate of function_name_idx on Function.name)
		{
			Name:       "search_displayname_idx",
			NodeLabel:  "Symbol",
			Properties: []string{"displayName"},
			Type:       "BTREE",
		},
		// Composite indexes for common query patterns
		{
			Name:       "file_service_path_idx",
			NodeLabel:  "File",
			Properties: []string{"serviceName", "path"},
			Type:       "BTREE",
		},
		{
			Name:       "symbol_service_idx",
			NodeLabel:  "Symbol",
			Properties: []string{"serviceName", "kind"},
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
		// ExternalCall / ExternalService indexes (AWS external service indexing)
		{
			Name:       "externalcall_nodekey_scope_idx",
			NodeLabel:  "ExternalCall",
			Properties: []string{"nodeKey", "scopeId"},
			Type:       "BTREE",
		},
		{
			Name:       "externalcall_service_scope_idx",
			NodeLabel:  "ExternalCall",
			Properties: []string{"externalService", "scopeId"},
			Type:       "BTREE",
		},
		{
			Name:       "externalservice_nodekey_scope_idx",
			NodeLabel:  "ExternalService",
			Properties: []string{"nodeKey", "scopeId"},
			Type:       "BTREE",
		},
		{
			Name:       "externalservice_name_scope_idx",
			NodeLabel:  "ExternalService",
			Properties: []string{"name", "scopeId"},
			Type:       "BTREE",
		},
		// GRPCCall / HTTPCall / OutboxCall indexes (Phase 7 — cross-service RPC gap fix)
		{
			Name:       "grpccall_nodekey_idx",
			NodeLabel:  "GRPCCall",
			Properties: []string{"nodeKey"},
			Type:       "BTREE",
		},
		{
			Name:       "grpccall_nodekey_scope_idx",
			NodeLabel:  "GRPCCall",
			Properties: []string{"nodeKey", "scopeId"},
			Type:       "BTREE",
		},
		{
			Name:       "grpccall_caller_scope_idx",
			NodeLabel:  "GRPCCall",
			Properties: []string{"callerService", "scopeId"},
			Type:       "BTREE",
		},
		{
			Name:       "grpccall_target_scope_idx",
			NodeLabel:  "GRPCCall",
			Properties: []string{"targetService", "scopeId"},
			Type:       "BTREE",
		},
		{
			Name:       "httpcall_nodekey_idx",
			NodeLabel:  "HTTPCall",
			Properties: []string{"nodeKey"},
			Type:       "BTREE",
		},
		{
			Name:       "httpcall_nodekey_scope_idx",
			NodeLabel:  "HTTPCall",
			Properties: []string{"nodeKey", "scopeId"},
			Type:       "BTREE",
		},
		{
			Name:       "httpcall_caller_scope_idx",
			NodeLabel:  "HTTPCall",
			Properties: []string{"callerService", "scopeId"},
			Type:       "BTREE",
		},
		{
			Name:       "outboxcall_nodekey_idx",
			NodeLabel:  "OutboxCall",
			Properties: []string{"nodeKey"},
			Type:       "BTREE",
		},
		{
			Name:       "outboxcall_nodekey_scope_idx",
			NodeLabel:  "OutboxCall",
			Properties: []string{"nodeKey", "scopeId"},
			Type:       "BTREE",
		},
		{
			Name:       "outboxcall_event_scope_idx",
			NodeLabel:  "OutboxCall",
			Properties: []string{"eventType", "scopeId"},
			Type:       "BTREE",
		},
		// EventType hub indexes (async event-flow indexing)
		{
			Name:       "eventtype_nodekey_scope_idx",
			NodeLabel:  "EventType",
			Properties: []string{"nodeKey", "scopeId"},
			Type:       "BTREE",
		},
		{
			Name:       "eventtype_event_scope_idx",
			NodeLabel:  "EventType",
			Properties: []string{"eventType", "scopeId"},
			Type:       "BTREE",
		},
		{
			Name:       "eventtype_group_scope_idx",
			NodeLabel:  "EventType",
			Properties: []string{"group", "scopeId"},
			Type:       "BTREE",
		},
		// DBCall indexes (Phase 1 — DBCall Node & Relationship)
		{
			Name:       "dbcall_nodekey_scope_idx",
			NodeLabel:  "DBCall",
			Properties: []string{"nodeKey", "scopeId"},
			Type:       "BTREE",
		},
		{
			Name:       "dbcall_table_service_idx",
			NodeLabel:  "DBCall",
			Properties: []string{"table", "serviceName"},
			Type:       "BTREE",
		},
		{
			Name:       "dbcall_operation_idx",
			NodeLabel:  "DBCall",
			Properties: []string{"operation"},
			Type:       "BTREE",
		},
		// ControlFlowScope indexes (Change #2 — control-flow reification)
		{
			Name:       "cfs_nodekey_idx",
			NodeLabel:  "ControlFlowScope",
			Properties: []string{"nodeKey", "scopeId"},
			Type:       "BTREE",
		},
		{
			Name:       "cfs_filepath_idx",
			NodeLabel:  "ControlFlowScope",
			Properties: []string{"filePath"},
			Type:       "BTREE",
		},
		// ConcurrentScope indexes (Change #3 — concurrent + tx scope reification)
		{
			Name:       "concurrentscope_nodekey_idx",
			NodeLabel:  "ConcurrentScope",
			Properties: []string{"nodeKey", "scopeId"},
			Type:       "BTREE",
		},
		{
			Name:       "concurrentscope_filepath_idx",
			NodeLabel:  "ConcurrentScope",
			Properties: []string{"filePath"},
			Type:       "BTREE",
		},
		// TxScope indexes (Change #3 — concurrent + tx scope reification)
		{
			Name:       "txscope_nodekey_idx",
			NodeLabel:  "TxScope",
			Properties: []string{"nodeKey", "scopeId"},
			Type:       "BTREE",
		},
		{
			Name:       "txscope_filepath_idx",
			NodeLabel:  "TxScope",
			Properties: []string{"filePath"},
			Type:       "BTREE",
		},
		// Cross-service handler resolution indexes (Change #5 — ResolveCrossServiceHandlersStage)
		// GRPCCall.targetMethod is the primary lookup key used to find handler functions.
		{
			Name:       "grpccall_targetmethod_idx",
			NodeLabel:  "GRPCCall",
			Properties: []string{"targetMethod"},
			Type:       "BTREE",
		},
	}
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

	// Create constraints
	if err := sm.createConstraints(ctx); err != nil {
		return fmt.Errorf("failed to create constraints: %w", err)
	}

	// Create indexes
	if err := sm.createIndexes(ctx); err != nil {
		return fmt.Errorf("failed to create indexes: %w", err)
	}

	return nil
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
	
	switch constraint.Type {
	case "UNIQUE":
		cypher = fmt.Sprintf(
			"CREATE CONSTRAINT %s IF NOT EXISTS FOR (n:%s) REQUIRE n.%s IS UNIQUE",
			constraint.Name, constraint.NodeLabel, constraint.Property,
		)
	case "EXISTENCE":
		cypher = fmt.Sprintf(
			"CREATE CONSTRAINT %s IF NOT EXISTS FOR (n:%s) REQUIRE n.%s IS NOT NULL",
			constraint.Name, constraint.NodeLabel, constraint.Property,
		)
	case "NODE_KEY":
		cypher = fmt.Sprintf(
			"CREATE CONSTRAINT %s IF NOT EXISTS FOR (n:%s) REQUIRE (n.%s) IS NODE KEY",
			constraint.Name, constraint.NodeLabel, constraint.Property,
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

// DropSchema drops all constraints and indexes
func (sm *SchemaManager) DropSchema(ctx context.Context) error {
	// Drop constraints first (constraint-backed indexes can't be dropped separately)
	if err := sm.dropAllConstraints(ctx); err != nil {
		return fmt.Errorf("failed to drop constraints: %w", err)
	}

	// Drop remaining indexes
	if err := sm.dropAllIndexes(ctx); err != nil {
		return fmt.Errorf("failed to drop indexes: %w", err)
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

// dropAllIndexes drops all indexes in the database
func (sm *SchemaManager) dropAllIndexes(ctx context.Context) error {
	// Get all index names
	cypher := "SHOW INDEXES YIELD name"
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