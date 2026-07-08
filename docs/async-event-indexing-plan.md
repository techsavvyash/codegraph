# Async Event-Flow Indexing for CodeGraph

> **One-line goal:** Teach CodeGraph to see event-driven (SQS) flows the same way it
> already sees synchronous gRPC/HTTP calls — so that "who fires `settlement.failed`, who
> listens, and what happens next" becomes a graph you can query, not tribal knowledge.

---

## 1. Context — why we are doing this

CodeGraph today links **synchronous** cross-service calls: a gRPC/HTTP call site in service
A is resolved to the concrete handler function in service B (see
`libs/indexer-go/pipeline/cross_service_resolver.go`). That machinery is solid and recently
landed (`RESOLVES_TO` edges, service-scoped node keys, a standalone `resolve` command).

But a large part of Tazapay is **asynchronous**. Services publish domain events
(`settlement.failed`, `settlement.succeeded`, `fx_transaction.created`, `payout.*`, …) onto an
SQS queue. The **event service** consumes them, enriches the payload with extra gRPC lookups,
and **fans them back out** to more queues (notification webhook, notification email, balance,
etc.). None of this is in the graph today, so an LLM (or engineer) exploring the code cannot
answer questions like:

- *"When settlement fails, who eventually gets notified?"*
- *"What downstream effects does emitting `settlement.succeeded` trigger?"*
- *"Which services consume `fx_transaction.created`?"*

This is a **known, documented gap.** The KB literally flags it:

> *CODEGRAPH GAP: event publish goes through `utils.TriggerPayoutEvent` /
> `TriggerAutoPayoutInitiateEvent`, which are NOT in the indexer's allowlist. So no
> OutboxCall node is emitted — the event effect is invisible to the graph.*

**Intended outcome:** a producer function → **event** → **listener** → downstream-consumers
chain, fully navigable in Neo4j and via one MCP tool, mirroring the existing synchronous
resolver design so it feels native to the codebase.

---

## 2. The mental model (read this first — everything below builds on it)

Think of an event name like **a radio channel**:

```
   PRODUCER                    CHANNEL                  LISTENER                 DOWNSTREAM
   (settlement)             "settlement.failed"       (event svc)            (notification, balance)

   TriggerSettlement... ──broadcasts on──▶ ┌───────────────┐ ──tuned in──▶ settlementUpdateEvent
                                           │ settlement.   │                      │
                                           │   failed      │                      │ re-broadcasts on
                                           └───────────────┘               "notification.*" channels
                                                                                  │
                                                                                  ▼
                                                                       notification svc listener
```

- A **producer** *broadcasts* on a channel (publishes an event to a queue).
- The **channel** is the event name, `"{group}.{action}"` e.g. `settlement.failed`.
- The **listener** service is *tuned in* (its SQS listener is bound to that queue).
- The listener may **re-broadcast** on other channels → the same model repeats recursively.

We turn each of those four things into graph objects. The channel becomes a shared hub node so
that *every* broadcaster and *every* listener of `settlement.failed` connects to the **same**
node — that is what makes "who touches this event?" a one-hop query.

---

## 3. How the code actually works today (ground truth)

Verified by reading `settlement/utils/event.go`, `event/service/sqs/route.go`,
`event/service/register.go`, `*/env/service.go`, and `grpc-framework/client/queue/*`.

### 3a. The envelope (shared, in `grpc-framework`)
Every event on every queue is the same struct — there is **no per-event proto type**:
```go
// grpc-framework/client/queue/type.go
type AsyncMessage struct {
    EventType string         `json:"event_type"` // "{group}.{action}", e.g. "settlement.failed"
    Data      map[string]any `json:"data"`       // dynamic, untyped payload
    ...
}
```

