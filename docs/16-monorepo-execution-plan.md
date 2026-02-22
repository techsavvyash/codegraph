# Polyglot Monorepo - Concrete Execution Plan

> Generated: 2026-02-21
> Companion to: [15-polyglot-monorepo.md](./15-polyglot-monorepo.md)

---

## Current State Snapshot

```
codegraph/
  cmd/codegraph/        # Go CLI (root go.mod)
  mcp-server/           # MCP server (own go.mod — already decoupled)
  pkg/
    models/             # node/edge models, nodeKey, scope, tombstone
    neo4j/              # Neo4j driver client
    indexer/
      static/           # SCIP + AST indexing pipeline
      generated/        # PR summaries, generated doc indexing
      documents/        # Confluence/doc ingestion
    query/              # LSP-style queries, overlay, graph traversal
    search/             # hybrid search, vector store, Qdrant client
    llm/                # LLM service wrappers
    schema/             # Neo4j constraints/indexes
    benchmarks/
```

**Storage today:** Neo4j (graph) + Qdrant (vector). OpenSearch/text store is **missing** — that is net-new work in Phase 3.

---

## Target Directory Structure

```
codegraph/
  apps/
    cli-go/             # cmd/codegraph → here
    mcp-server-go/      # mcp-server → here
    docs-intel-py/      # new: Python doc parsing sidecar

  services/
    indexing-go/
      static/           # pkg/indexer/static → here
      generated/        # pkg/indexer/generated → here
      documents/        # pkg/indexer/documents → here
    retrieval-go/
      query/            # pkg/query → here
      search/           # pkg/search (non-vector-client files) → here
      llm/              # pkg/llm → here
    connectors/
      confluence-go/    # stub
      gdocs-go/         # stub

  libs/
    core-models-go/     # pkg/models → here
    neo4j-client-go/    # pkg/neo4j → here
    vector-client-go/   # pkg/search/vector_store.go + qdrant_vector_store.go → here
    text-index-client-go/ # new: OpenSearch abstraction
    protocols/
      json-schema/      # tool contracts, API payload schemas

  infra/
    docker/
      compose.platform.yml  # neo4j + qdrant + opensearch + services

  tools/
    scripts/
      bootstrap.sh
      smoke-test.sh

  nx.json
  package.json          # workspace runner only
  pnpm-workspace.yaml
```

---

## Task Dependency Graph

```
Task 1 (freeze invariant tests)
  └─ Task 2 (baseline perf metrics)   [serial: needs task 1 green]
  └─ Task 3 (Nx bootstrap)            [parallel with task 2]
       └─ Task 4 (dir skeleton)
            └─ Task 5 (move libs: models, neo4j, vector-client)
                 └─ Task 6 (move services: indexing, retrieval)
                      ├─ Task 7 (move apps: cli-go, mcp-server-go)
                      │    └─ Task 11 (Python docs-intel service)
                      │         └─ Task 12 (CI/CD + Nx affected pipeline)
                      └─ Task 8 (GraphStore interface)
                           └─ Task 9 (TextIndexStore + OpenSearch client)
                                └─ Task 10 (e2e 3-store validation)
                                     └─ Task 12 (CI/CD + Nx affected pipeline)
```

---

## Phase 0 — Safety Rails

**Goal:** ensure existing behaviour is fully captured by tests before any file moves.

### Task 1 — Freeze schema invariants in tests
- Audit `pkg/models/nodekey_test.go`, `pkg/models/tombstone_test.go`, `pkg/models/scope_test.go`, `pkg/query/overlay_test.go`
- Add missing assertions for:
  - `nodeKey` stability across re-index cycles
  - Overlay precedence: overlay wins > tombstone hides > main fallback
  - `tenantId` + `repo` always present on nodes
- Add integration test stubs: one test each for Neo4j write/read, Qdrant write/read, OpenSearch write/read (OpenSearch stub can be skipped-if-unavailable)
- Run `make test` — must be green before proceeding

### Task 2 — Capture baseline performance metrics
- Run `make benchmark` and save output to `docs/baselines/performance.md`
- Create `docs/baselines/compat-matrix.md` listing every current CLI command and MCP tool with expected input/output behaviour

**Phase 0 exit criteria:**
- `make test` green
- Baseline docs committed

