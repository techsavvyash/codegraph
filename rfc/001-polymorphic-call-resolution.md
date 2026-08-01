# RFC-001: Polymorphic Call Resolution via SCIP Relationships + Forked Indexers

| Field | Value |
|-------|-------|
| **Status** | Implemented for Go + TypeScript (Layers 1–3; Go resolver `internal/ingest/resolve`, TS resolver `tools/ts-resolver` closed 2026-07-30). Phases 7–8 closed 2026-08-02 by RFC-014 (`internal/query/reachability`, `codegraph query deadcode`). Python untouched. |
| **Created** | 2026-03-07 |
| **Updated** | 2026-08-02 |
| **Authors** | @techsavvyash |
| **Relates to** | `internal/ingest/scip/call_graph_generic.go`, `internal/ingest/scip/scip_relationships.go`, `internal/ingest/scip/scip_indexer.go`, `internal/ingest/resolve/` |

> **Status update (2026-07-30, post-rebuild).** Layers 1+2 survived the rebuild
> (`internal/ingest/scip`). Layer 3 landed for Go as the in-repo resolver this
> RFC lists under "Optional fallback" — `internal/ingest/resolve`, using
> `go/packages` + `types.Implements`, joining output onto symbol strings parsed
> from the actual index, provenance-stamped `detectionSource: go-types-resolver`
> (fork strategy abandoned: no fork maintenance, same authority). Key empirical
> correction: **current scip-go emits intra-project structural implementations
> well** (129 implementation relationships for this repo vs the ~12 measured in
> March), so the Go resolver is a drift/regression guarantee rather than the
> primary source. Verified live on this repo: 55/57 implementing methods are
> reachable through Layer-2 CALLS fan-out; the 2 unreached are mock test
> doubles (correctly caller-less). The tier-2 entry-point queries were also
> fixed to match method-level IMPLEMENTS edges (they demanded `->(:Interface)`,
> a shape only class-level edges have). **TypeScript remains the real gap**
> (scip-typescript walks heritage clauses only) — the TS structural resolver
> (`tools/ts-resolver` + `internal/ingest/resolve/tsresolver.go`) is in
> progress, following the same join-against-actual-index design.

## Problem

When code calls a method through a polymorphic reference (interface, abstract type, protocol), the reference occurrence resolves to the **abstract declaration symbol**. To reach concrete implementations, consumers must use SCIP `SymbolInformation.Relationships` (`is_implementation`) or equivalent supplemental data.

Historically, CodeGraph did not ingest or use those relationships, so:

1. The call graph builder creates a CALLS edge from the caller to the interface method.
2. The concrete implementation method receives no incoming CALLS edges.
3. The concrete method gets `inDegree = 0`, and downstream analysis (dead code detection, impact analysis, dependency tracing) treats it as unreachable.

### Validated pre-fix state (CodeGraph)

1. `scip_parser.go` parses symbols and occurrences, but drops `SymbolInformation.Relationships`.
2. `scip_indexer.go` creates `DEFINES` and `REFERENCES`, but does not persist symbol-level `IMPLEMENTS`.
3. `call_graph_generic.go` and `call_graph_scip.go` resolve calls only through direct `Reference -> Symbol <- DEFINES -> target`.

This is sufficient to produce false dead-code reports in polymorphic call paths even when the `.scip` file contains valid implementation relationships.

### Current implemented state (2026-03-08)

Layer 1 and Layer 2 are now implemented in this repository:

1. `scip_relationships.go` adds `ExtractRelationships()` and `SCIPRelationship` parsing from `doc.Symbols[*].Relationships`.
2. `scip_indexer.go` extracts relationships during indexing and creates Neo4j `IMPLEMENTS` edges via `buildImplementsBatch(...)`.
3. `call_graph_generic.go` and `call_graph_scip.go` now resolve references using `IMPLEMENTS` fan-out with direct fallback.

This closes the main consumer-side gap. It improves call graph quality where indexers emit `is_implementation`, but does **not** by itself make dead-code classification reliable.

### Affected languages

The problem affects every language with structural or implicit interface satisfaction:

