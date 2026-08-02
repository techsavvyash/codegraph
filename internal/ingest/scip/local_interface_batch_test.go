package static

import (
	"testing"

	"github.com/context-maximiser/code-graph/internal/ingest/resolve"
	models "github.com/context-maximiser/code-graph/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuildLocalCallBatch_KindSplitAndProps verifies the pure batch-building
// wiring: relations are split by Kind (CALLS vs USES_VALUE), endpoints resolve
// to definition-node IDs (preferring def over symbol node), edge props carry
// line/scope/scopeId/detectionSource, and relations with an unresolvable
// endpoint are dropped.
func TestBuildLocalCallBatch_KindSplitAndProps(t *testing.T) {
	const (
		callerSym  = "scip-go gomod example.com v1 `pkg`/summaryModelName()."
		calleeSym1 = "scip-go gomod example.com v1 `llm`/openAICompatCompleter#Model()."
		calleeSym2 = "scip-go gomod example.com v1 `llm`/otherCompleter#Model()."
		missingSym = "scip-go gomod example.com v1 `pkg`/NotIndexed#Method()."
	)

	// symbolToDefKey + defIDs give the caller and calleeSym1 definition nodes;
	// calleeSym2 has only a symbol node (falls back), missingSym has neither.
	symbolIDs := map[string]string{
		callerSym:  "sym:caller",
		calleeSym1: "sym:callee1",
		calleeSym2: "sym:callee2",
	}
	defIDs := map[string]string{
		"fn#" + callerSym: "def:caller",
		"m#" + calleeSym1: "def:callee1",
	}
	symbolToDefKey := map[string]string{
		callerSym:  "fn#" + callerSym,
		calleeSym1: "m#" + calleeSym1,
	}

	rels := []resolve.LocalCallRelation{
		{FromSymbol: callerSym, ToSymbol: calleeSym1, Kind: resolve.LocalCallInvoke, Line: 27},
		{FromSymbol: callerSym, ToSymbol: calleeSym2, Kind: resolve.LocalCallInvoke, Line: 30},
		{FromSymbol: callerSym, ToSymbol: calleeSym1, Kind: resolve.LocalCallValue, Line: 40},
		// Dropped: callee not in any map.
		{FromSymbol: callerSym, ToSymbol: missingSym, Kind: resolve.LocalCallInvoke, Line: 50},
	}

	scope := models.DefaultScope()

	calls := buildLocalCallBatch(rels, resolve.LocalCallInvoke, symbolIDs, defIDs, symbolToDefKey, scope)
	values := buildLocalCallBatch(rels, resolve.LocalCallValue, symbolIDs, defIDs, symbolToDefKey, scope)

	// CALLS batch: two resolvable invoke relations (calleeSym1 via def,
	// calleeSym2 via symbol fallback); missingSym dropped.
	require.Len(t, calls, 2, "two resolvable CALLS relations; missing-symbol relation dropped")

	// Assert the def-preferred edge is present with correct props.
	var found bool
	for _, item := range calls {
		if item["fromId"] == "def:caller" && item["toId"] == "def:callee1" {
			found = true
			props := item["props"].(map[string]any)
			assert.Equal(t, "local-interface", props["detectionSource"])
			assert.Equal(t, 27, props["line"])
			assert.Equal(t, scope.Scope, props["scope"])
			assert.Equal(t, scope.ScopeID, props["scopeId"])
		}
	}
	assert.True(t, found, "expected CALLS edge def:caller -> def:callee1 (definition nodes preferred)")

	// The symbol-fallback edge (calleeSym2 has no def node) resolves to sym node.
	var foundFallback bool
	for _, item := range calls {
		if item["toId"] == "sym:callee2" {
			foundFallback = true
		}
	}
	assert.True(t, foundFallback, "calleeSym2 has no def node, edge must fall back to sym:callee2")

	// USES_VALUE batch: one relation, def-preferred endpoints.
	require.Len(t, values, 1, "one USES_VALUE relation")
	assert.Equal(t, "def:caller", values[0]["fromId"])
	assert.Equal(t, "def:callee1", values[0]["toId"])
	vprops := values[0]["props"].(map[string]any)
	assert.Equal(t, "local-interface", vprops["detectionSource"])
	assert.Equal(t, 40, vprops["line"])

	// No batch item may have an empty endpoint.
	for _, item := range append(append([]map[string]any{}, calls...), values...) {
		assert.NotEmpty(t, item["fromId"])
		assert.NotEmpty(t, item["toId"])
	}
}

// TestBuildLocalCallBatch_Empty verifies no-op behavior on an empty input.
func TestBuildLocalCallBatch_Empty(t *testing.T) {
	got := buildLocalCallBatch(nil, resolve.LocalCallInvoke, map[string]string{}, map[string]string{}, map[string]string{}, models.DefaultScope())
	assert.Empty(t, got)
}
