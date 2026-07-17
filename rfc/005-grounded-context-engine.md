# RFC-005: The Grounded Context Engine — Target Architecture

| Field | Value |
|-------|-------|
| **Status** | Draft |
| **Created** | 2026-07-06 |
| **Authors** | @techsavvyash |
| **Supersedes** | Amends RFC-004 (re-admits docs + multi-language on a fixed foundation) |
| **Companion** | RFC-006 (execution plan) |

## 1. Vision

CodeGraph is a **queryable context engine grounded in code and docs** for a large org
with many repos/microservices:

- **Agents** query it over MCP for verified structure: callers, implementations,
  blast radius, cross-service flows.
- **Engineers** chat with it during debugging to trace information flow across
  microservices.
- **PMs** ask in business language ("what implements invoice reconciliation?") and
  get grounded answers: the code, its state, and a basis for effort estimates.
- **Everyone** can visualize the index: dependency maps, request flows, doc↔code
  coverage.

### 1.1 What we learned (2026 landscape, five-subsystem audit)

Three facts shape everything below:

1. **Snippet retrieval is commoditized.** Agentic grep won it (Anthropic removed
   Claude Code's embedding index after A/B tests; Sourcegraph/Cursor/Copilot converge
   on lexical + embeddings + agent loop). We do not compete there.
2. **Verified structure is the moat.** Precise implementation edges, cross-repo call
   graphs, and cross-service boundaries are what grep *cannot* do — and what nobody
   ships correctly today (stack-graphs archived Sept 2025; SCIP indexers don't emit
   structural-typing closure; no vendor does static cross-service stitching).
3. **Nobody has production semantic doc→function linking.** The art is: deterministic
   identifier mining (high precision) + LLM code summaries as the fuzzy matching
   medium (confidence-scored) — never unqualified semantic edges presented as facts.

Therefore: **CodeGraph's product is trustworthy edges.** Every design rule in this
document serves that.

### 1.2 Relationship to RFC-004

RFC-004's *mechanism* (8 composable MCP primitives + cypher escape hatch) is kept
verbatim — it is the right agent contract. RFC-004's *scope cut* (Go-only, code-only)
is amended: multi-language and docs return, but only **after** the foundation defects
that motivated the cut are fixed (Phases 0–2 of RFC-006), and in the redesigned form
of §6 and §7 — not by resurrecting the retired RFC-002 subsystem.

## 2. Non-negotiable invariants

These are the rules the audited codebase violated. All new and rewritten code MUST
satisfy them; CI enforces where possible.

| # | Invariant | Enforcement |
|---|-----------|-------------|
| I1 | **Idempotent writes.** Indexing the same input twice produces a byte-identical graph. All relationship writes are `MERGE` (batched via `UNWIND`); every (re)index of a scope is delete-diff or wipe-then-write within that scope. | Integration test: index fixture twice, assert node+edge counts equal. |
| I2 | **Identity is enforced by the database.** Uniqueness constraints on `(nodeKey, scopeId)` per label. No "the index enforces it" fictions. | Schema test asserts `SHOW CONSTRAINTS` non-empty per label. |
| I3 | **No silent failure.** Data-affecting errors aggregate into an `IndexReport` (counts of nodes/edges written, skipped, failed per phase); any failure ⇒ non-zero exit and the report says exactly what is missing from the graph. | `fmt.Printf("Warning...")` is banned by lint rule; errors flow through the report. |
| I4 | **Provenance on every edge.** Every relationship carries `source` (`scip` \| `typechecker` \| `treesitter` \| `contract` \| `runtime` \| `docmine` \| `llm`) and, for non-deterministic sources, `confidence` (0–1). Facts and inferences are never the same edge without a distinguishing property. | Model layer refuses to build a relationship without `source`. |
| I5 | **Every query is bounded.** Default transaction timeout (10s) on all reads; per-tool `timeout_ms` override; pagination or explicit truncation flags on every list-shaped result. | Client layer sets `WithTxTimeout` unconditionally. |
| I6 | **Every hot query is index-backed.** Labels are interpolated (from an allowlist), never passed as predicate parameters; name search goes through FULLTEXT indexes; no `toLower(...) CONTAINS` scans on hot paths. | `EXPLAIN`-based test: hot queries must not contain `AllNodesScan`. |
| I7 | **Identifiers are validated, values are parameterized.** Labels/rel-types/property names pass an allowlist chokepoint; all values (including `scopeId`) are driver parameters. No `fmt.Sprintf` of user input into query text. | Single `cypher.Ident()` helper; lint forbids `Sprintf` into query strings outside it. |
| I8 | **Deletion ships before addition.** A superseded subsystem is deleted in the same PR (or an immediately preceding one) as its replacement. No more `retiredTools` maps guarding compiled-in corpses. | Review discipline; RFC checklists carry explicit demolition items. |

