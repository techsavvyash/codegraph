# RFC-004: MCP Primitives — Composable Tools for Code Graph Intelligence

| Field | Value |
|-------|-------|
| **Status** | Accepted (2026-05-10) |
| **Created** | 2026-05-10 |
| **Authors** | @techsavvyash |
| **Supersedes** | (none) |
| **Obsoletes** | RFC-002 (PageIndex Document Memory) — see "Scope cuts" |

## Problem

The MCP server (`apps/mcp-server-go/main.go`) currently exposes 21 tools. An audit reveals three structural problems:

1. **Macro-tool sprawl.** Half the tools are thin wrappers around a single Cypher query (`service_dependencies`, `service_api_endpoints`, `list_services`, etc.). Every novel question an agent might ask requires a new tool, because the existing tools are pre-baked answers rather than primitives.
2. **Doc-linking debt.** Seven tools (`index_documents`, `search_docs`, `search_by_comment`, `link_docs_to_code`, `intelligent_link`, `hybrid_search`, `vector_search`) implement a document-to-code linking subsystem that no longer fits the product direction.
3. **Multi-language ambiguity.** Tools were designed for polyglot use (TS, Python, Go, Java). Measurement on `apps/chat-ui` (see "Background") shows TS coverage is severely degraded — `.svelte` files invisible, arrow-function-as-const definitions missed, ~55% precision on call edges. The product cannot honestly claim multi-language support today.

The fix is to recommit to **Go as the primary target** and redesign the MCP surface around composable primitives that capable LLM agents can compose into any question, including ones we did not anticipate.

## Background

### Measurement that motivated the scope cut

On 2026-05-10, we measured indexing coverage of `apps/chat-ui` (15 source files: 8 `.ts` + 7 `.svelte`):

| Dimension | On disk | In graph | Coverage |
|---|---|---|---|
| Source files | 15 | 9 (.ts only) | 60% files / **0% UI** |
| `.svelte` files | 7 | 0 | scip-typescript ignores them |
| API entry points | 1 (`POST` arrow-const) | 0 | indexer skips arrow-const definitions |
| CALLS edges | — | 9 total, ~4 false positives | precision ~55% |
| End-to-end UI→backend chains traceable | — | 0 of 5 attempted | — |

This is not a small gap that can be patched. Multi-language indexing is an RFC-shaped problem on its own (deferred — see "Non-goals"), and shipping a Go-focused product first is the higher-value move.

### What the audit found about the existing MCP surface

| Category | Count | Examples |
|---|---|---|
| Solid graph tools | 4 | `trace_call_graph`, `generate_flows`, `get_entry_points`, `service_architecture` |
| Thin Cypher wrappers (collapsible) | 6 | `list_services`, `service_dependencies`, `service_api_endpoints`, `service_api_calls`, `cross_service_calls`, `analyze_function` |
| Search trio (overlapping) | 3 | `search`, `vector_search`, `hybrid_search` |
| Doc-linking (out of scope) | 5 | `index_documents`, `search_docs`, `search_by_comment`, `link_docs_to_code`, `intelligent_link` |
| Source/reference retrieval | 3 | `get_source`, `find_references`, `analyze_function` |
| **Built but unexposed** | — | `LSPService.FindImplementations()` — the Go interface→impl killer feature |

## Proposal

Replace the 21-tool surface with **8 composable tools** organized in three layers.

### Layer 1 — Graph atoms (5 tools)

| Tool | Inputs | Output | Replaces |
|---|---|---|---|
| `schema` | (none) | node labels with property schemas; relationship types with valid (from-label, to-label) endpoints | NEW |
| `find` | `label?`, `name_pattern?`, `service?`, `filter?`, `limit`, `cursor?` | paginated node list | `search`, `list_services`, `analyze_function` (partial) |
| `expand` | `node_id`, `rel_types[]`, `direction` (in/out/both), `depth`, `limit`, `format` | nodes + edges (text/json/mermaid) | `trace_call_graph`, `find_references`, `service_dependencies`, `service_api_endpoints`, `service_api_calls`, `analyze_function` (partial) |
| `path` | `from_id`, `to_id`, `rel_types[]`, `max_hops`, `shortest=bool`, `format` | path(s) between nodes | `cross_service_calls` (partial) |
| `cypher` | `query`, `params?`, `timeout_ms`, `row_limit` | rows from a **read-only** Cypher query | NEW (escape hatch) |

`schema` is non-negotiable: the entire "agent composes primitives" model collapses if the agent has to guess relationship type names. `schema` returns a compact, canonical description of the graph contract.

### Layer 2 — Source retrieval (1 tool)

| Tool | Inputs | Output | Replaces |
|---|---|---|---|
| `source` | `symbol_name` or `node_id`, `include_signature?` | source code text + file path + line range | `get_source` |

Not a graph operation; kept because every analysis needs to ground in actual source text.

### Layer 3 — Algorithmic tools (2 tools)

These earn their keep because they implement non-trivial algorithms, not graph traversals an agent could write itself.

| Tool | Why it earns L3 | Replaces |
|---|---|---|
| `entry_points` | 4-tier classification: API-exposed, no-callers-but-implements-iface, exported-roots, high-centrality. Heuristic logic, not graph traversal. | `get_entry_points` |
| `flows` | 840 LOC of multi-strategy seed detection, priority scoring, step dedup, traversal budget enforcement, and Flow-node materialization. | `generate_flows` |

