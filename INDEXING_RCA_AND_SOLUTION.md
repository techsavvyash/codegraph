# CodeGraph Indexing — RCA & Solution Report

**Date:** 2026-05-23
**Author:** Claude (Opus 4.7, 1M context) for @nandhu
**Subject:** Why behavioral flow queries (e.g. `createPayout`) return *structure* instead of *behavior*, and concretely what to change in the indexer / schema / linker.
**Scope:** `apps/cli`, `libs/core-models-go`, `libs/schema-go`, `libs/indexer-go/static`, `libs/indexer-go/pipeline`, `libs/search-go`.

---

## 0. Executive Summary

A side-by-side comparison of a CodeGraph MCP query and a plain `grep/Read` walk of the same `createPayout` flow showed the MCP output was **a symbol map**, while the native walk was **a behavioral trace**. The MCP answer comprehensively listed *what functions exist where*, but failed to capture:

- execution order across a flow,
- conditional gates around each call (`if OBO`, `if first-payout`, `if status == requires_approval`),
- transaction scope (`BeginTx … EndTx`),
- parallelism (`go func() / errgroup.Go`),
- defer'd side-effects,
- one-hop cross-service inlining (settlement → SOL → PSP),
- SQS topic / DB table string literals at the call site,
- per-call purpose comments / nearest docstring.

These omissions are not bugs in the consumer — they are **structural gaps in the indexing model**. The graph stores *who can call whom*; it does not store *under what conditions, in what order, with what side-effects, inside what transactional/concurrency scope* a call actually executes.

This document explains, with code citations, **why** the current model produces this output and **what specific schema, AST-walker, and pipeline changes** would close the gap without rewriting CodeGraph.

---

## 1. Current Indexing Model — Ground Truth

### 1.1 Node labels (libs/core-models-go/node.go)

| Category | Labels | Notes |
|---|---|---|
| Structural | `Service`, `File`, `Module`, `Class`, `Interface`, `Function`, `Method` | Carry `filePath`, `startLine`, `endLine`, `docstring`, `signature`, `complexity` |
| Symbol | `Symbol`, `APIRoute` | SCIP-canonical symbols + API surface |
| Call-site | `GRPCCall`, `HTTPCall`, `OutboxCall`, `DBCall` | Carry `filePath`, `line`, plus type-specific fields (`eventType`, `queueOrTopic`, `table`, `operation`, `targetService`, `targetMethod`) |
| Documents | `Document`, `DocumentChunk`, `Flow` | `Flow` carries `entrypointKey`, `flowType`, `maxDepth` |

All nodes inherit `BaseNode` (id, nodeKey, scope, scopeId, tenantId, repo, repoId, createdAt, updatedAt — node.go:40-52).

### 1.2 Relationship types (libs/core-models-go/relationship.go)

| Edge | Endpoints | Properties |
|---|---|---|
| `CALLS` | Function/Method → Function/Method | `line`, `isDynamic`, `isRecursive` |
| `CALLS_API` | Function → GRPCCall/HTTPCall/OutboxCall/DBCall | `line`, `transport` |
| `CALLS_SERVICE` | Call-site → Service | `protocol` |
| `CALLS_DB` | Function → DBCall | `line` |
| `CONTAINS` | File/Module → child | `order` |
| `DEFINES` | Module/Class → Symbol | `isExported` |
| `IMPLEMENTS` | Class/Method → Interface/Method | `confidence`, `validationMethod` |
| `EXPOSES_API` | Function → APIRoute | — |
| `HAS_CHUNK` | Document → DocumentChunk | — |
| `HAS_STEP` | Flow → Function/Method | — |
| `MENTIONS` | DocumentChunk → Flow | `context` |
| `CONSUMES_FROM` | Function → OutboxCall | — |
| `DEPENDS_ON` | Service → Service | `version`, `isDirect` |

`REFERENCES`, `INHERITS_FROM`, `SCHEDULED_BY` are declared but **not emitted** by the call-graph indexer (relationship.go:10-11, 19-20, 37-38).

### 1.3 How CALLS edges are built (libs/indexer-go/static/call_graph_scip.go)

Two-pass:

1. **SCIP pass** emits one *Reference* per call site (line-precise).
2. **AST pass** computes `funcRange{Name, DeclLine, StartLine, EndLine}` for every function body (call_graph_scip.go:19-26). For each reference at line `L`, the builder finds the *innermost* function whose body range contains `L`, and writes `(caller)-[:CALLS {line:L, isDynamic:false, isRecursive:caller==target}]->(callee)`.

