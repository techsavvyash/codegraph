# Polyglot Monorepo Design and Refactor Plan

## Goal

Consolidate CodeGraph into a **single polyglot monorepo platform** where all services are developed, versioned, and deployed together while preserving the agreed **three-storage architecture**:

1. **Graph store** for structure and relationships (Neo4j)
2. **Vector store** for embeddings (Qdrant)
3. **Text index** for keyword/BM25 retrieval (OpenSearch or Elasticsearch)

This design intentionally keeps Go for graph/indexing core paths, and introduces language boundaries only where it improves maintainability and delivery speed.

---

## Non-Negotiable Architecture Invariants

These are preserved from prior architecture decisions and must hold during refactor:

- `nodeKey` is canonical and stable across re-indexes
- `scope` + `scopeId` support `main` and PR overlay behavior
- Tombstones hide main-scope nodes in PR overlays
- `tenantId` and `repo` are first-class dimensions on nodes and relationships
- Overlay precedence is deterministic: overlay wins, tombstone hides, then fallback to main
- Semantic links carry provenance (`confidence`, `reasons`, `model`)

---

## Recommended Monorepo Management Tool

### Choice: `Nx`

Use **Nx** as the workspace/task orchestrator for this polyglot repo.

Why Nx is a good fit here:

- Handles polyglot task graphs with explicit dependencies and caching
- Works well even when projects are not TypeScript-first (via `run-commands`)
- Gives a clean CI model (`affected` targets) for fast PR checks
- Easier to adopt incrementally than Bazel for current team velocity

When to prefer another tool:

- Choose **Bazel** only if you need maximal hermeticity and remote execution at very large scale
- Choose **Pants** only if Python becomes dominant

---

## Target Repository Structure

```text
codegraph/
  apps/
    cli-go/                      # current cmd/codegraph + internal wiring
    mcp-server-go/               # current mcp-server (can later become ts)
    docs-intel-py/               # Python document parsing/extraction sidecar
    gateway-ts/                  # optional API gateway/control-plane facade

  services/
    indexing-go/
      static/                    # SCIP pipeline, symbol analyzer, call graph
      generated/                 # PR summaries, generated docs
    retrieval-go/
      query/                     # LSP-style and graph traversals
      search/                    # hybrid search orchestration
    connectors/
      confluence-go/
      gdocs-go/

  libs/
    core-models-go/              # node/edge models, scope and nodeKey logic
    neo4j-client-go/
    vector-client-go/            # Qdrant abstraction
    text-index-client-go/        # OpenSearch abstraction
    protocols/
      protobuf/                  # shared protobuf/scip adapters if needed
      json-schema/               # tool contracts and API payload schemas

  infra/
    docker/
      compose.platform.yml       # neo4j + qdrant + opensearch + local services
    k8s/
    terraform/

  tools/
    nx/                          # nx workspace config, generators, plugins
    scripts/
      bootstrap.sh
      smoke-test.sh

  docs/
    15-polyglot-monorepo.md

  nx.json
  package.json                   # workspace runner only
  pnpm-workspace.yaml
```

Notes:

- Keep Go code grouped by domain (`indexing-go`, `retrieval-go`) not by transport (CLI/server).
- Keep storage clients in shared libraries to avoid direct store-specific code in business services.
- Allow `docs-intel-py` to evolve independently while still part of one repo and one CI graph.

---

## Platform Composition (How Services Connect)

### Data flow

1. `indexing-go` ingests code/docs and writes canonical graph entities to Neo4j.
2. Embedding workloads push vectors to Qdrant through `vector-client-go`.
3. Lexical content is indexed into OpenSearch through `text-index-client-go`.
4. `retrieval-go` executes candidate-fetch across vector+text, then graph expansion, then rerank.
5. `mcp-server-go` and `cli-go` call `retrieval-go`/`indexing-go` APIs or shared libs.

### Three-store contract boundaries

- Graph store owns: entity identity, topology, provenance, scope semantics.
- Vector store owns: chunk/comment/generated-doc embeddings and ANN retrieval.
- Text index owns: tokenized source text, docs text, symbol keywords.

Do not duplicate ownership semantics across stores. Cross-store records should be joined by stable IDs (`nodeKey`, chunk IDs).

---

## Nx Workspace Model

Define standard targets for each project:

- `build`
- `test`
- `lint`
- `typecheck` (where relevant)
- `integration`
- `smoke`

Example dependency policy:

