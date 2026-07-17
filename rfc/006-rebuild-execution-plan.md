# RFC-006: Rebuild Execution Plan

| Field | Value |
|-------|-------|
| **Status** | Draft |
| **Created** | 2026-07-06 |
| **Authors** | @techsavvyash |
| **Implements** | RFC-005 (target architecture) |

Phases are strictly ordered; each ends with a green build, passing tests, an indexed
verification run, and incremental commits. "Deletion ships before addition" (RFC-005
I8) is the standing rule.

## Phase 0 — Demolition (no capability loss)

Everything here was verified dead or superseded by the 2026-07 five-subsystem audit.

**0a. Dead modules & artifacts**
- Delete: `libs/retrieval-go`, `libs/context-bundles-go`, `libs/neo4j-client-go`,
  `libs/vector-client-go`, `libs/protocols`, `services/indexing-go`,
  `services/retrieval-go`, `apps/docs-intel-py`, `test/three_store_pipeline_test.go`.
- Delete legacy AST indexer: `libs/indexer-go/static/indexer.go`,
  `static/call_graph.go`, their tests, and the `index project` CLI command.
- `.gitignore` `bin/` + stray binaries; `git rm --cached bin/*`; delete
  `apps/cli/cli`, `mcp-server-go` (root), `bin/*-new`, `bin/cli`.
- Remove deleted modules from `go.work`; drop dead Makefile targets
  (`build-server` doc lie, `index-ts-example`, etc.); fix CLAUDE.md.

**0b. MCP server purge**
- Delete the 20 retired tool definitions, handlers, dispatch cases, and the
  `retiredTools` map; delete Qdrant/Gemini/OpenSearch startup wiring.
- Surface after purge: `schema`, `find`, `expand`, `path`, `cypher`, `source`,
  `entry_points`, `flows`, `render` — behavior unchanged.

**0c. CLI & search-go trim**
- Delete CLI commands for the retired subsystem: `docs`, `enrich`, `embed`,
  `comments`, `link`, `features`, retrieval evals — and their lib backers:
  `libs/evals-go`, `libs/generation-go`, `libs/verification-go`, `libs/llm-go`,
  `libs/indexer-go/documents`, `libs/indexer-go/pipeline` doc stages, tri-store
  writer (`tristore.go`).
- `libs/search-go`: keep fulltext + RRF fusion core (needed by Phase 2); delete the
  doc-linking files (`intelligent_linker`, `chunk_linker`, `flow_linker`,
  `feature_linker`, `llm_validator`, `embedding_service`, qdrant store, …).
- `libs/query-go`: delete `advanced.go` stub methods and dead `GetHover`.
- `libs/intelligence-go`: keep `contracts` + `provenance` only.
- **Preserve in-repo (referenced by RFC-005 §7):** `ChunkDocumentWithMeta` + hash
  chunk-sync — move to `internal/ingest/docs/chunker.go` (or park under
  `libs/indexer-go/docs-parked/` until Phase 4).

**0d. Single module**
- Collapse all remaining `libs/*`, `apps/*` modules into the root `go.mod` under the
  RFC-005 §4 layout (`cmd/`, `internal/`); delete per-module go.mod/go.sum and all
  `replace` directives; delete `go.work`, `nx.json`, `pnpm-workspace.yaml` (root).
- Unify RFC dirs: move `docs/rfc/00{1,2,3}-*` → `rfc/` with new unique numbers,
  stamp real statuses (Superseded/Withdrawn), banner the dead design docs.

**Exit criteria:** `go build ./... && go vet ./...` green; unit tests green;
`codegraph index scip` self-run works against Neo4j; MCP server answers all 9 tools;
diff is pure deletion/moves (~15–18k LOC removed).

## Phase 1 — Write-path correctness (trustworthy graph)

1. **`MergeRelsBatch`** (UNWIND + MERGE) in the graph client; convert every
   structural edge write (DEFINES, CONTAINS, REFERENCES, IMPLEMENTS, DEPENDS_ON,
   EXPOSES_API, File/Service edges) and the per-edge CALLS loop to batched MERGE.
2. **Scope-bounded re-index**: before writing a service's data, delete that
   service's subgraph *within the target scope* via bounded, label-qualified,
   batched deletes (`CALL { } IN TRANSACTIONS`) — replaces both "never delete"
   (SCIP path) and the catastrophic undirected `DETACH DELETE` (removed with AST
   indexer in 0a).
3. **Constraints**: `(nodeKey, scopeId)` uniqueness per label in schema-go (or
   scopeId-in-key fallback per RFC-005 §5.1); migration command
   (`codegraph schema migrate`) that dedupes existing violators then applies.
4. **Injection & identifier hygiene**: parameterize `scopeId` in `TombstoneFilter`;
   single `Ident()` allowlist chokepoint for labels/rel-types/property names;
   remove `Sprintf`-into-Cypher elsewhere.