A `branchRange{StartLine, EndLine, Depth}` struct **exists** (call_graph_scip.go:29-33) but is never read by the writer. **This is a key finding: the indexer was set up to know about nested branches but never persists the relationship.**

### 1.4 Call-site enrichment (rpc_call_detector.go, db_call_detector.go, event_call_detector.go)

- RPC detector tracks `varTypeMap` per function for client variables → builds `GRPCCall` nodes; `targetService` is resolved by suffix-stripping the client interface name (`PaymentServiceClient → PaymentService`) and a Neo4j fuzzy lookup (`toLower(s.name) CONTAINS toLower($name)`).
- DB detector tracks `pgx`/`sqlx` receiver variables and extracts table names from raw SQL via a `tableFromSQL` regex (db_call_detector.go:20).
- Event detector extracts string literals with `extractFirstStringLiteral`, `extractQueueStringLiteral`, `extractStringArg` and writes them to `OutboxCall.eventType` / `OutboxCall.queueOrTopic` (event_call_detector.go:317-330).

Important: **string-literal extraction exists for SQS/event detection, but the literal is attached to the *node*, not to the *edge* — and only for explicitly recognised RPC/event/DB call sites.** Generic `CALLS` edges (function→function) carry no such payload. So `TriggerPayoutEvent(ctx, payoutID, "created")` will produce an `OutboxCall` only if the detector recognises `TriggerPayoutEvent` as an event-producer; otherwise the literal `"created"` is dropped.

### 1.5 What is absent — verified by grep

The static indexer has **no code paths** that:

1. Capture the nearest enclosing `if`/`switch`/`for`/`select` for a call.
2. Detect `go func()`, `errgroup.Go`, `sync.WaitGroup.Add`, channel sends, or `select` blocks as concurrency scopes.
3. Detect `BeginTx … Commit/Rollback` or `WithTx(func(tx){…})` as transaction scopes.
4. Distinguish `defer call()` from a normal call.
5. Capture per-call leading-line / trailing comments and attach them to the edge.
6. Traverse multi-level method chains (`repository.Pgx().Payout.Save(...)`) — only the innermost callable name is captured.
7. Inline one hop across services when emitting flow context (a `CALLS_SERVICE` to SOL stops there).

### 1.6 Pipeline (libs/indexer-go/pipeline/stages.go)

1. `IngestCodeStage` — SCIP + AST (Functions, Files, Symbols, CALLS, call-site nodes).
2. `InferServiceDepsStage` — placeholder; inferred inline.
3. `GenerateFlowSpinesStage` — `FlowSpineGenerator.GenerateFlows(ctx, 3)` produces `Flow` + `HAS_STEP`.
4. `IngestDocumentsStage` — docs/chunks.
5. `LinkDocumentChunksStage` — `MENTIONS` (chunk → flow).
6. `GenerateContextDocsStage` — LLM-written summaries.
7. `RefreshRetrievalIndexesStage` — vector / text index refresh.
8. `ComputeGraphMetricsStage` — PageRank etc.

