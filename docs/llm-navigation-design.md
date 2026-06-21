# CodeGraph as a Map for an AI — Design Report (Plain-English Edition)

> **What this is.** Think of an AI assistant (me) as a new engineer dropped into a huge,
> multi-service codebase and asked: *"show me, start to finish, what happens when someone
> creates a payout."* I could read thousands of lines of code to figure that out — slow and
> expensive. Or I could look at a good **map** first, find the few spots that actually
> matter, and read only those.
>
> CodeGraph is meant to be that map. This document explains, in simple terms, what the map
> should let me do (the MCP functions) and what each point on the map should tell me (the
> node information) so I can answer questions while reading as little code as possible.
>
> This is a **design/wish-list** document, not code. A later pass will compare it to what
> exists today. To make that easy, each idea notes the closest thing you already have.

---

## 0. The one idea everything rests on

**The graph is a map, not the territory.**

A map of a city doesn't contain the actual buildings — it shows you where things are and how
roads connect them, so you can decide where to drive *before* you drive there. CodeGraph is
the same: it shouldn't hold all the code, it should hold *pointers to the code* plus just
enough labelling that I can plan my route.

Here's my routine when tracing how something works:

```
find the start  →  see the whole route  →  follow it across services  →  zoom in on a few stops  →  finally, READ the code at those stops
   └────────────────── all of this is "reading the map" (cheap) ──────────────────┘                        └─ the only expensive part ─┘
```

The whole point: the map should be good enough that by the time I open a real file, I
already know it's one of the two or three files that actually matter. Everything in this
document serves that goal, and it comes down to two questions:

1. **The functions** — Can I see the shape of a *whole* flow, *across services*, in one or
   two map-lookups? (Section 3)
2. **The labels** — Does each point on the map tell me enough — in one line — that I can
   decide "do I need to open this?" without opening it? (Section 4)

---

## 1. What I actually do when I trace a flow

Whatever you ask — *"trace the create-payout API"* or *"what happens when a refund comes
in"* — I go through the same five steps. The map should match these steps one-for-one.

| Step | In plain words | What it costs me when the map is bad |
|------|----------------|--------------------------------------|
| **1. Find the start** | Turn a vague request ("create payout") into the exact function where the request first lands. | A bunch of keyword searches and reading router files just to find the front door. |
| **2. See the route** | Get the *main* steps in order — check input, save to DB, call another service, send an event — not every tiny helper. | Reading the whole handler and every function it calls, over and over. **This is the biggest waste.** |
| **3. Cross services** | When the flow calls another service, jump into *that* service and keep following. | Manually figuring out which other repo to open, finding the handler there, and repeating. Often I just give up at the boundary. |
| **4. Zoom in** | For the 2–3 steps that matter, check the details to confirm they're relevant. | Opening lots of files just to throw most of them away. |
| **5. Read** | Open the *handful* of files that truly matter and reason about them. | (This is the *only* step that should involve reading code.) |

**The takeaway:** steps 1–4 should be answerable from the map alone. If I have to read code
to do step 2 or 3, the map isn't doing its job.

---

## 2. The rules that keep map-lookups cheap

These are the habits that make the map fast to use. They're also the checklist for judging
the tools you have today.

1. **Give directions, not the building.** Every result should be a tiny pointer: a stable
   ID + a name + a `file:line` + a one-line "what it is." Never hand me full source unless I
   specifically ask for it. *(Good news: most of your current tools already do this.)*

2. **Bundle the journey into one trip.** The expensive part isn't the size of an answer —
   it's the back-and-forth. One lookup that returns the *whole* route beats fifteen lookups
   that each take one step. Always prefer one fat, complete answer.

3. **Hand me a key I can reuse.** Every result should include the ID I need for my *next*
   question, so I never have to search for the same thing twice. *(Today some tools take a
   plain function name, which is ambiguous when two services share a name — more on that
   in Section 4.)*

4. **Let me pick the zoom level.** Offer three levels, cheap by default:
   - **Outline** — just the step type and target (e.g. "saves to the `payouts` table"). Tiny.
   - **Summary** — the above plus a one-line "what this step does." **The default.**
   - **Full** — the above plus signatures, conditions, arguments. Only when I ask.

