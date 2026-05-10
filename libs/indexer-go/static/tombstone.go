package static

import (
	"context"
	"fmt"

	"github.com/context-maximiser/code-graph/libs/core-models-go"
	"github.com/context-maximiser/code-graph/libs/neo4j-go"
)

// TombstoneCreator handles creating tombstone nodes for PR overlays.
// When files or symbols are deleted in a PR branch, tombstones are created
// so that queries against the PR scope can hide the corresponding main-scope nodes.
//
// serviceName scopes deletions to a single service. Module-relative file paths
// (e.g. "main.go") collide across services, so a tombstone for "main.go"
// without a service would shadow every service's main.go in PR scope.
type TombstoneCreator struct {
	client      *neo4j.Client
	scopeCtx    models.ScopeContext
	serviceName string
}

// NewTombstoneCreator creates a new TombstoneCreator scoped to a single service.
func NewTombstoneCreator(client *neo4j.Client, scopeCtx models.ScopeContext, serviceName string) *TombstoneCreator {
	return &TombstoneCreator{
		client:      client,
		scopeCtx:    scopeCtx,
		serviceName: serviceName,
	}
}

// CreateFileDeletedTombstones creates tombstones for a deleted file and all its child definitions.
// It queries the main scope for the file node and all nodes contained within it,
// then creates Tombstone nodes in the PR scope for each.
//
// Lookup is anchored on the file's globally-unique nodeKey (which now embeds
// serviceName). Children are reached via the Service→File→* CONTAINS chain
// rather than by `n.filePath = $filePath`, since filePaths are module-relative
// and not unique on their own.
func (tc *TombstoneCreator) CreateFileDeletedTombstones(ctx context.Context, deletedPaths []string) (int, error) {
	totalCreated := 0

	for _, filePath := range deletedPaths {
		fileNodeKey := models.FileNodeKey(tc.serviceName, filePath)

		// Create tombstone for the file itself
		if err := tc.createTombstone(ctx, fileNodeKey, "File", models.TombstoneFileDeleted); err != nil {
			return totalCreated, fmt.Errorf("failed to create file tombstone for %s: %w", filePath, err)
		}
		totalCreated++

		// Find all child definitions in main scope reachable from the File node.
		// Anchored on the file's nodeKey (service-disambiguated) and traversed
		// through CONTAINS so we never tombstone another service's same-named file.
		cypher := `
			MATCH (f:File {nodeKey: $fileNodeKey, scopeId: 'main'})-[:CONTAINS*1..3]->(n)
			WHERE n.scopeId = 'main' AND n.nodeKey IS NOT NULL
			RETURN DISTINCT n.nodeKey AS nodeKey, labels(n) AS labels
		`
		results, err := tc.client.ExecuteQuery(ctx, cypher, map[string]any{
			"fileNodeKey": fileNodeKey,
		})
		if err != nil {
			fmt.Printf("Warning: failed to query child nodes for %s: %v\n", filePath, err)
			continue
		}

		for _, record := range results {
			recMap := record.AsMap()
			childNodeKey, ok := recMap["nodeKey"].(string)
			if !ok || childNodeKey == "" {
				continue
			}

			// Get the primary label
			var label string
			if labels, ok := recMap["labels"].([]interface{}); ok && len(labels) > 0 {
				label, _ = labels[0].(string)
			}

			if err := tc.createTombstone(ctx, childNodeKey, label, models.TombstoneFileDeleted); err != nil {
				fmt.Printf("Warning: failed to create tombstone for child %s: %v\n", childNodeKey, err)
				continue
			}
			totalCreated++
		}
	}

	return totalCreated, nil
}

// CreateSymbolRemovedTombstones creates tombstones for removed symbols.
func (tc *TombstoneCreator) CreateSymbolRemovedTombstones(ctx context.Context, removedNodeKeys []string, label string) (int, error) {
	created := 0
	for _, nodeKey := range removedNodeKeys {
		if err := tc.createTombstone(ctx, nodeKey, label, models.TombstoneSymbolRemoved); err != nil {
			return created, fmt.Errorf("failed to create tombstone for %s: %w", nodeKey, err)
		}
		created++
	}
	return created, nil
}

// createTombstone creates a single Tombstone node in the PR scope.
func (tc *TombstoneCreator) createTombstone(ctx context.Context, targetNodeKey, targetLabel string, reason models.TombstoneReason) error {
	tombstoneKey := models.TombstoneNodeKey(tc.scopeCtx.ScopeID, targetNodeKey)

	props := map[string]any{
		"nodeKey":       tombstoneKey,
		"targetNodeKey": targetNodeKey,
		"targetLabel":   targetLabel,
		"reason":        string(reason),
		"scope":         tc.scopeCtx.Scope,
		"scopeId":       tc.scopeCtx.ScopeID,
	}

	_, err := tc.client.MergeNode(ctx, []string{"Tombstone"},
		map[string]any{"nodeKey": tombstoneKey, "scopeId": tc.scopeCtx.ScopeID}, props)
	return err
}