The flow spine (#3) walks `CALLS` edges to depth 3 from entry points. Because edges have no order/condition/scope payload, the resulting spine is a **set of reachable functions**, not a **sequenced trace**. This is exactly what surfaces to the MCP client.

---

## 2. Root-Cause Analysis — Why the Gap Exists

### RCA-1: Edges have no temporal property
`CALLS.line` is a *position*, not an *order index*. A reader cannot reconstruct order without re-reading the source. The flow-spine query returns sets, not paths.
**Effect:** Output is grouped by category, never by execution order.

### RCA-2: Edges have no control-flow property
The `branchRange` struct exists in the AST walker but is never written out. There is no `:CALLS {underCondition: "if hasPSP", branchDepth: 2}` style annotation.
**Effect:** Every call appears unconditional. The fact that `GetMerchantPSPDetails` only runs when `ProvidersRequiringMerchantID[payoutRail]` is true is invisible.

### RCA-3: No concurrency scope nodes
`go func()`, `errgroup.Go`, and similar are walked as regular calls. There is no `ConcurrentScope` node nor an `IN_PARALLEL_WITH` edge.
**Effect:** The reader sees a linear sequence even though "FX holding + FX destination fetch" or "Account config + Onboarding KYB lookup" run in parallel goroutines.

### RCA-4: No transaction scope nodes
The DB detector ignores `BeginTx`/`Commit`. There is no `TxScope` node nor `IN_TX` edge.
**Effect:** It is impossible to answer "what writes are inside the same transaction as the `payout` INSERT?" — a routine impact-analysis question.

### RCA-5: String literals attach to nodes, not to call edges
The event detector recognises a narrow set of producers (Kafka/SQS/NATS publish APIs). Anything else that takes string literals (`status` transitions, event-type enums passed to generic dispatchers, named-mutex keys, table-name strings used outside SQL) is unparameterised on the edge.
**Effect:** `TriggerPayoutEvent(ctx, id, "created")` and `TriggerPayoutEvent(ctx, id, "initiated")` look identical in the graph.

### RCA-6: Cross-service edges terminate at the boundary
`CALLS_SERVICE` connects a `GRPCCall` to a `Service` node; it does not connect to the *handler function* in that service. Even when both services are indexed in the same Neo4j instance, the graph does not stitch `settlement.PayoutInitiate` → `sol.PayoutInitiate` handler.
**Effect:** "What does SOL do with this?" requires a second, manual MCP query.

### RCA-7: Method chains lose intermediate receivers
`repository.Pgx().Payout.Save(ctx, p)` is recognised as `Save`, but the `Payout` repo (which determines the table) and the `Pgx()` provider (which determines transactionality) are silently dropped during edge resolution. The DB detector recovers the table only when the SQL string is itself parsable; with a repo-pattern abstraction (which this codebase uses heavily) the table is invisible.
**Effect:** The MCP output says "DB write" without naming the table, which is exactly what we saw.

### RCA-8: Comments / nearest docstrings are not propagated
`Function.docstring` is captured for the *function*, but the leading comment above a specific call (`// fetch FX rate before locking holding currency`) is dropped.
**Effect:** The "purpose" column of the native trace cannot be reconstructed from the graph at all.

### RCA-9: Flow spines are sets, not sequences (consequence of RCA-1/2/3)
`GenerateFlows(ctx, 3)` does a BFS over `CALLS`. Output is `Flow -[:HAS_STEP]-> Function`, with no ordering, no parallel-fork annotation, no conditional gate. The downstream MCP consumer has no choice but to render these as a grouped table.

---

## 3. Proposed Schema Changes

### 3.1 New node labels

| Label | Purpose | Key properties |
|---|---|---|
| `ControlFlowScope` | Represents a single `if`/`switch case`/`for`/`select case` block enclosing one or more calls. | `kind` (`if`/`else`/`switch_case`/`for`/`select_case`), `condition` (raw source text, ≤200 chars), `filePath`, `startLine`, `endLine`, `parentScopeKey` |
| `ConcurrentScope` | A `go func()`, `errgroup.Go` body, or worker-pool task. | `kind` (`goroutine`/`errgroup`/`waitgroup`/`channel_send`), `enclosingFunction`, `filePath`, `startLine`, `endLine` |
| `TxScope` | A transaction boundary. | `kind` (`pgx_begintx`/`with_tx_func`/`gorm_transaction`), `enclosingFunction`, `filePath`, `startLine`, `endLine`, `isolation` (if parseable) |
| `DeferredCall` *(or use an edge flag — see §3.2)* | Marks a defer'd invocation. | `enclosingFunction`, `filePath`, `line` |

### 3.2 New / extended edges

| Edge | Endpoints | Properties |
|---|---|---|
| `CALLS` *(extended)* | Function → Function | **+`orderIndex` (int, monotonic per caller)**, **+`isDeferred` (bool)**, **+`isGoroutine` (bool)**, **+`literalArgs` (list<string>, the first 3 string-literal args)**, **+`nearestComment` (string, ≤200 chars from the line above)** |
| `UNDER_CONTROL_FLOW` | CALLS edge → ControlFlowScope | `branchDepth` (int) — encoded as an edge-on-edge using a reified-relationship node if Neo4j edge-properties aren't enough |
| `IN_PARALLEL_WITH` | ConcurrentScope → CALLS edge (reified) | `forkPoint` (line where the scope opens) |
| `IN_TX` | TxScope → CALLS edge (reified) | `order` |
| `RESOLVES_TO` | Cross-service `CALLS_SERVICE` → handler `Function` in target service | `confidence` (0.0–1.0), `resolutionMethod` (`proto_matched`/`http_route_matched`/`heuristic`) |
| `ENRICHES` | DBCall → Method on a repo-pattern type (e.g. `payoutRepo.Save`) | `table` (resolved table), `operation`, `derivedFrom` (`tag`/`naming`/`sql_regex`) |

**Note on edge-on-edge:** Neo4j does not support relationship-on-relationship natively. The pragmatic path is to **reify CALLS as a node** (`:CallEdge`) for any call that has a non-trivial scope (i.e. is inside a control-flow, parallel, or tx scope) and connect that node to scope nodes. Plain unconditional calls remain edges to keep the graph thin. Decide the reification threshold by measuring node-count blow-up on a real index (expected 1.5–2× nodes).

### 3.3 New property on `Function` / `Method`

- `bodyTokens` (int) — for cost estimation by MCP consumers.
- `parallelForkCount`, `txOpenCount`, `branchCount` — cached summary stats so a consumer can ask "is this function flat or branchy?" without traversing.

---

## 4. AST Walker Changes (libs/indexer-go/static)

The walker already has `funcRange` + (unused) `branchRange`. The minimal change is to **emit what it already sees**.

### 4.1 Walk additions

Add to the visitor in `call_graph_scip.go`:

```text
on enter ast.IfStmt        → push ControlFlowScope{kind:"if",   condition:src(node.Cond)}
on enter ast.SwitchStmt    → push ControlFlowScope{kind:"switch"}  (push case scopes on CaseClause)
on enter ast.ForStmt       → push ControlFlowScope{kind:"for",   condition:src(node.Cond)}
on enter ast.RangeStmt     → push ControlFlowScope{kind:"range"}
on enter ast.SelectStmt    → push ControlFlowScope{kind:"select"} (push case scopes on CommClause)
on enter ast.GoStmt        → push ConcurrentScope{kind:"goroutine"}
on enter ast.DeferStmt     → set nextCallIsDeferred = true
on enter ast.CallExpr
   if recv is "errgroup" and sel.Name == "Go" → push ConcurrentScope{kind:"errgroup"}
   if matches BeginTx pattern (see §4.2) → push TxScope; pair with matching Commit/Rollback or end of function
```

The current builder already records call sites via the SCIP reference pass; the new scope stack lets it tag each emitted `CALLS` edge with the current top-of-stack scopes.

### 4.2 Transaction-scope heuristic

Two patterns cover ~95% of Go transaction code in this codebase:

1. **Pgx pattern:** `repo, err = repository.Pgx().BeginTx(ctx)` followed (in defer or in flow) by `EndPgxTx(...)`. Match: variable assignment whose RHS is a selector ending in `BeginTx`; close scope at the *innermost* `Commit`/`Rollback`/`EndPgxTx` call on the same variable, or at function return.
2. **Closure pattern:** `WithTx(ctx, func(tx pgx.Tx) error { … })`. Match: any call whose name matches `(?i)withtx|intx|transaction` and last argument is a `FuncLit`; the FuncLit body is the scope.

Both can be detected with pure AST — no SCIP type info required. Wire them into the same scope-stack mechanism as control flow.

### 4.3 Literal argument capture

In the call-emission code path, walk `CallExpr.Args` and collect the first 3 `*ast.BasicLit` of kind `STRING`. Truncate each to 80 chars. Attach as `CALLS.literalArgs`. This is cheap and immediately distinguishes:

- `TriggerPayoutEvent(ctx, id, "created")` from `TriggerPayoutEvent(ctx, id, "initiated")`,
- `Save(ctx, payout)` from `Save(ctx, attempt)` (where the var name leaks the table).

### 4.4 Nearest-comment capture

`go/ast` provides `*ast.CommentGroup` on most nodes. During CallExpr emission, look up the previous comment in the file's `*ast.CommentMap` whose `End()` is on the same or previous line as the call. Attach as `CALLS.nearestComment`, truncated to 200 chars.

### 4.5 Order index

The walker visits the body in source order. A simple counter per enclosing function gives a monotonic `orderIndex` to attach to each emitted CALLS edge. This is the single highest-leverage change — it lets the MCP consumer reconstruct execution sequences with a single sorted query.

### 4.6 Method-chain receiver capture

In `rpc_call_detector.go` (and a new helper in `db_call_detector.go`), when the callee is a `*ast.SelectorExpr`, walk the receiver chain and record the full dotted path (e.g. `repository.Pgx().Payout.Save` → `["repository", "Pgx()", "Payout", "Save"]`). Attach as `CALLS.receiverChain` (list<string>). Then a post-processing step can map `["Payout", "Save"]` → table `payout` via a one-time scan of the `repository/intf/postgres/` directory.

---

## 5. Pipeline Changes (libs/indexer-go/pipeline)

### 5.1 New stage between `IngestCodeStage` and `GenerateFlowSpinesStage`

**`ResolveCrossServiceHandlersStage`** — for each `CALLS_SERVICE` edge whose target is a Service indexed in the same DB, attempt to resolve to a concrete handler `Function` and write a `RESOLVES_TO` edge:

- For gRPC: parse the `.proto` repo (already present in this org as `~/Workspace/Tazapay/proto/`) and match `service.method` to a `Function` whose `signature` matches the generated stub interface name in the target service.
- For HTTP: match `APIRoute.path` + `APIRoute.method` against the target's `APIRoute` nodes.

Confidence on the edge lets downstream consumers grade the inlining.

### 5.2 Rewrite `GenerateFlowSpinesStage` output

Today it writes `Flow -[:HAS_STEP]-> Function` with no order. Make `HAS_STEP` carry:

- `stepIndex` (int) — execution order along the dominant path,
- `branchKey` (string, optional) — when the step is inside a `ControlFlowScope`, name of the branch (e.g. `"if hasPSP"`),
- `parallelGroup` (string, optional) — siblings that share a `parallelGroup` were forked together,
- `inTx` (bool) — convenience flag for "step is in a transaction scope".

These can all be derived from the `CALLS` edges with the new properties from §4 — no new AST work needed once §4 lands.

### 5.3 New stage `GenerateBehavioralSummariesStage`

For each `Flow`, materialise a compact behavioral summary string that the MCP server can return verbatim — e.g.:

```
1. validate request (local)
2. fork: [fetchHoldingFX, fetchDestinationFX] (parallel)
3. if hasPSP: GetMerchantPSPDetails (RPC payout-router)
4. tx { Save(payout), SaveToOutbox, …commit }
5. TriggerPayoutEvent("created") (SQS)
```

This is essentially the native walk, generated **once** at index time so MCP consumers don't re-pay the cost on every query. Cache key: hash of all CALLS edges along the flow.

### 5.4 Speed: cache flow spines by entrypoint

The 3–4 min latency cited by the user is dominated by Stage-3 BFS at query time. Precompute and store the spine + summary at index time; serve from a key-value lookup. (This is the largest single-query latency win available without touching Neo4j.)

---

## 6. Consumer-Side Changes (mcp-server-go)

Once the schema carries order/condition/parallel/tx, the MCP server should:

1. Replace its grouped-table renderer with a **phase-numbered renderer** keyed off `HAS_STEP.stepIndex`.
2. Tag each step with side-effect type derived from the connected call-site node (`DB|RPC|SQS|S3|local`) — same taxonomy the native walk uses.
3. Drop per-call line ranges from summary view; expose them via a follow-up "expand step N" call.
4. For any `CALLS_SERVICE` with a `RESOLVES_TO` edge, inline the *target handler's* one-line summary directly under the step.

These are pure rendering changes; they only become possible after §3 / §4 land.

---

## 7. Suggested Implementation Order

| # | Change | Effort | Unlocks |
|---|---|---|---|
| 1 | `CALLS.orderIndex` + literal/comment capture in walker (§4.3, §4.4, §4.5) | S | Sequenced output, distinguishable calls, "purpose" column |
| 2 | `ControlFlowScope` + reified `CallEdge` for branchy calls (§3.1, §4.1) | M | Conditional gates ("only when hasPSP") |
| 3 | `ConcurrentScope` + `TxScope` detection (§4.1, §4.2) | M | Parallel + tx awareness |
| 4 | Receiver-chain capture + repo→table mapping (§4.6) | S | Named DB tables on every write |
| 5 | `ResolveCrossServiceHandlersStage` (§5.1) | M | One-hop inlining settlement→SOL |
| 6 | Spine v2 with `stepIndex/branchKey/parallelGroup/inTx` (§5.2) | S (depends on 1–3) | Sequenced spines |
| 7 | `GenerateBehavioralSummariesStage` + spine cache (§5.3, §5.4) | M | Latency win, MCP becomes a lookup |
| 8 | MCP renderer rewrite (§6) | S | The output the user actually wanted |

Steps 1, 4 are independent and high-ROI — recommended for a first PR. Steps 2, 3 require the scope-stack refactor and are best grouped. Step 5 needs the local `proto/` repo accessible at index time; gate behind a config flag.

---

## 8. Risks & Open Questions

- **Graph size:** Reifying CALLS as nodes for branchy calls is the single biggest growth driver. Measure on a real index before merging — set a node-cap threshold per Function (e.g. reify only if branchDepth ≥ 2).
- **Condition text fidelity:** Raw `src(node.Cond)` can be long. Truncating to 200 chars loses precision for complex conditions; a hash + first-line approach may be better for retrieval.
- **Tx scope across functions:** A function that opens a tx and passes the tx-bearing repo to a helper will not be caught by a purely AST-local heuristic. Decide whether to do interprocedural propagation (high cost, high value) or accept the limitation and document it.
- **errgroup detection:** Requires a small symbol-resolution step — the visitor must know that the receiver is `*errgroup.Group`. SCIP already has this; pipe the type info through.
- **Comment relevance:** The "nearest comment" heuristic will sometimes attach an unrelated comment. Mitigation: only attach if the comment line is within 2 lines of the call AND its content matches `/^\s*//\s*\S/`. Accept noise; humans/LLMs filter.
- **Cross-service handler resolution:** Will require either a shared proto registry or co-indexed services. The `RESOLVES_TO` edge with a `confidence` property is honest about uncertainty.

---

## 9. Validation Plan

Once steps 1–4 land, re-run the `createPayout` MCP query and compare to the native trace. Concrete acceptance criteria:

1. Output is **ordered** (numbered phases, not grouped tables).
2. Each step lists its **side-effect type** (DB / RPC / SQS / S3 / local).
3. At least 80% of the conditional gates in the native trace appear as `UNDER_CONTROL_FLOW` annotations (`if OBO`, `if first payout`, `if status == requires_approval`, `if hasPSP`, etc.).
4. The two parallel fan-outs (FX holding+destination, account-config+onboarding-KYB) appear as `parallelGroup` siblings.
5. The `Save(payout) / SaveToOutbox / Save(payout_document)` cluster is tagged `inTx=true` with the same `TxScope` key.
6. All `TriggerPayoutEvent(...)` call edges carry their `"created"` / `"initiated"` literal in `literalArgs`.
7. The `CALLS_SERVICE → SOL.PayoutInitiate` edge has a `RESOLVES_TO` pointing at the actual SOL handler function.
8. End-to-end MCP latency for the same query is under 60 seconds (down from 3–4 minutes), thanks to spine caching.

If these eight checks pass, the gap identified in the original analysis is closed. If 1–6 pass but 7–8 don't, the indexing fix is good and the gap reduces to a cross-service / caching problem — separately tractable.

---

## 10. Appendix — File-by-File Touch List

- `libs/core-models-go/node.go` — add `ControlFlowScope`, `ConcurrentScope`, `TxScope`, optional `CallEdge` structs.
- `libs/core-models-go/relationship.go` — add `UNDER_CONTROL_FLOW`, `IN_PARALLEL_WITH`, `IN_TX`, `RESOLVES_TO`, `ENRICHES`; extend `CALLS` properties.
- `libs/schema-go/` — new constraints/indexes for the above; index `CALLS.orderIndex`, `ControlFlowScope.condition`.
- `libs/indexer-go/static/call_graph_scip.go` — scope-stack walker, order index, literal/comment capture, defer/goroutine flagging.
- `libs/indexer-go/static/rpc_call_detector.go` — receiver-chain capture.
- `libs/indexer-go/static/db_call_detector.go` — tx-scope detection, receiver-chain → table mapping.
- `libs/indexer-go/static/event_call_detector.go` — broaden literal capture beyond recognised producers (already close).
- `libs/indexer-go/pipeline/stages.go` — insert `ResolveCrossServiceHandlersStage`, rewrite `GenerateFlowSpinesStage`, add `GenerateBehavioralSummariesStage`.
- `libs/search-go/flow_linker.go` — update `MENTIONS.context` to use new step metadata.
- `apps/mcp-server-go/` (or equivalent) — phase-numbered renderer, side-effect taxonomy.

---

**End of report.**
