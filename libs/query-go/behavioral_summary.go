package query

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"

	models "github.com/context-maximiser/code-graph/libs/core-models-go"
	neo4j "github.com/context-maximiser/code-graph/libs/neo4j-go"
)

// enrichedStep holds a FlowStep plus side-effect metadata resolved from call-site nodes.
type enrichedStep struct {
	FlowStep
	SideEffectType string // "local", "DB", "RPC <service>", "HTTP", "SQS"
}

// BehavioralSummaryGenerator materialises a compact plain-text behavioral summary on each
// Flow node. The summary is deterministic (derived from spine v2 enrichment, not an LLM)
// and is cached via a 16-char SHA-256 prefix of the step sequence; unchanged flows are
// skipped so re-indexing a service that has not changed is effectively free.
type BehavioralSummaryGenerator struct {
	client   *neo4j.Client
	scopeCtx models.ScopeContext
}

// NewBehavioralSummaryGenerator returns a generator bound to the given Neo4j client.
func NewBehavioralSummaryGenerator(client *neo4j.Client) *BehavioralSummaryGenerator {
	return &BehavioralSummaryGenerator{
		client:   client,
		scopeCtx: models.DefaultScope(),
	}
}

// SetScope sets the scope context for the generator.
func (g *BehavioralSummaryGenerator) SetScope(scope models.ScopeContext) {
	g.scopeCtx = scope
}

// GenerateSummaries generates or refreshes behavioral summaries for all Flow nodes in scope.
// Returns the count of flows updated (flows whose spine has not changed are skipped).
func (g *BehavioralSummaryGenerator) GenerateSummaries(ctx context.Context) (int, error) {
	flows, err := g.listFlows(ctx)
	if err != nil {
		return 0, err
	}

	updated := 0
	for _, flow := range flows {
		steps, err := g.loadEnrichedSteps(ctx, flow.flowNodeKey)
		if err != nil || len(steps) == 0 {
			continue
		}

		hash := computeSpineHash(steps)
		if flow.spineHash == hash {
			continue // spine unchanged — cache is valid
		}

		summary := buildBehavioralSummary(steps)
		if err := g.persistSummary(ctx, flow.flowNodeKey, summary, hash); err != nil {
			fmt.Printf("Warning: behavioral summary persist failed for %s: %v\n", flow.flowNodeKey, err)
			continue
		}
		updated++
	}
	return updated, nil
}

// --- internal types ---

type flowCacheEntry struct {
	flowNodeKey string
	spineHash   string
}

// --- query helpers ---

func (g *BehavioralSummaryGenerator) listFlows(ctx context.Context) ([]flowCacheEntry, error) {
	cypher := `
		MATCH (f:Flow)
		WHERE f.scopeId = $scopeId OR f.scopeId = 'main'
		RETURN f.nodeKey AS flowNodeKey, coalesce(f.spineHash, '') AS spineHash`

	records, err := g.client.ExecuteQuery(ctx, cypher, map[string]any{
		"scopeId": g.scopeCtx.ScopeID,
	})
	if err != nil {
		return nil, fmt.Errorf("listFlows: %w", err)
	}

	out := make([]flowCacheEntry, 0, len(records))
	for _, r := range records {
		m := r.AsMap()
		nk := strVal(m, "flowNodeKey")
		if nk == "" {
			continue
		}
		out = append(out, flowCacheEntry{
			flowNodeKey: nk,
			spineHash:   strVal(m, "spineHash"),
		})
	}
	return out, nil
}

