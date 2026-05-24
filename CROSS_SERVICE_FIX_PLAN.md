# Cross-Service Stitching Fix — Phased Plan

**Author:** Generated from RCA + native-vs-MCP comparison + post-fix MCP re-evaluation
**Date:** 2026-05-23
**Scope:** Make cross-service RPC tracing actually work in the graph, and make per-RPC node structure mirror the real source.
**Companion docs:** [`INDEXING_RCA_AND_SOLUTION.md`](./INDEXING_RCA_AND_SOLUTION.md), [`Gap fix.md`](./Gap%20fix.md), [`optimisation_implementation.md`](./optimisation_implementation.md)

---

## 1. What's actually broken (and what isn't)

The exploration of the current code overturned two assumptions from the earlier RCA:

| Earlier belief | Actual state |
|---|---|
| CALLS_SERVICE edges are missing from the writer | **They are written** at `libs/indexer-go/static/call_node_buffer.go:189-216` and `rpc_call_detector.go:236-238, 442-446`. |
| Cross-service handler stitching doesn't exist | **A stage exists**: `libs/indexer-go/pipeline/cross_service_resolver.go:56-122` writes `RESOLVES_TO` from GRPCCall to handler Function. |
| Node enrichment (control flow, tx, concurrency) hasn't started | **Partly in tree**: `call_metadata.go`, `control_flow_scope.go`, `concurrent_tx_scope.go` exist per repo listing — likely written but not surfaced in queries. |

So the real failure modes are:

### Root cause A — Service-name resolution silently fails (the #1 bug)

`rpc_call_detector.go:188-208` derives the target service name from the client *type suffix*:

```
fxsvcpb.FXServiceClient   →  strip "Client"  →  "FXService"
solgrpcv1.RemittanceServiceClient → "RemittanceService"
```

Then `resolveTargetService` (lines 274-296) fuzzy-matches that against `Service.name`. But services are indexed with names like `"fx"`, `"settlement-orchestration"`, `"settlement"` — short, kebab-cased, **not the PascalCase proto-service name**. The fuzzy `CONTAINS` match between `"FXService"` and `"fx"` returns nothing. The edge is silently skipped (lines 237, 260 are best-effort).

**One bug, blast radius across every cross-service call in the graph.**

### Root cause B — The cross-service resolver depends on the same broken signal

`cross_service_resolver.go` reads `GRPCCall` nodes and writes `RESOLVES_TO` to the matching handler `Function`. If the `CALLS_SERVICE` edge wasn't written (Root cause A), the resolver has no anchor to chase. Even if it ran, it joins on `targetMethod` against function names — proto-generated handler functions follow a specific pattern (`<ServiceName>Server.<Method>`) that the resolver must know about.

### Root cause C — Helper-shim opacity

Roughly 9 of 22 native-trace RPCs go through `utils.CreateFxQuote`, `utils.SaveToOutbox`, `client.GetAccountConfig` etc. — wrappers that internally hold the actual gRPC client call. The detector only fires inside the helper's body, so the edge from `createPayout → utils.CreateFxQuote` is a plain `CALLS` (function-to-function), not `CALLS_SERVICE`. The cross-service relationship is one hop deeper than queries currently traverse.

### Root cause D — Enrichment landed on disk, never wired to queries

`call_metadata.go` writes `literalArgs`, `nearestComment`, `isDeferred`, `isGoroutine` on `CALLS` edges (per repo state). MCP query results never carry these fields. Either the Cypher `RETURN` clauses don't project them, or the resolvers (`apps/mcp-server-go/main.go:476, 561`) flatten responses too aggressively.

### Root cause E — Mock / placeholder node residue

Per `libs/indexer-go/static/call_graph.go:~80`, external-function call sites get **placeholder nodes** (deprecated path). When users see "mock data" in the graph, they're seeing these stubs. The deprecation flag exists but the code path still runs in some pipelines.

---

## 2. Design intent (the user's mental model)

For an RPC `foo()` whose body has `1 external RPC call + 2 DB calls + 3 sub-functions`, the graph must contain, attached under `foo`:

