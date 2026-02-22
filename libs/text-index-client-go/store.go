package textindex

import "context"

// TextResult is a single result from a text/keyword search.
type TextResult struct {
	NodeKey  string
	Score    float64
	Snippet  string
	Metadata map[string]string
}

// SearchOpts controls text search behaviour.
type SearchOpts struct {
	Limit     int
	MinScore  float64
	TenantID  string
	Repo      string
	NodeTypes []string
}

// TextIndexStore abstracts keyword/BM25 text search operations.
// Implementations: OpenSearch (production), Mock (tests).
type TextIndexStore interface {
	// IndexDocument indexes a document identified by nodeKey.
	// content is the raw text. meta contains arbitrary key-value metadata.
	IndexDocument(ctx context.Context, nodeKey, content string, meta map[string]string) error

	// IndexDocuments batch-indexes a slice of documents.
	IndexDocuments(ctx context.Context, docs []IndexDoc) error

	// Search performs a BM25/keyword search and returns ranked results.
	Search(ctx context.Context, query string, opts SearchOpts) ([]TextResult, error)

	// Delete removes a document by nodeKey.
	Delete(ctx context.Context, nodeKey string) error

	// DeleteByRepo removes all documents for a given tenantID + repo.
	DeleteByRepo(ctx context.Context, tenantID, repo string) error

	// Ping checks that the text index backend is reachable.
	Ping(ctx context.Context) error

	// Close releases any underlying connections.
	Close() error
}

// IndexDoc is the input unit for batch indexing.
type IndexDoc struct {
	NodeKey  string
	Content  string
	Metadata map[string]string
}
