# Indexer Gap Analysis — 18th July (Round 2, audited 2026-07-19)

Second audit round, run after all round-1 P0/P1/P2 and P3-1..P3-3 fixes were
implemented (branch `FEAT/cross-service-rpc`, HEAD `b4a3b32`). Every service repo
was pulled to latest `main` (repos on feature branches — event, settlement,
payment-orchestration, cdc, sandbox, proto, TazapayDocsV2 — audited as-is per
instruction). Finding IDs continue the round-1 numbering.

## Context you must load before starting

- Detector code: `libs/indexer-go/static/`. Read the file you are changing
  **and its `_test.go`** first.
- Round-1 report: `Indexer gap analysis - 18th July.md` (same directory).
  Detailed round-1 findings + statuses:
  `~/Workspace/Tazapay/.knowledge/analysis/codegraph-indexer-gap-analysis-2026-07-18.yaml`.
- Same ground rules as round 1: one task = one commit, never regress a test,
  prefer type/import-path facts over name heuristics, **silent drops are the
  enemy** — emit `dynamic`/`unresolved` markers instead of skipping.

## What was re-verified and HOLDS (no action)

- All 17 settlement-orchestration PSP connectors (`client/api/{scb,jpmorgan,yesbank,…}`),
  monitoring's sardine + complyadvantage, and genai-orchestration's llm/tavily/
  googlesearch/parallel clients all send via `grpc-framework/client/api` → covered
  by the P0-1 `APIClientCallDetector` (package-verb, local-var, param, chained,
  and same-file-helper shapes all appear; `jpmorgan.newMTLSClient` and
  `yesbank.getYBLClientWithTransport` resolve via the same-file/param paths).
- `scip_rpc_detector.queryHTTPRefs` includes `grpc-framework/client/api` (P0-1 SCIP side ✓).
- ops-dashboard **and cdc** mongo repos use
  `client.Database(env.GetX()).Collection(env.Get<X>Collection())` → P2-6 resolves them.
- No `pgx/`/`postgres/` subdirectories anywhere; no aliased `repository` package
  imports; no chained `grpcclient.GetXService(ctx).Method(…)` call sites; no
  struct fields typed `*api.Client`; pgxscan usage is only `Get`/`Select`/`NotFound`.
- All 21 SQS-consuming services register listeners via `server.SQSListener{QueueURL:…, Route:…}` —
  the composite-literal detection shape is universal.
- `/testdata/` **is** excluded from indexing (`isGeneratedFilePath`) — earlier
  suspicion retracted; see P3-5 for the one place it leaks in.
- Env consts hold canonical queue names (`EventQueue = "queue.event.event"`), so
  P1-4 below is fixable without touching QueueToService.
- grpcclient getter files in all newer services (monitoring, treasury, cdc, report,
  liquidity-/genai-orchestration) follow the proto-return-type convention → P0-5
  getter table binds them.

---

## P0 — Critical

### P0-6: Struct-field `PgxRepo` receivers — ~136 DB call sites invisible

- **Problem:** `detectTazapayRepoCall` (`db_call_detector.go`) resolves the repo
  chain root only when it is an `*ast.Ident` bound in `varDBType` or a
  `repository.Pgx()` call. Worker/wrapper structs that carry the repo as a
  **field** produce `sw.Repo.SettlementAttempt.UpdateByID(…)` — the chain root is
  `sw.Repo` (a `SelectorExpr`), which falls through the switch. Every such DB
  call is silently dropped (it also increments no P3-1 counter, because the
  repo-pattern path bails before `statCandidatesSeen`).
- **Scale:** 117 `X.Repo.<Repo>.<Method>` + 19 `x.repo.<Repo>.<Method>` call
  sites ≈ 136 across settlement, onboarding, account, bfi, pricing,
  payment-orchestration.
- **Evidence:**
  - `settlement/service/grpc/v1/settlement_action.go:36` (`Repo *repository.PgxRepo`
    struct field) with 10 call sites, e.g. `:710` `sw.Repo.SettlementAttempt.UpdateByID(ctx, …)`.
  - `settlement/service/http/v3/payout_action.go:47` — 32 sites.
  - `onboarding/service/http/v3/update_kyb.go` — 33 sites.
  - `account/service/http/v3/update_entity.go:548` — lowercase field:
    `e.repo.Entity.UpdateByID(ctx, updatedEntity)`.