```
Function(foo)
  ├── CALLS_API → GRPCCall(fxClient.GetByID)        [1 node]
  │                └── CALLS_SERVICE → Service(fx)
  │                       └── RESOLVES_TO → Function(fx.GetByID)   ← cross-service edge
  ├── CALLS_DB  → DBCall(SELECT payout)              [2 nodes]
  ├── CALLS_DB  → DBCall(INSERT payout_attempt)
  ├── CALLS     → Function(buildRequest)             [3 sub-function CALLS]
  ├── CALLS     → Function(validateInput)
  └── CALLS     → Function(serializeResponse)
```

Three invariants:

1. **One node per real call site**, never per "potential" call site.
2. **Zero placeholder/stub nodes** — if the target can't be resolved within the indexed services, mark the edge `unresolved=true` and skip the dangling node.
3. **Cross-service edges always traverse `RESOLVES_TO`** — the boundary between two `Service` subgraphs must be walkable in a single Cypher hop.

---

## 3. Phased plan

### Phase 0 — Diagnostic & ground truth (½ day)

Before changing any code, prove what is and isn't in the graph today. Run these from `apps/cli`:

```cypher
// 0.1 — Are CALLS_SERVICE edges being written at all?
MATCH ()-[r:CALLS_SERVICE]->() RETURN count(r);

// 0.2 — Are they all targeting the same wrong service? (broken-resolver signature)
MATCH (c:GRPCCall)-[r:CALLS_SERVICE]->(s:Service)
RETURN s.name, count(*) ORDER BY count(*) DESC;

// 0.3 — Are GRPCCall nodes accumulating with targetService=""?
MATCH (c:GRPCCall) WHERE c.targetService IS NULL OR c.targetService = ""
RETURN count(c);

// 0.4 — Did the cross-service resolver actually run?
MATCH ()-[r:RESOLVES_TO]->() RETURN count(r);

// 0.5 — Is the enrichment data on edges?
MATCH ()-[r:CALLS]->() WHERE r.literalArgs IS NOT NULL
RETURN count(r);

// 0.6 — Are there placeholder/stub Function nodes? (no filePath = synthetic)
MATCH (f:Function) WHERE f.filePath IS NULL OR f.filePath = ""
RETURN count(f), collect(f.name)[..10];
```

Result of 0.1 vs 0.2 vs 0.3 alone tells us whether the **writer** is broken or only the **resolver** is broken. The plan below assumes 0.2 returns "almost nothing" — confirm before proceeding.

**Deliverable:** `scripts/diagnostic_cross_service.cypher` checked into `scripts/` and a one-page result note.

---

### Phase 1 — Fix cross-service resolution (the priority — 2-3 days)

Goal: every gRPC call whose target service is indexed gets a `CALLS_SERVICE` edge to that `Service` and a `RESOLVES_TO` edge to the handler `Function`. No silent failures.

#### 1.1 — Capture proto-package as first-class identity *(touch: `rpc_call_detector.go`)*

The detector already reads `varPkgMap[recv.Name]` at line 192 but never uses it for resolution. Change the GRPCCall `setProps` (lines 218-229) so it carries:

| New property | Source | Why |
|---|---|---|
| `protoPackage` | `varPkgMap[recv.Name]`, e.g. `fxsvcpb` | Stable identifier from caller side |
| `protoService` | derived from client constructor, e.g. `FXService` from `NewFXServiceClient` | Matches `*Server` type on handler side |
| `protoMethod` | `sel.Sel.Name` | Already captured as `methodName` |
| `clientConstructor` | walk back to `NewFooClient(...)` if visible | Strongest identity signal |

For HTTP calls (`processHTTPCallExpr`, line 310+): capture the constant URL prefix and HTTP method.

#### 1.2 — Replace fuzzy-CONTAINS resolution with deterministic match *(touch: `rpc_call_detector.go:274-296`, new file `service_identity_index.go`)*

Drop the `toLower CONTAINS` heuristic. Build a small in-memory `ServiceIdentityIndex` that the indexer populates per-service when each service's `Service` node is written. Keys:

