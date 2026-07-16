package semlink

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	models "github.com/context-maximiser/code-graph/internal/model"
	"github.com/context-maximiser/code-graph/internal/model/provenance"
	neo4jdrv "github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

const (
	summarySystemPrompt = "You summarize source code for retrieval. Reply with 1-3 plain sentences describing the purpose and behavior of the code. No preamble, no markdown."
	maxSummaryChars     = 600
	maxBodyBytes        = 16 * 1024 // head+tail window for large bodies
	fileSummaryFanIn    = 30        // symbol summaries fed into a file summary
	serviceFanIn        = 50        // file summaries fed into the service summary
)

// summaryTarget is one node awaiting (re-)summarization.
type summaryTarget struct {
	elementID string
	nodeKey   string
	label     string
	name      string
	signature string
	docstring string
	filePath  string
	startByte int64
	endByte   int64
	oldHash   string
}

// summarizeAll runs the bottom-up pass: exported symbols → files → service.
// Each level is hash-cached: unchanged input costs zero LLM calls.
func (r *Runner) summarizeAll(ctx context.Context, report *Report) error {
	symbols, err := r.loadSymbolTargets(ctx)
	if err != nil {
		return err
	}
	for _, t := range symbols {
		if err := r.summarizeSymbol(ctx, t, report); err != nil {
			return err
		}
	}

	if err := r.summarizeFiles(ctx, report); err != nil {
		return err
	}
	return r.summarizeService(ctx, report)
}

// loadSymbolTargets returns the service's exported, non-test Functions and
// Methods (serviceName-scoped) plus Classes/Interfaces reached through file
// containment (those labels carry no serviceName — see scip indexer notes).
func (r *Runner) loadSymbolTargets(ctx context.Context) ([]summaryTarget, error) {
	var out []summaryTarget

	records, err := r.client.ExecuteQuery(ctx, `
		MATCH (n)
		WHERE (n:Function OR n:Method)
		  AND n.serviceName = $svc AND n.scopeId = $scope
		  AND coalesce(n.isExported, false) AND NOT coalesce(n.isTestFunction, false)
		RETURN elementId(n) AS id, n.nodeKey AS nodeKey, labels(n)[0] AS label,
		       n.name AS name, coalesce(n.signature,'') AS signature,
		       coalesce(n.docstring,'') AS docstring, coalesce(n.filePath,'') AS filePath,
		       coalesce(n.startByte,-1) AS startByte, coalesce(n.endByte,-1) AS endByte,
		       coalesce(n.summaryHash,'') AS oldHash
		ORDER BY n.nodeKey
	`, map[string]any{"svc": r.serviceName, "scope": r.scope.ScopeID})
	if err != nil {
		return nil, fmt.Errorf("failed to load function/method summary targets: %w", err)
	}
	out = append(out, targetsFromRecords(records)...)

	records, err = r.client.ExecuteQuery(ctx, `
		MATCH (f:File {serviceName: $svc, scopeId: $scope})-[:CONTAINS]->(n)
		WHERE (n:Class OR n:Interface) AND n.scopeId = $scope
		RETURN DISTINCT elementId(n) AS id, n.nodeKey AS nodeKey, labels(n)[0] AS label,
		       n.name AS name, coalesce(n.signature,'') AS signature,
		       coalesce(n.docstring,'') AS docstring, coalesce(n.filePath,'') AS filePath,
		       -1 AS startByte, -1 AS endByte,
		       coalesce(n.summaryHash,'') AS oldHash
		ORDER BY nodeKey
	`, map[string]any{"svc": r.serviceName, "scope": r.scope.ScopeID})
	if err != nil {
		return nil, fmt.Errorf("failed to load class/interface summary targets: %w", err)
	}
	out = append(out, targetsFromRecords(records)...)

	return out, nil
}