- **Fix:** add a per-file pre-pass (BeginFile, mirroring the apiclient detector)
  that scans struct type declarations for fields typed `*<pkg>.PgxRepo` /
  `<pkg>.PgxRepo` (reuse `isPgxRepoType`) and records `structType.fieldName`.
  In `detectTazapayRepoCall`, accept an inner `SelectorExpr` whose `.Sel` is a
  known PgxRepo field name (match on field name set collected file-wide; the
  wrapper structs are declared in the same file as their methods in every cited
  case). Do NOT match the bare name "Repo"/"repo" without the type fact.
- **Accept:** unit test for the `sw.Repo.<R>.<M>` shape; re-index settlement and
  onboarding — DBCall counts rise materially (settlement baseline first).

### P0-7: serviceIndex segment aliases re-introduce cross-service mis-binding

- **Problem:** `service_index.go` → `serviceAliases` splits every service
  name/package on `/.-_` and registers **each segment** as an alias:
  `settlement-orchestration` also claims `settlement`; `payout-router` claims
  `payout` and `router`; `payment-router` also claims `router`;
  ops-/mp-/merchant-dashboard all claim `dashboard`; the four `*-orchestration`
  services all claim `orchestration`. `byName[alias] = id` overwrites blindly,
  so whichever Service row loads **last** owns the contested alias —
  nondeterministic across runs. `resolveByName("settlement")` can bind
  settlement-orchestration; `resolveFromURL` host-segment candidates hit the
  same map. This is the P0-5 disease reintroduced one layer down, and it defeats
  the exact-match fallback because the ambiguity is inside the index itself.
- **Evidence:** `libs/indexer-go/static/service_index.go:158-189` (serviceAliases),
  `:44-54` (blind overwrite). Live workspace has settlement + settlement-orchestration,
  payment-router + payout-router, 3 dashboards, 4 orchestrations.
- **Fix:** during load, track alias → set of distinct service ids. An alias
  claimed by more than one service is **contested**: delete it from both maps
  (keep only full-name and canonical-key aliases, which are unique by
  construction). Never let a segment alias overwrite an existing full-name
  alias. Log contested aliases once per load (P3-1 style WARN).
- **Accept:** unit test: load settlement + settlement-orchestration in either
  order; `resolveByName("settlement")` always returns settlement;
  `resolveByName("orchestration")` returns "".

---

## P1 — High: event graph correctness

### P1-4: Queue URL assigned to a local var → destination service lost

- **Problem:** `event_emission_resolver.go` → `resolveQueueArg` resolves the
  send's URL arg only as an inline expression (unwrapping `env.Get(…)` /
  consts). The fleet-standard shape in **newer services** is
  `queueURL := env.Get(svcenv.EventQueue)` on a previous line, then
  `queue.SendSQSMsg(ctx, queueURL, qMsg)`. The Ident resolves to nothing →
  `destQueue=""` → `destService=""` → the OutboxCall has no destination and the
  fan-out/ROUTED_TO chain never forms. The emission itself IS written (event
  name resolves), so the break is quiet — nodes exist but are disconnected.
- **Evidence:** `monitoring/utils/event.go:27-29` and `:67-69`;
  `monitoring/service/http/v3/sardine_webhook.go:463-465`, `:1386-1388`;
  `monitoring/utils/helper.go:111-117`; `monitoring/service/sqs/helper.go:73`;
  `payment-orchestration/service/sqs/custody_helper.go:583-585`;
  `genai-orchestration/utils/slack.go:47`;
  `liquidity-orchestration/service/startup.go:28`;
  `liquidity-orchestration/service/http/v3/liquidate.go:107`;
  `settlement-orchestration/tools/queue/settlement.go:229`.
- **Fix:** in `passAClassifyEmitters` / `passBAttributeEmissions`, before
  resolving sends, collect per-function `<var> := <expr>` string bindings whose
  RHS resolves statically via the const resolver after unwrapping `env.Get`
  (exactly the `localStrings` pass the apiclient detector already runs). Pass
  the binding map into `resolveQueueArg` (use `ResolveStringWithBindings`).
  A URL that is a function **parameter** stays unresolved — record
  `destQueue:"dynamic"` rather than `""` so telemetry can count it.
- **Accept:** re-index monitoring — `monitoring/utils/event.go` emissions carry
  `destService:"event"`; fan-out edges appear.

### P1-5: EventType stamped inside a message-constructor helper → silent drop

- **Problem:** the create-then-restamp tracing (P1-2) only sees
  `msg.EventType = …` assignments **in the sending function**. The shape
  `qMsg = constructTMSQMsg(qMsg, resp)` followed by `queue.SendSQSMsg(ctx, url, qMsg)`
  — where the **callee** builds/stamps the message and returns it — resolves
  nothing: `asyncEventTypeExpr` finds no composite literal, `restampEventExprs`
  finds no local restamp, and pass B `continue`s. The send vanishes entirely
  (violates the "never silently skip" rule — not even a `group.*` hub or
  dynamic OutboxCall is written).