```
{ protoPackage: "fxsvcpb" }            → Service(fx)
{ protoService: "FXService" }           → Service(fx)
{ aliasName: "fx-service" }            → Service(fx)
```

The mapping data comes from indexing the **`proto/` repo** as a special "contracts" pass (Phase 1.4). Per-service, when `.pb.go` files are indexed, extract `package` declarations and `*ServiceClient` / `*ServiceServer` symbol names and register them against the current `--service` flag.

If a call site resolves to an unknown protoPackage: write the `GRPCCall` node, write `targetServiceUnresolved: true`, **do not write a CALLS_SERVICE edge** (no dangling/wrong edges).

#### 1.3 — Make `ResolveCrossServiceHandlersStage` non-optional and proto-aware *(touch: `libs/indexer-go/pipeline/cross_service_resolver.go`, `stages.go`)*

Current state: stage is optional (line 73 per Explore report). Change `Optional() bool` to `false` — if resolution fails, the pipeline must surface it, not skip silently.

Rewrite the join in `resolveGRPC` (lines 56-122) to match handlers via proto contract:

```cypher
// For each GRPCCall with known proto identity:
MATCH (c:GRPCCall)
WHERE c.protoService IS NOT NULL AND c.protoMethod IS NOT NULL
MATCH (f:Function)
WHERE f.receiverType = c.protoService + 'Server'
  AND f.name = c.protoMethod
  AND f.scopeId <> c.scopeId   // must be a different service
MERGE (c)-[:RESOLVES_TO {confidence: 1.0, basis: 'proto'}]->(f)
```

Confidence levels:
- `1.0 basis: 'proto'` — exact proto package + service + method + receiver match
- `0.7 basis: 'name'` — fallback method-name + service-name (only when protoPackage unknown)
- `< 0.7` — refuse to write; record unresolved with reason

#### 1.4 — Index the `proto/` repo as a contracts pass *(new: `libs/indexer-go/contracts/proto_indexer.go`)*

The user already indexes proto. Make it produce a `ProtoContract` node per gRPC service:

```
ProtoContract { protoPackage, protoService, protoFile }
  ├── DEFINES_METHOD → ProtoMethod { name, requestType, responseType }
  └── (later) IMPLEMENTED_BY → Function (handler) when discovered
  └── (later) CALLED_BY → GRPCCall (caller) when discovered
```

These nodes act as a stable **rendezvous point** between caller and handler indexed in independent runs.

#### 1.5 — Collapse the helper-shim layer *(new: `libs/indexer-go/pipeline/helper_collapse_stage.go`)*

When `createPayout → utils.CreateFxQuote` and `utils.CreateFxQuote → fxClient.GetPayoutFX`, the user-meaningful edge is `createPayout → fxClient.GetPayoutFX`. Add a stage that:

1. Finds chains `Function -[CALLS]-> Function -[CALLS_API]-> GRPCCall` where the middle function has ≤ N (e.g. 5) statements and only one outbound RPC call.
2. Writes a `TRANSITIVE_CALLS_API` edge from the outer Function to the GRPCCall, carrying `viaShim: utils.CreateFxQuote`.
3. **Doesn't delete the original edges** — readers choose collapsed or full granularity.

This is what closes "9 of 22 missing RPCs" without touching the AST walker.

**Phase 1 acceptance test:** after re-indexing settlement + fx + sol + payout-router + account + onboarding + proto, the createPayout flow query returns ≥ 20 of the 22 RPCs the native trace found, with `RESOLVES_TO` edges into the correct handlers.

---

### Phase 2 — Real-node-per-call-site, zero mock data (1-2 days)

Goal: enforce the user's invariant — one graph node per real source-position, no placeholders.

#### 2.1 — Audit and delete the placeholder Function path *(touch: `libs/indexer-go/static/call_graph.go`)*

Per the Explore report, deprecated placeholder-function creation still runs for "calls to external functions." Trace and remove. Edges to unindexed targets should record `external: true` on the edge, not synthesize a fake node.

#### 2.2 — De-dupe by `nodeKey`, not by occurrence *(touch: `call_node_buffer.go`)*

