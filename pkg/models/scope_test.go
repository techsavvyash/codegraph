package models

import (
	"testing"
)

func TestDefaultScope(t *testing.T) {
	sc := DefaultScope()
	if sc.Scope != ScopeMain {
		t.Errorf("expected scope %q, got %q", ScopeMain, sc.Scope)
	}
	if sc.ScopeID != ScopeMain {
		t.Errorf("expected scopeId %q, got %q", ScopeMain, sc.ScopeID)
	}
}

func TestNewPRScope(t *testing.T) {
	sc := NewPRScope("42")
	if sc.Scope != ScopePR {
		t.Errorf("expected scope %q, got %q", ScopePR, sc.Scope)
	}
	if sc.ScopeID != "pr-42" {
		t.Errorf("expected scopeId %q, got %q", "pr-42", sc.ScopeID)
	}
}

func TestScopeProps(t *testing.T) {
	sc := NewPRScope("99")
	props := sc.Props()
	if props["scope"] != "pr" {
		t.Errorf("expected scope pr, got %v", props["scope"])
	}
	if props["scopeId"] != "pr-99" {
		t.Errorf("expected scopeId pr-99, got %v", props["scopeId"])
	}
}

func TestDefaultScopeProps(t *testing.T) {
	sc := DefaultScope()
	props := sc.Props()
	if props["scope"] != "main" {
		t.Errorf("expected scope main, got %v", props["scope"])
	}
	if props["scopeId"] != "main" {
		t.Errorf("expected scopeId main, got %v", props["scopeId"])
	}
}
