# samename-go

Regression fixture for the RFC-013 same-name range-clobbering bug: two
unrelated structs, `FooStage` and `BarStage`, each declare a method named
`Run` in the same file, and each `Run` calls a different helper function.

Before the fix, `graphNodesByName` (internal/ingest/scip/call_graph_scip.go)
keyed its lookup purely by bare method name with no receiver
disambiguation, so both `Run` nodes collapsed to a single map entry
(nondeterministic last-writer-wins). `updateFunctionBodyRanges` then wrote
one AST body range onto both nodes' shared identity, and `findEnclosingCaller`
attributed calls made inside either method's body to whichever single node
won the collision — misattributing `BarStage.Run`'s call to `helperB` onto
the `FooStage.Run` node (or vice versa), and giving one of the two Run nodes
the wrong startLine/endLine entirely.

Used by test/integration/call_graph_samename_test.go, which indexes this
fixture into an isolated scope and asserts:
  - FooStage.Run's stored body range matches its own AST span, not BarStage's
  - BarStage.Run's stored body range matches its own AST span, not FooStage's
  - FooStage.Run -[:CALLS]-> helperA exists
  - BarStage.Run -[:CALLS]-> helperB exists
  - FooStage.Run does NOT call helperB, and BarStage.Run does NOT call helperA
    (the misattribution the bug used to produce)
