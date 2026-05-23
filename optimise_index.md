# CodeGraph Indexer Optimization Plan

> Authored: 2026-05-19  
> Branch: FEAT/cross-service-rpc  
> Goal: Reduce SCIP indexing time by 5–10x, add DB-call tracking, enable full RPC→dependency mapping

---

## 0. Symbol Relevance Audit

We define **22 node types** and **16 relationship types** = **38 total symbols**.

The question being answered: *"For a given RPC, what DB calls does it make, what RPC calls does it make (same or different service), and how is it related to other RPCs?"*

### Node Types

| Node | Status | Why |
|---|---|---|
| `Service` | ✅ Core | The container. Every RPC belongs to a service; every outbound call targets one. |
| `Function` | ✅ Core | The handler IS a Function. The intra-service call chain is all Functions. |
| `Method` | ✅ Core | gRPC handlers in Go are Methods on a struct (e.g. `func (s *Server) CreatePayment`). Same as Function. |
| `APIRoute` | ✅ Core | The RPC from the external perspective — path, HTTP method, protocol. The anchor for the question. |
| `GRPCCall` | ✅ Core | Outbound gRPC call site. Carries callerService, targetService, targetMethod. |
| `HTTPCall` | ✅ Core | Outbound HTTP call site. Same role as GRPCCall for HTTP. |
| `OutboxCall` | ✅ Core | Async event publish. Event-driven deps are as real as sync ones. |
| `File` | 🔸 Scaffold | Only needed to traverse `Service→File→Function`. Carries no call semantics itself. |
| `Interface` | 🔸 Scaffold | Required for polymorphic call resolution: if handler calls an interface method, `IMPLEMENTS` tells you the concrete type. Not a query target. |
| `Symbol` | 🔸 Scaffold | Canonical SCIP name node. Acts as a routing key for cross-file reference resolution. Internal indexer plumbing. |
| `Module` | 🔸 Scaffold | Package grouping. Only useful for proto-package → service name resolution during indexing. |
| `Class` | 🔸 Scaffold | gRPC client structs are Classes. Not queried directly for RPC context. |
| `Variable` | ❌ Noise | Implementation detail below call graph. `paymentID := uuid.New()` tells you nothing about dependencies. |
| `Parameter` | ❌ Noise | Describes function input shape. Does not participate in call graph traversal. |
| `Comment` | ❌ Noise | Documentation artifact. Not part of any RPC dependency traversal. |
| `Document` | ❌ Noise | Business/tech doc linking. Different use case entirely. |
| `DocumentChunk` | ❌ Noise | Sub-document granularity for doc linking. Irrelevant to RPC context. |
| `Flow` | ❌ Noise (now) | The right abstraction for "RPC execution path" but currently generated, not indexed. Could become core if populated during indexing. |
| `Feature` | ❌ Noise | Requirements node. No role in call graph traversal. |
| `PullRequest` | ❌ Noise | PR overlay node. Irrelevant to RPC dependency mapping. |
| `GeneratedDoc` | ❌ Noise | LLM output artifact. Not queried for RPC context. |
| `GenerationDiagnostic` | ❌ Noise | Audit/debug artifact. Not queried for RPC context. |
| **`DBCall`** | ❌ **Missing** | **The biggest gap.** Without it, the question "which DB tables does this RPC touch?" is unanswerable. |

**Score: 7 core (32%) · 6 scaffold (27%) · 9 noise (41%) · 1 missing**

---

### Relationship Types

