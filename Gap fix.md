# Cross-Service RPC & HTTP Call Tracking — Gap Fix Plan

## Background & Problem Statement

CodeGraph currently builds an accurate **intra-service** call graph: every function-to-function
call within a single indexed service is captured as a `CALLS` edge in Neo4j. However, the moment
a call crosses a service boundary — via gRPC, HTTP, or an async message queue — the graph goes
silent. The call either disappears into an unconnected placeholder node or is never written at all.

This means:

- `codegraph_trace_call_graph` stops at the service edge and never shows what external RPC is invoked.
- `codegraph_cross_service_calls` always returns empty results because the underlying data was
  never indexed.
- `codegraph_service_api_calls` always returns empty results for the same reason.
- The service dependency graph (`DEPENDS_ON`) only reflects Go package imports, not actual runtime
  call relationships.

This document describes **every gap** and the **phased plan** to fix all of them. Each phase is
self-contained and produces observable, testable improvements before the next phase begins.

---

## Repo Layout Orientation (for the worker LLM)

```
codegraph/
  libs/
    core-models-go/
      node.go           ← all Neo4j node type definitions
      relationship.go   ← all Neo4j relationship type definitions
    indexer-go/
      static/
        call_graph.go           ← Go AST call graph extractor (intra-service)
        call_graph_generic.go   ← SCIP-based call graph for non-Go (intra-service)
        call_graph_scip.go      ← SCIP-specific Go call graph
        api_surface.go          ← detects which functions are API handlers
        scip_indexer.go         ← top-level SCIP indexing orchestration
        indexer.go              ← top-level AST indexing orchestration
  apps/
    mcp-server-go/
      main.go   ← all 28 MCP tool handlers in one file (~3067 lines)
```

Key facts:
- All graph writes go through `libs/neo4j-go` (`client.MergeNode`, `client.MergeRelationship`,
  `client.ExecuteQuery`).
- Node types are string constants in `core-models-go/node.go` (`NodeType`).
- Relationship types are string constants in `core-models-go/relationship.go` (`RelationshipType`).
- The SCIP indexer runs first (symbol/reference ingestion), then the call graph builder runs as a
  post-processing step over those symbols and references.
- `ScopeContext` (from `libs/core-models-go/scope.go`) must be attached to every node/relationship
  written so that multi-tenant / multi-workspace queries work correctly.

---

## Complete Gap Inventory

| ID | Gap | File(s) | Impact |
|----|-----|---------|--------|
| G1 | `GRPCCall`, `HTTPCall`, `OutboxCall` node types do not exist | `node.go` | All downstream work blocked |
| G2 | `CALLS_API` relationship is defined but never written by any indexer | `relationship.go`, all indexers | `handleServiceAPICallsTool` always empty |
| G3 | `CALLS_SERVICE` relationship is defined but never written | `relationship.go`, all indexers | Cross-service graph has no edges |
| G4 | gRPC outbound call sites are not detected | `call_graph.go`, `call_graph_generic.go` | gRPC calls invisible |
| G5 | HTTP outbound call sites are not detected | same | HTTP calls invisible |
| G6 | Async/outbox publish sites are not detected | no file exists | Queue-based flows invisible |
| G7 | `call_graph_generic.go` filters out cross-service symbols via `packageName` | `call_graph_generic.go:228` | Cross-service CALLS never created |
| G8 | `call_graph.go` creates unlinked placeholder `Function` nodes for external calls | `call_graph.go:206` | Dead-end islands, no Service link |
| G9 | `handleCrossServiceCallsTool` Cypher uses unfiltered `shortestPath` on all rel types | `mcp-server-go/main.go:2178` | Returns wrong/garbage paths |
| G10 | `handleTraceCallGraphTool` / `handleGenerateFlowsTool` stop at service boundary | `mcp-server-go/main.go` | Trace never exits the originating service |
| G11 | `handleServiceAPICallsTool` queries for `HTTPCall`/`SDKCall` nodes that don't exist | `mcp-server-go/main.go:2115` | Always returns empty |
| G12 | No MCP tool to trace a full end-to-end flow across N services | `mcp-server-go/main.go` | Cannot answer "what does this RPC fan out to?" |

---

## Phase 1 — Schema Foundation

**Goal:** Add the missing node types and relationship constants so that all subsequent phases have
a typed, stable schema to write into. No indexing logic changes yet — this phase is purely
additive to the data model.

**Files to change:**

### 1a. `libs/core-models-go/node.go`

Add three new `NodeType` constants after `APIRouteNode`:

```go
GRPCCallNode   NodeType = "GRPCCall"
HTTPCallNode   NodeType = "HTTPCall"
OutboxCallNode NodeType = "OutboxCall"
```

Add corresponding Go structs:

```go
// GRPCCall represents a gRPC outbound call site within a function body.
type GRPCCall struct {
    BaseNode
    CallerService string `json:"callerService" neo4j:"callerService"` // name of the Service making the call
    TargetService string `json:"targetService" neo4j:"targetService"` // resolved target Service name (if known)
    TargetMethod  string `json:"targetMethod"  neo4j:"targetMethod"`  // e.g. "PaymentService.CreatePayment"
    ProtoPackage  string `json:"protoPackage"  neo4j:"protoPackage"`  // e.g. "payment.grpc.v1"
    FilePath      string `json:"filePath"      neo4j:"filePath"`
    Line          int    `json:"line"          neo4j:"line"`
}

// HTTPCall represents an outbound HTTP call site within a function body.
type HTTPCall struct {
    BaseNode
    CallerService string `json:"callerService" neo4j:"callerService"`
    TargetService string `json:"targetService" neo4j:"targetService"` // resolved, if determinable
    URL           string `json:"url"           neo4j:"url"`           // literal URL or template
    Method        string `json:"method"        neo4j:"method"`        // GET, POST, etc. ("ANY" if unknown)
    FilePath      string `json:"filePath"      neo4j:"filePath"`
    Line          int    `json:"line"          neo4j:"line"`
}

// OutboxCall represents a message publish/enqueue site (outbox, SQS, Kafka, etc.).
type OutboxCall struct {
    BaseNode
    CallerService  string `json:"callerService"  neo4j:"callerService"`
    Transport      string `json:"transport"      neo4j:"transport"`      // "outbox", "sqs", "kafka", "nats"
    EventType      string `json:"eventType"      neo4j:"eventType"`      // e.g. "payment.created"
    QueueOrTopic   string `json:"queueOrTopic"   neo4j:"queueOrTopic"`
    FilePath       string `json:"filePath"       neo4j:"filePath"`
    Line           int    `json:"line"           neo4j:"line"`
}
```

Also add the three new types to `NodeFactory`.

### 1b. `libs/core-models-go/relationship.go`

The constants `CallsAPIRel` and `CallsServiceRel` already exist. No new constants needed.
However, add a typed struct for `CallsServiceRelationship` (currently missing):

```go
// CallsServiceRelationship connects a GRPCCall/HTTPCall/OutboxCall node to a target Service node.
type CallsServiceRelationship struct {
    BaseRelationship
    Protocol string `json:"protocol" neo4j:"protocol"` // "grpc", "http", "outbox", "sqs", "kafka"
}
```

Also add the new types to `RelationshipFactory`.

**Done when:** `go build ./libs/core-models-go/...` passes with zero errors.

---

## Phase 2 — gRPC Call Site Detector (Indexer)

**Goal:** During indexing, detect every outbound gRPC call site in Go source files and write:
- A `GRPCCall` node for the call site.
- A `CALLS_API` edge from the calling `Function` node to the `GRPCCall` node.
- A `CALLS_SERVICE` edge from the `GRPCCall` node to the target `Service` node (if the service
  is already indexed in the same graph).

**Background — how gRPC calls look in Go:**

Generated gRPC client code always follows this pattern:
```go
// Step 1: construct client (usually in init or a constructor)
client := pb.NewPaymentServiceClient(conn)
// or
client := paymentv1.NewPaymentServiceClient(conn)

// Step 2: call a method
resp, err := client.CreatePayment(ctx, &pb.CreatePaymentRequest{...})
```

The client constructor name is always `New<ServiceName>Client` (generated by `protoc-gen-go-grpc`).
The method call is a selector expression: `<clientVar>.<MethodName>(ctx, req)`.

**Detection strategy for Go AST indexer:**

Inside `call_graph.go`, extend `processCallExpression` to detect and classify RPC call sites
before falling back to the generic `CALLS` logic:

1. Check if the callee is a `SelectorExpr` whose receiver type (resolved via a simple variable
   type tracker) ends in `Client` and whose package path contains `grpc` or a known proto package
   suffix (e.g. `.grpc.v1`, `.grpcv1`). If yes → it is a gRPC call.
