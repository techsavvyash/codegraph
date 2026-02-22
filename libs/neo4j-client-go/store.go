package neo4j

import (
	"context"
	"fmt"
	"strings"

	models "github.com/context-maximiser/code-graph/libs/core-models-go"
)

// Neo4jGraphStore implements models.GraphStore using Neo4j.
type Neo4jGraphStore struct {
	client *Client
}

// NewGraphStore creates a new Neo4jGraphStore wrapping the given client.
func NewGraphStore(client *Client) *Neo4jGraphStore {
	return &Neo4jGraphStore{client: client}
}

// UpsertNode creates or updates a node identified by its NodeKey using MERGE on (nodeKey, scopeId).
func (s *Neo4jGraphStore) UpsertNode(ctx context.Context, node *models.Node) error {
	if node.NodeKey == "" {
		return fmt.Errorf("UpsertNode: node.NodeKey must not be empty")
	}

	// Determine the primary label; fall back to "Node" if no labels provided.
	label := "Node"
	if len(node.Labels) > 0 {
		label = node.Labels[0]
	}

	// Build the props map by merging all fields.
	props := buildNodeProps(node)

	item := map[string]any{
		"nodeKey": node.NodeKey,
		"scopeId": node.ScopeID,
		"props":   props,
	}

	_, err := s.client.MergeNodesBatch(ctx, label, []map[string]any{item}, 1)
	if err != nil {
		return fmt.Errorf("UpsertNode(%s): %w", node.NodeKey, err)
	}
	return nil
}

// UpsertNodes batch-upserts a slice of nodes grouped by their primary label.
func (s *Neo4jGraphStore) UpsertNodes(ctx context.Context, nodes []*models.Node) error {
	// Group nodes by their primary label so we can call MergeNodesBatch once per label.
	byLabel := make(map[string][]map[string]any)
	for _, node := range nodes {
		if node.NodeKey == "" {
			return fmt.Errorf("UpsertNodes: encountered node with empty NodeKey")
		}
		label := "Node"
		if len(node.Labels) > 0 {
			label = node.Labels[0]
		}
		item := map[string]any{
			"nodeKey": node.NodeKey,
			"scopeId": node.ScopeID,
			"props":   buildNodeProps(node),
		}
		byLabel[label] = append(byLabel[label], item)
	}

	for label, items := range byLabel {
		if _, err := s.client.MergeNodesBatch(ctx, label, items, 500); err != nil {
			return fmt.Errorf("UpsertNodes(%s): %w", label, err)
		}
	}
	return nil
}

// UpsertRelationship creates or updates a directed relationship by merging on
// (fromNodeKey, toNodeKey, type). Nodes are matched by their nodeKey.
func (s *Neo4jGraphStore) UpsertRelationship(ctx context.Context, rel *models.Relationship) error {
	if rel.StartID == "" || rel.EndID == "" {
		return fmt.Errorf("UpsertRelationship: StartID and EndID must not be empty")
	}
	if rel.Type == "" {
		return fmt.Errorf("UpsertRelationship: Type must not be empty")
	}

	relType := string(rel.Type)

	// Use MERGE on nodeKey to find the nodes, then MERGE the relationship.
	cypher := fmt.Sprintf(`
		MATCH (a {nodeKey: $fromKey}), (b {nodeKey: $toKey})
		MERGE (a)-[r:%s]->(b)
		SET r += $props
		RETURN elementId(r) AS id
	`, relType)

	props := rel.Properties
	if props == nil {
		props = map[string]any{}
	}

	_, err := s.client.ExecuteQuery(ctx, cypher, map[string]any{
		"fromKey": rel.StartID,
		"toKey":   rel.EndID,
		"props":   props,
	})
	if err != nil {
		return fmt.Errorf("UpsertRelationship(%s→%s %s): %w", rel.StartID, rel.EndID, relType, err)
	}
	return nil
}

// UpsertRelationships batch-upserts a slice of relationships grouped by type.
func (s *Neo4jGraphStore) UpsertRelationships(ctx context.Context, rels []*models.Relationship) error {
	for _, rel := range rels {
		if err := s.UpsertRelationship(ctx, rel); err != nil {
			return err
		}
	}
	return nil
}

// ApplyTombstone persists a Tombstone node that marks a main-scope node as
// deleted within the given PR overlay scope.
func (s *Neo4jGraphStore) ApplyTombstone(ctx context.Context, tombstone *models.Tombstone) error {
	if tombstone.NodeKey == "" {
		return fmt.Errorf("ApplyTombstone: tombstone.NodeKey must not be empty")
	}

	props := map[string]any{
		"nodeKey":       tombstone.NodeKey,
		"scopeId":       tombstone.ScopeID,
		"scope":         tombstone.Scope,
		"targetNodeKey": tombstone.TargetNodeKey,
		"targetLabel":   tombstone.TargetLabel,
		"reason":        string(tombstone.Reason),
	}

	item := map[string]any{
		"nodeKey": tombstone.NodeKey,
		"scopeId": tombstone.ScopeID,
		"props":   props,
	}

	_, err := s.client.MergeNodesBatch(ctx, "Tombstone", []map[string]any{item}, 1)
	if err != nil {
		return fmt.Errorf("ApplyTombstone(%s): %w", tombstone.NodeKey, err)
	}
	return nil
}

