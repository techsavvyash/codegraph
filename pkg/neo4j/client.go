package neo4j

import (
	"context"
	"fmt"
	"time"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// Config holds the configuration for Neo4j connection
type Config struct {
	URI      string
	Username string
	Password string
	Database string
}

// Client wraps the Neo4j driver and provides higher-level operations
type Client struct {
	driver   neo4j.DriverWithContext
	database string
}

// NewClient creates a new Neo4j client with the given configuration
func NewClient(config Config) (*Client, error) {
	driver, err := neo4j.NewDriverWithContext(
		config.URI,
		neo4j.BasicAuth(config.Username, config.Password, ""),
		func(c *neo4j.Config) {
			c.MaxConnectionPoolSize = 50
			c.MaxConnectionLifetime = 30 * time.Minute
			c.ConnectionAcquisitionTimeout = 2 * time.Minute
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create Neo4j driver: %w", err)
	}

	// Test the connection
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err = driver.VerifyConnectivity(ctx)
	if err != nil {
		driver.Close(ctx)
		return nil, fmt.Errorf("failed to verify Neo4j connectivity: %w", err)
	}

	return &Client{
		driver:   driver,
		database: config.Database,
	}, nil
}

// Close closes the Neo4j driver connection
func (c *Client) Close(ctx context.Context) error {
	return c.driver.Close(ctx)
}

// ExecuteQuery executes a Cypher query and returns the result
func (c *Client) ExecuteQuery(ctx context.Context, cypher string, params map[string]any) ([]*neo4j.Record, error) {
	session := c.driver.NewSession(ctx, neo4j.SessionConfig{
		DatabaseName: c.database,
	})
	defer session.Close(ctx)

	result, err := session.Run(ctx, cypher, params)
	if err != nil {
		return nil, err
	}

	records, err := result.Collect(ctx)
	if err != nil {
		return nil, err
	}

	return records, nil
}

// ExecuteWrite executes a write transaction
func (c *Client) ExecuteWrite(ctx context.Context, work func(tx neo4j.ManagedTransaction) (any, error)) (any, error) {
	session := c.driver.NewSession(ctx, neo4j.SessionConfig{
		DatabaseName: c.database,
		AccessMode:   neo4j.AccessModeWrite,
	})
	defer session.Close(ctx)

	return session.ExecuteWrite(ctx, work)
}

// ExecuteRead executes a read transaction
func (c *Client) ExecuteRead(ctx context.Context, work func(tx neo4j.ManagedTransaction) (any, error)) (any, error) {
	session := c.driver.NewSession(ctx, neo4j.SessionConfig{
		DatabaseName: c.database,
		AccessMode:   neo4j.AccessModeRead,
	})
	defer session.Close(ctx)

	return session.ExecuteRead(ctx, work)
}

// CreateNode creates a single node in the graph
func (c *Client) CreateNode(ctx context.Context, labels []string, properties map[string]any) (string, error) {
	labelStr := ""
	for i, label := range labels {
		if i > 0 {
			labelStr += ":"
		}
		labelStr += label
	}

	cypher := fmt.Sprintf("CREATE (n:%s) SET n = $props RETURN elementId(n) as id", labelStr)
	
	result, err := c.ExecuteQuery(ctx, cypher, map[string]any{
		"props": properties,
	})
	if err != nil {
		return "", fmt.Errorf("failed to create node: %w", err)
	}

	if len(result) == 0 {
		return "", fmt.Errorf("no records returned from create node query")
	}

	id, ok := result[0].AsMap()["id"].(string)
	if !ok {
		return "", fmt.Errorf("failed to extract node ID from result")
	}

	return id, nil
}

// MergeNode creates or updates a node using MERGE
func (c *Client) MergeNode(ctx context.Context, labels []string, mergeProps, setProps map[string]any) (string, error) {
	labelStr := ""
	for i, label := range labels {
		if i > 0 {
			labelStr += ":"
		}
		labelStr += label
	}

	// Build the merge properties clause
	mergeClause := ""
	for key := range mergeProps {
		if mergeClause != "" {
			mergeClause += ", "
		}
		mergeClause += fmt.Sprintf("%s: $merge.%s", key, key)
	}

	cypher := fmt.Sprintf(`
		MERGE (n:%s {%s})
		SET n += $set
		RETURN elementId(n) as id
	`, labelStr, mergeClause)

	params := map[string]any{
		"merge": mergeProps,
		"set":   setProps,
	}

	result, err := c.ExecuteQuery(ctx, cypher, params)
	if err != nil {
		return "", fmt.Errorf("failed to merge node: %w", err)
	}

	if len(result) == 0 {
		return "", fmt.Errorf("no records returned from merge node query")
	}

	id, ok := result[0].AsMap()["id"].(string)
	if !ok {
		return "", fmt.Errorf("failed to extract node ID from result")
	}

	return id, nil
}

// CreateRelationship creates a relationship between two nodes
func (c *Client) CreateRelationship(ctx context.Context, fromID, toID, relType string, properties map[string]any) (string, error) {
	cypher := fmt.Sprintf(`
		MATCH (from), (to)
		WHERE elementId(from) = $fromId AND elementId(to) = $toId
		CREATE (from)-[r:%s]->(to)
		SET r = $props
		RETURN elementId(r) as id
	`, relType)

	params := map[string]any{
		"fromId": fromID,
		"toId":   toID,
		"props":  properties,
	}

	result, err := c.ExecuteQuery(ctx, cypher, params)
	if err != nil {
		return "", fmt.Errorf("failed to create relationship: %w", err)
	}

	if len(result) == 0 {
		return "", fmt.Errorf("no records returned from create relationship query")
	}

	id, ok := result[0].AsMap()["id"].(string)
	if !ok {
		return "", fmt.Errorf("failed to extract relationship ID from result")
	}

	return id, nil
}

// BatchCreateNodes creates multiple nodes in a single transaction
func (c *Client) BatchCreateNodes(ctx context.Context, nodes []BatchNode) error {
	cypher := `
		UNWIND $nodes AS nodeData
		CALL apoc.create.node(nodeData.labels, nodeData.properties) YIELD node
		RETURN count(node) as created
	`

	params := map[string]any{
		"nodes": nodes,
	}

	_, err := c.ExecuteQuery(ctx, cypher, params)
	if err != nil {
		return fmt.Errorf("failed to batch create nodes: %w", err)
	}

	return nil
}

// BatchMergeNodes creates or updates multiple nodes in a single transaction
func (c *Client) BatchMergeNodes(ctx context.Context, nodes []BatchMergeNode) error {
	cypher := `
		UNWIND $nodes AS nodeData
		CALL apoc.merge.node(nodeData.labels, nodeData.mergeProps, nodeData.setProps) YIELD node
		RETURN count(node) as processed
	`

	params := map[string]any{
		"nodes": nodes,
	}

	_, err := c.ExecuteQuery(ctx, cypher, params)
	if err != nil {
		return fmt.Errorf("failed to batch merge nodes: %w", err)
	}

	return nil
}

// BatchCreateRelationships creates multiple relationships in a single transaction
func (c *Client) BatchCreateRelationships(ctx context.Context, relationships []BatchRelationship) error {
	cypher := `
		UNWIND $rels AS relData
		MATCH (from), (to)
		WHERE elementId(from) = relData.fromId AND elementId(to) = relData.toId
		CALL apoc.create.relationship(from, relData.type, relData.properties, to) YIELD rel
		RETURN count(rel) as created
	`

	params := map[string]any{
		"rels": relationships,
	}

	_, err := c.ExecuteQuery(ctx, cypher, params)
	if err != nil {
		return fmt.Errorf("failed to batch create relationships: %w", err)
	}

	return nil
}

// GetDatabaseInfo returns information about the database
func (c *Client) GetDatabaseInfo(ctx context.Context) (map[string]any, error) {
	cypher := "CALL dbms.components() YIELD name, versions, edition"
	
	result, err := c.ExecuteQuery(ctx, cypher, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get database info: %w", err)
	}

	info := make(map[string]any)
	for _, record := range result {
		recordMap := record.AsMap()
		info["name"] = recordMap["name"]
		info["versions"] = recordMap["versions"]
		info["edition"] = recordMap["edition"]
	}

	return info, nil
}

// BatchNode represents a node for batch operations
type BatchNode struct {
	Labels     []string       `json:"labels"`
	Properties map[string]any `json:"properties"`
}

// BatchMergeNode represents a node for batch merge operations
type BatchMergeNode struct {
	Labels     []string       `json:"labels"`
	MergeProps map[string]any `json:"mergeProps"`
	SetProps   map[string]any `json:"setProps"`
}

// BatchRelationship represents a relationship for batch operations
type BatchRelationship struct {
	FromID     string         `json:"fromId"`
	ToID       string         `json:"toId"`
	Type       string         `json:"type"`
	Properties map[string]any `json:"properties"`
}