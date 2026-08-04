package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/context-maximiser/code-graph/internal/query/reachability"
)

// handleDeadcodeTool runs RFC-014 reachability classification for one
// service and returns the verdicts. Read-only by default (no stamping):
// MCP consumers ask questions, the index pipeline owns graph writes.
func (s *CodeGraphMCPServer) handleDeadcodeTool(ctx context.Context, args map[string]interface{}) ToolCallResponse {
	serviceName, _ := args["service_name"].(string)
	if serviceName == "" {
		return errorResponse("service_name is required")
	}
	scopeID, _ := args["scope_id"].(string)
	verdictFilter, _ := args["verdict"].(string)
	limit := 100
	if l, ok := args["limit"].(float64); ok && l > 0 {
		limit = int(l)
	}

	result, err := reachability.Compute(ctx, s.client, reachability.Options{
		ServiceName: serviceName,
		ScopeID:     scopeID,
	})
	if err != nil {
		return errorResponse(fmt.Sprintf("reachability failed: %v", err))
	}

	type entry struct {
		Name        string `json:"name"`
		Label       string `json:"label"`
		FilePath    string `json:"filePath"`
		StartLine   int64  `json:"startLine"`
		Verdict     string `json:"verdict"`
		Tier        int    `json:"tier,omitempty"`
		RootName    string `json:"rootName,omitempty"`
		DeadCluster bool   `json:"deadCluster,omitempty"`
		IsExported  bool   `json:"isExported"`
	}

	// Default view: everything that is NOT plainly live — the actionable
	// set (dead, possibly_live, test_only, unknown). verdict narrows to one.
	entries := make([]entry, 0, limit)
	truncated := false
	for _, v := range result.Verdicts {
		if verdictFilter != "" {
			if string(v.Verdict) != verdictFilter {
				continue
			}
		} else if v.Verdict == reachability.VerdictLive {
			continue
		}
		if len(entries) >= limit {
			truncated = true
			break
		}
		entries = append(entries, entry{
			Name:        v.Name,
			Label:       v.Label,
			FilePath:    v.FilePath,
			StartLine:   v.StartLine,
			Verdict:     string(v.Verdict),
			Tier:        v.Tier,
			RootName:    v.RootName,
			DeadCluster: v.DeadCluster,
			IsExported:  v.IsExported,
		})
	}

	payload := map[string]interface{}{
		"service":         result.ServiceName,
		"scopeId":         result.ScopeID,
		"total":           result.Total,
		"live":            result.Live,
		"testOnly":        result.TestOnly,
		"dead":            result.Dead,
		"deadCluster":     result.DeadCluster,
		"possiblyLive":    result.PossiblyLive,
		"unknown":         result.Unknown,
		"roots":           result.Roots,
		"testRoots":       result.TestRoots,
		"abstractSkipped": result.AbstractSkipped,
		"entries":         entries,
		"truncated":       truncated,
	}
	out, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return errorResponse(fmt.Sprintf("marshal failed: %v", err))
	}
	return ToolCallResponse{Content: []ToolContent{{Type: "text", Text: string(out)}}}
}