func targetsFromRecords(records []*neo4jdrv.Record) []summaryTarget {
	var out []summaryTarget
	for _, rec := range records {
		m := rec.AsMap()
		t := summaryTarget{
			elementID: str(m, "id"),
			nodeKey:   str(m, "nodeKey"),
			label:     str(m, "label"),
			name:      str(m, "name"),
			signature: str(m, "signature"),
			docstring: str(m, "docstring"),
			filePath:  str(m, "filePath"),
			oldHash:   str(m, "oldHash"),
		}
		if v, ok := m["startByte"].(int64); ok {
			t.startByte = v
		}
		if v, ok := m["endByte"].(int64); ok {
			t.endByte = v
		}
		out = append(out, t)
	}
	return out
}

func (r *Runner) summarizeSymbol(ctx context.Context, t summaryTarget, report *Report) error {
	input := strings.Join([]string{t.label, t.name, t.signature, t.docstring, r.readBody(t)}, "\n")
	hash := hashString(input)
	if hash == t.oldHash {
		report.SummariesUpToDate++
		return nil
	}

	if !r.spendBudget() {
		report.SkippedBudget++
		return nil
	}

	user := fmt.Sprintf("%s %s\nSignature: %s\nDocstring: %s\n\nCode:\n%s",
		t.label, t.name, t.signature, t.docstring, r.readBody(t))
	summary, err := r.completer.Complete(ctx, summarySystemPrompt, user)
	if err != nil {
		return fmt.Errorf("failed to summarize %s: %w", t.nodeKey, err)
	}

	return r.writeSummary(ctx, t.elementID, t.nodeKey, clampSummary(summary), hash, report)
}

// summarizeFiles builds file-level summaries from the file's symbol summaries.
func (r *Runner) summarizeFiles(ctx context.Context, report *Report) error {
	records, err := r.client.ExecuteQuery(ctx, `
		MATCH (f:File {serviceName: $svc, scopeId: $scope})-[:CONTAINS]->(n)
		WHERE n.summary IS NOT NULL
		WITH f, n ORDER BY n.nodeKey
		WITH f, collect(n.name + ': ' + n.summary)[0..$fanIn] AS parts
		WHERE size(parts) > 0
		RETURN elementId(f) AS id, f.nodeKey AS nodeKey, f.path AS path,
		       parts, coalesce(f.summaryHash,'') AS oldHash
		ORDER BY nodeKey
	`, map[string]any{"svc": r.serviceName, "scope": r.scope.ScopeID, "fanIn": fileSummaryFanIn})
	if err != nil {
		return fmt.Errorf("failed to load file summary inputs: %w", err)
	}

	for _, rec := range records {
		m := rec.AsMap()
		parts := stringList(m["parts"])
		input := strings.Join(parts, "\n")
		hash := hashString(input)
		if hash == str(m, "oldHash") {
			report.SummariesUpToDate++
			continue
		}
		if !r.spendBudget() {
			report.SkippedBudget++
			continue
		}

		user := fmt.Sprintf("File %s contains:\n%s\n\nSummarize what this file is responsible for.", str(m, "path"), input)
		summary, err := r.completer.Complete(ctx, summarySystemPrompt, user)
		if err != nil {
			return fmt.Errorf("failed to summarize file %s: %w", str(m, "nodeKey"), err)
		}
		if err := r.writeSummary(ctx, str(m, "id"), str(m, "nodeKey"), clampSummary(summary), hash, report); err != nil {
			return err
		}
	}
	return nil
}

