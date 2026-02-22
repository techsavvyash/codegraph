# Implementation Audit (Post-Plan Execution)

This audit evaluates implementation quality against the enterprise plan in `docs/12-implementation-plan.md`, with focus on correctness, scalability, and cleanliness.

## Summary

The repository has meaningful progress on overlays, chunked docs, tombstones, generated context primitives, and indexer management scaffolding.

However, there are still important gaps between "feature present" and "feature robust/end-to-end":

- Bundled SCIP indexers are not yet true no-install end-to-end.
- Overlay precedence is not deterministic in scoped search (can return duplicate logical nodes).
- Inter-service dependency inference query shape does not match the edges currently written by analyzers.
- Scope-safe behavior is incomplete across relationships and some query paths.
- Multi-tenant/repo invariants from the design docs are not implemented yet.

## Findings

### 1) Critical: Bundled SCIP indexers are not no-install end-to-end

What exists:

- `IndexerManager` and CLI commands exist (`indexers install`, `indexers status`).

Gaps:

- `index scip` still hard-fails on PATH validation before indexing:
  - `cmd/codegraph/main.go` (SCIP command path) calls `ValidateEnvironment()`.
  - `pkg/indexer/static/scip_indexer.go` `ValidateEnvironment()` only checks `exec.LookPath`.
- `DefaultReleases()` has versions but no download URLs/checksums, so "install" falls back to manual tool install commands.

Impact:

- Violates the plan requirement that users should not have to install SCIP indexers separately.

---

### 2) Critical: Overlay-aware search is not deterministic overlay-wins by `nodeKey`

What exists:

- `SearchNodesScoped()` includes PR + main nodes and applies tombstone filtering.

Gaps:

- Query returns both overlay and main rows; it only sorts PR first.
- It does not collapse to one effective result per `nodeKey`.

Impact:

- Violates deterministic "effective node" resolution requirement.

---

### 3) Critical: Service dependency inference is disconnected from produced graph shape

What exists:

- `ServiceDepsQuery` with `CALLS_SERVICE` querying and creation.

Gaps:

- Inference expects `[:MAKES_HTTP_CALL]` and `call.targetUrl`.
- Current symbol analyzer writes `[:CALLS_API]` and `SDKCall.url`.

Impact:

- Task 8 appears implemented structurally, but likely not functionally in real indexing runs.

---

### 4) High: Flow spine generation and retrieval are scope-leaky

What exists:

- Flow node generation, `HAS_STEP` edges, list/get commands.

Gaps:

- Discovery/traversal queries are unscoped.
- `HAS_STEP` target match is by `nodeKey` without scope guard.
- `GetFlow` / `ListFlows` are unscoped.

Impact:

- PR overlay flow queries can blend main + PR data and produce non-deterministic results.

---

### 5) High: Scope metadata is incomplete on relationships

What exists:

- Many nodes now include `scope`/`scopeId`.

Gaps:

- Core relationships are still often written without scope fields (`CONTAINS`, `DEFINES`, `REFERENCES`, etc.).
- Document relationship paths also have mixed scoped/unscoped behavior.

Impact:

- Violates "every indexed node and relationship is scope-queryable" invariant.

---

### 6) High: Multi-tenant/repo invariants are not implemented

What exists:

- Extensive index set including `(nodeKey, scopeId)` patterns.

Gaps:

- `GetConstraints()` currently returns empty.
- Node model does not include `tenantId` and `repo` as first-class required properties.
- Node key derivation is not repo-qualified for all entities.

Impact:

- Limits enterprise isolation guarantees and can create collisions in multi-repo/multi-tenant deployments.

---

### 7) Medium: Generated context is implemented as a library surface, but not wired into PR indexing lifecycle

What exists:

- `ContextGenerator` supports `PullRequest` and `GeneratedDoc` creation and linking.

Gaps:

- No clear CLI/indexing pipeline path invoking generator during PR overlay indexing.

Impact:

- Task 10 appears partial (storage helpers present, lifecycle integration missing).

---

### 8) Medium: Legacy indexer/query paths still violate stable identity + scope model

What exists:

