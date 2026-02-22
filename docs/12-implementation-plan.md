# Implementation Plan (Independently Committable Tasks)

This plan builds on the current CodeGraph repository and evolves it into an enterprise-grade, continuously updated context engine.

Constraints from requirements:

- PR overlays become queryable in ~3–4 minutes.
- Main graph remains stable; PRs are overlays.
- Business-context documents (Confluence/GDocs/Markdown/docstrings) are chunked + embedded + linked to code.
- Auto-generated “documentation” means generated docstrings + flow summaries + PR summaries stored as first-class knowledge.
- SCIP indexers should be built into CodeGraph so users do not install them separately.

Each task below is designed to be **independently committable** with explicit verification steps.

## Task 0: Baseline Verification & Bench Harness

**Goal:** establish “before” numbers and prevent regressions.

Changes:

- Add a small benchmark/harness script for indexing a known fixture repo.
- Add a simple “smoke test” that:
  - starts Neo4j (or uses existing)
  - creates schema
  - indexes sample project
  - runs a basic search query

Verification:

- `make build`
- `make test`
- `make docker-up` (optional local)
- `make neo4j-schema`
- run the new smoke script

Commit: `test: add indexing smoke harness`

## Task 1: Introduce Stable `nodeKey` Across Core Node Types

**Goal:** overlays, linking, and citations require stable identity.

Current repo notes:

- Several subsystems rely on Neo4j `elementId()` or an `id` property inconsistently.
- Call graph and intelligent linking code frequently match on `f.id`, but static indexer does not set `id` properties consistently.

Changes:

- Define `nodeKey` for:
  - `Service`, `File`, `Symbol`, `Function`, `Method`, `Class`, `Interface`, `Variable`, `Parameter`
- Update indexers to set `nodeKey` on merge.
- Update query code paths to prefer matching by `nodeKey` (or SCIP `symbol`) rather than `id`.
- Add constraints/indexes on `(label, tenantId, repo, scope, scopeId, nodeKey)`.

Verification:

- `make test`
- `make docker-up`
- `make neo4j-schema`
- index a project twice and verify node counts do not explode (idempotence)

Commit: `feat: add stable nodeKey to core graph entities`

## Task 2: Add Scope Fields (`main` + `pr`) to Indexers

**Goal:** every indexed node/edge can exist in main or overlay.

Changes:

- Add `scope` and `scopeId` fields to indexing context.
- Update SCIP and document indexers to write scoped nodes.
- Ensure relationships include the same scope fields.

Verification:

- Index in main scope; record node count.
- Index the same repo into a PR scope; verify you can query by scope.

Commit: `feat: add scope/scopeId for main and PR overlays`

## Task 3: Tombstones for Overlay Deletions

**Goal:** deleting a file/symbol in a PR should hide main results.

Changes:

- Add `Tombstone` node type.
- Implement tombstone creation from PR diffs (deleted files, removed symbols).
- Update search/query resolution to respect tombstones when scope=pr.

Verification:

- Create a PR overlay fixture with a deleted symbol.
- Verify lookup by `nodeKey` returns “not found” in overlay resolution.

Commit: `feat: tombstones for PR overlay deletions`

## Task 4: DocumentChunk Nodes + Hash-Based Incremental Doc Updates

**Goal:** documents must be chunked and linked at chunk level.

Current repo notes:

- `pkg/indexer/documents/parser.go` has chunking logic, but the indexer currently stores `Document.content` as one blob.

Changes:

- Create `DocumentChunk` nodes with:
  - `textHash`, `chunkId`, `headingPath`, `offsets`, `documentKey`
- Add `(Document)-[:HAS_CHUNK]->(DocumentChunk)`
- Ensure doc re-index uses `textHash` to only update changed chunks.

Verification:

- Index a markdown folder twice, verify `DocumentChunk` counts are stable.
- Modify one paragraph, reindex, verify only one chunk changes (`textHash` differs).

Commit: `feat: document chunk nodes with incremental updates`

## Task 5: External Doc Connectors (Confluence / GDocs)

**Goal:** ingest business context from external sources on a schedule with an “immediate sync” option.

Changes:

- Add connector interfaces:
  - `ListDocuments()`
  - `FetchDocument(docId)`
- Implement one connector first (Confluence or GDocs) behind feature flags.
- Add a CLI command:
  - `codegraph docs sync --source confluence --space X` (scheduled use)
  - `codegraph docs sync --url <docUrl>` (immediate button equivalent)

Verification:

- Unit test connector parsing and normalizing.
- Manual: sync one doc and confirm chunks appear in graph.

Commit: `feat: external doc connector + sync CLI`

## Task 6: Vector Store Abstraction (Prepare to Move Embeddings Out of Graph)

**Goal:** keep graph responsive by moving high-volume embeddings to a vector store.

Current repo notes:

- `pkg/search/vector_search.go` currently uses Neo4j vector indexes.

Changes:

