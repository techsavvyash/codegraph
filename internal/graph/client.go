package neo4j

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// ScopedKey derives a composite identity key from nodeKey and scopeId.
// If scopeId is empty, defaults to "main". Returns "nodeKey|scopeId".
// This is used for UNIQUE constraints on per-label scopedKey properties.
func ScopedKey(nodeKey, scopeId string) string {
	if scopeId == "" {
		scopeId = "main"
	}
	return nodeKey + "|" + scopeId
}

// applyScopedKey adds scopedKey to properties if nodeKey exists.
// scopeId is read from properties["scopeId"] with default "main".
// This ensures all written nodes have the composite identity key.
func applyScopedKey(props map[string]any) {
	if nodeKey, ok := props["nodeKey"].(string); ok && nodeKey != "" {
		scopeId := "main"
		if s, ok := props["scopeId"].(string); ok && s != "" {
			scopeId = s
		}
		props["scopedKey"] = ScopedKey(nodeKey, scopeId)
	}
}

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
			c.MaxConnectionPoolSize = 10  // Reduced for stability
			c.MaxConnectionLifetime = 5 * time.Minute
			c.ConnectionAcquisitionTimeout = 30 * time.Second
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
		labelStr += Ident(label)
	}

	// Derive scopedKey automatically for identity
	applyScopedKey(properties)

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
		labelStr += Ident(label)
	}

	// Build the merge properties clause with Ident() for property names
	mergeClause := ""
	for key := range mergeProps {
		if mergeClause != "" {
			mergeClause += ", "
		}
		mergeClause += fmt.Sprintf("%s: $merge.%s", Ident(key), Ident(key))
	}

	// Derive scopedKey from mergeProps nodeKey and setProps/mergeProps scopeId.
	// Nodes without a nodeKey must not get a scopedKey — a shared sentinel like
	// "|main" would collide under the per-label UNIQUE constraints.
	setClause := "SET n += $set"
	params := map[string]any{
		"merge": mergeProps,
		"set":   setProps,
	}
	if nodeKey, ok := mergeProps["nodeKey"].(string); ok && nodeKey != "" {
		scopeId := "main"
		if si, ok := mergeProps["scopeId"].(string); ok && si != "" {
			scopeId = si
		} else if si, ok := setProps["scopeId"].(string); ok && si != "" {
			scopeId = si
		}
		setClause += ", n.scopedKey = $scopedKey"
		params["scopedKey"] = ScopedKey(nodeKey, scopeId)
	}

	cypher := fmt.Sprintf(`
		MERGE (n:%s {%s})
		%s
		RETURN elementId(n) as id
	`, labelStr, mergeClause, setClause)

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
	`, Ident(relType))

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

// MergeRelationship creates or updates a relationship between two nodes using MERGE.
// mergeProps are used to match an existing relationship; setProps are applied on match/create.
func (c *Client) MergeRelationship(ctx context.Context, fromID, toID, relType string, mergeProps, setProps map[string]any) (string, error) {
	mergeClause := ""
	if len(mergeProps) > 0 {
		parts := make([]string, 0, len(mergeProps))
		for key := range mergeProps {
			parts = append(parts, fmt.Sprintf("%s: $merge.%s", Ident(key), Ident(key)))
		}
		mergeClause = " {" + strings.Join(parts, ", ") + "}"
	}

	cypher := fmt.Sprintf(`
		MATCH (from), (to)
		WHERE elementId(from) = $fromId AND elementId(to) = $toId
		MERGE (from)-[r:%s%s]->(to)
		SET r += $set
		RETURN elementId(r) as id
	`, Ident(relType), mergeClause)

	params := map[string]any{
		"fromId": fromID,
		"toId":   toID,
		"merge":  mergeProps,
		"set":    setProps,
	}

	result, err := c.ExecuteQuery(ctx, cypher, params)
	if err != nil {
		return "", fmt.Errorf("failed to merge relationship: %w", err)
	}

	if len(result) == 0 {
		return "", fmt.Errorf("no records returned from merge relationship query")
	}

	id, ok := result[0].AsMap()["id"].(string)
	if !ok {
		return "", fmt.Errorf("failed to extract relationship ID from result")
	}

	return id, nil
}

// MergeNodesBatch merges nodes in UNWIND batches of batchSize, using pure Cypher (no APOC).
// label is the single Neo4j label for all items.
// Each item must have "nodeKey", "scopeId", and "props" keys.
// Returns a map of nodeKey → elementId for every merged node.
func (c *Client) MergeNodesBatch(ctx context.Context, label string, items []map[string]any, batchSize int) (map[string]string, error) {
	if len(items) == 0 {
		return make(map[string]string), nil
	}
	if batchSize <= 0 {
		batchSize = 500
	}

	cypher := fmt.Sprintf(`
		UNWIND $batch AS item
		MERGE (n:%s {nodeKey: item.nodeKey, scopeId: item.scopeId})
		SET n += item.props, n.scopedKey = item.nodeKey + '|' + coalesce(item.scopeId, 'main')
		RETURN item.nodeKey AS nodeKey, elementId(n) AS id
	`, Ident(label))

	result := make(map[string]string, len(items))

	for start := 0; start < len(items); start += batchSize {
		end := start + batchSize
		if end > len(items) {
			end = len(items)
		}
		chunk := items[start:end]

		records, err := c.ExecuteQuery(ctx, cypher, map[string]any{"batch": chunk})
		if err != nil {
			return nil, fmt.Errorf("MergeNodesBatch(%s) chunk %d-%d failed: %w", label, start, end, err)
		}

		for _, rec := range records {
			m := rec.AsMap()
			nk, _ := m["nodeKey"].(string)
			id, _ := m["id"].(string)
			if nk != "" && id != "" {
				result[nk] = id
			}
		}
	}

	return result, nil
}

// CreateRelsBatch creates relationships in UNWIND batches of batchSize, using pure Cypher (no APOC).
// relType is the single relationship type for all items.
// Each item must have "fromId" and "toId" (elementId strings) and "props" (map).
func (c *Client) CreateRelsBatch(ctx context.Context, relType string, items []map[string]any, batchSize int) error {
	if len(items) == 0 {
		return nil
	}
	if batchSize <= 0 {
		batchSize = 500
	}

	cypher := fmt.Sprintf(`
		UNWIND $batch AS item
		MATCH (a), (b)
		WHERE elementId(a) = item.fromId AND elementId(b) = item.toId
		CREATE (a)-[r:%s]->(b)
		SET r = item.props
	`, Ident(relType))

	for start := 0; start < len(items); start += batchSize {
		end := start + batchSize
		if end > len(items) {
			end = len(items)
		}
		chunk := items[start:end]

		_, err := c.ExecuteQuery(ctx, cypher, map[string]any{"batch": chunk})
		if err != nil {
			return fmt.Errorf("CreateRelsBatch(%s) chunk %d-%d failed: %w", relType, start, end, err)
		}
	}

	return nil
}

// MergeRelsBatch is the idempotent counterpart of CreateRelsBatch: it MERGEs
// relationships in UNWIND batches so re-indexing the same scope cannot
// duplicate edges. Each item must have "fromId" and "toId" (elementId strings)
// and "props" (map). The merge key is (fromId, toId, relType); props are
// updated with SET r += item.props on both create and match.
func (c *Client) MergeRelsBatch(ctx context.Context, relType string, items []map[string]any, batchSize int) error {
	if len(items) == 0 {
		return nil
	}
	if batchSize <= 0 {
		batchSize = 500
	}

	cypher := fmt.Sprintf(`
		UNWIND $batch AS item
		MATCH (a), (b)
		WHERE elementId(a) = item.fromId AND elementId(b) = item.toId
		MERGE (a)-[r:%s]->(b)
		SET r += item.props
	`, Ident(relType))

	for start := 0; start < len(items); start += batchSize {
		end := start + batchSize
		if end > len(items) {
			end = len(items)
		}
		chunk := items[start:end]

		_, err := c.ExecuteQuery(ctx, cypher, map[string]any{"batch": chunk})
		if err != nil {
			return fmt.Errorf("MergeRelsBatch(%s) chunk %d-%d failed: %w", relType, start, end, err)
		}
	}

	return nil
}

// BatchCreateNodes creates multiple nodes in a single transaction
func (c *Client) BatchCreateNodes(ctx context.Context, nodes []BatchNode) error {
	cypher := `
		UNWIND $nodes AS nodeData
		CALL apoc.create.node(nodeData.labels, nodeData.properties) YIELD node
		RETURN count(node) as created
	`

	// Serialize BatchNode structs to map[string]any — Neo4j driver
	// cannot serialize custom struct types directly as parameters.
	nodeParams := make([]map[string]any, len(nodes))
	for i, n := range nodes {
		// Derive scopedKey automatically for identity
		applyScopedKey(n.Properties)
		nodeParams[i] = map[string]any{
			"labels":     n.Labels,
			"properties": n.Properties,
		}
	}

	params := map[string]any{
		"nodes": nodeParams,
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

	nodeParams := make([]map[string]any, len(nodes))
	for i, n := range nodes {
		// Derive scopedKey from mergeProps nodeKey + scopeId. Skip nodes
		// without a nodeKey — a shared sentinel like "|main" would collide
		// under the per-label UNIQUE constraints.
		mergeProps := n.MergeProps
		setProps := n.SetProps
		if nodeKey, ok := mergeProps["nodeKey"].(string); ok && nodeKey != "" {
			scopeId := "main"
			if si, ok := mergeProps["scopeId"].(string); ok && si != "" {
				scopeId = si
			} else if si, ok := setProps["scopeId"].(string); ok && si != "" {
				scopeId = si
			}
			if setProps == nil {
				setProps = make(map[string]any)
			}
			setProps["scopedKey"] = ScopedKey(nodeKey, scopeId)
		}

		nodeParams[i] = map[string]any{
			"labels":     n.Labels,
			"mergeProps": mergeProps,
			"setProps":   setProps,
		}
	}

	params := map[string]any{
		"nodes": nodeParams,
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

	relParams := make([]map[string]any, len(relationships))
	for i, r := range relationships {
		relParams[i] = map[string]any{
			"fromId":     r.FromID,
			"toId":       r.ToID,
			"type":       r.Type,
			"properties": r.Properties,
		}
	}

	params := map[string]any{
		"rels": relParams,
	}

	_, err := c.ExecuteQuery(ctx, cypher, params)
	if err != nil {
		return fmt.Errorf("failed to batch create relationships: %w", err)
	}

	return nil
}

// SetNodeProperty sets a property on a node identified by its ID
func (c *Client) SetNodeProperty(ctx context.Context, nodeID string, propertyName string, propertyValue any) error {
	cypher := `
		MATCH (n)
		WHERE elementId(n) = $nodeId
		SET n.` + propertyName + ` = $value
		RETURN n
	`

	params := map[string]any{
		"nodeId": nodeID,
		"value":  propertyValue,
	}

	result, err := c.ExecuteQuery(ctx, cypher, params)
	if err != nil {
		return fmt.Errorf("failed to set node property: %w", err)
	}

	if len(result) == 0 {
		return fmt.Errorf("node not found: %s", nodeID)
	}

	return nil
}

// SetNodeProperties sets multiple properties on a node identified by its ID
func (c *Client) SetNodeProperties(ctx context.Context, nodeID string, properties map[string]any) error {
	cypher := `
		MATCH (n)
		WHERE elementId(n) = $nodeId
		SET n += $props
		RETURN n
	`

	params := map[string]any{
		"nodeId": nodeID,
		"props":  properties,
	}

	result, err := c.ExecuteQuery(ctx, cypher, params)
	if err != nil {
		return fmt.Errorf("failed to set node properties: %w", err)
	}

	if len(result) == 0 {
		return fmt.Errorf("node not found: %s", nodeID)
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