| Relationship | Status | Why |
|---|---|---|
| `CONTAINS` | ✅ Core | `Service→File→Function` hierarchy. Without it you cannot traverse from Service to its handlers. |
| `CALLS` | ✅ Core | The intra-service call chain. Handler may delegate to service layer → repository before making a DB/RPC call. Must traverse this. |
| `EXPOSES_API` | ✅ Core | `Function -[EXPOSES_API]→ APIRoute`. The link between code and the RPC surface. Without it there is no "which function handles /v1/payments/create". |
| `CALLS_API` | ✅ Core | `Function -[CALLS_API]→ GRPCCall/HTTPCall/OutboxCall`. The edge that marks an outbound call. |
| `CALLS_SERVICE` | ✅ Core | `GRPCCall/HTTPCall/OutboxCall -[CALLS_SERVICE]→ Service`. Identifies the target service. |
| `CONSUMES_FROM` | ✅ Core | Consumer side of an OutboxCall. Makes the event-driven dependency explicit: Service A publishes, Service B consumes. |
| `IMPLEMENTS` | 🔸 Scaffold | Needed for accurate polymorphic dispatch in call graph. Not a query target but silently required for correctness. |
| `DEFINES` | 🔸 Scaffold | `Function -[DEFINES]→ Symbol`. SCIP indexer plumbing. Not queried for RPC context. |
| `DEPENDS_ON` | 🔸 Scaffold | Module-level import dependency. Coarser than runtime call dependency. Useful for "which services does this service import proto stubs from" but not for actual call mapping. |
| `REFERENCES` | ❌ Noise | Every identifier usage site (reads, writes, type annotations). ~80% are not function calls. Volume: 5–10x all other relationships combined. Primary source of SCIP indexing slowness. |
| `INHERITS_FROM` | ❌ Noise | Class inheritance. No role in RPC dependency traversal. |
| `FLOWS_TO` | ❌ Noise | Data flow edges. Not populated during indexing. Unused. |
| `NEXT_EXECUTION` | ❌ Noise | Statement-level control flow. Too granular — CFG-level data, not service-level. |
| `DESCRIBES` | ❌ Noise | Doc-to-code link. Different use case. |
| `MENTIONS` | ❌ Noise | Doc mention. Different use case. |
| `SCHEDULED_BY` | ❌ Noise (marginal) | Cron trigger. Relevant for "what triggers this function" but not for "what does this RPC depend on". |
| **`CALLS_DB`** | ❌ **Missing** | **`Function -[CALLS_DB]→ DBCall`.** The missing edge for the DB dependency question. |

**Score: 6 core (37%) · 3 scaffold (19%) · 7 noise (44%) · 1 missing**

---

### The Minimum Graph for Full RPC Context

To answer *"for RPC X: what DB tables, what services, what events?"* the minimum traversal is:

```
(APIRoute {path: "/v1/payments/create"})
  ←[EXPOSES_API]—
(Function "CreatePaymentHandler")          ← handler
  —[CALLS*0..3]→
(Function "CreatePayment")                 ← service layer
  —[CALLS*0..3]→
(Function "InsertPayment")                 ← repository layer
  —[CALLS_DB]→ (DBCall {table:"payments", op:"INSERT"})      ← MISSING TODAY
  —[CALLS_DB]→ (DBCall {table:"accounts", op:"SELECT"})      ← MISSING TODAY
  —[CALLS_API]→ (GRPCCall {targetMethod:"GetAccount"})
    —[CALLS_SERVICE]→ (Service {name:"account-service"})
      —[CONTAINS*]→ (Function "GetAccountHandler")
        ←[EXPOSES_API]— (APIRoute {path:"/grpc/GetAccount"}) ← target RPC
  —[CALLS_API]→ (OutboxCall {event:"payment.created"})
    ←[CONSUMES_FROM]— (Function "HandlePaymentCreated")       ← consumer
      —[CONTAINS*]← (Service {name:"notification-service"})
```

To answer *"which other RPCs of the same service share dependencies with this one?"*:

```
// Two RPCs share a downstream function = they are related
(APIRoute A) ←[EXPOSES_API]— (Fn A) —[CALLS*]→ (SharedFn)
(APIRoute B) ←[EXPOSES_API]— (Fn B) —[CALLS*]→ (SharedFn)
// → A and B are related via SharedFn (same DB table, same downstream service call)
```

**The 7 core nodes + 6 core relationships are sufficient for all of the above. Everything else is either scaffolding for traversal accuracy or noise that adds write cost and query complexity.**

---

### What REFERENCES is Costing You

`REFERENCES` is the single biggest problem in the current graph:

