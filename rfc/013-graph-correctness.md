# RFC-013: Graph Correctness — Verification, Oracles, and Drift Detection

**Status**: Accepted (2026-08-01)
**Author**: codegraph team

## Problem

Every major graph-correctness bug shipped in July 2026 was caught by *manual live
verification*, never by the automated suite:

1. **Decorator route detection silently never ran for TypeScript** — `Detect()` was
   gated on `language == go`; the integration tests called the strategy directly and
   stayed green. 424 real routes vs 107 detected, invisible for the whole life of the
   legacy analyzer.
2. **Tier-2 entry-point queries were dead for months** — they matched
   `-[:IMPLEMENTS]->(:Interface)` while real method-level edges are Method→Method.
   Zero results looked like "no interface impls", not "wrong query".
3. **TS structural resolver produced ~96% noise** (767 edges on khaata, dominated by
   all-optional option-bag interfaces) — caught only by eyeballing output before commit.
4. **Built-in LOOKUP indexes were missing for weeks** — `DropSchema` deleted them;
   every labeled MATCH became AllNodesScan. Everything "worked", slowly.

The common thread: the graph has no way to tell us it is wrong. Golden snapshots
assert "same as last time", not "true"; unit tests assert the code does what the code
does. Correctness decomposes into four distinct failure modes, each needing a
different mechanism:

| # | Failure mode | Example above | Caught by |
|---|--------------|---------------|-----------|
| 1 | Missing truth (recall) | routes 107/424 | differential oracles, census, telemetry |
| 2 | Fabricated data (precision) | 767 noise edges | differential oracles |
| 3 | Broken integrity | leaked itest scopes, dangling edges, missing LOOKUP | invariant suite |
| 4 | Silent drift | decorator pass not running | per-run telemetry diff |

## Design

Four layers, exposed under a single CLI namespace `codegraph verify` plus automatic
hooks in the index pipeline. All layers live in `internal/verify/*`.

### Layer 1: Integrity invariant suite — `codegraph verify integrity`

A suite of Cypher-backed invariants runnable against any graph at any time
(`internal/verify/integrity.go`). Initial invariant set:

1. **No dangling relationship endpoints** — CALLS, IMPLEMENTS, CONTAINS, DEFINES,
   REFERENCES, EXPOSES_API endpoints carry the expected labels.
2. **Identity uniqueness** — no duplicate `scopedKey` per label (mirrors the
   constraint, catches pre-constraint residue and label drift).
3. **Containment** — every Function/Method is reachable from a File via CONTAINS.
4. **Range sanity** — `startLine <= endLine`, non-negative, present where
   `rangeSource` claims a real source.
5. **Stamping** — code nodes carry `serviceName` and `scopeId`; Services carry
   `rootPath` that exists on this machine (warn, not fail — graphs move hosts).
6. **Scope hygiene** — non-`main` scopes are reported (leaked `itest-*` residue).
7. **Schema presence** — required constraints, indexes, and the built-in LOOKUP
   indexes exist (regression guard for incident 4).

Output statuses are `pass` / `warn` / `fail` per check with counts and up to N sample
node/edge identifiers. `--service` scopes checks; `--strict` makes warnings fail the
exit code. The index pipeline runs the service-scoped suite automatically after every
index and prints findings (never blocks the index itself).

### Layer 2: Differential oracles — `codegraph verify oracle`

Independent recomputation of graph facts from a second implementation, joined onto
the graph and reported as precision/recall. The join follows the RFC-001 principle:
parse descriptors from symbols present in the actual index; never string-construct.

**Go (`--language=go`)**: build the call graph twice more via
`golang.org/x/tools/go/callgraph` and apply the *sandwich principle*:

    static(direct calls) ⊆ graph CALLS ⊆ CHA(all may-call edges)

- Edges in `static` missing from the graph → **recall gaps** (mode 1).
- Graph edges absent from `CHA` → **precision suspects** (mode 2).
- Both bounds are computed from types, sharing no code with the SCIP pipeline.

**TypeScript (`--language=typescript`)**: `tools/ts-oracle/oracle.mjs` runs the
target project's own compiler (same loading strategy as `tools/ts-resolver`),
enumerates call expressions with compiler-resolved declarations, and samples the
comparison (full TS call-graph extraction is unbounded; a random sample of resolved
call sites gives a recall estimate with a stated sample size).

**Census (`codegraph verify census`)**: universal cheap recall floor — tree-sitter
declaration counts per file (`internal/ingest/structure`) vs Function/Method node
counts per file in the graph. Language-independent; catches whole-file and
whole-construct dropouts (e.g. "no functions indexed from .tsx files").

