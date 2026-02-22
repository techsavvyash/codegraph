# Monorepo Refactor Gap Evaluation (Second Pass)

## Scope

This document captures the second-pass evaluation of the current refactor state against the monorepo target and three-store architecture goals.

## Findings (Ordered by Severity)

### 1) Critical: app cutover is incomplete

- `apps/mcp-server-go` source is disabled by build tag:
  - `apps/mcp-server-go/main.go` has `//go:build ignore`
- Nx `mcp-server-go:build` still compiles the legacy `mcp-server` directory:
  - `apps/mcp-server-go/project.json`
- `apps/cli-go` still imports legacy `pkg/*` paths:
  - `apps/cli-go/main.go`

Impact:

- The new `apps/*` structure is not yet the runtime source of truth.

### 2) Critical: three-store architecture not enforced in runtime paths

- Full-text retrieval still uses Neo4j fulltext calls directly:
  - `services/retrieval-go/search/fulltext_search.go`
- Vector retrieval still uses Neo4j vector calls directly:
  - `services/retrieval-go/search/vector_search.go`
- `libs/text-index-client-go` is currently not wired into runtime retrieval/indexing paths.
- Duplicate vector interfaces exist in two locations:
  - `libs/vector-client-go/vector_store.go`
  - `services/retrieval-go/search/vector_store.go`

Impact:

- The intended split (Graph: Neo4j, Vector: Qdrant, Text: OpenSearch) is only partially implemented.

### 3) High: tenant/repo invariants are missing in core models

- `BaseNode` includes `scope/scopeId` but not `tenantId/repo/repoId`:
  - `libs/core-models-go/node.go`
- `BaseRelationship` also lacks tenant/repo dimensions:
  - `libs/core-models-go/relationship.go`

Impact:

- Multi-tenant/repo isolation requirements are not yet encoded at the model layer.

### 4) High: phase completion criteria in execution plan are not yet satisfied

- `pkg/` and `cmd/` are still active and imported while plan requires full move.
- Plan reference:
  - `docs/16-monorepo-execution-plan.md`

Impact:

- Current state is transitional; drift risk remains until old paths are retired or fully shimmed.

### 5) Medium: Nx project graph is partial

- `nx.json` defines target defaults (`integration`, `smoke`, `lint`), but most projects only expose `build/test`.
- This weakens `nx affected` CI value.

### 6) Medium: Python sidecar is present but not CI-ready

- `docs-intel-py:test` fails when `pytest` is not preinstalled.
- `lint` target suppresses failures (`|| true`).

Impact:

- CI signal quality and local repeatability are reduced.

### 7) Medium: platform compose is partial

- `infra/docker/compose.platform.yml` includes Neo4j/Qdrant/OpenSearch.
- First-party app services are not yet declared in compose.

Impact:

- Local platform is close but not a full “one command up” environment for all app services.

### 8) Low: workspace hygiene is incomplete

- `.gitignore` does not cover common monorepo/Python artifacts:
  - `.nx/workspace-data`, `.venv`, `.pytest_cache`, etc.

Impact:

- Noise in working tree and higher chance of accidental artifact commits.

## What Is Solid

- Monorepo skeleton (`apps/`, `services/`, `libs/`, `infra/`, `tools/`) is in place.
- Nx project discovery and uncached build execution works.
- Scope/tombstone overlay logic appears carried into `services` code.
- Execution plan (`docs/16`) is detailed and actionable.

## Validation Summary

- `pnpm nx run-many -t build --all --skip-nx-cache`: passed.
- `pnpm nx run-many -t test --all --skip-nx-cache`: failed on Python env setup and MCP project packaging.
- `make test`: fails in current environment, and output indicates both legacy and new trees are still exercised.

## Missing Pieces to Reach “Refactor Complete”

1. Make `apps/mcp-server-go` and `apps/cli-go` the true source of build/runtime truth.
2. Remove runtime dependency on legacy `pkg/*` imports in moved apps/services.
3. Wire `libs/vector-client-go` and `libs/text-index-client-go` into production retrieval/indexing paths.
4. Add tenant/repo fields and constraints through models + schema + query filters.
5. Consolidate duplicate interfaces (single `VectorStore` contract).
6. Add per-project `integration/smoke/lint` Nx targets and enforce in CI.
7. Make Python targets self-provisioning and fail-fast on lint/test.
8. Finish workspace hygiene (`.gitignore`, generated artifacts, temp files).
