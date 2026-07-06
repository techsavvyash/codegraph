# RFC-003: Monorepo Refactor & Codebase Cleanup

> **Status note:** Superseded by RFC-005 before implementation. The multi-module
> layout this RFC proposes is replaced by the single-module `cmd/`+`internal/`
> collapse in RFC-006 Phase 0d. Kept for historical context only.

| Field | Value |
|-------|-------|
| **Status** | Superseded by RFC-005 |
| **Created** | 2026-03-09 |
| **Authors** | @techsavvyash |
| **Depends on** | RFC-002 (PageIndex Document Memory) |

## Problem

The codegraph repo has grown organically across many feature branches into a structure with several pain points:

### 1. Naming Inconsistency

Every Go package is suffixed with `-go` (`core-models-go`, `neo4j-go`, `retrieval-go`). This is redundant — they're Go modules in a Go workspace. The suffix adds noise to every import path and directory listing.

### 2. Duplicate / Overlapping Packages

| Package A | Package B | Overlap |
|-----------|-----------|---------|
| `libs/neo4j-go/` | `libs/neo4j-client-go/` | Both wrap the Neo4j driver. Unclear which to use. |
| `libs/retrieval-go/` | `services/retrieval-go/` | Same name, different roles. `libs/` has orchestrator + overlay logic, `services/` has query + LLM subpackages. |
| `libs/search-go/` | `libs/vector-client-go/` | `search-go` contains `qdrant_vector_store.go` and `vector_search.go`. `vector-client-go` also contains `qdrant_vector_store.go` and `vector_store.go`. |
| `libs/search-go/llm_service.go` | `libs/llm-go/` | Both provide LLM abstractions. |

### 3. Catch-All Packages

`libs/search-go/` is a 21-file grab bag containing: chunk linking, flow linking, feature linking, embedding services, vector search, hybrid search, fulltext search, LLM validation, code summarization, and semantic analysis. These are distinct concerns crammed into one package.

### 4. Stray Files at Root

| File | Issue |
|------|-------|
| `BUGFIX_COMPLETION_SUMMARY.md` | Session artifact, not docs |
| `CODE_REVIEW_CHECKLIST.md` | Session artifact |
| `T4_INTEGRATION_TEST.md` | Session artifact |
| `INDEX.md` | Duplicates README purpose |
| `priv-key.pem` | **Private key in repo** (gitignored now but file exists) |
| `cli` | Stray compiled binary at root |
| `apps/mcp-server-go/mcp-server-go` | Compiled binary committed |
| `bin/codegraph`, `bin/codegraph-mcp` | Compiled binaries committed |

### 5. No `packages/` Convention

The `libs/` + `apps/` + `services/` split creates ambiguity. `libs/retrieval-go/` vs `services/retrieval-go/` — which owns retrieval? The `services/` directory has only 2 entries and its purpose overlaps with `libs/`.

### 6. No Unified Task Runner

Nx is installed but barely configured — `nx.json` has target defaults but no project graph. `pnpm-workspace.yaml` only includes `tools/nx` and `apps/*`. Go packages aren't wired into Nx. Builds are driven by `Makefile` which doesn't know about the package dependency graph.

## Target State

### Directory Structure

