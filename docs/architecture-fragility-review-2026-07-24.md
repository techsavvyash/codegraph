# CodeGraph Architecture Review — Fragility Assessment

**Date:** 2026-07-24 · **Branch:** `FEAT/cross-service-rpc` · **Method:** full read of the cross-service RPC chain (`rpc_call_detector.go`, `scip_rpc_detector.go`, `grpcclient_getter_scanner.go`, `service_index.go`, `cross_service_resolver.go`, `api_surface.go`), the summary generator (`node_summary_generator.go`), plus verification queries against the live fleet graph (17 services indexed). Prior gap audits (2026-07-18, 2026-07-19) used as baseline.

---


## Executive summary

| Question | Verdict |
|---|---|
| 1. Cross-service RPC mapping quality | **Layered architecture, not patchwork — but one silent integration defect (the proto contract repo poisoning the service index) collapses the last mile: only 15% of gRPC calls resolve to a concrete handler, and 82% of CALLS_SERVICE edges point at the contract repo instead of the implementing service.** |
| 2. Summary generation quality | **Disciplined implementation, correct design decisions, genuinely useful output where effects exist (59% of 11,835 summaries). Three cheap quality wins remain; one inherited defect from finding F1.** |
| 3. Coupling / decay trade-off | **The coupling is to the platform team's `grpc-framework` wrappers, not to individual engineers' styles — that's the strongest anchor available. But decay is already observable at month-scale, not year-scale. The lever is not less coupling; it is *measurable* coupling: index-time coverage SLOs + a golden-fixture corpus.** |

Live-graph evidence gathered for this review:

| Metric | Value |
|---|---|
| GRPCCall nodes | 519 |
| … with CALLS_SERVICE edge | 503 (97%) |
| … CALLS_SERVICE pointing at Service **"proto"** (the contract repo) | **422 (82%)** |
| … with RESOLVES_TO handler edge | **76 (15%)** — 73 `proto` @1.0, 3 `name_match` @0.7 |
| HTTPCall nodes | 201 |
| … with CALLS_SERVICE edge | **0** |
| … with `url = "dynamic"` | 199 (99%) |
| OutboxCall nodes / with destService | 548 / 499 (91%) |
| REACHES_CALL edges (sync / async) | 2,442 / 4,547 |
| Function/Method summaries | 11,835 — 6,997 (59%) carry an effects clause |
| Summaries polluted with `proto.` targets | **428** |

---

## 1. Cross-service RPC mapping

### Architecture map

The chain is a five-layer pipeline, each layer with a distinct responsibility:

1. **Detection** — `rpc_call_detector.go` (AST: two-pass per-function var→client-type binding) and `scip_rpc_detector.go` (Cypher over ingested SCIP Symbol/Reference graph) emit `GRPCCall` / `HTTPCall` / `OutboxCall` nodes + `CALLS_API` edges.
2. **Identity** — `grpcclient_getter_scanner.go` builds the authoritative getter→owning-service table from the **proto import path of the client each getter actually constructs**.
3. **Arbitration** — `service_index.go` resolves names to Service nodes via primary/segment alias claims; contested aliases are dropped.
4. **Global resolution** — `pipeline/cross_service_resolver.go` writes `RESOLVES_TO` (call→handler, with `confidence` + `resolutionMethod` provenance), `ROUTED_TO` (event→consumer, three tiers), and bridges OutboxCalls into the sync traversal structure.
5. **Materialization** — `api_surface.go` precomputes `REACHES_CALL` closures (sync BFS depth ≤16, async continuation across self-consumed events, iterated to fixpoint).

### What is genuinely well-engineered (not patchwork)

- **The getter table is the single best design decision in the codebase.** It refuses to trust names (`GetPaymentService` → payin, `GetSOLBalanceService` → sol/BalanceService) and derives truth from the proto import path of the constructed client — including the proxy idiom where the declared return type and the constructed client disagree (`grpcclient_getter_scanner.go:130-146`). This is ground truth, not heuristics.
- **The alias-arbitration failure policy is principled**: primary claims own outright, contested segments are dropped and logged — "a miss yields no edge, never a wrong edge" (`service_index.go:189-212`). Deterministic, fail-closed, self-reporting.
- **REACHES_CALL is the best-designed subsystem**: depth bound chosen from a *measured* fleet maximum (12 observed, 16 bound, documented at `api_surface.go:78-84`); async propagation restricted to self-consumed events so closures never bleed across a shared bus; fixpoint with convergence check; `async` in the edge identity so sync/async coexist.
- **Provenance everywhere**: every RESOLVES_TO carries `confidence` + `resolutionMethod`, every ROUTED_TO carries `tier`. Consumers can discount heuristic edges. This is what allows the system to be honest about its own uncertainty.

