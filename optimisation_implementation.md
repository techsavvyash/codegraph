# CodeGraph Optimisation Implementation Plan

> Created: 2026-05-19  
> Branch: FEAT/cross-service-rpc  
> Source analysis: optimise_index.md

---

## Overview

Six phases building on each other. Phases 1–3 are schema and model changes that everything downstream depends on. Phase 4 is query optimisation (P0 quick wins from the audit). Phase 5 adds RPC→dependency mapping MCP tools. Phase 6 is the hybrid SCIP+AST architecture.

| Phase | Name | What changes | Risk | Est. effort |
|---|---|---|---|---|
| 1 | DBCall Node & Relationship | New node type + CALLS_DB edge + schema indexes | Low | 1 day |
| 2 | Node Type Cleanup | Remove 9 noise node types, keep 7 core + 6 scaffold | Low–Medium | 1 day |
| 3 | Relationship Type Cleanup | Remove 7 noise rel types, add CALLS_DB | Low–Medium | 1 day |
| 4 | Query Optimisation | 1A–1E quick wins, ~5–6x indexing speedup | Low | 3 days |
| 5 | RPC Dependency Mapping | 2 new MCP tools using the cleaned schema | Low | 2 days |
| 6 | Hybrid SCIP+AST | SCIP for types/symbols, AST for call graph | Medium | 5 days |

---

## Phase 1 — DBCall Node & Relationship

The biggest missing capability. Without it the question "which DB tables does this RPC touch?" is unanswerable.

### 1.1 Add `DBCall` node type

**File**: `libs/core-models-go/node.go`

Add constant and struct:

```go
DBCallNode NodeType = "DBCall"

type DBCall struct {
    BaseNode
    Table        string `json:"table" neo4j:"table"`
    Operation    string `json:"operation" neo4j:"operation"` // SELECT, INSERT, UPDATE, DELETE
    QueryPattern string `json:"queryPattern" neo4j:"queryPattern"`
    ServiceName  string `json:"serviceName" neo4j:"serviceName"`
    FilePath     string `json:"filePath" neo4j:"filePath"`
    Line         int    `json:"line" neo4j:"line"`
}
```

**NodeKey pattern**: `dbcall:{scopeId}:{serviceName}:{filePath}:{line}` — per-call-site uniqueness avoids merge conflicts.

### 1.2 Add `CALLS_DB` relationship type

**File**: `libs/core-models-go/relationship.go`

Add constant, struct, and factory case:

```go
// DB Relationships
CallsDBRel RelationshipType = "CALLS_DB"

type CallsDBRelationship struct {
    BaseRelationship
    Line int `json:"line" neo4j:"line"`
}
```

### 1.3 Add schema indexes

**File**: `libs/schema-go/schema.go`

```go
`CREATE INDEX dbcall_nodekey IF NOT EXISTS FOR (n:DBCall) ON (n.nodeKey, n.scopeId)`,
`CREATE INDEX dbcall_table   IF NOT EXISTS FOR (n:DBCall) ON (n.table, n.serviceName)`,
`CREATE INDEX dbcall_op      IF NOT EXISTS FOR (n:DBCall) ON (n.operation)`,
```

### 1.4 Add DBCallDetector

**New file**: `libs/indexer-go/static/db_call_detector.go`

Two-pass Go AST walk mirroring `rpc_call_detector.go`:

- **Pass 1** — build variable→DB-client binding map:
  - `pgxpool.New(...)` / `conn.Acquire(...)` → pgx
  - `sqlx.Connect(...)` / `sqlx.Open(...)` → sqlx
  - `gorm.Open(...)` → gorm
- **Pass 2** — scan call expressions on bound variables:
  - `conn.Query(ctx, "SELECT * FROM payments...")` → parse SQL
  - `db.NamedExec(ctx, "INSERT INTO payments...", ...)` → parse SQL
  - `db.Model(&Payment{}).Find(...)` → infer table from struct name

Table extraction regex:

