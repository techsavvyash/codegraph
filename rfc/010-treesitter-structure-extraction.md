# RFC-010: Tree-sitter Structure Extraction

| Field | Value |
|-------|-------|
| **Status** | Draft |
| **Created** | 2026-07-10 |
| **Authors** | @techsavvyash |
| **Refines** | RFC-005 §6.1, §6.4 · RFC-006 Phase 3 items 1 & 7 |
| **Depends on** | RFC-006 Phases 0–2 (shipped) |

## 1. Summary

Phase 3's "correct ranges" work assumed we would read `Occurrence.EnclosingRange`
from SCIP to get real function body ranges (RFC-005 §6.1, RFC-006 Phase 3 item 1).
**That field is empty in every index our shipped indexers produce** — so the plan
does not survive contact with the tools. This RFC replaces the `EnclosingRange`
dependency with a dedicated tree-sitter structure pass that owns body ranges,
nesting, and control-flow shape, and draws a hard line between what tree-sitter can
own (syntax/structure) and what stays with SCIP (symbol resolution). The pivot is
small in the RFCs' own terms: RFC-005 already names an `internal/ingest/treesitter/`
package and lists `treesitter` as a first-class provenance source — it framed
tree-sitter as the fallback to `EnclosingRange`. This RFC promotes it to primary.

## 2. Motivation

### 2.1 The empirical finding

The SCIP protocol defines `Occurrence.enclosing_range` for exactly our use case:
the full span of the definition a symbol occurrence belongs to. RFC-005 §6.1 called
reading it the fix that "deletes the declaration-order inference hack and most of the
Go-AST re-parsing crutch." Measured against the tools we actually invoke:

| Indexer | Version | Definitions | With `enclosing_range` |
|---|---|---|---|
| `scip-go` | 0.1.26 | 28 | **0** |
| `scip-typescript` | 0.3.11 | 16 | **0** |

Neither exposes a flag to emit it (`scip-typescript index --help`, `scip-go --help`
checked). The field is optional in the protocol and unimplemented in these emitters;
`scip-python`, `scip-java` are no better. Building on `EnclosingRange` means forking
or waiting on upstream Sourcegraph indexers — the identical dependency trap RFC-001
documented for structural-typing relationships. We do not take it.

### 2.2 What we run today, and why it is wrong

Non-Go languages use declaration-order inference in
`internal/ingest/scip/call_graph_generic.go`: functions are sorted by start line, and
each function's `endLine` is set to `nextFunction.startLine − 1`; the **last function
in a file gets `startLine + 10000`, clamped to EOF** — i.e. it claims the entire tail
of the file. Byte offsets are then summed from line lengths over that guessed range.

This is wrong whenever the file's shape departs from "functions back-to-back":

- **Last-function-eats-the-file**: the final function's range extends to EOF
  regardless of where its body actually ends (the `endLine: 10003` tails visible in
  `test/fixtures/tiny-ts/golden.json`).
- **Trailing non-function declarations**: a function followed by a type/const/export
  over-extends across all of it.
- **No nesting**: closures, nested functions, and methods inside classes are
  attributed by flat line order, so an inner function's calls are miscredited to the
  outer one (and vice versa).
- **Blank lines / comments between declarations** inflate every range.

Every downstream consumer inherits the error: `source` retrieval returns the wrong
span, `find`/`expand` line anchors are off, and call-site → enclosing-function
attribution (which drives CALLS edges) misfires exactly where nesting exists.

The Go path does **not** have this problem: `call_graph_scip.go` re-parses with
`go/parser` and gets exact ranges, param types, receiver types, and branch depth from
the AST. That is the quality bar. Tree-sitter brings every other language up to it
without a per-language hand-written parser.

### 2.3 Why tree-sitter, specifically

- **It is the structure oracle.** Given a byte/line position (which SCIP *does* give
  us for every definition), the enclosing function is the innermost tree-sitter
  function node containing that position. No name matching, no ordering heuristic —
  a containment query on a real parse tree.