func (g *BehavioralSummaryGenerator) loadEnrichedSteps(ctx context.Context, flowNodeKey string) ([]enrichedStep, error) {
	// For each HAS_STEP target, check whether it makes RPC/HTTP/SQS/DB calls so we can
	// annotate the summary line with its side-effect type.
	cypher := `
		MATCH (f:Flow {nodeKey: $flowKey})
		WHERE f.scopeId = $scopeId OR f.scopeId = 'main'
		OPTIONAL MATCH (f)-[r:HAS_STEP]->(step)
		WHERE (step.scopeId = $scopeId OR step.scopeId = 'main')
		  AND (step:Function OR step:Method)
		OPTIONAL MATCH (step)-[:CALLS_API]->(grpc:GRPCCall)
		  WHERE grpc.scopeId = $scopeId OR grpc.scopeId = 'main'
		OPTIONAL MATCH (step)-[:CALLS_API]->(http:HTTPCall)
		  WHERE http.scopeId = $scopeId OR http.scopeId = 'main'
		OPTIONAL MATCH (step)-[:CALLS_API]->(outbox:OutboxCall)
		  WHERE outbox.scopeId = $scopeId OR outbox.scopeId = 'main'
		OPTIONAL MATCH (step)-[:CALLS_DB]->(db:DBCall)
		  WHERE db.scopeId = $scopeId OR db.scopeId = 'main'
		WITH step, r,
		     count(DISTINCT grpc)   AS grpcCount,
		     count(DISTINCT http)   AS httpCount,
		     count(DISTINCT outbox) AS outboxCount,
		     count(DISTINCT db)     AS dbCount,
		     collect(DISTINCT grpc.targetService)[0] AS targetService
		WHERE step IS NOT NULL
		RETURN step.nodeKey                            AS stepKey,
		       step.name                               AS stepName,
		       labels(step)                            AS stepLabels,
		       coalesce(r.order, 0)                    AS stepOrder,
		       coalesce(r.stepIndex, 0)                AS stepIndex,
		       coalesce(r.branchKey, '')               AS branchKey,
		       coalesce(r.parallelGroup, '')            AS parallelGroup,
		       coalesce(r.inTx, false)                 AS inTx,
		       grpcCount, httpCount, outboxCount, dbCount,
		       coalesce(targetService, '')             AS targetService
		ORDER BY r.order`

	records, err := g.client.ExecuteQuery(ctx, cypher, map[string]any{
		"flowKey": flowNodeKey,
		"scopeId": g.scopeCtx.ScopeID,
	})
	if err != nil {
		return nil, err
	}

	steps := make([]enrichedStep, 0, len(records))
	for _, rec := range records {
		m := rec.AsMap()
		sk := strVal(m, "stepKey")
		if sk == "" {
			continue
		}

		label := "Function"
		if labels, ok := m["stepLabels"].([]any); ok {
			for _, l := range labels {
				if s, ok := l.(string); ok && s == "Method" {
					label = s
					break
				}
			}
		}

		order := 0
		if v, ok := m["stepOrder"].(int64); ok {
			order = int(v)
		}
		stepIndex := 0
		if v, ok := m["stepIndex"].(int64); ok {
			stepIndex = int(v)
		}
		inTx := false
		if v, ok := m["inTx"].(bool); ok {
			inTx = v
		}

		grpcCount := int64(0)
		if v, ok := m["grpcCount"].(int64); ok {
			grpcCount = v
		}
		httpCount := int64(0)
		if v, ok := m["httpCount"].(int64); ok {
			httpCount = v
		}
		outboxCount := int64(0)
		if v, ok := m["outboxCount"].(int64); ok {
			outboxCount = v
		}
		dbCount := int64(0)
		if v, ok := m["dbCount"].(int64); ok {
			dbCount = v
		}

		targetSvc := strVal(m, "targetService")
		sideEffect := resolveSideEffect(grpcCount, httpCount, outboxCount, dbCount, targetSvc)

		steps = append(steps, enrichedStep{
			FlowStep: FlowStep{
				NodeKey:       sk,
				Name:          strVal(m, "stepName"),
				Label:         label,
				Order:         order,
				StepIndex:     stepIndex,
				BranchKey:     strVal(m, "branchKey"),
				ParallelGroup: strVal(m, "parallelGroup"),
				InTx:          inTx,
			},
			SideEffectType: sideEffect,
		})
	}
	return steps, nil
}