2. Extract `TargetMethod` as `<ReceiverTypeName>.<MethodName>` (strip the `Client` suffix to get
   the service name: `PaymentServiceClient` → `PaymentService`).
3. Write a `GRPCCall` node via `client.MergeNode`.
4. Write a `CALLS_API` edge from the current `Function` node to the `GRPCCall` node.
5. Attempt service resolution: query Neo4j for a `Service` whose `name` or `packageName` fuzzy-
   matches the extracted service name. If found, write a `CALLS_SERVICE` edge.

**New file to create:** `libs/indexer-go/static/rpc_call_detector.go`

```
// Package static — rpc_call_detector.go
//
// RPCCallDetector detects outbound gRPC and HTTP call sites in Go AST and
// writes GRPCCall / HTTPCall nodes with CALLS_API and CALLS_SERVICE edges.
```

Struct:
```go
type RPCCallDetector struct {
    client       *neo4j.Client
    serviceName  string
    scopeCtx     models.ScopeContext
    // varTypeMap tracks local variable name → inferred type within the current function.
    // Reset per function. Key: variable name. Value: type string (e.g. "PaymentServiceClient").
    varTypeMap   map[string]string
}
```

Key methods:
- `DetectInFunction(ctx, funcDecl *ast.FuncDecl, callerFuncID, filePath string) error`
  — walks the AST, populates `varTypeMap` from assignment statements, then processes call expressions.
- `processAssignment(assignStmt *ast.AssignStmt)` — if RHS is a `New*Client(...)` call, record
  variable name → client type in `varTypeMap`.
- `processCallExpr(ctx, callExpr *ast.CallExpr, callerFuncID, filePath string, line int)`
  — checks if receiver is in `varTypeMap` with a `*Client` type. If yes, creates `GRPCCall` node
  and edges.
- `resolveTargetService(ctx, serviceName string) string` — queries Neo4j for matching Service node.