- **It closes the Go-parity gap for free**: branch depth and `isConditional` on CALLS
  edges, nested-function attribution, and decorator spans (the syntactic half of the
  "NestJS controllers show no callers" blind spot) all fall out of the same parse.
- **It is incremental by construction** — the property that makes the "fast path" in
  RFC-005 §6.4 real. Tree-sitter re-parses a changed file in sub-millisecond time and
  supports true incremental edits; a changed file's *structure* refreshes without
  re-running anything else.
- **The runtime story is settled, not speculative** (§4.2): the tree-sitter org
  maintains official Go bindings and per-grammar Go modules, verified correct on
  this machine against every probe input; a pure-Go runtime was also validated
  end-to-end against this repo's fixtures and the dough/clanker codebases as the
  future no-CGO swap.

## 3. Non-goals (the boundary that makes this tractable)

Tree-sitter is a **parser, not a resolver**. It sees one file's syntax and nothing
else — no types, no import resolution, no knowledge that a call in file A lands on a
definition in file B. Therefore this RFC explicitly does **not** touch:

- **Semantic edges.** `DEFINES`, `REFERENCES`, and the target end of `CALLS` (which
  symbol a call site resolves to) stay 100% SCIP-sourced. Tree-sitter supplies the
  *enclosing function* of a call site; SCIP supplies *what it calls*.
- **Type-driven `IMPLEMENTS`** (RFC-006 Phase 3 items 3–4, the RFC-001 fix). That is
  the type-checker resolvers' job; tree-sitter cannot see structural typing.
- **True incremental *graph* indexing.** This RFC makes the *structure layer*
  incremental. Semantic edges still require a SCIP run, and `scip-typescript` has no
  incremental mode — so end-to-end incremental indexing remains a later, separate
  effort (RFC-006 Phase 3 item 7) that this work is a prerequisite for, not a
  delivery of. §6 states the honest boundary.
- **The Go builder.** `go/parser` is exact and dependency-free; there is no reason to
  replace it with tree-sitter and every reason (fidelity, param/receiver types) to
  keep it. Tree-sitter is for the languages that currently guess.

Naming these out loud is the point: the reason `EnclosingRange` looked attractive was
that it promised structure *and* rode the semantic pipeline. Splitting the two is what
lets the structure layer become fast and incremental without dragging the batch SCIP
run along.

## 4. Design

### 4.1 New package: `internal/ingest/structure`

A language-agnostic structure extractor with a per-language grammar registry.

```go
package structure

// FunctionNode is one function-like syntactic construct in a file: a top-level
// function, a method, a closure/lambda, or a nested function. Positions are
// 1-based lines and 0-based byte offsets, matching the graph's existing props.
type FunctionNode struct {
    StartLine, EndLine int
    StartByte, EndByte int
    // Innermost-enclosing function span; child ranges are strictly contained in
    // their parent's, so attribution is "smallest containing node wins".
    ParentIndex int      // -1 for top-level
    Kind        string   // "function" | "method" | "closure"
    NameHint    string   // identifier text if the grammar exposes it; advisory only
}

// CallSite is a call/invocation expression's position, used to attribute a SCIP
// reference to its enclosing function without re-deriving it from line order.
type CallSite struct {
    Line, Byte  int
    BranchDepth int   // nesting under if/for/switch/try — drives isConditional
}

// Extract parses content once and returns every function node and call site.
// Pure over (lang, content): no I/O, no Neo4j — unit-testable on string inputs.
func Extract(lang Language, content []byte) (FileStructure, error)
```

The extractor is a pure function of `(language, bytes)`. It never touches Neo4j —
mirroring how `resolveGenericCallEdges` and `lineRangeByteOffsets` are already pure
and unit-tested today.

### 4.2 Runtime delivery: official bindings first, validated 2026-07

The candidate runtimes, assessed empirically (probes parsed the tiny-ts fixture and
all 389 dough-gateway + clanker `.ts` files, read-only) and by support posture
(repo-health data pulled 2026-07-16):

