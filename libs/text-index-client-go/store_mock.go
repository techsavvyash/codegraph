package textindex

import (
	"context"
	"strings"
	"sync"
)

// MockTextIndexStore is an in-memory TextIndexStore for unit tests.
type MockTextIndexStore struct {
	mu     sync.RWMutex
	docs   map[string]IndexDoc // nodeKey → doc
	Errors map[string]error
}

func NewMockTextIndexStore() *MockTextIndexStore {
	return &MockTextIndexStore{
		docs:   make(map[string]IndexDoc),
		Errors: make(map[string]error),
	}
}

func (m *MockTextIndexStore) IndexDocument(ctx context.Context, nodeKey, content string, meta map[string]string) error {
	if err := m.Errors["IndexDocument"]; err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.docs[nodeKey] = IndexDoc{NodeKey: nodeKey, Content: content, Metadata: meta}
	return nil
}

func (m *MockTextIndexStore) IndexDocuments(ctx context.Context, docs []IndexDoc) error {
	for _, d := range docs {
		if err := m.IndexDocument(ctx, d.NodeKey, d.Content, d.Metadata); err != nil {
			return err
		}
	}
	return nil
}

// Search does simple case-insensitive substring matching on content.
func (m *MockTextIndexStore) Search(ctx context.Context, query string, opts SearchOpts) ([]TextResult, error) {
	if err := m.Errors["Search"]; err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	lower := strings.ToLower(query)
	var out []TextResult
	for _, d := range m.docs {
		if strings.Contains(strings.ToLower(d.Content), lower) {
			out = append(out, TextResult{NodeKey: d.NodeKey, Score: 1.0, Snippet: d.Content, Metadata: d.Metadata})
		}
	}
	limit := opts.Limit
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MockTextIndexStore) Delete(ctx context.Context, nodeKey string) error {
	if err := m.Errors["Delete"]; err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.docs, nodeKey)
	return nil
}

func (m *MockTextIndexStore) DeleteByRepo(ctx context.Context, tenantID, repo string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, d := range m.docs {
		if d.Metadata["tenantId"] == tenantID && d.Metadata["repo"] == repo {
			delete(m.docs, k)
		}
	}
	return nil
}

func (m *MockTextIndexStore) Ping(ctx context.Context) error {
	return m.Errors["Ping"]
}

func (m *MockTextIndexStore) Close() error {
	return nil
}

// AllDocs returns all indexed documents (for test assertions).
func (m *MockTextIndexStore) AllDocs() []IndexDoc {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]IndexDoc, 0, len(m.docs))
	for _, d := range m.docs {
		out = append(out, d)
	}
	return out
}
