package pipeline

import (
	"context"
	"fmt"
	"log"
	"time"

	models "github.com/context-maximiser/code-graph/libs/core-models-go"
	"github.com/context-maximiser/code-graph/libs/neo4j-go"
)

// HelperCollapseStage traverses Function→Function→GRPCCall/HTTPCall chains where
// the middle function is a "helper shim" (≤20 statements, single outbound RPC),
// and writes a TRANSITIVE_CALLS_API edge from the outer function directly to the
// call node (carrying viaShim metadata). Original edges are preserved.
type HelperCollapseStage struct{}

func (s *HelperCollapseStage) Name() StageName { return StageHelperCollapse }
func (s *HelperCollapseStage) Optional() bool  { return true }

func (s *HelperCollapseStage) Run(ctx context.Context, cfg *PipelineConfig) (int, error) {
	written, err := collapseHelperShims(ctx, cfg.Client, cfg.ScopeCtx.ScopeID)
	if err != nil {
		log.Printf("[HelperCollapse] error: %v", err)
	}
	return written, err
}

// collapseHelperShims finds chains: outer -[CALLS]-> shim -[CALLS_API]-> call
// where shim has only 1 outbound CALLS_API edge (single-RPC shim), and writes
// TRANSITIVE_CALLS_API from outer to call.
func collapseHelperShims(ctx context.Context, client *neo4j.Client, scopeID string) (int, error) {
	// Find shims: functions with exactly 1 outbound CALLS_API edge.
	findShimsCypher := `
		MATCH (shim:Function)-[:CALLS_API]->(call)
		WHERE (shim.scopeId = $scopeId OR shim.scopeId = 'main')
		  AND (call:GRPCCall OR call:HTTPCall OR call:OutboxCall)
		WITH shim, count(call) AS outRPCCount
		WHERE outRPCCount = 1
		RETURN elementId(shim) AS shimId, shim.name AS shimName
		LIMIT 500
	`
	shims, err := client.ExecuteQuery(ctx, findShimsCypher, map[string]any{"scopeId": scopeID})
	if err != nil {
		return 0, fmt.Errorf("HelperCollapse: find shims: %w", err)
	}

	written := 0
	now := time.Now().UTC().Unix()

	for _, row := range shims {
		rm := row.AsMap()
		shimId, _ := rm["shimId"].(string)
		shimName, _ := rm["shimName"].(string)
		if shimId == "" {
			continue
		}

		// Find callers of this shim and the shim's single outbound call.
		collapseCypher := `
			MATCH (outer)-[:CALLS]->(shim)-[:CALLS_API]->(call)
			WHERE elementId(shim) = $shimId
			  AND (outer:Function OR outer:Method)
			  AND (call:GRPCCall OR call:HTTPCall OR call:OutboxCall)
			RETURN elementId(outer) AS outerId, elementId(call) AS callId
			LIMIT 50
		`
		pairs, err := client.ExecuteQuery(ctx, collapseCypher, map[string]any{"shimId": shimId})
		if err != nil {
			continue
		}

		for _, pair := range pairs {
			pm := pair.AsMap()
			outerId, _ := pm["outerId"].(string)
			callId, _ := pm["callId"].(string)
			if outerId == "" || callId == "" {
				continue
			}

			_, err := client.MergeRelationship(ctx, outerId, callId,
				string(models.TransitiveCallsAPIRel),
				map[string]any{},
				map[string]any{
					"viaShim":    shimName,
					"resolvedAt": now,
				},
			)
			if err != nil {
				log.Printf("[HelperCollapse] TRANSITIVE_CALLS_API write failed: %v", err)
				continue
			}
			written++
		}
	}

	log.Printf("[HelperCollapse] Wrote %d TRANSITIVE_CALLS_API edges", written)
	return written, nil
}
