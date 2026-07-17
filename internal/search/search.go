package search

import (
	"context"
	"encoding/base64"
	"fmt"
	"sort"
	"strconv"
	"strings"

	neo4j "github.com/context-maximiser/code-graph/internal/graph"
	"github.com/context-maximiser/code-graph/internal/graph/schema"
)

// Searcher performs fulltext search using Neo4j FULLTEXT indexes with RRF fusion.
type Searcher struct {
	client   *neo4j.Client
	embedder Embedder
}

// Embedder is the subset of llm.Embedder that semantic search needs (declared
// locally so internal/search does not depend on internal/llm).
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// NewSearcher creates a new Searcher instance.
func NewSearcher(client *neo4j.Client) *Searcher {
	return &Searcher{client: client}
}

// SetEmbedder enables semantic search (Options.Semantic). Without one,
// semantic requests return a configuration error.
func (s *Searcher) SetEmbedder(e Embedder) { s.embedder = e }

// Options configures a search request.
type Options struct {
	Labels  []string // Which labels to search (validated against schema.GetFulltextIndexes)
	ScopeID string   // Overlay scope (e.g., "main", "pr-42"). If empty, default to "main".
	Service string   // Optional: filter by serviceName
	Limit   int      // Max results per label; fused limit is min(Limit, maxCursor)
	Cursor  string   // Keyset pagination cursor (base64-encoded "score:nodeID")
	// Semantic adds a vector-kNN ranked list (doc chunks + code summaries,
	// RFC-011) to the RRF fusion. Requires SetEmbedder.
	Semantic bool
}

// Result represents a single search result.
type Result struct {
	NodeID    string  `json:"node_id"` // elementId
	NodeKey   string  `json:"node_key"`
	Label     string  `json:"label"`
	Name      string  `json:"name"`                 // primary name (varies by label: name, path, displayName, etc.)
	Signature string  `json:"signature"`            // for Function/Method; empty for others
	FilePath  string  `json:"file_path"`            // for File; empty for others
	Service   string  `json:"service"`              // serviceName from node
	StartLine int     `json:"start_line,omitempty"` // 1-based; 0 when the node has no location
	EndLine   int     `json:"end_line,omitempty"`   // 1-based inclusive; exact for rangeSource=treesitter/go-ast nodes
	Score     float64 `json:"score"`                // fused RRF score
}

// Response contains the results and pagination cursor.
type Response struct {
	Results    []Result `json:"results"`
	NextCursor string   `json:"next_cursor,omitempty"` // base64 cursor for next page; empty if no more results
}

// indexResult holds the raw result from one index before fusion.
type indexResult struct {
	Label     string
	NodeID    string
	NodeKey   string
	Name      string
	Signature string
	FilePath  string
	Service   string
	StartLine int
	EndLine   int
	Score     float64
}