// GetNode retrieves a single node by nodeKey scoped to the given ScopeContext.
// Returns nil if not found.
func (s *Neo4jGraphStore) GetNode(ctx context.Context, nodeKey string, scope models.ScopeContext) (*models.Node, error) {
	cypher := `
		MATCH (n {nodeKey: $nodeKey, scopeId: $scopeId})
		RETURN n, labels(n) AS nodeLabels
		LIMIT 1
	`
	records, err := s.client.ExecuteQuery(ctx, cypher, map[string]any{
		"nodeKey": nodeKey,
		"scopeId": scope.ScopeID,
	})
	if err != nil {
		return nil, fmt.Errorf("GetNode(%s): %w", nodeKey, err)
	}
	if len(records) == 0 {
		return nil, nil
	}

	return recordToNode(records[0].AsMap()), nil
}

// FindNodes retrieves nodes matching the given filter.
func (s *Neo4jGraphStore) FindNodes(ctx context.Context, filter models.NodeFilter) ([]*models.Node, error) {
	var conditions []string
	params := map[string]any{}

	if filter.ScopeID != "" {
		conditions = append(conditions, "n.scopeId = $scopeId")
		params["scopeId"] = filter.ScopeID
	}
	if filter.Scope != "" {
		conditions = append(conditions, "n.scope = $scope")
		params["scope"] = filter.Scope
	}
	if filter.Service != "" {
		conditions = append(conditions, "n.service = $service")
		params["service"] = filter.Service
	}
	if filter.TenantID != "" {
		conditions = append(conditions, "n.tenantId = $tenantId")
		params["tenantId"] = filter.TenantID
	}
	if filter.Repo != "" {
		conditions = append(conditions, "n.repo = $repo")
		params["repo"] = filter.Repo
	}

	// Label filter
	var labelClauses []string
	for _, nt := range filter.NodeTypes {
		labelClauses = append(labelClauses, fmt.Sprintf("n:%s", string(nt)))
	}

	matchClause := "MATCH (n)"
	if len(labelClauses) > 0 {
		matchClause = fmt.Sprintf("MATCH (n) WHERE (%s)", strings.Join(labelClauses, " OR "))
		if len(conditions) > 0 {
			matchClause += " AND " + strings.Join(conditions, " AND ")
		}
	} else if len(conditions) > 0 {
		matchClause += " WHERE " + strings.Join(conditions, " AND ")
	}

	cypher := matchClause + "\nRETURN n, labels(n) AS nodeLabels"
	if filter.Limit > 0 {
		cypher += fmt.Sprintf("\nLIMIT %d", filter.Limit)
	}

	records, err := s.client.ExecuteQuery(ctx, cypher, params)
	if err != nil {
		return nil, fmt.Errorf("FindNodes: %w", err)
	}

	nodes := make([]*models.Node, 0, len(records))
	for _, rec := range records {
		nodes = append(nodes, recordToNode(rec.AsMap()))
	}
	return nodes, nil
}

// FindRelationships retrieves relationships matching the given filter.
func (s *Neo4jGraphStore) FindRelationships(ctx context.Context, filter models.RelFilter) ([]*models.Relationship, error) {
	params := map[string]any{}

	// Build the match clause.
	fromClause := "(a)"
	toClause := "(b)"
	if filter.FromNodeKey != "" {
		fromClause = "(a {nodeKey: $fromKey})"
		params["fromKey"] = filter.FromNodeKey
	}
	if filter.ToNodeKey != "" {
		toClause = "(b {nodeKey: $toKey})"
		params["toKey"] = filter.ToNodeKey
	}

	relTypeClause := "[r]"
	if len(filter.RelTypes) > 0 {
		types := make([]string, len(filter.RelTypes))
		for i, rt := range filter.RelTypes {
			types[i] = string(rt)
		}
		relTypeClause = fmt.Sprintf("[r:%s]", strings.Join(types, "|"))
	}

	cypher := fmt.Sprintf(`
		MATCH %s-%s->%s
		RETURN a.nodeKey AS fromKey, b.nodeKey AS toKey, type(r) AS relType, properties(r) AS props
	`, fromClause, relTypeClause, toClause)

	if filter.Limit > 0 {
		cypher += fmt.Sprintf("\nLIMIT %d", filter.Limit)
	}

	records, err := s.client.ExecuteQuery(ctx, cypher, params)
	if err != nil {
		return nil, fmt.Errorf("FindRelationships: %w", err)
	}

	rels := make([]*models.Relationship, 0, len(records))
	for _, rec := range records {
		m := rec.AsMap()
		fromKey, _ := m["fromKey"].(string)
		toKey, _ := m["toKey"].(string)
		relType, _ := m["relType"].(string)
		props, _ := m["props"].(map[string]any)
		if props == nil {
			props = map[string]any{}
		}
		rels = append(rels, &models.Relationship{
			StartID:    fromKey,
			EndID:      toKey,
			Type:       models.RelationshipType(relType),
			Properties: props,
		})
	}
	return rels, nil
}

