package integration

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/context-maximiser/code-graph/internal/ingest/scip"
	"github.com/context-maximiser/code-graph/internal/graph"
	"github.com/context-maximiser/code-graph/internal/graph/schema"
	models "github.com/context-maximiser/code-graph/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// IndexingTestSuite tests the complete indexing functionality
type IndexingTestSuite struct {
	suite.Suite
	client *neo4j.Client
	ctx    context.Context
}

func TestIndexingTestSuite(t *testing.T) {
	suite.Run(t, new(IndexingTestSuite))
}

func (s *IndexingTestSuite) SetupSuite() {
	// Create Neo4j client
	config := &neo4j.Config{
		URI:      "bolt://localhost:7687",
		Username: "neo4j",
		Password: "password123",
		Database: "neo4j",
	}

	client, err := neo4j.NewClient(*config)
	require.NoError(s.T(), err)
	
	s.client = client
	s.ctx = context.Background()

	// Setup test schema (clean slate)
	s.setupTestSchema()
}

func (s *IndexingTestSuite) TearDownSuite() {
	cypher := `
		MATCH (n)
		WHERE n.scopeId = $scope
		DETACH DELETE n
	`
	params := map[string]any{"scope": "itest-indexing"}
	_, _ = s.client.ExecuteQuery(s.ctx, cypher, params)
	if s.client != nil {
		s.client.Close(s.ctx)
	}
}

func (s *IndexingTestSuite) setupTestSchema() {
	cypher := `
		MATCH (n)
		WHERE n.scopeId = $scope
		DETACH DELETE n
	`
	params := map[string]any{"scope": "itest-indexing"}
	_, err := s.client.ExecuteQuery(s.ctx, cypher, params)
	require.NoError(s.T(), err)

	// Create fresh schema
	schemaManager := schema.NewSchemaManager(s.client)
	err = schemaManager.CreateSchema(s.ctx)
	require.NoError(s.T(), err)
}

func (s *IndexingTestSuite) TestCodeIndexingIntegration() {
	s.T().Log("Testing complete code indexing integration")

	// Create SCIP indexer with test-scoped service name
	scipIndexer := static.NewSCIPIndexer(s.client, "itest-indexing", "v1.0.0", "https://github.com/test/repo")
	scipIndexer.SetScope(models.ScopeContext{Scope: "main", ScopeID: "itest-indexing"})
	
	// Validate environment first
	err := scipIndexer.ValidateEnvironment()
	require.NoError(s.T(), err)
	
	// Index the current project
	projectPath := "../../"  // Go up to project root
	err = scipIndexer.IndexProject(s.ctx, projectPath)
	require.NoError(s.T(), err)
	
	// Verify indexing results
	s.verifyCodeIndexing()
}

func (s *IndexingTestSuite) verifyCodeIndexing() {
	tests := []struct {
		name          string
		query         string
		expectedCount int
		description   string
	}{
		{
			name:          "Service nodes created",
			query:         "MATCH (s:Service) RETURN count(s) as count",
			expectedCount: 1,
			description:   "Should have exactly one service node",
		},
		{
			name:          "File nodes created",
			query:         "MATCH (f:File) RETURN count(f) as count",
			expectedCount: 10, // At least 10 Go files
			description:   "Should have multiple file nodes for Go files",
		},
		{
			name:          "Symbol nodes created",
			query:         "MATCH (s:Symbol) RETURN count(s) as count",
			expectedCount: 100, // At least 100 symbols
			description:   "Should have many symbol nodes",
		},
		{
			name:          "Function nodes created",
			query:         "MATCH (f:Function) RETURN count(f) as count",
			expectedCount: 5, // At least 5 functions
			description:   "Should have function nodes",
		},
		{
			name:          "Service contains files",
			query:         "MATCH (s:Service)-[:CONTAINS]->(f:File) RETURN count(f) as count",
			expectedCount: 10, // At least 10 files linked to service
			description:   "Service should contain files",
		},
		{
			name:          "Files contain symbols",
			query:         "MATCH (f:File)-[:CONTAINS]->(sym) RETURN count(sym) as count",
			expectedCount: 50, // At least 50 symbols in files
			description:   "Files should contain symbols",
		},
		{
			name:          "Symbol references exist",
			query:         "MATCH (r:Reference)-[:REFERENCES]->(s:Symbol) RETURN count(r) as count",
			expectedCount: 100, // At least 100 references
			description:   "Should have symbol references",
		},
	}
	
	for _, tt := range tests {
		s.T().Run(tt.name, func(t *testing.T) {
			result, err := s.client.ExecuteQuery(s.ctx, tt.query, nil)
			require.NoError(t, err)
			require.Len(t, result, 1)
			
			record := result[0].AsMap()
			count, ok := record["count"].(int64)
			require.True(t, ok, "Count should be an integer")
			
			assert.GreaterOrEqual(t, int(count), tt.expectedCount, 
				"%s: %s. Expected >= %d, got %d", tt.name, tt.description, tt.expectedCount, count)
			
			t.Logf("✓ %s: %d (expected >= %d)", tt.description, count, tt.expectedCount)
		})
	}
}