// Search executes a fulltext search across the requested labels, fuses results with RRF,
// applies exact-match boost, and returns paginated results.
func (s *Searcher) Search(ctx context.Context, query string, opts Options) (*Response, error) {
	// Defaults
	if opts.Limit <= 0 {
		opts.Limit = 20
	}
	if opts.ScopeID == "" {
		opts.ScopeID = "main"
	}

	// Determine labels to search: use provided or all seven fulltext indexes
	labelsToSearch := opts.Labels
	if len(labelsToSearch) == 0 {
		for _, idx := range schema.GetFulltextIndexes() {
			labelsToSearch = append(labelsToSearch, idx.NodeLabel)
		}
	}

	// Validate labels against schema
	validLabelsMap := make(map[string]bool)
	for _, idx := range schema.GetFulltextIndexes() {
		validLabelsMap[idx.NodeLabel] = true
	}
	for _, label := range labelsToSearch {
		if !validLabelsMap[label] {
			validLabelsList := make([]string, 0, len(validLabelsMap))
			for k := range validLabelsMap {
				validLabelsList = append(validLabelsList, k)
			}
			sort.Strings(validLabelsList)
			return nil, fmt.Errorf("invalid label %q; valid labels: %v", label, validLabelsList)
		}
	}

	// Map label -> indexName
	indexMap := make(map[string]string)
	for _, idx := range schema.GetFulltextIndexes() {
		indexMap[idx.NodeLabel] = idx.Name
	}

	// Escape the user query for Lucene syntax: remove special chars and wrap in quotes.
	escapedQuery := escapeLuceneQuery(query)

	// Per-index cap: max(200, Limit*10) to have room for fusion and pagination.
	perIndexCap := opts.Limit * 10
	if perIndexCap < 200 {
		perIndexCap = 200
	}

	// Fetch results from each index
	// Accumulate per-label results
	allResults := make(map[string][]indexResult) // label -> sorted results
	for _, label := range labelsToSearch {
		indexName := indexMap[label]
		results, err := s.queryIndex(ctx, indexName, label, escapedQuery, opts.ScopeID, opts.Service, perIndexCap)
		if err != nil {
			// Log but continue; one index failure shouldn't fail the whole search.
			fmt.Printf("Warning: index query %s failed: %v\n", indexName, err)
			continue
		}
		allResults[label] = results
	}

	// Semantic mode: one additional RRF list ranked by vector similarity
	// (doc chunks + code summaries in the same embedding space, RFC-011 §7).
	// Unlike a per-index failure, a missing embedder is a configuration error
	// the caller asked about explicitly — fail loudly, not silently weaker.
	if opts.Semantic {
		if s.embedder == nil {
			return nil, fmt.Errorf("semantic search requires an embedding provider (configure llm.embedding)")
		}
		semResults, err := s.querySemantic(ctx, query, opts.ScopeID, opts.Service, opts.Limit)
		if err != nil {
			return nil, fmt.Errorf("semantic search failed: %w", err)
		}
		allResults["__semantic__"] = semResults
	}

	// RRF fusion: accumulate scores across labels and deduplicate by nodeID
	type rrfEntry struct {
		result     Result
		rrfScore   float64
		labelRanks map[string]int // label -> rank (for debugging)
	}
	byNodeID := make(map[string]*rrfEntry)

	for label, results := range allResults {
		for rank, res := range results {
			// rank is 0-indexed; RRF uses 1-indexed ranks
			rrfContrib := rrfScoreSingle(rank + 1)

			entry, ok := byNodeID[res.NodeID]
			if !ok {
				entry = &rrfEntry{
					result: Result{
						NodeID:    res.NodeID,
						NodeKey:   res.NodeKey,
						Label:     res.Label,
						Name:      res.Name,
						Signature: res.Signature,
						FilePath:  res.FilePath,
						Service:   res.Service,
						StartLine: res.StartLine,
						EndLine:   res.EndLine,
						Score:     0,
					},
					labelRanks: make(map[string]int),
				}
				byNodeID[res.NodeID] = entry
			}

			// Update with fresher node data if available
			if res.Name != "" && entry.result.Name == "" {
				entry.result.Name = res.Name
			}
			if res.Signature != "" && entry.result.Signature == "" {
				entry.result.Signature = res.Signature
			}
			if res.FilePath != "" && entry.result.FilePath == "" {
				entry.result.FilePath = res.FilePath
			}
			if res.Service != "" && entry.result.Service == "" {
				entry.result.Service = res.Service
			}

			// Accumulate RRF
			entry.rrfScore += rrfContrib
			entry.labelRanks[label] = rank + 1
		}
	}

	// Apply exact-match boost: if name or path exactly matches query (case-insensitive),
	// add a large bonus so exact hits always rank first.
	queryLower := strings.ToLower(query)
	exactMatchBonus := 10.0 // Empirically chosen to always exceed fused RRF scores
	for _, entry := range byNodeID {
		if nameLower := strings.ToLower(entry.result.Name); nameLower == queryLower {
			entry.rrfScore += exactMatchBonus
		} else if pathLower := strings.ToLower(entry.result.FilePath); pathLower == queryLower {
			entry.rrfScore += exactMatchBonus
		}
	}

	// Build result slice and sort by RRF score descending, then Name, then NodeID
	fused := make([]Result, 0, len(byNodeID))
	for _, entry := range byNodeID {
		entry.result.Score = entry.rrfScore
		fused = append(fused, entry.result)
	}

	sort.Slice(fused, func(i, j int) bool { return sortLess(fused[i], fused[j]) })

	// Keyset pagination: resume strictly after the cursor's sort key.
	var startIdx int
	if opts.Cursor != "" {
		idx, err := decodeCursorStart(fused, opts.Cursor)
		if err != nil {
			return nil, fmt.Errorf("bad cursor: %w", err)
		}
		startIdx = idx
	}

	// Slice results to [startIdx, startIdx+Limit)
	endIdx := startIdx + opts.Limit
	if endIdx > len(fused) {
		endIdx = len(fused)
	}
	pageResults := fused[startIdx:endIdx]

	// Generate next cursor if more results remain
	nextCursor := ""
	if endIdx < len(fused) && len(pageResults) > 0 {
		nextCursor = encodeCursor(pageResults[len(pageResults)-1])
	}

	return &Response{
		Results:    pageResults,
		NextCursor: nextCursor,
	}, nil
}