func (g *BehavioralSummaryGenerator) persistSummary(ctx context.Context, flowNodeKey, summary, hash string) error {
	cypher := `
		MATCH (f:Flow {nodeKey: $flowKey})
		WHERE f.scopeId = $scopeId OR f.scopeId = 'main'
		SET f.behavioralSummary = $summary,
		    f.spineHash          = $hash`

	_, err := g.client.ExecuteQuery(ctx, cypher, map[string]any{
		"flowKey": flowNodeKey,
		"scopeId": g.scopeCtx.ScopeID,
		"summary": summary,
		"hash":    hash,
	})
	if err != nil {
		return fmt.Errorf("persistSummary: %w", err)
	}
	return nil
}

// --- summary building ---

// computeSpineHash returns a 16-char hex prefix of the SHA-256 of the ordered step sequence.
// Any change in step identity, order, branchKey, parallelGroup, or inTx flag invalidates the cache.
func computeSpineHash(steps []enrichedStep) string {
	h := sha256.New()
	for _, s := range steps {
		fmt.Fprintf(h, "%s|%d|%s|%s|%v\n", s.NodeKey, s.StepIndex, s.BranchKey, s.ParallelGroup, s.InTx)
	}
	return fmt.Sprintf("%x", h.Sum(nil))[:16]
}

// buildBehavioralSummary converts an ordered slice of enriched steps into a numbered
// plain-text summary. Groups parallel siblings into "fork" lines, consecutive
// in-transaction steps into "tx { … }" blocks, and prefixes conditional steps with
// "if <condition>:".
func buildBehavioralSummary(steps []enrichedStep) string {
	if len(steps) == 0 {
		return ""
	}

	var lines []string
	processed := make(map[string]bool, len(steps))

	for i := 0; i < len(steps); i++ {
		s := steps[i]
		if processed[s.NodeKey] {
			continue
		}

		switch {
		case s.ParallelGroup != "":
			// Collect all unprocessed steps in the same parallel fork group.
			siblings := parallelSiblings(steps, s.ParallelGroup, processed)
			names := make([]string, len(siblings))
			for j, sib := range siblings {
				names[j] = sib.Name
				processed[sib.NodeKey] = true
			}
			line := fmt.Sprintf("fork: [%s] (parallel)", strings.Join(names, ", "))
			if s.BranchKey != "" {
				line = "if " + s.BranchKey + ": " + line
			}
			lines = append(lines, line)

		case s.InTx:
			// Collect consecutive inTx steps (stop at first non-inTx step).
			txGroup := []enrichedStep{s}
			processed[s.NodeKey] = true
			for j := i + 1; j < len(steps); j++ {
				next := steps[j]
				if processed[next.NodeKey] {
					continue
				}
				if !next.InTx {
					break
				}
				txGroup = append(txGroup, next)
				processed[next.NodeKey] = true
			}
			lines = append(lines, formatTxBlock(txGroup))

		default:
			processed[s.NodeKey] = true
			line := s.Name + " (" + s.SideEffectType + ")"
			if s.BranchKey != "" {
				line = "if " + s.BranchKey + ": " + line
			}
			lines = append(lines, line)
		}
	}

	var sb strings.Builder
	for i, line := range lines {
		if i > 0 {
			sb.WriteByte('\n')
		}
		fmt.Fprintf(&sb, "%d. %s", i+1, line)
	}
	return sb.String()
}

func parallelSiblings(steps []enrichedStep, group string, processed map[string]bool) []enrichedStep {
	var out []enrichedStep
	for _, s := range steps {
		if s.ParallelGroup == group && !processed[s.NodeKey] {
			out = append(out, s)
		}
	}
	return out
}

func formatTxBlock(txSteps []enrichedStep) string {
	const maxShow = 3
	names := make([]string, len(txSteps))
	for i, s := range txSteps {
		names[i] = s.Name
	}
	if len(names) > maxShow {
		return "tx { " + strings.Join(names[:maxShow], ", ") + ", …commit }"
	}
	return "tx { " + strings.Join(names, ", ") + " }"
}

func resolveSideEffect(grpcCount, httpCount, outboxCount, dbCount int64, targetSvc string) string {
	switch {
	case grpcCount > 0:
		if targetSvc != "" {
			return "RPC " + targetSvc
		}
		return "RPC"
	case httpCount > 0:
		return "HTTP"
	case outboxCount > 0:
		return "SQS"
	case dbCount > 0:
		return "DB"
	default:
		return "local"
	}
}