### Mermaid as a per-tool output format

Every tool that returns graph-shaped data accepts `format: "text" | "json" | "mermaid"` (default `text` for human, `json` for agents). The `mermaid` mode emits a Mermaid `graph TD` block ready for inline rendering in chat-ui or any Mermaid-aware client. This unifies the agent and human surfaces without a separate visualization service.

### Killed outright (12 tools)

| Tool | Reason |
|---|---|
| `vector_search`, `hybrid_search`, `search_by_comment` | Doc-linking + vector primitives out of scope |
| `index_documents`, `search_docs`, `link_docs_to_code`, `intelligent_link` | Doc-linking out of scope |
| `search` | Subsumed by `find` |
| `list_services` | Subsumed by `find(label=Service)` |
| `service_dependencies`, `service_api_endpoints`, `service_api_calls` | Subsumed by `expand` once `schema` reveals rel-type vocabulary |
| `cross_service_calls` | Subsumed by `path` with `rel_types=[CALLS_API, CALLS, CONTAINS]` |
| `service_architecture` | Composition of `find(Service)` + 3-4 `expand` calls; agent can build it |
| `analyze_function` | Composition of `find` + `expand([CALLS], in/out)` + `source` |
| `find_references` | Composition: `expand(_, [REFERENCES], in)` |
| `trace_call_graph` | Composition: `expand(_, [CALLS], in/out, depth)` |
| `get_source` | Renamed to `source` |

### Schema cuts that fall out of this RFC

- Drop `Document` and `Feature` node types and their relationships (`HAS_FEATURE`, `LINKS_TO_CODE`, `MENTIONS_SYMBOL`).
- Drop the embedding service as a hard dependency. Embeddings are not part of the code-graph primitive surface.
- RFC-002 (PageIndex Document Memory) becomes obsolete and should be marked **Withdrawn**.

## Non-goals

- **Multi-language indexing improvements.** Deferred to a future RFC (provisionally B5: Svelte/Vue/JSX support, TS arrow-const definitions, generic call-graph false-positive rate). The product ships Go-first; TS/Python remain best-effort with no coverage guarantees.
- **Mutation tools.** All MCP tools in this RFC are read-only. Index management stays in the CLI.
- **Authentication / multi-tenant.** Out of scope.

## Design decisions (resolved 2026-05-10)

### D1. Node addressing — **decided**

Every node-accepting input accepts **either** a qualified name (`pkg.Type.Method`) **or** an opaque `node_id` returned by a prior `find`/`expand` call. The server resolves names; ambiguity returns an error with disambiguation candidates. Agents start with names; tools return ids that can be reused.

### D2. The `cypher` tool — **decided: ship it**

Read-only enforced server-side via two layers: keyword regex pre-check (CREATE, MERGE, DELETE, SET, REMOVE, DROP, LOAD CSV, CALL with sub-query writes) **and** `EXPLAIN` plan inspection rejecting any plan containing update operators. Hard caps: `timeout_ms ≤ 5000`, `row_limit ≤ 1000`. Tool description marks it as advanced/escape-hatch — agents should prefer `find`/`expand`/`path` first.

### D3. Find and expand stay separate — **decided**

The conceptual split (filter nodes vs. traverse from a known node) is worth the second tool. A unified `query` becomes a blob of conditional options that's harder for agents to use correctly than two narrow tools.

## Rollout plan

| Phase | Work | Dependency |
|---|---|---|
| 1 | Implement `schema`, `find`, `expand`, `source`. Wire chat-ui to render Mermaid blocks. | — |
| 2 | Implement `path`, `cypher`. Validate by porting the existing `trace_call_graph` and `cross_service_calls` use cases to compositions. | Phase 1 |
| 3 | Migrate `entry_points` and `flows` to the new tool naming. Drop `format` parameter inconsistencies. | Phase 1 |
| 4 | Delete the 12 retired tools. Drop `Document`/`Feature` schema. Mark RFC-002 Withdrawn. | Phases 1-3 |
| 5 | Refactor remaining raw Cypher out of MCP handlers into `libs/query-go` builders. | Phase 4 |

Backwards compatibility: not a goal. The MCP server is an internal tool with no external consumers we need to preserve.

## Success metrics

1. **Tool count: 21 → 8.** Verified by handler registration count.
2. **No raw Cypher in MCP handlers** for the 8 tools (Phase 5).
3. **End-to-end demo on this repo:** an agent answers each of these without us adding tools — "what's the blast radius of `SCIPIndexer.IndexProject`", "find all implementations of `neo4j.Client`", "find dead functions in `libs/indexer-go/static`", "show the call chain from a CLI command to Neo4j writes". Each renders as a Mermaid diagram in chat-ui.
4. **chat-ui renders Mermaid inline** for any tool returning `format=mermaid`.

## Alternatives considered

- **Keep the 21-tool surface, add visualization.** Rejected: tool sprawl is the root problem; adding visualization on top doesn't address composability.
- **Single `cypher` tool only.** Rejected: makes every agent prompt depend on schema knowledge, no ergonomic surface for common questions.
- **GraphQL-style query primitive.** Rejected: blob-of-options anti-pattern; harder for agents to discover valid combinations than separate `find`/`expand`/`path`.
- **Defer the cleanup, just add `find_implementations` and Mermaid.** Rejected: the redundancy and doc-linking debt make every future change harder. Better to lance it now while the surface is still small.
