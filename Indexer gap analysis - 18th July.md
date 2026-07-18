# Indexer Gap Analysis — 18th July

Action plan for a worker LLM. Derived from the audit in
`~/Workspace/Tazapay/.knowledge/analysis/codegraph-indexer-gap-analysis-2026-07-18.yaml`
(25 findings, verified against every Tazapay service repo on 2026-07-18).

## Context you must load before starting

- All detector code lives in `libs/indexer-go/static/`. Read the file you are changing
  **and its `_test.go`** before editing.
- Every task below has file:line evidence from the real workspace at
  `~/Workspace/Tazapay/<service>/`. Verify the evidence still holds before fixing —
  services move fast.
- After each task: `make test`, and re-index one affected service
  (`make build && ./bin/codegraph index scip ~/Workspace/Tazapay/<svc> --service=<svc>`)
  to confirm node counts move in the expected direction.
- Do not weaken the existing allowlist guardrails (comments marked "DO NOT add" in
  `cache_call_detector.go` and `external_call_registry.go`). Extend via the documented
  extension points only.

## Ground rules

1. One task = one commit. Reference the task ID (e.g. `P0-1`) in the commit message.
2. Never regress an existing test. If a fix changes expected output, update the test
   with a comment explaining why.
3. Prefer making bindings **type-driven** over adding more name heuristics. The root
   cause of most gaps is that detectors re-derive type information from naming
   conventions instead of using type facts (function parameter types, SCIP symbols).
4. Silent drops are the enemy. Where a pattern is recognized but unresolvable, emit the
   node with `dynamic`/`unresolved` markers rather than skipping — mirroring how
   `event_emission_resolver.go` already handles `group.*` hubs.

---

## P0 — Critical: whole relationship classes are wrong or empty

### P0-1: Detect outbound HTTP via `grpc-framework/client/api` (the fleet standard)

- **Problem:** 406 files import `github.com/tazapay/grpc-framework/client/api` as
  `apiclient` (+30 unaliased, +6 as `httpclient`). All third-party/PSP egress HTTP goes
  through it. Neither the AST detector (`rpc_call_detector.go` →
  `processHTTPCallExpr`) nor the SCIP query (`scip_rpc_detector.go` → `queryHTTPRefs`)
  matches it. HTTPCall coverage is near zero.
- **Evidence:** `payin/utils/slack_invoice.go:142` —
  `apiclient.Post(ctx, webhookURL, apiclient.JSON(message), ...)`.
- **Fix:**
  - AST path: match by **import path** (build/import-map like `cache_call_detector.go`
    does — do NOT match the alias token). Wrapper surface (from
    `grpc-framework/client/api/do.go` / `client.go`): `Get`, `Post`, `Put`, `Delete`,
    `Patch`, `Do`, `Form`, `JSON`, `Multipart`, `Bytes`. URL is arg index 1
    (`fn(ctx, url, ...)`); resolve it with `extractStringArg` plus the const resolver
    for `constants.X`/`env.Get(...)` shapes; fall back to `"dynamic"`.
  - SCIP path: add `sym.symbol CONTAINS 'grpc-framework/client/api'` with the method
    names above to `queryHTTPRefs`.
- **Accept:** re-indexing payin produces HTTPCall nodes for `apiclient.*` sites;
  existing `http.*` tests still pass. Note: no service go.mod contains resty — leave
  resty code, but do not invest in it.

### P0-2: Bind `*repository.PgxRepo` function parameters in DBCallDetector

- **Problem:** `db_call_detector.go` recognizes `repo.<Repo>.<Method>()` only when
  `repo` was bound via `repository.Pgx().BeginTx(...)` **in the same function**.
  263 non-test functions across ≥8 services take `repo *repository.PgxRepo` as a
  **parameter** — all their DB calls produce no DBCall node.
- **Evidence:** `settlement/utils/settlement.go:66`, `settlement/utils/outbox.go:19`,
  `settlement/service/grpc/v1/delete_adjustment.go:158`.
- **Fix:** in `DetectInFunction`, before pass 1, walk `funcDecl.Type.Params` and bind
  any parameter whose type is `*<pkg>.PgxRepo` (or `<pkg>.PgxRepo`) as
  `varDBType[name] = "tazapay-tx"`. Match on the type name `PgxRepo`, not the package
  alias.
- **Accept:** unit test: function with `repo *repository.PgxRepo` param and
  `repo.Settlement.GetByID(ctx, id)` body yields a DBCall. Re-index settlement:
  DBCall count rises materially.

### P0-3: Bind plain `repo := repository.Pgx()` assignments

