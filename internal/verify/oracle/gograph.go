package oracle

import (
	"context"
	"fmt"
	"strings"

	neo4j "github.com/context-maximiser/code-graph/internal/graph"
	"github.com/context-maximiser/code-graph/internal/ingest/resolve"
)

// goCallsRow is one (caller symbol, callee symbol) pair read off the graph's
// CALLS edges, restricted to Function/Method nodes carrying a DEFINES edge
// to a Symbol node — the same join shape internal/ingest/scip's call graph
// builders use (see call_graph_scip.go's REFERENCES->DEFINES traversal).
type goCallsRow struct {
	fromSymbol string
	toSymbol   string
}

// fetchGoCallsEdges reads every CALLS edge between Function/Method nodes
// scoped to serviceName (and scopeId, if given — "main" is always included
// alongside an itest scope the same way the indexer's own degree query
// does), returning each endpoint's SCIP symbol string as emitted by
// scip-go. Only edges whose caller AND callee both resolve to a Symbol are
// returned; endpoints with no DEFINES edge (shouldn't happen for Go
// Function/Method nodes, but the query is defensive) are simply absent from
// the result rather than erroring.
func fetchGoCallsEdges(ctx context.Context, client *neo4j.Client, serviceName, scopeID string) ([]goCallsRow, error) {
	cypher := `
		MATCH (caller)-[:CALLS]->(callee)
		WHERE (caller:Function OR caller:Method)
		  AND (callee:Function OR callee:Method)
		  AND caller.serviceName = $serviceName
		  AND callee.serviceName = $serviceName
		  AND (caller.scopeId = $scopeId OR caller.scopeId = 'main')
		  AND (callee.scopeId = $scopeId OR callee.scopeId = 'main')
		MATCH (caller)-[:DEFINES]->(callerSym:Symbol)
		MATCH (callee)-[:DEFINES]->(calleeSym:Symbol)
		RETURN callerSym.symbol AS fromSymbol, calleeSym.symbol AS toSymbol
	`
	scopeID = strings.TrimSpace(scopeID)
	if scopeID == "" {
		scopeID = "main"
	}
	params := map[string]any{
		"serviceName": serviceName,
		"scopeId":     scopeID,
	}

	records, err := client.ExecuteQuery(ctx, cypher, params)
	if err != nil {
		return nil, fmt.Errorf("fetch graph CALLS edges: %w", err)
	}

	rows := make([]goCallsRow, 0, len(records))
	for _, rec := range records {
		fromRaw, _ := rec.Get("fromSymbol")
		toRaw, _ := rec.Get("toSymbol")
		from, _ := fromRaw.(string)
		to, _ := toRaw.(string)
		if from == "" || to == "" {
			continue
		}
		rows = append(rows, goCallsRow{fromSymbol: from, toSymbol: to})
	}
	return rows, nil
}

// fetchGoNodeSymbols reads every Function/Method node's SCIP symbol in the
// given scope, independent of CALLS participation — mirroring the TS
// oracle's nodeExists set (see tsoracle.go's fetchCallsIndex). A node with
// zero in/out CALLS edges never appears in fetchGoCallsEdges, but still
// needs to count as "exists" for the stale-graph guard in RunGoOracle: a
// static/CHA edge whose caller or callee was never indexed at all (e.g.
// source added after the last index run) is a graph-staleness artifact,
// not a recall gap, and must not be reported as one.
func fetchGoNodeSymbols(ctx context.Context, client *neo4j.Client, serviceName, scopeID string) ([]string, error) {
	cypher := `
		MATCH (fn)
		WHERE (fn:Function OR fn:Method)
		  AND fn.serviceName = $serviceName
		  AND (fn.scopeId = $scopeId OR fn.scopeId = 'main')
		MATCH (fn)-[:DEFINES]->(sym:Symbol)
		RETURN sym.symbol AS symbol
	`
	scopeID = strings.TrimSpace(scopeID)
	if scopeID == "" {
		scopeID = "main"
	}
	records, err := client.ExecuteQuery(ctx, cypher, map[string]any{
		"serviceName": serviceName,
		"scopeId":     scopeID,
	})
	if err != nil {
		return nil, fmt.Errorf("fetch graph Function/Method node symbols: %w", err)
	}

	symbols := make([]string, 0, len(records))
	for _, rec := range records {
		raw, _ := rec.Get("symbol")
		if s, _ := raw.(string); s != "" {
			symbols = append(symbols, s)
		}
	}
	return symbols, nil
}

// knownGoFuncIDs reduces a set of graph node symbols to the goFuncID set
// used to gate the stale-graph guard. Uses the same callableFuncID parse as
// edges do, so a node counts as "known" under exactly the identity an edge
// endpoint would resolve to.
func knownGoFuncIDs(symbols []string) map[goFuncID]bool {
	known := make(map[goFuncID]bool, len(symbols))
	for _, sym := range symbols {
		if id, ok := callableFuncID(sym); ok {
			known[id] = true
		}
	}
	return known
}