| Approach | Build | Verified state |
|---|---|---|
| **Official bindings (`tree-sitter/go-tree-sitter` + per-grammar `bindings/go`)** | CGO | maintained by the tree-sitter org, tags track core (v0.23→v0.24); grammars versioned by the org that writes them; **parsed every probe input correctly** (it was the verification oracle for the §8 bug) |
| `odvcencio/gotreesitter` (pure-Go runtime, parse tables extracted from upstream `parser.c`) | pure Go, `CGO_ENABLED=0` verified | v0.37.0: 206 embedded grammars incl. Go external scanners for TS/TSX/Python; ships `ExtractDefinitionSpans`/`EnclosingDefinition` (≈ our §4.1/§4.3 API for free); incremental parsing; MIT — but **created 2026-02 (months old), single maintainer, and one confirmed parser bug in core TS syntax (§8)** |
| `smacker/go-tree-sitter` (the historical community binding) | CGO | dead: last commit 2024-08, 42 open issues; its adopters (Bearer, CircleCI YAML LS, DeepSource) are stranded legacy users — do not adopt |
| `malivvan/tree-sitter` (tree-sitter WASM on `wazero`) | pure Go | 3 commits, self-described pre-release — not viable |

**Decision: official bindings first.** An earlier draft of this section picked
`gotreesitter` by weighing the pure-Go build against *parse speed* — the wrong
axis. The right axis is **correctness and support**: structure spans feed CALLS
attribution, which is the product, and ten minutes of testing found a
`gotreesitter` parser bug in bread-and-butter TypeScript (§8) — in a months-old,
single-maintainer reimplementation with no institutional backing. Meanwhile CGO's
actual cost to this project today is near zero: codegraph builds from source via
`make` on dev machines and CI where a C compiler is present, and we distribute no
prebuilt multi-platform binaries. If that ever changes, the official binding also
offers a `purego` mode (runtime-loaded grammar `.so`s — no CGO, but shared-library
distribution), and `gotreesitter` remains the pure-Go swap once it matures.

The `Language` interface in §4.1 isolates the choice either way: runtimes are
swappable behind it, decided by evidence, not speculatively. Parse cost is not the
bottleneck regardless — the pure-Go runtime did all 389 dough/clanker TS files
(2.35 MB) in ~6 s and CGO is faster still; `scip-typescript` alone takes longer on
the same repos.

Validation highlights (tiny-ts fixture, pure-Go probe): `greet` extracted at lines
3–5 exactly — the node the graph currently stores as `endLine: 10003`; nested
`class ConsoleLogger` 5–11 with `constructor` 6 and `log` 8–10 attributed to the
class, not flat file order; enclosing-definition lookup at the `logger.log(`
call-site byte resolves to `greet`. That is §4.3's mechanism working end-to-end
before we write a line of integration code. (`ExtractDefinitionSpans`/
`EnclosingDefinition` are `gotreesitter` conveniences; over the official bindings
the same ~50 lines are written once in `internal/ingest/structure` as a walk over
function-like node kinds.)

### 4.3 Attribution: position → enclosing function

The mechanism that replaces declaration-order inference:

1. SCIP parse yields, per definition occurrence, its **identifier position** and, per
   reference occurrence, the **call-site position**.
2. Tree-sitter `Extract` yields the file's `FunctionNode` spans.
3. A definition's body range = the innermost `FunctionNode` whose span contains the
   definition's identifier position. Written to the Function/Method node's
   `startLine/endLine/startByte/endByte`.
4. A reference's enclosing caller = the innermost `FunctionNode` containing the
   call-site position — replacing `findEnclosingGenericFunc`'s line-order scan. Since
   child spans nest strictly inside parents, "smallest containing span wins" is exact
   for closures and nested functions.
5. `BranchDepth`/`isConditional` come from the call site's ancestor chain in the parse
   tree, matching what the Go AST builder already computes.

This is a containment test against a real tree, not an ordering guess — the class of
bug in §2.2 disappears rather than shrinks.