| Language | Interface model | `implements` keyword? | SCIP indexer captures implementations? |
|----------|----------------|----------------------|----------------------------------------|
| Go | Fully structural | No | Indexer attempts structural matching, but observed output in our project is sparse (~12 rels, mostly testify suite embedding cases) |
| TypeScript | Structural + optional `implements` | Optional | Only explicit `implements` clauses + object literal contextual types |
| Python | Protocol classes (PEP 544) | No | Unknown — likely nothing |
| Java/Kotlin | Nominal | Yes (required) | Yes — the one language family where this works |
| C# | Nominal | Yes (required) | Likely yes |

Languages with nominal typing (Java, C#) are unaffected because their SCIP indexers capture explicit `implements` declarations. Languages with structural typing (Go, TypeScript, Python) lose most or all implementation relationships.

### Evidence

**TypeScript (khaata project — NestJS backend):**

`scip-typescript` emits 1,025 implementation relationships, but misses the critical case of classes satisfying local interfaces without an explicit `implements` clause. Example:

```typescript
// quotations.controller.ts — local interface, no link to concrete class
interface PdfOperationsServiceInterface {
  enqueuePdfGeneration(documentType: string, companyId: string, documentId: string): Promise<any>;
  getPdfUrl(documentType: string, companyId: string, documentId: string): Promise<any>;
  downloadPdf(documentType: string, companyId: string, documentId: string): Promise<...>;
}

// pdf-operations.service.ts — concrete class, never declares `implements`
@Injectable()
export class PdfOperationsService {
  async enqueuePdfGeneration(...) { ... }
  async getPdfUrl(...) { ... }
  async downloadPdf(...) { ... }
}
```

SCIP stores these as two completely separate symbol trees. When a controller calls `this.pdfOperationsService.enqueuePdfGeneration(...)`, the reference resolves to `PdfOperationsServiceInterface#enqueuePdfGeneration()` — a symbol with no DEFINES edge from the concrete `PdfOperationsService#enqueuePdfGeneration()`. The call graph builder cannot bridge the gap.

Result: all 4 methods of `PdfOperationsService` reported as dead code (inDegree=0). They are not dead — they are called from 6 controllers.

**Go (codegraph project itself):**

`scip-go` emits only 12 implementation relationships for the entire codebase, all from testify `suite.Suite` struct embeddings. Core interfaces like `GraphStore` (with implementations `Neo4jGraphStore`, `MockGraphStore`), `EmbeddingService`, `VectorStore`, and `PipelineTimer` have **zero** implementation relationships in the SCIP index.

Go interfaces are 100% structural — there is never an `implements` keyword. Any missing implementation relationships in the index directly degrade call-graph quality.

### Root cause analysis

The protocol is not the problem. SCIP already has the exact edge type needed:

```protobuf
message Relationship {
  string symbol = 1;
  bool is_reference = 2;
  bool is_implementation = 3;
  bool is_type_definition = 4;
}
```

The practical failure is a two-part gap:

1. **Consumer gap (CodeGraph)** — relationships are not ingested/stored/used for call-graph resolution.
2. **Indexer coverage gap (language-specific)** — some structural implementation cases are not emitted by certain indexers.

Indexer-specific notes:

- **scip-typescript** (`FileIndexer.ts:forEachAncestor()`): implementation relationships are derived from ancestor traversal (`extends`/`implements`) and contextual object typing, but not from general structural assignability checks (`checker.isTypeAssignableTo`).
- **scip-go**: upstream includes implementation inference via method-set matching and emits `is_implementation` relationships, but observed output in this repository is still sparse for key interfaces (`GraphStore`, `EmbeddingService`, etc.).

### Scope of impact

This affects every downstream feature that depends on the call graph:

- **Dead code detection** — false positives for any method called through an interface
- **Impact analysis** — changing an interface method appears to have no downstream impact
- **Call chain tracing** — chains terminate at the interface method instead of reaching concrete implementations
- **Dependency analysis** — services that communicate through interfaces appear disconnected

## Is This Enough For Agent Input?

Short answer: **no, not yet for reliable dead-code decisions**.

The enriched index is a strong base signal source, but an agent still needs deterministic reachability context and uncertainty modeling. Without that, it will over-flag valid runtime paths as dead.

Minimum additional requirements for a reliable initial information source:

1. **Entrypoint/root modeling** — API handlers, queue consumers, schedulers/cron jobs, CLI entrypoints, framework bootstraps.
2. **Deterministic reachability pass** — graph traversal from roots over `CALLS` plus polymorphic fan-out.
3. **Dynamic edge channel** — model uncertain edges (`dynamic_dispatch`, `reflection`, `event_bus`, `di_resolution`) separately from strict static edges.
4. **Coverage telemetry** — per-language counts for `IMPLEMENTS`, unresolved references, root coverage, and unreachable-node ratio.
5. **Confidence tiers** — classify outputs as `definitely_dead`, `probably_dead`, `unknown` instead of binary dead/alive.

## Hybrid Dead-Code Pipeline (Concrete)

The target model is deterministic candidate generation + agent triage/explanation.

### Stage A: Build analysis graph snapshot

Inputs:

1. Indexed graph nodes/edges from SCIP (`DEFINES`, `REFERENCES`, `CALLS`, `IMPLEMENTS`, `EXPOSES_API`)
2. Language/runtime detectors (HTTP routes, message subscriptions, cron/scheduler metadata)
3. Scope context (`scopeId`, service name, package/module filters)

Output:

1. Versioned analysis snapshot keyed by `(scopeId, indexRunId)`
2. Quality counters: `totalFunctions`, `callsEdges`, `implementsEdges`, `rootCount`, `unresolvedRefCount`

### Stage B: Deterministic reachability engine

Algorithm:

1. Seed roots from structural tiers (`EXPOSES_API`, interface-impl roots, topo roots, centrality roots)
2. Traverse `CALLS` edges with may-call semantics for polymorphic fan-out
3. Traverse `DYNAMIC_CALLS` edges with uncertainty weight (do not mark definitely reachable from dynamic-only evidence)
4. Materialize per-node reachability state and provenance

Reachability states:

1. `reachable_strong` — reached via deterministic static edges from high-confidence roots
2. `reachable_weak` — reached only via weak roots and/or dynamic edges
3. `unreached` — no path found from any root

### Stage C: Candidate generation and scoring

Generate dead-code candidates from `unreached` nodes, then score with explicit features:

1. Root-quality features (tier, detection source, count of root paths)
2. Structural features (`inDegree`, `outDegree`, caller/callee entropy, interface participation)
3. Dynamic-risk features (reflection markers, DI wiring presence, event subscription evidence)
4. Churn/observability features (recent edits, runtime traces if available)

Classification policy (initial):

1. `definitely_dead` when `unreached` and no dynamic-risk evidence and no external contract signal
2. `probably_dead` when `unreached` with partial/dynamic risk evidence
3. `unknown` when graph completeness or root coverage is below threshold

### Stage D: Agent triage and explanation layer

Agent input contract per candidate:

```json
{
  "node_key": "method:src/service/foo.ts#...",
  "classification": "probably_dead",
  "confidence": 0.71,
  "reasons": ["unreached_from_roots", "implements_edge_missing_for_peer_type"],
  "evidence": {
    "roots_considered": 18,
    "strong_paths": 0,
    "weak_paths": 1,
    "dynamic_signals": ["nestjs_provider_injection"],
    "related_symbols": ["FooServiceInterface#doWork()"]
  }
}
```

Agent responsibilities:

1. Validate borderline candidates by local code reading and pattern checks
2. Produce human explanations and confidence-adjusted recommendations
3. Propose missing-edge fixes (`IMPLEMENTS`, `DYNAMIC_CALLS`, root seeds)

Deterministic engine remains the source of truth for candidate generation.

### Stage E: Feedback loop and calibration

1. Persist analyst/user dispositions (`true_dead`, `false_positive`, `intentional_unused`)
2. Recalibrate scorer thresholds per language/runtime profile
3. Track precision by tier and by signal source (`static_only`, `static+dynamic`)
4. Feed misses into forked indexer backlog (especially structural-typing gaps)

## Proposed solution

A pragmatic three-layer approach:

1. **Layer 1 — Ingest existing SCIP relationships** (high ROI, low risk)
2. **Layer 2 — Use those relationships in call-graph resolution**
3. **Layer 3 — Improve relationship emission via forked indexers**

### Layer 1: Extract existing SCIP relationships

Our SCIP parser (`scip_parser.go`) currently discards `SymbolInformation.Relationships`. These contain 1,025 implementation relationships in a typical TypeScript project (explicit `implements`, object literal contextual types, class heritage chains).

**Change**: Parse `Relationships` in `ExtractSymbols()`, return them alongside symbol definitions, and create `IMPLEMENTS` edges in Neo4j during `indexSymbols()`.

This is a pure data extraction change — no heuristics, no new dependencies, no language-specific logic. It captures everything SCIP indexers already compute.

### Layer 2: Call graph enhancement

The call graph builder's reference resolution query changes from direct-only resolution to implementation-aware resolution.

Current query:

```cypher
MATCH (ref:Reference)-[:REFERENCES]->(sym:Symbol)<-[:DEFINES]-(target)
WHERE (target:Function OR target:Method)
RETURN ref.startLine AS refLine, elementId(target) AS targetId
```

Enhanced query:

```cypher
MATCH (ref:Reference)-[:REFERENCES]->(sym:Symbol)<-[:DEFINES]-(directTarget)
WHERE (directTarget:Function OR directTarget:Method)

OPTIONAL MATCH (directTarget)<-[:IMPLEMENTS]-(concreteTarget)
WHERE (concreteTarget:Function OR concreteTarget:Method)
  AND concreteTarget.signature CONTAINS $packageName

WITH ref, COALESCE(concreteTarget, directTarget) AS target
RETURN ref.startLine AS refLine, elementId(target) AS targetId
```

Semantics:

- If implementations exist, connect CALLS to concrete methods.
- If not, fall back to direct target.
- If multiple implementations exist, emit multiple CALLS edges (may-call semantics).

### Layer 3: Forked indexers (preferred over upstream contribution for now)

Given current delivery constraints, we will use a **fork-first** strategy instead of waiting on upstream contributions.

Fork targets:

- `scip-typescript`: add structural implementation inference using `checker.isTypeAssignableTo(...)` for class/interface method mapping where explicit ancestry does not exist.
- `scip-go`: investigate why structural relationships are sparse in our workloads and patch emission logic as needed.

Delivery model:

1. Pin fork versions in CodeGraph tooling.
2. Generate standard `index.scip` (no protocol change).
3. Keep compatibility with upstream CLI flags/output format.
4. Rebase fork periodically, with regression tests on relationship counts and dead-code false-positive benchmarks.

### Optional fallback: Type Resolver protocol

If fork maintenance cost becomes too high, we can reintroduce an external `implements.json` resolver pipeline as a decoupled fallback.

Define a resolver interface that supplements SCIP with implementation relationships the indexer missed. Each language provides a resolver that uses its own type checker.

#### Protocol

```
Input:
  - index.scip          (SCIP index file produced by the language's indexer)
  - project root path   (source tree)
  - package filter       (limit to intra-project symbols)

Output:
  - implements.json      (list of supplementary implementation relationships)
```

Output format:

```json
{
  "relationships": [
    {
      "from_symbol": "scip-go gomod example.com/app . `pkg`/MyStruct#Write().",
      "to_symbol": "scip-go gomod std . `io`/Writer#Write().",
      "is_implementation": true,
      "is_reference": true
    }
  ]
}
```

The `from_symbol` and `to_symbol` fields use SCIP symbol strings, ensuring direct compatibility with the existing graph.

#### Pipeline integration

```
┌─────────────────┐
│  SCIP Indexer    │  scip-go / scip-typescript / scip-python / ...
│  (unchanged)     │
└────────┬────────┘
         │ index.scip
         ▼
┌─────────────────┐
│  Type Resolver   │  Language-specific binary
│  (new)           │
│                  │
│  Reads: index.scip + source tree
│  Uses: language's own type checker
│  Writes: implements.json
└────────┬────────┘
         │ implements.json
         ▼
┌─────────────────────────────────────────────┐
│  CodeGraph Indexer (language-agnostic)       │
│                                              │
│  1. Parse index.scip                         │
│     - Extract symbols, references (existing) │
│     - Extract Relationships (Layer 1 — new)  │
│  2. Store nodes + IMPLEMENTS edges in Neo4j  │
│  3. Build call graph (enhanced query)        │
│  4. Compute inDegree/outDegree               │
└─────────────────────────────────────────────┘
```

### Complexity bounds

Layer 1 and Layer 2 are linear in parsed symbol/reference volume and add negligible asymptotic cost compared to current indexing.

Fork-side structural checks are approximately `O(interfaces × candidate_types)` per language module and can be bounded with package/module prefilters.

## Design decisions

### Why fork SCIP indexers now?

Forking `scip-go` and `scip-typescript` is the fastest path to production-grade coverage without waiting on upstream process latency.

Trade-offs:

1. **Pros** — immediate control, faster iteration, can ship fixes on our timeline.
2. **Cons** — maintenance burden and periodic upstream rebasing.
3. **Mitigation** — keep forks minimal, protocol-compatible, and heavily regression-tested.

Forking does not block future upstreaming; we can upstream patches later if/when process constraints change.

### Why not a graph-based heuristic (method name matching)?

A post-indexing Cypher query that matches interface methods to class methods by name would avoid language-specific tools. However:

1. **No correctness guarantee** — methods with the same name but different semantics would create false IMPLEMENTS edges
2. **No parameter type checking** — `toString()` on 50 unrelated classes would all match any interface with a `toString()` method
3. **Cannot distinguish interfaces from classes** — SCIP indexers emit `UnspecifiedKind` for most interface definitions, so we can't even identify which types to match against
4. **Language-specific typing rules vary** — Go requires exact signature match, TypeScript allows structural covariance, Python checks at runtime. A single heuristic cannot model this.

The fundamental issue: **structural typing rules are defined by each language's type system.** Any correct solution must consult that type system. Reimplementing it in Cypher would be both incomplete and language-specific — the worst of both approaches.

### Why JSON output and not a modified SCIP file?

The resolver outputs `implements.json` rather than modifying `index.scip` because:

1. **Simplicity** — writing protobuf from a TypeScript/Go resolver adds unnecessary complexity
2. **Debuggability** — JSON is human-readable, easy to inspect during development
3. **Idempotency** — the original `index.scip` is never mutated
4. **Merging** — our Go indexer already parses JSON; adding a SCIP protobuf writer per language is overhead

A future optimization could output supplementary SCIP protobuf, but JSON is the right starting point.

### COALESCE vs UNION for call graph resolution

The enhanced Cypher query uses `COALESCE(concreteTarget, directTarget)` rather than a UNION of two queries. This means:

- If an IMPLEMENTS edge exists, the call graph connects to the **concrete** method only
- If no IMPLEMENTS edge exists, it falls back to the **direct** target (interface method or plain function)

This is correct for call graph semantics: we want to know what code actually executes, not what type signature was referenced.

For the case where multiple concrete types implement the same interface method, the `OPTIONAL MATCH` produces multiple `concreteTarget` rows, creating CALLS edges to each implementation. This models virtual dispatch correctly.

## Implementation plan

### Phase 0: Instrumentation baseline (new)

**Files changed**: `scip_indexer.go` (logging only)

1. Print counts during ingest: documents, symbols, relationships, `is_implementation` relationships.
2. Fail fast (or warn loudly) when relationship counts are suspiciously low for large projects.
3. Capture before/after metrics for dead-code false positives.

### Phase 1: Extract existing SCIP relationships (Layer 1)

**Status**: Implemented

**Files changed**: `scip_parser.go`, `scip_indexer.go`

1. Add `ExtractRelationships()` to `SCIPParser` — iterates `doc.Symbols[*].Relationships`, returns `[]SCIPRelationship`
2. Add `indexImplementsRelationships()` to `SCIPIndexer` — after `indexSymbols()`, creates IMPLEMENTS edges for all `IsImplementation: true` relationships
3. Match relationship source/target to existing Symbol nodes using SCIP symbol strings

**Estimated scope**: ~100 lines of Go. No new dependencies.

**Immediate impact**: Captures explicit `implements` (TypeScript), struct embeddings (Go), object literal contextual types (TypeScript). Fixes a subset of false dead code reports.

### Phase 2: Call graph enhancement (Layer 2)

**Status**: Implemented

**Files changed**: `call_graph_generic.go`, `call_graph_scip.go`

1. Update reference resolution queries to traverse method-level `IMPLEMENTS` edges.
2. Preserve direct fallback for symbols without implementations.
3. Treat multiple implementations as may-call fan-out.

### Phase 3: Fork scip-typescript

**Status**: Pending

**Fork repo**: `sourcegraph/scip-typescript`

1. Add structural assignability-based relationship emission for class/interface pairs not connected via heritage clauses.
2. Emit class-level and method-level `is_implementation` relationships.
3. Keep output protocol-compatible with upstream SCIP.

### Phase 4: Fork scip-go

**Status**: Pending

**Fork repo**: `sourcegraph/scip-go`

1. Investigate sparse implementation emission in our workloads.
2. Patch relationship generation where current method-set logic misses valid local satisfactions.
3. Add fixture covering interfaces such as `GraphStore`-like patterns.

### Phase 5: Pipeline integration

**Status**: Pending

**Files changed**: `scip_indexer.go`, `call_graph_generic.go`

1. Add indexer override flags (`--scip-go-binary`, `--scip-typescript-binary`) to support forked binaries cleanly.
2. Keep default path behavior for upstream binaries.
3. Add compatibility checks in CI to ensure produced `.scip` remains consumable.

### Phase 6: Optional external resolver fallback (future)

Reintroduce `implements.json` protocol only if fork maintenance becomes untenable.

### Phase 7: Reachability engine for dead-code reliability

**Files planned**: `libs/inference-go/graph_seeds.go`, new `libs/inference-go/reachability.go`, `libs/query-go/*`

1. Materialize root sets with tier metadata.
2. Compute `reachable_strong`, `reachable_weak`, `unreached` labels.
3. Store path provenance summary for agent consumption.

### Phase 8: Candidate classifier + agent handoff contract

**Files planned**: new `libs/inference-go/deadcode_classifier.go`, `apps/cli/main.go`

1. Score unreached nodes into `definitely_dead` / `probably_dead` / `unknown`.
2. Expose CLI/API endpoint to emit candidate bundles for agent triage.
3. Track validation outcomes to calibrate thresholds.

## Open questions

1. **Should resolvers run automatically or on-demand?** Auto-running during `codegraph index scip` is convenient but adds latency. An explicit `codegraph resolve-types` command is more transparent.

2. **How should we handle transitive implementations?** If `A implements B` and `B extends C`, should we create `A implements C`? The SCIP Relationships already handle this for explicit heritage chains. The resolver should emit direct relationships only — transitive closure can be computed in the graph if needed.

3. **Do we upstream fork changes later?** For now, no dependency on upstream acceptance. Revisit once behavior is stable in production.

4. **Multiple implementations per interface method**: When `io.Writer` is satisfied by 10 types, a call through `io.Writer.Write()` creates 10 CALLS edges. Is this correct for all downstream use cases (dead code, impact analysis), or do some use cases need "may-call" vs "must-call" semantics?

## References

- [SCIP Protocol — Relationship message](https://github.com/sourcegraph/scip/blob/main/scip.proto)
- [scip-typescript — FileIndexer.ts:forEachAncestor()](https://github.com/sourcegraph/scip-typescript/blob/main/src/FileIndexer.ts)
- [scip-typescript issue #252 — Object literal implementation relationships](https://github.com/sourcegraph/scip-typescript/issues/252)
- [TypeScript PR #56448 — Public isTypeAssignableTo API (TS 5.4+)](https://github.com/microsoft/TypeScript/pull/56448)
- [Go `types.Implements` — standard library](https://pkg.go.dev/go/types#Implements)
- [scip-go implementations logic (`internal/implementations/implementations.go`)](https://github.com/sourcegraph/scip-go/blob/main/internal/implementations/implementations.go)
- [scip-go issue #64 — cross-repo find implementations gap](https://github.com/sourcegraph/scip-go/issues/64)
- [scip-go issue #65 — remote type/interface implementations gap](https://github.com/sourcegraph/scip-go/issues/65)
- [Kythe schema — `satisfies` edge kind](https://kythe.io/docs/schema/)
- [CodeQL TypeScript library — no structural subtyping support](https://codeql.github.com/docs/codeql-language-guides/codeql-library-for-typescript/)
- [PEP 544 — Python Protocol classes](https://peps.python.org/pep-0544/)