// queryIndex issues a CALL db.index.fulltext.queryNodes call for a single index.
func (s *Searcher) queryIndex(ctx context.Context, indexName string, label string, escapedQuery string, scopeID string, serviceFilter string, limit int) ([]indexResult, error) {
	// Build the Cypher query:
	// CALL db.index.fulltext.queryNodes($indexName, $escapedQuery) YIELD node, score
	// WHERE (node.scopeId = $scopeId OR node.scopeId = 'main') AND (serviceFilter condition)
	// RETURN elementId(node), node.nodeKey, node.name/path/displayName/signature, node.serviceName, score
	// ORDER BY score DESC
	// LIMIT $limit

	whereClause := "(node.scopeId = $scopeId OR node.scopeId = 'main')"
	if serviceFilter != "" {
		whereClause += " AND node.serviceName = $service"
	}

	// The "primary name" property varies by label: File is indexed on path,
	// Symbol's display string is displayName. Signature only exists on
	// Function/Method. Every column needs an explicit AS — without it the
	// record key is the literal expression ("node.nodeKey"), not "nodeKey".
	nameExpr := "node.name"
	signatureExpr := "''"
	switch label {
	case "Function", "Method":
		signatureExpr = "COALESCE(node.signature, '')"
	case "Symbol":
		nameExpr = "COALESCE(node.displayName, node.name, '')"
	case "File":
		nameExpr = "node.path"
	case "Document":
		nameExpr = "COALESCE(node.title, node.sourceUrl, '')"
	case "DocumentChunk":
		nameExpr = "COALESCE(node.headingPath, node.nodeKey, '')"
	}

	query := fmt.Sprintf(`
CALL db.index.fulltext.queryNodes($indexName, $escapedQuery) YIELD node, score
WHERE %s
RETURN elementId(node) AS nodeID,
       COALESCE(node.nodeKey, '') AS nodeKey,
       COALESCE(%s, '') AS name,
       %s AS signature,
       COALESCE(node.serviceName, '') AS serviceName,
       COALESCE(node.startLine, 0) AS startLine,
       COALESCE(node.endLine, 0) AS endLine,
       score
ORDER BY score DESC
LIMIT $limit
`, whereClause, nameExpr, signatureExpr)

	params := map[string]interface{}{
		"indexName":    indexName,
		"escapedQuery": escapedQuery,
		"scopeId":      scopeID,
		"limit":        limit,
	}
	if serviceFilter != "" {
		params["service"] = serviceFilter
	}

	rows, err := s.client.ExecuteQuery(ctx, query, params)
	if err != nil {
		return nil, fmt.Errorf("fulltext query failed: %w", err)
	}

	var results []indexResult
	for _, row := range rows {
		m := row.AsMap()
		nodeID, _ := m["nodeID"].(string)
		nodeKey, _ := m["nodeKey"].(string)
		name, _ := m["name"].(string)
		signature, _ := m["signature"].(string)
		serviceName, _ := m["serviceName"].(string)
		score := 0.0
		if s, ok := m["score"].(float64); ok {
			score = s
		}

		// Only include if nodeID is present (safety check)
		if nodeID != "" {
			filePath := ""
			if label == "File" {
				filePath = name // File's indexed "name" IS its path
			}
			results = append(results, indexResult{
				Label:     label,
				NodeID:    nodeID,
				NodeKey:   nodeKey,
				Name:      name,
				Signature: signature,
				FilePath:  filePath,
				Service:   serviceName,
				StartLine: intFromRecord(m, "startLine"),
				EndLine:   intFromRecord(m, "endLine"),
				Score:     score,
			})
		}
	}

	return results, nil
}

