package generated

import (
	"context"
	"fmt"

	"github.com/context-maximiser/code-graph/libs/core-models-go"
	"github.com/context-maximiser/code-graph/libs/neo4j-go"
)

// GeneratedDocType constants for the different types of generated docs.
const (
	DocTypePRSummary          = "pr_summary"
	DocTypeFlowSummary        = "flow_summary"
	DocTypeDocstringSuggestion = "docstring_suggestion"
)

// ContextGenerator creates and manages generated context nodes in the graph.
type ContextGenerator struct {
	client   *neo4j.Client
	scopeCtx models.ScopeContext
}

// NewContextGenerator creates a new context generator.
func NewContextGenerator(client *neo4j.Client) *ContextGenerator {
	return &ContextGenerator{
		client:   client,
		scopeCtx: models.DefaultScope(),
	}
}

// SetScope sets the scope context.
func (g *ContextGenerator) SetScope(scope models.ScopeContext) {
	g.scopeCtx = scope
}

// CreatePullRequestNode creates a PullRequest node in the graph.
func (g *ContextGenerator) CreatePullRequestNode(ctx context.Context, prID, title, author, baseBranch, headBranch, description string) (string, error) {
	nodeKey := models.PullRequestNodeKey(prID)
	props := map[string]any{
		"prId":        prID,
		"title":       title,
		"author":      author,
		"baseBranch":  baseBranch,
		"headBranch":  headBranch,
		"status":      "open",
		"description": description,
		"nodeKey":     nodeKey,
		"scope":       g.scopeCtx.Scope,
		"scopeId":     g.scopeCtx.ScopeID,
	}

	id, err := g.client.MergeNode(ctx, []string{"PullRequest"},
		map[string]any{"nodeKey": nodeKey, "scopeId": g.scopeCtx.ScopeID}, props)
	if err != nil {
		return "", fmt.Errorf("failed to create PullRequest node: %w", err)
	}
	return id, nil
}

// StorePRSummary creates a GeneratedDoc node with type "pr_summary" and links it
// to the PullRequest via DOCUMENTS, and to changed files via DERIVED_FROM.
func (g *ContextGenerator) StorePRSummary(ctx context.Context, prID, title, content, model string, changedFileKeys []string) (string, error) {
	prNodeKey := models.PullRequestNodeKey(prID)
	genDocKey := models.GeneratedDocNodeKey(DocTypePRSummary, prNodeKey)

	docProps := map[string]any{
		"type":       DocTypePRSummary,
		"title":      title,
		"content":    content,
		"model":      model,
		"sourceType": "pull_request",
		"sourceKey":  prNodeKey,
		"nodeKey":    genDocKey,
		"scope":      g.scopeCtx.Scope,
		"scopeId":    g.scopeCtx.ScopeID,
	}

	docID, err := g.client.MergeNode(ctx, []string{"GeneratedDoc"},
		map[string]any{"nodeKey": genDocKey, "scopeId": g.scopeCtx.ScopeID}, docProps)
	if err != nil {
		return "", fmt.Errorf("failed to create GeneratedDoc node: %w", err)
	}

	// Link GeneratedDoc -[DOCUMENTS]-> PullRequest
	cypher := `
		MATCH (gd:GeneratedDoc {nodeKey: $genDocKey, scopeId: $scopeId})
		MATCH (pr:PullRequest {nodeKey: $prNodeKey, scopeId: $scopeId})
		MERGE (gd)-[:DOCUMENTS]->(pr)`
	_, err = g.client.ExecuteQuery(ctx, cypher, map[string]any{
		"genDocKey": genDocKey,
		"prNodeKey": prNodeKey,
		"scopeId":   g.scopeCtx.ScopeID,
	})
	if err != nil {
		fmt.Printf("Warning: failed to create DOCUMENTS edge: %v\n", err)
	}

	// Link GeneratedDoc -[DERIVED_FROM]-> changed Files (batch)
	if len(changedFileKeys) > 0 {
		cypher = `
			UNWIND $fileKeys AS fk
			MATCH (gd:GeneratedDoc {nodeKey: $genDocKey, scopeId: $scopeId})
			MATCH (f:File {nodeKey: fk})
			MERGE (gd)-[:DERIVED_FROM]->(f)`
		_, err = g.client.ExecuteQuery(ctx, cypher, map[string]any{
			"genDocKey": genDocKey,
			"fileKeys":  changedFileKeys,
			"scopeId":   g.scopeCtx.ScopeID,
		})
		if err != nil {
			fmt.Printf("Warning: failed to create DERIVED_FROM edges: %v\n", err)
		}
	}

	return docID, nil
}

