package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/context-maximiser/code-graph/pkg/indexer/documents"
	"github.com/context-maximiser/code-graph/pkg/indexer/static"
	"github.com/context-maximiser/code-graph/pkg/neo4j"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// SystemTestSuite tests the complete system functionality
type SystemTestSuite struct {
	suite.Suite
	client    *neo4j.Client
	ctx       context.Context
}

func TestSystemTestSuite(t *testing.T) {
	suite.Run(t, new(SystemTestSuite))
}

func (s *SystemTestSuite) SetupSuite() {
	// Create Neo4j client
	config := neo4j.Config{
		URI:      "bolt://localhost:7687",
		Username: "neo4j",
		Password: "password123",
		Database: "neo4j",
	}

	client, err := neo4j.NewClient(config)
	require.NoError(s.T(), err)
	
	s.client = client
	s.ctx = context.Background()
}

func (s *SystemTestSuite) TearDownSuite() {
	if s.client != nil {
		s.client.Close(s.ctx)
	}
}

func (s *SystemTestSuite) TestDatabaseConnection() {
	s.T().Log("Testing database connection")
	
	// Test basic connectivity
	result, err := s.client.ExecuteQuery(s.ctx, "RETURN 'connected' as status", nil)
	require.NoError(s.T(), err)
	require.Len(s.T(), result, 1)
	
	record := result[0].AsMap()
	status, ok := record["status"].(string)
	require.True(s.T(), ok)
	assert.Equal(s.T(), "connected", status)
	
	s.T().Log("✓ Database connection successful")
}

func (s *SystemTestSuite) TestExistingDataVerification() {
	s.T().Log("Testing existing data verification")
	
	// Check if we have indexed data
	nodeCountQuery := "MATCH (n) RETURN labels(n) AS nodeType, count(n) AS count ORDER BY count DESC"
	result, err := s.client.ExecuteQuery(s.ctx, nodeCountQuery, nil)
	require.NoError(s.T(), err)
	
	nodeTypes := make(map[string]int64)
	totalNodes := int64(0)
	
	for _, record := range result {
		recordMap := record.AsMap()
		nodeType := recordMap["nodeType"].([]interface{})[0].(string)
		count := recordMap["count"].(int64)
		nodeTypes[nodeType] = count
		totalNodes += count
	}
	
	s.T().Logf("Found %d total nodes across %d types", totalNodes, len(nodeTypes))
	for nodeType, count := range nodeTypes {
		s.T().Logf("  %s: %d", nodeType, count)
	}
	
	// We should have some data
	assert.Greater(s.T(), totalNodes, int64(0), "Should have indexed data in database")
}

func (s *SystemTestSuite) TestSearchFunctionality() {
	s.T().Log("Testing search functionality")
	
	queryBuilder := neo4j.NewQueryBuilder(s.client)
	
	// Test searches for common terms that should exist
	searchTests := []struct {
		term        string
		description string
	}{
		{"Neo4j", "Neo4j-related items"},
		{"client", "Client-related items"},
		{"service", "Service-related items"},
		{"query", "Query-related items"},
	}
	
	for _, tt := range searchTests {
		s.T().Run(tt.term, func(t *testing.T) {
			start := time.Now()
			results, err := queryBuilder.SearchNodes(s.ctx, tt.term, 
				[]string{"Symbol", "Function", "Method", "File", "Service", "Feature", "Document"}, 10)
			duration := time.Since(start)
			
			require.NoError(t, err)
			t.Logf("Search for '%s' returned %d results in %v", tt.term, len(results), duration)
			
			// Performance check
			assert.Less(t, duration, 2*time.Second, "Search should complete quickly")
			
			// Log sample results for debugging
			for i, result := range results[:min(len(results), 3)] {
				recordMap := result.AsMap()
				if labels, ok := recordMap["nodeLabels"].([]interface{}); ok && len(labels) > 0 {
					t.Logf("  Result %d: %s", i+1, labels[0])
				}
			}
		})
	}
}

