# RFC-012: CodeGraph Studio — Explorer & Chat UI

- **Status**: Draft (requirements + design phase)
- **Author**: Claude (with Yash)
- **Date**: 2026-07-17
- **Supersedes**: `web/chat-ui` (the SvelteKit chat prototype is replaced wholesale, not extended)
- **Depends on**: RFC-004 (MCP primitives), RFC-010 (structure extraction), RFC-011 (doc-code linking)

## 1. Motivation

The graph is now genuinely useful — SCIP-precise code structure, tree-sitter body
ranges, flow spines, entry-point tiers, and (RFC-011) judge-confirmed doc↔code
links that answer business-language questions. But the only surfaces are an MCP
server built for agents and a chat prototype that renders text. Humans exploring
an indexed system need what a text transcript cannot give them: *seeing* the
neighborhood of a node, walking a call path hop by hop, and moving fluidly
between a doc section and the code that implements it.

CodeGraph Studio is one web application with two entangled modes:

- **Explore** — direct manipulation: search, an interactive graph canvas,
  inspectors, flows, docs.
- **Ask** — a chat agent whose answers are wired into the explorer: every
  citation is a live reference that focuses the canvas.

Neither mode is subordinate. Chat without a canvas produces walls of text;
a canvas without chat makes users write Cypher.

## 2. Users

1. **Developers** onboarding to or auditing an indexed codebase (primary).
2. **Non-engineers** (PM/support) asking business-language questions that
   resolve through the RFC-011 docs plane — verified to work end-to-end.
3. **Agent supervisors** — humans checking what the MCP tools/agents actually
   see in the graph (debugging index quality, provenance, coverage).

## 3. Functional requirements

Grounded 1:1 in the existing MCP tool surface — the UI invents no new query
semantics, only composition.

### R1 — Omnibox search (`codegraph_find`)
- Single search field, always reachable (`/` or `⌘K`).
- Three modes: **lexical** (name/path), **structural** (label + property
  filters), **semantic** (needs `CODEGRAPH_EMBED_*`; degrade to hidden when
  unset — the UI must show *why* semantic is unavailable).
- Filters: node label(s), service, scope. Results grouped by label, showing
  service, file path, and signature. Enter opens in explorer; each result also
  offers "add to canvas".

### R2 — Graph canvas (`codegraph_expand`, `codegraph_path`)
- Force/level layout of the current working set (not the whole graph — the
  canvas only ever holds explicitly loaded nodes).
- Click node → inspector. Double-click / expand affordance → choose relationship
  type(s) and direction, fan out via `expand` (respecting its limits).
- Pin any two nodes → `path` (shortest by default; "all paths" toggle capped as
  the tool caps).
- **Provenance is first-class**: structural edges (CALLS, CONTAINS, DEFINES,
  HAS_CHUNK…) render solid/neutral; inferred edges (MENTIONS) render dashed
  with a strategy + confidence badge (docmine 0.70–0.95 vs semlink ≤0.60 bands
  visually distinct). Node color = label category; `rangeSource` shown in
  inspector.
- Canvas operations: remove node, collapse expansion, clear, fit, export
  (PNG/mermaid via `codegraph_render`).

### R3 — Inspector (`codegraph_source`, `codegraph_expand`)
- Right-side pane for the selected node: properties (name, service, file:line,
  signature, provenance stamps), source code with syntax highlighting (doc
  chunks render as markdown), and the edge list grouped by relationship type
  with counts — each group expandable onto the canvas.

### R4 — Flows (`codegraph_entry_points`, `codegraph_flows`)
- Entry-point list, grouped by tier (1 API-exposed → 4 high-centrality), with
  tier explanations.
- Selecting an entry point traces its flow spine; rendered as a stepped
  vertical sequence (depth-indented), each step clickable into the inspector,
  whole flow loadable onto the canvas.

### R5 — Docs plane (RFC-011)
- Document tree per service → chunk list (headingPath) → chunk content rendered
  as markdown.
- Both directions, symmetric: from a chunk, "code this section mentions"
  (MENTIONS out); from a code node, "documented in" (MENTIONS in). Confidence
  band and strategy always visible; nothing inferred is presented as ground
  truth.

### R6 — Chat dock
- Dockable panel (bottom drawer default, right-dock option), streaming, with
  visible tool activity (which MCP tool, arguments summary, duration).