## 3. System overview

```
                        ┌──────────────────────────────────────────────┐
                        │                 INGESTION                     │
  repos ──► scip-* ──►  │  1 SCIP parse        (facts)                  │
                        │  2 tree-sitter enrich (body ranges, chunks)   │
                        │  3 type-checker resolve (IMPLEMENTS closure)  │
                        │  4 boundary stitch   (proto / REST / OTel)    │
  Confluence/Jira ────► │  5 doc mine          (ticket IDs, summaries)  │
   (via Atlassian MCP)  └───────────────┬──────────────────────────────┘
                                        │ idempotent MERGE batches + IndexReport
                                        ▼
                        ┌──────────────────────────────────────────────┐
                        │        Neo4j property graph (one store)       │
                        │  facts + provenanced inferences, scoped        │
                        │  FULLTEXT + vector indexes (native, no        │
                        │  Qdrant/OpenSearch sidecar by default)        │
                        └───────────────┬──────────────────────────────┘
                                        │
                        ┌───────────────┴──────────────────────────────┐
                        │              QUERY (MCP server)               │
                        │  schema · find · expand · path · cypher ·     │
                        │  source · entry_points · flows · render       │
                        └───────┬───────────────┬───────────────┬──────┘
                                ▼               ▼               ▼
                          coding agents      chat-ui        dashboards
                          (Claude Code…)  (eng + PM chat)  (visualization)
```

One Go module. One graph store. One write path. One query surface.

## 4. Repository layout (single module)

The 26-module workspace (~65 `replace` directives, all pinned to fake versions) is
replaced by **one `go.mod`** at root. Nothing is published or independently
versioned, so multi-module buys zero isolation and costs go.mod/go.sum churn in N
places per cross-package import — and lets dead modules stay green.

```
codegraph/
├── go.mod                      # the only one (plus test fixtures')
├── cmd/
│   ├── codegraph/              # CLI (cobra commands split per file)
│   └── codegraph-mcp/          # MCP server (protocol loop only)
├── internal/
│   ├── model/                  # node/rel types, nodeKey, scopes, provenance
│   ├── graph/                  # THE Neo4j client (one), schema, migrations
│   ├── ingest/
│   │   ├── scip/               # parser (occurrences, relationships, EnclosingRange)
│   │   ├── treesitter/         # body ranges, chunk outlines, dirty-file detection
│   │   ├── resolve/            # per-language type-checker resolvers (RFC-001)
│   │   ├── boundary/           # proto + REST + OTel cross-service stitching
│   │   ├── docs/               # Atlassian ingestion, ticket mining, summaries
│   │   └── pipeline/           # orchestration, IndexReport, incremental state
│   ├── query/                  # primitive implementations, flows, entry points
│   ├── search/                 # fulltext + vector + RRF fusion
│   └── mcpserver/              # tool schemas, dispatch, render
├── web/chat-ui/                # SvelteKit app (binary path via env, not ../../bin)
├── rfc/                        # single RFC directory, unique numbering
└── test/                       # fixtures, harness, integration
```