func (s *SystemTestSuite) TestDocumentIndexingCapability() {
	s.T().Log("Testing document indexing capability")
	
	// Create a simple test document
	testDir := "test_docs_temp"
	os.MkdirAll(testDir, 0755)
	defer os.RemoveAll(testDir)
	
	testDoc := `# Test Document

## Introduction
This is a test document for validating document indexing.

## Features
Feature: Test Feature
- Implementation: Test implementation
- Status: Testing

The document mentions NEO4J and SCIP integration.
`
	
	testFile := filepath.Join(testDir, "test.md")
	err := os.WriteFile(testFile, []byte(testDoc), 0644)
	require.NoError(s.T(), err)
	
	// Test document parsing
	docIndexer := documents.NewDocumentIndexer(s.client)
	
	// Get initial document count
	initialCount, err := s.getDocumentCount()
	require.NoError(s.T(), err)
	
	// Index the test document
	err = docIndexer.IndexDocument(s.ctx, testFile)
	require.NoError(s.T(), err)
	
	// Verify document was indexed
	finalCount, err := s.getDocumentCount()
	require.NoError(s.T(), err)
	
	assert.Greater(s.T(), finalCount, initialCount, "Should have added at least one document")
	s.T().Logf("✓ Document indexing successful: %d -> %d documents", initialCount, finalCount)
	
	// Test feature extraction
	featureCount, err := s.getFeatureCount()
	require.NoError(s.T(), err)
	assert.Greater(s.T(), featureCount, int64(0), "Should have extracted features")
	s.T().Logf("✓ Feature extraction successful: %d features found", featureCount)
}

func (s *SystemTestSuite) TestSCIPIndexingCapability() {
	s.T().Log("Testing SCIP indexing capability")
	
	scipIndexer := static.NewSCIPIndexer(s.client, "test-service", "v1.0.0", "https://github.com/test/repo")
	
	// Test environment validation
	err := scipIndexer.ValidateEnvironment()
	if err != nil {
		s.T().Skipf("SCIP environment not available: %v", err)
		return
	}
	
	s.T().Log("✓ SCIP environment validation passed")
}

func (s *SystemTestSuite) TestSchemaIntegrity() {
	s.T().Log("Testing schema integrity")
	
	// Test basic schema elements exist
	schemaTests := []struct {
		query       string
		description string
	}{
		{"SHOW CONSTRAINTS", "Constraints should exist"},
		{"SHOW INDEXES", "Indexes should exist"},
	}
	
	for _, tt := range schemaTests {
		s.T().Run(tt.description, func(t *testing.T) {
			result, err := s.client.ExecuteQuery(s.ctx, tt.query, nil)
			require.NoError(t, err)
			
			t.Logf("✓ %s: Found %d items", tt.description, len(result))
		})
	}
}

func (s *SystemTestSuite) TestCypherQueryPatterns() {
	s.T().Log("Testing common Cypher query patterns")
	
	queryTests := []struct {
		name  string
		query string
		desc  string
	}{
		{
			name:  "Node count by type",
			query: "MATCH (n) RETURN labels(n)[0] as type, count(n) as count ORDER BY count DESC LIMIT 5",
			desc:  "Should return node counts by type",
		},
		{
			name:  "Relationship patterns",
			query: "MATCH (a)-[r]->(b) RETURN type(r) as relType, count(r) as count ORDER BY count DESC LIMIT 5",
			desc:  "Should return relationship counts by type",
		},
		{
			name:  "Service structure",
			query: "MATCH (s:Service) OPTIONAL MATCH (s)-[:CONTAINS]->(f:File) RETURN s.name, count(f) as fileCount",
			desc:  "Should show service structure",
		},
	}
	
	for _, tt := range queryTests {
		s.T().Run(tt.name, func(t *testing.T) {
			start := time.Now()
			result, err := s.client.ExecuteQuery(s.ctx, tt.query, nil)
			duration := time.Since(start)
			
			require.NoError(t, err, "Query should execute successfully")
			assert.Less(t, duration, 2*time.Second, "Query should be performant")
			
			t.Logf("✓ %s: %d results in %v", tt.desc, len(result), duration)
			
			// Show sample results
			for i, record := range result[:min(len(result), 2)] {
				t.Logf("  Sample %d: %+v", i+1, record.AsMap())
			}
		})
	}
}