---

## Phase 1 — Nx Workspace Bootstrap

**Goal:** Nx as task orchestrator; zero behaviour changes to Go code.

### Task 3 — Initialize Nx workspace at repo root
1. Create `package.json` (workspace runner only, no app deps):
   ```json
   { "name": "codegraph", "private": true, "devDependencies": { "nx": "latest" } }
   ```
2. Create `pnpm-workspace.yaml` listing tool paths
3. Create `nx.json` with `targetDefaults` for `build`, `test`, `lint`, `integration`, `smoke`
4. Register current Go apps as Nx projects via `project.json` files (pointing to existing Makefile targets as `run-commands`):
   - `apps/cli-go/project.json` -> `make build`, `make test`
   - `apps/mcp-server-go/project.json` -> `cd mcp-server && go build`, etc.
5. Verify: `nx run cli-go:build` and `nx run mcp-server-go:build` both succeed

**Phase 1 exit criteria:**
- `nx run cli-go:build` green
- `nx run mcp-server-go:build` green

---

## Phase 2 — Logical Re-layout (No Runtime Changes)

**Goal:** move source files into target structure; use shim re-exports to avoid big-bang breakage.

**Shim pattern:**
```go
// pkg/models/node.go  (shim — kept during migration)
package models
import newpkg "github.com/context-maximiser/code-graph/libs/core-models-go"
type Node = newpkg.Node
var NewNode = newpkg.NewNode
```
Remove shims only after all call sites updated.

### Task 4 — Create monorepo directory skeleton
- `mkdir -p` all target dirs listed above
- Add placeholder `README.md` in each directory
- Register stubs as Nx projects (even empty ones)

### Task 5 — Move libs
Order: models first (fewest dependents on it), then neo4j, then vector-client.

1. `pkg/models/` → `libs/core-models-go/`
2. `pkg/neo4j/` → `libs/neo4j-client-go/`
3. `pkg/search/vector_store.go` + `qdrant_vector_store.go` → `libs/vector-client-go/`

After each move: add shim in old location, run `make test`, verify green.

### Task 6 — Move services
1. `pkg/indexer/static/` → `services/indexing-go/static/`
2. `pkg/indexer/generated/` → `services/indexing-go/generated/`
3. `pkg/indexer/documents/` → `services/indexing-go/documents/`
4. `pkg/query/` → `services/retrieval-go/query/`
5. `pkg/search/` (remaining files) → `services/retrieval-go/search/`
6. `pkg/llm/` → `services/retrieval-go/llm/`

### Task 7 — Move apps
1. `cmd/codegraph/` → `apps/cli-go/`
   - Merge into root `go.mod` (drop separate module path if needed)
   - Update Nx project.json
2. `mcp-server/` → `apps/mcp-server-go/`
   - Decision: merge `mcp-server/go.mod` into root `go.mod` (single workspace module)
   - Update Nx project.json
3. Delete old `cmd/` and `mcp-server/` top-level dirs after verification

**Phase 2 exit criteria:**
- All unit + integration tests pass
- No feature regressions on CLI or MCP surface
- `nx run-many -t build --all` green

---

## Phase 3 — Three-Store Abstraction Hardening

**Goal:** all indexing/retrieval routes through typed interfaces, not direct driver calls.

### Task 8 — Define and enforce GraphStore interface
```go
// libs/core-models-go/store.go
type GraphStore interface {
    UpsertNode(ctx context.Context, node Node) error
    UpsertRelationship(ctx context.Context, rel Relationship) error
    QueryNodes(ctx context.Context, filter NodeFilter) ([]Node, error)
    QueryRelationships(ctx context.Context, filter RelFilter) ([]Relationship, error)
    GetWithOverlay(ctx context.Context, nodeKey, scope, scopeId string) (*Node, error)
    ApplyTombstone(ctx context.Context, nodeKey, scope, scopeId string) error
}
```
- Implement `GraphStore` in `libs/neo4j-client-go/`
- Add mock implementation for unit tests (no live Neo4j required)
- Refactor all `services/indexing-go` write paths to use `GraphStore`