- **Problem:** `processAssignment` only handles the `.BeginTx` shape. A bare
  `repo := repository.Pgx()` binds nothing, so subsequent `repo.<Repo>.<Method>` is
  missed.
- **Evidence:** `settlement/service/scheduler/scheduler.go:38`,
  `notification/service/scheduler/scheduler.go:38`,
  `payment-orchestration/service/scheduler/scheduler.go:42`,
  `settlement-orchestration/utils/helper.go:78`,
  `settlement/service/http/v3/beneficiary.go:2947`.
- **Fix:** in `processAssignment`, when RHS is a call and `isRepositoryPgxCall(rhs)`
  is true (no `.BeginTx`), bind LHS as `"tazapay-tx"`. Reuse the existing helper.
- **Accept:** unit test for the scheduler shape passes; no false binding for
  unrelated `X()` calls.

### P0-4: Fix repository table resolution — scan `postgres/` and fix `camelToSnake`

- **Problem (a):** `repository_sql_scanner.go` → `ScanRepositorySQL` reads only
  `<svc>/pgx/`. Seven services keep implementations in `postgres/`: ruleengine (14
  files), refund (7), metadata (5), payment-orchestration (4), onboarding,
  settlement-orchestration, grpc-micro.
- **Problem (b):** `camelToSnake` in `db_call_detector.go` breaks acronyms:
  `ForterAPITrace` → `forter_a_p_i_trace`; real table is `forter_api_trace`
  (verified `ruleengine/postgres/forter_api_trace.go:32`). Affects 30+ repo
  interfaces (`DLQEvent`, `ConfigMFASMS`, `OutboxBFI`, `GPI`, `LookupVASP`,
  `APIAccessControl`, `MISData`, ...).
- **Fix:**
  - (a) Scan both `pgx/` and `postgres/` (same file format, same receiver
    convention). Keep the per-directory relPath in `RepoMethodInfo.FilePath`.
  - (b) Rewrite `camelToSnake` to treat consecutive capitals as one token:
    `ForterAPITrace` → `forter_api_trace`, `DLQEvent` → `dlq_event`,
    `ConfigMFASMS` → `config_mfasms` (acceptable — SQL-scan will now usually win
    anyway after (a)). Standard rule: insert `_` before an uppercase letter only if
    the previous char is lowercase/digit, or the next char is lowercase.
- **Accept:** table for `repository.Pgx().ForterAPITrace.*` resolves to
  `forter_api_trace` via SQL scan; camelToSnake unit tests cover the acronym cases.

### P0-5: Replace fuzzy CALLS_SERVICE resolution with an explicit getter→service table

- **Problem:** target-service derivation from `grpcclient.Get<X>Service` names
  produces tokens (`solremittance`, `polhttp`, `cpn`, `rbac`, `kyb`, `collect`,
  `entity`) that either match no Service node (edge silently dropped) or, via the
  both-ways `CONTAINS ... LIMIT 1` Cypher in `resolveTargetService`, bind an
  **arbitrary wrong** service (`GetDashboardService` → ops-/mp-/merchant-dashboard;
  `GetPayoutService` → payout-router regardless of owner).
- **Fix:**
  1. Authoritative source: each service's `grpcclient/` package declares which proto
     client every getter wraps. Build a pre-pass that parses `<svc>/grpcclient/*.go`
     (excluding `grpc_ut.go`, see P2-3) and maps getter name → proto import path →
     owning service (proto path convention `proto/<svc>/grpc/v1`,
     `proto/<svc>/http/v3` per the workspace CLAUDE.md).
  2. Where the table has no answer, keep name-based fallback but **require exact or
     prefix match** — delete the bidirectional `CONTAINS` clause; never bind on
     substring-both-ways. When unresolved, still write the GRPCCall node with
     `targetService` set from the best guess and no CALLS_SERVICE edge.
- **Also cover (cheap, same file):** getters missed by the `Get*`+`*Service` shape:
  `GetSettlementServiceServer`, `GetRuleEngineServiceClient`,
  `AccountHolderNameService`. Accept suffixes `Service|ServiceServer|ServiceClient`
  and make the `Get` prefix optional when the name ends in `Service`.
- **Accept:** re-index two callers of `GetKYBService`/`GetPOLHTTPService`; edges
  point at onboarding/payment-orchestration respectively (verify real owners in
  `proto/` first); no edge binds a dashboard service from `GetDashboardService`
  unless exact.

---

## P1 — High: event graph correctness

### P1-1: Accept aliased `SendSQSMsg` receivers

- **Problem:** `event_emission_resolver.go` → `allAsyncSends` requires the receiver
  ident to be literally `queue`. `settlement/service/grpc/v1/create_adjustment.go:534`
  sends via `sqsqueue.SendSQSMsg` — missed. 122 files import the queue package as
  `sqsclient`; any aliased send disappears silently.