Verify `nodeKey` for each call-site type is exactly `<kind>:<service>:<file>:<targetMethod or table>:<line>`. One per AST call site. Add a Cypher constraint:

```cypher
CREATE CONSTRAINT grpccall_nodekey IF NOT EXISTS
FOR (c:GRPCCall) REQUIRE c.nodeKey IS UNIQUE;
```

Same for DBCall, HTTPCall, OutboxCall.

#### 2.3 — Add `originLine` + `originCol` for every behavioral node

Every GRPCCall/DBCall/HTTPCall/OutboxCall node must carry `originLine`, `originCol`, `originFile`. If any are missing → the node is synthetic → delete it.

**Phase 2 acceptance test:** for a chosen RPC (`PayoutServiceServer.CreatePayout`), the count of GRPCCall + DBCall + HTTPCall + OutboxCall nodes attached under it equals the count of distinct call sites grep finds in the source. Off by ≤ 1 fails the test.

---

### Phase 3 — Surface enrichment that already exists (½ day)

The files `call_metadata.go`, `control_flow_scope.go`, `concurrent_tx_scope.go` are in the tree. They write properties; MCP queries don't return them.

#### 3.1 — Probe what's actually written

```cypher
MATCH ()-[r:CALLS]->()
RETURN keys(r) AS props, count(*) ORDER BY count(*) DESC LIMIT 10;

MATCH (n:Function)
RETURN keys(n) AS props, count(*) ORDER BY count(*) DESC LIMIT 5;
```

#### 3.2 — Wire missing properties into MCP responses *(touch: `apps/mcp-server-go/main.go:476, 561` and corresponding Cypher in `libs/query-go/`)*

For each tool that returns RPC flows (`codegraph_cross_service_flow`, `codegraph_trace_call_graph`, `codegraph_analyze_function`): the Cypher `RETURN` must include — if present on the edge/node — `orderIndex`, `isDeferred`, `isGoroutine`, `literalArgs`, `nearestComment`, `inTx`, `branchKey`, `parallelGroup`.

#### 3.3 — Add per-RPC structured response

A new MCP tool `codegraph_rpc_anatomy(rpcName)` returns, in source order:

```json
{
  "rpc": "PayoutServiceServer.CreatePayout",
  "steps": [
    {"order": 1, "kind": "validation", "function": "validatePayoutRequest"},
    {"order": 2, "kind": "rpc", "target": "fx.CreateFxQuote", "inParallelGroup": "g1"},
    {"order": 3, "kind": "rpc", "target": "balance.GetHoldingCurrency", "inParallelGroup": "g1"},
    {"order": 4, "kind": "db",  "table": "payout", "operation": "INSERT", "inTx": "tx1"},
    {"order": 5, "kind": "outbox", "event": "PayoutCreated", "inTx": "tx1"},
    {"order": 6, "kind": "rpc", "target": "sol.PayoutInitiate", "afterTx": "tx1"}
  ]
}
```

This is the shape the user's mental model demands. It's a thin projection over already-indexed data once Phases 1-2 land.

---

### Phase 4 — Validation harness (1 day)

#### 4.1 — Golden trace for createPayout

Check the native-trace output (the 22-RPC, 8-service, 9-table list already produced) into `test/golden/createpayout.json`. Add a test in `apps/cli` that runs the indexer on a fixture set of all relevant services and asserts the graph contains ≥ 95% of the golden trace edges.

#### 4.2 — Per-phase Cypher validators in `scripts/`

```
scripts/validate_phase1_cross_service.cypher
scripts/validate_phase2_no_mocks.cypher
scripts/validate_phase3_enrichment_surfaced.cypher
```

Each prints PASS / FAIL with counts.

#### 4.3 — MCP-vs-native parity test

Replay the same `createPayout` question through (a) MCP tools only and (b) raw Cypher. Diff the RPC list, DB list, event list. CI fails if MCP coverage drops below the agreed threshold (e.g. 80% of native).

---

## 4. File-by-file touch list