### Layer 3: Per-run telemetry + drift — `codegraph verify drift`

Every index run stamps an `IndexRun` node:

    (:Service)-[:HAS_RUN]->(:IndexRun {
      runId, serviceName, scopeId, startedAt, finishedAt,
      files, functions, methods, symbols,
      callsEdges, implementsEdges, apiRoutes,
      callsPerFunction,                    // derived
      rangeSourceDist, detectionSourceDist, // JSON-encoded distribution maps
      promotedFunctions, decoratedFunctions
    })

Counters are computed from the graph (Cypher) after the pipeline finishes, so they
measure what actually landed, not what the indexer believed it wrote. After stamping,
the pipeline diffs against the previous IndexRun of the same service and prints drift
warnings on large deltas (default: ±25% on any counter, any distribution key
appearing/disappearing). `codegraph verify drift --service=X` shows the run history
and diffs on demand. Old runs are pruned (keep last 10 per service).

This is also RFC-001 Phase 7's "coverage telemetry" prerequisite.

### Layer 4: Labeled truth fixtures — `test/harness/labeled/`

Golden snapshots detect *change*; labeled fixtures detect *wrongness*. A small
per-language corpus where every expected edge was enumerated by a human once:

    test/harness/labeled/<lang>-<topic>/
      src/...            (fixture project, indexable)
      expected.json      { "mustHaveEdges": [...], "mustNotHaveEdges": [...],
                           "mustHaveNodes": [...], "mustNotHaveNodes": [...] }

Constructs covered initially: Go interfaces + embedding + generic skip; TS arrow
functions, decorators (NestJS routes), re-exports, default exports, interface
implementations, option-bag non-edges (the 767-noise class as explicit
`mustNotHaveEdges`). Tests index the fixture into an isolated scope and assert exact
presence/absence — additions elsewhere in the graph never break them, unlike
snapshots.

## Shared contracts

`internal/verify/report.go` defines `CheckResult{Name, Status, Detail, Count,
Samples}` and `Report`, shared by integrity, census, and drift output.
`internal/verify/oracle` defines `OracleReport{MustEdges, MayEdges,
MissingFromGraph, PrecisionSuspects, Recall, ...}`. All CLI surfaces support
`--format=json` for agent/CI consumption.

## Non-goals

- Runtime tracing / production coverage-based liveness (future signal for RFC-001
  Phase 8, out of scope here).
- Fixing every finding the new checks surface in the dev graph — findings become
  issues; the machinery is the deliverable.
- Python oracle (no active Python projects; census still covers Python recall floor).

## Relationship to RFC-001

Phases 7–8 (reachability engine, dead-code classifier) consume this RFC's output:
telemetry provides root-coverage counters that gate `unknown` verdicts, and oracle
precision/recall numbers calibrate how much to trust CALLS completeness per language.
Correctness first, then verdicts on top of it.

## Addendum (2026-08-02): call-vs-value classification + module-scope attribution

Two of the recall/precision gaps the oracles surfaced on day one are now fixed
at the indexer level rather than merely recorded:

- **Function-VALUE references** (`embedder.Fn = semlinkVectors`) no longer
  fabricate CALLS edges. Both builders classify every reference against the
  file's call sites (Go AST `CallExpr` callee identifiers; tree-sitter
  call-node callee identifiers across all 8 grammars, `internal/ingest/
  structure` `CallSites`). Value references become
  `(caller|File)-[:USES_VALUE]->(fn)` — kept in-graph because liveness must
  treat address-taken functions as conservatively live (`cobra.OnInitialize
  (initConfig)` is the canonical case).
- **Module-scope call sites** (package-level `var x = compute()`, top-level TS
  statements, load-time decorators) now produce `(File)-[:CALLS]->(fn)` —
  import-time invocation attributed to the file itself. Degree properties
  deliberately still count Function/Method callers only; the RFC-001 phase-7
  reachability engine consumes File-CALLS and USES_VALUE explicitly.
- The TS oracle gained a `decorator` skip category: decorator invocations
  execute at class-definition time and are attributed to the File, outside the
  oracle's Function/Method-caller model (previously misread as recall gaps).

Post-fix numbers: Go oracle on codegraph **100% recall, 0 precision
suspects**; TS oracle on khaata **100% recall** (was 98.9% with 2 Go suspects
and decorator-shaped false gaps). khaata CALLS 1591 → 1137 with the removed
454 reappearing among 556 USES_VALUE edges — the drift detector flagged the
transition at -28.5%, which is the system working as designed. `usesValueEdges`
is now an IndexRun counter; integrity checks the USES_VALUE endpoint shape.