```
codegraph/
├── packages/
│   │
│   │  ── Core ──────────────────────────────────────
│   ├── domain/                  # Node types, relationships, value objects
│   ├── graph/                   # Neo4j client, connection, query builder
│   ├── schema/                  # Neo4j schema (constraints, indexes, migrations)
│   ├── storage/                 # Object storage client (MinIO/S3 + filesystem)
│   ├── llm/                     # LLM provider abstraction (multi-provider)
│   │
│   │  ── Code Intelligence ─────────────────────────
│   ├── indexer/                  # SCIP + AST indexing pipelines
│   │   ├── static/              #   SCIP parser, call graph builders
│   │   ├── documents/           #   Document parser, chunk creation
│   │   └── pipeline/            #   Pipeline orchestration
│   ├── query/                   # LSP-like queries, flow spine traversal
│   ├── inference/               # Graph-based inference (flow seeds, scoring)
│   ├── intelligence/            # Contracts, provenance, identity, rollout
│   ├── generation/              # LLM-powered doc generation + verification
│   ├── verification/            # Verification policies for generated content
│   ├── gds/                     # Neo4j Graph Data Science wrappers
│   │
│   │  ── Memory (RFC-002) ──────────────────────────
│   ├── mem/
│   │   ├── structure/           #   Document structure extraction
│   │   ├── summarizer/          #   Bottom-up summary tree builder
│   │   └── navigator/           #   Summary tree navigation for retrieval
│   │
│   │  ── Retrieval ─────────────────────────────────
│   ├── retrieval/               # Unified retrieval orchestrator
│   │   ├── graph/               #   Graph-structural filtering
│   │   ├── text/                #   BM25/fulltext (OpenSearch client)
│   │   ├── vector/              #   Vector search (Qdrant client)
│   │   └── hybrid/              #   Multi-signal orchestration
│   │
│   │  ── Evaluation ────────────────────────────────
│   ├── evals/                   # Quality gates, ablation, seed evals
│   ├── benchmarks/              # Performance benchmarks, baselines
│   │
│   │  ── Apps ──────────────────────────────────────
│   ├── cli/                     # Unified CLI (all commands)
│   ├── mcp/                     # MCP server (all tools)
│   ├── daemon/                  # HTTP API + background workers (future)
│   └── ui/                      # Chat/exploration UI (TypeScript)
│
├── infra/
│   └── docker/                  # docker-compose files
│
├── docs/                        # Architecture docs, guides
├── rfc/                         # RFCs
├── scripts/                     # Dev/ops scripts
├── test/                        # Cross-package integration tests
│
├── go.work                      # Go workspace
├── go.mod                       # Root module
├── nx.json                      # Nx config (wired to all packages)
├── package.json                 # Workspace root
├── Makefile                     # Convenience targets (delegates to Nx)
├── CLAUDE.md                    # Updated for new structure
└── README.md
```

### Key Changes from Current State

| Current | Target | Action |
|---------|--------|--------|
| `libs/core-models-go/` | `packages/domain/` | Rename, drop `-go` suffix |
| `libs/neo4j-go/` + `libs/neo4j-client-go/` | `packages/graph/` | **Merge** into single package |
| `libs/schema-go/` | `packages/schema/` | Rename |
| `libs/llm-go/` | `packages/llm/` | Rename |
| `libs/indexer-go/` | `packages/indexer/` | Rename, retain subpackage structure |
| `libs/query-go/` | `packages/query/` | Rename |
| `libs/inference-go/` | `packages/inference/` | Rename |
| `libs/intelligence-go/` | `packages/intelligence/` | Rename |
| `libs/generation-go/` | `packages/generation/` | Rename |
| `libs/verification-go/` | `packages/verification/` | Rename |
| `libs/gds-go/` | `packages/gds/` | Rename |
| `libs/evals-go/` | `packages/evals/` | Rename |
| `libs/benchmarks-go/` | `packages/benchmarks/` | Rename |
| `libs/context-bundles-go/` | `packages/retrieval/` | Absorb into retrieval |
| `libs/search-go/` | **Split** | See breakdown below |
| `libs/vector-client-go/` | `packages/retrieval/vector/` | Merge into retrieval |
| `libs/text-index-client-go/` | `packages/retrieval/text/` | Merge into retrieval |
| `libs/retrieval-go/` | `packages/retrieval/` | Merge into retrieval root (orchestrator) |
| `services/retrieval-go/` | `packages/retrieval/` | Merge query/llm subpackages |
| `services/indexing-go/` | `packages/indexer/` | Merge into indexer |
| `libs/protocols/` | Evaluate | If empty/unused, remove |
| `apps/cli/` | `packages/cli/` | Move under packages |
| `apps/mcp-server-go/` | `packages/mcp/` | Rename, drop suffix |
| `apps/docs-intel-py/` | `packages/docs-intel/` | Rename (keeps Python) |
| `apps/chat-ui/` | `packages/ui/` | Rename |
| NEW | `packages/storage/` | MinIO/S3 + filesystem client |
| NEW | `packages/mem/` | RFC-002 memory packages |