- **Evidence:** `monitoring/utils/helper.go:110` → `:133-165`
  (`constructTMSQMsg` stamps 4 distinct events: `PayoutTMSRuleDetails`,
  `PayoutScreeningSucceeded`, `CollectTMSRuleDetails`, `CollectComplianceSucceeded`);
  `monitoring/client/api/sardine/initiate.go:674` (`constructSardineQMsg`).
  Same shape in the event service (`event/service/sqs/settlement.go:217`,
  `refund.go:286`) — those specific sites ride the fan-out/route-handler
  machinery, but verify they still attribute after any fix.
- **Fix:** add a "message constructor" classification to the resolver pre-pass:
  a same-repo function whose return type includes `*queue.AsyncMessage` and
  which stamps `EventType` (composite-literal field or field assignment on its
  local/param message var). When pass B finds the send's message var (re)assigned
  from a call to a known constructor, attribute the constructor's collected
  EventType expressions (each resolved with the normal static/partial/hub
  rules). Minimum acceptable fallback: write an OutboxCall with
  `eventType:"dynamic"` so the send is never invisible.
- **Accept:** re-index monitoring — the TMS send yields OutboxCalls for the 4
  events (or documented hubs); no send site in monitoring/utils remains
  node-less.

### P1-6: `*api.Client` factory helpers in another package/file → HTTP egress missed

- **Problem:** `apiclient_call_detector.go` tracks client-returning helper
  functions **per file** (`fileClientFuncs`, reset in BeginFile). A client
  built by a helper living in a different file/package —
  `cpnClient, _ := tools.NewCpnClient(ctx)` — is unbound, so subsequent
  `cpnClient.Post(…)` writes no HTTPCall. This currently hides the **CPN
  merchant-webhook delivery path**.
- **Evidence:** `bfi/tools/cpn.go:19` (`func NewCpnClient(ctx) (*apiclient.Client, error)`)
  consumed at `bfi/service/http/v3/support_ticket.go:163` → `cpnClient.Post(ctx,
  supportTicketURL, …)` at `:174`; `notification/tools/cpn.go:17` consumed at
  `notification/utils/helper.go:637` → `client.Post(ctx, outbound.WebhookURL, …)`
  (~`:670`, TriggerCPNWebhookAPI — the CPN webhook egress).
