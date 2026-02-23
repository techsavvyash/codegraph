package documents

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestConfluenceStorageToMarkdown_Headers(t *testing.T) {
	input := `<h1>Title</h1><h2>Section</h2><h3>Sub</h3>`
	result := confluenceStorageToMarkdown(input)

	if !strings.Contains(result, "# Title") {
		t.Errorf("expected '# Title', got %q", result)
	}
	if !strings.Contains(result, "## Section") {
		t.Errorf("expected '## Section', got %q", result)
	}
	if !strings.Contains(result, "### Sub") {
		t.Errorf("expected '### Sub', got %q", result)
	}
}

func TestConfluenceStorageToMarkdown_BoldAndItalic(t *testing.T) {
	input := `<p><strong>bold</strong> and <em>italic</em></p>`
	result := confluenceStorageToMarkdown(input)

	if !strings.Contains(result, "**bold**") {
		t.Errorf("expected **bold**, got %q", result)
	}
	if !strings.Contains(result, "*italic*") {
		t.Errorf("expected *italic*, got %q", result)
	}
}

func TestConfluenceStorageToMarkdown_Lists(t *testing.T) {
	input := `<ul><li>item one</li><li>item two</li></ul>`
	result := confluenceStorageToMarkdown(input)

	if !strings.Contains(result, "- item one") {
		t.Errorf("expected '- item one', got %q", result)
	}
	if !strings.Contains(result, "- item two") {
		t.Errorf("expected '- item two', got %q", result)
	}
}

func TestConfluenceStorageToMarkdown_Links(t *testing.T) {
	input := `<a href="https://example.com">Example</a>`
	result := confluenceStorageToMarkdown(input)

	if !strings.Contains(result, "[Example](https://example.com)") {
		t.Errorf("expected markdown link, got %q", result)
	}
}

func TestConfluenceStorageToMarkdown_InlineCode(t *testing.T) {
	input := `<p>Use <code>foo()</code> here</p>`
	result := confluenceStorageToMarkdown(input)

	if !strings.Contains(result, "`foo()`") {
		t.Errorf("expected inline code, got %q", result)
	}
}

func TestConfluenceStorageToMarkdown_HTMLEntities(t *testing.T) {
	input := `<p>A &amp; B &lt; C &gt; D</p>`
	result := confluenceStorageToMarkdown(input)

	if !strings.Contains(result, "A & B < C > D") {
		t.Errorf("expected decoded entities, got %q", result)
	}
}

func TestConfluenceStorageToMarkdown_StripsTags(t *testing.T) {
	input := `<div class="panel"><span style="color:red">text</span></div>`
	result := confluenceStorageToMarkdown(input)

	if strings.Contains(result, "<") {
		t.Errorf("expected no HTML tags, got %q", result)
	}
	if !strings.Contains(result, "text") {
		t.Errorf("expected 'text' in output, got %q", result)
	}
}

func TestConfluenceStorageToMarkdown_Empty(t *testing.T) {
	result := confluenceStorageToMarkdown("")
	if result != "" {
		t.Errorf("expected empty string, got %q", result)
	}
}

func TestConfluenceConnector_Name(t *testing.T) {
	c := NewConfluenceConnector(ConfluenceConfig{})
	if c.Name() != "confluence" {
		t.Errorf("expected 'confluence', got %s", c.Name())
	}
}

func TestConfluenceConnector_FetchDocument(t *testing.T) {
	// Mock Confluence API server.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/api/v2/pages/12345") {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		page := confluencePageV2{
			ID:     "12345",
			Title:  "Test Page",
			Status: "current",
		}
		page.Body.Storage.Value = "<h1>Hello</h1><p>World</p>"
		page.Version.Number = 3
		page.Version.CreatedAt = "2025-01-15T10:00:00Z"

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(page)
	}))
	defer server.Close()

	c := NewConfluenceConnector(ConfluenceConfig{
		BaseURL:  server.URL,
		Username: "user",
		APIToken: "token",
	})

	doc, err := c.FetchDocument(context.Background(), "12345")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if doc.ID != "12345" {
		t.Errorf("expected ID '12345', got %s", doc.ID)
	}
	if doc.Title != "Test Page" {
		t.Errorf("expected title 'Test Page', got %s", doc.Title)
	}
	if !strings.Contains(doc.Content, "# Hello") {
		t.Errorf("expected markdown content with '# Hello', got %q", doc.Content)
	}
	if !strings.Contains(doc.Content, "World") {
		t.Errorf("expected 'World' in content, got %q", doc.Content)
	}
	if doc.Source != "confluence" {
		t.Errorf("expected source 'confluence', got %s", doc.Source)
	}
	if doc.Metadata["version"] != "3" {
		t.Errorf("expected version '3', got %s", doc.Metadata["version"])
	}
}

func TestConfluenceConnector_ListDocuments(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if strings.Contains(r.URL.Path, "/api/v2/spaces") && !strings.Contains(r.URL.Path, "/pages") {
			// Space lookup.
			json.NewEncoder(w).Encode(map[string]any{
				"results": []map[string]any{
					{"id": "space-1", "key": "DEV", "name": "Development"},
				},
			})
			return
		}

		if strings.Contains(r.URL.Path, "/pages") {
			// Pages listing.
			json.NewEncoder(w).Encode(map[string]any{
				"results": []map[string]any{
					{"id": "101", "title": "Page One", "status": "current"},
					{"id": "102", "title": "Page Two", "status": "current"},
				},
			})
			return
		}

		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer server.Close()

	c := NewConfluenceConnector(ConfluenceConfig{
		BaseURL:  server.URL,
		Username: "user",
		APIToken: "token",
	})

	docs, err := c.ListDocuments(context.Background(), "DEV")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(docs) != 2 {
		t.Fatalf("expected 2 documents, got %d", len(docs))
	}

	if docs[0].Title != "Page One" {
		t.Errorf("expected first doc 'Page One', got %s", docs[0].Title)
	}
	if docs[1].ID != "102" {
		t.Errorf("expected second doc ID '102', got %s", docs[1].ID)
	}
}

// Verify the interface is satisfied at compile time.
var _ DocConnector = (*ConfluenceConnector)(nil)