### `search-go` Breakup

This is the messiest package. Here's where each file goes:

| File | Target Package | Rationale |
|------|---------------|-----------|
| `chunk_linker.go` | `packages/indexer/documents/` | Links chunks during indexing |
| `flow_linker.go` | `packages/indexer/documents/` | Links flows during indexing |
| `feature_linker.go` | `packages/indexer/documents/` | Links features during indexing |
| `intelligent_linker.go` | `packages/indexer/documents/` | Semantic linking during indexing |
| `fulltext_search.go` | `packages/retrieval/text/` | BM25 search |
| `vector_search.go` | `packages/retrieval/vector/` | Vector similarity search |
| `qdrant_vector_store.go` | `packages/retrieval/vector/` | Qdrant implementation |
| `hybrid_search.go` | `packages/retrieval/hybrid/` | Multi-signal orchestration |
| `embedding_service.go` | `packages/retrieval/vector/` | Embedding generation |
| `comment_embedding_service.go` | `packages/retrieval/vector/` | Comment embedding |
| `semantic_analyzer.go` | `packages/inference/` | Semantic analysis belongs with inference |
| `llm_service.go` | `packages/llm/` | LLM abstraction (merge with existing) |
| `llm_validator.go` | `packages/verification/` | Validation belongs with verification |
| `code_summarizer.go` | `packages/generation/` | LLM-powered summarization |

### Cleanup Tasks

**Delete**:
- `BUGFIX_COMPLETION_SUMMARY.md` — session artifact
- `CODE_REVIEW_CHECKLIST.md` — session artifact
- `T4_INTEGRATION_TEST.md` — session artifact
- `INDEX.md` — duplicates README
- `priv-key.pem` — private key (purge from git history)
- `cli` — stray binary at root
- `apps/mcp-server-go/mcp-server-go` — committed binary
- `bin/codegraph`, `bin/codegraph-mcp` — committed binaries

**Add to `.gitignore`**:
```
bin/
*.pem
cli
```

**Move**:
- `arch-docs/` → `docs/architecture/` (consolidate docs)
- `plugins/` → `infra/plugins/` (Neo4j GDS jar)

## Nx Wiring

Every package gets a `project.json`:

```json
{
  "name": "domain",
  "targets": {
    "build": { "command": "go build ./..." },
    "test": { "command": "go test ./..." },
    "lint": { "command": "golangci-lint run ./..." }
  },
  "implicitDependencies": []
}
```

For TypeScript packages (`ui`, `daemon` if added):

```json
{
  "name": "ui",
  "targets": {
    "build": { "command": "bun run build" },
    "test": { "command": "bun test" },
    "dev": { "command": "bun run dev" }
  }
}
```

`go.work` is updated to point to all `packages/*/` with Go modules.

Nx project graph infers dependencies from Go imports + `project.json` implicit dependencies. This gives us:
- `nx affected -t test` — only test what changed
- `nx graph` — visualize package dependencies
- `nx run mcp:build` — build with upstream deps

## Execution Plan

### Wave 0: Cleanup (no structural changes)

- [ ] Delete stray files (session artifacts, binaries)
- [ ] Purge `priv-key.pem` from git history (`git filter-repo`)
- [ ] Update `.gitignore` (binaries, pem files)
- [ ] Remove `libs/protocols/` if empty/unused

### Wave 1: Rename `libs/` → `packages/`, drop `-go` suffix

Mechanical renames. No code changes, just directory moves + `go.mod` module path updates + `go.work` updates.

