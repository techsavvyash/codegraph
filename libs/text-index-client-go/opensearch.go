package textindex

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// OpenSearchStore implements TextIndexStore using OpenSearch REST API.
type OpenSearchStore struct {
	baseURL    string
	indexName  string
	httpClient *http.Client
}

// OpenSearchConfig configures the OpenSearch connection.
type OpenSearchConfig struct {
	BaseURL   string // e.g. "http://localhost:9200"
	IndexName string // e.g. "codegraph"
}

// NewOpenSearchStore creates a new OpenSearchStore.
func NewOpenSearchStore(cfg OpenSearchConfig) *OpenSearchStore {
	return &OpenSearchStore{
		baseURL:   strings.TrimRight(cfg.BaseURL, "/"),
		indexName: cfg.IndexName,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// EnsureIndex creates the index with appropriate mappings if it does not exist.
func (s *OpenSearchStore) EnsureIndex(ctx context.Context) error {
	// PUT /<index> with mappings for nodeKey (keyword), content (text), metadata (object)
	mapping := map[string]interface{}{
		"mappings": map[string]interface{}{
			"properties": map[string]interface{}{
				"nodeKey":  map[string]string{"type": "keyword"},
				"content":  map[string]string{"type": "text", "analyzer": "standard"},
				"tenantId": map[string]string{"type": "keyword"},
				"repo":     map[string]string{"type": "keyword"},
				"nodeType": map[string]string{"type": "keyword"},
				"metadata": map[string]string{"type": "object"},
			},
		},
	}
	body, _ := json.Marshal(mapping)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPut,
		s.baseURL+"/"+s.indexName, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("opensearch EnsureIndex: %w", err)
	}
	defer resp.Body.Close()
	// 200 = created, 400 with "resource_already_exists_exception" = ok
	if resp.StatusCode >= 500 {
		return fmt.Errorf("opensearch EnsureIndex: status %d", resp.StatusCode)
	}
	return nil
}

func (s *OpenSearchStore) IndexDocument(ctx context.Context, nodeKey, content string, meta map[string]string) error {
	// PUT /<index>/_doc/<nodeKey>
	doc := map[string]interface{}{
		"nodeKey":  nodeKey,
		"content":  content,
		"metadata": meta,
	}
	if v, ok := meta["tenantId"]; ok {
		doc["tenantId"] = v
	}
	if v, ok := meta["repo"]; ok {
		doc["repo"] = v
	}
	if v, ok := meta["nodeType"]; ok {
		doc["nodeType"] = v
	}

	body, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("%s/%s/_doc/%s", s.baseURL, s.indexName, nodeKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("opensearch IndexDocument: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("opensearch IndexDocument: status %d", resp.StatusCode)
	}
	return nil
}

func (s *OpenSearchStore) IndexDocuments(ctx context.Context, docs []IndexDoc) error {
	// Use _bulk API for efficiency
	var buf bytes.Buffer
	for _, doc := range docs {
		meta := map[string]interface{}{"index": map[string]string{"_index": s.indexName, "_id": doc.NodeKey}}
		metaLine, _ := json.Marshal(meta)
		docBody := map[string]interface{}{"nodeKey": doc.NodeKey, "content": doc.Content, "metadata": doc.Metadata}
		docLine, _ := json.Marshal(docBody)
		buf.Write(metaLine)
		buf.WriteByte('\n')
		buf.Write(docLine)
		buf.WriteByte('\n')
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.baseURL+"/_bulk", &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-ndjson")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("opensearch IndexDocuments bulk: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("opensearch IndexDocuments bulk: status %d", resp.StatusCode)
	}
	return nil
}

func (s *OpenSearchStore) Search(ctx context.Context, query string, opts SearchOpts) ([]TextResult, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = 10
	}

	must := []map[string]interface{}{
		{"multi_match": map[string]interface{}{
			"query":  query,
			"fields": []string{"content", "nodeKey"},
			"type":   "best_fields",
		}},
	}
	filter := []map[string]interface{}{}
	if opts.TenantID != "" {
		filter = append(filter, map[string]interface{}{"term": map[string]string{"tenantId": opts.TenantID}})
	}
	if opts.Repo != "" {
		filter = append(filter, map[string]interface{}{"term": map[string]string{"repo": opts.Repo}})
	}
	if len(opts.NodeTypes) > 0 {
		filter = append(filter, map[string]interface{}{"terms": map[string]interface{}{"nodeType": opts.NodeTypes}})
	}

	searchBody := map[string]interface{}{
		"size": limit,
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must":   must,
				"filter": filter,
			},
		},
		"min_score": opts.MinScore,
	}
	body, err := json.Marshal(searchBody)
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/%s/_search", s.baseURL, s.indexName)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("opensearch Search: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Hits struct {
			Hits []struct {
				ID     string                 `json:"_id"`
				Score  float64                `json:"_score"`
				Source map[string]interface{} `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("opensearch Search decode: %w", err)
	}

	out := make([]TextResult, 0, len(result.Hits.Hits))
	for _, h := range result.Hits.Hits {
		snippet := ""
		if c, ok := h.Source["content"].(string); ok && len(c) > 200 {
			snippet = c[:200]
		} else if c, ok := h.Source["content"].(string); ok {
			snippet = c
		}
		meta := map[string]string{}
		if m, ok := h.Source["metadata"].(map[string]interface{}); ok {
			for k, v := range m {
				if sv, ok := v.(string); ok {
					meta[k] = sv
				}
			}
		}
		out = append(out, TextResult{
			NodeKey:  h.ID,
			Score:    h.Score,
			Snippet:  snippet,
			Metadata: meta,
		})
	}
	return out, nil
}

func (s *OpenSearchStore) Delete(ctx context.Context, nodeKey string) error {
	url := fmt.Sprintf("%s/%s/_doc/%s", s.baseURL, s.indexName, nodeKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return err
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("opensearch Delete: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 && resp.StatusCode != 404 {
		return fmt.Errorf("opensearch Delete: status %d", resp.StatusCode)
	}
	return nil
}

func (s *OpenSearchStore) DeleteByRepo(ctx context.Context, tenantID, repo string) error {
	body := map[string]interface{}{
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"filter": []map[string]interface{}{
					{"term": map[string]string{"tenantId": tenantID}},
					{"term": map[string]string{"repo": repo}},
				},
			},
		},
	}
	b, _ := json.Marshal(body)
	url := fmt.Sprintf("%s/%s/_delete_by_query", s.baseURL, s.indexName)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("opensearch DeleteByRepo: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("opensearch DeleteByRepo: status %d", resp.StatusCode)
	}
	return nil
}

func (s *OpenSearchStore) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.baseURL+"/_cluster/health", nil)
	if err != nil {
		return err
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("opensearch Ping: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("opensearch Ping: status %d", resp.StatusCode)
	}
	return nil
}

func (s *OpenSearchStore) Close() error {
	s.httpClient.CloseIdleConnections()
	return nil
}
