package documents

import (
	"context"
	"time"
)

// ExternalDocument represents a document fetched from an external source.
type ExternalDocument struct {
	ID        string            // Unique identifier in the source system.
	Title     string            // Human-readable title.
	Content   string            // Markdown or plain-text content.
	SourceURL string            // Canonical URL to the document.
	Source    string            // Source system name (e.g., "confluence", "gdocs").
	UpdatedAt time.Time         // Last modification time in the source.
	Metadata  map[string]string // Arbitrary key-value metadata from the source.
}

// DocConnector defines the interface for fetching documents from external systems.
type DocConnector interface {
	// Name returns the connector name (e.g., "confluence", "gdocs").
	Name() string

	// ListDocuments returns a list of document IDs/URLs available in the given space/collection.
	ListDocuments(ctx context.Context, space string) ([]ExternalDocument, error)

	// FetchDocument retrieves a single document by its ID or URL and returns normalized content.
	FetchDocument(ctx context.Context, docID string) (*ExternalDocument, error)
}