| Phase | File | Action |
|---|---|---|
| 1.1 | `libs/indexer-go/static/rpc_call_detector.go` | Capture `protoPackage`, `protoService`, `clientConstructor` in `setProps` |
| 1.2 | `libs/indexer-go/static/service_identity_index.go` (new) | Deterministic identity map; replace fuzzy CONTAINS |
| 1.2 | `libs/indexer-go/static/rpc_call_detector.go:274-296` | Replace `resolveTargetService` body |
| 1.3 | `libs/indexer-go/pipeline/cross_service_resolver.go` | Proto-aware join; add confidence + basis |
| 1.3 | `libs/indexer-go/pipeline/stages.go` | Make `ResolveCrossServiceHandlersStage` non-optional |
| 1.4 | `libs/indexer-go/contracts/proto_indexer.go` (new) | Parse `proto/` repo into `ProtoContract` nodes |
| 1.4 | `libs/core-models-go/node.go` | Add `ProtoContract`, `ProtoMethod` node labels |
| 1.4 | `libs/core-models-go/relationship.go` | Add `DefinesMethodRel`, `ImplementedByRel`, `CalledByRel` |
| 1.5 | `libs/indexer-go/pipeline/helper_collapse_stage.go` (new) | Synthesize `TRANSITIVE_CALLS_API` edges |
| 2.1 | `libs/indexer-go/static/call_graph.go` | Delete placeholder Function creation |
| 2.2 | `libs/indexer-go/static/call_node_buffer.go` | Add nodeKey uniqueness constraint emission |
| 2.2 | `libs/schema-go/schema.go` | Add UNIQUE constraints for `GRPCCall.nodeKey`, `DBCall.nodeKey`, `HTTPCall.nodeKey`, `OutboxCall.nodeKey` |
| 2.3 | All detectors | Require `originLine`/`originCol`/`originFile`; refuse to emit without |
| 3.2 | `apps/mcp-server-go/main.go` | Project enrichment fields in tool responses |
| 3.2 | `libs/query-go/service_deps.go` | Return enrichment in Cypher |
| 3.3 | `apps/mcp-server-go/main.go` | Add `codegraph_rpc_anatomy` tool |
| 4.* | `scripts/`, `test/golden/` | Validators + golden traces |

---

## 5. Risks

| Risk | Mitigation |
|---|---|
| Helper-collapse generates too many transitive edges | Cap shim depth at N=3, require shim function ≤ 20 statements, require single outbound RPC |
| Proto repo indexing diverges from generated `.pb.go` in service repos | Use proto repo as **primary** identity source; verify per-service `.pb.go` files reference the same `package` name; flag mismatches |
| Existing `RESOLVES_TO` edges get duplicated | Use MERGE with full property key, not just elementId match |
| Making `ResolveCrossService` non-optional breaks single-service indexing flows | Skip the stage gracefully when there's only 1 Service in scope; surface a warning, not failure |
| Schema constraints (Phase 2.2) reject existing data on first apply | Phase 0 includes a one-time `MATCH (c:GRPCCall) WHERE c.nodeKey IS NULL DETACH DELETE c` cleanup with confirmation prompt |

---

## 6. Recommended execution order

Strictly sequential by phase. Within Phase 1: do 1.1 → 1.4 → 1.2 → 1.3 → 1.5 in that order (proto contracts must exist before identity index can populate; identity index must exist before resolver can use it; helper collapse runs last).

Estimated total: **5-7 working days** assuming the in-tree `call_metadata.go` / `control_flow_scope.go` already write correct data and Phase 3 is purely a surfacing fix. If those need rework, add 2 days.

---

## 7. What this plan does NOT do

- Does not introduce tree-sitter or non-Go indexing. That's a separate initiative — see RCA §4.3.
- Does not add runtime/trace augmentation for map-dispatch (e.g., SOL's `providerFuncMap`). That requires either an opt-in registration pattern in the SOL code or a runtime collector — proposed as a follow-up after Phase 4 validates we've closed the static-resolvable gaps.
- Does not change the `Document` / `Flow` / `MENTIONS` subsystem; those were addressed in the 2026-03-01 Bug Fix Initiative ([`INDEX.md`](./INDEX.md)).