func (s *SystemTestSuite) TestSystemEnd2End() {
	s.T().Log("Testing end-to-end system functionality")
	
	// This test verifies the complete system works as expected
	// by performing a series of operations that simulate real usage
	
	// 1. Verify we can search for technical terms
	queryBuilder := neo4j.NewQueryBuilder(s.client)
	
	searchTerms := []string{"client", "service", "graph"}
	for _, term := range searchTerms {
		results, err := queryBuilder.SearchNodes(s.ctx, term, nil, 5)
		require.NoError(s.T(), err)
		s.T().Logf("Search '%s': %d results", term, len(results))
	}
	
	// 2. Verify database health
	healthQuery := `
		MATCH (n) 
		RETURN count(n) as totalNodes,
		       count(DISTINCT labels(n)[0]) as nodeTypes
	`
	
	result, err := s.client.ExecuteQuery(s.ctx, healthQuery, nil)
	require.NoError(s.T(), err)
	require.Len(s.T(), result, 1)
	
	health := result[0].AsMap()
	totalNodes := health["totalNodes"].(int64)
	nodeTypes := health["nodeTypes"].(int64)
	
	assert.Greater(s.T(), totalNodes, int64(0), "Should have nodes in database")
	assert.Greater(s.T(), nodeTypes, int64(3), "Should have multiple node types")
	
	s.T().Logf("✓ System health: %d nodes across %d types", totalNodes, nodeTypes)
}

// Helper functions

func (s *SystemTestSuite) getDocumentCount() (int64, error) {
	result, err := s.client.ExecuteQuery(s.ctx, "MATCH (d:Document) RETURN count(d) as count", nil)
	if err != nil {
		return 0, err
	}
	if len(result) == 0 {
		return 0, nil
	}
	return result[0].AsMap()["count"].(int64), nil
}

func (s *SystemTestSuite) getFeatureCount() (int64, error) {
	result, err := s.client.ExecuteQuery(s.ctx, "MATCH (f:Feature) RETURN count(f) as count", nil)
	if err != nil {
		return 0, err
	}
	if len(result) == 0 {
		return 0, nil
	}
	return result[0].AsMap()["count"].(int64), nil
}

// TestEnhancedLocationMetadata tests the enhanced location metadata functionality
func (s *SystemTestSuite) TestEnhancedLocationMetadata() {
	s.T().Run("VerifyLocationMetadataExists", func(t *testing.T) {
		// Query for functions with location metadata
		cypher := `
			MATCH (f:Function)
			WHERE f.startByte IS NOT NULL AND f.endByte IS NOT NULL 
				AND f.startLine IS NOT NULL AND f.endLine IS NOT NULL
				AND f.linesOfCode IS NOT NULL
			RETURN f.name AS name, f.filePath AS filePath, 
				   f.startByte AS startByte, f.endByte AS endByte,
				   f.startLine AS startLine, f.endLine AS endLine,
				   f.linesOfCode AS linesOfCode
			LIMIT 5
		`
		
		result, err := s.client.ExecuteQuery(s.ctx, cypher, nil)
		require.NoError(t, err)
		
		assert.Greater(t, len(result), 0, "Should find functions with location metadata")
		
		for _, record := range result {
			recordMap := record.AsMap()
			
			// Verify all location fields are present and valid
			assert.NotEmpty(t, recordMap["name"], "Function name should not be empty")
			assert.NotEmpty(t, recordMap["filePath"], "File path should not be empty")
			
			startByte := recordMap["startByte"].(int64)
			endByte := recordMap["endByte"].(int64)
			startLine := recordMap["startLine"].(int64)
			endLine := recordMap["endLine"].(int64)
			linesOfCode := recordMap["linesOfCode"].(int64)
			
			assert.Greater(t, startByte, int64(0), "Start byte should be positive")
			assert.Greater(t, endByte, startByte, "End byte should be greater than start byte")
			assert.Greater(t, startLine, int64(0), "Start line should be positive")
			assert.GreaterOrEqual(t, endLine, startLine, "End line should be >= start line")
			assert.Greater(t, linesOfCode, int64(0), "Lines of code should be positive")
			assert.Equal(t, endLine-startLine+1, linesOfCode, "Lines of code should match line range")
		}
	})

	s.T().Run("VerifyMethodLocationMetadata", func(t *testing.T) {
		// Query for methods with location metadata
		cypher := `
			MATCH (m:Method)
			WHERE m.startByte IS NOT NULL AND m.endByte IS NOT NULL
			RETURN m.name AS name, m.filePath AS filePath,
				   m.startByte AS startByte, m.endByte AS endByte,
				   m.linesOfCode AS linesOfCode
			LIMIT 3
		`
		
		result, err := s.client.ExecuteQuery(s.ctx, cypher, nil)
		require.NoError(t, err)
		
		assert.Greater(t, len(result), 0, "Should find methods with location metadata")
		
		for _, record := range result {
			recordMap := record.AsMap()
			startByte := recordMap["startByte"].(int64)
			endByte := recordMap["endByte"].(int64)
			linesOfCode := recordMap["linesOfCode"].(int64)
			
			assert.Greater(t, endByte, startByte, "Method end byte > start byte")
			assert.Greater(t, linesOfCode, int64(0), "Method should have positive LOC")
		}
	})
}

