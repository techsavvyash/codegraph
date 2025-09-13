package integration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/context-maximiser/code-graph/pkg/indexer/documents"
	"github.com/context-maximiser/code-graph/pkg/indexer/static"
	"github.com/context-maximiser/code-graph/pkg/neo4j"
	"github.com/context-maximiser/code-graph/pkg/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// IndexingTestSuite tests the complete indexing functionality
type IndexingTestSuite struct {
	suite.Suite
	client    *neo4j.Client
	ctx       context.Context
	testDir   string
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

	// Create test directory
	s.testDir = filepath.Join("test", "fixtures")
	os.MkdirAll(s.testDir, 0755)
	
	// Setup test schema (clean slate)
	s.setupTestSchema()
}

func (s *IndexingTestSuite) TearDownSuite() {
	if s.client != nil {
		s.client.Close(s.ctx)
	}
}

func (s *IndexingTestSuite) setupTestSchema() {
	// Clear existing data
	_, err := s.client.ExecuteQuery(s.ctx, "MATCH (n) DETACH DELETE n", nil)
	require.NoError(s.T(), err)
	
	// Create fresh schema
	schemaManager := schema.NewSchemaManager(s.client)
	err = schemaManager.CreateSchema(s.ctx)
	require.NoError(s.T(), err)
}

