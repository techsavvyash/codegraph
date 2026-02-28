package search

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

	"github.com/context-maximiser/code-graph/libs/neo4j-go"
)

// FlowLinker creates MENTIONS edges from DocumentChunks to Flow nodes
// when chunk content references functions that are steps of a flow spine.
type FlowLinker struct {
	client  *neo4j.Client
	scopeID string
}

// NewFlowLinker creates a new flow-aware linker.
func NewFlowLinker(client *neo4j.Client) *FlowLinker {
	return &FlowLinker{client: client, scopeID: "main"}
}

// SetScope sets the scope for flow-aware linking.
func (fl *FlowLinker) SetScope(scopeID string) {
	fl.scopeID = scopeID
}

// flowCandidate is a function/method name detected in chunk content.
type flowCandidate struct {
	name     string
	patterns []string // which patterns matched
}

// DetectFlowCandidates scans chunk content for flow-related patterns:
// - backtick-quoted identifiers that match Flow step functions
// - HTTP method + path patterns (e.g. "GET /api/users")
// - keywords like "handler", "endpoint", "route", "middleware"
func DetectFlowCandidates(content string) []flowCandidate {
	var candidates []flowCandidate
	seen := make(map[string]bool)

	// 1. Backtick-quoted identifiers
	backtickRe := regexp.MustCompile("`([A-Za-z_][A-Za-z0-9_.]*)`")
	for _, match := range backtickRe.FindAllStringSubmatch(content, -1) {
		name := match[1]
		if !seen[name] {
			seen[name] = true
			candidates = append(candidates, flowCandidate{name: name, patterns: []string{"backtick"}})
		}
	}

	// 2. HTTP method + path patterns
	httpRe := regexp.MustCompile(`(?i)\b(GET|POST|PUT|DELETE|PATCH)\s+(/[a-z0-9/_\-{}:]+)`)
	for _, match := range httpRe.FindAllStringSubmatch(content, -1) {
		routeName := strings.ToUpper(match[1]) + " " + match[2]
		if !seen[routeName] {
			seen[routeName] = true
			candidates = append(candidates, flowCandidate{name: routeName, patterns: []string{"http_route"}})
		}
	}

	return candidates
}

// LinkFlowsForDocument finds DocumentChunks for a given document and creates
// MENTIONS edges to Flow nodes when the chunk content references flow steps.
// Returns the number of links created.
func (fl *FlowLinker) LinkFlowsForDocument(ctx context.Context, docNodeKey string) (int, error) {
	// Get all chunks for this document.
	chunkQuery := `
		MATCH (d:Document {nodeKey: $docKey})-[:HAS_CHUNK]->(c:DocumentChunk)
		WHERE d.scopeId = $scopeId OR d.scopeId = 'main'
		RETURN c.nodeKey AS chunkKey, c.content AS content
	`
	chunks, err := fl.client.ExecuteQuery(ctx, chunkQuery, map[string]any{
		"docKey":  docNodeKey,
		"scopeId": fl.scopeID,
	})
	if err != nil {
		return 0, fmt.Errorf("failed to query chunks: %w", err)
	}

	// Get all flow step function nodeKeys in this scope.
	flowStepQuery := `
		MATCH (f:Flow)-[:HAS_STEP]->(step)
		WHERE (f.scopeId = $scopeId OR f.scopeId = 'main')
		  AND (step:Function OR step:Method)
		RETURN f.nodeKey AS flowKey, f.name AS flowName,
		       step.nodeKey AS stepKey, step.name AS stepName
	`
	flowSteps, err := fl.client.ExecuteQuery(ctx, flowStepQuery, map[string]any{
		"scopeId": fl.scopeID,
	})
	if err != nil {
		return 0, fmt.Errorf("failed to query flow steps: %w", err)
	}

	// Build lookup: function name → (flowKey, stepKey)
	type flowRef struct {
		flowKey, flowName, stepKey string
	}
	stepByName := make(map[string]flowRef)
	for _, r := range flowSteps {
		m := r.AsMap()
		stepName := getStringValue(m, "stepName")
		if stepName == "" {
			continue
		}
		stepByName[stepName] = flowRef{
			flowKey:  getStringValue(m, "flowKey"),
			flowName: getStringValue(m, "flowName"),
			stepKey:  getStringValue(m, "stepKey"),
		}
	}

	if len(stepByName) == 0 {
		return 0, nil // No flows to link to.
	}

	totalLinks := 0
	for _, chunk := range chunks {
		cm := chunk.AsMap()
		chunkKey := getStringValue(cm, "chunkKey")
		content := getStringValue(cm, "content")
		if chunkKey == "" || content == "" {
			continue
		}

		candidates := DetectFlowCandidates(content)
		for _, c := range candidates {
			ref, found := stepByName[c.name]
			if !found {
				continue
			}

			// Create MENTIONS edge: chunk → flow
			if err := fl.createFlowMention(ctx, chunkKey, ref.flowKey, ref.flowName, c.patterns); err != nil {
				log.Printf("Warning: failed to create flow MENTIONS for %s → %s: %v", chunkKey, ref.flowKey, err)
				continue
			}
			totalLinks++
		}
	}

	return totalLinks, nil
}

func (fl *FlowLinker) createFlowMention(ctx context.Context, chunkKey, flowKey, flowName string, patterns []string) error {
	// Build reasons from detected patterns.
	reasons := make([]string, 0, len(patterns)+1)
	reasons = append(reasons, "flow_step_reference")
	for _, p := range patterns {
		reasons = append(reasons, p)
	}

	now := time.Now().UTC().Format(time.RFC3339)

	cypher := `
		MATCH (chunk:DocumentChunk {nodeKey: $chunkKey})
		WHERE chunk.scopeId = $scopeId OR chunk.scopeId = 'main'
		WITH chunk ORDER BY CASE WHEN chunk.scopeId = $scopeId THEN 0 ELSE 1 END LIMIT 1
		MATCH (flow:Flow {nodeKey: $flowKey})
		WHERE flow.scopeId = $scopeId OR flow.scopeId = 'main'
		WITH chunk, flow ORDER BY CASE WHEN flow.scopeId = $scopeId THEN 0 ELSE 1 END LIMIT 1
		MERGE (chunk)-[r:MENTIONS]->(flow)
		SET r.contextType = 'flow_aware_linking',
		    r.flowName = $flowName,
		    r.patterns = $patterns,
		    r.scopeId = $scopeId,
		    r.confidence = $confidence,
		    r.reasons = $reasons,
		    r.createdAt = $createdAt,
		    r.model = $model
		RETURN elementId(r) AS id
	`
	_, err := fl.client.ExecuteQuery(ctx, cypher, map[string]any{
		"chunkKey":   chunkKey,
		"flowKey":    flowKey,
		"flowName":   flowName,
		"patterns":   patterns,
		"scopeId":    fl.scopeID,
		"confidence": 0.7,
		"reasons":    reasons,
		"createdAt":  now,
		"model":      "flow_aware_linking",
	})
	return err
}