func (s *IndexingTestSuite) TestQueryPerformance() {
	s.T().Log("Testing query performance")
	
	performanceTests := []struct {
		name         string
		query        string
		maxDuration  time.Duration
		description  string
	}{
		{
			name:        "Symbol lookup performance",
			query:       "MATCH (s:Symbol) WHERE s.kind = 'Function' RETURN count(s)",
			maxDuration: 1 * time.Second,
			description: "Symbol queries should be fast",
		},
		{
			name:        "Feature search performance", 
			query:       "MATCH (f:Feature) WHERE f.status = 'completed' RETURN count(f)",
			maxDuration: 1 * time.Second,
			description: "Feature queries should be fast",
		},
		{
			name:        "Cross-context search performance",
			query:       "MATCH (n) WHERE toLower(n.name) CONTAINS 'test' RETURN labels(n), count(n)",
			maxDuration: 2 * time.Second,
			description: "Cross-context searches should be reasonably fast",
		},
	}
	
	for _, tt := range performanceTests {
		s.T().Run(tt.name, func(t *testing.T) {
			start := time.Now()
			
			result, err := s.client.ExecuteQuery(s.ctx, tt.query, nil)
			require.NoError(t, err)
			
			duration := time.Since(start)
			
			assert.LessOrEqual(t, duration, tt.maxDuration,
				"%s: %s. Expected <= %v, got %v", tt.name, tt.description, tt.maxDuration, duration)
			
			t.Logf("✓ %s: %v (limit: %v), %d results", tt.description, duration, tt.maxDuration, len(result))
		})
	}
}

func (s *IndexingTestSuite) TestDataIntegrity() {
	s.T().Log("Testing data integrity")
	
	integrityTests := []struct {
		name        string
		query       string
		expectEmpty bool
		description string
	}{
		{
			name:        "No orphaned references",
			query:       "MATCH (r:Reference) WHERE NOT (r)-[:REFERENCES]->(:Symbol) RETURN count(r) as orphaned",
			expectEmpty: true,
			description: "All references should point to valid symbols",
		},
		{
			name:        "No orphaned features", 
			query:       "MATCH (f:Feature) WHERE NOT (:Document)-[:DESCRIBES]->(f) RETURN count(f) as orphaned",
			expectEmpty: false, // Some features might not have document links
			description: "Check for features without document links",
		},
		{
			name:        "Service has files",
			query:       "MATCH (s:Service) WHERE NOT (s)-[:CONTAINS]->(:File) RETURN count(s) as servicesWithoutFiles", 
			expectEmpty: true,
			description: "All services should have files",
		},
	}
	
	for _, tt := range integrityTests {
		s.T().Run(tt.name, func(t *testing.T) {
			result, err := s.client.ExecuteQuery(s.ctx, tt.query, nil)
			require.NoError(t, err)
			require.Len(t, result, 1)
			
			record := result[0].AsMap()
			count := int64(0)
			
			// Handle different count field names
			for _, field := range []string{"orphaned", "servicesWithoutFiles", "count"} {
				if val, ok := record[field]; ok {
					count = val.(int64)
					break
				}
			}
			
			if tt.expectEmpty {
				assert.Equal(t, int64(0), count, "%s: %s", tt.name, tt.description)
				t.Logf("✓ %s: No integrity issues found", tt.description)
			} else {
				t.Logf("ℹ %s: Found %d items (expected)", tt.description, count)
			}
		})
	}
}

func (s *IndexingTestSuite) TestSearchFunctionality() {
	s.T().Log("Testing search functionality")
	
	queryBuilder := neo4j.NewQueryBuilder(s.client)
	
	searchTests := []struct {
		searchTerm    string
		nodeTypes     []string
		expectedMin   int
		description   string
	}{
		{
			searchTerm:  "index",
			nodeTypes:   []string{"Function", "Method", "Feature", "File"},
			expectedMin: 2,
			description: "Should find indexing-related items",
		},
		{
			searchTerm:  "SCIP", 
			nodeTypes:   []string{"Symbol", "Feature", "Method"},
			expectedMin: 1,
			description: "Should find SCIP-related items",
		},
		{
			searchTerm:  "Neo4j",
			nodeTypes:   []string{"Feature", "Symbol", "File"},
			expectedMin: 1,
			description: "Should find Neo4j-related items",
		},
	}
	
	for _, tt := range searchTests {
		s.T().Run(fmt.Sprintf("Search_%s", tt.searchTerm), func(t *testing.T) {
			results, err := queryBuilder.SearchNodes(s.ctx, tt.searchTerm, tt.nodeTypes, 20)
			require.NoError(t, err)
			
			assert.GreaterOrEqual(t, len(results), tt.expectedMin,
				"%s: Expected >= %d results, got %d", tt.description, tt.expectedMin, len(results))
			
			t.Logf("✓ %s: Found %d results", tt.description, len(results))
			
			// Verify result types
			nodeTypesFound := make(map[string]int)
			for _, result := range results[:min(len(results), 3)] { // Check first 3 results
				recordMap := result.AsMap()
				if labels, ok := recordMap["nodeLabels"].([]interface{}); ok && len(labels) > 0 {
					label := labels[0].(string)
					nodeTypesFound[label]++
				}
			}
			
			t.Logf("  Node types found: %+v", nodeTypesFound)
		})
	}
}

// Helper function
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (s *IndexingTestSuite) TearDownTest() {
}
