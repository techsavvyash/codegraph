package generated

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/context-maximiser/code-graph/libs/core-models-go"
	"github.com/context-maximiser/code-graph/libs/generation-go"
	"github.com/context-maximiser/code-graph/libs/intelligence-go/contracts"
	"github.com/context-maximiser/code-graph/libs/neo4j-go"
)

// GeneratedDocType constants for the different types of generated docs.
const (
	DocTypePRSummary           = "pr_summary"
	DocTypeFlowSummary         = "flow_summary"
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
			bundle := g.buildBundle(ctx,
				contracts.RetrievalCandidate{NodeKey: prNodeKey, NodeType: "PullRequest", Metadata: map[string]any{"prId": prID, "title": prTitle}},
				DocTypePRSummary, 1000, 24, 12)

			ok, err := g.generateAndVerify(ctx, bundle, DocTypePRSummary, "pull_request", prNodeKey, title)
			if err != nil {
				g.storeGenerationFailureDiagnostic(ctx, DocTypePRSummary, "pull_request", prNodeKey, err)
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
		  AND (n.filePath IS NULL OR (NOT n.filePath CONTAINS '/vendor/' AND NOT n.filePath ENDS WITH '_test.go'))
		  AND (n.docstring IS NULL OR n.docstring = '')
		OPTIONAL MATCH (n)<-[:CALLS]-(caller)
		WHERE (caller:Function OR caller:Method)
		  AND (caller.scopeId = $scopeId OR caller.scopeId = 'main')
		WITH n, count(DISTINCT caller) AS callerCount, 0 AS apiExposureCount,
		     CASE
		       WHEN toLower(coalesce(n.name, '')) CONTAINS 'handler' OR toLower(coalesce(n.name, '')) CONTAINS 'controller' THEN 18
		       WHEN toLower(coalesce(n.name, '')) CONTAINS 'service' THEN 12
		       ELSE 0
		     END AS nameSignal
		RETURN n.nodeKey AS nodeKey, n.name AS name, n.signature AS signature,
		       labels(n)[0] AS nodeType,
		       callerCount, apiExposureCount,
		       (callerCount * 2) + (apiExposureCount * 15) + nameSignal AS relevanceScore
		ORDER BY relevanceScore DESC, n.name ASC
		LIMIT 30
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
			bundle := g.buildBundle(ctx,
				contracts.RetrievalCandidate{NodeKey: nk, NodeType: nodeType, Metadata: map[string]any{"name": name, "signature": sig}},
				DocTypeDocstringSuggestion, 500, 16, 8)

			ok, err := g.generateAndVerify(ctx, bundle, DocTypeDocstringSuggestion, "code_symbol", nk, title)
			if err != nil {
				g.storeGenerationFailureDiagnostic(ctx, DocTypeDocstringSuggestion, "code_symbol", nk, err)
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
		OPTIONAL MATCH (f)-[:HAS_STEP]->(step)
		WHERE step:Function OR step:Method
		WITH f, count(step) AS stepCount,
		     CASE WHEN toLower(coalesce(f.name, '')) CONTAINS '/api/' OR toLower(coalesce(f.name, '')) CONTAINS 'http' THEN 25 ELSE 0 END AS apiSignal
		RETURN f.nodeKey AS nodeKey, f.name AS name, f.flowType AS flowType,
		       f.entrypoint AS entrypoint,
		       stepCount,
		       (stepCount * 3) + apiSignal AS relevanceScore
		ORDER BY relevanceScore DESC, f.name ASC
		LIMIT 30
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
			bundle := g.buildBundle(ctx,
				contracts.RetrievalCandidate{NodeKey: nk, NodeType: "Flow", Metadata: map[string]any{"name": name, "flowType": flowType}},
				DocTypeFlowSummary, 1000, 20, 12)

			ok, err := g.generateAndVerify(ctx, bundle, DocTypeFlowSummary, "flow", nk, title)
			if err != nil {
				g.storeGenerationFailureDiagnostic(ctx, DocTypeFlowSummary, "flow", nk, err)
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
	if insufficiency := insufficientEvidenceViolation(docType, bundle); insufficiency != "" {
		g.storeDiagnostic(ctx, docType, sourceType, sourceKey, nil, nil, []string{insufficiency})
		return false, nil
	}

	genResult, err := g.generator.Generate(ctx, bundle)
	if err != nil {
		if genResult != nil {
			g.storeDiagnostic(ctx, docType, sourceType, sourceKey, genResult, ensureVerificationResult(genResult, nil), generationViolationsFromError(err))
			return false, nil
		}
		return false, fmt.Errorf("generation: %w", err)
	}
	var verResult *contracts.VerificationResult

	// Run verifier if available.
	if g.verifier != nil {
		verResult, err = g.verifier.Verify(ctx, genResult, g.scopeCtx)
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

	if lowInfoViolation := lowInformationViolation(docType, genResult.Content); lowInfoViolation != "" {
		g.storeDiagnostic(ctx, docType, sourceType, sourceKey, genResult, ensureVerificationResult(genResult, verResult), []string{lowInfoViolation})
		return false, nil
	}

	// Validate that all statements have at least one citation before persisting.
	if uncited := generation.ValidateGenerationResult(genResult); len(uncited) > 0 {
		violations := []string{fmt.Sprintf("citation_key_missing: %d/%d statements have no citations", len(uncited), len(genResult.Citations))}
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
		Index int `json:"index"`
		Refs  int `json:"refs"`
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
	model := ""
	content := ""
	if genResult != nil {
		model = genResult.Model
		content = genResult.Content
	}
	unsupportedClaims := []int{}
	if verResult != nil {
		unsupportedClaims = verResult.UnsupportedClaims
	}
	if len(violations) == 0 {
		violations = []string{"generation_rejected"}
	}
	rawViolations := append([]string(nil), violations...)
	normalizedViolations := normalizeViolations(violations)

	diagKey := models.GenerationDiagnosticNodeKey(docType, sourceKey)
	props := map[string]any{
		"nodeKey":             diagKey,
		"type":                docType,
		"sourceType":          sourceType,
		"sourceKey":           sourceKey,
		"model":               model,
		"content":             content,
		"rejectionReasons":    normalizedViolations,
		"rawRejectionReasons": rawViolations,
		"unsupportedClaims":   unsupportedClaims,
		"scope":               g.scopeCtx.Scope,
		"scopeId":             g.scopeCtx.ScopeID,
		"createdAt":           time.Now().UTC().Format(time.RFC3339),
	}

	_, err := g.client.MergeNode(ctx, []string{"GenerationDiagnostic"},
		map[string]any{"nodeKey": diagKey, "scopeId": g.scopeCtx.ScopeID}, props)
	if err != nil {
		fmt.Printf("Warning: failed to persist generation diagnostic for %s: %v\n", sourceKey, err)
	}
}

func (g *ContextGenerator) storeGenerationFailureDiagnostic(ctx context.Context, docType, sourceType, sourceKey string, generationErr error) {
	if generationErr == nil {
		return
	}
	g.storeDiagnostic(ctx, docType, sourceType, sourceKey,
		&contracts.GenerationResult{Model: "generation_error"},
		&contracts.VerificationResult{Passed: false, Errors: []string{generationErr.Error()}},
		generationViolationsFromError(generationErr))
}

func (g *ContextGenerator) buildBundle(ctx context.Context, anchor contracts.RetrievalCandidate, template string, maxTokens, expansionLimit, inferenceLimit int) *contracts.ContextBundle {
	bundle := &contracts.ContextBundle{
		Anchors:   []contracts.RetrievalCandidate{anchor},
		Template:  template,
		MaxTokens: maxTokens,
		Scope:     g.scopeCtx.Scope,
		ScopeID:   g.scopeCtx.ScopeID,
	}

	if g.client == nil {
		return bundle
	}
	bundle.Expansions = g.loadRelatedEvidence(ctx, anchor.NodeKey, expansionLimit)
	bundle.Inferences = g.loadInferredEvidence(ctx, anchor.NodeKey, inferenceLimit)
	return bundle
}

func (g *ContextGenerator) loadRelatedEvidence(ctx context.Context, sourceKey string, limit int) []contracts.RetrievalCandidate {
	if limit <= 0 {
		return nil
	}
	cypher := `
		MATCH (src {nodeKey: $sourceKey})
		WHERE src.scopeId = $scopeId OR src.scopeId = 'main'
		OPTIONAL MATCH (src)-[r]-(nbr)
		WHERE nbr.nodeKey IS NOT NULL
		  AND nbr.nodeKey <> $sourceKey
		  AND (nbr.scopeId = $scopeId OR nbr.scopeId = 'main')
		  AND (nbr:File OR nbr:Function OR nbr:Method OR nbr:Flow OR nbr:DocumentChunk OR nbr:Service OR nbr:PullRequest)
		WITH nbr, count(r) AS edgeWeight
		RETURN nbr.nodeKey AS nodeKey,
		       labels(nbr)[0] AS nodeType,
		       coalesce(nbr.name, nbr.title, nbr.path, nbr.nodeKey) AS name,
		       edgeWeight,
		       coalesce(nbr.filePath, '') AS filePath
		ORDER BY edgeWeight DESC, name ASC
		LIMIT $limit
	`
	records, err := g.client.ExecuteQuery(ctx, cypher, map[string]any{
		"sourceKey": sourceKey,
		"scopeId":   g.scopeCtx.ScopeID,
		"limit":     limit,
	})
	if err != nil {
		return nil
	}

	candidates := make([]contracts.RetrievalCandidate, 0, len(records))
	for _, record := range records {
		m := record.AsMap()
		nodeKey, _ := m["nodeKey"].(string)
		if nodeKey == "" {
			continue
		}
		nodeType, _ := m["nodeType"].(string)
		name, _ := m["name"].(string)
		filePath, _ := m["filePath"].(string)
		score := 0.0
		if edgeWeight, ok := m["edgeWeight"].(int64); ok {
			score = float64(edgeWeight)
		}
		candidates = append(candidates, contracts.RetrievalCandidate{
			NodeKey:  nodeKey,
			NodeType: nodeType,
			Scope:    g.scopeCtx.Scope,
			ScopeID:  g.scopeCtx.ScopeID,
			Score:    score,
			Source:   "graph",
			Metadata: map[string]any{"name": name, "filePath": filePath},
		})
	}
	return candidates
}

func (g *ContextGenerator) loadInferredEvidence(ctx context.Context, sourceKey string, limit int) []contracts.InferenceResult {
	if limit <= 0 {
		return nil
	}
	cypher := `
		MATCH (src {nodeKey: $sourceKey})-[r]->(target)
		WHERE target.nodeKey IS NOT NULL
		  AND (src.scopeId = $scopeId OR src.scopeId = 'main')
		  AND (target.scopeId = $scopeId OR target.scopeId = 'main')
		RETURN src.nodeKey AS sourceKey, target.nodeKey AS targetKey, type(r) AS relationType
		ORDER BY relationType ASC, target.nodeKey ASC
		LIMIT $limit
	`
	records, err := g.client.ExecuteQuery(ctx, cypher, map[string]any{
		"sourceKey": sourceKey,
		"scopeId":   g.scopeCtx.ScopeID,
		"limit":     limit,
	})
	if err != nil {
		return nil
	}
	results := make([]contracts.InferenceResult, 0, len(records))
	for _, record := range records {
		m := record.AsMap()
		source, _ := m["sourceKey"].(string)
		target, _ := m["targetKey"].(string)
		relation, _ := m["relationType"].(string)
		if source == "" || target == "" || relation == "" {
			continue
		}
		results = append(results, contracts.InferenceResult{
			SourceKey:    source,
			TargetKey:    target,
			RelationType: relation,
			Confidence:   0.75,
			Strategy:     "graph_relation_seed",
			Reasons:      []string{fmt.Sprintf("derived_from_%s", strings.ToLower(relation))},
			EvidenceRefs: []contracts.EvidenceRef{{Kind: "graph_edge", NodeKey: target}},
			CreatedAt:    time.Now().UTC(),
		})
	}
	return results
}

func insufficientEvidenceViolation(docType string, bundle *contracts.ContextBundle) string {
	if bundle == nil {
		return "insufficient_evidence_bundle: missing_bundle"
	}
	minAnchors := 1
	minExpansions := 2
	minEvidence := 4
	switch docType {
	case DocTypePRSummary:
		minExpansions = 4
		minEvidence = 7
	case DocTypeFlowSummary:
		minExpansions = 3
		minEvidence = 6
	case DocTypeDocstringSuggestion:
		minExpansions = 2
		minEvidence = 4
	}
	if len(bundle.Anchors) < minAnchors {
		return fmt.Sprintf("insufficient_evidence_bundle: anchors(%d<%d)", len(bundle.Anchors), minAnchors)
	}
	if len(bundle.Expansions) < minExpansions {
		return fmt.Sprintf("insufficient_evidence_bundle: expansions(%d<%d)", len(bundle.Expansions), minExpansions)
	}

	evidence := make(map[string]struct{})
	for _, anchor := range bundle.Anchors {
		evidence[anchor.NodeKey] = struct{}{}
	}
	for _, expansion := range bundle.Expansions {
		evidence[expansion.NodeKey] = struct{}{}
	}
	for _, inference := range bundle.Inferences {
		evidence[inference.SourceKey] = struct{}{}
		evidence[inference.TargetKey] = struct{}{}
	}
	if len(evidence) < minEvidence {
		return fmt.Sprintf("insufficient_evidence_bundle: evidence_nodes(%d<%d)", len(evidence), minEvidence)
	}
	return ""
}

func ensureVerificationResult(gen *contracts.GenerationResult, ver *contracts.VerificationResult) *contracts.VerificationResult {
	if ver != nil {
		return ver
	}
	total := len(gen.Citations)
	return &contracts.VerificationResult{
		Passed:          false,
		TotalStatements: total,
		CitedStatements: total,
	}
}

func lowInformationViolation(docType, content string) string {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return "low_information_content: empty_content"
	}
	wordCount := len(strings.Fields(trimmed))
	minWords := 10
	switch docType {
	case DocTypePRSummary:
		minWords = 24
	case DocTypeFlowSummary:
		minWords = 14
	case DocTypeDocstringSuggestion:
		minWords = 8
	}
	if wordCount < minWords {
		return fmt.Sprintf("low_information_content: too_short (%d words < %d)", wordCount, minWords)
	}

	lower := strings.ToLower(trimmed)
	for _, phrase := range []string{
		"ready for the next steps",
		"successfully passed",
		"enhance the overall",
		"critical issues identified",
	} {
		if strings.Contains(lower, phrase) {
			return fmt.Sprintf("low_information_content: contains_generic_phrase(%q)", phrase)
		}
	}
	return ""
}

func generationViolationsFromError(err error) []string {
	if err == nil {
		return nil
	}
	var citationErr *generation.CitationValidationError
	if errors.As(err, &citationErr) {
		violations := make([]string, 0, len(citationErr.Errors))
		for _, statementErr := range citationErr.Errors {
			violations = append(violations,
				fmt.Sprintf("citation_key_missing: statement_%d missing_refs=%s", statementErr.StatementIndex, strings.Join(statementErr.MissingRefs, ",")))
		}
		if len(violations) > 0 {
			return violations
		}
	}

	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "parsing response"):
		return []string{fmt.Sprintf("format_error: %v", err)}
	case strings.Contains(msg, "llm completion"):
		return []string{fmt.Sprintf("generation_transport_error: %v", err)}
	default:
		return []string{fmt.Sprintf("generation_error: %v", err)}
	}
}

func normalizeViolations(violations []string) []string {
	normalized := make([]string, 0, len(violations))
	for _, violation := range violations {
		if violation == "" {
			continue
		}
		lower := strings.ToLower(violation)
		code := "policy_violation"
		switch {
		case strings.Contains(lower, "insufficient_evidence_bundle"):
			code = "insufficient_evidence"
		case strings.Contains(lower, "low_information_content") || strings.Contains(lower, "content too short") || strings.Contains(lower, "generic phrase"):
			code = "low_information"
		case strings.Contains(lower, "citation_key_missing") || strings.Contains(lower, "citation coverage") || strings.Contains(lower, "no citations"):
			code = "citation_key_missing"
		case strings.Contains(lower, "unsupported claim rate") || strings.Contains(lower, "verification did not pass"):
			code = "verification_failure"
		case strings.Contains(lower, "format_error"):
			code = "format_error"
		case strings.Contains(lower, "generation_transport_error"):
			code = "generation_transport_error"
		case strings.Contains(lower, "generation_error"):
			code = "generation_error"
		}
		normalized = append(normalized, fmt.Sprintf("%s: %s", code, violation))
	}
	if len(normalized) == 0 {
		return []string{"policy_violation: generation_rejected"}
	}
	return normalized
}