- [ ] `libs/core-models-go/` → `packages/domain/`
- [ ] `libs/schema-go/` → `packages/schema/`
- [ ] `libs/llm-go/` → `packages/llm/`
- [ ] `libs/indexer-go/` → `packages/indexer/`
- [ ] `libs/query-go/` → `packages/query/`
- [ ] `libs/inference-go/` → `packages/inference/`
- [ ] `libs/intelligence-go/` → `packages/intelligence/`
- [ ] `libs/generation-go/` → `packages/generation/`
- [ ] `libs/verification-go/` → `packages/verification/`
- [ ] `libs/gds-go/` → `packages/gds/`
- [ ] `libs/evals-go/` → `packages/evals/`
- [ ] `libs/benchmarks-go/` → `packages/benchmarks/`
- [ ] Move `apps/cli/` → `packages/cli/`
- [ ] Move `apps/mcp-server-go/` → `packages/mcp/`
- [ ] Move `apps/docs-intel-py/` → `packages/docs-intel/`
- [ ] Move `apps/chat-ui/` → `packages/ui/`
- [ ] Update all import paths (automated with `sed` on module paths)
- [ ] Update `go.work` to reference new paths
- [ ] Move `arch-docs/` → `docs/architecture/`
- [ ] Move `plugins/` → `infra/plugins/`

### Wave 2: Merge overlapping packages

- [ ] Merge `libs/neo4j-go/` + `libs/neo4j-client-go/` → `packages/graph/`
- [ ] Split `libs/search-go/` into `packages/retrieval/`, `packages/indexer/documents/`, `packages/llm/`, etc. (per table above)
- [ ] Merge `libs/vector-client-go/` → `packages/retrieval/vector/`
- [ ] Merge `libs/text-index-client-go/` → `packages/retrieval/text/`
- [ ] Merge `libs/retrieval-go/` → `packages/retrieval/` (orchestrator)
- [ ] Merge `services/retrieval-go/` → `packages/retrieval/` (query/llm subpackages)
- [ ] Merge `services/indexing-go/` → `packages/indexer/`
- [ ] Absorb `libs/context-bundles-go/` → `packages/retrieval/`
- [ ] Remove `libs/`, `apps/`, `services/` directories

### Wave 3: Nx wiring + tooling

- [ ] Add `project.json` to every package
- [ ] Update `nx.json` with proper target defaults and caching
- [ ] Update `pnpm-workspace.yaml` to include `packages/*`
- [ ] Update `Makefile` to delegate to Nx where possible
- [ ] Update `CLAUDE.md` for new structure
- [ ] Update `README.md`

### Wave 4: New packages (RFC-002)

- [ ] Add `packages/storage/` — MinIO/S3 + filesystem client
- [ ] Add `packages/mem/structure/` — document structure extractor
- [ ] Add `packages/mem/summarizer/` — summary tree builder
- [ ] Add `packages/mem/navigator/` — summary tree navigation
- [ ] Extend `packages/domain/` — `Summary` node, new relationships
- [ ] Extend `packages/schema/` — summary indexes
- [ ] Extend `packages/indexer/documents/` — summary tree integration
- [ ] Extend `packages/mcp/` — `mem_*` tools
- [ ] Extend `packages/cli/` — `mem` subcommands

## Risk Mitigation

1. **Import path breakage**: Every rename changes Go module paths. Automated `sed` replacements across all `*.go` files + `go.mod` replace directives reduce risk. Run `go build ./...` after each wave.

2. **Merge conflicts with other branches**: The many existing feature branches will conflict with wave 1-2 renames. **Recommendation**: merge or close stale branches before starting. The refactor is a clean break.

3. **Binary size / build time**: No impact — same code, different directories.

4. **CI/CD**: Update `.github/workflows/` to use new paths after wave 1. Nx caching should improve CI speed.

## Open Questions

1. **Go module paths**: Should the module root remain `github.com/context-maximiser/code-graph` or change to match the new name (e.g., `github.com/techsavvyash/codegraph`)? The GitHub remote is already `techsavvyash/codegraph`.

2. **Single `go.mod` vs per-package**: Currently each lib has its own `go.mod` stitched together by `go.work`. We could consolidate into a single root `go.mod` for simplicity, or keep per-package modules for independent versioning. **Recommendation**: single root `go.mod` — we're not publishing these as independent packages, and per-module `go.work` is maintenance overhead.

3. **`services/` elimination**: Merging `services/` into `packages/` means no distinction between "library" and "service". This is fine if all deployable units are in `packages/cli/`, `packages/mcp/`, `packages/daemon/`. The package type (library vs app) is evident from whether it has a `main.go`.