- Volume: ~50,000 per 50K LOC service (vs. ~500 Function nodes, ~2,000 CALLS edges)
- Use: only ~20% of these are function-call references (the ones needed for call graph)
- The other ~80% (variable reads, type annotations, field accesses) have zero value for RPC context
- They consume ~80% of SCIP indexing time (100 batch-writes out of ~165 total queries)
- They inflate Neo4j storage with data that is never traversed in any MCP query

**The `REFERENCES` relationship should be filtered to function-call occurrences only, or dropped entirely in favor of the `CALLS` relationship which already carries the same information more semantically.**

---

## 1. Problem Statement

The AST indexer was fast because it operated entirely in-process over Go source files. The SCIP indexer is slow for three compounding reasons:

1. **Data volume**: SCIP emits every identifier reference (reads, writes, field accesses, type usages). For a 50K LOC service this is ~50,000 references vs. ~500 nodes in AST mode — a 100x difference — yet most of that data is useless for call graph construction.

2. **Query fanout**: The call graph phase issues one Neo4j query per file (`MATCH (file)-[:CONTAINS]->(fn:Function)`). A 200-file service = 200 round-trips, each with connection setup overhead.

3. **No incremental mode**: Every run re-processes everything from scratch. The AST indexer had file-hash change detection; the SCIP indexer does not.

There is also a missing capability entirely: **DB call tracking**. The graph can tell you which service an RPC calls, but not which database tables that handler touches. Without it, "map what this endpoint depends on" is incomplete.

---

## 2. Current Architecture Audit

### 2.1 Pipeline Flow (SCIP)

```
[scip-go / scip-typescript / scip-python]
        ↓ external process, outputs index.scip (100MB–1GB protobuf)
[SCIPParser.ParseFile]
        ↓ deserialize all symbols + all references into memory
[indexSymbolDefs]        ← batch-500, good
[indexSymbolReferences]  ← batch-500, but 100% of references included (too many)
[buildImplementsBatch]   ← batch-500, good
[indexPackageDependencies]
        ↓
[Call graph — per-file loop]   ← N queries for N files  ← BOTTLENECK #1
        ↓
[Cross-service detection]      ← individual node writes  ← BOTTLENECK #2
        ↓
[RPC Detection (Go AST)]       ← individual node writes  ← BOTTLENECK #2
        ↓
[Event Detection]              ← individual node writes  ← BOTTLENECK #2
        ↓
[Secondary store population]
```

### 2.2 Write Pattern Summary

| Write Site | Current Pattern | Queries for 50K LOC service |
|---|---|---|
| Symbol definitions | Batch-500 | ~10 |
| Symbol references | Batch-500 | ~100 (all refs, not just calls) |
| IMPLEMENTS edges | Batch-500 | ~5 |
| Call graph (CALLS edges) | Batch-500 per file | ~200 (one query/file for function list) |
| GRPCCall/HTTPCall nodes | Individual per call site | ~50-500 per service |
| CALLS_SERVICE edges | Individual per call site | same |
| OutboxCall nodes | Individual per event publish | ~20-100 |
| Degree computation | Post-hoc full CALLS scan | 1 expensive query |

---

## 3. Optimization Roadmap

### Phase 1 — Quick Wins (Zero schema changes, low risk, ~3–5x speedup)

#### 1A. Service-level call graph query (100 queries → 1)

**File**: `libs/indexer-go/static/call_graph_generic.go`, `call_graph_scip.go`

**Current code pattern** (per-file loop):
```go
for _, doc := range docs {
    rows, _ = neo4jClient.ExecuteQuery(ctx,
        `MATCH (f:File {path: $path})-[:CONTAINS]->(fn:Function)
         RETURN fn.startLine, fn.endLine, fn.name, fn.nodeKey`,
        map[string]any{"path": doc.RelativePath})
}
```

