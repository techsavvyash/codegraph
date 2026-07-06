package integration

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/context-maximiser/code-graph/libs/neo4j-go"
	"github.com/context-maximiser/code-graph/libs/schema-go"
)

// Test configuration
var (
	testNeo4jURI  = getEnv("TEST_NEO4J_URI", "bolt://localhost:7687")
	testNeo4jUser = getEnv("TEST_NEO4J_USER", "neo4j")
	testNeo4jPass = getEnv("TEST_NEO4J_PASS", "password123")
	testNeo4jDB   = getEnv("TEST_NEO4J_DB", "neo4j")
)

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// createTestClient creates a Neo4j client for testing
func createTestClient(t *testing.T) *neo4j.Client {
	t.Helper()
	
	config := neo4j.Config{
		URI:      testNeo4jURI,
		Username: testNeo4jUser,
		Password: testNeo4jPass,
		Database: testNeo4jDB,
	}

	client, err := neo4j.NewClient(config)
	if err != nil {
		t.Skipf("Cannot connect to Neo4j: %v (set TEST_NEO4J_URI to run integration tests)", err)
	}

	return client
}

// cleanupDatabase removes all test data from the database
func cleanupDatabase(t *testing.T, client *neo4j.Client) {
	t.Helper()
	
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Delete all nodes and relationships
	cypher := "MATCH (n) DETACH DELETE n"
	_, err := client.ExecuteQuery(ctx, cypher, nil)
	if err != nil {
		t.Logf("Warning: failed to cleanup database: %v", err)
	}
}

func TestNeo4jConnection(t *testing.T) {
	client := createTestClient(t)
	defer client.Close(context.Background())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	info, err := client.GetDatabaseInfo(ctx)
	if err != nil {
		t.Fatalf("Failed to get database info: %v", err)
	}

	if name, ok := info["name"]; !ok || name == "" {
		t.Error("Expected database name in info response")
	}

	t.Logf("Connected to Neo4j database: %+v", info)
}

func TestSchemaCreation(t *testing.T) {
	client := createTestClient(t)
	defer func() {
		cleanupDatabase(t, client)
		client.Close(context.Background())
	}()

	schemaManager := schema.NewSchemaManager(client)
	
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Create schema
	err := schemaManager.CreateSchema(ctx)
	if err != nil {
		t.Fatalf("Failed to create schema: %v", err)
	}

	// Validate schema
	err = schemaManager.ValidateSchema(ctx)
	if err != nil {
		t.Fatalf("Schema validation failed: %v", err)
	}

	// Get schema info
	info, err := schemaManager.GetSchemaInfo(ctx)
	if err != nil {
		t.Fatalf("Failed to get schema info: %v", err)
	}

	// Check that we have indexes.
	// Note: GetConstraints() returns [] by design (multi-scope model removed all
	// single-property unique constraints); only check that indexes were created.
	constraints, _ := info["constraints"].([]map[string]any)

	indexes, ok := info["indexes"].([]map[string]any)
	if !ok || len(indexes) == 0 {
		t.Error("Expected indexes to be created")
	}

	t.Logf("Schema: %d constraints (intentionally 0 in multi-scope model), %d indexes", len(constraints), len(indexes))
}

func TestBasicNodeOperations(t *testing.T) {
	client := createTestClient(t)
	defer func() {
		cleanupDatabase(t, client)
		client.Close(context.Background())
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create a test service node
	serviceProps := map[string]any{
		"name":          "test-service",
		"language":      "Go",
		"version":       "v1.0.0",
		"repositoryUrl": "https://github.com/test/test-service",
		"createdAt":     time.Now().UTC(),
		"updatedAt":     time.Now().UTC(),
	}

	serviceID, err := client.CreateNode(ctx, []string{"Service"}, serviceProps)
	if err != nil {
		t.Fatalf("Failed to create service node: %v", err)
	}

	// Create a test file node
	fileProps := map[string]any{
		"path":         "/test/main.go",
		"absolutePath": "/home/user/test/main.go",
		"language":     "Go",
		"hash":         "abc123",
		"lineCount":    100,
		"createdAt":    time.Now().UTC(),
		"updatedAt":    time.Now().UTC(),
	}

	fileID, err := client.CreateNode(ctx, []string{"File"}, fileProps)
	if err != nil {
		t.Fatalf("Failed to create file node: %v", err)
	}

	// Create relationship between service and file
	_, err = client.CreateRelationship(ctx, serviceID, fileID, "CONTAINS", nil)
	if err != nil {
		t.Fatalf("Failed to create relationship: %v", err)
	}

	// Query the relationship
	cypher := `
		MATCH (s:Service {name: $serviceName})-[:CONTAINS]->(f:File)
		RETURN s.name as serviceName, f.path as filePath
	`
	params := map[string]any{"serviceName": "test-service"}
	
	result, err := client.ExecuteQuery(ctx, cypher, params)
	if err != nil {
		t.Fatalf("Failed to query relationship: %v", err)
	}

	if len(result) != 1 {
		t.Fatalf("Expected 1 record, got %d", len(result))
	}

	record := result[0].AsMap()
	if record["serviceName"] != "test-service" {
		t.Errorf("Expected service name 'test-service', got %v", record["serviceName"])
	}
	if record["filePath"] != "/test/main.go" {
		t.Errorf("Expected file path '/test/main.go', got %v", record["filePath"])
	}

	t.Log("Successfully created and queried nodes and relationships")
}

func TestBatchOperations(t *testing.T) {
	client := createTestClient(t)
	defer func() {
		cleanupDatabase(t, client)
		client.Close(context.Background())
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Test batch node creation
	nodes := []neo4j.BatchNode{
		{
			Labels: []string{"TestNode"},
			Properties: map[string]any{
				"name":  "node1",
				"value": 1,
			},
		},
		{
			Labels: []string{"TestNode"},
			Properties: map[string]any{
				"name":  "node2",
				"value": 2,
			},
		},
		{
			Labels: []string{"TestNode"},
			Properties: map[string]any{
				"name":  "node3",
				"value": 3,
			},
		},
	}

	err := client.BatchCreateNodes(ctx, nodes)
	if err != nil {
		t.Fatalf("Failed to batch create nodes: %v", err)
	}

	// Verify nodes were created
	cypher := "MATCH (n:TestNode) RETURN count(n) as count"
	result, err := client.ExecuteQuery(ctx, cypher, nil)
	if err != nil {
		t.Fatalf("Failed to query test nodes: %v", err)
	}

	if len(result) == 0 {
		t.Fatal("No results returned from count query")
	}

	count, ok := result[0].AsMap()["count"].(int64)
	if !ok || count != 3 {
		t.Fatalf("Expected 3 test nodes, got %v", count)
	}

	t.Log("Successfully created nodes in batch")
}