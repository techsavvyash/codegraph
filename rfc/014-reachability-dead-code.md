# RFC-014: Reachability Engine + Dead-Code Classification

| Field | Value |
|-------|-------|
| **Status** | Draft |
| **Created** | 2026-08-02 |
| **Authors** | @techsavvyash |
| **Relates to** | RFC-001 phases 7–8, RFC-013 (correctness foundation), `internal/query/inference/graph_seeds.go` |

## Problem

"Is this function dead?" is the single highest-value question a call graph can
answer, and the naive answer (`inDegree = 0`) is wrong in both directions:

- **False positives**: entry points (API handlers, main, scheduled tasks,
  broker consumers), interface implementations invoked polymorphically,
  functions called only at module scope, and address-taken callbacks all have
  `inDegree = 0` or unattributed callers while being emphatically alive.
- **False negatives**: a function *with* callers is still dead when every
  caller is itself dead. On khaata, the empirical gap was 380 naive
  candidates vs 439 truly-unreached including ~59 dead-cluster members
  invisible to per-node degree checks.

The graph now carries every signal needed to do this properly (RFC-013 made it
trustworthy; the call-vs-value classification made it complete):

| Signal | Edge/property | Liveness meaning |
|---|---|---|
| Direct + polymorphic calls | `(Function\|Method)-[:CALLS]->` (IMPLEMENTS fan-out baked in) | caller live → callee live |
| Module-scope execution | `(File)-[:CALLS]->` | file loaded → callee runs |
| Address-taken functions | `(caller\|File)-[:USES_VALUE]->` | caller live → callee *may* run (dynamic dispatch) |
| API surface | `(fn)-[:EXPOSES_API]->(:APIRoute)` | externally invoked |
| Broker/cron | `fn.consumesBroker`, `fn.scheduledTask` | externally invoked |
| Self-loops excluded from inDegree | asymmetric degrees (RFC-013) | recursion can't fake liveness |

## Design

### Roots (liveness sources), in tier order

1. **API-exposed**: any fn with `EXPOSES_API` (all detection sources).
2. **Runtime entry**: Go `main`/`init` functions; `fn.scheduledTask`,
   `fn.consumesBroker` stamps.
3. **Module load**: every `(File)-[:CALLS]->` target whose File belongs to the
   service. Files are treated as unconditionally loaded — TS lazy imports and
   Go build tags over-approximate toward liveness, which is the safe direction
   for a "safe to delete?" verdict.
4. **Test roots** (tracked separately): functions in test files (`_test.go`,
   `*.spec.ts`, `*.test.ts` path patterns) with no non-test path to them.

Exported-but-unreached functions are NOT roots: for an application service
they are prime dead-code candidates. They get a distinct verdict
(`exported_unreached`) so library-style services can be read accordingly.

### Traversal

Single BFS over the union graph `CALLS ∪ USES_VALUE` from all non-test roots
(Cypher path expansion via one query per frontier, or apoc-free iterative
expansion in Go — implementation detail; graph sizes here are 10³–10⁴ nodes).
A second BFS from test roots labels what only tests reach. Self-loops are
naturally harmless in BFS.

`USES_VALUE` traversal direction: caller → target only (taking an address
keeps the *target* alive; it says nothing about the taker).

### Verdicts (stamped on Function/Method nodes)

- `live` — reached from a non-test root. Also stamps `reachabilityRoot`
  (nearest root's nodeKey) and `reachabilityTier` (1–3).
- `test_only` — reached only from test roots.
- `exported_unreached` — unreached but exported/public (library candidate).
- `dead` — unreached, unexported. Sub-classified `dead_cluster` when
  `inDegree > 0` (its callers are all dead too — the naive-check blind spot).
- `unknown` — service-level guard: if a service has zero tier-1/2 roots AND
  zero flows (telemetry root-coverage gate from RFC-013), every would-be
  `dead` in it becomes `unknown` — a graph with no detected entry points
  cannot honestly call anything dead.

Properties: `fn.reachability`, `fn.reachabilityRoot`, `fn.reachabilityTier`,
stamped per (serviceName, scopeId), recomputed by a pipeline stage after
degree computation, and by `codegraph verify` consumers on demand.

### Surfaces

- **Pipeline stage** `ComputeReachability` (after ComputeGraphMetrics): stamps
  verdicts at index time so they're always fresh; prints a one-line summary
  (`live=812 test_only=41 dead=57 (12 in clusters) exported_unreached=9`).
- **CLI** `codegraph query deadcode --service=X [--include-test-only]
  [--format=json]`: verdict listing with file/line, sorted by cluster then
  file; `--explain <name>` prints the root path for a live fn (why is this
  alive?).
- **MCP tool** `codegraph_deadcode`: same data for agents; per-fn
  `reachability` also surfaces in existing find/expand inspectors for free
  (it's a node property).
- **Studio**: dead-code count per service on the dashboard (warn flag when
  > N% of functions are dead); later a dedicated pane (out of scope here).

### Verification (RFC-013 discipline)

- Labeled fixtures: hand-verified verdict assertions in
  `test/fixtures/labeled-{go,ts}` — a dead cluster (a→b→a unreached pair), an
  address-taken-only function (must be `live`), a module-scope-called function
  (must be `live`), a test-only helper.
- Cross-check against the Go oracle's CHA reachability on codegraph: every
  `dead` verdict must also be CHA-unreachable from main (sandwich again —
  disagreements are bugs).
- khaata acceptance: the 59 dead-cluster members from the phase-0 empirical
  assessment must be found; spot-check 10 verdicts by hand.

## Non-goals

- Runtime/coverage-based liveness (future).
- Reflection/registry-pattern detection beyond USES_VALUE (address-taken is
  the conservative catch-all).
- Cross-service reachability (a service's API surface is its boundary; the
  CALLS_SERVICE layer already models the coupling).