// summarizeService builds one service-level summary from file summaries. The
// Service node is summarized for context but not embedded (no service vector
// index — a doc chunk matching a whole service is not a useful edge).
func (r *Runner) summarizeService(ctx context.Context, report *Report) error {
	records, err := r.client.ExecuteQuery(ctx, `
		MATCH (s:Service {nodeKey: $key, scopeId: $scope})
		OPTIONAL MATCH (s)-[:CONTAINS]->(f:File)
		WHERE f.summary IS NOT NULL
		WITH s, f ORDER BY f.nodeKey
		WITH s, collect(f.path + ': ' + f.summary)[0..$fanIn] AS parts
		RETURN elementId(s) AS id, s.nodeKey AS nodeKey, parts,
		       coalesce(s.summaryHash,'') AS oldHash
	`, map[string]any{"key": models.ServiceNodeKey(r.serviceName), "scope": r.scope.ScopeID, "fanIn": serviceFanIn})
	if err != nil {
		return fmt.Errorf("failed to load service summary input: %w", err)
	}
	if len(records) == 0 {
		return nil
	}

	m := records[0].AsMap()
	parts := stringList(m["parts"])
	if len(parts) == 0 {
		return nil
	}
	input := strings.Join(parts, "\n")
	hash := hashString(input)
	if hash == str(m, "oldHash") {
		report.SummariesUpToDate++
		return nil
	}
	if !r.spendBudget() {
		report.SkippedBudget++
		return nil
	}

	user := fmt.Sprintf("Service %s files:\n%s\n\nSummarize this service's purpose.", r.serviceName, input)
	summary, err := r.completer.Complete(ctx, summarySystemPrompt, user)
	if err != nil {
		return fmt.Errorf("failed to summarize service: %w", err)
	}
	return r.writeSummary(ctx, str(m, "id"), str(m, "nodeKey"), clampSummary(summary), hash, report)
}

// writeSummary persists summary props and clears the node's embedding (the
// old vector described the old summary). Props are validated through the
// GeneratedDoc provenance contract before the write.
func (r *Runner) writeSummary(ctx context.Context, elementID, nodeKey, summary, hash string, report *Report) error {
	now := time.Now().UTC().Format(time.RFC3339)
	if err := provenance.ValidateDocProps(map[string]any{
		"type":      "code-summary",
		"sourceKey": nodeKey,
		"createdAt": now,
		"strategy":  "semlink-summary/" + r.summaryModelName(),
	}); err != nil {
		return fmt.Errorf("summary provenance invalid for %s: %w", nodeKey, err)
	}

	_, err := r.client.ExecuteQuery(ctx, `
		MATCH (n) WHERE elementId(n) = $id
		SET n.summary = $summary, n.summaryHash = $hash,
		    n.summaryModel = $model, n.summaryAt = $now
		REMOVE n.embedding, n.embeddingModel
	`, map[string]any{
		"id": elementID, "summary": summary, "hash": hash,
		"model": r.summaryModelName(), "now": now,
	})
	if err != nil {
		return fmt.Errorf("failed to write summary for %s: %w", nodeKey, err)
	}
	report.SummariesWritten++
	return nil
}

// summaryModelName identifies what generated summaries; the completer has no
// self-describing interface, so the embedder's model namespace is reused only
// when nothing better exists.
func (r *Runner) summaryModelName() string {
	type modelNamer interface{ Model() string }
	if mn, ok := r.completer.(modelNamer); ok {
		return mn.Model()
	}
	return "completer"
}

// readBody best-effort reads the symbol's body via RFC-010 byte anchors,
// relative to projectRoot. Missing root/file/anchors degrade to "" —
// signatures and docstrings still summarize usefully.
func (r *Runner) readBody(t summaryTarget) string {
	if r.projectRoot == "" || t.filePath == "" || t.startByte < 0 || t.endByte <= t.startByte {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(r.projectRoot, t.filePath))
	if err != nil || int64(len(data)) < t.endByte {
		return ""
	}
	body := data[t.startByte:t.endByte]
	if len(body) > maxBodyBytes {
		half := maxBodyBytes / 2
		return string(body[:half]) + "\n…\n" + string(body[len(body)-half:])
	}
	return string(body)
}

func clampSummary(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > maxSummaryChars {
		s = s[:maxSummaryChars]
	}
	return s
}

func hashString(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func str(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

func stringList(v any) []string {
	items, _ := v.([]any)
	out := make([]string, 0, len(items))
	for _, it := range items {
		if s, ok := it.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