**Kind promotion (found during implementation, 2026-07-16).** Declarator widening
turned out to matter one layer earlier than attribution. scip-typescript emits
`export const f = (…) => …` as a bare **term** symbol (`f.`, `Kind=UnspecifiedKind`)
— verified against the tiny-ts fixture — and class-property arrows as fields
(`C#f.`). Descriptor-based classification therefore labels them `Variable`, they
never become `Function`/`Method` nodes, and no CALLS edge can start or end at them:
before this RFC, every const-bound arrow in a TS codebase was invisible to the call
graph. SCIP carries no signal to fix this; the parse tree does. `ExtractSymbols` now
runs a promotion pass (`promoteDeclaratorBoundFunctions`): a Variable/Field symbol
whose definition occurrence sits inside a declarator-widened function node bound to
the **same name** promotes to Function/Method before node creation. Name equality is
the safety condition — a plain `const x = createLogger()` sits in no function node,
and a const holding an object of arrows sits outside the pair-widened arrows inside
it. Interface properties with function *types* never match (a `function_type` is not
a function node in any wired grammar).

### 4.4 Integration with the call-graph builders

- `GenericCallGraphBuilder` (`call_graph_generic.go`): its `computeByteRanges` /
  declaration-order block (the `nextStart−1` / `+10000` logic) is **deleted** and
  replaced by a lookup into the file's `FileStructure`. `resolveGenericCallEdges` and
  the min-line dedup stay — only the source of ranges and enclosing-function
  attribution changes. The service-bounding invariants from commit `c7b8bbc` are
  preserved verbatim (structure is per-file, so nothing about scope changes).
- `SCIPCallGraphBuilder` (Go): **unchanged.** Keeps `go/parser`.
- Deletion ships before addition (RFC-005 I8): the `+10000` sentinel and its tests go
  in the same change that adds the tree-sitter path, and the golden fixtures
  regenerate with real ranges (no more `endLine: 10003`).

### 4.5 Provenance

Ranges and structure written by this pass carry `source: treesitter` (RFC-005 I4;
the provenance vocabulary already lists it). Today provenance is enforced only on
`InferenceResult` (`internal/model/provenance`); this RFC stamps the structural props
it writes but does **not** take on the "every edge writer refuses unprovenanced
edges" enforcement — that is RFC-006 Phase 3 item 6 and stays there. Where a range is
tree-sitter-derived vs Go-AST-derived is distinguishable by `source`
(`treesitter` vs `scip`/`go-ast`), so the two builders' outputs are never silently
conflated.

## 5. What this fixes, concretely

- `source <function>` returns the exact body for TS/Python/Java, not a
  declaration-line stub or an over-long tail.
- Nested/closure calls are attributed to the right function → CALLS edges and
  in/out-degree stop being wrong wherever nesting exists.
- `isConditional`/`branchDepth` on CALLS edges reach parity with the Go builder for
  all languages.
- Golden fixtures gain a real double-call-site / nested-function case (called out as a
  gap in the Phase 2 close-out), turning today's `+10000` sentinels into asserted
  ranges.

## 6. Incremental indexing — what this does and does not buy

Stated plainly so it is not oversold:

- **Buys:** the structure layer becomes per-file and instant. On a file change,
  re-`Extract` just that file and MERGE its Function/Method ranges + nesting.
  File-hash storage (RFC-005 §6.4; File nodes currently store `""`) plus this pass is
  the entire "fast path."
- **Does not buy:** refreshed *semantic edges*. A changed file's `REFERENCES`/`CALLS`
  targets still need a SCIP run, and `scip-typescript` is batch-only. So end-to-end
  incremental indexing (dirty-detect → fast structural update now, lazy full SCIP
  re-run with scope-diff write later) is a **separate RFC** for which this is the
  enabling half. RFC-006 Phase 3 item 7 should be re-scoped to depend on this.

## 7. Rollout

Ordered; each step ends green with incremental commits (RFC-006 house rules).

1. `internal/ingest/structure`: `Extract` + `Language` registry over the official
   bindings, one language wired (TypeScript, our worst offender; grammar module
   `tree-sitter/tree-sitter-typescript/bindings/go`). Pure unit tests on string
   inputs (nesting, closures, trailing non-function decls, EOF, and an
   ERROR-recovery input asserting the per-definition fallback path).