// StoreFlowSummary creates a GeneratedDoc node with type "flow_summary" and links it
// to the Flow via DOCUMENTS and DERIVED_FROM.
func (g *ContextGenerator) StoreFlowSummary(ctx context.Context, flowNodeKey, title, content, model string) (string, error) {
	genDocKey := models.GeneratedDocNodeKey(DocTypeFlowSummary, flowNodeKey)

	docProps := map[string]any{
		"type":       DocTypeFlowSummary,
		"title":      title,
		"content":    content,
		"model":      model,
		"sourceType": "flow",
		"sourceKey":  flowNodeKey,
		"nodeKey":    genDocKey,
		"scope":      g.scopeCtx.Scope,
		"scopeId":    g.scopeCtx.ScopeID,
	}

	docID, err := g.client.MergeNode(ctx, []string{"GeneratedDoc"},
		map[string]any{"nodeKey": genDocKey, "scopeId": g.scopeCtx.ScopeID}, docProps)
	if err != nil {
		return "", fmt.Errorf("failed to create GeneratedDoc node: %w", err)
	}

	// Link GeneratedDoc -[DOCUMENTS]-> Flow
	cypher := `
		MATCH (gd:GeneratedDoc {nodeKey: $genDocKey, scopeId: $scopeId})
		MATCH (f:Flow {nodeKey: $flowKey})
		MERGE (gd)-[:DOCUMENTS]->(f)
		MERGE (gd)-[:DERIVED_FROM]->(f)`
	_, err = g.client.ExecuteQuery(ctx, cypher, map[string]any{
		"genDocKey": genDocKey,
		"flowKey":   flowNodeKey,
		"scopeId":   g.scopeCtx.ScopeID,
	})
	if err != nil {
		fmt.Printf("Warning: failed to create edges to Flow: %v\n", err)
	}

	return docID, nil
}

// StoreDocstringSuggestion creates a GeneratedDoc node with type "docstring_suggestion"
// and links it to the target function/method via DOCUMENTS and DERIVED_FROM.
func (g *ContextGenerator) StoreDocstringSuggestion(ctx context.Context, targetNodeKey, title, content, model string) (string, error) {
	genDocKey := models.GeneratedDocNodeKey(DocTypeDocstringSuggestion, targetNodeKey)

	docProps := map[string]any{
		"type":       DocTypeDocstringSuggestion,
		"title":      title,
		"content":    content,
		"model":      model,
		"sourceType": "code_symbol",
		"sourceKey":  targetNodeKey,
		"nodeKey":    genDocKey,
		"scope":      g.scopeCtx.Scope,
		"scopeId":    g.scopeCtx.ScopeID,
	}

	docID, err := g.client.MergeNode(ctx, []string{"GeneratedDoc"},
		map[string]any{"nodeKey": genDocKey, "scopeId": g.scopeCtx.ScopeID}, docProps)
	if err != nil {
		return "", fmt.Errorf("failed to create GeneratedDoc node: %w", err)
	}

	// Link to target node (Function or Method)
	cypher := `
		MATCH (gd:GeneratedDoc {nodeKey: $genDocKey, scopeId: $scopeId})
		MATCH (target {nodeKey: $targetKey})
		WHERE target:Function OR target:Method
		WITH gd, target LIMIT 1
		MERGE (gd)-[:DOCUMENTS]->(target)
		MERGE (gd)-[:DERIVED_FROM]->(target)`
	_, err = g.client.ExecuteQuery(ctx, cypher, map[string]any{
		"genDocKey": genDocKey,
		"targetKey": targetNodeKey,
		"scopeId":   g.scopeCtx.ScopeID,
	})
	if err != nil {
		fmt.Printf("Warning: failed to create edges to target: %v\n", err)
	}

	return docID, nil
}

