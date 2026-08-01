package oracle

import (
	"testing"
)

func TestChaReachableFromMain(t *testing.T) {
	main := goFuncID{pkgPath: "example.com/app", funcName: "main"}
	handler := goFuncID{pkgPath: "example.com/app", funcName: "handler"}
	helper := goFuncID{pkgPath: "example.com/app/util", typeName: "Svc", funcName: "Do"}
	orphan := goFuncID{pkgPath: "example.com/app", funcName: "orphan"}
	orphanCallee := goFuncID{pkgPath: "example.com/app", funcName: "orphanCallee"}
	// A METHOD named main must not be treated as a root.
	methodMain := goFuncID{pkgPath: "example.com/app", typeName: "Runner", funcName: "main"}
	methodMainCallee := goFuncID{pkgPath: "example.com/app", funcName: "onlyViaMethodMain"}

	may := map[edgeKey]bool{
		{from: main, to: handler}:                true,
		{from: handler, to: helper}:              true,
		{from: orphan, to: orphanCallee}:         true,
		{from: methodMain, to: methodMainCallee}: true,
	}

	reachable := chaReachableFromMain(may)
	for _, want := range []goFuncID{main, handler, helper} {
		if !reachable[want] {
			t.Errorf("%v should be CHA-reachable from main", want)
		}
	}
	for _, notWant := range []goFuncID{orphan, orphanCallee, methodMainCallee} {
		if reachable[notWant] {
			t.Errorf("%v should NOT be reachable (no path from a package-level main)", notWant)
		}
	}
}

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