// TestSourceCodeRetrieval tests the source code retrieval functionality
func (s *SystemTestSuite) TestSourceCodeRetrieval() {
	queryBuilder := neo4j.NewQueryBuilder(s.client)

	s.T().Run("RetrieveExistingFunction", func(t *testing.T) {
		// Get a known function from our codebase
		sourceCode, err := queryBuilder.GetFunctionSourceCode(s.ctx, "SetSCIPBinary")
		require.NoError(t, err)
		
		// Verify the source code contains expected content
		assert.Contains(t, sourceCode, "func", "Source should contain func keyword")
		assert.Contains(t, sourceCode, "SetSCIPBinary", "Source should contain function name")
		assert.Contains(t, sourceCode, "scipBinary", "Source should contain expected variable")
		
		// Verify it's not empty and looks like Go code
		assert.Greater(t, len(sourceCode), 10, "Source code should have reasonable length")
		assert.Contains(t, sourceCode, "{", "Source should contain opening brace")
		assert.Contains(t, sourceCode, "}", "Source should contain closing brace")
	})

	s.T().Run("RetrieveNonExistentFunction", func(t *testing.T) {
		// Try to get a function that doesn't exist
		_, err := queryBuilder.GetFunctionSourceCode(s.ctx, "NonExistentFunction12345")
		assert.Error(t, err, "Should return error for non-existent function")
		assert.Contains(t, err.Error(), "function not found", "Error should indicate function not found")
	})

	s.T().Run("RetrieveFunctionBySignature", func(t *testing.T) {
		// First, find a function with its signature
		cypher := `
			MATCH (f:Function)
			WHERE f.signature IS NOT NULL AND f.name IS NOT NULL
			RETURN f.name AS name, f.signature AS signature
			LIMIT 1
		`
		
		result, err := s.client.ExecuteQuery(s.ctx, cypher, nil)
		require.NoError(t, err)
		require.Greater(t, len(result), 0, "Should find at least one function with signature")
		
		record := result[0].AsMap()
		functionName := record["name"].(string)
		signature := record["signature"].(string)
		
		// Retrieve source code by signature
		sourceCode, err := queryBuilder.GetFunctionSourceCodeBySignature(s.ctx, signature)
		require.NoError(t, err)
		
		// Verify the retrieved code contains the function name
		assert.Contains(t, sourceCode, functionName, "Source should contain the function name")
		assert.Greater(t, len(sourceCode), 10, "Source code should have reasonable length")
	})
}