### Task 9 — Define TextIndexStore + OpenSearch client
```go
// libs/text-index-client-go/store.go
type TextIndexStore interface {
    IndexDocument(ctx context.Context, nodeKey, content string, meta map[string]string) error
    Search(ctx context.Context, query string, opts SearchOpts) ([]TextResult, error)
    Delete(ctx context.Context, nodeKey string) error
}
```
- Implement OpenSearch client (`libs/text-index-client-go/opensearch.go`)
- Add OpenSearch to `docker-compose.yml`:
  ```yaml
  opensearch:
    image: opensearchproject/opensearch:2.13.0
    environment:
      - discovery.type=single-node
      - DISABLE_SECURITY_PLUGIN=true
    ports: ["9200:9200"]
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:9200/_cluster/health"]
  ```
- Wire `TextIndexStore` into indexing pipeline alongside `GraphStore` and `VectorStore`

### Task 10 — Validate end-to-end retrieval across all three stores
Write integration test:
1. Index a sample project → writes to Neo4j + Qdrant + OpenSearch
2. Run hybrid retrieval: ANN from Qdrant + BM25 from OpenSearch → graph expansion via Neo4j
3. Assert results contain expected symbols with correct `nodeKey`s

Write degraded-mode tests:
- Skip Qdrant → retrieval falls back to text + graph (partial results, no panic)
- Skip OpenSearch → retrieval falls back to vector + graph (partial results, no panic)

**Phase 3 exit criteria:**
- Integration test green with all three stores up
- Degraded mode tests green

---

## Phase 4 — Python Doc Intelligence Service

### Task 11 — Create apps/docs-intel-py
1. `apps/docs-intel-py/pyproject.toml` (uv-based)
2. FastAPI server exposing:
   ```
   POST /parse
   Body: { "source": "confluence|html|pdf", "content": "<raw>" }
   Response: { "nodeKey": "...", "chunks": [...], "metadata": {...} }
   ```
3. JSON schema contracts in `libs/protocols/json-schema/`
4. Go contract test: spin up the Python service, send sample payload, validate response schema
5. Add to `infra/docker/compose.platform.yml`
6. Nx project with targets: `install`, `serve`, `test`, `lint`

**Phase 4 exit criteria:**
- Document ingestion parity with existing Go path
- Contract tests green in CI

---

## Phase 5 — CI/CD and Developer Experience

### Task 12 — Replace CI scripts with Nx affected pipeline
1. `.github/workflows/ci.yml`:
   ```yaml
   - run: nx affected -t lint,test,build     # all PRs
   - run: nx affected -t integration          # protected branches
   - run: nx run-many -t smoke --all          # nightly
   ```
2. `nx.json` task dependency graph:
   - `apps/cli-go:build` depends on `services/indexing-go:build`, `services/retrieval-go:build`
   - `apps/mcp-server-go:test` depends on `libs/*:test`
   - `*:integration` depends on `infra/docker:up`
3. `infra/docker/compose.platform.yml`: neo4j + qdrant + opensearch + codegraph
4. `tools/scripts/bootstrap.sh`, `tools/scripts/smoke-test.sh`
5. Update `README.md` with new contributor quickstart

**Phase 5 exit criteria:**
- PR checks faster via `affected`
- New contributor setup: `./tools/scripts/bootstrap.sh` → working environment

---

## Key Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Monorepo manager | Nx | Polyglot, incremental, CI-friendly; Bazel overkill at current scale |
| mcp-server go.mod | Merge into root | Eliminates dual-module complexity; mcp-server already uses same deps |
| Text store | OpenSearch | BM25 retrieval, self-hosted, compatible with Elasticsearch clients |
| Python runtime | uv + FastAPI | Fast installs, modern Python tooling |
| TS MCP migration | Deferred (Phase 6) | Only if MCP tool surface changes rapidly |
| Shim pattern | Re-export wrappers | Keeps dependents working during incremental moves |

---

## Delivery Checklist

- [ ] Phase 0: invariant tests frozen, baselines captured
- [ ] Phase 1: `nx run-many -t build --all` green
- [ ] Phase 2: all source under `apps/`, `services/`, `libs/`; no `pkg/` or `cmd/` remaining
- [ ] Phase 3: three-store e2e integration test green; degraded-mode tests green
- [ ] Phase 4: Python docs-intel service with contract tests
- [ ] Phase 5: CI uses `nx affected`; developer quickstart updated