5. **Label things in human terms.** A step should already read like *"checks the payment
   isn't a duplicate"* or *"reserves money in the balance service"* — not just a function
   name. That sentence is exactly what I'd otherwise read code to figure out.

6. **Hide the noise.** Logging, metrics, error-wrapping, plumbing — leave these out of the
   route by default (with a switch to include them). Show me only the steps that actually
   *do* something.

7. **Say when you've cut things off.** If a list is trimmed, tell me, and tell me how to see
   the rest. Quietly hiding steps makes me think I've seen the whole flow when I haven't —
   that's worse than a big answer.

8. **Cross service boundaries automatically.** "Start to finish" usually means the request
   travels through several services. Following that jump should be built into the main route
   lookup, not a separate chore I have to stitch together by hand.

---

## 3. The map functions I'd ideally have

Seven functions cover the whole routine. Fewer, bigger, and they snap together. For each, I
give what it does, why it saves effort, and the closest thing you have today (for the later
comparison).

### 3.1 `locate` — turn a request into a starting point

> **Replaces all the searching in Step 1.**

- **You give it:** a phrase ("create payout"), *or* a route (`POST /v3/payouts`), *or* an RPC
  name, *or* a handler name. Plus optional filters.
- **It gives back:** a short ranked list. Each item: the ID, the name, the `file:line`, what
  kind of entry it is (API / background worker / scheduled job), the contract it serves (the
  route or RPC), a one-line description, and a confidence score.
- **Why it helps:** instead of "search, filter, read a router file to figure out which match
  is real," I get the front door in one shot — and I can hand it a human phrase *or* an exact
  name, so I don't have to guess which search tool to use.
- **Today:** spread across `codegraph_search`, `codegraph_get_entry_points`, and
  `codegraph_hybrid_search`. There's no single "give me the entry point for this action or
  route" call.

### 3.2 `get_flow` — show the whole route, across services *(the most important one)*

> **Answers Steps 2 and 3 for the entire flow in one call.**

- **You give it:** the starting ID (from `locate`), a zoom level (outline / summary / full),
  a depth limit, and whether to follow calls into other services (yes, by default).
- **It gives back:** an **ordered list of the meaningful steps**. Each step tells me:
  - its position and type (check input / compute / database / gRPC call / HTTP call / send
    event / branch / loop / transaction / start goroutine / return),
  - a one-line "what it does,"
  - the concrete target (the table and operation, the `service.method`, the event topic),
  - the ID and `file:line` so I can zoom in or read,
  - the surrounding context (inside which `if`, which transaction, which goroutine),
  - and, when a step calls another service, a pointer into *that service's* handler. With
    cross-service following on, that handler's steps are **folded right into the route**, so
    a Settlement → Balance → Ledger journey comes back as one connected picture.
- **Why it helps:** this is the heart of the whole system. Rebuilding this route by reading
  code means reading every function the flow touches, in every service. Here it's a single
  compact answer. And folding in the cross-service jumps is what makes "start to finish"
  actually reach the finish — instead of stopping at the first service boundary.
- **Today:** the pieces exist — `codegraph_generate_flows` (it even pre-computes a behavioral
  summary), `codegraph_expand_step`, `codegraph_rpc_anatomy`, `codegraph_cross_service_flow`,
  and `codegraph_trace_call_graph` — but they're **separate calls**, and the cross-service
  jump isn't folded into the route automatically. Merging them into one zoomable,
  cross-service call is the single biggest improvement.

### 3.3 `expand` — full detail on one stop, still without opening the file

> **Step 4, for a single point on the map.**

- **You give it:** an ID and a zoom level.
- **It gives back:** the signature, the description, a **summary of side-effects** (which
  tables it touches, which services it calls, which events it sends, what auth it checks —
  see Section 4), who calls it, what it calls, and the conditions/arguments at this spot. No
  raw code.
- **Why it helps:** lets me confirm "yes, this is the validation that matters" from the map
  alone. The side-effects summary is usually what saves me from opening the file.
