package models

import "fmt"

// TombstoneReason describes why a tombstone was created.
type TombstoneReason string

const (
	TombstoneFileDeleted   TombstoneReason = "file_deleted"
	TombstoneSymbolRemoved TombstoneReason = "symbol_removed"
)

// Tombstone represents a marker that hides a main-scope node in a PR overlay.
// When querying a PR scope, tombstoned nodes from the main scope should be excluded.
type Tombstone struct {
	BaseNode
	TargetNodeKey string          `json:"targetNodeKey" neo4j:"targetNodeKey"`
	TargetLabel   string          `json:"targetLabel" neo4j:"targetLabel"`
	Reason        TombstoneReason `json:"reason" neo4j:"reason"`
}

// TombstoneNodeKey returns "tombstone:{scopeId}:{targetNodeKey}".
func TombstoneNodeKey(scopeID, targetNodeKey string) string {
	return fmt.Sprintf("tombstone:%s:%s", scopeID, targetNodeKey)
}