- `apps/cli-go:build` depends on `services/indexing-go:build`, `services/retrieval-go:build`
- `apps/mcp-server-go:test` depends on `libs/*:test`
- `integration` targets depend on `infra/docker:up`

Recommended CI commands:

- `nx affected -t lint,test,build`
- `nx affected -t integration` on protected branches
- `nx run-many -t smoke --all` for nightly checks

---

## Refactor Plan (Phased, Low Risk)

### Phase 0: Prep and Safety Rails

- Freeze schema invariants in tests (`nodeKey`, overlay precedence, tombstones)
- Add integration tests that validate all three stores are writable/readable
- Introduce a compatibility matrix doc for current commands and future equivalents

Exit criteria:

- Existing CLI/MCP behavior unchanged
- Baseline performance captured for indexing and retrieval

### Phase 1: Workspace Bootstrap

- Initialize Nx at repo root (task runner only)
- Register current Go CLI and MCP as separate Nx projects
- Register Make targets as Nx tasks (no behavior change yet)

Exit criteria:

- `nx run cli-go:build` and `nx run mcp-server-go:build` are green

### Phase 2: Logical Re-layout (No Runtime Changes)

- Move packages into `apps/`, `services/`, `libs/` structure
- Use temporary shim imports where needed to avoid a big-bang rewrite
- Keep `go.mod` stable initially; run `go mod tidy` only after moves stabilize

Exit criteria:

- Unit/integration tests still pass
- No feature regressions

### Phase 3: Three-Store Abstraction Hardening

- Finalize `GraphStore`, `VectorStore`, `TextIndexStore` interfaces
- Ensure all indexing paths write through abstractions, not direct clients
- Ensure retrieval uses candidate-from-vector+text then graph expansion

Exit criteria:

- End-to-end retrieval works with all three backends enabled
- Disabling one store produces expected degraded mode behavior

### Phase 4: Python Doc Intelligence Service

- Create `apps/docs-intel-py` for Confluence/HTML parsing and richer extraction
- Keep orchestration and persistence contracts in Go
- Add contract tests for payload compatibility between Go and Python services

Exit criteria:

- Document ingestion parity with existing Go path
- Better extraction quality without breaking IDs/provenance

### Phase 5: CI/CD and Developer Experience

- Replace ad-hoc CI scripts with Nx affected pipeline
- Add preconfigured local platform compose (`neo4j + qdrant + opensearch`)
- Publish developer workflows: build, index, query, integration, smoke

Exit criteria:

- Faster PR checks
- New contributor setup time reduced

### Phase 6: Optional MCP TypeScript Migration

- Only if MCP tool surface starts changing rapidly
- Keep tool contracts in shared JSON schemas first, then migrate implementation

Exit criteria:

- Zero MCP protocol regression
- Existing clients continue to work

---

## Incremental Migration Mapping (Current -> Target)

- `cmd/codegraph` -> `apps/cli-go`
- `mcp-server/` -> `apps/mcp-server-go`
- `pkg/indexer/static` -> `services/indexing-go/static`
- `pkg/indexer/generated` -> `services/indexing-go/generated`
- `pkg/query` + `pkg/search` -> `services/retrieval-go`
- `pkg/models` -> `libs/core-models-go`
- `pkg/neo4j` -> `libs/neo4j-client-go`

Keep import aliases stable during migration to reduce merge risk.

---

## Store Provisioning for Local Platform

Local compose should include:

- `neo4j` (graph)
- `qdrant` (vector)
- `opensearch` (text)
- `codegraph-api`/`mcp` services

Health checks should gate integration tests so cross-store tests do not flake.

---

## Delivery Checklist

- [ ] Nx workspace initialized with Go and shell-based targets
- [ ] New directory layout in place with no behavior changes
- [ ] Three-store abstractions enforced in code paths
- [ ] Overlay + tombstone behavior covered by tests
- [ ] Python docs-intel service added behind stable contracts
- [ ] CI uses `nx affected`
- [ ] Developer quickstart updated for monorepo workflows

---

## Decision Summary

- **Monorepo:** Yes, single polyglot platform repo
- **Manager:** Nx (incremental, practical, CI-friendly)
- **Core runtime:** Go remains primary for indexing/retrieval/graph operations
- **Polyglot use:** Python for document intelligence, optional TypeScript for fast-evolving MCP layer
- **Storage model:** Preserve and enforce graph + vector + text as first-class architecture