// querySemantic embeds the query and ranks nodes across all RFC-011 vector
// indexes (doc chunks + code summaries) by raw cosine similarity, returning
// one list for RRF fusion. Vector indexes that don't exist yet (no Layer S
// run) fail per-index and are skipped, mirroring fulltext behavior.
func (s *Searcher) querySemantic(ctx context.Context, query, scopeID, service string, limit int) ([]indexResult, error) {
	vecs, err := s.embedder.Embed(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("failed to embed query: %w", err)
	}
	if len(vecs) != 1 {
		return nil, fmt.Errorf("embedder returned %d vectors for 1 input", len(vecs))
	}
	vector := make([]float64, len(vecs[0]))
	for i, x := range vecs[0] {
		vector[i] = float64(x)
	}

	k := limit * 2
	if k < 20 {
		k = 20
	}

	whereClause := "(node.scopeId = $scopeId OR node.scopeId = 'main')"
	params := map[string]any{"scopeId": scopeID, "k": k, "vector": vector}
	if service != "" {
		whereClause += " AND node.serviceName = $service"
		params["service"] = service
	}

	var all []indexResult
	for _, idx := range schema.GetVectorIndexes() {
		params["index"] = idx.Name
		rows, err := s.client.ExecuteQuery(ctx, fmt.Sprintf(`
CALL db.index.vector.queryNodes($index, $k, $vector) YIELD node, score
WHERE %s
RETURN elementId(node) AS nodeID,
       COALESCE(node.nodeKey, '') AS nodeKey,
       labels(node)[0] AS label,
       COALESCE(node.name, node.path, node.headingPath, node.title, '') AS name,
       COALESCE(node.signature, '') AS signature,
       COALESCE(node.serviceName, '') AS serviceName,
       COALESCE(node.startLine, 0) AS startLine,
       COALESCE(node.endLine, 0) AS endLine,
       score
ORDER BY score DESC
`, whereClause), params)
		if err != nil {
			// Index may not exist before the first Layer S run — skip like a
			// failed fulltext index rather than failing the whole search.
			fmt.Printf("Warning: vector query %s failed: %v\n", idx.Name, err)
			continue
		}
		for _, row := range rows {
			m := row.AsMap()
			nodeID, _ := m["nodeID"].(string)
			if nodeID == "" {
				continue
			}
			score, _ := m["score"].(float64)
			label, _ := m["label"].(string)
			name, _ := m["name"].(string)
			filePath := ""
			if label == "File" {
				filePath = name
			}
			all = append(all, indexResult{
				Label:     label,
				NodeID:    nodeID,
				NodeKey:   str(m, "nodeKey"),
				Name:      name,
				Signature: str(m, "signature"),
				FilePath:  filePath,
				Service:   str(m, "serviceName"),
				StartLine: intFromRecord(m, "startLine"),
				EndLine:   intFromRecord(m, "endLine"),
				Score:     2*score - 1, // Neo4j reports (cos+1)/2; keep raw cosine
			})
		}
	}

	// One globally ranked list: RRF consumes rank order, so the cross-index
	// merge must be sorted before fusion.
	sort.Slice(all, func(i, j int) bool {
		if all[i].Score != all[j].Score {
			return all[i].Score > all[j].Score
		}
		return all[i].NodeID < all[j].NodeID
	})
	if len(all) > k {
		all = all[:k]
	}
	return all, nil
}