- **Fix:** resolve by import path. `parseFiles` already has the `*ast.File`; build a
  per-file import map (alias → path) and accept any receiver whose alias maps to
  `github.com/tazapay/grpc-framework/client/queue`. Same change in
  `EventCallDetector` where relevant.
- **Accept:** the create_adjustment.go site produces an OutboxCall on re-index.

### P1-2: Trace `qMsg.EventType = ...` field assignments for direct producers

- **Problem:** `asyncEventTypeExpr` only traces EventType inside a composite literal
  (inline or `msg := &queue.AsyncMessage{...}`). The
  create-then-restamp shape (`qMsg.EventType = constants.X + CharDot + Y` followed by
  `queue.SendSQSMsg(ctx, url, qMsg)`) is only handled at *relay* call sites
  (`staticEventLiterals` in `event_fanout_resolver.go`), not in
  `passBAttributeEmissions`.
- **Evidence:** `event/service/sqs/collect.go:774`, `event/service/sqs/payout.go:1835`,
  `event/service/sqs/settlement.go:337` (dynamic: `constants.Settlement + CharDot + status`).
- **Fix:** in `passBAttributeEmissions`, when `asyncEventTypeExpr` returns nil for a
  local message var, reuse `staticEventLiterals(fn, msgArg)` to pick up field
  re-assignments; for partially-static values (`Settlement + CharDot + <var>`) run the
  existing switch-enumeration path (`collectLocalEventAssigns` context machinery) or
  degrade to the `group.*` hub — never silently skip.
- **Accept:** re-index event service; the three cited sites emit OutboxCall +
  EventType (or `group.*` hub for the dynamic one).

### P1-3: Enumerate switch tags that are call expressions

- **Problem:** `mapAssignSwitchContext` / `mapCallSwitchLabels` require an
  `*ast.Ident` tag. `switch v.GetLabel()`, `switch payout.payoutEventData.GetTransferType()`
  in `event/service/sqs/` lose enumeration → `group.*` fallback hubs.
- **Fix:** low-cost version — also accept `switch <ident> := <call>; <ident>` and
  selector/call tags by treating the tag as an opaque key: enumeration doesn't need
  the tag's *name*, only the case labels. Bind case-label constants exactly as today.
- **Accept:** event-service switch sites with call tags yield concrete
  `group.action` emissions per case.

---

## P2 — Medium: coverage and noise

### P2-1: Extend the external-call registry to wrappers actually in use

- Add to `external_call_registry.go` (import path → func → op), after reading each
  wrapper's exported surface in `grpc-framework/client/`:
  `featureflag` (13 consumer files), `kms` (2), `cloudfront` (2), `webauthn` (3),
  `sms` (1, SNS), `recaptcha` (1), `chrome` (1), `session` (1).
  Follow the existing allowlist discipline: per-operation entries only, constructors
  and init helpers excluded with a "DO NOT add" comment.

### P2-2: Detect direct AWS SDK usage (account service)

- **Problem:** account calls AWS SDKs directly in production code:
  `account/utils/mfa.go`, `account/utils/document.go`,
  `account/service/grpc/v1/signin.go`, `mfa_authentication.go`,
  `document_helper.go` (cognito/S3/SNS/CloudFront). The import-path allowlist only
  knows grpc-framework wrappers.
- **Fix:** add registry entries keyed by the `aws-sdk-go-v2/service/<x>` import paths
  for the operations actually used in those files (enumerate them first; do not
  bulk-import the SDK surface).

### P2-3: Exclude `grpc_ut.go` unit-test scaffolding

- **Problem:** `grpcclient/grpc_ut.go` files (not `_test.go`, not `mocks/`) are
  indexed as real code — 5,120 `grpcclient.GetUTObject(` references, plus
  ServiceClient-typed structs whose fields (`Account`, `Fx`, `RuleEngine`, ...) feed
  noise into heuristics.
- **Fix:** add `grpc_ut.go` (basename match) to `isGeneratedFilePath` in
  `scip_indexer.go` with a comment naming the convention. First verify the one
  known non-test usage (`settlement/service/http/v3/beneficiary.go` calls
  `grpcclient.GetUTObject`) to ensure exclusion doesn't orphan a real edge —
  if it's genuinely production code, keep file-level indexing and instead skip only
  UT-prefixed symbols.

### P2-4: CTE-aware and Sprintf-aware SQL table extraction

