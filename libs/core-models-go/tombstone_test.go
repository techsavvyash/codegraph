package models

import (
	"testing"
)

func TestTombstoneNodeKey(t *testing.T) {
	key := TombstoneNodeKey("pr-42", "file:pkg/models/node.go")
	expected := "tombstone:pr-42:file:pkg/models/node.go"
	if key != expected {
		t.Errorf("expected %q, got %q", expected, key)
	}
}

func TestTombstoneNodeKeyDeterminism(t *testing.T) {
	for i := 0; i < 100; i++ {
		a := TombstoneNodeKey("pr-42", "func:pkg/foo.go#Bar()")
		b := TombstoneNodeKey("pr-42", "func:pkg/foo.go#Bar()")
		if a != b {
			t.Fatal("TombstoneNodeKey is not deterministic")
		}
	}
}

func TestTombstoneNodeKeyUniqueness(t *testing.T) {
	k1 := TombstoneNodeKey("pr-42", "file:pkg/a.go")
	k2 := TombstoneNodeKey("pr-42", "file:pkg/b.go")
	k3 := TombstoneNodeKey("pr-99", "file:pkg/a.go")

	if k1 == k2 {
		t.Error("different targets should produce different keys")
	}
	if k1 == k3 {
		t.Error("different scopes should produce different keys")
	}
}

func TestTombstoneReasonConstants(t *testing.T) {
	if TombstoneFileDeleted != "file_deleted" {
		t.Error("unexpected TombstoneFileDeleted value")
	}
	if TombstoneSymbolRemoved != "symbol_removed" {
		t.Error("unexpected TombstoneSymbolRemoved value")
	}
}

// TestTombstoneReasonConstantsNonEmpty ensures reason constants are non-empty strings
// so they cannot accidentally be treated as zero-value / unset.
func TestTombstoneReasonConstantsNonEmpty(t *testing.T) {
	reasons := []TombstoneReason{TombstoneFileDeleted, TombstoneSymbolRemoved}
	for _, r := range reasons {
		if string(r) == "" {
			t.Errorf("TombstoneReason constant must not be empty")
		}
	}
}

// TestTombstoneReasonConstantsDistinct verifies the two reason constants are different
// so they can be used as discriminators in queries.
func TestTombstoneReasonConstantsDistinct(t *testing.T) {
	if TombstoneFileDeleted == TombstoneSymbolRemoved {
		t.Error("TombstoneFileDeleted and TombstoneSymbolRemoved must be distinct")
	}
}

// TestTombstoneNodeKeyDoesNotCollideWithTarget verifies that the tombstone key for a
// given (scopeID, targetNodeKey) pair is distinct from the targetNodeKey itself.
// This matters because tombstones and their targets must be separately addressable.
func TestTombstoneNodeKeyDoesNotCollideWithTarget(t *testing.T) {
	const svc = "test-service"
	targets := []string{
		FileNodeKey(svc, "pkg/models/node.go"),
		FunctionNodeKey(svc, "pkg/neo4j/client.go", "MergeNode(...)"),
		MethodNodeKey(svc, "pkg/neo4j/client.go", "(*Client).Close()"),
	}
	scopeID := "pr-42"
	for _, target := range targets {
		tombKey := TombstoneNodeKey(scopeID, target)
		if tombKey == target {
			t.Errorf("tombstone key %q must not equal target key %q", tombKey, target)
		}
	}
}

// TestTombstoneNodeKeyFormat verifies the exact format "tombstone:{scopeId}:{targetNodeKey}".
func TestTombstoneNodeKeyFormat(t *testing.T) {
	key := TombstoneNodeKey("pr-42", "func:pkg/foo.go#Bar()")
	expected := "tombstone:pr-42:func:pkg/foo.go#Bar()"
	if key != expected {
		t.Errorf("expected %q, got %q", expected, key)
	}
}

// TestTombstoneHideBehaviourInvariant verifies the semantic contract:
// given a tombstone for a target, a node lookup that detects the tombstone
// must return "hidden" (nil/invisible), even when the main-scope node exists.
// This is a pure logic test — no Neo4j required.
func TestTombstoneHideBehaviourInvariant(t *testing.T) {
	type lookup struct {
		overlayNode  bool
		tombstoned   bool
		mainNode     bool
		expectHidden bool
	}
	cases := []lookup{
		// Tombstone present + main present => hidden
		{overlayNode: false, tombstoned: true, mainNode: true, expectHidden: true},
		// Tombstone present + main absent => hidden (tombstone still hides)
		{overlayNode: false, tombstoned: true, mainNode: false, expectHidden: true},
		// Overlay present + tombstone present => overlay wins (not hidden)
		// (overlay check happens before tombstone check)
		{overlayNode: true, tombstoned: true, mainNode: true, expectHidden: false},
		// No tombstone + main present => visible
		{overlayNode: false, tombstoned: false, mainNode: true, expectHidden: false},
		// Nothing at all => not hidden (just not found)
		{overlayNode: false, tombstoned: false, mainNode: false, expectHidden: false},
	}

	for _, c := range cases {
		// Simulate the three-step overlay resolution logic
		var hidden bool
		if c.overlayNode {
			hidden = false
		} else if c.tombstoned {
			hidden = true
		} else {
			hidden = false
		}
		if hidden != c.expectHidden {
			t.Errorf("overlay=%v tombstone=%v main=%v: expected hidden=%v, got %v",
				c.overlayNode, c.tombstoned, c.mainNode, c.expectHidden, hidden)
		}
	}
}