func str(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

// intFromRecord reads an integer column from a Neo4j record map (the driver
// returns int64; anything absent or non-integer yields 0).
func intFromRecord(m map[string]any, key string) int {
	if v, ok := m[key].(int64); ok {
		return int(v)
	}
	return 0
}

// rrfScoreSingle returns the RRF contribution for a single rank (1-indexed).
// Standard constant k=60.
func rrfScoreSingle(rank int) float64 {
	return 1.0 / (60.0 + float64(rank))
}

// escapeLuceneQuery escapes special Lucene characters so arbitrary user text
// can never be interpreted as Lucene syntax. Single-token queries get a
// trailing * for prefix matching — the wildcard must stay OUTSIDE quotes,
// because Lucene treats * inside a quoted phrase as a literal character
// (a quoted "foo*" matches nothing useful). Multi-word queries become a
// quoted phrase with no wildcard.
func escapeLuceneQuery(query string) string {
	var b strings.Builder
	for _, r := range query {
		switch r {
		case '+', '-', '!', '(', ')', '{', '}', '[', ']', '^', '"', '~', '*', '?', ':', '\\', '/', '&', '|':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	escaped := b.String()
	if strings.ContainsAny(query, " \t\n") {
		return "\"" + escaped + "\""
	}
	if escaped == "" {
		return escaped
	}
	return escaped + "*"
}

// cursorSep separates cursor fields. NUL cannot appear in scores, names from
// source code, or element IDs, so splitting on it is unambiguous (":" is not —
// element IDs contain colons).
const cursorSep = "\x00"

// encodeCursor encodes the full sort key (score, name, nodeID) of the last
// returned result. Carrying the whole key lets the next page resume by
// comparison instead of by finding the identical row again, so a row deleted
// between pages shifts the boundary instead of resetting to page one.
func encodeCursor(r Result) string {
	cursor := strconv.FormatFloat(r.Score, 'g', -1, 64) + cursorSep + r.Name + cursorSep + r.NodeID
	return base64.StdEncoding.EncodeToString([]byte(cursor))
}

// decodeCursorStart decodes a cursor and returns the index of the first result
// that sorts strictly AFTER the cursor's key under sortLess. Malformed cursors
// return an error; the caller decides the fallback.
func decodeCursorStart(results []Result, encodedCursor string) (int, error) {
	decoded, err := base64.StdEncoding.DecodeString(encodedCursor)
	if err != nil {
		return 0, fmt.Errorf("invalid cursor: %w", err)
	}
	parts := strings.SplitN(string(decoded), cursorSep, 3)
	if len(parts) != 3 {
		return 0, fmt.Errorf("malformed cursor: expected 3 fields")
	}
	score, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return 0, fmt.Errorf("malformed cursor score: %w", err)
	}
	key := Result{Score: score, Name: parts[1], NodeID: parts[2]}

	// results is sorted by sortLess; the page starts at the first element
	// strictly after the cursor key.
	return sort.Search(len(results), func(i int) bool {
		return sortLess(key, results[i])
	}), nil
}

// sortLess is THE result ordering: fused score descending, then name, then
// NodeID ascending. Pagination correctness depends on the fusion sort and the
// cursor comparison using this exact same function.
func sortLess(a, b Result) bool {
	if a.Score != b.Score {
		return a.Score > b.Score
	}
	if a.Name != b.Name {
		return a.Name < b.Name
	}
	return a.NodeID < b.NodeID
}