### 3b. The emit pattern (producer side) — remarkably uniform
Every emitter in `settlement/utils/event.go` (and balance) follows ONE shape:
```go
qMsg := &queue.AsyncMessage{ EventType: <expr>, Data: <map> }
queue.SendSQSMsg(ctx, env.Get(svcenv.<QueueVar>), qMsg)          // or SendDelaySQSMsg(..., delay)
```
`<expr>` (the event name) appears in exactly these forms:

| Form | Example | Resolvable statically? |
|------|---------|------------------------|
| String constant | `constants.EventPayoutAutoInitiate` → `"payout.auto_initiate"` | ✅ yes |
| Const + `CharDot` + const | `EventGroupSettlement + CharDot + EventActionFailed` → `"settlement.failed"` | ✅ yes |
| Const group + **runtime var** action | `EventGroupPayout + CharDot + payoutStatus` | ⚠️ group only |
| Plain variable (relay) | `EventType: eventType` (param) — value set in caller's `switch` | ⚠️ needs caller |

**The relay case is the important one.** `TriggerSettlementStatusUpdateEvent` picks the name in
a `switch`, then passes it *down* to a tiny helper `sendSettlementEvent(ctx, eventType, …)`
that does the actual `AsyncMessage{EventType: eventType}` + `SendDelaySQSMsg`. So the **name**
lives in one function and the **send** in another. Our algorithm must bridge that gap
(Section 5, Phase 1).

### 3c. Queue → service is deterministic
Queue names follow `queue.<service>.<name>` (documented in `event/env/service.go`). The
**2nd segment is the owning service**, even when names are surprising:
`PayoutQueue = "queue.settlement.payout"` (owned by *settlement*),
`QueueEventURL = "queue.event.event"` (owned by *event*). So any destination queue string
resolves to its listener service with zero guesswork.

### 3d. Listener binding is a findable anchor
```go
// event/service/register.go
server.SQSListener{ QueueURL: env.Get(svcenv.EventQueue), Route: sqs.GetEventRoutes() }
```
So per service we can extract `(queue → route-entry function)` from `SQSListener{...}` literals.

### 3e. Routing + fan-out (inside the listener)
`event/service/sqs/route.go`: `getEventGroupAction` splits `EventType` on `.`; a **two-level
switch** dispatches group → `settlementActionRoute` → action → `settlementUpdateEvent`. The
handler enriches via gRPC calls, then **fans out** through `utils.SendTo*Consumer` helpers, each
of which is *itself* a `queue.SendSQSMsg(...)` to a downstream queue. → **The fan-out is just
another instance of the emit pattern (3b), so the same detector handles it recursively.**

---

## 4. Target graph model

### New node
- **`EventType`** — the channel hub. One per `{group}.{action}` per scope.
  - `nodeKey = "eventtype:<group>.<action>"` (e.g. `eventtype:settlement.failed`).
  - Group-fallback hubs use `nodeKey = "eventtype:<group>.*"` (e.g. `eventtype:payout.*`).
  - Props: `eventType`, `group`, `action`, `dynamic` (bool).
  - Service-agnostic name so producers and consumers across services share one node.

### Enriched existing node
- **`OutboxCall`** (already exists) — the physical publish call-site. We upgrade its
  `eventType` prop from today's *queue env-var name* to the **semantic event name**, and add
  `eventGroup`, `eventAction`, `destQueue`, `destService`, `dynamic`.

### New edges
| Edge | From → To | Meaning | Written by |
|------|-----------|---------|------------|
| `EMITS_EVENT` | `OutboxCall` → `EventType` | "this publish site broadcasts this event" (carries `destQueue`, `destService`, `transport`) | detector (indexing) |
| `ROUTED_TO` | `EventType` → `Function`/`Method` | "this event is handled here" (carries `service`, `tier`, `confidence`) | resolver (post-index) |

Existing edges are unchanged and reused: `CALLS_API` (producer fn → OutboxCall) already links
the enclosing function; the intra-service `CALLS` graph already connects the listener entry to
the precise handler and its fan-out.

