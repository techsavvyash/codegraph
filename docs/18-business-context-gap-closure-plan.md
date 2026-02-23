# Business Context Enrichment Gap-Closure Plan

This document turns the current implementation gap analysis into an execution plan with independently committable slices.

Primary objective:

- Make business-context documentation reliably link to code and flow paths across scopes (`main` + `pr`) even when docs arrive after code indexing.

## Target Outcomes

1. Delayed doc ingestion works:
- If code is indexed today and business docs are indexed days later, chunk-level links, semantic links, and flow links are created deterministically.

2. Overlay-aware retrieval works end-to-end:
- Query with `scopeId=pr-*` returns effective overlay view (overlay wins, tombstones hide, no cross-scope vector/text collisions).

3. Analysis pipeline is first-class:
- Flow inference, doc linking, and generated context (`pr_summary`, `flow_summary`, `docstring_suggestion`) run as explicit pipeline stages with verifiable outputs.

4. Provenance is auditable:
- Links and generated docs include reason/source/model/confidence metadata.

---

## Phase 0: Baseline + Guardrail Tests (No Behavior Change)

Goal:
- Lock in current behavior and add failing tests that represent required enterprise behavior.

Changes:

- Add integration tests for:
  - delayed doc ingestion linking to existing code + flows
  - external docs sync creating chunk links (currently missing)
  - overlay-aware hybrid retrieval with `scope-id`
  - tri-store key collision across `main` vs `pr` (same `nodeKey`)

Suggested file targets:

- `test/integration/audit_fixes_test.go`
- `test/integration/indexing_test.go`
- `test/three_store_pipeline_test.go`

Verification:

- `go test ./test/integration -run Delayed|Overlay|Chunk|TriStore -count=1`
- `go test ./test -count=1`

Commit:

- `test: add delayed-doc and overlay tri-store regression coverage`

---

## Phase 1: Fix External Doc Sync Parity With Local Doc Indexing

Goal:
- Ensure `index docs sync` runs the same enrichment path as `index docs`.

Gaps addressed:

- External doc sync currently does not run chunk linker.
- External doc sync does not wire vector/text stores in CLI path.

Changes:

1. Wire optional embedding/vector/text stores in `docs sync` command.
2. In external indexing path, execute chunk-level linker after chunk creation.
3. Ensure scope is passed consistently to all linking paths.

Suggested file targets:

- `apps/cli/main.go`
- `libs/indexer-go/documents/indexer.go`

Verification:

- Sync one external doc with backticked symbol references.
- Assert `(:DocumentChunk)-[:MENTIONS]->(...)` edges exist in the expected scope.
- Re-run sync unchanged and verify chunk counts stable.

Commit:

- `feat: run full enrichment pipeline for external docs sync`

---

## Phase 2: Scope-Safe Chunk Linking + Intelligent Linking Identity Migration

Goal:
- Remove legacy `id` assumptions and make doc->code linking fully `nodeKey` + scope aware.

Gaps addressed:

- `ChunkLinker` scope value is not set by `DocumentIndexer` before lookup.
- `IntelligentDocumentLinker` still depends on legacy `id` and unscoped call graph queries.

Changes:

1. Set chunk linker scope in indexer before linking.
2. Migrate intelligent linker internals from `id` to `nodeKey`:
  - direct matches return nodeKey
  - semantic/hybrid matches preserve nodeKey identity
  - call graph expansion traverses by nodeKey + scope filters
  - `MENTIONS` relationship creation resolves target by `nodeKey` in visible scope
3. Preserve provenance fields (`confidence`, `reasons`, `model`, `createdAt`, `scope`, `scopeId`).

Suggested file targets:

- `libs/indexer-go/documents/indexer.go`
- `libs/search-go/chunk_linker.go`
- `libs/search-go/intelligent_linker.go`

Verification:

- PR scope test where same symbol exists in `main` and `pr`.
- Assert linker chooses visible overlay target, never unrelated scope target.
- Assert no failed relationship creation from element-id mismatch.

Commit:

- `feat: migrate document linking to scoped nodeKey resolution`

---

## Phase 3: Overlay-Aware Hybrid Retrieval End-to-End

Goal:
- Make `query search --scope-id` actually scope-aware across graph + vector + text.

Gaps addressed:

- CLI ignores `scope-id` in hybrid search path.
- Vector/text IDs currently collide across scopes (`ID=nodeKey`).
- Neo4j rehydration resolves by nodeKey without scope filter.

Changes:

1. Introduce canonical retrieval key:
- `vectorId = <scopeId>::<nodeKey>`

2. Write scope metadata into vector/text payloads:
- `scope`, `scopeId`, `repo`, `tenantId` (where available)

3. Plumb `scopeId` into search API:
- CLI `query search` should pass `scope-id` into hybrid search.
- Vector query should apply metadata filters.
- Text query should filter by scope metadata where backend supports it.
- Neo4j resolver should resolve `(scopeId=nodeScope OR main)` and apply overlay precedence.

4. Update dedupe key logic for results:
- use effective identity (`scope-aware node identity`) to avoid duplicate or cross-scope bleed.

Suggested file targets:

- `apps/cli/main.go`
- `libs/search-go/hybrid_search.go`
- `libs/search-go/vector_store.go`
- `libs/search-go/qdrant_vector_store.go`
- `libs/indexer-go/static/tristore.go`
- `libs/indexer-go/documents/indexer.go`

Verification:

- Index same repo into `main` and `pr-42` with same nodeKeys but different content.
- Query with no scope -> main result.
- Query with `--scope-id pr-42` -> overlay result.
- Tombstoned main node does not appear in PR view.

Commit:

- `feat: enforce scope-aware hybrid retrieval and tri-store identity`