The core is architecture with a philosophy (fail-closed, provenance-tagged, measured bounds), not accreted patchwork.

### Fragility register

> **STATUS 2026-07-24 — F1 and F2 FIXED and realized on the live graph.** F1 fix: `service_index.go` now excludes contract-repo services (own ProtoContract nodes, implement no `…Server` handler) from resolution via a new `loadContractRepoIDs`; `scip_indexer.go` DEPENDS_ON query got the same structural guard. F2 fix: the dead `resolveHTTP` pass and its helpers were deleted. Build + vet green, 414 tests pass, binary redeployed. Realized (non-destructively — deleted 454 `CALLS_SERVICE→proto` + 17 `DEPENDS_ON→proto`, re-ran `codegraph resolve`): **RESOLVES_TO 76 → 424 (15% → 82%)**, `CALLS_SERVICE→proto` **454 → 0** (now account 158, payment-router 50, onboarding 47, …), `DEPENDS_ON→proto` **17 → 0**. Residual 89 unresolved = ~38 calls into unindexed services + the new finding **F6** (fx/account receiverType casing mismatch, e.g. proto `FXService` vs Go `*FxServiceServiceServer`). The 428 polluted summaries clear on the next per-service reindex (the edges they read are now correct). Details in the fixes/F6 sections of the companion KB yaml.

**F1 — FIXED (was: confirmed live defect, high): the proto contract repo poisons the service index, and the poisoning disables the fallback that would have recovered.**

Mechanism, verified against the live graph:

1. All 191 `ProtoContract` nodes `BELONGS_TO` the Service named **`proto`** (the shared contract repo, indexed as its own service).
2. `service_index.go:97-109` (`addProtoContract`) registers each contract's proto service name (e.g. `FXService`) as a **PRIMARY** name claim *for the contract repo's Service id*. Primary claims beat the real implementer's claims by design.
3. `resolveByName("FXService")` therefore returns the `proto` Service → detectors write `CALLS_SERVICE → proto` (422 of 519 gRPC calls).
4. `resolveGRPC` then searches for handlers *inside the proto repo's subtree* — which contains no `…Server` implementations → no RESOLVES_TO.
5. Fatally, `resolveGRPCUnlinked` — the pass that finds the real handler fleet-wide by `receiverType ENDS WITH '<Proto>Server'` and *would have succeeded* — is gated on `NOT (gc)-[:CALLS_SERVICE]->()` (`cross_service_resolver.go:491`). Having a *wrong* CALLS_SERVICE edge disqualifies the node from the pass that produces a *right* one.

Downstream blast radius: RESOLVES_TO coverage 15%; cross-service flow BFS hops into `proto` and stalls; 428 node summaries read "calls `proto.FXService.GetBaseFx`" instead of "calls `fx.…`". Note the `targetService` *property* on the call nodes is correct (it comes from the getter table / name derivation, a different code path) — only the *edge* is wrong, which is why REACHES_CALL (property-compared) survived while RESOLVES_TO (edge-dependent) collapsed.

This is the textbook fragility signature of the system: **every layer is locally correct; the contract between layers (what `BELONGS_TO` means for a shared contract repo) was never validated.** Fix directions (any one suffices): exclude ProtoContract-derived claims from `byName` when the owning service is the contract repo itself; map contracts to their implementing service; or drop the `NOT (gc)-[:CALLS_SERVICE]->()` gate when the existing edge targets the contract repo.

**F2 — FIXED (was: dead heuristic kept alive, high): `resolveHTTP` resolves nothing.**
0/201 HTTPCalls have CALLS_SERVICE; 199/201 have `url="dynamic"` (URLs live in env/config, never literals — a fact the round-1 audit established). The handler match that remains is a bidirectional substring test between function names and URL paths (`cross_service_resolver.go:637-646`), under a comment admitting "APIRoute nodes no longer exist — HTTP handler resolution via route path unavailable." This is the one part of the chain that *is* patchwork: a heuristic whose preconditions have died, still shipping. Delete it or rebuild it on route-table extraction; keeping it costs credibility (a reader auditing this file will judge the rest by it).

**F3 — The SCIP detector is a string-fragment taxonomy (medium).**
`scip_rpc_detector.go` classifies call sites by `CONTAINS` fragments over symbol FQNs: `'/grpc/'`, `'_pb2_grpc'`, `'@grpc/grpc-js'`, `'axios'`, `'httpx'`, an 11-name `displayName IN [...]` outbox list, etc. Each fragment is an unverified assertion about how three ecosystems' indexers format symbols; none are covered by fleet evidence (the multi-language arms have never run against real TS/Python repos here). Two concrete hazards: `LIMIT 2000` truncates silently on large services (no warning, no stat), and `parseGRPCSymbol`'s token scan (`first token ending in "Client"`) plus `extractProtoPackage`'s "any path part containing `proto`" are exactly the kind of best-effort parsing that produced F1-class bindings. It works today because the Go fleet is uniform; it is the file most likely to break *invisibly* on a new language or SCIP indexer version.