**Optimised** (one query, group in memory):
```go
rows, _ = neo4jClient.ExecuteQuery(ctx,
    `MATCH (svc:Service {name: $svc, scopeId: $scope})-[:CONTAINS*1..3]->(fn:Function)
     MATCH (fn)<-[:CONTAINS]-(file:File)
     RETURN file.path, fn.startLine, fn.endLine, fn.name, fn.nodeKey, elementId(fn) AS fnId`,
    map[string]any{"svc": serviceName, "scope": scopeId})

functionsByFile := groupByFilePath(rows) // group in Go memory — no extra queries

for _, doc := range docs {
    // Use functionsByFile[doc.RelativePath] — no DB call
}
```

**Impact**: 200 Neo4j round-trips → 1. The most impactful single change.

---

#### 1B. Filter SCIP references to call-site occurrences only

**File**: `libs/indexer-go/static/scip_indexer.go`, `indexSymbolReferences`

SCIP `SymbolInformation.Kind` values for functions: `Method`, `Function`, `Constructor`, `StaticMethod`.

**Current**: stores 100% of references.

**Optimised**:
```go
for _, doc := range scipIndex.Documents {
    for _, occ := range doc.Occurrences {
        if occ.SymbolRoles == 0 { // reference (not definition)
            sym := symbolIndex[occ.Symbol]
            if sym == nil { continue }
            if isFunctionKind(sym.Kind) { // only keep call-site references
                references = append(references, occ)
            }
        }
    }
}
```

**Impact**: Reduces reference volume ~4x. Directly cuts batch-500 write count from ~100 to ~25.

---

#### 1C. Batch RPC/event call node writes

**Files**: `rpc_call_detector.go`, `event_call_detector.go`, `scip_rpc_detector.go`

**Current**: Every `GRPCCall`, `HTTPCall`, `OutboxCall` is an individual `MergeNode()` call.

**Optimised**: Accumulate into slices during detection, flush in one `MergeNodesBatch()` at the end.

```go
type callNodeBuffer struct {
    grpcCalls   []models.GRPCCallNode
    httpCalls   []models.HTTPCallNode
    outboxCalls []models.OutboxCallNode
    callsAPI    []models.CallsAPIRel
    callsSvc    []models.CallsServiceRel
}

func (b *callNodeBuffer) flush(ctx context.Context, client neo4j.Client) error {
    client.MergeNodesBatch(ctx, "GRPCCall", b.grpcCalls, 500)
    client.MergeNodesBatch(ctx, "HTTPCall", b.httpCalls, 500)
    client.MergeNodesBatch(ctx, "OutboxCall", b.outboxCalls, 500)
    client.CreateRelsBatch(ctx, "CALLS_API", b.callsAPI, 500)
    client.CreateRelsBatch(ctx, "CALLS_SERVICE", b.callsSvc, 500)
    return nil
}
```

**Impact**: Eliminates ~300–1000 individual Neo4j calls for a medium-size service.

---

#### 1D. Pre-load service name index into memory

**Files**: `rpc_call_detector.go`, `scip_rpc_detector.go`

**Current**: Every RPC call creation does a live `MATCH (s:Service) WHERE toLower(s.name) CONTAINS $name`.

**Optimised**: Load once at indexer startup:
```go
type serviceIndex struct {
    byName  map[string]string // lowercase name → nodeKey
    byProto map[string]string // proto package path → nodeKey
}
```

Pass to all detectors. Resolve in O(1) via map lookup. Fixes potential fuzzy-match false positives too.

**Impact**: Eliminates N Neo4j queries (one per detected RPC call).

---

#### 1E. Inline degree computation during call graph

**Current**: Two post-hoc full-graph Cypher scans for `inDegree`/`outDegree`.

**Optimised**: Count in memory during CALLS edge construction, write as batch property update alongside edge creation.

**Impact**: Removes two expensive full-graph scans.

---

### Phase 2 — Incremental Indexing (~10x for re-index runs)

#### 2A. File-hash tracking for SCIP

Add `scip_hash` property to `File` node. On re-index:
1. Batch-query all `File.scip_hash` for service (one query)
2. Walk directory, compute sha256 per file
3. Only pass changed files to `scip-go --files <list>` (scip-go supports partial file indexing)
4. Only process SCIP documents for changed files
5. Update `scip_hash` after successful write