---

## Phase 4: Analysis Pipeline Orchestrator (Flow + Linking + Generated Context)

Goal:
- Add a first-class analysis pipeline instead of scattered post-indexing hooks.

Pipeline stages:

1. `IngestCode`
2. `InferServiceDependencies`
3. `GenerateFlowSpines`
4. `IngestDocuments`
5. `LinkDocumentChunks`:
- explicit linker
- semantic linker
- flow-aware linker
6. `GenerateContextDocs`:
- PR summary
- flow summaries
- docstring suggestions
7. `RefreshRetrievalIndexes`

Changes:

- Introduce pipeline coordinator with stage contracts and stage metrics.
- Ensure idempotent stage reruns by hash/status markers.
- Add failure isolation: partial stage failure should not corrupt prior stages.

Suggested file targets:

- `libs/indexer-go/static/scip_indexer.go`
- `libs/query-go/flow_spine.go`
- `libs/query-go/service_deps.go`
- `libs/indexer-go/generated/context.go`
- New package: `libs/indexer-go/pipeline/`

Verification:

- Run pipeline on this repository:
  - verify `Flow` nodes + `HAS_STEP`
  - verify `GeneratedDoc` nodes of all three types
  - verify `DERIVED_FROM` edges exist for generated docs

Commit:

- `feat: add analysis pipeline orchestrator for flow and generated context`

---

## Phase 5: Flow-Aware Document Linking

Goal:
- Link business narrative chunks to flow spines, not only symbol names.

Changes:

1. Add flow candidate detection from chunk content:
- endpoint-like paths
- event/topic references
- business action verbs mapped to entrypoints

2. Expand bounded graph from entrypoints:
- depth budget (default 2)
- fanout cap (default 20)

3. Persist links:
- `(DocumentChunk)-[:MENTIONS {reasons:['flow_spine'], confidence, model, scope, scopeId}]->(Flow|Function|Service|APIRoute)`

Suggested file targets:

- `libs/search-go/chunk_linker.go`
- `libs/query-go/flow_spine.go`

Verification:

- Add fixture doc describing one known API flow in this repo.
- Verify chunk links include at least one flow node and at least one flow step node.

Commit:

- `feat: add flow-aware document chunk linking`

---

## Phase 6: Generated Context Completeness + Provenance

Goal:
- Promote generated context from PR summary only to full set.

Changes:

1. Fix PR summary `changedFileKeys` bug (element ID vs nodeKey mismatch).
2. Add flow summary generation invocation after flow stage.
3. Add docstring suggestion generation for changed exported symbols.
4. Ensure all generated docs have:
- `DERIVED_FROM` edges
- `scope`, `scopeId`, `model`, `createdAt`

Suggested file targets:

- `libs/indexer-go/static/scip_indexer.go`
- `libs/indexer-go/generated/context.go`

Verification:

- For a PR scope run, assert:
  - exactly one `PullRequest` node
  - `GeneratedDoc` contains all expected `type`s
  - each `GeneratedDoc` has at least one `DERIVED_FROM`

Commit:

- `feat: complete generated context lifecycle with provenance`

---

## Phase 7: Multi-Repo / Tenant Identity Hardening

Goal:
- Prevent collisions and enforce enterprise isolation invariants.

Changes:

1. Expand identity model to include repo/tenant namespacing where required.
2. Add constraints/index strategy for scoped identities.
3. Ensure retrieval and linking queries include repo/tenant filters when available.

Suggested file targets:

- `libs/core-models-go/nodekey.go`
- `libs/schema-go/schema.go`
- `libs/search-go/*`
- `libs/query-go/*`

Verification:

- Dual-repo fixture with intentionally colliding symbol names.
- Assert no cross-repo contamination in links or retrieval.

Commit:

- `feat: harden scoped identity with repo and tenant isolation`

---

## Phase 8: Performance + SLA Tuning for 3-4 Minute Overlay Target

Goal:
- Keep enrichment fast enough for PR workflows.

Changes:

- Add stage timing + counters in pipeline.
- Batch writes for link creation and generated docs.
- Async/non-blocking optional stages (embeddings, long-form generation) with freshness markers.

Suggested file targets:

- `libs/indexer-go/pipeline/`
- `libs/benchmarks-go/`
- `tools/scripts/smoke-test.sh`

Verification:

- Benchmark this repo under PR scope with pipeline enabled.
- Capture per-stage durations and end-to-end latency.

Commit:

- `perf: add pipeline metrics and overlay SLA optimizations`

---

## Final Acceptance Checklist

Use this repository as the canonical acceptance fixture.

1. Index code in `main` scope.
2. Two days later (simulated), sync a business-context doc (external connector path).
3. Verify:
- `DocumentChunk` nodes are created/updated incrementally by `textHash`.
- chunk links exist for explicit + semantic + flow-aware reasons.
- links resolve to current effective nodes/flows in selected scope.
4. Run PR overlay indexing for same repo and query with `--scope-id`.
5. Verify overlay precedence and tombstones in hybrid retrieval.
6. Verify generated docs include `pr_summary`, `flow_summary`, `docstring_suggestion` with provenance.

Suggested validation commands:

- `make build`
- `go test ./libs/search-go ./libs/indexer-go/... ./libs/query-go ./libs/core-models-go`
- `go test ./test/integration -count=1`
- `make lint`

## Recommended Execution Order

1. Phase 0
2. Phase 1
3. Phase 2
4. Phase 3
5. Phase 6 (quick correctness win)
6. Phase 4
7. Phase 5
8. Phase 7
9. Phase 8

This ordering de-risks correctness first (linking/retrieval identity), then builds pipeline sophistication on top of stable semantics.
