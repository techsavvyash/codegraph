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
