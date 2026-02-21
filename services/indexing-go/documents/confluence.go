package documents

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// ConfluenceConnector fetches documents from Atlassian Confluence via REST API v2.
type ConfluenceConnector struct {
	baseURL    string // e.g., "https://your-domain.atlassian.net/wiki"
	username   string // Atlassian account email
	apiToken   string // API token (not password)
	httpClient *http.Client
}

// ConfluenceConfig holds configuration for connecting to Confluence.
type ConfluenceConfig struct {
	BaseURL  string
	Username string
	APIToken string
}

// NewConfluenceConnector creates a new Confluence connector.
func NewConfluenceConnector(cfg ConfluenceConfig) *ConfluenceConnector {
	return &ConfluenceConnector{
		baseURL:  strings.TrimRight(cfg.BaseURL, "/"),
		username: cfg.Username,
		apiToken: cfg.APIToken,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *ConfluenceConnector) Name() string { return "confluence" }

// ListDocuments lists pages in a Confluence space using the v2 REST API.
func (c *ConfluenceConnector) ListDocuments(ctx context.Context, space string) ([]ExternalDocument, error) {
	endpoint := fmt.Sprintf("%s/api/v2/spaces?keys=%s", c.baseURL, url.QueryEscape(space))

	// First, get the space ID.
	spaceResp, err := c.doRequest(ctx, endpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to get space %s: %w", space, err)
	}

	var spacesResult struct {
		Results []struct {
			ID   string `json:"id"`
			Key  string `json:"key"`
			Name string `json:"name"`
		} `json:"results"`
	}
	if err := json.Unmarshal(spaceResp, &spacesResult); err != nil {
		return nil, fmt.Errorf("failed to parse space response: %w", err)
	}
	if len(spacesResult.Results) == 0 {
		return nil, fmt.Errorf("space %s not found", space)
	}

	spaceID := spacesResult.Results[0].ID

	// List pages in the space.
	pagesEndpoint := fmt.Sprintf("%s/api/v2/spaces/%s/pages?limit=100&body-format=storage", c.baseURL, spaceID)
	pagesResp, err := c.doRequest(ctx, pagesEndpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to list pages: %w", err)
	}

	var pagesResult struct {
		Results []confluencePageV2 `json:"results"`
	}
	if err := json.Unmarshal(pagesResp, &pagesResult); err != nil {
		return nil, fmt.Errorf("failed to parse pages response: %w", err)
	}

	var docs []ExternalDocument
	for _, page := range pagesResult.Results {
		docs = append(docs, ExternalDocument{
			ID:        page.ID,
			Title:     page.Title,
			SourceURL: fmt.Sprintf("%s/pages/%s", c.baseURL, page.ID),
			Source:    "confluence",
			Metadata: map[string]string{
				"space":  space,
				"status": page.Status,
			},
		})
	}

	return docs, nil
}

// FetchDocument retrieves a single Confluence page by ID and converts its content to markdown.
func (c *ConfluenceConnector) FetchDocument(ctx context.Context, docID string) (*ExternalDocument, error) {
	endpoint := fmt.Sprintf("%s/api/v2/pages/%s?body-format=storage", c.baseURL, url.PathEscape(docID))
	body, err := c.doRequest(ctx, endpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch page %s: %w", docID, err)
	}

	var page confluencePageV2
	if err := json.Unmarshal(body, &page); err != nil {
		return nil, fmt.Errorf("failed to parse page response: %w", err)
	}

	content := ""
	if page.Body.Storage.Value != "" {
		content = confluenceStorageToMarkdown(page.Body.Storage.Value)
	}

	updatedAt := time.Time{}
	if page.Version.CreatedAt != "" {
		updatedAt, _ = time.Parse(time.RFC3339, page.Version.CreatedAt)
	}

	return &ExternalDocument{
		ID:        page.ID,
		Title:     page.Title,
		Content:   content,
		SourceURL: fmt.Sprintf("%s/pages/%s", c.baseURL, page.ID),
		Source:    "confluence",
		UpdatedAt: updatedAt,
		Metadata: map[string]string{
			"status":  page.Status,
			"version": fmt.Sprintf("%d", page.Version.Number),
		},
	}, nil
}

// doRequest performs an authenticated GET request and returns the response body.
func (c *ConfluenceConnector) doRequest(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(c.username, c.apiToken)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("confluence API returned %d: %s", resp.StatusCode, string(body))
	}

	return body, nil
}