Explicitly deleted (audit findings, zero consumers or superseded):
`libs/retrieval-go`, `libs/context-bundles-go`, `libs/neo4j-client-go`,
`libs/vector-client-go`, `libs/protocols`, `services/*` (both), the legacy AST
indexer + `index project` command, `apps/docs-intel-py`, retired MCP handlers,
`advanced.go` stubs, `llm-go`/`generation-go`/`verification-go`/`evals-go`
(RFC-002/003 relics; re-created narrowly if/when §7's summarizer needs them).
`intelligence-go` survives only as `contracts` + `provenance` folded into
`internal/model`.

## 5. Data model

### 5.1 Identity (kept, hardened)

- `nodeKey` scheme from `core-models-go/nodekey.go` is kept — deterministic,
  human-readable, service-disambiguated. Two fixes: `Variable`/`Reference` keys stop
  embedding line/col (they churn thousands of orphans per edit; use
  content-position-independent discriminators), and Symbol keys **normalize away the
  module version** for cross-repo joining (raw SCIP symbol retained as a property):
  repo A's reference to `mod@v1.2.3/Foo()` and repo B's definition of
  `mod@<local>/Foo()` MUST land on the same Symbol node.
- **Constraints, not conventions** (I2): composite uniqueness on `(nodeKey, scopeId)`
  per label. If Community edition rejects composite uniqueness constraints, fold
  `scopeId` into the key string and constrain the single property.
- **Scopes are the versioning story** (the `version` property is decorative today and
  stays informational). The main/PR-overlay + Tombstone design is kept and becomes
  the *only* mutation model: `main` scope per default branch, `pr-*` overlays with
  tombstones, resolved at query time. Re-index of a scope is
  wipe-or-diff *within that scope* — never unbounded graph traversal deletes.

### 5.2 Node types

Code (facts): `Service`, `File`, `Symbol`, `Function`, `Method`, `Class`,
`Interface`, `Module`. Boundaries: `APIRoute` (server side), `APICall` (client side —
replaces the URL-destroying `SDKCall`; one node **per call site**, keyed by
file+position, carrying `urlTemplate`, `method`, `framework`). Docs: `Document`,
`DocChunk`, `Ticket` (Jira issue nodes — new; the join hub for doc mining). `Feature`
returns only as a *view over Ticket/Document clusters*, not as a
markdown-header-extraction artifact.

Removed: `GeneratedDoc`, `GenerationDiagnostic` (RFC-003 relics), JSON-blob
properties (statements/citations become nodes/edges if ever needed).

### 5.3 Relationships (all provenanced per I4)

| Relationship | Source(s) | Notes |
|---|---|---|
| `CONTAINS`, `DEFINES`, `REFERENCES` | `scip` | facts; MERGE-batched |
| `CALLS` | `scip`+`treesitter` | body range from `EnclosingRange`, else tree-sitter — never declaration-order guessing |
| `IMPLEMENTS` | `scip` and `typechecker` | §6.2; the RFC-001 fix; `source` distinguishes indexer-emitted vs resolver-computed |
| `EXPOSES_API` | `scip`+framework detection | server routes |
| `CALLS_API` | `contract` | APICall→APIRoute, cross-service, `confidence` + `matchBasis` (`proto` \| `route-template` \| `runtime-confirmed`) |
| `DEPENDS_ON` | `scip` | import-based; kept (longest-prefix matcher survives) |
| `MENTIONS` | `docmine` (deterministic) or `llm` (fuzzy, confidence) | doc↔code; §7 |
| `TRACKS` | `docmine` | Ticket→commit/PR/symbol from identifier mining |

## 6. Ingestion pipeline

### 6.1 SCIP layer — consume what SCIP already provides

SCIP stays the substrate (actively maintained; every alternative is archived,
zombie, or Meta-shaped; the protocol supports everything we need). Parser fixes:

- **Read `Occurrence.EnclosingRange`** — real body ranges, today unread. This deletes
  the declaration-order inference hack and most of the Go-AST re-parsing crutch.
- **Keep all `SymbolInformation.relationships`** (implementation, reference,
  type-definition) including on `ExternalSymbols` — today reference/type-def are
  dropped and ExternalSymbols skipped.
- Imports come from SCIP occurrences, not per-language regexes over source text.
- `signature` means signature (from `SignatureDocumentation`); the SCIP identifier
  lives in its own field. One field stops doing three jobs.

### 6.2 Type-checker resolvers — the RFC-001 fix

No SCIP indexer emits structural-typing closure; nobody upstream will fix this
(GitHub archived stack-graphs rather than solve it heuristically). Per-language
resolvers run as a post-SCIP enrichment phase, using each language's authoritative
checker:

- **Go**: `go/types` — `types.Implements(T, iface)` over the package set already
  loaded for indexing. Emits `IMPLEMENTS` with `source: typechecker`.
- **TypeScript**: a small Node sidecar using the compiler API's
  `checker.isTypeAssignableTo` over declared classes vs interfaces/type aliases.
- **Python/Java**: later; protocol identical (resolver reads the graph's
  interface+class inventory for a service, emits edges).

The resolver protocol is: *input* = service scope + workspace path; *output* =
`IMPLEMENTS` edge batch. Resolvers are optional per language — the graph degrades
gracefully (edges missing, never wrong).

With this, `expand(IMPLEMENTS, in)` — the recipe the `schema` tool already
advertises — actually works, and polymorphic call traversal (CALLS→abstract method
→IMPLEMENTS fan-out, already implemented in the call-graph Cypher) has data to
stand on.

### 6.3 Cross-service boundary stitching

Today cross-service linking is structurally dead (intra-module filters; a query
matching an edge no writer creates; URL-substring joins). Replacement, in confidence
order:

1. **Proto/gRPC** (highest precision, first): the proto symbol is the shared
   identifier across client and server repos — SCIP indexes protos; stitch
   `APICall(grpc)` → `APIRoute(grpc)` on the fully-qualified RPC name. Deterministic;
   `confidence: 1.0`.
2. **REST route-template matching**: extract server route tables (framework
   annotations — this also fixes NestJS "no callers": the decorator produces a
   synthetic `APIRoute -EXPOSES_API-> handler` edge — and OpenAPI specs where
   present); extract client call sites per call site (not per framework-method);
   match `method + path-template` with parameter-aware comparison. Confidence scored
   by match specificity; dynamic URLs get low confidence or no edge — never a
   `url CONTAINS service.name` coincidence edge.
3. **OTel fusion** (differentiator, later): ingest service-graph spans keyed on the
   stabilized `code.function.name`/`code.file.path` semconv attributes to *confirm*
   static edges (`matchBasis: runtime-confirmed`) and surface edges static analysis
   missed. Runtime-confirmed static edges are something no vendor ships.

Cross-repo symbol joining works because of §5.1's version-normalized Symbol keys —
indexing repo A and repo B separately yields joined Symbols, DEPENDS_ON, and
CALLS_API edges instead of islands.

### 6.4 Incremental indexing

Copy the industry pattern (Cursor's Merkle sync; Copilot's local incremental store):

- **Dirty detection**: content-hash tree per service (file hashes actually stored —
  today hardcoded `""`). On re-index, diff hashes → changed file set.
- **Fast path**: tree-sitter re-parse of changed files updates File/Function nodes,
  body ranges, and chunk outlines immediately (sub-second).
- **Precise path**: full `scip-*` run scheduled lazily (SCIP indexers are batch tools;
  this is a substrate limit we schedule around, not fight); on completion, scope-diff
  write: MERGE changed, tombstone/delete removed — bounded to the service scope (I1),
  never an undirected variable-length `DETACH DELETE`.

### 6.5 Error policy

Every pipeline phase returns typed counts into an `IndexReport`; the CLI prints it
and exits non-zero on any data-affecting failure (I3). A failed Symbol batch aborts
the dependent edge phases loudly instead of silently writing an empty graph.

## 7. Docs subsystem (redesigned, not resurrected)

The retired RFC-002 stack (backtick-regex linking, markdown-header "features",
hardcoded confidence 0.5, three chunkers, Qdrant+OpenSearch tri-store) does not
return. The 2026-grounded design:

1. **Ingestion via Atlassian MCP / Teamwork Graph** where the org runs
   Confluence/Jira — not a bespoke crawler (ours silently truncated spaces at 100
   pages). A thin generic `docs source` interface keeps markdown-in-repo and other
   wikis pluggable. The one audited component worth keeping —
   `ChunkDocumentWithMeta` + SHA-256 incremental chunk sync — is the chunker.
2. **Deterministic mining first** (`source: docmine`, high precision): Jira keys in
   commit messages/branches/PR titles → `Ticket -TRACKS-> {commit, PR, File,
   Function}`; explicit code refs in docs (paths, fully-qualified names — validated
   against the graph before an edge is written, killing phantom links); ADR
   front-matter.
3. **Summarize-then-match second** (`source: llm`, confidence-scored): LLM-generated
   hierarchical summaries (service → module → file → exported symbol) stored on
   nodes and embedded in **Neo4j native vector indexes** (no Qdrant sidecar — native
   vector search shipped in Neo4j 2026.01; the tri-store's operational weight bought
   nothing). Doc chunks embed into the same space; `MENTIONS` edges materialize only
   above a threshold, always carrying confidence, always visually/verbally
   distinguished from facts in every surface (I4).
4. **The PM query path** is composition, not magic: PM question → fulltext+vector
   over Ticket/Document/summaries (`find`) → `TRACKS`/`MENTIONS` edges into code →
   `expand`/`flows` for implementation state. Each hop is inspectable; answers cite
   their edges.

## 8. Query layer

The RFC-004 surface is kept exactly (`schema`, `find`, `expand`, `path`, `cypher`,
`source`, `entry_points`, `flows`, plus `render`). The engine beneath is rewritten:

- **`find`**: validated-label interpolation (`MATCH (n:Function)`, allowlist from
  schema — never `$label IN labels(n)`), FULLTEXT index for `name_pattern`
  (Community supports it; the "Enterprise only" comment was false), relevance
  ordering via index score with exact-match boost; RRF fusion with the vector space
  from §7.3 when semantic search is requested. Keyset pagination (ordered
  `elementId` cursor), not `SKIP`.
- **`expand`**: BFS semantics via `apoc.path.subgraphNodes`/`expandConfig` with
  `NODE_GLOBAL` uniqueness (APOC is already deployed) — reachability without path
  enumeration. Work is bounded, not just output.
- **`path`**: `allShortestPaths` default; non-shortest mode capped tightly or
  dropped.
- **`flows`**: single-query traversal (APOC expand) replacing one-query-per-node
  recursion; label-qualified `nodeKey` lookups everywhere (label-less property match
  = AllNodesScan per recursion step — the actual cause of the "Neo4j times out"
  folklore); service filtering via the denormalized `serviceName` property, not
  `CONTAINS*1..3` existential subqueries.
- **Name-or-id addressing** (RFC-004 D1, decided but never shipped): every
  node-accepting input takes a qualified name; ambiguity returns candidates.
- **`schema`**: served from `apoc.meta.stats` with TTL cache, not a full
  relationship scan per call.
- **`cypher`**: keep the three-layer guard; add the promised `EXPLAIN`-plan
  inspection; default and max timeouts.
- **One search implementation.** The three parallel ones collapse into
  `internal/search` (RRF fusion core survives from search-go), used by `find`, the
  CLI, and chat-ui alike.

## 9. Surfaces

- **MCP server**: protocol loop in `cmd/codegraph-mcp`, tools in `internal/mcpserver`
  (one file per tool), Cypher in `internal/query` (RFC-004 Phase-5 for real). The
  9-tool surface is the contract for all agent consumers.
- **chat-ui**: kept as the human surface (eng debugging + PM questions). Fixes: a
  frontier-class default model (the current `gpt-4.1-nano` cannot compose
  find→expand→path chains and makes the engine look broken), Mermaid rendering of
  `format: mermaid` results, MCP server located via env/config not
  `../../bin` hardcode, conversation persistence.
- **Visualization**: `render` (Cytoscape HTML) for ad-hoc graphs; Mermaid in chat;
  service-dependency and flow views are `expand`/`flows` presets, not new
  subsystems.

## 10. Infrastructure

- **Store: Neo4j 5.x/2026.x Community, and only Neo4j.** The 2025 Kuzu collapse
  (archived; team acqui-hired) validated the boring incumbent; FalkorDB is SSPL;
  Memgraph meters RAM. Qdrant and OpenSearch leave docker-compose — native FULLTEXT
  + vector indexes cover §7/§8 at our scale, deleting two containers, the tri-store
  writer (which paged at `LIMIT 1000` with no loop and cross-contaminated services),
  and the embedding-at-index-time API-key requirement.
- The audited query timeouts were modeling defects (label-less scans, path
  enumeration), not engine limits — fix per §8 before ever revisiting the store. If
  the store is ever revisited, the exit is Postgres (recursive CTEs + pgvector; how
  Sourcegraph stores SCIP), not another graph DB. The `GraphStore` seam
  (`internal/graph`) is the ramp.
- **Ops honesty**: `db-reset` actually resets (`down -v`); healthcheck-gated startup
  (no `sleep 30`); heap/pagecache sized by profile; GDS optional (used at index time
  for centrality seeds; migrate off deprecated `gds.graph.project.cypher`).
- **Multi-tenancy**: out of scope, *stated honestly*. `scopeId`/`tenantId` properties
  are organization, not isolation — one `cypher` query crosses them. Single-org
  deployment per instance until a real RFC (separate databases or Enterprise). The
  README/CLAUDE.md must not imply otherwise.
- **Repo hygiene**: `bin/` gitignored and purged from tracking (`.git` is 568MB from
  binary churn — one-time history rewrite optional), no committed binaries as load-
  bearing artifacts, Makefile targets match reality, CLAUDE.md regenerated.

## 11. Non-goals

1. **Competing on snippet retrieval** — agents grep; we answer relationship questions.
2. **First-class semantic doc→function edges without confidence** — the research
   isn't there; we ship mined facts + scored suggestions.
3. **Embedded/local-graph-DB migration** — see §10.
4. **Owning parsers** — SCIP indexers + tree-sitter + official type checkers; we
   never hand-roll language semantics (the regex import extractor is the cautionary
   tale).
5. **Effort/timeline estimation as a feature** — the graph provides blast-radius
   facts (`expand` on IMPLEMENTS/CALLS/DEPENDS_ON); the agent on top turns them into
   estimates. We ground; we don't guess.

## 12. Success metrics

| Metric | Today | Target |
|---|---|---|
| Re-index idempotency (edge count drift after 2nd run) | ~2× per run | 0 |
| `IMPLEMENTS` edges for Go interfaces (vs `types.Implements` ground truth on this repo) | ~struct-embedding only | ≥95% recall |
| Cross-service: indexed repo pair (khaata FE+BE) traceable UI→API→handler chains | 0 of 5 (RFC-004 measurement) | ≥4 of 5 |
| Hot-path queries with `AllNodesScan` in `EXPLAIN` | all of find/flows/expand roots | 0 |
| p95 `find` on khaata-scale graph (~43k symbols) | full scan (seconds/timeout) | <200ms |
| Doc link precision (sampled `MENTIONS`/`TRACKS` audit) | n/a (retired) | ≥90% for `docmine`; `llm` edges always confidence-labeled |
| First-party LOC | ~63k | ~40k with strictly more capability |