5. **IndexReport**: typed per-phase counts (written/skipped/failed); non-zero exit
   on data-affecting failure; delete the warn-and-continue empty-`symbolIDs` path.
6. **Store file hashes** on File nodes (today `""`), groundwork for Phase 3
   incremental.
7. Fix `calculateByteOffsets` project-root bug; fix per-run `ComputeDegreeProperties`
   whole-graph write (scope to service).

**Verification:** idempotency test — index `test/fixtures/tiny-go` (and a real repo)
twice, assert identical node/edge counts; constraint violation test; injection
regression test (`scope-id` with `"} MATCH...` payload).

## Phase 2 — Query engine (fast + malleable)

1. **`find`**: validated-label interpolation; FULLTEXT indexes (Neo4j 5 syntax) on
   name/signature per hot label; relevance ordering (index score, exact-match
   boost); keyset pagination. Port search-go's fulltext + RRF core to
   `internal/search`; delete the other two search implementations.
2. **`expand`/`path`**: APOC `expandConfig`/`subgraphNodes` with `NODE_GLOBAL`
   uniqueness; `allShortestPaths` for `path`; caps enforce work bounds.
3. **`flows`/`entry_points`**: label-qualified `nodeKey` lookups (kill labelless
   AllNodesScans); single-traversal instead of query-per-node recursion; service
   filter via `serviceName` property; unify the two centrality definitions.
4. **Timeouts**: default 10s tx timeout in the client; per-tool `timeout_ms`;
   `EXPLAIN` guard on `cypher` as RFC-004 promised.
5. **Name-or-id addressing** on `expand`/`path`/`flows` with ambiguity candidates.
6. **`schema`** from `apoc.meta.stats` + TTL cache.
7. Split both monolith `main.go`s per RFC-005 §4 (protocol loop / tool files /
   query lib), fulfilling RFC-004 Phase 5.

**Verification:** `EXPLAIN` tests assert no `AllNodesScan` on hot paths; p95 `find`
< 200ms on a khaata-scale graph; `expand` depth-6 on the self-index completes < 2s;
all 9 tools exercised end-to-end via MCP stdio.

## Phase 3 — The moat (correct edges)

1. **SCIP parser**: consume `EnclosingRange` (real body ranges; delete
   declaration-order inference in `call_graph_generic.go`); keep reference/type-def
   relationships; scan `ExternalSymbols`; imports from occurrences not regex;
   `signature` vs SCIP-identifier field split.
2. **Symbol key version-normalization** for cross-repo joins (raw SCIP symbol kept
   as property); backfill migration.
3. **Go type-checker resolver**: `types.Implements()` pass emitting provenanced
   IMPLEMENTS edges; wire into `expand(IMPLEMENTS)` and entry-points Tier 2.
4. **TS resolver sidecar** (`checker.isTypeAssignableTo`) — after Go proves the
   protocol.
5. **Boundary stitching v1**: `APICall` per call-site (replaces SDKCall; keyed by
   position, keeps `urlTemplate`/`method`); server route tables incl. NestJS
   decorator synthetic edges; proto/gRPC exact stitching; REST template matching
   with confidence + `matchBasis`; delete `url CONTAINS name` inference.
6. **Provenance properties** (`source`, `confidence`) on all edge writers; edge
   model refuses unprovenanced edges.
7. **Incremental v1**: hash-diff dirty detection → tree-sitter fast path for
   changed files → lazy full SCIP re-run with scope-diff write.

**Verification:** RFC-005 §12 metrics — IMPLEMENTS recall ≥95% on this repo vs
`types.Implements` ground truth; khaata FE+BE cross-service chains ≥4/5; polyglot
fixture golden tests updated.

## Phase 4 — Docs, redone right

1. `Ticket` nodes + deterministic mining (Jira keys in commits/PRs/branches;
   validated code refs in docs) → `TRACKS`/`MENTIONS(source: docmine)`.
2. Atlassian MCP ingestion adapter + generic docs-source interface; revive the
   parked incremental chunker.
3. Hierarchical code summaries (service→module→file→symbol) + Neo4j native vector
   indexes; doc chunks in same space; thresholded `MENTIONS(source: llm,
   confidence)`.
4. Feature-as-view over Ticket/Document clusters; PM query path = find → TRACKS/
   MENTIONS → expand/flows, every answer citing edges.

**Verification:** doc-link precision audit ≥90% for docmine edges; PM-style query
walkthrough documented against a real repo + real docs.

## Verification matrix (run at every phase exit)

| Check | Command |
|---|---|
| Build + vet | `go build ./... && go vet ./...` |
| Unit | `make test` |
| Integration (Neo4j) | `make test-integration` |
| Idempotency | index fixture twice, diff counts |
| Self-index | `./bin/codegraph index scip . --service=codegraph` |
| External repos | index + query `dough-core`, `dough-gateway`, `clanker`, `dough-vm-core` |
| MCP smoke | scripted stdio session exercising all 9 tools |
