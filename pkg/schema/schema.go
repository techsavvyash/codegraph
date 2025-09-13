package schema

import (
	"context"
	"fmt"
	"strings"

	"github.com/context-maximiser/code-graph/pkg/neo4j"
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

// GetConstraints returns all constraint definitions for the code graph schema
func GetConstraints() []Constraint {
	return []Constraint{
		// Unique constraints for key identifiers
		{
			Name:      "symbol_unique",
			NodeLabel: "Symbol",
			Property:  "symbol",
			Type:      "UNIQUE",
		},
		{
			Name:      "service_name_unique",
			NodeLabel: "Service",
			Property:  "name",
			Type:      "UNIQUE",
		},
		{
			Name:      "file_path_unique",
			NodeLabel: "File", 
			Property:  "path",
			Type:      "UNIQUE",
		},
		// Node key constraints for composite uniqueness
		{
			Name:      "class_fqn_unique",
			NodeLabel: "Class",
			Property:  "fqn",
			Type:      "UNIQUE",
		},
		{
			Name:      "interface_fqn_unique",
			NodeLabel: "Interface",
			Property:  "fqn",
			Type:      "UNIQUE",
		},
		{
			Name:      "module_fqn_unique",
			NodeLabel: "Module",
			Property:  "fqn",
			Type:      "UNIQUE",
		},
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
		{
			Name:       "search_name_idx",
			NodeLabel:  "Function",
			Properties: []string{"name"},
			Type:       "BTREE",
		},
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
	}
}

// CreateSchema creates all constraints and indexes for the code graph
func (sm *SchemaManager) CreateSchema(ctx context.Context) error {
	// Create constraints first
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
	// Drop all indexes first
	if err := sm.dropAllIndexes(ctx); err != nil {
		return fmt.Errorf("failed to drop indexes: %w", err)
	}

	// Drop all constraints
	if err := sm.dropAllConstraints(ctx); err != nil {
		return fmt.Errorf("failed to drop constraints: %w", err)
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
	requiredConstraints := GetConstraints()
	requiredIndexes := GetIndexes()

	// Check constraints
	constraintsCypher := "SHOW CONSTRAINTS YIELD name"
	constraintsResult, err := sm.client.ExecuteQuery(ctx, constraintsCypher, nil)
	if err != nil {
		return fmt.Errorf("failed to check constraints: %w", err)
	}

	existingConstraints := make(map[string]bool)
	for _, record := range constraintsResult {
		if name, ok := record.AsMap()["name"].(string); ok {
			existingConstraints[name] = true
		}
	}

	for _, constraint := range requiredConstraints {
		if !existingConstraints[constraint.Name] {
			return fmt.Errorf("missing constraint: %s", constraint.Name)
		}
	}

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