// StepDetail carries the full metadata for a single flow step, returned by GetStepDetails.
type StepDetail struct {
	Name          string
	FilePath      string
	StartLine     int
	EndLine       int
	Signature     string
	Docstring     string
	Condition     string
	InTx          bool
	ParallelGroup string
	StepOrder     int
}

// GetOrComputeSummary returns the pre-computed behavioralSummary stored on the Flow node.
// If none exists (e.g. index ran before this stage was enabled), it falls back to computing
// the summary inline from the enriched steps without persisting.
func (g *BehavioralSummaryGenerator) GetOrComputeSummary(ctx context.Context, flowNodeKey string) (string, error) {
	cypher := `
		MATCH (f:Flow {nodeKey: $flowKey})
		WHERE f.scopeId = $scopeId OR f.scopeId = 'main'
		RETURN coalesce(f.behavioralSummary, '') AS summary`
	records, err := g.client.ExecuteQuery(ctx, cypher, map[string]any{
		"flowKey": flowNodeKey,
		"scopeId": g.scopeCtx.ScopeID,
	})
	if err == nil && len(records) > 0 {
		if s := strVal(records[0].AsMap(), "summary"); s != "" {
			return s, nil
		}
	}

	steps, err := g.loadEnrichedSteps(ctx, flowNodeKey)
	if err != nil {
		return "", err
	}
	if len(steps) == 0 {
		return "", nil
	}
	return buildBehavioralSummary(steps), nil
}

// GetStepDetails returns the full metadata for all HAS_STEP targets in the given flow
// whose name matches stepName (case-insensitive). Includes file location, signature,
// docstring, and scope annotations (condition, tx, parallel).
func (g *BehavioralSummaryGenerator) GetStepDetails(ctx context.Context, flowNodeKey, stepName string) ([]StepDetail, error) {
	cypher := `
		MATCH (f:Flow {nodeKey: $flowKey})-[r:HAS_STEP]->(step)
		WHERE (f.scopeId = $scopeId OR f.scopeId = 'main')
		  AND toLower(step.name) = toLower($stepName)
		RETURN step.name                            AS name,
		       coalesce(step.filePath, '')          AS filePath,
		       coalesce(step.startLine, 0)          AS startLine,
		       coalesce(step.endLine, 0)            AS endLine,
		       coalesce(step.signature, '')         AS signature,
		       coalesce(step.docstring, '')         AS docstring,
		       coalesce(r.branchKey, '')            AS condition,
		       coalesce(r.inTx, false)              AS inTx,
		       coalesce(r.parallelGroup, '')        AS parallelGroup,
		       coalesce(r.order, 0)                 AS stepOrder
		ORDER BY r.order`

	records, err := g.client.ExecuteQuery(ctx, cypher, map[string]any{
		"flowKey":  flowNodeKey,
		"scopeId":  g.scopeCtx.ScopeID,
		"stepName": stepName,
	})
	if err != nil {
		return nil, fmt.Errorf("GetStepDetails: %w", err)
	}

	out := make([]StepDetail, 0, len(records))
	for _, rec := range records {
		m := rec.AsMap()
		inTx := false
		if v, ok := m["inTx"].(bool); ok {
			inTx = v
		}
		startLine, endLine, stepOrder := 0, 0, 0
		if v, ok := m["startLine"].(int64); ok {
			startLine = int(v)
		}
		if v, ok := m["endLine"].(int64); ok {
			endLine = int(v)
		}
		if v, ok := m["stepOrder"].(int64); ok {
			stepOrder = int(v)
		}
		out = append(out, StepDetail{
			Name:          strVal(m, "name"),
			FilePath:      strVal(m, "filePath"),
			StartLine:     startLine,
			EndLine:       endLine,
			Signature:     strVal(m, "signature"),
			Docstring:     strVal(m, "docstring"),
			Condition:     strVal(m, "condition"),
			InTx:          inTx,
			ParallelGroup: strVal(m, "parallelGroup"),
			StepOrder:     stepOrder,
		})
	}
	return out, nil
}