- **Problem (a):** `tableFromSQL` takes the first `FROM|INTO|UPDATE|JOIN` match, so
  `WITH x AS (SELECT ... FROM inner) UPDATE real ...` resolves to the CTE's inner
  table. 10+ pgx files affected (`balance/pgx/balance.go`,
  `notification/pgx/outbound_webhook.go`, `payment-router/pgx/collection_account_*.go`).
- **Problem (b):** 27 pgx files build SQL with `fmt.Sprintf`; the current extractor
  returns empty SQL.
- **Fix:** (a) if the SQL starts with `WITH`, strip CTE definitions (track paren
  depth up to the matching close before the main statement) before applying the
  regex; also skip captured names that match a CTE alias. (b) in
  `extractStringArgWithVars` and `extractSQLInfoFromFunc`, when the expr/assignment
  RHS is `fmt.Sprintf(<lit>, ...)`, use the format-string literal.
- **Accept:** table for `payment-router/pgx/collection_account.go:1281`
  (`fmt.Sprintf("UPDATE collection_account SET %s ...")`) resolves to
  `collection_account`; CTE files resolve to the outer statement's table.

### P2-5: Disambiguate DBCall nodeKeys

- **Problem:** `dbcall:<scope>:<file>:<svc>:<line>` merges two DB calls on one
  source line into a single node.
- **Fix:** append repo+method (or operation) to the key in both
  `processCallExpr` and `writeTazapayRepoCall`. Keep the old key format out of
  MERGE collisions by bumping any schema/version constant if one exists.

### P2-6: Mongo collection resolution for ops-dashboard

- **Problem:** ops-dashboard is MongoDB-backed; detection rides the `col`
  field-name heuristic, `table` is always empty, and `isDBFieldName` false-positives
  on non-DB stores.
- **Fix (scoped):** when a mongo method matches, resolve the collection by tracing
  the struct's `col` field initialization (`db.Collection("name")` literal) in the
  same package — best-effort, else leave empty but set `dbKind: "mongo"`. Tighten
  `isDBFieldName`: require exact tokens (`db`, `col`, `repo`, `store`, `pool`,
  `conn`) split on camelCase boundaries rather than substring `Contains`.

---

## P3 — Hardening / observability

### P3-1: Coverage telemetry per index run

Emit counters at the end of each service index: functions scanned, DBCalls written,
DB-pattern candidates *rejected* (and why: untracked receiver, unknown method),
sends seen vs emissions attributed, HTTP wrapper calls seen vs written. A gap like
P0-1 (an entire wrapper invisible) should be visible as "0 HTTP calls in a service
with 40 `apiclient` imports" — make that an explicit WARN.

### P3-2: constResolver collision guard

Bare-name, first-wins resolution across the repo is a latent correctness risk for
generic const names (`Failed`, `Created`). On collection, when the same bare name
is seen with a **different** resolved string value, log a WARN with both file paths
and mark the name ambiguous (resolve → not static) instead of silently picking one.

### P3-3: QueueToService input validation

Reject values that don't match `^(queue|dlq)\.[a-z0-9-]+\.` (e.g. hard-coded
`https://sqs...` URLs, env-key strings like `queue.event_url`) → return `""`
rather than a garbage segment. Currently only observed in tests/migrations, but the
guard is one line.

### P3-4 (strategic, discuss before doing): type-driven binding

Most P0s share one root cause: detectors re-derive types from naming conventions
because the AST pass has no type information. Evaluate loading `go/packages` +
`go/types` for the detector pass (or resolving receivers via already-ingested SCIP
symbols) so that "is this a PgxRepo / ServiceClient / http wrapper" is a type fact,
not a string heuristic. This subsumes P0-2/P0-3/P0-5-fallback and the field-suffix
heuristics. Sizeable change — write a design note first.

---

## Verification checklist (after all P0s)

1. `make test` green.
2. Re-index settlement, payin, event, ruleengine, account.
3. Spot-check in Neo4j:
   - `MATCH (d:DBCall {serviceName:'settlement'}) RETURN count(d)` — should rise vs baseline (record baseline first).
   - `MATCH (h:HTTPCall {callerService:'payin'}) RETURN count(h)` — nonzero.
   - `MATCH (g:GRPCCall)-[:CALLS_SERVICE]->(s:Service) RETURN g.protoService, s.name` — no dashboard mis-binds.
   - `MATCH (o:OutboxCall {callerService:'settlement'}) WHERE o.filePath CONTAINS 'create_adjustment' RETURN o` — exists.
4. Update `~/Workspace/Tazapay/.knowledge/analysis/codegraph-indexer-gap-analysis-2026-07-18.yaml`: mark fixed findings with `fixed: <commit>` and `stale: true` where applicable.