// ListGeneratedDocs lists generated docs, optionally filtered by type and/or source.
func (g *ContextGenerator) ListGeneratedDocs(ctx context.Context, docType, sourceKey string) ([]map[string]any, error) {
	conditions := []string{"gd.scopeId = $scopeId"}
	params := map[string]any{"scopeId": g.scopeCtx.ScopeID}

	if docType != "" {
		conditions = append(conditions, "gd.type = $type")
		params["type"] = docType
	}
	if sourceKey != "" {
		conditions = append(conditions, "gd.sourceKey = $sourceKey")
		params["sourceKey"] = sourceKey
	}

	where := ""
	for i, c := range conditions {
		if i == 0 {
			where = "WHERE " + c
		} else {
			where += " AND " + c
		}
	}

	cypher := fmt.Sprintf(`MATCH (gd:GeneratedDoc) %s
		RETURN gd.nodeKey AS nodeKey, gd.type AS type, gd.title AS title,
		       gd.sourceKey AS sourceKey, gd.model AS model
		ORDER BY gd.title`, where)

	records, err := g.client.ExecuteQuery(ctx, cypher, params)
	if err != nil {
		return nil, fmt.Errorf("failed to list generated docs: %w", err)
	}

	var results []map[string]any
	for _, r := range records {
		results = append(results, r.AsMap())
	}
	return results, nil
}

// GenerateDocstringSuggestionsForScope queries for exported Functions/Methods in
// the current scope that lack a docstring and creates GeneratedDoc suggestions.
// Returns the number of suggestions created.
func (g *ContextGenerator) GenerateDocstringSuggestionsForScope(ctx context.Context) (int, error) {
	cypher := `
		MATCH (n)
		WHERE (n:Function OR n:Method)
		  AND n.scopeId = $scopeId
		  AND n.isExported = true
		  AND (n.docstring IS NULL OR n.docstring = '')
		RETURN n.nodeKey AS nodeKey, n.name AS name, n.signature AS signature
		LIMIT 50
	`
	records, err := g.client.ExecuteQuery(ctx, cypher, map[string]any{
		"scopeId": g.scopeCtx.ScopeID,
	})
	if err != nil {
		return 0, fmt.Errorf("failed to query exported symbols without docstrings: %w", err)
	}

	created := 0
	for _, r := range records {
		m := r.AsMap()
		nk, _ := m["nodeKey"].(string)
		name, _ := m["name"].(string)
		sig, _ := m["signature"].(string)
		if nk == "" {
			continue
		}

		title := fmt.Sprintf("Docstring suggestion for %s", name)
		content := fmt.Sprintf("// TODO: Add documentation for %s\n// Signature: %s", name, sig)

		if _, err := g.StoreDocstringSuggestion(ctx, nk, title, content, "auto-stub"); err != nil {
			fmt.Printf("Warning: failed to create docstring suggestion for %s: %v\n", name, err)
			continue
		}
		created++
	}
	return created, nil
}

// GenerateFlowSummariesForScope queries for Flow nodes in the current scope
// and creates GeneratedDoc summaries for any that don't already have one.
// Returns the number of summaries created.
func (g *ContextGenerator) GenerateFlowSummariesForScope(ctx context.Context) (int, error) {
	cypher := `
		MATCH (f:Flow)
		WHERE f.scopeId = $scopeId
		  AND NOT EXISTS {
		    MATCH (:GeneratedDoc {type: $docType, sourceKey: f.nodeKey})
		  }
		RETURN f.nodeKey AS nodeKey, f.name AS name, f.flowType AS flowType,
		       f.entrypoint AS entrypoint
		LIMIT 50
	`
	records, err := g.client.ExecuteQuery(ctx, cypher, map[string]any{
		"scopeId": g.scopeCtx.ScopeID,
		"docType": DocTypeFlowSummary,
	})
	if err != nil {
		return 0, fmt.Errorf("failed to query flows without summaries: %w", err)
	}

	created := 0
	for _, r := range records {
		m := r.AsMap()
		nk, _ := m["nodeKey"].(string)
		name, _ := m["name"].(string)
		flowType, _ := m["flowType"].(string)
		if nk == "" {
			continue
		}

		title := fmt.Sprintf("Flow summary: %s", name)
		content := fmt.Sprintf("Auto-generated summary for %s flow '%s'.", flowType, name)

		if _, err := g.StoreFlowSummary(ctx, nk, title, content, "auto-stub"); err != nil {
			fmt.Printf("Warning: failed to create flow summary for %s: %v\n", name, err)
			continue
		}
		created++
	}
	return created, nil
}