**Wire it in:** In `indexer.go` (the AST indexer's main orchestration loop), after creating
the `Function` node and before calling `ExtractCallsFromFunction`, also call
`rpcDetector.DetectInFunction(...)`.

**Done when:**
- After re-indexing a service that has gRPC client calls, Neo4j contains `GRPCCall` nodes with
  correct `targetMethod` and `callerService` properties.
- `CALLS_API` edges exist from the caller Function to the GRPCCall node.
- `CALLS_SERVICE` edges exist from the GRPCCall node to the target Service node (where resolvable).

---

## Phase 3 — HTTP Outbound Call Site Detector (Indexer)

**Goal:** Same as Phase 2 but for HTTP calls. HTTP clients in Go are more varied than gRPC, so
detection is pattern-based.

**Patterns to detect (Go AST):**

| Pattern | Detection signal |
|---------|-----------------|
| `http.NewRequest(method, url, body)` | `http.NewRequest` selector on `net/http` |
| `http.Get(url)` / `http.Post(url, ...)` | `http.Get`, `http.Post` etc. |
| `client.Do(req)` where client is `*http.Client` | Receiver type `*http.Client` in varTypeMap |
| `resty.R().Get(url)` | `resty` package selector |
| Custom wrapper calls | Configurable: list of known wrapper function names |

**Add to `rpc_call_detector.go`** (extend the same file from Phase 2):

New method: `processHTTPCallExpr(ctx, callExpr, callerFuncID, filePath, line)`:
1. Check if it matches any HTTP pattern above.
2. Extract `Method` (GET/POST/etc.) and `URL` (the first string literal argument, or `"dynamic"`
   if it is a variable).
3. Attempt to resolve `TargetService` by matching the URL literal against known service base URLs
   stored as properties on `Service` nodes (add a `baseURL` property to the `Service` node type
   in Phase 1 if it doesn't exist). If URL is dynamic, leave `TargetService` empty.
4. Write `HTTPCall` node, `CALLS_API` edge from caller Function, `CALLS_SERVICE` edge if resolved.

**Done when:** After re-indexing, `HTTPCall` nodes exist for all detected HTTP client call sites.

---

## Phase 4 — SCIP / Generic Language Call Detector ✅ DONE

### Phase 4 Implementation Record

**Files changed:**
- `libs/indexer-go/static/call_graph_generic.go`

#### What was added

`processFile` now has a **Step 6 (cross-service pass)** after the existing intra-service CALLS pass:

```go
crossRefs, err := cg.getCrossServiceRefsInFile(ctx, filePath)
// ... for each ref, find enclosing function, call writeCrossServiceCall
```

**`getCrossServiceRefsInFile`** — new Cypher query:
```cypher
MATCH (ref:Reference {filePath: $filePath, scopeId: $scopeId})
      -[:REFERENCES]->(sym:Symbol)
      <-[:DEFINES]-(target)
WHERE (target:Function OR target:Method)
  AND NOT target.signature CONTAINS $packageName
MATCH (targetFile:File)-[:CONTAINS]->(target)
MATCH (targetSvc:Service)-[:CONTAINS]->(targetFile)
WHERE targetSvc.name <> $serviceName
RETURN ref.startLine, sym.symbol, sym.displayName,
       targetSvc.name, elementId(targetSvc)
LIMIT 500
```

The `NOT signature CONTAINS $packageName` guard ensures no overlap with the existing intra-service
CALLS pass. The `Service -[:CONTAINS]-> File -[:CONTAINS]-> target` chain eliminates all
third-party/stdlib noise — only symbols whose owning service is already indexed are returned.

**`writeCrossServiceCall`** — dispatches to `writeGenericGRPCCall` or `writeGenericHTTPCall`
based on `isGRPCLikeSymbol(symbol)` (checks for `/grpc/`, `/pb/`, `/proto/`, `_pb2_grpc`,
`@grpc/grpc-js` in the FQN).

Both write methods:
- Create `GRPCCall`/`HTTPCall` node via `MergeNode` (idempotent).
- Write `CALLS_API` edge from enclosing function.
- Write `CALLS_SERVICE` edge to the already-known `targetServiceID` (from the query — no
  additional fuzzy resolution needed since the service ID comes directly from the graph traversal).

Node key formats:
- gRPC: `grpccall:generic:<service>:<filePath>:<SvcName>.<method>:<line>`
- HTTP: `httpcall:generic:<service>:<filePath>:<METHOD>:<line>`

**`isGRPCLikeSymbol`** — helper that checks lowercase FQN for gRPC indicators.

**`currentUnixTime`** — small helper returning `time.Now().UTC().Unix()` (avoids repeating the
expression in two write methods).

**Reuses** `parseGRPCSymbol`, `extractProtoPackage`, `inferHTTPMethodFromSymbol` from
`scip_rpc_detector.go` (same package, no import needed).

**Scope propagation**: `cg.scopeCtx.Props()` is applied to every new node, consistent with the
existing pattern in the file.

**Known limitations:**
- 500-result limit per file; files with >500 distinct cross-service references will be truncated.
- `targetMethod` for HTTP calls is `"dynamic"` — the symbol FQN in SCIP for HTTP handler methods
  does not embed an HTTP verb, so `inferHTTPMethodFromSymbol` falls back to `"ANY"` unless the
  verb appears in the display name or FQN.
- The 2-hop `Service -[:CONTAINS]-> File -[:CONTAINS]-> target` traversal works for both SCIP
  and AST-indexed services; deeper nesting (Module → Function) is not followed since
  `getReferencesInFile` already confirmed function nodes are direct children of File nodes in
  SCIP-indexed graphs.

---

## Phase 4 — SCIP / Generic Language Call Detector (original plan)

**Goal:** Apply the same cross-service detection to non-Go services indexed via SCIP
(TypeScript, Python, etc.).

**Problem with current `call_graph_generic.go`:**

The `getReferencesInFile` query at line 228 filters:
```cypher
AND directTarget.signature CONTAINS $packageName
```
This means any symbol whose package is not the current service is silently dropped. This was
intentional to avoid noise but it kills cross-service edges.

**Fix in `call_graph_generic.go`:**

Split the query into two:
1. **Intra-service pass** (existing behaviour): `signature CONTAINS $packageName` → creates
   standard `CALLS` edges (unchanged).
2. **Cross-service pass** (new): query references whose resolved symbol belongs to a _different_
   service's package. For these, instead of a `CALLS` edge, check if the symbol name matches an
   `APIRoute` node on another service. If yes, write a `GRPCCall` or `HTTPCall` node + edges
   following the same pattern as Phases 2–3.

**Why not just remove the filter entirely:** Removing it would create spurious `CALLS` edges to
standard library functions and third-party packages, bloating the graph. The split approach
preserves correctness for intra-service edges while enabling cross-service detection.

**Done when:** After indexing a TypeScript or Python service that calls another indexed service's
API, `GRPCCall` or `HTTPCall` nodes appear with `CALLS_SERVICE` edges.

---

## Phase 5 — Outbox / Message Queue Event Detector

**Goal:** Detect outbound async event publishes (outbox pattern, SQS, Kafka, NATS) and write
`OutboxCall` nodes + `CONSUMES_FROM` relationships so that async cross-service flows are visible.

> ⚠️ **Scope decision (aligned with Phase 3):** Phase 5 must cover **both** the AST path (Go,
> pipeline 1) and the SCIP path (all languages, pipelines 2 & 3). The original plan specified
> AST only. That is insufficient — the same principle established for Phase 3 applies here:
> SCIP is more powerful and language-agnostic; detection via SCIP symbol FQN patterns covers
> TypeScript, Python, and Go-SCIP services that the AST path cannot reach.

### 5a — AST path (pipeline 1, Go only)

**Patterns to detect (Go AST):**

| Pattern | Signal |
|---------|--------|
| Outbox pattern | Call to `SaveOutboxEvent(...)` or `InsertOutboxEvent(...)` or similar |
| AWS SQS | `sqs.SendMessage(...)` or `sqsClient.SendMessageWithContext(...)` |
| Kafka | `producer.Produce(...)` or `writer.WriteMessages(...)` |
| NATS | `nc.Publish(topic, ...)` |
| Custom event bus | Configurable list of wrapper function names |

**File:** `libs/indexer-go/static/event_call_detector.go` (already stubbed into `indexer.go`)

For each detected publish:
1. Extract `EventType` from the first string argument (queue name, topic name, or outbox event
   type field) — use the literal value or `"dynamic"` if it is a variable.
2. Write an `OutboxCall` node.
3. Write a `CALLS_API` edge from the caller `Function` to the `OutboxCall` node.
4. Query for a `Function` node in other services that is marked as a consumer of the same
   event type (future: when consumer-side detection is added). Write `CALLS_SERVICE` if found.

**Consumer-side detection** (same phase):
Detect functions that call queue/consumer registration patterns:
- `sqs.ReceiveMessage(...)` or a consumer loop that reads from a known queue.
- Mark these with a `consumesEvent` property.
- Write `CONSUMES_FROM` edges between the consuming `Function` and the `OutboxCall` node.

### 5b — SCIP path (pipelines 2 & 3, all languages)

Extend `scip_rpc_detector.go` (Phase 3's SCIP detector) to also match async publish symbols.
No new file needed — the same Reference→Symbol FQN query mechanism, with additional patterns:

| Language | FQN patterns to match |
|---|---|
| Go | `aws/aws-sdk-go*/sqs.SendMessage`, `confluentinc/confluent-kafka-go*/Producer#Produce`, `nats-io/nats.go*/Conn#Publish`, custom outbox wrapper names |
| TypeScript | `@aws-sdk/client-sqs*SendMessageCommand`, `kafkajs*Producer#send`, `nats*NatsConnection#publish` |
| Python | `boto3*SQS*send_message`, `confluent_kafka*Producer#produce`, `nats*Client#publish` |

Detection writes the same `OutboxCall` node + `CALLS_API` + `CALLS_SERVICE` edges as 5a.
`Transport` field is set from which pattern matched (`"sqs"`, `"kafka"`, `"nats"`, `"outbox"`).

**Done when:** Outbox publish sites and consumer registrations are visible in the graph with
`OutboxCall` nodes connecting producers to consumers, for both AST-indexed and SCIP-indexed services.

---

## Phase 6 — Fix MCP Tool Queries

> ⚠️ **This section is superseded.** The updated queries and full implementation spec are in
> **Architecture Update 1 → Phase 6** at the bottom of this document. Use that section, not this
> one. The queries here use `CONTAINS*` (variable depth) and are missing `OutboxCall` support.
> Key differences in the updated spec:
> - `CONTAINS*` replaced with two explicit hops `CONTAINS → CONTAINS`
> - All queries include `OutboxCall` alongside `GRPCCall`/`HTTPCall`
> - `handleCrossServiceCallsTool` accepts optional `target_service` (empty = all targets)
> - `handleCrossServiceFlowTool` specified as iterative Go loop, not Cypher recursion
> - SCIP-sourced nodes have `protoPackage = ""` and `url = "dynamic"` — queries handle this

---

## Phase 7 — Neo4j Schema (Indexes & Constraints) ✅ DONE

**Goal:** Add Neo4j indexes for the new node types so queries in Phases 2–6 are fast.

**File:** `libs/schema-go/` (wherever the current index creation lives — find it with
`grep -r "CREATE INDEX\|CREATE CONSTRAINT" libs/schema-go/`).

Add:
```cypher
CREATE INDEX grpc_call_caller IF NOT EXISTS FOR (n:GRPCCall) ON (n.callerService, n.scopeId);
CREATE INDEX grpc_call_target IF NOT EXISTS FOR (n:GRPCCall) ON (n.targetService, n.scopeId);
CREATE INDEX http_call_caller IF NOT EXISTS FOR (n:HTTPCall) ON (n.callerService, n.scopeId);
CREATE INDEX outbox_call_event IF NOT EXISTS FOR (n:OutboxCall) ON (n.eventType, n.scopeId);
```

---

## Phase 8 — Tests

**Goal:** Ensure the new indexing logic has test coverage so regressions are caught.

### Unit tests to add:

| Test file | What to test |
|-----------|-------------|
| `libs/indexer-go/static/rpc_call_detector_test.go` | Given a Go AST with a `New*Client(conn).Method(ctx, req)` call, verify a `GRPCCall` node is written with correct `targetMethod`, `callerService`, and a `CALLS_API` edge exists. |
| `libs/indexer-go/static/rpc_call_detector_test.go` | Given `http.NewRequest("POST", "https://payments.internal/v1/charge", body)`, verify an `HTTPCall` node is written with `method=POST`, `url=...`. |
| `libs/indexer-go/static/event_call_detector_test.go` | Given `sqsClient.SendMessage(ctx, &sqs.SendMessageInput{QueueUrl: &q, MessageBody: &body})`, verify an `OutboxCall` node is written with `transport=sqs`. |
| `apps/mcp-server-go/` | Integration test: index a fixture project with a cross-service gRPC call, then call `codegraph_cross_service_calls` and verify the response is non-empty and correct. |

---

## Implementation Order Summary

```
Phase 1 → Phase 2 → Phase 3 → Phase 4 → Phase 5 → Phase 6 → Phase 7 → Phase 8
  (schema)  (gRPC)   (HTTP)   (SCIP)   (async)   (MCP fix)  (indexes) (tests)
```

Phases 2, 3, and 5 are independent of each other once Phase 1 is done — they can be worked in
parallel if multiple engineers are available. Phase 6 depends on Phases 2–5 being done (or at
least Phases 2–3 for the most critical MCP fixes). Phase 7 and 8 can be done alongside Phase 6.

---

## Key Invariants the Worker LLM Must Respect

1. **Always set `scopeCtx` on every node and relationship written.** Omitting `scopeId` breaks
   multi-workspace queries. Copy the pattern from `call_graph_generic.go`.

2. **Use `MergeNode` / `MergeRelationship` not `CreateNode` / `CreateRelationship`** for
   idempotency — re-indexing a service must not create duplicate nodes.

3. **Do not remove the existing `CALLS` edge logic.** The new `GRPCCall`/`HTTPCall` detection is
   additive. A function that makes a gRPC call should have both a `CALLS` edge (to the local
   client stub function, if it exists in the graph) AND a `CALLS_API` edge to the `GRPCCall` node.

4. **Do not change the `DEPENDS_ON` relationship.** It is correct for import-level dependency. The
   new `CALLS_SERVICE` relationship is the runtime-call-level complement — both should exist.

5. **Variable type tracking (`varTypeMap`) must be reset per function**, not per file. gRPC client
   variables are typically function-scoped or struct-field-scoped; reusing the map across functions
   will produce false positives.

6. **`targetService` on `GRPCCall`/`HTTPCall` nodes may be empty.** Service resolution is best-
   effort — the target service may not be indexed yet. Queries must use `OPTIONAL MATCH` on
   `CALLS_SERVICE` edges, not a required match.

7. **SCIP is the primary detection path for ALL languages including Go. AST is the fallback.**
   This is a hard architectural decision made during implementation:
   - The recommended indexing command is `make dev-scip` / `codegraph index scip` — SCIP is the
     production path.
   - SCIP resolves actual types via `scip-go`; AST uses name heuristics (`New*Client`,
     field names ending in `"client"`). SCIP has zero false positives from naming coincidences.
   - SCIP symbol FQNs encode the exact proto package path (e.g.
     `github.com/org/repo/proto/payment/grpc/v1.PaymentServiceClient#CreatePayment`) — no guessing.
   - The AST detector (`rpc_call_detector.go`, `event_call_detector.go`) is **pipeline 1 only**
     (legacy `StaticIndexer`, `make dev`). Do not expand it to cover cases already handled by SCIP.
   - Never prefer the AST path over the SCIP path for the same language/service combination.

---

## Implementation Updates

Records what has actually been built, key decisions made during implementation, and deviations from
the plan that future worker LLMs need to know about.

---

### Phase 1 — Schema Foundation ✅ DONE

**Files changed:**
- `libs/core-models-go/node.go`
- `libs/core-models-go/relationship.go`

**What was added:**

`node.go` — three new `NodeType` constants:
```go
GRPCCallNode   NodeType = "GRPCCall"
HTTPCallNode   NodeType = "HTTPCall"
OutboxCallNode NodeType = "OutboxCall"
```

Three new structs (`GRPCCall`, `HTTPCall`, `OutboxCall`) with all fields from the plan, plus
corresponding cases in `NodeFactory`.

`relationship.go` — `CallsServiceRelationship` struct added (the `CallsServiceRel` constant already
existed). `RelationshipFactory` now has a `CallsServiceRel` case.

**Key facts:**
- `CallsAPIRel` and `CallsServiceRel` constants were already present before Phase 1; no new
  constants were needed in `relationship.go`.
- `NodeFactory` and `RelationshipFactory` are used for Neo4j result parsing, not for writing —
  they do not need to be called by the indexer when creating nodes.
- `go build ./libs/core-models-go/...` passes with zero errors.

---

### Phase 2 — gRPC Call Site Detector ✅ DONE (pipeline 1 / AST fallback only)

> **Priority note:** This is the AST-based fallback for the legacy `StaticIndexer` path
> (`make dev`). For Go services indexed via SCIP (`make dev-scip`) — which is the recommended
> production path — gRPC detection is handled by `scip_rpc_detector.go` (Phase 3). Do not
> extend this detector with features that duplicate Phase 3 SCIP detection.

**Files changed / created:**
- `libs/indexer-go/static/rpc_call_detector.go` ← **new file**
- `libs/indexer-go/static/indexer.go` ← wiring changes

#### `rpc_call_detector.go`

`RPCCallDetector` struct fields:
```go
type RPCCallDetector struct {
    client      *neo4j.Client
    serviceName string
    scopeCtx    models.ScopeContext
    fset        *token.FileSet
    varTypeMap  map[string]string  // varName → client type e.g. "PaymentServiceClient"
    varPkgMap   map[string]string  // varName → package alias e.g. "pb"
}
```

`varTypeMap` and `varPkgMap` are **reset at the start of every `DetectInFunction` call** — this is
the invariant from the key invariants section.

**Two-pass walk inside `DetectInFunction`:**
1. Pass 1 (`ast.Inspect` for `*ast.AssignStmt`): populates `varTypeMap`/`varPkgMap` from any
   `lhs := pkg.New*Client(conn)` assignment. Multi-return assignments (`client, err := ...`) are
   handled — only the first LHS ident is used.
2. Pass 2 (`ast.Inspect` for `*ast.CallExpr`): checks each call expression for gRPC patterns.

**Two detection paths in `processCallExpr`:**

| Path | Pattern | Example |
|------|---------|---------|
| Primary | Receiver is a local var in `varTypeMap` with a `*Client` type | `client.CreatePayment(ctx, req)` |
| Secondary | Receiver is a struct field selector whose name ends in `"client"` (case-insensitive) | `h.paymentClient.CreatePayment(ctx, req)` |

The secondary path is a heuristic — it avoids requiring full type analysis via `go/types`. It
covers the very common pattern where a service handler struct holds gRPC client fields.

**Node key format for `GRPCCall` nodes:**
```
grpccall:<callerService>:<filePath>:<targetMethod>:<line>
```
Used as the merge key so re-indexing is idempotent.

**`resolveTargetService` query:**
```cypher
MATCH (s:Service)
WHERE toLower(s.name) CONTAINS toLower($name)
   OR toLower($name) CONTAINS toLower(s.name)
RETURN elementId(s) AS id LIMIT 1
```
Bidirectional fuzzy match. Returns empty string if no match — callers tolerate this.

**Edges written per detected call site:**
- `CALLS_API`: `Function → GRPCCall` (via `MergeRelationship`, idempotent)
- `CALLS_SERVICE`: `GRPCCall → Service` (only if `resolveTargetService` returns non-empty)

**`protoPackage` field:** populated from the package alias recorded during `processAssignment`
(e.g. `pb` from `pb.NewPaymentServiceClient`). Empty for secondary-path detections since
there is no constructor call to inspect.

#### `indexer.go` wiring changes

- `StaticIndexer` gained a new field `rpcDetector *RPCCallDetector`.
- `NewStaticIndexer` initialises it: `NewRPCCallDetector(client, serviceName, models.DefaultScope())`.
  The scope is `DefaultScope()` (main branch). If the caller uses PR-scoped indexing the detector
  will not inherit that scope — **this is a known limitation**; a future change should accept a
  `ScopeContext` parameter in `NewStaticIndexer`.
- `extractCallGraph` signature changed from `(ctx, funcDecl, funcID)` to
  `(ctx, funcDecl, funcID, filePath string)`. This also **fixes a pre-existing bug** where
  `filePath` was always passed as `""` to the existing `CallGraphExtractor`.
- The visitor call site at `indexFunction` was updated to pass `v.filePath`.
- `extractCallGraph` now calls both extractors sequentially; errors from each are logged as
  warnings but do not abort indexing (consistent with the existing pattern in the file).

**`go build ./...` passes with zero errors.**

#### What Phase 2 does NOT cover (defer to Phase 3)

- HTTP outbound calls (AST path).
- **gRPC detection via SCIP** — pipelines 2 and 3 are untouched; all non-Go services and
  Go services indexed via `SCIPIndexer` still have no gRPC call site nodes.
- Struct fields that are gRPC clients but whose field name does not end in `"client"` (e.g.
  `h.svc` where `svc` is of type `pb.PaymentServiceClient`) — requires `go/types` analysis.
- Scope propagation for PR-scoped indexing runs — needs `NewStaticIndexer` to accept `ScopeContext`.

---

### Phase 3 — HTTP + SCIP RPC Detection ✅ DONE

**Files changed / created:**
- `libs/indexer-go/static/rpc_call_detector.go` ← HTTP detection added (pipeline 1)
- `libs/indexer-go/static/scip_rpc_detector.go` ← **new file** (pipelines 2 & 3)
- `libs/indexer-go/static/scip_indexer.go` ← SCIPRPCDetector wired in

#### `rpc_call_detector.go` additions

`processAssignment` extended to also track:
- `&http.Client{}` / `http.Client{}` composite literals → records var as `"http.Client"` type
- `resty.New()` calls → records var as `"resty.Client"` type

New `processHTTPCallExpr` method — three detection paths:
1. **Direct package calls**: `http.Get(url)`, `http.Post(url,...)`, `http.Head(url)`,
   `http.NewRequest(method, url, body)`, `http.NewRequestWithContext(...)`. URL extracted from
   string literal arg. Method extracted from literal (for `NewRequest`) or inferred from function name.
2. **Tracked variable**: `client.Do(req)` / `client.Execute(req)` where `client` is in
   `varTypeMap` as `http.Client` or `resty.Client`. Method = `"ANY"`, URL = `"dynamic"`.
3. **Struct field heuristic**: `h.httpClient.Do(req)` / `h.http_client.Do(req)` — field name
   contains "httpclient" or "http_client". Method = `"ANY"`, URL = `"dynamic"`.

New helpers added: `extractStringArg`, `resolveTargetServiceFromURL`, `extractHostFromURL`.

URL → service resolution uses `Service.baseURL` property (OPTIONAL MATCH; falls through gracefully
if the property doesn't exist).

`DetectInFunction` pass 2 now calls both `processCallExpr` (gRPC) and `processHTTPCallExpr` (HTTP)
for every `*ast.CallExpr` found in the function body.

#### `scip_rpc_detector.go` — new file

`SCIPRPCDetector` struct fields:
```go
type SCIPRPCDetector struct {
    client      *neo4j.Client
    serviceName string
    scopeCtx    models.ScopeContext
}
```

Entry point: `DetectRPCCalls(ctx)` — runs gRPC pass then HTTP pass.

**gRPC query** (`queryGRPCRefs`): Matches `Reference → Symbol` where the symbol FQN contains
`Client` AND one of: `/grpc/`, `/grpcv1`, `/pb/`, `/proto/`, `_pb2_grpc`, or `@grpc/grpc-js`.
Excludes constructor refs (symbol ends with `Client`). Returns up to 2000 refs.

**HTTP query** (`queryHTTPRefs`): Matches symbol FQNs containing `net/http` + known method
tokens (`.Get#`, `.Post#`, `.Do#`, `NewRequest`), or `resty`, `axios`, `node-fetch`,
`requests.get/.post`, `httpx.get/.post`. Returns up to 2000 refs.

For each matched ref, `findEnclosingFunction` finds the Function/Method node whose
`startLine ≤ ref.line ≤ endLine` (smallest span wins, same as GenericCallGraphBuilder).

`parseGRPCSymbol` — extracts `(serviceName, methodName)` from SCIP FQN by scanning
space/slash/dot-delimited tokens for types ending in `ServiceClient` or `Client`.

`inferHTTPMethodFromSymbol` — infers GET/POST/etc. from symbol FQN or displayName.

Node key formats (idempotent re-indexing):
- gRPC: `grpccall:scip:<service>:<filePath>:<ServiceName>.<MethodName>:<line>`
- HTTP: `httpcall:scip:<service>:<filePath>:<METHOD>:<line>`

#### `scip_indexer.go` wiring

After the call graph step (Step 9) and before API analysis (Step 10), added:
```go
fmt.Println("Detecting cross-service RPC call sites via SCIP symbols...")
rpcDetector := NewSCIPRPCDetector(si.client, si.serviceName, si.scopeCtx)
if err := rpcDetector.DetectRPCCalls(ctx); err != nil {
    fmt.Printf("Warning: SCIP RPC detection failed: %v\n", err)
}
```
This runs for **both** Go-SCIP (pipeline 2) and generic/non-Go (pipeline 3) because the detector
queries the already-ingested SCIP graph data — it doesn't care what language produced it.

**Known limitations:**
- HTTP URL is always `"dynamic"` for SCIP-detected calls (source not re-read → cannot extract
  literal URL from call arguments).
- `CALLS_SERVICE` edges are not written for SCIP HTTP calls (no URL to resolve). They are written
  for gRPC calls when `resolveSCIPTargetService` finds a matching service by name.
- The 2000-result limit per query prevents handling very large codebases in a single pass; a
  paginated approach would be needed for repos with >2000 RPC call sites.

---

### Phase 3 — Revised Scope Decision ⚠️ SUPERSEDED (see above)

**Original plan:** HTTP outbound call detection (AST path only).

**Revised scope (confirmed with developer):** Phase 3 must also add SCIP-based detection for
**both gRPC and HTTP** across all three indexing pipelines. SCIP is more powerful and
language-agnostic; AST alone is insufficient.

#### Three indexing pipelines that exist in this repo

| # | Pipeline | Entry point | Languages | Source data |
|---|---|---|---|---|
| 1 | AST | `StaticIndexer` (`indexer.go`) | Go only | `go/ast` parse |
| 2 | SCIP + Go AST | `SCIPIndexer` + `SCIPCallGraphBuilder` (`call_graph_scip.go`) | Go only | SCIP refs + Go AST for body ranges |
| 3 | SCIP generic | `SCIPIndexer` + `GenericCallGraphBuilder` (`call_graph_generic.go`) | All languages | Pure SCIP graph data (no source re-read) |

Phase 2 only wired `RPCCallDetector` into **pipeline 1**. Pipelines 2 and 3 remain blind to
all RPC call sites (both gRPC and HTTP).

#### Phase 3 revised file plan

```
libs/indexer-go/static/rpc_call_detector.go    ← extend: add processHTTPCallExpr (pipeline 1)
libs/indexer-go/static/scip_rpc_detector.go    ← NEW: gRPC + HTTP detection via SCIP Symbol FQNs
                                                   covers pipelines 2 and 3 for ALL languages
libs/indexer-go/static/call_graph_generic.go   ← add post-pass calling SCIPRPCDetector (pipeline 3)
libs/indexer-go/static/scip_indexer.go         ← wire SCIPRPCDetector into pipeline 2 (Go-SCIP)
```

#### How `scip_rpc_detector.go` works (SCIP path)

The SCIP graph already contains `Reference` nodes linked to `Symbol` nodes whose `symbol` field
is a fully-qualified name (SCIP format). The detector queries for references whose resolved symbol
FQN matches known client library patterns:

| Protocol | Go FQN pattern | TypeScript FQN pattern | Python FQN pattern |
|---|---|---|---|
| gRPC | `New*Client` in any `*/grpc*`, `*/pb`, `*/proto*` package | `@grpc/grpc-js*Client` | `*_pb2_grpc.*Stub` |
| HTTP | `net/http.NewRequest`, `net/http.Get`, `net/http.Post`, `resty*` | `axios*`, `node-fetch*`, `got*` | `requests.*`, `httpx.*` |

For each matched Reference:
1. Find the enclosing Function/Method node by line range (same technique as `GenericCallGraphBuilder`).
2. Determine whether it is a `GRPCCall` or `HTTPCall` based on which pattern matched.
3. Write the call-site node + `CALLS_API` edge from the enclosing function.
4. Attempt `CALLS_SERVICE` resolution via the existing `resolveTargetService` logic.

This is purely graph-to-graph (Cypher queries against already-ingested SCIP data) — no source file
re-reading is needed.

---

### Phase 5 — Outbox / Message Queue Event Detector ✅ DONE

**Files changed / created:**
- `libs/indexer-go/static/event_call_detector.go` ← **new file**
- `libs/indexer-go/static/indexer.go` ← wiring changes

#### `event_call_detector.go`

`EventCallDetector` struct fields:
```go
type EventCallDetector struct {
    client          *neo4j.Client
    serviceName     string
    scopeCtx        models.ScopeContext
    fset            *token.FileSet
    varTransportMap map[string]string  // varName → transport type; reset per function
}
```

**Two-pass walk inside `DetectInFunction`** — same pattern as `RPCCallDetector`:

**Pass 1** (`ast.Inspect` for `*ast.AssignStmt`): populates `varTransportMap` from constructor
calls and composite literals. Tracks:

| Constructor / literal | `varTransportMap` value |
|---|---|
| `sqs.New*(conn)` | `"sqs"` |
| `kafka.NewProducer(cfg)` | `"kafka"` |
| `kafka.NewConsumer(cfg)` | `"kafka-consumer"` |
| `&kafka.Writer{}` / `&kafka.Producer{}` | `"kafka"` |
| `nats.Connect(url)` / `stan.Connect(...)` | `"nats"` |

`varTransportMap` is **reset at the start of every `DetectInFunction` call** — same invariant
as `RPCCallDetector`.

**Pass 2** (`ast.Inspect` for `*ast.CallExpr`): calls both `processPublishCallExpr` and
`processConsumeCallExpr` for every call expression found.

#### `processPublishCallExpr` — detection paths

| # | Pattern | Transport | Example |
|---|---------|-----------|---------|
| 1 | Bare function call matching `outboxFuncNames` | `outbox` | `SaveOutboxEvent(ctx, "payment.created", payload)` |
| 2 | Selector call where method name matches `outboxFuncNames` | `outbox` | `repo.EmitEvent(ctx, "order.placed", data)` |
| 3 | Tracked var (`sqs`) + SQS publish method | `sqs` | `sqsClient.SendMessage(ctx, input)` |
| 4 | Tracked var (`kafka`) + Kafka produce method | `kafka` | `producer.Produce(msg, nil)` |
| 5 | Tracked var (`nats`) + NATS publish method | `nats` | `nc.Publish("payment.created", body)` |
| 6 | Struct field containing `"sqs"` + publish method | `sqs` | `h.sqsClient.SendMessage(...)` |
| 7 | Struct field containing `"kafka"` or `"producer"` + produce method | `kafka` | `h.producer.WriteMessages(...)` |
| 8 | Struct field containing `"nats"` + publish method | `nats` | `h.natsConn.Publish(...)` |

`outboxFuncNames` covers: `SaveOutboxEvent`, `InsertOutboxEvent`, `AddOutboxEvent`,
`PublishOutboxEvent`, `EnqueueOutboxEvent`, `CreateOutboxEvent`, `StoreOutboxEvent`,
`EmitEvent`, `PublishEvent`, `EnqueueEvent`, `DispatchEvent`.

**Node key format for `OutboxCall` nodes (idempotent re-indexing):**
```
outboxcall:<callerService>:<filePath>:<transport>:<eventType>:<line>
```

**Edges written per detected publish site:**
- `CALLS_API`: `Function → OutboxCall` (idempotent via `MergeRelationship`)
- `CALLS_SERVICE`: `OutboxCall → Service` (only if a consuming service is already indexed
  with a matching `consumesEvent` property on one of its functions)

**`eventType` extraction per transport:**
- Outbox: `extractEventTypeArg` — skips ctx/context identifiers, returns first string literal.
- SQS: `extractQueueStringLiteral` — walks call tree, prefers strings matching `sqs.`, `https://`,
  or `arn:` prefixes.
- Kafka: `extractFirstStringLiteral` — first string literal anywhere in the call tree.
- NATS: `extractStringArg(callExpr, 0)` — reused from `rpc_call_detector.go` (same package).

Falls back to `"dynamic"` if no string literal is found. `CALLS_SERVICE` is skipped for dynamic
event types to avoid spurious edges.

#### `processConsumeCallExpr` — detection paths

| # | Pattern | Transport | Example |
|---|---------|-----------|---------|
| 1 | Tracked var (`sqs`) + SQS consume method | `sqs` | `sqsClient.ReceiveMessage(ctx, input)` |
| 2 | Tracked var (`kafka-consumer`) + Kafka consume method | `kafka` | `consumer.ReadMessage(-1)` |
| 3 | Tracked var (`nats`) + NATS subscribe method | `nats` | `nc.Subscribe("payment.created", handler)` |
| 4 | Struct field containing `"sqs"` + consume method | `sqs` | `h.sqsClient.ReceiveMessage(...)` |
| 5 | Struct field containing `"kafka"` or `"consumer"` + consume method | `kafka` | `h.consumer.Poll(100)` |
| 6 | Struct field containing `"nats"` + subscribe method | `nats` | `h.nats.Subscribe(...)` |

**On consumer detection:**
1. Updates the caller `Function` node with `consumesEvent = <eventType>` and
   `consumesTransport = <transport>` properties via `ExecuteQuery`, so producers indexed later
   can resolve this service via `linkProducerToConsumer`.
2. Queries for `OutboxCall` nodes in other services with matching `eventType` and writes
   `CONSUMES_FROM` edges from the consumer function to each (up to 20 per call site).

#### `indexer.go` wiring changes

- `StaticIndexer` gained a new field `eventDetector *EventCallDetector`.
- `NewStaticIndexer` initialises it: `NewEventCallDetector(client, serviceName, models.DefaultScope())`.
  Same scope limitation as `rpcDetector` — PR-scoped indexing will not inherit the scope.
- `extractCallGraph` now calls `eventDetector.DetectInFunction(...)` after
  `rpcDetector.DetectInFunction(...)`. Errors are logged as warnings and do not abort indexing.

**`go build ./... && go vet ./...` both pass with zero errors.**

#### Known limitations / deferred work

- **Order-dependent linking**: if the consumer service is indexed before the producer service,
  `linkConsumerToOutboxCalls` finds no `OutboxCall` nodes (they don't exist yet). Re-indexing
  the consumer after the producer resolves this. A future post-processing pass could reconcile
  unlinked edges after all services are indexed.
- **Struct-field transport tracking**: `varTransportMap` only tracks local variable assignments.
  Struct fields that hold transport clients (e.g. `h.sqs` typed `*sqs.Client`) are covered only
  by the name-heuristic path (paths 6–8), which requires the field name to contain a transport
  keyword.
- **Kafka topic extraction**: `extractFirstStringLiteral` returns the first string literal in the
  AST subtree, which may be a field key or config string rather than the topic if the message
  struct has other string fields before `TopicPartition`.
- **SCIP pipeline (pipelines 2 & 3) not covered**: `EventCallDetector` runs only in the AST
  pipeline (pipeline 1). A future `SCIPEventDetector` could query SCIP symbol FQNs for known
  message queue client patterns (e.g. `aws-sdk-go-v2/service/sqs.*SendMessage`,
  `confluentinc/confluent-kafka-go.*Producer.Produce`).

---

### Phase 6 — MCP Tool Query Fixes ✅ DONE

**File changed:** `apps/mcp-server-go/main.go`

**What was fixed:**

#### 6a — `handleServiceAPICallsTool` (line ~2165)
- Replaced `CONTAINS*` with a precise 2-hop `Service→File→Function` traversal (was unbounded, slow).
- Replaced `SDKCall` (doesn't exist) with `GRPCCall OR HTTPCall OR OutboxCall`.
- Replaced `TARGETS_SERVICE` (doesn't exist) with `CALLS_SERVICE`.
- Added `limit` param support (default 50).
- Output now shows: Type, CallTarget, Method, TargetService, Function, File.
- Added helpful empty-result message directing users to run `codegraph index scip`.

#### 6b — `handleCrossServiceCallsTool` (line ~2230)
- Replaced `shortestPath((source)-[*..10]-(target))` — this was traversing ALL relationship types
  bidirectionally and returning garbage paths; `SDKCall` also referenced.
- New query uses directed `CALLS_API → CALLS_SERVICE` traversal (same 2-hop CONTAINS pattern).
- Both `from_service`/`to_service` are now optional — omitting either shows all cross-service calls.
- Fixed arg-name inconsistency: handler now accepts `from_service`/`to_service` (tool schema names)
  and falls back to `source_service`/`target_service` (legacy names) for backwards compatibility.
- Added `limit` param support (default 20).

#### 6c — `handleTraceCallGraphTool` (line ~2800)
- Added a **"Cross-Service Calls (outbound)"** section between the downstream and upstream blocks.
- New section queries all functions reachable via `CALLS*0..maxDepth` from root, then finds any
  `CALLS_API → CALLS_SERVICE` edges hanging off those functions.
- Shows: `caller → [callType] → callTarget @ targetService` for each cross-service hop found.

#### 6d — `handleServiceDependenciesTool` (line ~2050)
- Now runs TWO queries and renders both sections:
  1. **Static Import Dependencies** — existing `DEPENDS_ON` query (unchanged)
  2. **Runtime Call Dependencies** — new query over `CALLS_API → CALLS_SERVICE` edges showing
     which services are actually called at runtime, with call count and protocol breakdown.
- If either section returns results, it is rendered. Missing-data case handled gracefully.

#### 6e — `handleCrossServiceFlowTool` (NEW tool added)
- MCP tool name: `codegraph_cross_service_flow`
- Input: `start_service` (required), `start_function` (optional), `max_hops` (1–5, default 3).
- Implements iterative BFS across service boundaries:
  - Hop 1: from `start_service` (optionally filtered by `start_function`)
  - Subsequent hops: from any newly discovered target services
  - Stops when frontier is empty or `max_hops` reached
- Renders per-hop Markdown tables + "Services reached" summary line at end.
- Tool registration: added to `tools` slice in `ListTools` response AND switch case in `CallTool`.

#### Additional fix — `handleServiceArchitectureTool`
- Replaced `CONTAINS*` with 2-hop `Service→File→Function` traversal.
- Replaced `HTTPCall OR SDKCall` with `GRPCCall OR HTTPCall OR OutboxCall`.

**Key implementation facts:**
- All new Cypher uses 2-hop `CONTAINS` (never `CONTAINS*`) — consistent with the canonical pattern.
- `CALLS_SERVICE` edges are OPTIONAL in the graph (detector best-effort). All queries use directed
  MATCH (not OPTIONAL MATCH) so they naturally return only calls where the target was resolved.
- `handleCrossServiceFlowTool` performs multiple round-trips to Neo4j (one per hop) — this is
  intentional; Cypher recursive BFS on variable-depth multi-hop cross-service paths is less
  predictable than Go-level iteration.
- The `sort` package was already imported; `strings.Join` already available — no new imports needed.

---

### Phase 7 — Neo4j Schema (Indexes & Constraints) ✅ DONE

**File changed:** `libs/schema-go/schema.go`

**What was added:**

10 new `Index` entries appended to `GetIndexes()`, following the same `BTREE` pattern used by
every other node type in the file.

#### `GRPCCall` indexes (4)

| Index name | Properties | Purpose |
|---|---|---|
| `grpccall_nodekey_idx` | `nodeKey` | Fast single-key merge lookup |
| `grpccall_nodekey_scope_idx` | `nodeKey, scopeId` | Scoped `MergeNode` idempotency |
| `grpccall_caller_scope_idx` | `callerService, scopeId` | "What does service X call?" queries |
| `grpccall_target_scope_idx` | `targetService, scopeId` | "Who calls service X?" queries |

#### `HTTPCall` indexes (3)

| Index name | Properties | Purpose |
|---|---|---|
| `httpcall_nodekey_idx` | `nodeKey` | Fast single-key merge lookup |
| `httpcall_nodekey_scope_idx` | `nodeKey, scopeId` | Scoped `MergeNode` idempotency |
| `httpcall_caller_scope_idx` | `callerService, scopeId` | Caller queries |

#### `OutboxCall` indexes (3)

| Index name | Properties | Purpose |
|---|---|---|
| `outboxcall_nodekey_idx` | `nodeKey` | Fast single-key merge lookup |
| `outboxcall_nodekey_scope_idx` | `nodeKey, scopeId` | Scoped `MergeNode` idempotency |
| `outboxcall_event_scope_idx` | `eventType, scopeId` | Consumer resolution + async flow queries |

**Key facts:**
- The 4 indexes from the plan (`grpc_call_caller`, `grpc_call_target`, `http_call_caller`,
  `outbox_call_event`) are all covered by `*_caller_scope_idx`, `*_target_scope_idx`, and
  `*_event_scope_idx` respectively — with `scopeId` added to each composite per the multi-scope
  pattern established for every other node type.
- `nodeKey` and `nodeKey+scopeId` indexes were also added (not in the original plan) because every
  node type that uses `MergeNode` requires these for idempotent re-indexing to be fast. Omitting
  them would cause full-label scans on every indexing run.
- Indexes are created with `IF NOT EXISTS` (handled by `createIndexes` in `schema.go`) so running
  `make neo4j-schema` on an existing database is safe.
- `go build ./libs/schema-go/...` passes with zero errors.

---

## Architecture Update 1 — State of the Graph After Phases 1–5

> **Audience:** Worker LLM implementing Phase 6 and beyond. Read this before touching
> `apps/mcp-server-go/main.go`. Phases 1–5 are fully implemented and compiled. The graph
> schema and detection pipelines are live. Everything below describes what is actually in
> Neo4j after a full indexing run.

---

### What is now in the graph

Three new node types are written during indexing. Every node uses `MergeNode` (idempotent).

#### `GRPCCall` node

| Property | Value |
|---|---|
| `nodeKey` | Unique merge key (see format below) |
| `callerService` | Name of the service that makes the call |
| `targetService` | Name of the target service — **may be empty** if not resolved |
| `targetMethod` | `"ServiceName.MethodName"` e.g. `"PaymentService.CreatePayment"` |
| `protoPackage` | Proto package alias (e.g. `"pb"`) — may be empty for SCIP detections |
| `filePath` | Source file path of the call site |
| `line` | Line number of the call expression |
| `scope`, `scopeId` | Copied from `ScopeContext` of the indexing run |

**Node key formats** (never overlap across detectors):
```
grpccall:<callerSvc>:<filePath>:<ServiceName.Method>:<line>         ← AST pipeline 1
grpccall:scip:<callerSvc>:<filePath>:<ServiceName.Method>:<line>    ← SCIP pipelines 2&3
grpccall:generic:<callerSvc>:<filePath>:<ServiceName.Method>:<line> ← cross-svc pass
```

#### `HTTPCall` node

| Property | Value |
|---|---|
| `nodeKey` | Unique merge key |
| `callerService` | Service making the call |
| `targetService` | Resolved target — **may be empty** |
| `url` | Literal URL string if extractable from AST; `"dynamic"` from SCIP detections |
| `method` | `GET`, `POST`, `PUT`, etc.; `"ANY"` when not determinable |
| `filePath`, `line` | Call site location |

**Node key formats:**
```
httpcall:<callerSvc>:<filePath>:<METHOD>:<line>         ← AST pipeline 1
httpcall:scip:<callerSvc>:<filePath>:<METHOD>:<line>    ← SCIP pipelines 2&3
httpcall:generic:<callerSvc>:<filePath>:<METHOD>:<line> ← cross-svc pass
```

#### `OutboxCall` node

| Property | Value |
|---|---|
| `nodeKey` | Unique merge key |
| `callerService` | Service publishing the event |
| `transport` | `"outbox"`, `"sqs"`, `"kafka"`, `"nats"` |
| `eventType` | String literal (queue name / topic / event type); `"dynamic"` if variable |
| `queueOrTopic` | Same as `eventType` for queue-based transports |
| `filePath`, `line` | Call site location |

**Node key format:**
```
outboxcall:<callerSvc>:<filePath>:<transport>:<eventType>:<line>    ← AST pipeline 1 only
```

---

### What edges are now written

| Edge | From → To | Written by | Condition |
|---|---|---|---|
| `CALLS_API` | `Function → GRPCCall/HTTPCall/OutboxCall` | All detectors | Always (every detected call site) |
| `CALLS_SERVICE` | `GRPCCall → Service` | AST + SCIP detectors | Best-effort; absent if target service not indexed |
| `CALLS_SERVICE` | `HTTPCall → Service` | AST detector only | Absent for SCIP HTTP (no URL to resolve) |
| `CALLS_SERVICE` | `OutboxCall → Service` | AST event detector | Only if consuming service already indexed with matching `consumesEvent` property |
| `CONSUMES_FROM` | `Function → OutboxCall` | AST event detector | Consumer-side; written when consumer is detected |

---

### How the three pipelines map to CLI commands

**Critical:** each indexing run triggers exactly ONE pipeline. They are mutually exclusive entry
points — running `make dev-scip` does NOT also run pipeline 1. The pipelines never chain.

```
codegraph index project /path                    → Pipeline 1 only  (StaticIndexer, AST)
codegraph index scip /path --language=go         → Pipeline 2 only  (SCIPIndexer + SCIPCallGraphBuilder)
codegraph index scip /path --language=typescript → Pipeline 3 only  (SCIPIndexer + GenericCallGraphBuilder)
codegraph index scip /path   [no --language]     → Polyglot: auto-detects languages in the repo,
                                                   runs Pipeline 2 for each Go root,
                                                   runs Pipeline 3 for each TS/Python/Java root.
                                                   Pipeline 1 is NEVER invoked.
```

Pipelines 2 and 3 share the same `SCIPIndexer` struct — the language flag controls which call
graph builder is used internally (branch at `scip_indexer.go:236`).

### Detection coverage matrix

| Command used | gRPC detection | HTTP detection | Async/Outbox detection |
|---|---|---|---|
| `make dev` (pipeline 1) | `RPCCallDetector` — name heuristics | `RPCCallDetector.processHTTPCallExpr` | ✅ `EventCallDetector` |
| `make dev-scip` Go (pipeline 2) | ✅ `SCIPRPCDetector` — type-precise | ✅ `SCIPRPCDetector` | ❌ **nothing** |
| `codegraph index scip --language=ts` (pipeline 3) | ✅ `SCIPRPCDetector` + cross-svc pass | ✅ `SCIPRPCDetector` + cross-svc pass | ❌ **nothing** |
| `codegraph index scip` polyglot | Same as pipeline 2/3 per language root | Same | ❌ **nothing** |

**Phase 5b gap — production severity:** The recommended command is `make dev-scip` (pipeline 2).
Any Go service indexed the recommended way has **zero `OutboxCall` nodes** in the graph.
`EventCallDetector` only runs in the legacy pipeline 1. Phase 5b (adding outbox symbol FQN
pattern queries to `scip_rpc_detector.go`) must be implemented before async event flows are
visible for any SCIP-indexed service. This is the top remaining indexing gap.

---

### The canonical query traversal for cross-service calls

Any MCP tool query that asks "what does service X call?" must follow this pattern:

```cypher
MATCH (s:Service {name: $serviceName})-[:CONTAINS]->(file:File)-[:CONTAINS]->(fn:Function)
-[:CALLS_API]->(call)
WHERE call:GRPCCall OR call:HTTPCall OR call:OutboxCall
OPTIONAL MATCH (call)-[:CALLS_SERVICE]->(target:Service)
RETURN fn, call, target
```

Key rules:
- Use **two explicit `CONTAINS` hops** (`Service → File → Function`), not `CONTAINS*`. The
  variable-depth form is expensive and the actual graph has exactly this 2-hop structure for
  both AST-indexed and SCIP-indexed services.
- Always `OPTIONAL MATCH` on `CALLS_SERVICE` — it may be absent.
- Filter on labels (`call:GRPCCall OR call:HTTPCall OR call:OutboxCall`) rather than a type
  property — labels are indexed, properties are not.

---

### Key implementation facts for Phase 6 implementors

1. **`handleServiceAPICallsTool`** queries for `HTTPCall`/`SDKCall` which did not exist before.
   Now they do. The query just needs updating — the data is there.

2. **`handleCrossServiceCallsTool`** uses `shortestPath` with no relationship filter — it will
   traverse `CALLS`, `CONTAINS`, `DEFINES`, and everything else. The fix is to use directed
   `CALLS_API → CALLS_SERVICE` traversal instead.

3. **`handleTraceCallGraphTool`** stops at service boundaries because it only follows `CALLS`
   edges. To cross a boundary it must also follow `CALLS_API` → `GRPCCall/HTTPCall` → `CALLS_SERVICE`
   → target `Service`, then find the target service's `APIRoute`/handler and continue tracing.

4. **`handleServiceDependenciesTool`** currently uses only `DEPENDS_ON` (import-level). It should
   be augmented with the `CALLS_SERVICE` traversal above to show runtime call evidence.

5. **Line numbers and `filePath`** are present on all call-site nodes — surface them in tool output
   so developers can click-to-navigate.

6. **`targetService` may be empty** on any call node. Every query returning call nodes must guard
   with `OPTIONAL MATCH` for `CALLS_SERVICE`, never a required `MATCH`.

7. **SCIP-sourced `GRPCCall` nodes have `protoPackage = ""`** and `url = "dynamic"` for HTTP.
   Do not filter on these fields being non-empty; use `coalesce(n.protoPackage, "unknown")` in
   RETURN clauses instead.

---

## Phase 6 — Fix MCP Tool Queries (Updated for SCIP-first world)

**Goal:** Update all MCP tool handlers in `apps/mcp-server-go/main.go` to query the call-site
nodes that now exist in the graph. Phases 1–5 built the data; Phase 6 surfaces it.

**Where to find handlers:** `apps/mcp-server-go/main.go` is a single ~3000-line file. Each handler
is a function named `handle*Tool`. Search by function name.

**Traversal rule for all queries:** Use `(s:Service)-[:CONTAINS]->(f:File)-[:CONTAINS]->(fn:Function)`
— two explicit hops, never `CONTAINS*`.

---

### 6a. Fix `handleServiceAPICallsTool`

Find current handler and replace its Cypher with:

```cypher
MATCH (s:Service {name: $serviceName})-[:CONTAINS]->(file:File)-[:CONTAINS]->(fn:Function)
OPTIONAL MATCH (fn)-[:CALLS_API]->(grpc:GRPCCall)
OPTIONAL MATCH (fn)-[:CALLS_API]->(http:HTTPCall)
OPTIONAL MATCH (fn)-[:CALLS_API]->(outbox:OutboxCall)
OPTIONAL MATCH (grpc)-[:CALLS_SERVICE]->(grpcTarget:Service)
OPTIONAL MATCH (http)-[:CALLS_SERVICE]->(httpTarget:Service)
OPTIONAL MATCH (outbox)-[:CALLS_SERVICE]->(outboxTarget:Service)
WITH fn, grpc, http, outbox, grpcTarget, httpTarget, outboxTarget
WHERE grpc IS NOT NULL OR http IS NOT NULL OR outbox IS NOT NULL
RETURN
  fn.name AS functionName,
  fn.filePath AS filePath,
  CASE
    WHEN grpc IS NOT NULL THEN 'gRPC'
    WHEN http IS NOT NULL THEN 'HTTP'
    ELSE 'async'
  END AS callType,
  coalesce(grpc.targetMethod, http.url, outbox.eventType) AS target,
  coalesce(grpc.protoPackage, http.method, outbox.transport) AS detail,
  coalesce(grpcTarget.name, httpTarget.name, outboxTarget.name) AS targetService,
  coalesce(grpc.line, http.line, outbox.line) AS callLine
ORDER BY callType, targetService, callLine
```

---

### 6b. Fix `handleCrossServiceCallsTool`

Replace the `shortestPath` with a directed `CALLS_API → CALLS_SERVICE` traversal.
Also: make `target_service` optional so callers can query all services a source calls.

```cypher
MATCH (source:Service {name: $sourceService})
      -[:CONTAINS]->(file:File)-[:CONTAINS]->(callerFn:Function)
      -[:CALLS_API]->(call)
      -[:CALLS_SERVICE]->(target:Service)
WHERE (call:GRPCCall OR call:HTTPCall OR call:OutboxCall)
  AND ($targetService = '' OR target.name = $targetService)
RETURN
  callerFn.name AS callerFunction,
  callerFn.filePath AS callerFile,
  labels(call)[0] AS callType,
  CASE
    WHEN call:GRPCCall   THEN call.targetMethod
    WHEN call:HTTPCall   THEN coalesce(call.url, 'dynamic')
    WHEN call:OutboxCall THEN call.eventType
  END AS target,
  call.line AS callLine,
  target.name AS targetService
ORDER BY callerFile, callLine
LIMIT 100
```

Pass `$targetService = ''` when the caller omits the target — the `AND` condition becomes a
no-op and all target services are returned.

---

### 6c. Fix `handleTraceCallGraphTool`

The current tool follows only `CALLS` edges and stops at the first uncalled node. Add a
cross-service hop when a `CALLS_API` edge is encountered.

**Strategy — two-pass per service boundary:**

Pass 1 (intra-service): follow `CALLS` edges up to depth 10 from the starting function within
the same service. Collect every `GRPCCall`/`HTTPCall` node reachable via `CALLS_API`.

```cypher
MATCH (startFn:Function {name: $functionName})
      <-[:CONTAINS]-(file:File)<-[:CONTAINS]-(svc:Service {name: $serviceName})
CALL apoc.path.subgraphNodes(startFn, {
  relationshipFilter: 'CALLS>',
  maxLevel: 10
}) YIELD node AS callee
OPTIONAL MATCH (callee)-[:CALLS_API]->(call)
WHERE call:GRPCCall OR call:HTTPCall
OPTIONAL MATCH (call)-[:CALLS_SERVICE]->(targetSvc:Service)
RETURN callee.name, callee.filePath, call.targetMethod, targetSvc.name
```

Pass 2 (cross-service): for each `targetSvc` found in pass 1, find the `APIRoute` or handler
function in that service that corresponds to the call, then repeat pass 1 from there.

```cypher
MATCH (targetSvc:Service {name: $targetSvcName})
      -[:CONTAINS]->(file:File)-[:CONTAINS]->(handler:Function)
WHERE handler.name CONTAINS $methodName
   OR EXISTS {
     MATCH (handler)-[:EXPOSES_API]->(route:APIRoute)
     WHERE route.path CONTAINS $methodName
   }
RETURN elementId(handler) AS handlerID, handler.name, handler.filePath
LIMIT 1
```

If APOC is not available, implement the intra-service traversal as an iterative Cypher loop
with a depth counter instead.

---

### 6d. Fix `handleServiceDependenciesTool`

Augment the existing `DEPENDS_ON` query to also show runtime call evidence. Run both queries
and merge results by target service name.

**Runtime call evidence query (add alongside existing DEPENDS_ON query):**

```cypher
MATCH (s:Service {name: $serviceName})
      -[:CONTAINS]->(file:File)-[:CONTAINS]->(fn:Function)
      -[:CALLS_API]->(call)-[:CALLS_SERVICE]->(target:Service)
WHERE call:GRPCCall OR call:HTTPCall OR call:OutboxCall
RETURN
  target.name AS targetService,
  count(DISTINCT call) AS callSiteCount,
  collect(DISTINCT
    CASE
      WHEN call:GRPCCall   THEN 'gRPC'
      WHEN call:HTTPCall   THEN 'HTTP'
      WHEN call:OutboxCall THEN call.transport
    END
  ) AS protocols
ORDER BY callSiteCount DESC
```

Present as: import-level dependency (DEPENDS_ON) + runtime call count + protocols.

---

### 6e. Add `handleCrossServiceFlowTool` (new tool)

New MCP tool `codegraph_cross_service_flow`: given a starting function + service, returns the
full multi-hop call chain across all services it touches. This is the end-to-end trace the
original `handleTraceCallGraphTool` cannot produce.

**Input params:** `function_name` (string), `service_name` (string), `max_hops` (int, default 3).

**Implementation approach:** Implement as an iterative Go loop (not Cypher recursion):
1. Start with `{service: service_name, function: function_name}` as the frontier.
2. Per iteration: run the pass-1 intra-service query from 6c. Collect all `GRPCCall`/`HTTPCall`
   nodes found.
3. For each call node with a `CALLS_SERVICE` edge, find the handler in the target service (pass-2
   query from 6c). Add that `{service, function}` pair to the next frontier.
4. Stop after `max_hops` iterations or when the frontier is empty.
5. Return the full chain as an ordered list of `{service, function, callType, target, filePath, line}`.

**Response format** (JSON in MCP result):
```json
{
  "startService": "order-service",
  "startFunction": "CreateOrder",
  "hops": [
    {"service": "order-service",   "function": "CreateOrder",      "callType": "gRPC",  "target": "PaymentService.Charge",  "file": "...", "line": 42},
    {"service": "payment-service", "function": "Charge",           "callType": "async", "target": "payment.charged",        "file": "...", "line": 88},
    {"service": "ledger-service",  "function": "HandlePayCharged", "callType": "",      "target": "",                       "file": "...", "line": 0}
  ]
}
```