// confluencePageV2 represents a Confluence page from the v2 API.
type confluencePageV2 struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
	Body   struct {
		Storage struct {
			Value string `json:"value"`
		} `json:"storage"`
	} `json:"body"`
	Version struct {
		Number    int    `json:"number"`
		CreatedAt string `json:"createdAt"`
	} `json:"version"`
}

// confluenceStorageToMarkdown converts Confluence storage format (XHTML) to markdown.
// This is a simplified converter that handles common elements.
func confluenceStorageToMarkdown(storage string) string {
	s := storage

	// Headers: <h1> through <h6>
	for i := 6; i >= 1; i-- {
		prefix := strings.Repeat("#", i)
		openTag := fmt.Sprintf("<h%d[^>]*>", i)
		closeTag := fmt.Sprintf("</h%d>", i)
		s = regexp.MustCompile(openTag).ReplaceAllString(s, prefix+" ")
		s = strings.ReplaceAll(s, closeTag, "\n\n")
	}

	// Paragraphs
	s = regexp.MustCompile(`<p[^>]*>`).ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "</p>", "\n\n")

	// Bold and italic
	s = regexp.MustCompile(`<strong[^>]*>`).ReplaceAllString(s, "**")
	s = strings.ReplaceAll(s, "</strong>", "**")
	s = regexp.MustCompile(`<em[^>]*>`).ReplaceAllString(s, "*")
	s = strings.ReplaceAll(s, "</em>", "*")

	// Code blocks
	s = regexp.MustCompile(`<ac:structured-macro[^>]*ac:name="code"[^>]*>.*?<ac:plain-text-body><!\[CDATA\[(.*?)\]\]></ac:plain-text-body></ac:structured-macro>`).
		ReplaceAllString(s, "```\n$1\n```\n")

	// Inline code
	s = regexp.MustCompile(`<code[^>]*>`).ReplaceAllString(s, "`")
	s = strings.ReplaceAll(s, "</code>", "`")

	// Lists
	s = regexp.MustCompile(`<ul[^>]*>`).ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "</ul>", "\n")
	s = regexp.MustCompile(`<ol[^>]*>`).ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "</ol>", "\n")
	s = regexp.MustCompile(`<li[^>]*>`).ReplaceAllString(s, "- ")
	s = strings.ReplaceAll(s, "</li>", "\n")

	// Links
	s = regexp.MustCompile(`<a[^>]*href="([^"]*)"[^>]*>(.*?)</a>`).
		ReplaceAllString(s, "[$2]($1)")

	// Images
	s = regexp.MustCompile(`<ac:image[^>]*><ri:url ri:value="([^"]*)"[^/]*/></ac:image>`).
		ReplaceAllString(s, "![]($1)")

	// Tables (simplified)
	s = regexp.MustCompile(`<table[^>]*>`).ReplaceAllString(s, "\n")
	s = strings.ReplaceAll(s, "</table>", "\n")
	s = regexp.MustCompile(`<tr[^>]*>`).ReplaceAllString(s, "| ")
	s = strings.ReplaceAll(s, "</tr>", " |\n")
	s = regexp.MustCompile(`<t[hd][^>]*>`).ReplaceAllString(s, "")
	s = regexp.MustCompile(`</t[hd]>`).ReplaceAllString(s, " | ")
	s = regexp.MustCompile(`<tbody[^>]*>`).ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "</tbody>", "")

	// Strip remaining HTML tags
	s = regexp.MustCompile(`<[^>]+>`).ReplaceAllString(s, "")

	// Clean up HTML entities
	s = strings.ReplaceAll(s, "&amp;", "&")
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&gt;", ">")
	s = strings.ReplaceAll(s, "&quot;", "\"")
	s = strings.ReplaceAll(s, "&#39;", "'")
	s = strings.ReplaceAll(s, "&nbsp;", " ")

	// Normalize whitespace
	s = regexp.MustCompile(`\n{3,}`).ReplaceAllString(s, "\n\n")
	s = strings.TrimSpace(s)

	return s
}