2. Wire `GenericCallGraphBuilder` to consume `FileStructure`; delete declaration-order
   inference + its tests; regenerate TS/polyglot goldens with real ranges; add the
   nested/double-call-site fixture case.
3. Add remaining grammars (python, java, tsx) behind the same interface; extend the
   auto-detected language set already in the indexer.
4. Provenance stamp (`source: treesitter`) on structural writes; assert it in the
   golden dumps.

**Exit criteria:** golden fixtures carry exact body ranges (no `+10000`); a
nested-function fixture asserts correct caller attribution; `source <fn>` on a
re-indexed dough-gateway/clanker function returns the true body; full suite green
against the populated dev graph; self-index + dough repos re-indexed and spot-checked
via MCP `source`/`flows`.

## 8. Risks & alternatives

- **`gotreesitter` v0.37.0 parser bug (confirmed 2026-07-10; why it is not our
  primary runtime).** Arrow functions with both a typed parameter and a
  return-type annotation — `(a: X): Y => …` — produce ERROR nodes; minimal repro
  `const g = (a: X): Y => a;`. The official CGO bindings parse the same input
  clean, so this is a `gotreesitter` runtime bug (GLR disambiguation), not an
  upstream grammar gap. Measured impact: 12 of 389 dough-gateway + clanker `.ts`
  files (~3%) carried ERROR nodes; error recovery still preserved most definitions
  (17 of ~20 in `tasks.repo.ts`), with one total loss (`auth.decorators.ts`).
  This is the §4.2 decision's evidence: a common-syntax correctness bug found
  within minutes of testing means the pure-Go swap waits until the library
  matures. Reporting it upstream (with this repro) is the constructive follow-up.
- **Grammar/language drift** (new TS syntax the pinned grammar can't parse):
  `Extract` degrades per definition — spans extracted from clean regions are kept,
  definitions inside ERROR regions fall back to the SCIP declaration line
  (`startLine == endLine`) rather than a wrong guess — strictly better than today's
  confident-but-wrong tail. Grammar versions are pinned via the library dependency
  and bumped deliberately.
- **Parse cost on huge files**: measured at ~6 s for 389 files / 2.35 MB even on
  the slower pure-Go runtime (§4.2) — not a concern at our repo sizes; the
  existing `--max-file-byte-size` skip philosophy bounds pathological inputs.
- **CGO in the build**: the real cost of the official bindings. Today it is
  near-zero (source builds via `make` on dev machines/CI with a C compiler; no
  prebuilt multi-platform distribution). If distribution needs change, the
  documented paths out are the official bindings' `purego` shared-library mode or
  the matured pure-Go runtime behind the same `Language` interface — a dependency
  swap, not a redesign.
- **Alternative — fork the SCIP indexers to emit `EnclosingRange`.** Rejected:
  unbounded upstream dependency across four indexers in three ecosystems, the exact
  trap RFC-001 called out, and it still would not give us incremental structure.
- **Alternative — keep declaration-order, just improve the heuristic.** Rejected: no
  heuristic over "definitions in line order" recovers nesting or true end-of-body; it
  is guessing at a problem tree-sitter answers exactly.

## 9. Open questions

- ~~Grammar sourcing/build~~ — resolved by §4.2: grammars are `go get`-able Go
  modules published from each official grammar repo (`bindings/go`), versioned by
  the tree-sitter org; we pin them like any dependency.
- File the `(a: X): Y =>` `gotreesitter` bug upstream with the §8 minimal repro —
  needs a decision on who files it (it is an outward-facing action).
- Do we attribute class/interface **member** ranges (methods) via the same pass now,
  or scope this strictly to Function/Method and leave Class/Interface spans to a
  follow-up? (Leaning: include methods immediately — they are function nodes and the
  same containment query covers them.)
- Should `find`/`expand` byte anchors switch to tree-sitter ranges in the same change,
  or after ranges are proven on goldens? (Leaning: after — one variable at a time.)