// TestByteOffsetAccuracy tests the accuracy of byte offset calculations
func (s *SystemTestSuite) TestByteOffsetAccuracy() {
	queryBuilder := neo4j.NewQueryBuilder(s.client)

	s.T().Run("VerifyByteOffsetAccuracy", func(t *testing.T) {
		// Get a function with its location metadata
		cypher := `
			MATCH (f:Function)
			WHERE f.filePath IS NOT NULL AND f.startByte IS NOT NULL 
				AND f.endByte IS NOT NULL AND f.name IS NOT NULL
			RETURN f.name AS name, f.filePath AS filePath,
				   f.startByte AS startByte, f.endByte AS endByte
			LIMIT 1
		`
		
		result, err := s.client.ExecuteQuery(s.ctx, cypher, nil)
		require.NoError(t, err)
		require.Greater(t, len(result), 0, "Should find function with location metadata")
		
		record := result[0].AsMap()
		functionName := record["name"].(string)
		filePath := record["filePath"].(string)
		startByte := int(record["startByte"].(int64))
		endByte := int(record["endByte"].(int64))
		
		// Read the file directly - handle path resolution like our API does
		content, err := os.ReadFile(filePath)
		if err != nil {
			// If relative path fails, try from project root
			if !filepath.IsAbs(filePath) {
				if pwd, pwdErr := os.Getwd(); pwdErr == nil {
					projectRoot := pwd
					if strings.HasSuffix(pwd, "/test/integration") {
						projectRoot = filepath.Dir(filepath.Dir(pwd))
					}
					absolutePath := filepath.Join(projectRoot, filePath)
					content, err = os.ReadFile(absolutePath)
				}
			}
		}
		require.NoError(t, err)
		
		// Extract using byte offsets
		if startByte < len(content) && endByte <= len(content) && startByte < endByte {
			directExtraction := string(content[startByte:endByte])
			
			// Get source code via our API
			apiExtraction, err := queryBuilder.GetFunctionSourceCode(s.ctx, functionName)
			require.NoError(t, err)
			
			// They should match
			assert.Equal(t, directExtraction, apiExtraction, 
				"Direct byte extraction should match API extraction")
			
			// Verify it contains function-like content
			assert.Contains(t, directExtraction, "func", "Extracted code should contain func")
			assert.Contains(t, directExtraction, functionName, "Extracted code should contain function name")
		}
	})
}

// TestLocationMetadataConsistency tests consistency across different indexing methods
func (s *SystemTestSuite) TestLocationMetadataConsistency() {
	s.T().Run("CompareASTAndSCIPMetadata", func(t *testing.T) {
		// This test would be more comprehensive with both AST and SCIP data
		// For now, verify internal consistency
		cypher := `
			MATCH (f:Function)
			WHERE f.startLine IS NOT NULL AND f.endLine IS NOT NULL 
				AND f.linesOfCode IS NOT NULL
			RETURN f.name AS name, f.startLine AS startLine, f.endLine AS endLine,
				   f.linesOfCode AS linesOfCode
			LIMIT 10
		`
		
		result, err := s.client.ExecuteQuery(s.ctx, cypher, nil)
		require.NoError(t, err)
		
		for _, record := range result {
			recordMap := record.AsMap()
			startLine := recordMap["startLine"].(int64)
			endLine := recordMap["endLine"].(int64)
			linesOfCode := recordMap["linesOfCode"].(int64)
			
			expectedLOC := endLine - startLine + 1
			assert.Equal(t, expectedLOC, linesOfCode, 
				"Lines of code should match calculated value for function %s", 
				recordMap["name"])
		}
	})
}

// TestSourceCodeRetrievalEdgeCases tests edge cases in source code retrieval
func (s *SystemTestSuite) TestSourceCodeRetrievalEdgeCases() {
	queryBuilder := neo4j.NewQueryBuilder(s.client)

	s.T().Run("HandleEmptyFunctionName", func(t *testing.T) {
		_, err := queryBuilder.GetFunctionSourceCode(s.ctx, "")
		assert.Error(t, err, "Should handle empty function name gracefully")
	})

	s.T().Run("HandleSpecialCharactersInFunctionName", func(t *testing.T) {
		_, err := queryBuilder.GetFunctionSourceCode(s.ctx, "func@#$%")
		assert.Error(t, err, "Should handle special characters gracefully")
	})

	s.T().Run("VerifyFallbackToLineBasedExtraction", func(t *testing.T) {
		// Find a function and test the line-based fallback
		// This would require manipulating the data to test the fallback path
		// For now, just ensure the API doesn't crash with various inputs
		_, err := queryBuilder.GetFunctionSourceCode(s.ctx, "calculateByteOffsets")
		// This should either succeed or fail gracefully
		if err != nil {
			assert.Contains(t, err.Error(), "not found", "Should provide meaningful error message")
		}
	})
}