### The full picture
```
 (fn TriggerSettlementStatusUpdateEvent)
        │ CALLS_API
        ▼
 (OutboxCall  transport=sqs, destQueue="queue.event.event", destService=event)
        │ EMITS_EVENT
        ▼
 (EventType  settlement.failed) ─────────────────────────────────────────┐
        │ ROUTED_TO {service:event, tier:entry,  conf:1.0}                │ ROUTED_TO {tier:handler,
        ▼                                                                 ▼   conf:0.8}
 (fn GetEventRoutes / eventRoutes)  ──CALLS──▶ ... ──▶ (fn settlementUpdateEvent)
                                                              │ CALLS
                                                              ▼
                                                   (fn SendToEmailConsumer)
                                                              │ CALLS_API
                                                              ▼
                                                   (OutboxCall destService=notification)
                                                              │ EMITS_EVENT
                                                              ▼
                                                   (EventType settlement.failed) ──ROUTED_TO──▶ notification listener
```
Tracing a chain = alternate `EMITS_EVENT` (its `destService` tells you the next hop) and the
matching `ROUTED_TO` edge (filtered by `service = destService`).

---

## 5. Implementation plan (phased, each phase independently testable)

### Phase 0 — Constant & env resolution helper  *(foundation)*
**Why:** the detector needs to turn `EventGroupSettlement + CharDot + EventActionFailed` and
`svcenv.QueueEventURL` into real strings. These are ordinary `const` declarations in each
service repo (`constants/`, `env/`).

**What:** a new `constResolver` (new file `libs/indexer-go/static/const_resolver.go`).
- One pre-pass over the service's `.go` files collecting **string const declarations** into
  `map[string]string` keyed by bare name (`EventActionFailed`) and `pkg.Name`
  (`constants.EventActionFailed`).
- Resolve values transitively: a const whose value is `A + B` is resolved by looking up `A`, `B`.
- Public helpers: `ResolveString(expr ast.Expr) (val string, fullyStatic bool)` — handles
  `BasicLit`, `Ident`, `SelectorExpr`, and `BinaryExpr(+)`; returns partial strings + `false`
  when an operand is a non-const variable.
- `QueueToService(queueURL string) string` — split on `.`, return 2nd segment.

**Files:** new `const_resolver.go`. Built once per service run and injected into the detector.

---

### Phase 1 — Producer detection upgrade (semantic event names + EventType nodes + EMITS_EVENT)
**Why:** replace today's coarse "eventType = queue env-var name" with the real event name, and
create the hub nodes + producer edges.

**What:** extend `libs/indexer-go/static/event_call_detector.go`.

1. **Anchor on the `AsyncMessage` literal.** In `processPublishCallExpr`, when we detect a send
   (existing `queue.SendSQSMsg`/`SendDelaySQSMsg`, or var/field SQS/Kafka/NATS patterns), locate
   the `&queue.AsyncMessage{...}` composite literal for the message argument and read its
   `EventType:` field expression. Resolve it via `constResolver.ResolveString`.
   - Fully static → `event = "settlement.failed"`, `dynamic=false`.
   - Group-only (`"payout." + <var>`) → `event = "payout.*"`, `dynamic=true`.
   - Plain variable → go to step 2.

2. **Bridge the switch-relay** (the `TriggerSettlementStatusUpdateEvent` case). Implement a small
   intra-repo resolver (`EventEmissionResolver`, may live in `const_resolver.go` or a new
   `event_emission_resolver.go`) run once per service:
   - **Pass A:** mark *emitter functions* = functions that contain a transport send. Record the
     resolved `destQueue`/`transport` and whether their `AsyncMessage.EventType` is static or a
     parameter/variable.
   - **Pass B:** for every function, collect resolvable event-name expressions — both
     `AsyncMessage.EventType` fields and `<var> = <group> + CharDot + <action>` /
     `<var> = <eventConst>` assignments (these are the `switch` cases).
   - **Attribution:** a function is a *producer of event E* if either (a) it is an emitter with a
     static name E, or (b) it assigns name E **and** calls an emitter function (matched by name,
     same repo). Case (b) attributes all enumerated `switch` names to the trigger function and
     borrows the callee emitter's `destQueue`.
   - Output: `[]{producerFuncID, event, group, action, destQueue, transport, dynamic}`.