**Impact**: For 10% file change rate → 90% reduction in SCIP work.

---

#### 2B. Differential symbol/reference updates

When a file changes, tombstone its existing graph before re-writing:
```cypher
MATCH (f:File {path: $path, scopeId: $scope})-[:CONTAINS]->(fn:Function)
DETACH DELETE fn
```

Then write new Function/Symbol/CALLS nodes for that file. Safe because nodeKeys are deterministic.

---

### Phase 3 — DB Call Detection (New capability)

#### 3A. DBCallDetector (Go AST-based)

**New file**: `libs/indexer-go/static/db_call_detector.go`

Mirrors `RPCCallDetector` structure. Two-pass scan:

- **Pass 1**: Track variable → DB client bindings
  - `pgx`: `pgxpool.New(...)`, `conn.Acquire(...)`
  - `sqlx`: `sqlx.Connect(...)`, `sqlx.Open(...)`
  - `GORM`: `gorm.Open(...)`
- **Pass 2**: Scan for query calls
  - `conn.Query(ctx, "SELECT * FROM payments WHERE...")` → extract SQL, parse table name
  - `db.NamedExec(ctx, "INSERT INTO payments...", ...)` → extract SQL, parse table + operation
  - `db.Model(&Payment{}).Find(...)` → infer table from struct name (GORM)