- **Fix:** repo-wide pre-pass (once per service index run, like the
  EventEmissionResolver's emitter pass): collect bare names of all non-test
  functions whose results include `*api.Client`/`api.Client` (resolve the
  selector via each file's import map — same `isAPIClientType` check). Consult
  that set in `isFileClientFuncCall` after the per-file fast path. Bare-name
  matching is safe here: the fleet has exactly 4 such helpers, all uniquely
  named (`NewCpnClient` ×2 in different services, `getYBLClientWithTransport`,
  `newMTLSClient`).
- **Accept:** re-index bfi + notification — HTTPCall nodes exist for
  support_ticket.go:174 and utils/helper.go:~670 (`url:"dynamic"` for the
  webhook URL is fine).

---

## P2 — Medium: coverage

### P2-7: Kinesis operations (cdc's entire transport) unregistered

- **Problem:** `grpc-framework/client/queue` also exports Kinesis ops —
  `ListShards`, `FetchShardIterator`, `GetRecords`, `PutRecords`
  (`queue/kinesis.go:61-135`). No detector/registry covers them, and the cdc
  service's async topology is Kinesis-based: it consumes CDC streams and
  re-emits records. cdc currently shows near-zero egress/ingress in the graph.
- **Evidence:** `cdc/service/http/v3/cdc_event_retrigger.go` (production
  PutRecords/GetRecords retrigger path); consumption wiring under `cdc/handler/`.
- **Fix:** add `externalCallRegistry` entries keyed on the queue import path for
  the four Kinesis funcs (`provider:"aws"`, `service:"kinesis"`,
  `category:"messaging"`). Do NOT add SetKinesis/InitKinesis (allowlist
  guardrail). This deliberately does not model per-stream routing — that needs
  a design note (streams are ARNs from env, not `queue.<svc>.<name>` strings).
- **Accept:** re-index cdc — ExternalService `aws:kinesis` with USES_SERVICE
  edges from the retrigger RPC.

### P2-8: Redis pipeline and distributed-mutex operations invisible

- **Problem:** `cache_call_detector.go` matches only package-level
  `cache.<Op>(…)`. Two real shapes bypass it: (a)
  `pipe, _ := cache.NewPipeline(ctx)` then `pipe.Set/Get/Expire(…)` — pipeline
  ops on the returned var; (b) `mutex := cache.GetRedisMutex(key, ttl)` then
  `mutex.Lock()/Unlock()` — distributed locks.
- **Evidence:** `liquidity-orchestration/client/fix/wintermute/redis_store.go:184,210,291`;
  `monitoring/client/api/sardine/initiate.go:501,550`;
  `monitoring/utils/helper.go:34,68`.
- **Fix:** track vars assigned from `cache.NewPipeline` (import-path matched)
  and record registry ops invoked on them (same op names + `variant:"pipeline"`);
  track `cache.GetRedisMutex` vars and record `Lock`/`Unlock` as
  LOCK/UNLOCK CacheCalls. Keep NewPipeline/GetRedisMutex themselves out of the
  registry (constructors — guardrail).
- **Accept:** unit tests for both shapes; re-index monitoring +
  liquidity-orchestration → new CacheCall nodes.

### P2-9: FIX-protocol egress (liquidity-orchestration → Wintermute) has no representation

- **Problem:** liquidity-orchestration trades via FIX using
  `quickfixgo/quickfix` (`go.mod:14`; 11 non-test files under
  `client/fix/wintermute/` — session, orders, positions). No node class covers
  it: not HTTP, not gRPC, not SQS. The service's core business egress
  (order placement to the Wintermute market maker) is invisible.
- **Fix (scoped):** registry-style entries for the quickfix send surface
  (`quickfix.Send`, `quickfix.SendToTarget`) → ExternalService
  `{provider:"wintermute", service:"fix", category:"trading"}`. Grep the
  wintermute package for the actual send calls first and enumerate only those.
  A fuller FIX model (sessions, message types) needs a design note — do not
  build it speculatively.
- **Accept:** re-index liquidity-orchestration → USES_SERVICE edges from the
  order/session functions to the wintermute ExternalService node.

---

## P3 — Hardening

### P3-5: const scanner does not skip `testdata/`

- **Problem:** `const_resolver.go` → `skipForConstScan` excludes `_test.go`,
  `.pb.go`, `vendor/`, `mocks/` — but not `testdata/` (the reference indexer
  excludes it via `isGeneratedFilePath`; the const scanner has its own filter).
  36 testdata dirs contain AsyncMessage/event fixtures whose string consts can
  hijack first-wins resolution or trigger false P3-2 ambiguity WARNs against
  production constants.
- **Fix:** add `/testdata/` (and `testdata/` prefix) to `skipForConstScan`.
  One line + test.

### Minor notes (no task)

- `grpc-micro/test/abc.go:20` uses `repository.Postgres().BeginTx(…)` — a
  `Postgres()` accessor exists in the template repo's test dir only; no
  production service uses it. Revisit only if a service adopts it.
- `monitoring/utils/helper.go:117` and `monitoring/service/sqs/helper.go:73`
  send with a queue URL that is a function **parameter** — after P1-4, these
  still resolve to `dynamic`; the P3-1 telemetry should count them
  (sends-with-unresolved-dest) so growth of that shape is visible.

---

## Verification checklist (after P0/P1 tasks)

1. `make test` green; build + vet clean.
2. Re-index settlement, onboarding, account, monitoring, bfi, notification, cdc,
   liquidity-orchestration.
3. Neo4j spot checks:
   - `MATCH (d:DBCall {serviceName:'settlement'}) RETURN count(d)` — rises vs
     round-1 baseline (record it first).
   - `MATCH (d:DBCall) WHERE d.filePath CONTAINS 'settlement_action' RETURN count(d)` — ≥10.
   - `MATCH (o:OutboxCall {callerService:'monitoring'}) WHERE o.destService = 'event' RETURN count(o)` — nonzero.
   - `MATCH (h:HTTPCall {callerService:'notification'}) WHERE h.filePath CONTAINS 'utils/helper' RETURN h` — exists.
   - `MATCH (g:GRPCCall)-[:CALLS_SERVICE]->(s:Service) WHERE g.targetService='settlement' AND s.name='settlement-orchestration' RETURN count(g)` — 0.
4. Update `~/Workspace/Tazapay/.knowledge/analysis/codegraph-indexer-gap-analysis-2026-07-19.yaml`
   with `fixed:` statuses as tasks land.
