# Monorepo Architecture

This document describes the two-phase plan for making CodeGraph a proper polyglot monorepo.

Option A (cosmetic, completed on `refactor/monorepo-option-a`) removes stale duplicates and
reshapes the directory layout. Option B (future) gives each Go application its own module so
dependencies are truly isolated.

---

## Current layout (post Option A)

```
codegraph/
├── apps/                        # Deployable applications
│   ├── cli/                     # Go CLI  (was cmd/codegraph/)
│   ├── mcp-server-go/           # Go MCP server
│   ├── chat-ui/                 # SvelteKit web UI
│   └── docs-intel-py/           # Python FastAPI sidecar
│
├── libs/                        # Shared packages (all under root go.mod)
│   ├── core-models-go/          # Canonical Go model types
│   ├── neo4j-go/                # Neo4j QueryBuilder + Client  (was pkg/neo4j/)
│   ├── neo4j-client-go/         # Neo4j GraphStore interface   (for services/)
│   ├── indexer-go/              # AST + SCIP indexing pipeline (was pkg/indexer/)
│   ├── query-go/                # LSP-style query layer        (was pkg/query/)
│   ├── schema-go/               # Schema management            (was pkg/schema/)
│   ├── search-go/               # Full-text + vector search    (was pkg/search/)
│   ├── llm-go/                  # LLM provider adapters        (was pkg/llm/)
│   ├── benchmarks-go/           # Benchmarks                   (was pkg/benchmarks/)
│   ├── text-index-client-go/    # OpenSearch integration
│   ├── vector-client-go/        # Qdrant integration
│   └── protocols/               # JSON Schema definitions
│
├── services/                    # Standalone services (use libs/, not imported back)
│   ├── indexing-go/             # Generated-doc + schema service
│   └── retrieval-go/            # LLM + query service
│
├── infra/                       # Docker / platform configs
├── scripts/                     # Shell scripts (install, smoke-test, uninstall)
└── docs/                        # Documentation (you are here)
```

All Go code shares a single root `go.mod`
(`github.com/context-maximiser/code-graph`).  This keeps tooling simple but
means all Go packages are rebuilt together and there is no hard boundary
preventing a library from accidentally importing an app.

---

## Option B — proper module isolation

### Goal

Each application (`apps/*`) and each shared library (`libs/*`) becomes an
independent Go module with its own `go.mod`.  Consumers declare the library as
a `require` dependency and use a `replace` directive pointing at the local path
during development; in CI / production the library version is resolved from the
VCS tag.

### Final layout (post Option B)

```
apps/cli/
  go.mod  → module github.com/context-maximiser/code-graph/apps/cli
            require github.com/context-maximiser/code-graph/libs/indexer-go v0.0.0
            replace github.com/context-maximiser/code-graph/libs/indexer-go => ../../libs/indexer-go
            ...

libs/indexer-go/
  go.mod  → module github.com/context-maximiser/code-graph/libs/indexer-go
```

### Benefits

| Concern | Single go.mod (Option A) | Per-module (Option B) |
|---|---|---|
| Accidental cross-app imports | Possible | Compile error |
| Independent versioning | No | Yes (semver tags per lib) |
| Incremental CI (only affected modules) | Nx best-effort | Native go test ./... scoping |
| go.sum surface area | One large file | Small per module |
| Tooling complexity | Low | Medium |

### Migration steps

1. **Stabilise the API of each lib** — add a `CHANGELOG.md`, tag `v0.1.0`.
2. **Extract `libs/core-models-go`** first (lowest dependency count):
   ```
   cd libs/core-models-go
   go mod init github.com/context-maximiser/code-graph/libs/core-models-go
   go mod tidy
   ```
3. **Extract dependents bottom-up** — `neo4j-go`, `neo4j-client-go`, then
   `schema-go`, `indexer-go`, `query-go`, `search-go`, `llm-go`.
4. **Wire replace directives** in each consumer `go.mod`:
   ```
   replace github.com/context-maximiser/code-graph/libs/core-models-go => ../../libs/core-models-go
   ```
5. **Extract apps** — `apps/cli`, `apps/mcp-server-go` last (highest
   dependency count).
6. **Root go.mod** becomes a thin workspace coordinator (or is removed).
7. **Update Nx** — add `go mod tidy` to `targetDefaults` so affected-graph
   works correctly across module boundaries.
8. **CI** — each job calls `go test ./...` inside the module, scoped by Nx
   affected to avoid rebuilding unaffected modules.

### Known risks / open questions

- `go work` (Go 1.18+) is an alternative to per-`go.mod` `replace` directives.
  Evaluate whether a `go.work` file at the root is cleaner than individual
  `replace` blocks.
- `services/indexing-go` and `services/retrieval-go` both depend on
  `libs/core-models-go` and `libs/neo4j-client-go`.  They should be extracted
  as apps (or services) with their own `go.mod` as part of this phase.
- The two parallel Neo4j packages (`libs/neo4j-go` and `libs/neo4j-client-go`)
  should be consolidated into a single library before Option B begins.

---

## Decision log

| Date | Decision | Rationale |
|---|---|---|
| 2026-02-22 | Option A merged | Remove dead code, establish correct directory layout with single go.mod |
| TBD | Option B begins | After API surfaces are stable and teams need independent versioning |