**Table extraction from SQL**:
```go
var tableFromSQL = regexp.MustCompile(
    `(?i)\b(?:FROM|INTO|UPDATE|JOIN)\s+["']?(\w+)["']?`)
```

**Output**: `DBCallInfo{CallerFunc, Operation, Table, QuerySnippet, Line}`

---

#### 3B. New node type: `DBCall`

**File**: `libs/core-models-go/node.go`

```go
DBCallNode NodeType = "DBCall"

type DBCall struct {
    BaseNode
    Table        string `json:"table"`
    Operation    string `json:"operation"` // SELECT, INSERT, UPDATE, DELETE
    QueryPattern string `json:"queryPattern"`
    ServiceName  string `json:"serviceName"`
    FilePath     string `json:"filePath"`
    Line         int    `json:"line"`
}
```

**New relationship**: `CALLS_DB` — `Function -[CALLS_DB]→ DBCall`

---

#### 3C. Schema indexes

```go
`CREATE INDEX dbcall_nodekey IF NOT EXISTS FOR (n:DBCall) ON (n.nodeKey, n.scopeId)`,
`CREATE INDEX dbcall_table   IF NOT EXISTS FOR (n:DBCall) ON (n.table, n.serviceName)`,
`CREATE INDEX dbcall_op      IF NOT EXISTS FOR (n:DBCall) ON (n.operation)`,
```

---

### Phase 4 — RPC→Dependency Mapping (The business value)

#### 4A. MCP tool: `codegraph_rpc_dependencies`

```cypher
MATCH (svc:Service {name: $service, scopeId: $scope})
MATCH (svc)-[:CONTAINS*1..4]->(handler:Function)
WHERE handler.name = $rpc OR handler.exposedAs = $rpc

OPTIONAL MATCH (handler)-[:CALLS*0..3]->(callee:Function)-[:CALLS_DB]->(db:DBCall)
OPTIONAL MATCH (handler)-[:CALLS*0..3]->(callee2:Function)-[:CALLS_API]->(grpc:GRPCCall)-[:CALLS_SERVICE]->(targetSvc:Service)
OPTIONAL MATCH (handler)-[:CALLS*0..3]->(callee3:Function)-[:CALLS_API]->(http:HTTPCall)-[:CALLS_SERVICE]->(targetHttp:Service)
OPTIONAL MATCH (handler)-[:CALLS*0..3]->(callee4:Function)-[:CALLS_API]->(event:OutboxCall)

RETURN 
  COLLECT(DISTINCT {table: db.table, op: db.operation}) AS dbCalls,
  COLLECT(DISTINCT {service: targetSvc.name, method: grpc.targetMethod}) AS grpcCalls,
  COLLECT(DISTINCT {service: targetHttp.name, url: http.url}) AS httpCalls,
  COLLECT(DISTINCT {event: event.eventType, transport: event.transport}) AS events
```

#### 4B. MCP tool: `codegraph_service_dependency_map`

Per-RPC dependency manifest for a whole service:

```cypher
MATCH (svc:Service {name: $service})-[:CONTAINS*1..4]->(handler:Function)
WHERE handler.exposedAs IS NOT NULL

OPTIONAL MATCH (handler)-[:CALLS*0..3]->(:Function)-[:CALLS_DB]->(db:DBCall)
OPTIONAL MATCH (handler)-[:CALLS*0..3]->(:Function)-[:CALLS_API]->(:GRPCCall|HTTPCall)-[:CALLS_SERVICE]->(dep:Service)

RETURN handler.name AS rpc, handler.exposedAs AS endpoint,
       COLLECT(DISTINCT db.table) AS tables,
       COLLECT(DISTINCT dep.name) AS dependsOn
ORDER BY rpc
```

---

### Phase 5 — Architecture: Hybrid SCIP+AST for Go

| Capability | Use SCIP | Use AST |
|---|---|---|
| Symbol definitions | ✅ (semantic, compiler-accurate) | |
| IMPLEMENTS edges | ✅ (compiler-verified) | |
| Call references | Filter to function-call only | |
| Call graph | | ✅ (body ranges exact) |
| RPC detection | | ✅ (AST heuristics, excellent) |
| DB call detection | | ✅ (direct query inspection) |
| Event detection | | ✅ (transport binding tracking) |
| Non-Go languages | ✅ (only option) | N/A |

For Go: SCIP = type-system oracle (definitions, implements). AST = behavioral graph (calls, RPCs, DB, events). Combined = full picture at half the volume.

---

## 4. Prioritized Implementation Order

| Priority | Change | Effort | Speedup | Risk |
|---|---|---|---|---|
| P0 | 1A: Single service-level call graph query | 1 day | ~3x | Low |
| P0 | 1B: Filter SCIP references to call-sites only | 0.5 day | ~2x | Low |
| P0 | 1C: Batch RPC/event node writes | 1 day | ~1.5x | Low |
| P0 | 1D: Pre-load service index in memory | 0.5 day | ~1.2x | Low |
| P0 | 1E: Inline degree computation | 0.5 day | ~1.1x | Low |
| P1 | 2A/2B: Incremental SCIP indexing | 3 days | ~10x re-runs | Medium |
| P1 | 3A-3D: DB call detection + schema | 3 days | N/A (new cap) | Low |
| P2 | 4A: MCP `rpc_dependencies` tool | 1 day | N/A (new cap) | Low |
| P2 | 4B: MCP `service_dependency_map` tool | 1 day | N/A (new cap) | Low |
| P3 | Phase 5: Hybrid SCIP+AST for Go | 5 days | ~5x | Medium |

**P0 combined: ~5–6x speedup for initial index. P0+P1: ~10x initial + ~50x re-index.**

---

## 5. What NOT to Change

- **Batch size of 500**: Already optimal for Neo4j community edition.
- **Symbol definitions indexing**: Already batched and efficient.
- **IMPLEMENTS batch**: Already batched.
- **AST indexer for non-SCIP Go**: Already fast.
- **Neo4j indexes**: Already comprehensive. Only DBCall additions needed.
- **Tri-store population**: Deferred is already correct.

---

## 6. Open Questions

1. **SCIP incremental + cross-file IMPLEMENTS**: If only file A changes but implements interface in file B, include the full package (not just the file) in the partial SCIP run.
2. **DBCall deduplication**: Per-call-site nodes (not shared nodes) with a `queryHash` property avoids merge conflicts under concurrent indexing.
3. **CALLS depth cap**: Use depth 4 max with `LIMIT 200` to prevent query timeout on large services.
4. **Test file exclusion**: Exclude test symbols from SCIP entirely (~20% volume reduction).
5. **DB detection for non-Go**: Requires SCIP reference pattern matching against `pg`, `psycopg2`, `prisma` symbols. Phase 5 territory.
