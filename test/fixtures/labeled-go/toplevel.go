// toplevel.go exercises RFC-013 follow-up classification (tasks #18/#19):
// module-scope call sites must produce (File)-[:CALLS]-> edges, and
// function-VALUE references (both at module scope and inside bodies) must
// produce NO edges at all.
package main

// Module-scope CALL: buildTable() runs at package init. Expected:
// (File toplevel.go)-[:CALLS]->(Function buildTable).
var precomputedTable = buildTable()

// Module-scope VALUE reference: savedHandler holds handleValue without
// invoking it. Expected: no CALLS edge to handleValue from anywhere.
var savedHandler = handleValue

func buildTable() []int { return []int{1, 2, 3} }

func handleValue() {}

// bodyValueUse re-creates the live false-positive the Go oracle found
// (`embedder.Fn = semlinkVectors`): a function value assigned to a struct
// field inside a body. Expected: no CALLS edge from bodyValueUse to
// handleValue.
func bodyValueUse() func() {
	cfg := struct{ Fn func() }{}
	cfg.Fn = handleValue
	_ = precomputedTable
	_ = savedHandler
	return cfg.Fn
}
