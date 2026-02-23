package textindex

import (
	"context"
	"errors"
	"testing"
)

func TestMockTextIndexStore_IndexAndSearch(t *testing.T) {
	store := NewMockTextIndexStore()
	ctx := context.Background()

	err := store.IndexDocument(ctx, "key1", "hello world foo bar", map[string]string{"tenantId": "t1", "repo": "r1"})
	if err != nil {
		t.Fatalf("IndexDocument: %v", err)
	}

	results, err := store.Search(ctx, "hello", SearchOpts{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].NodeKey != "key1" {
		t.Errorf("expected nodeKey=key1, got %s", results[0].NodeKey)
	}
	if results[0].Score != 1.0 {
		t.Errorf("expected score=1.0, got %f", results[0].Score)
	}
}

func TestMockTextIndexStore_SearchNoMatch(t *testing.T) {
	store := NewMockTextIndexStore()
	ctx := context.Background()

	_ = store.IndexDocument(ctx, "key1", "hello world", nil)

	results, err := store.Search(ctx, "notfound", SearchOpts{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestMockTextIndexStore_Delete(t *testing.T) {
	store := NewMockTextIndexStore()
	ctx := context.Background()

	_ = store.IndexDocument(ctx, "key1", "some content", nil)
	_ = store.IndexDocument(ctx, "key2", "other content", nil)

	if err := store.Delete(ctx, "key1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	docs := store.AllDocs()
	if len(docs) != 1 {
		t.Errorf("expected 1 doc after delete, got %d", len(docs))
	}
	if docs[0].NodeKey != "key2" {
		t.Errorf("expected key2 to remain, got %s", docs[0].NodeKey)
	}
}

func TestMockTextIndexStore_DeleteByRepo(t *testing.T) {
	store := NewMockTextIndexStore()
	ctx := context.Background()

	_ = store.IndexDocument(ctx, "k1", "content1", map[string]string{"tenantId": "t1", "repo": "r1"})
	_ = store.IndexDocument(ctx, "k2", "content2", map[string]string{"tenantId": "t1", "repo": "r2"})
	_ = store.IndexDocument(ctx, "k3", "content3", map[string]string{"tenantId": "t1", "repo": "r1"})

	if err := store.DeleteByRepo(ctx, "t1", "r1"); err != nil {
		t.Fatalf("DeleteByRepo: %v", err)
	}

	docs := store.AllDocs()
	if len(docs) != 1 {
		t.Errorf("expected 1 doc after DeleteByRepo, got %d", len(docs))
	}
	if docs[0].NodeKey != "k2" {
		t.Errorf("expected k2 to remain, got %s", docs[0].NodeKey)
	}
}

func TestMockTextIndexStore_SearchLimit(t *testing.T) {
	store := NewMockTextIndexStore()
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		key := "key" + string(rune('0'+i))
		_ = store.IndexDocument(ctx, key, "matching content here", nil)
	}

	results, err := store.Search(ctx, "matching", SearchOpts{Limit: 3})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) > 3 {
		t.Errorf("expected at most 3 results with limit=3, got %d", len(results))
	}
}

func TestMockTextIndexStore_ErrorInjection_IndexDocument(t *testing.T) {
	store := NewMockTextIndexStore()
	ctx := context.Background()
	injected := errors.New("index error")
	store.Errors["IndexDocument"] = injected

	err := store.IndexDocument(ctx, "k1", "content", nil)
	if !errors.Is(err, injected) {
		t.Errorf("expected injected error, got %v", err)
	}
}

func TestMockTextIndexStore_ErrorInjection_Search(t *testing.T) {
	store := NewMockTextIndexStore()
	ctx := context.Background()
	injected := errors.New("search error")
	store.Errors["Search"] = injected

	_, err := store.Search(ctx, "query", SearchOpts{})
	if !errors.Is(err, injected) {
		t.Errorf("expected injected error, got %v", err)
	}
}

func TestMockTextIndexStore_ErrorInjection_Delete(t *testing.T) {
	store := NewMockTextIndexStore()
	ctx := context.Background()
	injected := errors.New("delete error")
	store.Errors["Delete"] = injected

	err := store.Delete(ctx, "k1")
	if !errors.Is(err, injected) {
		t.Errorf("expected injected error, got %v", err)
	}
}

func TestMockTextIndexStore_ErrorInjection_Ping(t *testing.T) {
	store := NewMockTextIndexStore()
	ctx := context.Background()
	injected := errors.New("ping error")
	store.Errors["Ping"] = injected

	err := store.Ping(ctx)
	if !errors.Is(err, injected) {
		t.Errorf("expected injected error, got %v", err)
	}
}

func TestMockTextIndexStore_IndexDocuments_Batch(t *testing.T) {
	store := NewMockTextIndexStore()
	ctx := context.Background()

	docs := []IndexDoc{
		{NodeKey: "a", Content: "alpha content", Metadata: map[string]string{"repo": "r1"}},
		{NodeKey: "b", Content: "beta content", Metadata: map[string]string{"repo": "r1"}},
	}
	if err := store.IndexDocuments(ctx, docs); err != nil {
		t.Fatalf("IndexDocuments: %v", err)
	}

	all := store.AllDocs()
	if len(all) != 2 {
		t.Errorf("expected 2 docs after batch index, got %d", len(all))
	}
}

func TestMockTextIndexStore_Ping_NoError(t *testing.T) {
	store := NewMockTextIndexStore()
	ctx := context.Background()
	if err := store.Ping(ctx); err != nil {
		t.Errorf("expected no error from Ping, got %v", err)
	}
}

func TestMockTextIndexStore_Close(t *testing.T) {
	store := NewMockTextIndexStore()
	if err := store.Close(); err != nil {
		t.Errorf("expected no error from Close, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// ScopeID filtering tests (Phase 3)
// ---------------------------------------------------------------------------

func TestMockTextIndexStore_SearchScopeFilter_MatchesExactScope(t *testing.T) {
	store := NewMockTextIndexStore()
	ctx := context.Background()

	_ = store.IndexDocument(ctx, "main::k1", "matching content", map[string]string{"scopeId": "main"})
	_ = store.IndexDocument(ctx, "pr-42::k2", "matching content", map[string]string{"scopeId": "pr-42"})
	_ = store.IndexDocument(ctx, "pr-99::k3", "matching content", map[string]string{"scopeId": "pr-99"})

	results, err := store.Search(ctx, "matching", SearchOpts{ScopeID: "pr-42"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	// Should match pr-42 and main (overlay precedence), but NOT pr-99.
	if len(results) != 2 {
		t.Fatalf("expected 2 results (pr-42 + main), got %d", len(results))
	}
	keys := map[string]bool{}
	for _, r := range results {
		keys[r.NodeKey] = true
	}
	if !keys["main::k1"] {
		t.Error("expected main::k1 in results")
	}
	if !keys["pr-42::k2"] {
		t.Error("expected pr-42::k2 in results")
	}
	if keys["pr-99::k3"] {
		t.Error("pr-99::k3 should NOT be in results")
	}
}

func TestMockTextIndexStore_SearchScopeFilter_MainSeesBothMainScope(t *testing.T) {
	store := NewMockTextIndexStore()
	ctx := context.Background()

	_ = store.IndexDocument(ctx, "main::k1", "hello world", map[string]string{"scopeId": "main"})
	_ = store.IndexDocument(ctx, "pr-42::k2", "hello world", map[string]string{"scopeId": "pr-42"})

	results, err := store.Search(ctx, "hello", SearchOpts{ScopeID: "main"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	// main scope should only see "main" docs.
	if len(results) != 1 {
		t.Fatalf("expected 1 result (only main), got %d", len(results))
	}
	if results[0].NodeKey != "main::k1" {
		t.Errorf("expected main::k1, got %s", results[0].NodeKey)
	}
}

func TestMockTextIndexStore_SearchScopeFilter_EmptyScopeReturnsAll(t *testing.T) {
	store := NewMockTextIndexStore()
	ctx := context.Background()

	_ = store.IndexDocument(ctx, "k1", "content", map[string]string{"scopeId": "main"})
	_ = store.IndexDocument(ctx, "k2", "content", map[string]string{"scopeId": "pr-42"})
	_ = store.IndexDocument(ctx, "k3", "content", map[string]string{"scopeId": "pr-99"})

	results, err := store.Search(ctx, "content", SearchOpts{}) // No ScopeID filter
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	if len(results) != 3 {
		t.Errorf("expected 3 results with no scope filter, got %d", len(results))
	}
}

func TestMockTextIndexStore_SearchScopeFilter_NoMetadata(t *testing.T) {
	store := NewMockTextIndexStore()
	ctx := context.Background()

	// Doc without scopeId metadata
	_ = store.IndexDocument(ctx, "k1", "content", nil)

	results, err := store.Search(ctx, "content", SearchOpts{ScopeID: "pr-42"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	// Doc without scopeId metadata should be filtered out when ScopeID is set.
	if len(results) != 0 {
		t.Errorf("expected 0 results (doc has no scopeId), got %d", len(results))
	}
}

// Compile-time check that MockTextIndexStore implements TextIndexStore.
var _ TextIndexStore = (*MockTextIndexStore)(nil)