3. **Write nodes/edges** (through the existing `callNodeBuffer`):
   - Upgrade the `OutboxCall` node props with `eventType`(semantic), `eventGroup`, `eventAction`,
     `destQueue`, `destService = QueueToService(destQueue)`, `dynamic`.
   - `addEventTypeNode(nodeKey, props)` → merge the `EventType` hub.
   - `addEmitsEventEdge(outboxCallNodeKey, eventTypeNodeKey, props)` → `EMITS_EVENT`.
   - Keep existing `CALLS_API` (producer fn → OutboxCall).
   - **Remove** the now-obsolete inline `linkProducerToConsumer` / `resolveConsumerServiceID`
     (property-equality matching) — the resolver (Phase 3) replaces it and is order-independent.

**Files:** `event_call_detector.go` (major), new `event_emission_resolver.go`,
`call_node_buffer.go` (new buckets — see Phase 6/models), and the detector-pass wiring in
`scip_indexer.go` (~lines 1420–1493, where detectors are constructed with the buffer) to build
& inject the `constResolver`/`EventEmissionResolver`.

---

### Phase 2 — Consumer/listener indexing (mark handlers + listener entries)
**Why:** the resolver needs to know, per service, (a) which function is the SQS listener entry
for a queue, and (b) best-effort, which function handles a given event.

**What:** extend `event_call_detector.go` (or a small `consumer_detector.go`):
1. **Listener entry (tier-1 anchor):** detect `server.SQSListener{ QueueURL: <q>, Route: <fn> }`
   composite literals. Resolve `<q>` via `constResolver` → queue string → mark `<fn>` (the route
   entry) with `listensOnQueue = <queueURL>` and `listensService = <thisService>`.
2. **Per-event handler (tier-2 signal):** mark functions that reference an event's group **and**
   action constants (the `switch` cases / handler bodies) with `handlesEvent = "<group>.<action>"`
   (multi-valued). This is heuristic and best-effort by design.

Replaces the old `consumesEvent`/`ReceiveMessage`-based consumer detection, which never fired
for the switch-based router.

**Files:** `event_call_detector.go` (consume path), reusing `constResolver`.

---

### Phase 3 — Async resolver pass (`EMITS_EVENT`/`OutboxCall` → `ROUTED_TO`)  *(cross-service, post-index)*
**Why:** links must be written *after all services are indexed*, exactly like the gRPC resolver.

**What:** add a `resolveAsyncConsumers` method to `CrossServiceHandlerResolver`
(`libs/indexer-go/pipeline/cross_service_resolver.go`), called from `Resolve()` alongside
`resolveGRPC`/`resolveHTTP`. Two tiers (mirrors the proto/heuristic pattern already there):

- **Tier 1 — listener entry (confidence 1.0, always):** for each `EventType` node, find the
  `destService` from its incoming `EMITS_EVENT` edges; find that service's function marked
  `listensOnQueue = <the emit's destQueue>`; write `ROUTED_TO {service, tier:'entry', conf:1.0}`.
- **Tier 2 — precise handler (confidence ~0.8, best-effort):** within `destService`, find a
  function marked `handlesEvent = <event>` (or matching group+action constants); write
  `ROUTED_TO {service, tier:'handler', conf:0.8}`. Skip silently if none.

Group-fallback hubs (`payout.*`) resolve tier-1 only.

**Files:** `cross_service_resolver.go` (new method + call in `Resolve`).

---

### Phase 4 — Pipeline & CLI wiring
**Why:** make it run automatically during indexing and on demand.

