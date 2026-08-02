package oracle

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCrossCheckDeadVerdicts(t *testing.T) {
	handler := goFuncID{pkgPath: "example.com/labeledgo", funcName: "handler"}
	reachable := map[goFuncID]bool{handler: true}

	deadSymbols := []string{
		// Parses to handler — CHA-reachable → disagreement.
		"scip-go gomod example.com/labeledgo . `example.com/labeledgo`/handler().",
		// Parses fine, not reachable → agreement (correctly dead).
		"scip-go gomod example.com/labeledgo . `example.com/labeledgo`/unused().",
		// Abstract slot — skipped, cannot appear in CHA.
		"scip-go gomod example.com/labeledgo . `example.com/labeledgo`/Storer#Save.",
	}

	got := crossCheckDeadVerdicts(deadSymbols, reachable)
	if len(got) != 1 || got[0] != handler {
		t.Fatalf("expected exactly the handler disagreement, got %+v", got)
	}
}

// TestCrossCheckDeadVerdicts_AgainstRealExtraction wires the actual
// MainReachable set produced from testdata/goproj into the cross-check,
// feeding it scip-go-shaped dead symbols. A function that IS reachable via
// in-module interface dispatch (reachedViaDispatch) must be flagged as a
// disagreement (the classifier would be wrong to call it dead); a function
// reachable from nothing (neverCalled) must NOT be flagged (correctly dead).
// This is the end-to-end proof that the raw-graph reachability replaces the
// vacuous folded-edge walk: reachedViaDispatch only connects to main through
// a CHA may-edge, which the deleted folded walk could not follow.
func TestCrossCheckDeadVerdicts_AgainstRealExtraction(t *testing.T) {
	root, err := filepath.Abs("testdata/goproj")
	require.NoError(t, err)

	ex, err := extractGoCallGraphs(root)
	require.NoError(t, err)

	const pkg = "example.com/goproj"
	// scip-go free-function symbol grammar: 5 space-separated fields, the
	// last a backtick-quoted package path then `/Name().` (see
	// parseFreeFunctionSymbol / callableFuncID).
	reachableSym := "scip-go gomod " + pkg + " . `" + pkg + "`/reachedViaDispatch()."
	deadSym := "scip-go gomod " + pkg + " . `" + pkg + "`/neverCalled()."

	// Sanity-check the symbols parse to the identities we expect, so a
	// grammar drift fails loudly here rather than silently making the
	// assertions vacuous.
	rid, ok := callableFuncID(reachableSym)
	require.True(t, ok, "reachableSym must parse to a callable identity")
	require.Equal(t, id(pkg, "", "reachedViaDispatch"), rid)
	did, ok := callableFuncID(deadSym)
	require.True(t, ok, "deadSym must parse to a callable identity")
	require.Equal(t, id(pkg, "", "neverCalled"), did)

	got := crossCheckDeadVerdicts([]string{reachableSym, deadSym}, ex.MainReachable)
	if len(got) != 1 || got[0] != rid {
		t.Fatalf("expected exactly the reachedViaDispatch disagreement, got %+v", got)
	}
}