- **Today:** `codegraph_analyze_function` (callers/callees) and `codegraph_rpc_dependencies`
  (DB/RPC/events) cover parts of this, but they're not combined and they key off a name.

### 3.4 `read` — the actual code, by a reliable key

> **Step 5 — and the only function that should ever return code.**

- **You give it:** an ID (best) or a name plus service; optionally some surrounding lines.
- **It gives back:** the exact code for that node and its precise `file:line` range (so I can
  also just open the file myself).
- **Why it helps:** this is the *only* place a full body should appear. Using a stable ID
  instead of a bare name avoids the "two services both have a `Create` function" mix-up that
  otherwise wastes calls.
- **Today:** `codegraph_get_source` — but it takes only a function name, which is ambiguous
  across services.

### 3.5 `callers` — run the route backwards ("what triggers this?")

- **You give it:** an ID, a depth limit, and whether to cross services.
- **It gives back:** who reaches this point, working backwards, across services too, as
  pointers with one-line descriptions.
- **Why it helps:** answers "who calls this RPC?" or "who writes to this table?" as a map
  instead of a pile of search results.
- **Today:** `codegraph_trace_call_graph` (upstream direction) and `codegraph_find_references`.
  The gap is following the trail backwards across service boundaries.

### 3.6 `resolve` — "this call goes... where, exactly?"

- **You give it:** a call identifier (an RPC name, an HTTP route, or a call-site ID).
- **It gives back:** the handler on the other side — its ID, service, `file:line`, and how
  confident the match is.
- **Why it helps:** the single "jump across the boundary" move, for when I'm mid-trace and
  just need the other side. (`get_flow` uses this internally; having it on its own lets me
  hop without pulling a whole route.)
- **Today:** this exists as connections in the graph and inside `codegraph_cross_service_flow`,
  but not as a simple "resolve this one call" function.

### 3.7 `search` — general lookup (the safety net)

- **You give it:** a query, an optional type filter, and whether to match by meaning.
- **It gives back:** matching nodes as pointers (ID, name, type, `file:line`, one-liner).
- **Why it helps:** the fallback for when I don't have a key yet and `locate` is too
  flow-specific. Keep it — just make it return the same tidy pointer shape as everything else.
- **Today:** four overlapping tools (`codegraph_search`, `codegraph_hybrid_search`,
  `codegraph_vector_search`, `codegraph_search_by_comment`). Consider one tool with a "mode."

### What I'd happily set aside for flow-tracing

The architecture tools — `service_architecture`, `service_dependency_map`,
`service_dependencies`, `cross_service_calls`, `service_api_calls`, `list_services` — are
great for "how is the system shaped" questions, but they're not on the path for "trace this
one flow." Keep them as their own group; just don't make me reach for them mid-trace. The
document tools (`index_documents`, `link_docs_to_code`, `intelligent_link`, `search_docs`)
are a separate feature again.

---

## 4. What each point on the map should tell me

The rule: **a node should carry just enough to let me decide whether to open its file.** You
already store where things are and what they're called — that part is strong. The missing
piece is *meaning*: a one-line "what it does" and a "what it affects." Below, ✅ means you
have it today and ➕ means it's the addition that would save the most effort.

### 4.1 The two labels almost every code node is missing

These two, added to functions and methods (and shown on each route step), would help more
than any new function:

- ➕ **A one-line "what it does"** — e.g. *"checks the idempotency key and rejects
  duplicates."* You have a `Docstring` field, but it's often empty and, when present, it's
  the author's prose rather than a crisp behavior label. This is the single thing I most want
  on every node, because it's what makes the "summary" zoom level actually useful without a
  read. (You already write a behavioral summary at the whole-*flow* level — push that same
  idea down to individual functions.)
- ➕ **A "what it affects" summary** — a small set of tags like: writes to `payouts`, reads
  `ledger`, calls `balance.Reserve`, sends `payout.created`, checks team membership. You can
  *rebuild* this today by walking the database/call/event connections (and
  `rpc_dependencies` does exactly that on demand), but storing the summary *right on the
  function* means one lookup answers "does this do anything I care about?" instead of several.