**What:**
- `ResolveCrossServiceHandlersStage` (`libs/indexer-go/pipeline/stages.go`) already calls
  `resolver.Resolve(ctx)` — since Phase 3 adds the async pass *inside* `Resolve`, both the
  pipeline stage **and** the standalone `resolve` command (`apps/cli/main.go:175`) pick it up
  with **no extra wiring**. Verify only.
- Confirm `DefaultStages`/`DefaultTiers` in `pipeline.go` still place resolution after full
  ingest (they do). No change expected.

**Files:** verification pass over `stages.go`, `pipeline.go`, `apps/cli/main.go`.

---

### Phase 5 — Node-summary event effects
**Why:** the auto-generated one-line node summaries should mention emitted events so they show up
in search/hybrid results.

**What:** in `libs/indexer-go/static/node_summary_generator.go`, the effects fetch already has an
"events emitted" sub-query (~line 243). Point it at the new
`(fn)-[:CALLS_API]->(:OutboxCall)-[:EMITS_EVENT]->(et:EventType)` path and format effects as
`emits settlement.failed, settlement.succeeded`. `formatEffects` (~line 513) already clamps length.

**Files:** `node_summary_generator.go` (query + formatting only).

---

### Phase 6 — Models, node keys, buffer, schema  *(supporting changes, do alongside Phases 1–3)*
- `libs/core-models-go/node.go`: add `EventTypeNode NodeType = "EventType"`; add an `EventType`
  struct; add a `NodeFactory` case; extend the `OutboxCall` struct with the new fields.
- `libs/core-models-go/relationship.go`: add `EmitsEventRel = "EMITS_EVENT"` and
  `RoutedToRel = "ROUTED_TO"` consts (+ factory entries if the factory enumerates types).
- `libs/core-models-go/nodekey.go`: add `EventTypeNodeKey(group, action)` →
  `"eventtype:<group>.<action>"`, and (optional) an `OutboxCallNodeKey` helper to replace the
  inline `fmt.Sprintf` in the detector.
- `libs/indexer-go/static/call_node_buffer.go`: add `eventTypes` node bucket + `emitsEvent`
  edge bucket; add a `flushRelsByBothNodeKeys` helper (both endpoints matched by `nodeKey` —
  a small variant of the existing `flushRelsByTargetNodeKey`); flush them in `flush()` and clear
  in `reset()`.
- `libs/schema-go`: add a uniqueness constraint / index on `EventType.nodeKey` (mirror the
  existing `OutboxCall`/`GRPCCall` constraints).

---

### Phase 7 — MCP trace tool
**Why:** make the new graph usable by an LLM in one call.

**What:** add `codegraph_trace_event` in `apps/mcp-server-go` (model after the existing
`codegraph_cross_service_flow` tool). Input: an event name (e.g. `settlement.failed`). Output:
- **Producers:** services + functions with `EMITS_EVENT` to that `EventType`.
- **Listener(s):** `ROUTED_TO` targets (entry + precise handler, with confidence).
- **Downstream:** follow the handler's `CALLS`→fan-out `OutboxCall`→`EMITS_EVENT` to the next
  `EventType`, recursively (depth-limited), producing the settlement→event→notification chain.

**Files:** new tool file under `apps/mcp-server-go` + registration in the tool list.

---

## 6. Files to touch (summary)