func (s *IndexingTestSuite) TestCodeIndexingIntegration() {
	s.T().Log("Testing complete code indexing integration")
	
	// Create SCIP indexer
	scipIndexer := static.NewSCIPIndexer(s.client, "test-service", "v1.0.0", "https://github.com/test/repo")
	
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

func (s *IndexingTestSuite) TestDocumentIndexingIntegration() {
	s.T().Log("Testing complete document indexing integration")
	
	// Create test documents
	s.createTestDocuments()
	
	// Create document indexer
	docIndexer := documents.NewDocumentIndexer(s.client)
	
	// Index test documents
	err := docIndexer.IndexDirectory(s.ctx, s.testDir)
	require.NoError(s.T(), err)
	
	// Verify document indexing
	s.verifyDocumentIndexing()
}

func (s *IndexingTestSuite) createTestDocuments() {
	// Test document 1: Architecture document
	archDoc := `# Test Architecture Document

## Introduction
This document describes the test architecture for our system.

## Features
Feature: User Authentication
- Implementation: OAuth 2.0
- Status: Completed

Feature: Data Processing Pipeline  
- Implementation: Stream processing
- Status: In Progress

## Components
The system implements several key components:
- Authentication service
- Data processor
- API gateway

## Neo4j Integration
The system uses Neo4j for graph storage and provides indexing capabilities.
`
	
	testFile1 := filepath.Join(s.testDir, "architecture.md")
	err := os.WriteFile(testFile1, []byte(archDoc), 0644)
	require.NoError(s.T(), err)
	
	// Test document 2: RFC document
	rfcDoc := `# RFC 001: Test Feature Implementation

## Summary
This RFC proposes implementing the test feature using SCIP indexing.

## Requirements
Requirement: Code Intelligence
- Must support Go projects
- Must extract symbol information
- Status: Planned

## Implementation Plan
1. Set up SCIP indexer
2. Create Neo4j schema
3. Index project symbols
4. Build query interface

The implementation uses `+"`IndexProject`"+` and `+"`NewSCIPIndexer`"+` functions.
`
	
	testFile2 := filepath.Join(s.testDir, "rfc-001.md")
	err = os.WriteFile(testFile2, []byte(rfcDoc), 0644)
	require.NoError(s.T(), err)
}

func (s *IndexingTestSuite) verifyDocumentIndexing() {
	tests := []struct {
		name          string
		query         string
		expectedCount int
		description   string
	}{
		{
			name:          "Document nodes created",
			query:         "MATCH (d:Document) RETURN count(d) as count",
			expectedCount: 2,
			description:   "Should have test document nodes",
		},
		{
			name:          "Feature nodes extracted",
			query:         "MATCH (f:Feature) RETURN count(f) as count",
			expectedCount: 5, // At least 5 features from test docs
			description:   "Should have extracted feature nodes",
		},
		{
			name:          "Documents describe features",
			query:         "MATCH (d:Document)-[:DESCRIBES]->(f:Feature) RETURN count(f) as count",
			expectedCount: 3, // At least 3 features linked to docs
			description:   "Documents should describe features",
		},
		{
			name:          "Features have different statuses",
			query:         "MATCH (f:Feature) RETURN DISTINCT f.status as status",
			expectedCount: 2, // At least 2 different statuses
			description:   "Features should have various statuses",
		},
	}
	
	for _, tt := range tests {
		s.T().Run(tt.name, func(t *testing.T) {
			result, err := s.client.ExecuteQuery(s.ctx, tt.query, nil)
			require.NoError(t, err)
			
			if tt.name == "Features have different statuses" {
				// Special case for distinct values
				assert.GreaterOrEqual(t, len(result), tt.expectedCount, tt.description)
				t.Logf("✓ %s: %d statuses found", tt.description, len(result))
			} else {
				require.Len(t, result, 1)
				record := result[0].AsMap()
				count, ok := record["count"].(int64)
				require.True(t, ok, "Count should be an integer")
				
				assert.GreaterOrEqual(t, int(count), tt.expectedCount,
					"%s: %s. Expected >= %d, got %d", tt.name, tt.description, tt.expectedCount, count)
				
				t.Logf("✓ %s: %d (expected >= %d)", tt.description, count, tt.expectedCount)
			}
		})
	}
}

func (s *IndexingTestSuite) TestCrossContextIntegration() {
	s.T().Log("Testing cross-context integration between code and documents")
	
	// Test cross-context queries
	s.verifyCrossContextQueries()
}

func (s *IndexingTestSuite) verifyCrossContextQueries() {
	tests := []struct {
		name        string
		query       string
		description string
	}{
		{
			name: "Find SCIP-related items across contexts",
			query: `
				MATCH (n)
				WHERE (n:Symbol OR n:Feature OR n:Function OR n:Document)
				  AND (
					toLower(n.name) CONTAINS 'scip' OR
					toLower(n.symbol) CONTAINS 'scip' OR
					toLower(n.title) CONTAINS 'scip'
				  )
				RETURN labels(n) as nodeTypes, count(n) as count
			`,
			description: "Should find SCIP references in both code and documents",
		},
		{
			name: "Find indexing-related items across contexts", 
			query: `
				MATCH (n)
				WHERE (n:Symbol OR n:Feature OR n:Function OR n:File)
				  AND (
					toLower(n.name) CONTAINS 'index' OR
					toLower(n.path) CONTAINS 'index' OR
					toLower(n.description) CONTAINS 'index'
				  )
				RETURN labels(n) as nodeTypes, count(n) as count
			`,
			description: "Should find indexing references in both code and documents",
		},
		{
			name: "Verify service-to-document traceability",
			query: `
				MATCH (s:Service), (d:Document)
				OPTIONAL MATCH (s)-[:CONTAINS]->()-[:REFERENCES]->(sym:Symbol)
				OPTIONAL MATCH (d)-[:DESCRIBES]->(f:Feature)
				RETURN 
					s.name as service,
					count(DISTINCT sym) as codeSymbols,
					count(DISTINCT f) as features,
					count(DISTINCT d) as documents
			`,
			description: "Should show traceability from service to documents",
		},
	}
	
	for _, tt := range tests {
		s.T().Run(tt.name, func(t *testing.T) {
			result, err := s.client.ExecuteQuery(s.ctx, tt.query, nil)
			require.NoError(t, err)
			
			assert.Greater(t, len(result), 0, "%s: %s", tt.name, tt.description)
			
			t.Logf("✓ %s: Found %d result rows", tt.description, len(result))
			
			// Log some sample results for debugging
			for i, record := range result {
				if i < 3 { // Show first 3 results
					t.Logf("  Sample result %d: %+v", i+1, record.AsMap())
				}
			}
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
	// Clean up test files
	os.RemoveAll(s.testDir)
}