**F4 — Dual detection paths spell the same fact two ways (medium).**
The AST path derives `targetService` from the getter table (canonical form, e.g. `paymentrouter`), the SCIP path from `TrimSuffix(protoService, "Service")` lowered (e.g. `payoutrouter` from `PayoutRouterService`). Both then feed string comparisons (`REACHES_CALL`'s `toLower(call.targetService) <> toLower(call.callerService)`) and the service index's alias forgiveness absorbs most divergence — but "two spellings reconciled downstream by a fuzzy index" is a standing invitation for the next F1. The AST secondary path (`field name ends with "client"`, `rpc_call_detector.go:259-267`) is the weakest detector still live: it produces `serviceName` guesses with no proto anchor at all.

**F5 — Ordering contracts are comment-enforced (low).**
`entryPredicate` in `api_surface.go` depends on the event detector having stamped `listensOnQueue`/`handlesEvents*` earlier in the same run; ROUTED_TO arrives later from the global resolver; summaries are generated before the resolver runs (see §2). The pipeline tiers encode this, but only in comments — there is no assertion that prerequisite properties exist before a stage consumes them. A future reordering (or a stage moved from CLI to server pipeline) fails silently by producing empty closures, not errors.

**F6 — Proto-contract name vs Go-impl receiver name mismatch defeats handler resolution (medium). OPEN — surfaced 2026-07-24 while realizing the F1 fix.**

Once F1 stopped mis-routing calls to the `proto` repo, 89 gRPC calls remained unresolved. ~38 target services that simply aren't indexed (no fix possible without indexing them). The other **~51 target *indexed* services and still fail to resolve** — and they fail for a single reason: the name the caller knows the service by (the **proto contract** name) is not the name the implementing service's **Go receiver type** carries.

Concrete, live case (fx, 33 of the 51):

| Source | Value |
|---|---|
| Proto contract / caller's `protoService` | `FXService` |
| fx's actual Go handler receiver (`GetBaseFx`) | `*FxServiceServiceServer` |

Two independent divergences stack: **casing** (`FX` vs `Fx`) and a **doubled segment** (`FxService`**`Service`**`Server` — the codegen/impl added an extra `Service`). The resolver (`cross_service_resolver.go`, `resolveGRPC` line ~420 and `resolveGRPCUnlinked` line ~524) matches with a **case-sensitive** `receiverType ENDS WITH ($protoService + 'Server')` and a `CONTAINS $protoService` fallback. `'*FxServiceServiceServer'` neither ends with `'FXServiceServer'` nor contains `'FXService'` (case), so both arms miss and no `RESOLVES_TO` / `CALLS_SERVICE` edge is written. Note the `*FXServiceServer` handlers (3, correct casing) *do* resolve — proving the mechanism works and the miss is purely nominal.

*This is not an F1 regression; it is the class-(c) name-heuristic decay the coupling section (§3) predicts — a binding keyed on a naming convention breaking the moment one service's convention diverges. It is worth a numbered finding precisely because it is the canonical example of that whole risk class made concrete.*

**Problems it can cause.**
- **Silent under-reporting of real cross-service dependencies.** These 51 calls *happen* at runtime; the graph says they resolve to nothing. An engineer using `cross_service_flow` / `rpc_anatomy` / `api_callers` sees a dead end where a live edge exists — the most dangerous failure mode for a code-intelligence tool, because absence reads as "no dependency," not "unknown." A blast-radius analysis before an fx change would miss its 33 inbound callers.
- **It is invisible.** Nothing distinguishes "unresolved because the target isn't indexed" (legitimate) from "unresolved because the names don't line up" (a defect). Both are just missing edges. Without a counter, this erodes silently as each new service picks its own receiver-naming style.
- **It scales with fleet growth, not calendar time.** Every new service that names its server type off-convention (or whose codegen doubles a segment, or uses different casing than its proto) adds its own slice of unresolved-but-real calls. Today it's fx + a little account; the mechanism guarantees more.
- **It quietly caps the F1 win.** F1 lifted resolution 15% → 82%; F6 is most of the remaining indexed-service gap. Left alone, "82%" is the ceiling and no one will know the missing 10-ish points are recoverable.

**How to fix (in ascending order of robustness / effort).**
1. **Case-insensitive receiver match** — `toLower(f.receiverType) ENDS WITH toLower($protoService + 'Server')`. One-line change, recovers the `FX`/`Fx` casing class immediately. *Caveat:* it is a genuine loosening of a fail-closed match, so pair it with rule 4 below — never ship a broadened matcher without a mis-bind counter, or F6 becomes a future false-positive finding.
2. **Normalize the receiver before matching** — strip a trailing `Server`, collapse a doubled `ServiceService` → `Service`, lower-case, then compare canonical forms on both sides (the same `canonicalServiceKey` idea already used in `service_index.go`). Handles both the casing *and* the doubled-segment divergence, and is still deterministic.
3. **Anchor on identity, not the name** — when the proto contract repo is indexed, `linkProtoImplementations` already writes `(ProtoMethod)-[:IMPLEMENTED_BY]->(handler)` from the proto method as ground truth. Resolving `GRPCCall → RESOLVES_TO → handler` *through* the `ProtoMethod` node (call's `protoService.protoMethod` → matching `ProtoMethod` → its `IMPLEMENTED_BY` handler) removes the receiver-name string match from the hot path entirely. This is the F4/§3 "prefer language/contract facts over name heuristics" direction applied here — the durable fix.
4. **Instrument regardless of which fix ships** — emit `% GRPCCall-into-an-indexed-service that resolved` as an index-time coverage stat (the §3 SLO idea). That number would have flagged F6 on day one instead of it surfacing incidentally; it is what converts this class of decay from silent to loud.

**How we benefit from fixing it.**
- **Immediate recall:** rules 1–2 recover ~33 fx calls (and account's slice) at near-zero risk — roughly a further **+6-8 percentage points** of gRPC resolution on top of F1's 82%, i.e. the graph goes from "most cross-service calls resolved" to "nearly all resolved for every indexed service."
- **Trustworthy negatives:** once name-mismatch is eliminated as a cause, an unresolved call means *exactly* "target not indexed" — a clean, actionable signal (go index that service) instead of an ambiguous gap. This is the larger win: it makes the *absence* of an edge meaningful.
- **A template for the whole class-(c) risk:** whichever fix ships (ideally 3 + 4), it becomes the worked example for every future name-convention divergence — normalize-then-match plus a coverage counter is the pattern §3 argues the indexer needs fleet-wide. F6 is small enough to be the pilot.
- **Locks in the F1 return:** without F6, ~10 points of the F1 headline are permanently stranded and mislabeled as unfixable. Fixing F6 is what makes the 82% actually approach the ~90% ceiling the fix was capable of.

### Grades

- gRPC chain: **B+ architecture. Was delivering C− results (F1); now A− at 82% resolution after the F1 fix.** The remaining gap for indexed services is F6 (name-convention mismatch); closing it takes resolution toward the ~90% ceiling.
- HTTP chain: **D** — non-functional for cross-service mapping (partially compensated at the CALLS_API level by the apiclient wrapper detector).
- Async/event chain: **A−** — the tiered ROUTED_TO + outbox bridging + self-consumed-event closure is the most complete and honest subsystem.

### Deep re-verification (2026-07-24, 2nd pass) — "is SCIP/AST used to its fullest? patchwork or elegant?"

A second forensic pass re-derived the answer from source + live graph rather than the prior register. Two new load-bearing findings emerged. Bottom line: **well-architected but heuristic-implemented — design A−, implementation B, precision-ceiling C+. Not patchwork; but neither SCIP nor AST is used to its fullest, and the two most precise facts the system already computes sit unused.**

Fresh evidence from this pass:

| Claim | Evidence |
|---|---|
| Resolution is precise, not guessy | 424/519 resolved; **410 @ confidence 1.0 (proto), 11 heuristic@0.6, 3 name_match@0.7** — 96.7% of resolved edges are exact |
| **SCIP does zero RPC work on this fleet** | All 519 GRPCCalls carry the AST nodeKey prefix; **0** carry `grpccall:scip:`. `scip_indexer.go:317` gates `if language==Go → AST else → SCIP`; the fleet is 100% Go |
| The load-bearing join is a string match | `cross_service_resolver.go:420,524`: `f.receiverType ENDS WITH $protoService+'Server'`, case-sensitive |
| A precise identity edge exists but is unused | **195 `IMPLEMENTED_BY` edges**, 132/191 ProtoContracts covered; the resolver never queries them |
| HTTP correctly abandoned | 199/201 HTTPCall `url="dynamic"` |
| Async is strongest | 356/548 OutboxCall resolved |

**F7 — SCIP's precise symbol identity is not used for RPC; the SCIP RPC detector is dead code on an all-Go fleet (medium, architectural).**
`scip_indexer.go:307-339` chooses the detector by language: Go → AST (`rpc_call_detector.go`), everything else → the SCIP-graph detector (`scip_rpc_detector.go`). Because the Tazapay fleet is 100% Go, **`scip_rpc_detector.go` (19KB) never executes** — confirmed live: 0 of 519 GRPCCalls carry its `grpccall:scip:` key. So SCIP's headline capability — precise, cross-language symbol resolution — draws **none** of the cross-service edges. SCIP is genuinely load-bearing on the *structural* layer (the 43KB `call_graph_scip.go`, the Symbol/Reference graph, `findEnclosingFunction`, IMPLEMENTS), but for the RPC question it is bypassed, and its string-taxonomy heuristics (F3) have never run against real data. This is the concrete form of "SCIP is not used to its fullest": a parallel 19KB detector, unexercised, silently drifting. *(The AST path is also mode-0 — `goparser.ParseFile(…, 0)` at `scip_indexer.go:1612`, no `go/types` — so it reads syntax, not types; its one ground-truth anchor is the getter table's proto-import-path regex. Deliberate cost/timeout trade-off, but it means AST isn't at its ceiling either.)*

**F8 — the resolver re-derives by string-match a link it has already materialized as a precise identity edge (medium; this is the durable F6 fix).**
`api_surface.go:398-411` (`linkProtoImplementations`) writes `(ProtoMethod)-[:IMPLEMENTED_BY]->(handler)` — 195 such edges live, covering 132/191 contracts. Crucially it matches on **method name only** (`last(split(fn.name,'.')) = pm.name`) + `receiverType ENDS WITH 'Server'`; it *ignores the service-name segment*, so it is **immune to the FX/Fx casing + doubled-`Service` divergence that breaks F6**. Yet `cross_service_resolver.go` never queries these edges — it independently re-derives the same call→handler link with the case-sensitive `receiverType ENDS WITH $protoService+'Server'` string match that F6 shows failing. Resolving `GRPCCall → ProtoContract{protoService} → DEFINES_METHOD → ProtoMethod → IMPLEMENTED_BY → handler` would make resolution convention-independent, structurally 1.0-confidence, and would erase F6 without loosening any matcher. This is finding F6's option 3 restated as its own defect: **the precise anchor is built, then thrown away.** (Runner-up, ships today: `toLower(receiverType) CONTAINS toLower(protoService)` — verified that `'fxservice'` *is* a substring of `'fxserviceserviceserver'`, so it alone recovers fx — but it broadens a fail-closed match and needs a mis-bind counter.)

**Patchwork or elegant — the honest split.** The *scaffolding* is real engineering, not patchwork: five clean layers, fail-closed alias arbitration ("a miss yields no edge, never a wrong edge"), an authoritative getter table anchored on proto import paths, honest confidence provenance. What caps it is that **every resolution *join* is a case-sensitive Go-codegen-name string match** — pure class-(c) name-heuristic coupling (§3), the one category that decays silently. F6 is its first live casualty; F8 is the precise fact that would end the whole category. The system is one wiring change (consume `IMPLEMENTED_BY`) away from converting its load-bearing join from name-shape to identity.

---

## 2. Summary generation

### Implementation style

`node_summary_generator.go` is disciplined: fetch one-hop effects with per-edge-type queries (explicitly to avoid cartesian products on hot nodes), compose per node (lead ← docstring-first-sentence else verb-phrase-from-name; `(Req→Resp)` clause; effects clause), write back in a single UNWIND. Deterministic, zero-LLM, idempotent, regenerates the whole service subtree each run so stale summaries can't survive a fixed bug.

The design decisions are the right ones, and — rare in indexer code — each carries its rationale in a comment:

- **One-hop effects, not transitive rollup** (`:29-34`): the card is a navigation map; the LLM walks the graph itself. Avoids cycle/order hazards and token bloat.
- **Callees in execution order** by call-site line (`:297-308`) — the card reads as a sequence ("first get, then act, then respond"), not a set.
- **Read/write overlap subtraction** (`rawEffects.resolve`) — a table both read and written reports as written only.
- **The dedup subtlety** (`:381-386`): docstring *prose* suppresses redundant effect tokens, but a *name-derived* lead must not — `FundPayout` writing `payout` keeps its "writes `payout`". This is exactly the distinction a careless implementation misses.
- Caps per category (+N more), 280-char clamp, effects-first information hierarchy.

Live samples confirm the mechanism delivers: `"Get psp id if exist, else generate (GetOrGeneratePspIDRequest→GetOrGeneratePspIDResponse) — calls …, reads config_provider_account"` is a genuinely useful triage card — the effects clause tells an agent things the identifier never could.

### Issues, in order of cost-effectiveness

**S1 — Docstring punctuation leakage (trivial fix, visible in ~every sampled service).** Leads like `":: func to construct the settlement summary screen"`, `"- fetch kyb business details"`, `": add/update the account level…"` leak Tazapay comment-style prefixes (`::`, `-`, `:`) that `summaryFromDocstring` doesn't strip. One `TrimLeft(":-• ")` + lowercase-"func to"-strip would raise perceived quality across the board. This is the single cheapest quality win in the codebase.

**S2 — Inherited F1 pollution.** 428 summaries name `proto.` as a call target. Fixing F1 and reindexing clears these.

**S3 — Structural staleness window.** Summaries are generated during each service's index run; the global CrossServiceHandlerResolver runs *after* (`apps/cli/main.go:206,511`). Effects that depend on CALLS_SERVICE edges written by the resolver are invisible to summaries until the *next* reindex of that service. Either run a summary-refresh pass after the resolver, or accept and document the lag.

**S4 — The verb table is cosmetic-decay surface.** The 130-entry `verbRules` conjugation list is the most maintenance-smelling artifact in the file — but its failure mode is benign (falls through to the naive pluralizer, historically producing "Processs"-class typos). It's cosmetic-only; don't invest here beyond bugfixes.

**S5 — 41% of summaries are lead-only** (no effects clause). Most restate the identifier — near-zero marginal information for an agent. Acceptable (they're cheap and harmless), but consider suppressing summaries that merely re-spell the name, so the presence of a summary stays a signal. Minor: `clampLen` slices bytes and can cut a multibyte rune.

**Grade: A− implementation, B+ current output** (S1 and S2 drag output below what the mechanism deserves).

---

## 3. The coupling / decay trade-off — opinion

You framed this as "coupling to today's coding pattern, decaying as engineers change." I'd reframe it first, because the reframe changes what you should do.

### What the indexer is actually coupled to

Grep the org-specific anchors and they cluster into three classes with very different decay physics:

**(a) Structural ground truth — slow decay, loud failure.** The `grpcclient/` directory contract, the `/proto/gen/go/<svc>/(grpc|http)/vN` path regex, `pgx/`+`postgres/` repo layout. These are *organizational architecture*, not style. They change rarely, deliberately, and platform-wide — and when they do, coverage falls off a cliff that telemetry can see.

**(b) Wrapper catalogs — step-function decay, single-point fixes.** `apiClientImportPath`, `queueClientPkgPath`, the external-call registry. These break exactly when the platform team refactors `grpc-framework`, and each is one constant. The real coupling of this indexer is to **one team's API surface** — and that's the strongest possible anchor short of runtime tracing, because the fleet's code is *forced* through those wrappers by review convention. This is why the indexer works at all, and it's a coupling worth keeping.

**(c) Shape/name heuristics — continuous, silent decay. This is where all the risk lives.** Field-ends-with-"client", `receiverType ENDS WITH 'Server'` + ctx+`*Request` shape, SCIP `CONTAINS` fragments, event-emission composite-literal shapes, `camelToSnake` table derivation. These erode one new-hire-house-style at a time, and nothing tells you.

### Decay is faster than you think — and that's the useful fact

Your own audits prove the timescale: the round-2 findings (P1-4 queue-URL-in-local-var, P1-5 constructor-stamped EventTypes) are *newer services* — monitoring, genai-orchestration, the current house style — deviating from patterns the detectors were tuned on **months** earlier, not years. Decay tracks *team growth and new-service creation*, not calendar time. Planning for "will this hold in 5 years" is the wrong question; the right one is "how do I notice within one index run that service #18 broke a heuristic."

### The strategic answer: don't reduce coupling — make it measurable

Tightly-coupled-and-instrumented beats loosely-coupled-and-blind. Four levers, in priority order:

1. **Coverage SLOs at index time.** You already built the instrument (IndexStats' "imported but wrote 0 nodes" alarm — the single best decay defense in the codebase). Generalize it: every resolution stage emits a recall proxy (`% GRPCCall with RESOLVES_TO`, `% OutboxCall with destService`, `% HTTPCall non-dynamic`), the report diffs against the previous run, and a drop past threshold is a loud WARN. Today's graph is the proof of need: **15% RESOLVES_TO coverage is a months-old silent regression that one printed percentage would have caught on day one.** The pipeline logs counts; nothing judges them.

2. **A golden-fixture corpus.** Every audit finding you've filed is a real code snippet a detector missed. Freeze them as test fixtures (you have the harness — 364 static tests). Then the quarterly 25-finding manual audit becomes: new style appears → add one fixture → CI guards it forever. This converts audits from an expensive event into a cheap accretion.

3. **A convention manifest.** The class-(a)/(b) anchors are roughly a dozen constants scattered across nine files. Extracting them into one declared inventory (even just a single Go file of named constants with a comment each) doesn't decouple anything — the AST shapes stay — but it makes the coupling *inventory* explicit. When the platform team announces a framework change, you review one file instead of grepping. Decay you can enumerate is decay you can price.

4. **Prefer language facts over org facts** (your P3-4 direction is correct). A binding derived from SCIP symbol resolution decays at the speed of the Go language; a binding derived from a field-naming convention decays at the speed of your newest hire. Where a class-(c) heuristic can be replaced by consuming an already-computed SCIP fact, that trade is nearly always worth it — it also collapses the AST/SCIP dual-path spelling problem (F4).

One cultural note: the codebase's fail-closed philosophy ("no edge beats a wrong edge") is right for precision, but silent drops without counters convert a precision *policy* into invisible recall *loss*. The two must ship together — every `continue`-on-miss deserves a stat. Where that rule was followed (DB detector bail points), audits went fast; where it wasn't (handler resolution), a 15% coverage collapse sat unnoticed in the live graph.

### Lifespan estimate

With levers 1+2 in place, expected upkeep is small and event-driven (a fixture per new house style, a constant per framework refactor) and the indexer's useful life is effectively open-ended — the decay rate stops mattering because detection latency drops to one index run. Without them, extrapolating from the two audits, expect a ~10-finding erosion every 6 months and a full re-audit each time the fleet gains a cohort of new services.

---

## What I deliberately did not flag

- The MERGE-idempotency of edge writers (fixed 2026-07-24) and the alias-arbitration design — both sound.
- `maxReachDepth`/`maxAsyncEventHops` caps — measured, documented, correct.
- The confidence-0.6 alphabetical tie-break in multi-candidate resolution — arbitrary but provenance-tagged, so consumers can discount it; acceptable.
- The AST/legacy `index project` path's remaining CREATE writers — known, out of fleet use.

## Verification queries (rerunnable)

```cypher
// F1 blast radius
MATCH (g:GRPCCall)-[:CALLS_SERVICE]->(s:Service) WHERE NOT (g)-[:RESOLVES_TO]->()
RETURN s.name, count(*) ORDER BY count(*) DESC;
// → proto: 422

MATCH (pc:ProtoContract)-[:BELONGS_TO]->(s:Service) RETURN s.name, count(pc);
// → proto: 191 (all of them)

MATCH (n) WHERE n.summary CONTAINS 'proto.' RETURN count(n);  // → 428

// Coverage SLO candidates
MATCH (g:GRPCCall) RETURN count(g),
  sum(CASE WHEN (g)-[:RESOLVES_TO]->() THEN 1 ELSE 0 END);    // 519 / 76
MATCH (h:HTTPCall) RETURN count(h),
  sum(CASE WHEN (h)-[:CALLS_SERVICE]->() THEN 1 ELSE 0 END);  // 201 / 0
```

---

## Action items (prioritized)

Two tracks. **Track A** raises the quality of what the MCP/graph returns *today*. **Track B** raises architecture resilience/maintainability *without changing current output* — these are decay insurance and hygiene, valuable precisely because they don't move the numbers now. Within each track, ordered by (impact ÷ effort): do the top of each first. Effort scale: **XS** = <1h/one-line · **S** = <½ day · **M** = 1–3 days · **L** = week+.

### Track A — increases output quality now

| # | Action | Source | Effort | Output impact | Notes |
|---|---|---|---|---|---|
| **A1** | **Reindex the fleet to flush the 428 `proto.`-polluted summaries** | S2 / F1 | **XS** (run reindex) | **Med–High** | The CALLS_SERVICE edges these summaries read are already correct post-F1; only a per-service reindex regenerates the cards. *Bonus:* this is also the first time `loadContractRepoIDs` runs at **detection** time (F1 was so far only validated on the resolve path) — so it doubles as F1's real-world exercise. **Start here: highest impact-per-effort, zero code.** |
| **A2** | **Fix F6 — quick path: case-insensitive + normalized receiver match** | F6 opt 1–2 / F8 | **S** | **High** (+6–8pp gRPC resolution; recovers ~51 calls incl. fx 33) | `toLower(receiverType) CONTAINS toLower(protoService)` recovers fx today (verified). Must ship **with B3's mis-bind counter** — it loosens a fail-closed match. Ship as the stopgap before A3. |
| **A3** | **Fix F6 — durable path: resolve through `IMPLEMENTED_BY` identity** | F8 / F6 opt 3 | **M** | **High** (same recall as A2, then convention-independent forever) | Join `GRPCCall → ProtoContract{protoService} → DEFINES_METHOD → ProtoMethod → IMPLEMENTED_BY → handler`. Structurally 1.0 confidence, immune to the naming class that caused F6. Supersedes A2 (keep A2's counter). This is the single change that converts the load-bearing join from name-shape to identity. |
| **A4** | **S1 — strip docstring punctuation prefixes in summary leads** | S1 | **XS** (`TrimLeft(":-• ")` + drop leading "func to") | **Low–Med** (cosmetic, but visible in ~every service) | Cheapest quality win in the codebase; raises perceived summary quality fleet-wide. Bundle into the next reindex (A1). |
| **A5** | **S3 — run a summary-refresh pass after the global resolver** | S3 | **M** | **Med** | Today summaries are generated *before* `CrossServiceHandlerResolver`, so cross-service effects lag one reindex. A post-resolver refresh (or accept+document the lag) makes summaries reflect resolved edges same-run. |
| **A6** | **Index the ~38-call cohort of unindexed services** (opsdashboard 23, sandbox 5, cpnofi 4, …) | F6 residual | **S–M** (operational, per service) | **Med** (completeness) | Not a code change — these calls can never resolve until the target repos are indexed. Do only for services that matter to blast-radius queries. After A3, an unresolved call means *exactly* "target not indexed," so this list becomes self-maintaining. |

### Track B — increases architecture quality, no change to current output

| # | Action | Source | Effort | Why (impact is on resilience, not today's numbers) |
|---|---|---|---|---|
| **B1** | **Index-time coverage SLOs** — every resolution stage emits a recall proxy (`% GRPCCall RESOLVES_TO`, `% OutboxCall destService`, `% HTTPCall non-dynamic`), diffed vs. previous run, loud WARN past threshold | §3 lever 1 / F6 opt 4 | **M** | **Highest strategic value.** The instrument already exists (IndexStats "imported but wrote 0"); generalize it. A single printed percentage would have caught F1's 15% collapse *and* F6 on day one. Converts silent recall loss into a loud signal. Doesn't change output — it makes future output regressions visible. **Start Track B here.** |
| **B2** | **Golden-fixture corpus** — freeze every past audit finding as a test fixture in the 414-test harness | §3 lever 2 | **M** | Turns the quarterly manual re-audit into cheap CI accretion: new house style → one fixture → guarded forever. The highest-leverage defense against month-scale decay. |
| **B3** | **Mis-bind counter for any loosened matcher** | §3 cultural note / F6 opt 1 | **XS–S** | Prerequisite for A2. Every `continue`-on-miss and every broadened match needs a stat, or a precision *policy* silently becomes recall *loss*. Small, but gates A2 safely. |
| **B4** | **F3 — kill the silent `LIMIT 2000` truncation in the SCIP detector** (count + WARN when hit) | F3 | **XS–S** | Cheap loudness on a currently-invisible cliff for large services. |
| **B5** | **F7 — decide the dead-on-Go SCIP RPC detector's fate**: either feature-guard/remove it, or add a TS/Python fixture that actually exercises it | F7 | **S–M** | 19KB of parallel detector never runs on the all-Go fleet and silently drifts. Either make it live (a fixture) or make its dormancy explicit. Hygiene + removes an F1-class latent hazard. |
| **B6** | **F4 — converge the AST/SCIP dual-path `targetService` spelling** (prefer the SCIP/contract fact; drop the weakest arm) | F4 / §3 lever 4 | **M** | "Two spellings reconciled by a fuzzy index" is the standing invitation for the next F1. Also retires the anchorless `field-ends-with-"client"` AST arm. |
| **B7** | **F5 — assert stage prerequisites** (fail loudly if a stage consumes properties an earlier stage hasn't stamped) | F5 | **S–M** | Today the pipeline order is comment-enforced; a future reorder fails *silently* with empty closures. Cheap guardrail. |
| **B8** | **Convention manifest** — hoist the ~dozen class-(a)/(b) anchor constants into one declared inventory | §3 lever 3 | **S–M** | Doesn't decouple anything; makes the coupling *enumerable* so a platform-team framework change is a one-file review instead of a fleet grep. |
| — | **S4 verb table — explicitly do NOT invest** beyond bugfixes | S4 | — | Benign failure mode (naive pluralizer). Listed to close the loop, not to action. |

### Suggested execution order (interleaved)

1. **A1** (reindex — flush summaries, exercise F1 at detection) — free, immediate.
2. **B3 → A2** (counter, then the case-insensitive stopgap) — recover the ~51 stranded calls this week.
3. **B1** (coverage SLOs) — so every step after this is measured, and F6-class decay can't hide again.
4. **A3** (IMPLEMENTED_BY identity resolution) — the durable F6 fix; retire A2's heuristic.
5. **A4** (S1 punctuation) — fold into the reindex you'll run to realize A3.
6. **B2** (golden fixtures), then **B4–B8** as capacity allows; **A5/A6** when summary-freshness or those specific services become relevant.

**Guiding rule (from §3):** never ship a Track-A recall win without its Track-B counter (B1/B3). A precision policy without instrumentation becomes invisible recall loss — which is exactly how F1 sat unnoticed at 15% for months.