```go
var tableFromSQL = regexp.MustCompile(
    `(?i)\b(?:FROM|INTO|UPDATE|JOIN)\s+["']?(\w+)["']?`)
```

Output type:

```go
type DBCallInfo struct {
    CallerFunc   string
    CallerNodeKey string
    Operation    string // SELECT, INSERT, UPDATE, DELETE
    Table        string
    QueryPattern string
    FilePath     string
    Line         int
}
```

### 1.5 Wire DBCallDetector into SCIP indexer

**File**: `libs/indexer-go/static/scip_indexer.go`

After the existing RPC and event detection passes:

```go
dbDetector := NewDBCallDetector(serviceName, scopeId)
dbCalls := dbDetector.DetectFromAST(ctx, fileASTs)
// batch-write DBCall nodes and CALLS_DB edges
```

### Deliverables
- [ ] `DBCallNode` constant + `DBCall` struct in `node.go`
- [ ] `CallsDBRel` constant + `CallsDBRelationship` struct + factory case in `relationship.go`
- [ ] 3 schema indexes in `schema.go`
- [ ] `db_call_detector.go` with `DBCallDetector` and `DBCallInfo`
- [ ] `db_call_detector_test.go` with pgx, sqlx, GORM test cases
- [ ] Integration in `scip_indexer.go`

---

## Phase 2 — Node Type Cleanup

Remove noise node types that have zero query value for RPC context. Keep core nodes and scaffold nodes that the traversal depends on.

### Decision table

| Node | Keep/Remove | Action |
|---|---|---|
| `Service` | ✅ Keep (core) | No change |
| `Function` | ✅ Keep (core) | No change |
| `Method` | ✅ Keep (core) | No change |
| `APIRoute` | ✅ Keep (core) | No change |
| `GRPCCall` | ✅ Keep (core) | No change |
| `HTTPCall` | ✅ Keep (core) | No change |
| `OutboxCall` | ✅ Keep (core) | No change |
| `DBCall` | ✅ Keep (core, new) | Added in Phase 1 |
| `File` | 🔸 Keep (scaffold) | Required for `Service→File→Function` hierarchy |
| `Interface` | 🔸 Keep (scaffold) | Required for `IMPLEMENTS` polymorphic resolution |
| `Symbol` | 🔸 Keep (scaffold) | SCIP routing key for cross-file reference resolution |
| `Module` | 🔸 Keep (scaffold) | Proto-package → service name resolution during indexing |
| `Class` | 🔸 Keep (scaffold) | gRPC client structs; needed to resolve receivers |
| `Variable` | 🔸 Keep (scaffold) | Below call-graph level; never traversed in MCP queries |
| `Parameter` | 🔸 Keep (scaffold) | Input shape description; not in any traversal |
| `Comment` | 🔸 Keep (scaffold) | Documentation artifact; not in any RPC query |
| `Document` | ❌ Remove (noise) | Different use case (doc linking) |
| `DocumentChunk` | ❌ Remove (noise) | Sub-doc granularity; irrelevant to RPC context |
| `Flow` | 🔸 Keep (scaffold) | Generated, not indexed; restore as core if populated during indexing |
| `Feature` | ❌ Remove (noise) | Requirements node; not in call graph |
| `PullRequest` | ❌ Remove (noise) | PR overlay; irrelevant to RPC mapping |
| `GeneratedDoc` | ❌ Remove (noise) | LLM output artifact; not queried |
| `GenerationDiagnostic` | ❌ Remove (noise) | Audit artifact; not queried |

### What "remove" means

**Do not delete the structs from `node.go`** — other parts of the system (document indexer, MCP doc tools) still reference them. Instead:

1. Mark removed constants with a `// Deprecated: noise — not written during SCIP/AST indexing` comment
2. In `scip_indexer.go` and `indexer.go` — add a guard that skips writing these node types during the call-graph indexing pass
3. In `schema.go` — do NOT create Neo4j constraints/indexes for noise types (or drop them if they already exist)

### Concrete changes

**File**: `libs/core-models-go/node.go`

```go
// Deprecated: noise nodes — not written during call-graph indexing
DocumentNode  NodeType = "Document"
// Document/DocumentChunk/Flow/Feature/PullRequest/GeneratedDoc/GenerationDiagnostic kept for doc-indexer compatibility
```

**File**: `libs/indexer-go/static/scip_indexer.go`

Add a `noiseNodeTypes` set and skip writes for them in `indexSymbolDefs` and `indexSymbolReferences`:

```go
var noiseNodeTypes = map[models.NodeType]bool{
    models.DocumentNode:  true,
}
```

**File**: `libs/schema-go/schema.go`

Remove (or do not add) `CREATE CONSTRAINT` / `CREATE INDEX` entries for noise types.

### Deliverables
- [x] Deprecation comments on 6 noise `NodeType` constants in `node.go` (Document, DocumentChunk, Feature, PullRequest, GeneratedDoc, GenerationDiagnostic)
- [x] `noiseNodeLabels` guard in `scip_indexer.go` — applied at the label-group merge loop in `indexSymbols`
- [x] AST `indexer.go` — no guard needed; it never writes any noise node types
- [x] Schema cleanup: removed 19 indexes for noise types from `schema.go` (5×Document, 4×DocumentChunk, 3×Feature, 3×PullRequest, 4×GeneratedDoc)

---

## Phase 3 — Relationship Type Cleanup

### Decision table

| Relationship | Keep/Remove | Action |
|---|---|---|
| `CONTAINS` | ✅ Keep (core) | No change |
| `CALLS` | ✅ Keep (core) | No change |
| `EXPOSES_API` | ✅ Keep (core) | No change |
| `CALLS_API` | ✅ Keep (core) | No change |
| `CALLS_SERVICE` | ✅ Keep (core) | No change |
| `CONSUMES_FROM` | ✅ Keep (core) | No change |
| `CALLS_DB` | ✅ Keep (core, new) | Added in Phase 1 |
| `IMPLEMENTS` | 🔸 Keep (scaffold) | Polymorphic dispatch accuracy |
| `DEFINES` | 🔸 Keep (scaffold) | SCIP plumbing |
| `DEPENDS_ON` | 🔸 Keep (scaffold) | Proto-package import resolution |
| `REFERENCES` | ❌ Remove (noise) | Not written by call-graph pipeline; volume too high, not in any RPC traversal |
| `INHERITS_FROM` | ❌ Remove (noise) | Class inheritance; not in RPC traversal |
| `FLOWS_TO` | 🔸 Keep (scaffold) | Not populated; unused |
| `NEXT_EXECUTION` | 🔸 Keep (scaffold) | CFG-level; too granular for service-level queries |
| `DESCRIBES` | 🔸 Keep (scaffold) | Doc-to-code link; different use case |
| `MENTIONS` | 🔸 Keep (scaffold) | Doc mention; different use case |
| `SCHEDULED_BY` | ❌ Remove (noise, marginal) | Cron trigger; marginal value; excluded for now |

### What "remove" means

Same approach as nodes: deprecate constants, add guards in indexers to not write these relationship types, do not create Neo4j indexes for them.

`REFERENCES` is treated as noise in Phase 3: added to `noiseRelTypes`, write guarded with `isNoiseRelType`. Phase 4 step 1B (filtered writes) is superseded.

### Concrete changes

**File**: `libs/core-models-go/relationship.go`

```go
// Deprecated: noise — not written during call-graph indexing
InheritsFromRel RelationshipType = "INHERITS_FROM"
ScheduledByRel  RelationshipType = "SCHEDULED_BY"
```

**File**: `libs/indexer-go/static/scip_indexer.go`

Add `noiseRelTypes` guard before any batch write:

```go
var noiseRelTypes = map[models.RelationshipType]bool{
    models.InheritsFromRel: true,
    models.ScheduledByRel:  true,
}
```

**File**: `libs/schema-go/schema.go`

Remove `CREATE INDEX` entries for noise relationship types.

### Deliverables
- [x] Deprecation comments on `InheritsFromRel` and `ScheduledByRel` in `relationship.go`
- [x] `noiseRelTypes` var + `isNoiseRelType()` guard in `scip_indexer.go`; guard applied at `CreateRelsBatch(IMPLEMENTS)` call
- [x] `call_graph.go` `createDataFlowRelationship` placeholder documents FLOWS_TO/NEXT_EXECUTION exclusion (both in `package static` — shared guard)
- [x] Schema already clean — no noise relationship type indexes exist in `schema.go`

---

## Phase 4 — Query Optimisation (P0 Quick Wins)

Target: ~5–6x reduction in Neo4j round-trips during initial SCIP indexing.  
No schema changes — all changes are in the indexer logic.

### 4A. Single service-level call graph query (100 queries → 1)

**File**: `libs/indexer-go/static/call_graph_generic.go`

**Current**: per-file loop emits one `MATCH (file)-[:CONTAINS]->(fn:Function)` per file. 200 files = 200 round-trips.

**Change**: one query that fetches all functions for the service, group into a `map[string][]FunctionInfo` in memory, then consume the map in the existing per-file loop without any DB calls.

```go
rows, err := neo4jClient.ExecuteQuery(ctx,
    `MATCH (svc:Service {name: $svc, scopeId: $scope})-[:CONTAINS*1..3]->(fn:Function)
     MATCH (fn)<-[:CONTAINS]-(file:File)
     RETURN file.path, fn.startLine, fn.endLine, fn.name, fn.nodeKey, elementId(fn) AS fnId`,
    map[string]any{"svc": serviceName, "scope": scopeId})

functionsByFile := groupFunctionsByFile(rows)

for _, doc := range docs {
    fns := functionsByFile[doc.RelativePath] // no DB call
    // existing call-graph edge construction
}
```

**Impact**: The single largest speedup. ~200 queries → 1.

---

### 4B. Filter SCIP references to function-call occurrences only

**File**: `libs/indexer-go/static/scip_indexer.go`, function `indexSymbolReferences`

**Current**: writes 100% of SCIP references (~50K per 50K LOC service).

**Change**: before batching, keep only occurrences where:
1. `occ.SymbolRoles == 0` (reference, not definition), AND
2. The symbol's `Kind` is a function kind: `Method`, `Function`, `Constructor`, `StaticMethod`

```go
func isFunctionKind(k scip.SymbolInformation_Kind) bool {
    switch k {
    case scip.SymbolInformation_Method,
         scip.SymbolInformation_Function,
         scip.SymbolInformation_Constructor,
         scip.SymbolInformation_StaticMethod:
        return true
    }
    return false
}
```

Filter application:

```go
for _, occ := range doc.Occurrences {
    if occ.SymbolRoles != 0 { continue } // skip definitions
    sym := symbolIndex[occ.Symbol]
    if sym == nil || !isFunctionKind(sym.Kind) { continue }
    filteredRefs = append(filteredRefs, occ)
}
```

**Impact**: ~4x reduction in reference volume. Batch-500 write count drops from ~100 to ~25.

---

### 4C. Batch RPC/event call node writes

**Files**: `rpc_call_detector.go`, `event_call_detector.go`, `scip_rpc_detector.go`

**Current**: every `GRPCCall`, `HTTPCall`, `OutboxCall` node is written individually via `MergeNode()`. 50–500 individual Neo4j calls per service.

**Change**: introduce a shared `callNodeBuffer` that each detector appends to, flushed in one batch at the end of the indexing pass.

```go
type callNodeBuffer struct {
    grpcCalls   []models.GRPCCall
    httpCalls   []models.HTTPCall
    outboxCalls []models.OutboxCall
    dbCalls     []models.DBCall
    callsAPI    []models.CallsAPIRelationship
    callsSvc    []models.CallsServiceRelationship
    callsDB     []models.CallsDBRelationship
}

func (b *callNodeBuffer) flush(ctx context.Context, client neo4jclient.Client) error {
    if err := client.MergeNodesBatch(ctx, "GRPCCall", toAny(b.grpcCalls), 500); err != nil { return err }
    if err := client.MergeNodesBatch(ctx, "HTTPCall", toAny(b.httpCalls), 500); err != nil { return err }
    if err := client.MergeNodesBatch(ctx, "OutboxCall", toAny(b.outboxCalls), 500); err != nil { return err }
    if err := client.MergeNodesBatch(ctx, "DBCall", toAny(b.dbCalls), 500); err != nil { return err }
    // ... relationship batches
    return nil
}
```

Pass `callNodeBuffer` to all detectors. Replace individual `MergeNode` calls with `buffer.append(...)`.

**Impact**: eliminates ~300–1000 individual Neo4j calls for a medium service.

---

### 4D. Pre-load service name index into memory

**Files**: `rpc_call_detector.go`, `scip_rpc_detector.go`

**Current**: every RPC call site issues a live Neo4j query:  
`MATCH (s:Service) WHERE toLower(s.name) CONTAINS $name`.

**Change**: load all services once at indexer startup into a `serviceIndex` struct:

```go
type serviceIndex struct {
    byName  map[string]string // lowercase name → nodeKey
    byProto map[string]string // proto package path → nodeKey
}

func loadServiceIndex(ctx context.Context, client neo4jclient.Client, scopeId string) (*serviceIndex, error) {
    rows, err := client.ExecuteQuery(ctx,
        `MATCH (s:Service {scopeId: $scope}) RETURN s.name, s.nodeKey`,
        map[string]any{"scope": scopeId})
    // build maps...
}
```

Pass `serviceIndex` to all detectors. Replace live queries with O(1) map lookups.

**Impact**: eliminates N Neo4j queries (one per detected RPC call site). Also prevents fuzzy-match false positives.

---

### 4E. Inline degree computation during call graph construction

**File**: `libs/indexer-go/static/call_graph_generic.go` (or wherever degree is computed)

**Current**: after all CALLS edges are written, two post-hoc full-graph Cypher scans compute `inDegree`/`outDegree` for every node.

**Change**: count in/out degrees in memory while building the CALLS edge list, then write degree as a property alongside edge creation in the same batch.

```go
inDegree  := map[string]int{} // nodeKey → count
outDegree := map[string]int{}

for _, edge := range callEdges {
    outDegree[edge.FromKey]++
    inDegree[edge.ToKey]++
}

// write degree as batch property update: UNWIND $nodes AS n MATCH (fn:Function {nodeKey: n.key}) SET fn.inDegree = n.in, fn.outDegree = n.out
```

**Impact**: removes two expensive full-graph scans.

---

### Deliverables — Phase 4
- [ ] `call_graph_generic.go`: single service-level function fetch query + `groupFunctionsByFile` helper
- [ ] `scip_indexer.go`: `isFunctionKind` filter in `indexSymbolReferences`
- [ ] `callNodeBuffer` struct + `flush()` in a new `call_node_buffer.go`
- [ ] `rpc_call_detector.go` + `event_call_detector.go` + `scip_rpc_detector.go`: use buffer instead of individual writes
- [ ] `serviceIndex` struct + `loadServiceIndex` + pass to detectors
- [ ] Inline degree computation; remove post-hoc Cypher degree scans

---

## Phase 5 — RPC Dependency Mapping (New MCP Tools)

Builds directly on the cleaned schema from Phases 1–3 and the batched call nodes from Phase 4.

### 5A. `codegraph_rpc_dependencies` MCP tool

**File**: `apps/mcp-server-go/main.go`

Returns all dependencies for a single named RPC handler: DB tables, downstream gRPC/HTTP services, async events published.

**Cypher**:
```cypher
MATCH (svc:Service {name: $service, scopeId: $scope})
MATCH (svc)-[:CONTAINS*1..4]->(handler:Function)
WHERE handler.name = $rpc OR handler.exposedAs = $rpc

OPTIONAL MATCH (handler)-[:CALLS*0..3]->(c1:Function)-[:CALLS_DB]->(db:DBCall)
OPTIONAL MATCH (handler)-[:CALLS*0..3]->(c2:Function)-[:CALLS_API]->(grpc:GRPCCall)-[:CALLS_SERVICE]->(tSvc:Service)
OPTIONAL MATCH (handler)-[:CALLS*0..3]->(c3:Function)-[:CALLS_API]->(http:HTTPCall)-[:CALLS_SERVICE]->(tHttp:Service)
OPTIONAL MATCH (handler)-[:CALLS*0..3]->(c4:Function)-[:CALLS_API]->(event:OutboxCall)

RETURN
  COLLECT(DISTINCT {table: db.table, op: db.operation, line: db.line}) AS dbCalls,
  COLLECT(DISTINCT {service: tSvc.name, method: grpc.targetMethod}) AS grpcCalls,
  COLLECT(DISTINCT {service: tHttp.name, url: http.url}) AS httpCalls,
  COLLECT(DISTINCT {event: event.eventType, transport: event.transport}) AS events
```

**Tool input schema**:
```json
{
  "service": "string",
  "rpc": "string (handler name or exposed endpoint path)",
  "scope": "string (optional)"
}
```

---

### 5B. `codegraph_service_dependency_map` MCP tool

**File**: `apps/mcp-server-go/main.go`

Returns the full dependency manifest for every exposed RPC in a service. Useful for architecture review and change-impact analysis.

**Cypher**:
```cypher
MATCH (svc:Service {name: $service})-[:CONTAINS*1..4]->(handler:Function)
WHERE handler.exposedAs IS NOT NULL

OPTIONAL MATCH (handler)-[:CALLS*0..3]->(:Function)-[:CALLS_DB]->(db:DBCall)
OPTIONAL MATCH (handler)-[:CALLS*0..3]->(:Function)-[:CALLS_API]->(:GRPCCall|HTTPCall)-[:CALLS_SERVICE]->(dep:Service)

RETURN handler.name AS rpc,
       handler.exposedAs AS endpoint,
       COLLECT(DISTINCT db.table) AS tables,
       COLLECT(DISTINCT dep.name) AS dependsOn
ORDER BY rpc
```

**Tool input schema**:
```json
{
  "service": "string",
  "scope": "string (optional)"
}
```

---

### Deliverables — Phase 5
- [ ] `codegraph_rpc_dependencies` handler in `main.go` (input parsing, Cypher, response formatting)
- [ ] `codegraph_service_dependency_map` handler in `main.go`
- [ ] Both tools registered in `handleToolsList`
- [ ] Both tools dispatched in `handleToolsCall`

---

## Phase 6 — Hybrid SCIP+AST Architecture for Go

Use each indexer for what it does best. Eliminates the redundancy and gets the combined accuracy of both.

| Capability | Who does it | Why |
|---|---|---|
| Symbol definitions | SCIP | Compiler-accurate, cross-file, canonical names |
| IMPLEMENTS edges | SCIP | Compiler-verified; interface satisfaction |
| Function-call REFERENCES (filtered) | SCIP | Cross-file reference accuracy for polymorphic dispatch |
| Call graph (CALLS edges) | AST | Exact body ranges, intra-body traversal, no SCIP overhead |
| RPC detection | AST | Pattern matching on annotations/routing; proven accuracy |
| DB call detection | AST | Direct query string inspection |
| Event detection | AST | Transport binding tracking |
| Non-Go languages | SCIP only | Only option |

### 6A. Refactor Go indexing pipeline

**File**: `libs/indexer-go/static/scip_indexer.go`, `indexer.go`

New entry point for Go services:

```go
func IndexGoService(ctx context.Context, cfg IndexConfig) error {
    // Stage 1: SCIP pass — type oracle
    scipResult, err := runSCIPPass(ctx, cfg) // symbols, IMPLEMENTS, filtered REFERENCES
    if err != nil { return err }

    // Stage 2: AST pass — behavioral graph
    astResult, err := runASTPass(ctx, cfg)   // CALLS, RPC detection, DB calls, events
    if err != nil { return err }

    // Stage 3: Merge — deduplicate shared nodes (Function keys are identical)
    return mergeResults(ctx, cfg, scipResult, astResult)
}
```

### 6B. SCIP pass responsibilities (reduced scope)

From `scip_indexer.go`:
- Write `Symbol`, `Function`, `Method`, `Class`, `Interface` nodes (definitions only)
- Write `IMPLEMENTS` edges
- Write `DEFINES` edges
- Write `REFERENCES` edges (function-call occurrences only, from Phase 4B)
- **Stop**: do not run call graph construction, RPC detection, or event detection

### 6C. AST pass responsibilities (expanded scope)

From `indexer.go`:
- Write `CALLS` edges (already does this well)
- Run `RPCCallDetector` → `GRPCCall`, `HTTPCall`, `APIRoute` nodes + `CALLS_API`, `CALLS_SERVICE`, `EXPOSES_API` edges
- Run `EventCallDetector` → `OutboxCall` nodes + `CALLS_API` edges
- Run `DBCallDetector` → `DBCall` nodes + `CALLS_DB` edges (new, from Phase 1)
- **Stop**: do not write symbol definitions (SCIP owns those)

### 6D. Node key compatibility guarantee

Both passes must use the **same nodeKey format** for `Function` nodes so that SCIP-written function nodes and AST-written call edges point to the same node. Current nodeKey format for functions:  
`fn:{scopeId}:{filePath}:{name}:{startLine}`

Verify this is consistent in both `symbol_analyzer.go` (SCIP) and `indexer.go` (AST) before merging.

### 6E. Non-Go languages (SCIP only)

For TypeScript, Python, Java: SCIP remains the full pipeline. The hybrid path is Go-only. Language router in `indexer_manager.go`:

```go
if cfg.Language == "go" {
    return IndexGoService(ctx, cfg)
}
return IndexViaSCIP(ctx, cfg) // existing path for all other languages
```

### Deliverables — Phase 6
- [ ] `IndexGoService` entry point splitting SCIP and AST passes
- [ ] SCIP pass stripped of call-graph, RPC, event, DB responsibilities
- [ ] AST pass expanded with DB call detection (from Phase 1)
- [ ] NodeKey format audit: `symbol_analyzer.go` vs `indexer.go` — confirm alignment
- [ ] Language router in `indexer_manager.go`
- [ ] Integration test: index a Go service with hybrid mode; assert `DBCall`, `GRPCCall`, `IMPLEMENTS` all present

---

## Implementation Order & Dependencies

```
Phase 1 (DBCall)
    ↓
Phase 2 (Node cleanup) ──┐
Phase 3 (Rel cleanup)  ──┤
    ↓                    │
Phase 4 (Query optimisation) ← no schema deps, but callNodeBuffer must know about DBCall from Phase 1
    ↓
Phase 5 (MCP tools) ← depends on DBCall nodes being populated (Phase 1) and clean schema (2+3)
    ↓
Phase 6 (Hybrid arch) ← refactors how Phases 1–4 are wired together; safe to do last
```

Phases 2 and 3 can be worked in parallel. Phase 4 can start after Phase 1 (DBCall must be included in callNodeBuffer).

---

## Open Questions (from optimise_index.md)

1. **SCIP incremental + cross-file IMPLEMENTS**: If only file A changes but it implements an interface defined in file B, include the full package (not just the changed file) in the partial SCIP run.
2. **DBCall deduplication**: Use per-call-site nodeKey (`dbcall:{scopeId}:{filePath}:{line}`) to avoid merge conflicts under concurrent indexing.
3. **CALLS depth cap**: Use `[:CALLS*0..4]` with `LIMIT 200` in all MCP Cypher queries to prevent timeout on large services.
4. **Test file exclusion**: Exclude test files (`*_test.go`) from SCIP symbol indexing — ~20% volume reduction.
5. **DB detection for non-Go**: Requires SCIP reference pattern matching against `pg`, `psycopg2`, `prisma` symbols. Deferred to a Phase 6 follow-up.