- Introduce a `VectorStore` interface:
  - `UpsertVectors([]VectorUpsert)`
  - `Query(vector, filters, k)`
- Implement `Neo4jVectorStore` adapter (current behavior).
- Add `QdrantVectorStore` (or other) as optional backend.
- Store `vectorId` on nodes; embeddings live in vector store.

Verification:

- Unit test vector store interface with a fake.
- Integration test with Neo4j (existing behavior).

Commit: `feat: vector store interface + neo4j adapter`

## Task 7: Semantic Linking at Chunk Level With Provenance

**Goal:** link `DocumentChunk` → code nodes with reasons/confidence.

Changes:

- Extend intelligent linking to operate on chunks.
- Store edges:
  - `MENTIONS {confidence, reasons, model, createdAt}`

Verification:

- Index a doc that references a known function.
- Verify `MENTIONS` edges exist and include reasons.

Commit: `feat: chunk-level semantic linking with provenance`

## Task 8: Inter-Service Interconnections (Microservices)

**Goal:** make service-to-service edges first-class so retrieval can answer “what depends on what?” and “what does this PR impact?”

Current repo notes:

- `pkg/indexer/static/api_analyzer.go` already detects API patterns and cross-service calls, but this data should become a stable, scoped, queryable service graph.

Changes:

- Add explicit service-level edges:
  - `(:Service)-[:CALLS_SERVICE]->(:Service)`
  - `(:Service)-[:PUBLISHES_TO|:SUBSCRIBES_TO]->(:Topic)` (optional first pass)
  - `(:Service)-[:READS_FROM|:WRITES_TO]->(:Datastore)` (optional first pass)
- Ensure inter-service edges carry:
  - `confidence`, `source`, `scope`, `scopeId`
- Add a query endpoint / CLI command:
  - `codegraph query deps --service <name> --scope-id <prId?>`

Verification:

- Index two small services with a known HTTP call relationship.
- Verify `CALLS_SERVICE` edges exist in both main and PR scope.

Commit: `feat: persist and query inter-service dependencies`

## Task 9: Flow Spine Nodes + Flow-Aware Linking

**Goal:** represent “flows” as first-class objects, not just many `CALLS` edges.

Changes:

- Add `Flow` node type:
  - `Flow {nodeKey, name, entrypointKey, scope, scopeId}`
- Add `(:Flow)-[:HAS_STEP]->(:Function|:Service|:APIEndpoint)` with order.
- Create flow spines from:
  - API endpoints
  - consumers
  - cron jobs

Verification:

- Pick a simple endpoint; generate a flow spine of depth 2.
- Confirm it can be retrieved and summarized.

Commit: `feat: flow spine nodes and bounded expansion`

## Task 10: Generated Context in PR Overlay

**Goal:** generate PR summaries, flow summaries, docstring suggestions as knowledge units.

Changes:

- Create `PullRequest` node on overlay indexing.
- Create `GeneratedDoc` nodes (types: `pr_summary`, `flow_summary`, `docstring_suggestion`).
- Add `DERIVED_FROM` and `DOCUMENTS` edges.

Verification:

- Run overlay indexing on a fixture PR diff.
- Verify generated nodes exist and are retrievable via search.

Commit: `feat: generate PR context and store as knowledge units`

## Task 11: Overlay-Aware Retrieval API

**Goal:** all search and query endpoints must resolve main+overlay deterministically.

Changes:

- Introduce a query parameter: `scopeId` (optional) and default to main.
- Enforce overlay precedence and tombstones in:
  - symbol lookup
  - search results
  - flow queries

Verification:

- Write tests for “overlay wins” and “tombstone hides”.

Commit: `feat: overlay-aware query resolution`

## Task 12: Bundled SCIP Indexers (No Separate Installs)

**Goal:** CodeGraph manages indexer acquisition and execution.

Changes:

- Add an `IndexerManager` that:
  - downloads language-specific SCIP indexers
  - caches them under `~/.codegraph/indexers/<name>/<version>/...`
  - verifies checksum and executable bit
  - selects the correct binary for OS/arch
- Update `pkg/indexer/static/scip_indexer.go` to call the manager rather than `exec.LookPath`.
- Add a CLI command:
  - `codegraph indexers install --language go,typescript,java`
  - optionally auto-install on first use.

Verification:

- Unit test manager resolution logic.
- Integration: run SCIP indexing in a clean environment where binaries are not in PATH.

Commit: `feat: bundled SCIP indexers with auto-install`

## Final End-to-End Verification

Once Tasks 1–12 are implemented:

1. Index repo into main.
2. Create PR overlay from a diff; verify it is queryable in minutes.
3. Sync a doc (markdown and one external connector) and link it to code.
4. Run a query like “payment capture flow” and verify:
   - doc chunks are retrieved
   - flow spine is retrieved
   - citations are correct

Suggested commands:

- `make build`
- `make test`
- `make lint`
- `make docker-up`
- `make neo4j-schema`
- `make test-integration`