| Area | File | Change |
|------|------|--------|
| Detector | `libs/indexer-go/static/event_call_detector.go` | semantic event extraction, EventType+EMITS_EVENT, listener/handler marking; drop inline consumer linking |
| Detector | `libs/indexer-go/static/const_resolver.go` *(new)* | const + env string resolution, queue→service |
| Detector | `libs/indexer-go/static/event_emission_resolver.go` *(new)* | switch-relay attribution (Pass A/B) |
| Detector wiring | `libs/indexer-go/static/scip_indexer.go` | build & inject resolvers into the detector pass |
| Buffer | `libs/indexer-go/static/call_node_buffer.go` | EventType node bucket, EMITS_EVENT edge bucket, both-nodeKey flush |
| Resolver | `libs/indexer-go/pipeline/cross_service_resolver.go` | `resolveAsyncConsumers` (tier-1 + tier-2), call in `Resolve` |
| Summaries | `libs/indexer-go/static/node_summary_generator.go` | event-effects query + formatting |
| Models | `libs/core-models-go/node.go`, `relationship.go`, `nodekey.go` | new node label/struct, rel types, key builder |
| Schema | `libs/schema-go/...` | `EventType.nodeKey` constraint/index |
| MCP | `apps/mcp-server-go/...` | `codegraph_trace_event` tool |
| CLI/pipeline | `stages.go`, `pipeline.go`, `apps/cli/main.go` | verify only (auto-picked-up) |

---

## 7. Verification (end-to-end)

1. **Build:** `make build && make build-server` (must compile cleanly).
2. **Unit sanity:** table-driven tests for `constResolver.ResolveString` (literal, const,
   const+CharDot+const, group+var, plain var) and `QueueToService`
   (`"queue.settlement.payout"` → `settlement`).
3. **Re-index the three services** into a clean scope:
   ```
   ./bin/codegraph index scip ~/Workspace/Tazapay/settlement --service=settlement
   ./bin/codegraph index scip ~/Workspace/Tazapay/balance    --service=balance
   ./bin/codegraph index scip ~/Workspace/Tazapay/event       --service=event
   ./bin/codegraph resolve
   ```
4. **Producer check (Phase 1):**
   ```cypher
   MATCH (:OutboxCall {callerService:'settlement'})-[:EMITS_EVENT]->(e:EventType)
   RETURN e.eventType ORDER BY e.eventType
   ```
   Expect `settlement.failed`, `settlement.succeeded`, `settlement.cancelled`,
   `settlement.initiated`, `payout.*` and friends — the switch cases in
   `TriggerSettlementStatusUpdateEvent` / `TriggerPayoutEvent`.
5. **Listener check (Phase 3):**
   ```cypher
   MATCH (e:EventType {eventType:'settlement.failed'})-[r:ROUTED_TO]->(f)
   RETURN r.tier, r.confidence, f.name
   ```
   Expect a tier `entry` edge to the event-svc route entry and, best-effort, a tier `handler`
   edge to `settlementUpdateEvent`.
6. **Full chain / propagation:** call the new `codegraph_trace_event` MCP tool with
   `settlement.failed`; confirm it shows **settlement (producer) → event (listener) →
   notification (downstream, via fan-out)**.
7. **Regression:** confirm existing gRPC/HTTP `RESOLVES_TO` counts are unchanged (the async pass
   is additive) and `make test` passes.

---

## 8. Risks & judgement calls (flagged for review)

- **Tier-2 handler match is heuristic** (constant-reference matching against the nested `switch`).
  Accepted per design decision; tier-1 entry edge is always correct, so no event is ever
  orphaned. Low-confidence tier-2 edges carry `confidence:0.8` and are clearly labeled.
- **Over-approximation of switch emitters:** a function like `TriggerSettlementStatusUpdateEvent`
  is linked to *all* events its `switch` can emit, not the one it emits on a given call. This is
  correct for a static graph (the function *can* emit any of them) and matches how the sync
  resolver treats multi-branch calls.
- **Cross-repo constants:** if an event/queue constant is ever imported from a shared module
  outside the indexed service repo, `constResolver` will see a partial value and fall back to a
  group hub. Observed usage is all in-repo (`constants/`, `env/`), so this should be rare; the
  group-fallback keeps the edge alive when it happens.
- **Kafka/NATS:** the same `EMITS_EVENT`/`EventType` model applies; queue→service resolution is
  SQS/Tazapay-specific. Non-SQS transports still get an `EventType` hub but may lack a
  `destService` (tier-1 resolves only when a listener anchor exists).