- SCIP paths use `nodeKey` + `scopeId` consistently for many writes.

Gaps:

- Legacy AST indexer merges by legacy keys (`name`, `path`, `signature`) and omits scope/nodeKey in major paths.
- Legacy call-graph path still depends on `f.id` patterns.

Impact:

- Coexistence of legacy and new models can reintroduce non-idempotent or non-overlay-safe behavior.

---

### 9) Medium: Chunk-level linking can cross scope

What exists:

- Chunk linker writes provenance on `MENTIONS` edges.

Gaps:

- Target code-node lookup/match is not scope-restricted.

Impact:

- PR scope document chunks can link against main-scope-only targets unexpectedly.

---

### 10) Medium: Tests pass but integration depth is shallow for key invariants

What exists:

- Package-level unit tests pass for several new components.

Gaps:

- Overlay tests are mostly semantic/logic stubs, not DB-backed precedence/tombstone integration tests.
- Limited end-to-end tests for generated context lifecycle, flow scope resolution, and service-edge inference.

Impact:

- Important regressions can pass CI undetected.

## What Looks Clean / Strong

- `DocumentChunk` + hash-based incremental updates are implemented with stable behavior.
- Tombstone creation API and model are straightforward and aligned to overlay-delete semantics.
- Scope propagation exists in many newer write paths (especially SCIP/document nodes).

## Prioritized Next Steps (Independently Shippable)

### Priority 1: Make bundled indexers truly no-install end-to-end

Ship chunk:

- Update `ValidateEnvironment()` to resolve/install via `IndexerManager` (or remove pre-check and rely on generation path).
- Add auto-install on first use (configurable flag default true).
- Provide real release URL + checksum metadata for supported indexers (at least Go + TS first).

Verification:

- Clean environment with empty cache + no indexers in PATH.
- `codegraph index scip ...` succeeds with auto-install.

---

### Priority 2: Enforce deterministic overlay precedence in search/retrieval

Ship chunk:

- Update scoped search to return one effective row per `nodeKey` (overlay > main, tombstone respected).
- Reuse this resolution in source/symbol/flow retrieval endpoints.

Verification:

- Integration tests for:
  - overlay node overrides main node
  - tombstone hides main node
  - no duplicate logical node in results

---

### Priority 3: Scope all relationship writes and reads

Ship chunk:

- Ensure newly written relationships include `scope` + `scopeId` where applicable.
- Scope-filter key read paths (flow generation/query, chunk linking targets, etc.).

Verification:

- Main + PR indexing of same repo produces no cross-scope traversal leakage in scoped queries.

---

### Priority 4: Repair inter-service dependency extraction pipeline

Ship chunk:

- Align inference query to actual emitted call nodes/relationships (`CALLS_API`/`SDKCall.url`, etc.).
- Invoke inference in post-index pipeline and/or provide explicit command.

Verification:

- Fixture with two services and known HTTP call yields scoped `CALLS_SERVICE` edge with evidence.

---

### Priority 5: Implement tenant/repo model + constraints

Ship chunk:

- Add `tenantId` and `repo` to core nodes and key relationships.
- Introduce real constraints (not only indexes) aligned with enterprise invariants.

Verification:

- Multi-repo fixture in one DB has no key collisions and correct isolation in queries.

---

### Priority 6: Wire generated context into PR overlay indexing lifecycle

Ship chunk:

- On PR overlay index: create/update `PullRequest` node, `GeneratedDoc` nodes, and provenance edges.
- Add retrieval command/path for generated context.

Verification:

- PR indexing fixture yields retrievable `pr_summary`, `flow_summary`, `docstring_suggestion` with `DERIVED_FROM` edges.

## Suggested Immediate Execution Order

1. Priority 1 (bundled indexers E2E)
2. Priority 2 (deterministic overlay resolution)
3. Priority 3 (relationship scoping consistency)
4. Priority 4 (service interconnections pipeline fix)
5. Priority 5 (tenant/repo invariants)
6. Priority 6 (generated context lifecycle wiring)

This order maximizes immediate correctness for enterprise PR workflows while keeping each step independently shippable.