// goGraphJoin is the outcome of joining graph CALLS edges onto goFuncID
// space via resolve.ParseGoSymbolDescriptor.
type goGraphJoin struct {
	Edges map[edgeKey]bool

	// Abstract counts edges dropped because an endpoint is a TERM-form
	// interface-method symbol (`Method.` with no call parens) — these
	// represent abstract dispatch and cannot appear in a concrete SSA call
	// graph by construction, so they are reported separately rather than
	// treated as precision suspects.
	Abstract int
	// Unmappable counts edges dropped because an endpoint's symbol string
	// did not resolve to a concrete callable: it failed to parse as a Go
	// descriptor at all, or parsed as a type-level/package symbol with no
	// method component (not a realistic shape for a CALLS endpoint, but
	// excluded defensively rather than asserted).
	Unmappable int
}

// joinGoGraphEdges converts raw (symbol, symbol) CALLS rows into goFuncID
// edge keys by parsing each symbol string with the same grammar-aware
// parser the RFC-001 resolver join uses — never by string-constructing a
// symbol to compare against. Method-level symbols (`Type#Method().`) map
// onto goFuncID{pkgPath, typ, method}; package-level function symbols
// (`Func().`, no `#`) map onto goFuncID{pkgPath, "", func}. Type-level
// symbols (`Type#`, no method) and abstract interface-method symbols
// (`Type#Method.` without the `()` call suffix) are not concrete callables
// and are excluded — the latter counted in Abstract, the former in
// Unmappable.
func joinGoGraphEdges(rows []goCallsRow) *goGraphJoin {
	out := &goGraphJoin{Edges: make(map[edgeKey]bool, len(rows))}

	for _, row := range rows {
		if isAbstractMethodSymbol(row.fromSymbol) || isAbstractMethodSymbol(row.toSymbol) {
			out.Abstract++
			continue
		}

		fromID, fromOK := callableFuncID(row.fromSymbol)
		toID, toOK := callableFuncID(row.toSymbol)
		if !fromOK || !toOK {
			out.Unmappable++
			continue
		}

		out.Edges[edgeKey{from: fromID, to: toID}] = true
	}

	return out
}

// callableFuncID parses sym and returns its goFuncID iff it denotes a
// concrete callable: a type-bound method (`Type#Method().`) or a
// package-level function (`Func().`, no `#`) — i.e. anything with a
// non-empty method/function component. resolve.ParseGoSymbolDescriptor
// covers the method case but, by design (it exists to serve IMPLEMENTS
// joins, which are always type-bound), returns ok=false for the free
// -function shape; parseFreeFunctionSymbol below covers that shape locally
// rather than changing the shared parser's contract.
func callableFuncID(sym string) (goFuncID, bool) {
	if pkgPath, typ, method, ok := resolve.ParseGoSymbolDescriptor(sym); ok {
		if method == "" {
			return goFuncID{}, false // type-level symbol, not callable
		}
		return goFuncID{pkgPath: pkgPath, typeName: typ, funcName: method}, true
	}
	if pkgPath, fn, ok := parseFreeFunctionSymbol(sym); ok {
		return goFuncID{pkgPath: pkgPath, funcName: fn}, true
	}
	return goFuncID{}, false
}

// parseFreeFunctionSymbol parses the one Go symbol shape
// parseGoSymbolDescriptor deliberately rejects: a package-level function or
// term, `` `<pkgPath>`/Name(). `` or `` `<pkgPath>`/Name. `` with no `#`.
// Only the call-suffix form (`Name().`) is a concrete callable; the
// bare-dot form (`Name.`) is a package-level term (e.g. a var or const) and
// returns ok=false.
func parseFreeFunctionSymbol(sym string) (pkgPath, name string, ok bool) {
	parts := strings.SplitN(sym, " ", 5)
	if len(parts) != 5 {
		return "", "", false
	}
	descriptor := parts[4]

	if !strings.HasPrefix(descriptor, "`") {
		return "", "", false
	}
	end := strings.Index(descriptor[1:], "`")
	if end < 0 {
		return "", "", false
	}
	pkgPath = descriptor[1 : 1+end]
	rest := strings.TrimPrefix(descriptor[1+end+1:], "/")

	if strings.Contains(rest, "#") || rest == "" {
		return "", "", false // type/method-bound or bare package symbol
	}
	if !strings.HasSuffix(rest, "().") {
		return "", "", false // not a call — e.g. a package-level term
	}
	name = strings.TrimSuffix(rest, "().")
	if name == "" {
		return "", "", false
	}
	return pkgPath, name, true
}

// isAbstractMethodSymbol reports whether sym is a scip-go interface abstract
// method / field symbol: `Type#Method.` (trailing bare dot, no call
// parens). Such symbols denote an interface's method slot, not a concrete
// callable, and can never appear as a node in a concrete SSA call graph —
// see parseGoSymbolDescriptor's grammar comment. Distinguishing this from
// the concrete-method shape (`Type#Method().`) requires re-inspecting the
// raw descriptor suffix, since parseGoSymbolDescriptor normalizes both to
// the same (typ, method) pair.
func isAbstractMethodSymbol(sym string) bool {
	pkgPath, typ, method, ok := resolve.ParseGoSymbolDescriptor(sym)
	if !ok || typ == "" || method == "" {
		_ = pkgPath
		return false
	}

	parts := strings.SplitN(sym, " ", 5)
	if len(parts) != 5 {
		return false
	}
	descriptor := parts[4]

	member := typ + "#" + method
	idx := strings.Index(descriptor, member)
	if idx < 0 {
		return false
	}
	rest := descriptor[idx+len(member):]
	return strings.HasPrefix(rest, ".") && !strings.HasPrefix(rest, "().")
}
