package generated

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/context-maximiser/code-graph/libs/core-models-go"
	"github.com/context-maximiser/code-graph/libs/generation-go"
	"github.com/context-maximiser/code-graph/libs/intelligence-go/contracts"
	"github.com/context-maximiser/code-graph/libs/neo4j-go"
)

// GeneratedDocType constants for the different types of generated docs.
const (
	DocTypePRSummary          = "pr_summary"
	DocTypeFlowSummary        = "flow_summary"
	DocTypeDocstringSuggestion = "docstring_suggestion"
)

// PolicyEvaluator decides whether a generation result may be persisted.
type PolicyEvaluator interface {
	Evaluate(gen *contracts.GenerationResult, ver *contracts.VerificationResult) PolicyDecision
}

// PolicyDecision is the outcome of the policy gate evaluation.
type PolicyDecision struct {
	Allowed          bool
	Reason           string
	PolicyViolations []string
}

// ContextGenerator creates and manages generated context nodes in the graph.
type ContextGenerator struct {
	client    *neo4j.Client
	scopeCtx  models.ScopeContext
	generator contracts.Generator
	verifier  contracts.Verifier
	policy    PolicyEvaluator
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

// SetGenerator sets the generation adapter for evidence-backed generation.
func (g *ContextGenerator) SetGenerator(gen contracts.Generator) {
	g.generator = gen
}

// SetVerifier sets the citation verifier.
func (g *ContextGenerator) SetVerifier(ver contracts.Verifier) {
	g.verifier = ver
}

// SetPolicy sets the persistence policy gate.
func (g *ContextGenerator) SetPolicy(p PolicyEvaluator) {
	g.policy = p
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
func (g *ContextGenerator) StorePRSummary(ctx context.Context, prID, title, content, model string, changedFileKeys []string, genResult *contracts.GenerationResult) (string, error) {
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
		"createdAt":  time.Now().UTC().Format(time.RFC3339),
		"strategy":   "evidence_backed",
	}
	marshalCitationProps(docProps, genResult)

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
func (g *ContextGenerator) StoreFlowSummary(ctx context.Context, flowNodeKey, title, content, model string, genResult *contracts.GenerationResult) (string, error) {
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
		"createdAt":  time.Now().UTC().Format(time.RFC3339),
		"strategy":   "evidence_backed",
	}
	marshalCitationProps(docProps, genResult)

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
func (g *ContextGenerator) StoreDocstringSuggestion(ctx context.Context, targetNodeKey, title, content, model string, genResult *contracts.GenerationResult) (string, error) {
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
		"createdAt":  time.Now().UTC().Format(time.RFC3339),
		"strategy":   "evidence_backed",
	}
	marshalCitationProps(docProps, genResult)

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

// GeneratePRSummaryForScope queries for PullRequest nodes in the current scope
// that do not already have a PR summary GeneratedDoc and creates one.
// Returns the number of summaries created.
func (g *ContextGenerator) GeneratePRSummaryForScope(ctx context.Context) (int, error) {
	// Find PR nodes in this scope that lack a summary.
	cypher := `
		MATCH (pr:PullRequest)
		WHERE pr.scopeId = $scopeId
		  AND NOT EXISTS {
		    MATCH (:GeneratedDoc {type: $docType, sourceKey: pr.nodeKey})
		  }
		RETURN pr.nodeKey AS nodeKey, pr.prId AS prId, pr.title AS title
		LIMIT 10
	`
	records, err := g.client.ExecuteQuery(ctx, cypher, map[string]any{
		"scopeId": g.scopeCtx.ScopeID,
		"docType": DocTypePRSummary,
	})
	if err != nil {
		return 0, fmt.Errorf("failed to query PRs without summaries: %w", err)
	}

	created := 0
	for _, r := range records {
		m := r.AsMap()
		prID, _ := m["prId"].(string)
		prTitle, _ := m["title"].(string)
		if prID == "" {
			continue
		}

		// Query file and symbol counts from the graph for this scope.
		countCypher := `
			MATCH (f:File)
			WHERE f.scopeId = $scopeId OR f.scope = 'main'
			WITH count(f) AS fileCount
			OPTIONAL MATCH (s:Symbol {scopeId: $scopeId})
			RETURN fileCount, count(s) AS symbolCount
		`
		countRecords, err := g.client.ExecuteQuery(ctx, countCypher, map[string]any{
			"scopeId": g.scopeCtx.ScopeID,
		})
		fileCount, symbolCount := int64(0), int64(0)
		if err == nil && len(countRecords) > 0 {
			cm := countRecords[0].AsMap()
			if v, ok := cm["fileCount"].(int64); ok {
				fileCount = v
			}
			if v, ok := cm["symbolCount"].(int64); ok {
				symbolCount = v
			}
		}

		summary := fmt.Sprintf("Indexed %d files and %d symbols for %s", fileCount, symbolCount, prTitle)
		title := fmt.Sprintf("Indexing summary for PR %s", prID)

		// Collect changed file keys linked to the PR.
		fileCypher := `
			MATCH (f:File)-[:CONTAINS|PART_OF*0..1]->(pr:PullRequest {prId: $prId, scopeId: $scopeId})
			RETURN f.nodeKey AS nodeKey
			UNION
			MATCH (f:File {scopeId: $scopeId})
			RETURN f.nodeKey AS nodeKey
		`
		fileRecords, _ := g.client.ExecuteQuery(ctx, fileCypher, map[string]any{
			"prId":    prID,
			"scopeId": g.scopeCtx.ScopeID,
		})
		var fileKeys []string
		for _, fr := range fileRecords {
			if nk, ok := fr.AsMap()["nodeKey"].(string); ok && nk != "" {
				fileKeys = append(fileKeys, nk)
			}
		}

		if g.generator != nil {
			prNodeKey, _ := m["nodeKey"].(string)
			bundle := &contracts.ContextBundle{
				Anchors: []contracts.RetrievalCandidate{
					{NodeKey: prNodeKey, NodeType: "PullRequest", Metadata: map[string]any{"prId": prID, "title": prTitle}},
				},
				Template:  DocTypePRSummary,
				MaxTokens: 1000,
				Scope:     g.scopeCtx.Scope,
				ScopeID:   g.scopeCtx.ScopeID,
			}

			ok, err := g.generateAndVerify(ctx, bundle, DocTypePRSummary, "pull_request", prNodeKey, title)
			if err != nil {
				fmt.Printf("Warning: evidence-backed PR summary generation failed for %s: %v\n", prID, err)
				continue
			}
			if ok {
				created++
			}
			continue
		}

		if _, err := g.StorePRSummary(ctx, prID, title, summary, "stage6-auto", fileKeys, nil); err != nil {
			fmt.Printf("Warning: failed to create PR summary for %s: %v\n", prID, err)
			continue
		}
		created++
	}
	return created, nil
}

// GenerateDocstringSuggestionsForScope queries for exported Functions/Methods in
// the current scope that lack a docstring and creates GeneratedDoc suggestions.
// When a Generator and Verifier are set, content is evidence-backed and
// verifier-gated. Otherwise, stub content is generated.
// Returns the number of suggestions created.
func (g *ContextGenerator) GenerateDocstringSuggestionsForScope(ctx context.Context) (int, error) {
	cypher := `
		MATCH (n)
		WHERE (n:Function OR n:Method)
		  AND n.scopeId = $scopeId
		  AND n.isExported = true
		  AND (n.docstring IS NULL OR n.docstring = '')
		RETURN n.nodeKey AS nodeKey, n.name AS name, n.signature AS signature,
		       labels(n)[0] AS nodeType
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
		nodeType, _ := m["nodeType"].(string)
		if nk == "" {
			continue
		}

		title := fmt.Sprintf("Docstring suggestion for %s", name)

		if g.generator != nil {
			bundle := &contracts.ContextBundle{
				Anchors: []contracts.RetrievalCandidate{
					{NodeKey: nk, NodeType: nodeType, Metadata: map[string]any{"name": name, "signature": sig}},
				},
				Template:  DocTypeDocstringSuggestion,
				MaxTokens: 500,
				Scope:     g.scopeCtx.Scope,
				ScopeID:   g.scopeCtx.ScopeID,
			}

			ok, err := g.generateAndVerify(ctx, bundle, DocTypeDocstringSuggestion, "code_symbol", nk, title)
			if err != nil {
				fmt.Printf("Warning: evidence-backed docstring generation failed for %s: %v\n", name, err)
				continue
			}
			if ok {
				created++
			}
			continue
		}

		content := fmt.Sprintf("// TODO: Add documentation for %s\n// Signature: %s", name, sig)
		if _, err := g.StoreDocstringSuggestion(ctx, nk, title, content, "auto-stub", nil); err != nil {
			fmt.Printf("Warning: failed to create docstring suggestion for %s: %v\n", name, err)
			continue
		}
		created++
	}
	return created, nil
}

// GenerateFlowSummariesForScope queries for Flow nodes in the current scope
// and creates GeneratedDoc summaries for any that don't already have one.
// When a Generator and Verifier are set, content is evidence-backed and
// verifier-gated. Otherwise, stub content is generated.
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

		if g.generator != nil {
			bundle := &contracts.ContextBundle{
				Anchors: []contracts.RetrievalCandidate{
					{NodeKey: nk, NodeType: "Flow", Metadata: map[string]any{"name": name, "flowType": flowType}},
				},
				Template:  DocTypeFlowSummary,
				MaxTokens: 1000,
				Scope:     g.scopeCtx.Scope,
				ScopeID:   g.scopeCtx.ScopeID,
			}

			ok, err := g.generateAndVerify(ctx, bundle, DocTypeFlowSummary, "flow", nk, title)
			if err != nil {
				fmt.Printf("Warning: evidence-backed flow summary generation failed for %s: %v\n", name, err)
				continue
			}
			if ok {
				created++
			}
			continue
		}

		content := fmt.Sprintf("Auto-generated summary for %s flow '%s'.", flowType, name)
		if _, err := g.StoreFlowSummary(ctx, nk, title, content, "auto-stub", nil); err != nil {
			fmt.Printf("Warning: failed to create flow summary for %s: %v\n", name, err)
			continue
		}
		created++
	}
	return created, nil
}

// generateAndVerify runs the generation + verification + policy gate pipeline.
// Returns true if content was persisted as GeneratedDoc, false if rejected (persisted as diagnostic).
func (g *ContextGenerator) generateAndVerify(ctx context.Context, bundle *contracts.ContextBundle, docType, sourceType, sourceKey, title string) (bool, error) {
	genResult, err := g.generator.Generate(ctx, bundle)
	if err != nil {
		return false, fmt.Errorf("generation: %w", err)
	}

	// Run verifier if available.
	if g.verifier != nil {
		verResult, err := g.verifier.Verify(ctx, genResult, g.scopeCtx)
		if err != nil {
			return false, fmt.Errorf("verification: %w", err)
		}

		// Run policy gate if available.
		if g.policy != nil {
			decision := g.policy.Evaluate(genResult, verResult)
			if !decision.Allowed {
				// Persist as diagnostic, not as published context.
				g.storeDiagnostic(ctx, docType, sourceType, sourceKey, genResult, verResult, decision.PolicyViolations)
				return false, nil
			}
		} else if !verResult.Passed {
			// No policy gate, but verifier failed — persist as diagnostic.
			g.storeDiagnostic(ctx, docType, sourceType, sourceKey, genResult, verResult, verResult.Errors)
			return false, nil
		}
	}

	// Validate that all statements have at least one citation before persisting.
	if uncited := generation.ValidateGenerationResult(genResult); len(uncited) > 0 {
		violations := []string{fmt.Sprintf("%d/%d statements have no citations", len(uncited), len(genResult.Citations))}
		g.storeDiagnostic(ctx, docType, sourceType, sourceKey, genResult,
			&contracts.VerificationResult{
				TotalStatements:   len(genResult.Citations),
				CitedStatements:   len(genResult.Citations) - len(uncited),
				UnsupportedClaims: uncited,
				Errors:            violations,
			}, violations)
		return false, nil
	}

	// Verification passed (or no verifier set) — persist as GeneratedDoc.
	switch docType {
	case DocTypeDocstringSuggestion:
		_, err = g.StoreDocstringSuggestion(ctx, sourceKey, title, genResult.Content, genResult.Model, genResult)
	case DocTypeFlowSummary:
		_, err = g.StoreFlowSummary(ctx, sourceKey, title, genResult.Content, genResult.Model, genResult)
	case DocTypePRSummary:
		_, err = g.StorePRSummary(ctx, sourceKey, title, genResult.Content, genResult.Model, nil, genResult)
	}
	return err == nil, err
}

// marshalCitationProps serializes statement-level citations from a GenerationResult
// into the docProps map for persistence. If genResult is nil (stub generation),
// the props are left unchanged.
func marshalCitationProps(docProps map[string]any, genResult *contracts.GenerationResult) {
	if genResult == nil || len(genResult.Citations) == 0 {
		return
	}

	// Build statement texts from content by splitting on newlines (mirrors generation.buildContent).
	statementsJSON, err := json.Marshal(genResult.Citations)
	if err == nil {
		docProps["citations"] = string(statementsJSON)
	}

	// Serialize individual statement texts extracted from citations index.
	type statementEntry struct {
		Index int    `json:"index"`
		Refs  int    `json:"refs"`
	}
	entries := make([]statementEntry, len(genResult.Citations))
	for i, c := range genResult.Citations {
		entries[i] = statementEntry{Index: c.StatementIndex, Refs: len(c.EvidenceRefs)}
	}
	stmtJSON, err := json.Marshal(entries)
	if err == nil {
		docProps["statements"] = string(stmtJSON)
	}
}

// storeDiagnostic persists a rejected generation as a GenerationDiagnostic node.
func (g *ContextGenerator) storeDiagnostic(ctx context.Context, docType, sourceType, sourceKey string, genResult *contracts.GenerationResult, verResult *contracts.VerificationResult, violations []string) {
	if g.client == nil {
		return
	}

	diagKey := models.GenerationDiagnosticNodeKey(docType, sourceKey)
	props := map[string]any{
		"nodeKey":           diagKey,
		"type":              docType,
		"sourceType":        sourceType,
		"sourceKey":         sourceKey,
		"model":             genResult.Model,
		"content":           genResult.Content,
		"rejectionReasons":  violations,
		"unsupportedClaims": verResult.UnsupportedClaims,
		"scope":             g.scopeCtx.Scope,
		"scopeId":           g.scopeCtx.ScopeID,
		"createdAt":         time.Now().UTC().Format(time.RFC3339),
	}

	_, err := g.client.MergeNode(ctx, []string{"GenerationDiagnostic"},
		map[string]any{"nodeKey": diagKey, "scopeId": g.scopeCtx.ScopeID}, props)
	if err != nil {
		fmt.Printf("Warning: failed to persist generation diagnostic for %s: %v\n", sourceKey, err)
	}
}