### 4.2 Per-type wish-list

| Node | ✅ Already has (selected) | ➕ Add to make navigation cheap |
|------|---------------------------|----------------------------------|
| **Function / Method** | name, signature, return type, file, lines, exported?, complexity, docstring, "is this an RPC handler?" | **one-line "what it does"**, **"what it affects" tags**, a **layer label** (handler / service / repository / helper / transport) so noise can be filtered, and an "is this just noise?" flag |
| **Service** | name, language, version, repo URL | a one-line purpose, simple counts (#RPCs, #entry points), which proto packages it owns |
| **File** | path, language, size, line count, hash | a one-line "what this file is for" (so I can skip opening it just to orient myself) |
| **Flow** | name, entry point, type, behavioral summary, spine hash | what **triggers** it (route / RPC / topic), which **services** it touches, and what it ultimately **writes or sends** — so `locate` → `get_flow` can show the payoff up front |
| **gRPC call / HTTP call** | caller, target service, proto package/service/method or URL, file, line | the **resolved handler's ID stored right here** (so I skip a hop) and the match confidence |
| **Outbox/event call** | caller, transport, event type, queue/topic, file, line | **who consumes this topic** — that's what closes the loop on async, end-to-end flows |
| **DB call** | table, operation, query pattern, repository interface/method, file, line | which **columns** it touches (useful for "who writes column X" — ties into your column-rename safety rule) and a one-line "what entity this is" |
| **Proto method** | name, request type, response type | a one-line "what this RPC is for"; the handler's ID and caller IDs stored here |
| **Proto contract** | proto package/service/file, owning service | (already a good meeting-point node; mostly just needs the stored handler/caller links above) |
| **Control-flow / transaction / goroutine scopes** | kind, condition, lines, depth/isolation | already well-shaped — the main thing is to **show them inline on each route step**, not as a separate lookup |
| **Symbol** | SCIP symbol, kind, display name, docs | fine as the precise-resolution backbone; not on the flow-tracing path |

### 4.3 Connection details that matter for routes

You already store rich details on the connections between nodes. The ones that matter most
for routes — keep showing these:

- **Call order** ✅ — this is what lets a route be shown *in order*. `get_flow` should always
  sort by it.
- **Nearby comment / literal arguments / method chain** ✅ — cheap hints; great raw material
  for writing the one-line "what it does," and handy at the "full" zoom level.
- **Resolution confidence and method** ✅ — when a step jumps to another service, show how
  sure we are. A solid proto match and a 60%-confidence guess should look different to me so I
  can weight my answer accordingly.
- **"Inside this condition / transaction / parallel block"** ✅ — exactly the context each
  route step needs. It's already recorded; it just needs to ride along in the route answer
  instead of requiring a separate lookup.

---

## 5. What makes a map *expensive* to use (things to avoid)

A "don't do this" list — each one costs me dearly when tracing a flow:

1. **Handing me code I didn't ask for.** If a tool returns full source when I only asked
   "what's here," most of that answer is wasted. *(Your tools are mostly clean on this.)*
2. **One step per call.** Making me walk the route one hop at a time. The fix is the fat
   `get_flow`.
3. **Looking things up by bare name.** In a multi-service codebase, two services share
   function names; a name-only lookup leads to mix-ups and wasted retries. Use IDs.
4. **No human-readable labels.** Pointers with no "what it does" force me to open files just
   to learn what something is — then the map is barely better than a plain symbol index.
5. **Making me cross service boundaries by hand.** If following an RPC into another service
   is *my* job, I'll often run out of budget and stop at the boundary — which is usually the
   most important part of an end-to-end flow.
6. **Trimming silently.** Cutting a list without telling me makes me confidently wrong about
   what I've seen.
7. **Too many overlapping tools.** Four search tools and five flow tools means I burn effort
   *choosing* — and sometimes pick the wrong one. Fewer tools with a zoom level are better.

---

## 6. A real example — "trace create-payout, start to finish"

Here's how the ideal map plays out, with rough cost.

| Call | What comes back | ~Cost |
|------|-----------------|-------|
| `locate("create payout")` | one entry: the `FundPayout` handler in settlement, route `POST /v3/payouts`, "creates a fund payout," 94% sure | tiny |
| `get_flow(that entry, summary, cross-service on)` | the ordered route: check for duplicate → save to `payouts` → open transaction → call `balance.Reserve` **(its steps folded in:** update balances → send `balance.reserved`**)** → send `payout.created` event → return. Every step has its type, one-liner, target, ID, `file:line`, and match confidence. | moderate |
| `expand(the balance.Reserve handler)` | signature + "affects: updates `balances`, sends `balance.reserved`," plus who calls it and the conditions | small |
| `read(...)` ×2 | the actual code of the two functions whose logic I genuinely need to reason about | the real cost |

Total: a correct, cross-service, start-to-finish picture **plus** the two reads that
actually matter — for a small fraction of what it would cost to do it blind. Doing the same
by searching and reading `fund_payout.go`, its helpers, the balance client, the balance
handler, and its repository — with no map — costs many times more and usually stalls at the
first service boundary. The map's whole job is to turn the first three rows into cheap
pointers so the last row is small and certain.

---

## 7. The highest-value changes (ranked)

Most benefit per unit of effort, from my point of view as the one using the map:

1. **Add the two labels** — "what it does" + "what it affects" — to every function/method
   (Section 4.1). Makes every pointer explain itself. **Biggest single win.**
2. **Merge the flow tools into one cross-service, zoomable `get_flow`** (Section 3.2) that
   folds in the other-service handlers. Turns many trips into one and makes "end-to-end" real.
3. **Use stable IDs, not bare names**, for `read` / `expand` / `analyze` (Section 3.4). Ends
   the cross-service name mix-ups.
4. **One `locate`** that takes a phrase *or* a route *or* an RPC name and returns the entry
   point with its contract (Section 3.1).
5. **Store the cross-service links directly on the call nodes** (the resolved handler's ID,
   the topic's consumers — Section 4.2) so crossing a boundary is reading a field, not a hop.

---

## 8. Setup for the comparison (delivered in full in Section 9)

This document is laid out so the comparison is mostly mechanical. Section 9 carries it out in
detail; this is the one-paragraph orientation:

- **Comparison 1 — functions (lower priority).** Line up Section 3's seven ideal functions
  against the 24 tools you have now. The "Today:" note under each one is the starting point.
  The short version: *the abilities mostly already exist but are split up*; the work is
  combining them, folding in cross-service jumps, and switching to IDs — not gathering new
  data.
- **Comparison 2 — node labels (higher priority).** Line up the ➕ column in Section 4.2
  against the fields you store today (in `libs/core-models-go/node.go` and
  `relationship.go`). The short version: *the "where" and "what it's called" data is
  excellent*; the gap is the **meaning layer** ("what it does," "what it affects," the layer
  label) plus a few **stored cross-links** that trade a little indexing work for far fewer
  lookups later.

> Where the "today" facts come from: the node definitions in `libs/core-models-go/node.go`,
> the connections in `libs/core-models-go/relationship.go`, and the MCP tool list in
> `apps/mcp-server-go/main.go` (the `handleToolsList` section, roughly lines 200–691).

---

## 9. Calculation of the delta

This section does the actual comparison: for both **node enrichment** and **MCP function
enrichment**, what we already have, what's missing, and — tailored to our case (a multi-service
Go codebase using gRPC + HTTP v3 + outbox/SQS, where the job is tracing one flow end-to-end
across services) — why closing the gap helps.

Two ratings are used throughout:

- **Effort** — how much work to build: 🟢 cheap (mechanical / projection) · 🟡 moderate ·
  🔴 heavy (needs generation or new pipeline stages).
- **Payoff** — how much it saves me per flow-trace: ⭐ to ⭐⭐⭐.

### 9.0 First, the "how do we even produce this?" question (because it sets the effort)

Before the tables, the feasibility point that drives every effort rating below. The three zoom
levels are **not** three generation jobs:

| Piece of enrichment | How it's actually produced | Cost |
|---------------------|----------------------------|------|
| **Outline** level (step type + target) | Direct projection of existing AST/SCIP facts + graph edges (`DBCall.Table`, `GRPCCall.ProtoMethod`, `OutboxCall.QueueOrTopic`). | 🟢 free, deterministic |
| **Full** level (signature, conditions, args, docstring) | Already extracted by AST/SCIP; just surface it. | 🟢 free, deterministic |
| **"What it affects" tags** | Aggregate the `CALLS_DB` / `CALLS_SERVICE` / `OutboxCall` edges that already exist — a graph query, then denormalise onto the node. | 🟢 free, deterministic |
| **The one-line "what it does"** | Template from the structured facts as a baseline; optionally upgrade *only* RPC handlers + service-layer functions with a **small local model** (e.g. Gemma 3 4B or a code-tuned 7B), cached by content hash so it runs once per code version. | 🟡 baseline free; LLM pass cheap & offline, on ~5–10% of nodes |

So almost the entire wish-list is mechanical. The only thing that *might* use a model is the
natural-language one-liner, and even that has a free template fallback. **No per-query LLM
cost** anywhere — enrichment happens at index time and is cached.

### 9.1 Delta — node detail enrichment *(higher priority)*

| Enrichment | Present today? | What's left | Why it helps our use case | Effort · Payoff |
|------------|----------------|-------------|----------------------------|-----------------|
| **One-line "what it does"** on Function/Method | ❌ Only `Docstring` (often empty, author prose) | Add a distilled behavioral field; template it, LLM-upgrade handlers/service-layer | Makes the default `summary` route readable without any file read — turns each step from a name into a sentence. This is what makes the whole map self-explanatory. | 🟡 · ⭐⭐⭐ |
| **"What it affects" tags** (tables / services / events / auth) | ⚠️ Derivable by walking `CALLS_DB`/`CALLS_SERVICE`/`OutboxCall` edges; `rpc_dependencies` does it on demand | Denormalise the summary onto the function node | One node lookup answers "does this touch the DB / call anyone / emit anything?" instead of a fan-out of edge queries. Critical in our SQS/outbox world for spotting side-effects fast. | 🟢 · ⭐⭐⭐ |
| **Layer label** (handler / service / repo / helper / transport) | ❌ | Classify at index time (path + signature heuristics) | Lets `get_flow` prune noise to the load-bearing steps — exactly the "show me the spine, not the plumbing" need. Our Go services have very regular layering, so heuristics will be accurate. | 🟢 · ⭐⭐ |
| **"Is this noise?" flag** (logging/metrics/getters) | ❌ | Derive from layer + name patterns | Same pruning goal; keeps routes short and cheap. | 🟢 · ⭐ |
| **Flow trigger + services-touched + terminal effects** on Flow nodes | ⚠️ Has `BehavioralSummary`, `EntrypointKey`, `FlowType`; not the trigger contract or touched-services list | Compute and store at flow-build time | Lets `locate` → `get_flow` show the payoff up front ("this flow touches settlement→balance→ledger and writes payouts") before I expand anything. | 🟡 · ⭐⭐ |
| **Resolved-handler ID + confidence on gRPC/HTTP call nodes** | ⚠️ Exists as the `RESOLVES_TO` edge | Denormalise the handler key + confidence onto the call node | Crossing a service boundary becomes a field read instead of a graph hop — directly powers `get_flow`'s inline cross-service folding, our single most important behavior. | 🟢 · ⭐⭐⭐ |
| **Consumer IDs on Outbox/event nodes** | ❌ | Link publishers to consumers via topic at index time | Closes the async loop: an event isn't a dead end, it points to who handles it. Essential for true end-to-end across our SQS flows. | 🟡 · ⭐⭐⭐ |
| **Columns + entity on DBCall** | ⚠️ Has table + operation, not columns | Capture column list during detection | Powers "who writes column X" precisely — and ties into the column-rename safety rule. | 🟡 · ⭐ |
| **Summary + denormalised handler/caller keys on ProtoMethod** | ⚠️ Has name/request/response; links exist as `IMPLEMENTED_BY`/`CALLED_BY` edges | Denormalise keys onto the node; add a one-line contract intent | Makes the proto contract a fast rendezvous: from an RPC name I get both sides in one read. | 🟢 · ⭐⭐ |

**Net read:** the *location and identity* layer is essentially complete. The gap is a thin
**meaning layer** (one-liner + affects-tags + layer) plus a handful of **denormalised
cross-links**. Almost all of it is 🟢 mechanical; only the natural-language one-liner is 🟡 and
it's a cheap, cached, offline pass on a small fraction of nodes.

### 9.2 Delta — MCP function enrichment *(lower priority, mostly consolidation)*

| Ideal function | Covered today by | What's left | Why it helps our use case | Effort · Payoff |
|----------------|------------------|-------------|----------------------------|-----------------|
| **`get_flow`** (one cross-service, zoomable route) | `generate_flows` + `expand_step` + `rpc_anatomy` + `cross_service_flow` + `trace_call_graph` | Merge into one call; **fold callee handlers inline** across services; add the outline/summary/full zoom switch | Collapses the many-trips problem and makes "end-to-end" actually reach the finish instead of stopping at the first service hop. The single biggest function-side win. | 🟡 · ⭐⭐⭐ |
| **`locate`** (phrase/route/RPC → entry point) | `search` + `get_entry_points` + `hybrid_search` | One entry point that accepts a phrase *or* route *or* `Service.Method` and returns the contract it serves | Removes the "which search tool do I pick, then disambiguate" dance at the very start of every trace. | 🟡 · ⭐⭐ |
| **`expand`** (full detail on one node, no source) | `analyze_function` + `rpc_dependencies` | Unify; key by node ID; include the affects-summary | One lookup confirms relevance of a step from the map. | 🟢 · ⭐⭐ |
| **`read`** (source by stable key) | `get_source` | Re-key from bare name → node ID; return precise line range | Ends the cross-service name collisions (two services with a `Create`) that waste retries in our multi-repo graph. | 🟢 · ⭐⭐ |
| **`resolve`** (one call → its handler) | Inside `cross_service_flow` + the `RESOLVES_TO` edge | Expose as a standalone call | Lets me hop a single boundary mid-trace without pulling a whole flow. | 🟢 · ⭐ |
| **`callers`** (reverse route across services) | `trace_call_graph` (upstream) + `find_references` | Add cross-service reverse resolution | Answers "who triggers this RPC / writes this table" across repos — common impact question. | 🟡 · ⭐⭐ |
| **`search`** (general lookup) | `search` + `hybrid_search` + `vector_search` + `search_by_comment` | Consolidate four tools into one with a `mode`; emit the standard pointer shape | Less time spent choosing among overlapping tools; consistent, cheap results. | 🟢 · ⭐ |

**Net read:** unlike the node side, there's **almost no missing capability** here — the
abilities already exist, just *fragmented across many tools*. The work is **consolidation**
(fewer, fatter, zoomable functions), **node-ID keying**, and **inline cross-service folding**
in `get_flow`. This is why it's lower priority: it's refactoring the surface, not building new
intelligence. It also depends on 9.1 — `get_flow`'s readable summaries and inline boundary
crossing only shine once the node-level one-liners and denormalised handler links exist.

### 9.3 Suggested order of work

Because the function side leans on the node side:

1. **🟢 Mechanical node enrichment first** — affects-tags, layer label, denormalised
   handler/caller/consumer links, DBCall columns. Free, deterministic, immediately useful.
2. **🟡 The one-line "what it does"** — template baseline for all nodes, then a cached small-LLM
   pass over handlers + service-layer functions.
3. **🟡 `get_flow` consolidation** — now that nodes carry meaning and cross-links, merge the
   flow tools into one zoomable, cross-service call.
4. **🟢 Re-key `read`/`expand` on node IDs and add `locate`/`resolve`** — the remaining surface
   cleanup.

The ordering means each step makes the next one more valuable, and the cheap deterministic
wins land before any model work.