// GetWithOverlay resolves a node applying overlay precedence:
// 1. Overlay-scope node wins if present.
// 2. A tombstone in the overlay scope hides the main-scope node.
// 3. Falls back to the main-scope node.
// 4. Returns nil if none found.
func (s *Neo4jGraphStore) GetWithOverlay(ctx context.Context, nodeKey string, scope models.ScopeContext) (*models.Node, error) {
	// If this is the main scope, just do a direct lookup.
	if scope.ScopeID == "" || scope.ScopeID == models.ScopeMain {
		return s.GetNode(ctx, nodeKey, scope)
	}

	cypher := `
		// Try overlay node first
		OPTIONAL MATCH (overlay {nodeKey: $nodeKey, scopeId: $scopeId})
		// Check for tombstone
		OPTIONAL MATCH (tombstone:Tombstone {scopeId: $scopeId, targetNodeKey: $nodeKey})
		// Fallback to main
		OPTIONAL MATCH (main {nodeKey: $nodeKey, scopeId: 'main'})
		RETURN
			overlay, labels(overlay) AS overlayLabels,
			tombstone,
			main, labels(main) AS mainLabels
		LIMIT 1
	`

	records, err := s.client.ExecuteQuery(ctx, cypher, map[string]any{
		"nodeKey": nodeKey,
		"scopeId": scope.ScopeID,
	})
	if err != nil {
		return nil, fmt.Errorf("GetWithOverlay(%s): %w", nodeKey, err)
	}
	if len(records) == 0 {
		return nil, nil
	}

	m := records[0].AsMap()

	// Overlay node wins.
	if overlay, ok := m["overlay"]; ok && overlay != nil {
		row := map[string]any{}
		if props, ok := overlay.(map[string]any); ok {
			for k, v := range props {
				row[k] = v
			}
		}
		if labels, ok := m["overlayLabels"]; ok {
			row["nodeLabels"] = labels
		}
		return recordToNode(row), nil
	}

	// Tombstone hides main-scope node.
	if tombstone, ok := m["tombstone"]; ok && tombstone != nil {
		return nil, nil
	}

	// Fall back to main-scope node.
	if main, ok := m["main"]; ok && main != nil {
		row := map[string]any{}
		if props, ok := main.(map[string]any); ok {
			for k, v := range props {
				row[k] = v
			}
		}
		if labels, ok := m["mainLabels"]; ok {
			row["nodeLabels"] = labels
		}
		return recordToNode(row), nil
	}

	return nil, nil
}

// Close releases the underlying Neo4j connection.
func (s *Neo4jGraphStore) Close(ctx context.Context) error {
	return s.client.Close(ctx)
}

// buildNodeProps merges all BaseNode fields into a flat properties map suitable
// for storage in Neo4j. Explicitly-set fields take precedence over Props.
func buildNodeProps(node *models.Node) map[string]any {
	props := make(map[string]any, len(node.Props)+8)
	for k, v := range node.Props {
		props[k] = v
	}
	// Always write the identity / scope fields.
	props["nodeKey"] = node.NodeKey
	if node.ScopeID != "" {
		props["scopeId"] = node.ScopeID
	}
	if node.Scope != "" {
		props["scope"] = node.Scope
	}
	if !node.CreatedAt.IsZero() {
		props["createdAt"] = node.CreatedAt.Unix()
	}
	if !node.UpdatedAt.IsZero() {
		props["updatedAt"] = node.UpdatedAt.Unix()
	}
	return props
}

// recordToNode converts a Neo4j record map (as returned by AsMap()) to a *Node.
func recordToNode(m map[string]any) *models.Node {
	node := &models.Node{}
	node.Props = make(map[string]any)

	for k, v := range m {
		switch k {
		case "nodeLabels":
			if labels, ok := v.([]any); ok {
				for _, l := range labels {
					if ls, ok := l.(string); ok {
						node.Labels = append(node.Labels, ls)
					}
				}
			}
		default:
			node.Props[k] = v
		}
	}

	// Promote well-known properties to struct fields.
	if nk, ok := node.Props["nodeKey"].(string); ok {
		node.NodeKey = nk
	}
	if sid, ok := node.Props["scopeId"].(string); ok {
		node.ScopeID = sid
	}
	if sc, ok := node.Props["scope"].(string); ok {
		node.Scope = sc
	}
	if id, ok := node.Props["id"].(string); ok {
		node.ID = id
	}

	return node
}