- **Citations are live**: every node reference in an answer is a chip; click →
  focus/load on canvas + open inspector. "Pin all" per answer.
- Chat context is scope-aware: the active service/scope filter travels with
  every tool call the agent makes.

### R7 — Dashboard (`codegraph_schema`, `codegraph_cypher`)
- Per-service cards: node counts by label, edge counts by type, last-indexed
  time, docs coverage (chunks, MENTIONS edges by band), semantic index state
  (embedding model, dimensions, stamped threshold).
- Doubles as index-health debugging: services with zero flows, dangling scopes,
  duplicate service names get flagged.

### R8 — Cypher console (`codegraph_cypher`)
- Read-only editor with parameter support, schema hints from
  `codegraph_schema`, results as a virtualized table, and — when the result
  shape contains nodes/relationships — "send to canvas".
- Surfaces the tool's own guardrails verbatim (EXPLAIN warnings, row caps,
  timeouts) instead of hiding them.

### R9 — Cross-cutting
- **Scoping**: service + scope selector in the top bar; every query is scoped
  by default (the known large-graph timeout guard). "All services" is an
  explicit, warned choice.
- **Deep links**: every view + selection is a URL (`/g/<scope>/<node_id>` etc.);
  refresh restores state.
- **Keyboard-first**: `⌘K` omnibox, `e` expand, `p` pin-for-path, `i`
  inspector, `c` chat toggle.
- **Failure honesty**: query timeouts, row caps, and truncation banners are
  explicit; silent truncation is a bug.

## 4. Architecture

- **Frontend**: SvelteKit (Svelte 5), TypeScript. Graph rendering via
  Cytoscape.js (mature, handles incremental element addition and custom edge
  styling; force + dagre layouts). Code highlighting via Shiki. Markdown via
  marked + DOMPurify.
- **Backend bridge**: SvelteKit server routes wrap a pooled stdio client to
  `bin/codegraph-mcp` — one long-lived child process, JSON-RPC multiplexed
  (the chat prototype's pattern, promoted to a proper connection manager with
  restart-on-crash and request timeouts). Chat streams over SSE.
- No new Go services, no new stores. The MCP server remains the single
  authority on query semantics; if the UI needs something the tools can't
  express, the fix is a tool change (per RFC-004), not a bypass.
- Lives at `web/studio/`; `web/chat-ui/` is deleted when Studio reaches parity
  (chat + citations).

## 5. Non-functional requirements

- Canvas remains interactive at 500 rendered nodes; expansion fan-outs are
  limited + paginated rather than dumped.
- Every MCP round-trip surfaced in UI within 150ms of arrival; skeletons, not
  spinners, for panes.
- All lists (results, edges, table rows) virtualized.
- Unit tests (vitest) for stores/URL-state/bridge protocol; Playwright e2e for
  the five core journeys (search→canvas, expand→path, doc→code, chat→citation
  →canvas, cypher→canvas). Proper assertions per repo rules.

## 6. Design system (this RFC's deliverable #2)

Designed fresh in Claude Design (claude.ai/design), project **CodeGraph
Studio**; the bundle is versioned in-repo at `design/studio/`.

- **Direction**: light, professional developer-tool aesthetic (per decision
  2026-07-17): paper-white surfaces, ink text, single blue accent, IBM Plex
  Sans/Mono voice.
- **Categorical node palette** (label → color) and **edge provenance grammar**
  (solid structural vs dashed inferred + confidence badge) are foundations,
  not component details — they must be identical on canvas, in chips, in
  inspector, and in chat citations.
- Deliverables: foundations (type, color, spacing), ~14 components, 5 screens
  (Graph, Docs, Flows, Chat-driving-canvas, Dashboard).

## 7. Decisions log

- 2026-07-17 (Yash): unified studio app — not chat-first, not two apps.
- 2026-07-17 (Yash): light visual direction; full design system + screens in
  the first design pass.
- 2026-07-17 (Yash): greenfield — `web/chat-ui` is reference material only and
  will be replaced ("ignore what exists, we'll redo it").

## 8. Out of scope (v1)

- Auth/multi-tenancy (localhost tool, like Neo4j Browser).
- Write operations of any kind (the UI inherits `codegraph_cypher`'s read-only
  stance).
- Graph-wide "show me everything" rendering; the canvas is a working set by
  design.
- Mobile layouts (responsive down to laptop widths only).
