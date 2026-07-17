# Pristine Audit Action Plan

This document converts the latest audit into an execution-ready checklist with clear implementation guidance and verification criteria.

Related sources:

- `docs/19-pristine-state-task-backlog.md`
- `rfc/009-evidence-driven-intelligence-architecture.md` (superseded by RFC-005)
- `docs/18-business-context-gap-closure-plan.md`

---

## 1. Critical Issues (Must Fix First)

### Issue A: Generated docs are not evidence-first

Current gap:

- `GeneratedDoc` content is free-form text.
- No statement-level citations are persisted.
- Verifier exists but is not enforced before persistence.

Primary files:

- `libs/indexer-go/pipeline/stages.go`
- `libs/indexer-go/generated/context.go`
- `libs/generation-go/generator.go`
- `libs/verification-go/verifier.go`

Actionable tasks:

1. Route Stage 6 (`GenerateContextDocs`) through generation + verification packages instead of direct stub persistence.
2. Change generated output schema from single `content` blob to statement list with citations.
3. Enforce policy gate: persist published docs only when verifier passes.
4. Persist failed outputs as diagnostics, not `GeneratedDoc` context.

How to implement:

1. Add a generation adapter call in Stage 6 that accepts task-specific evidence bundles and returns structured statements.
2. Add verifier invocation in Stage 6 before write path.
3. Add a persistence branch:
   - pass -> write `GeneratedDoc` + `DERIVED_FROM` + citation metadata
   - fail -> write `GenerationDiagnostic` (or equivalent) with rejection reasons
4. Add schema checks to reject uncited statements.

How to verify:

1. `go test ./libs/generation-go ./libs/verification-go ./libs/indexer-go/generated ./libs/indexer-go/pipeline -count=1`
2. `./bin/codegraph index pipeline . --service codegraph --version audit --doc-paths docs`
3. Neo4j checks:
   - No persisted `GeneratedDoc` statement without citations.
   - Verifier failures are persisted as diagnostics.
4. CI gate check: unsupported-claim rate and citation coverage thresholds are enforced.

Exit criteria:

- Every generated statement has at least one valid citation.
- No unverified generated context is published.

---

### Issue B: Provenance fields are incomplete/inconsistent

Current gap:

- `GeneratedDoc` nodes are missing required provenance fields such as `createdAt`.
- Some `MENTIONS` edges are missing `confidence` and `reasons`.

Primary files:

- `libs/intelligence-go/provenance/provenance.go`
- `libs/indexer-go/generated/context.go`
- `libs/search-go/chunk_linker.go`
- `libs/search-go/intelligent_linker.go`

Actionable tasks:

1. Make provenance validator mandatory for every inferred edge and generated artifact write.
2. Normalize minimum required fields:
   - `scope`, `scopeId`, `confidence`, `reasons`, `createdAt`, `strategy`, `evidenceRefs`
3. Block writes that fail provenance validation.

How to implement:

1. Add a shared helper invoked by all write paths before persistence.
2. Replace direct relationship creation calls with validated persistence wrappers.
3. Backfill existing generated write paths to include missing fields (`createdAt`, `strategy`, etc.).

How to verify:

1. `go test ./libs/intelligence-go/provenance ./libs/search-go ./libs/indexer-go/generated -count=1`
2. Re-run pipeline and query graph for missing provenance fields.
3. Add integration test: any write missing mandatory provenance must fail.

Exit criteria:

- Zero inferred edges/generated artifacts with missing mandatory provenance.

---

### Issue C: Flow quality is too noisy for insight consumption

Current gap:

- Framework-agnostic seeds work, but output is broad/noisy and includes too many generic entrypoints.

Primary files:

- `libs/query-go/flow_spine.go`
- `libs/inference-go/flow_seeds.go`
- `libs/inference-go/flow_quality.go`

Actionable tasks:

1. Tighten seed quality rules (exclude generic library/util symbols by default).
2. Add traversal budgets + allowlist/denylist policy controls.
3. Add dedupe and ranking for flow steps.
4. Gate on spurious-step rate in flow golden eval suite.

How to implement:

1. Introduce a seed scoring function and minimum score threshold.
2. Add max fanout and depth guardrails with deterministic ordering.
3. Add post-traversal filtering for low-information nodes.

How to verify:

1. `go test ./libs/query-go ./libs/inference-go ./libs/evals-go -count=1`
2. `./bin/codegraph query flows --generate --max-depth 3`
3. Compare against flow golden set and record spurious-step rate.

Exit criteria:

- Flow output is stable and meets spurious-step threshold in eval.

---

## 2. High Priority Implementation Tasks

### Task D: Make retrieval/manual insight checks operable without embeddings

Current gap:

- `query search` hard-fails if embedding API key is absent, limiting audit/ops workflows.

Primary file:

- `apps/cli/main.go`

Actionable tasks:

1. Add fallback mode for `query search`:
   - if embeddings unavailable -> run graph + BM25 only
2. Emit explicit mode info in output (`hybrid`, `bm25+graph`, etc.).

How to verify:

1. Run `./bin/codegraph query search "flow summary" --limit 10` with no embedding key.
2. Confirm command succeeds with fallback and returns results.
3. Ensure embedding-enabled mode still works unchanged.

---

### Task E: Enforce CI quality gates as blocking checks

Current gap:

- Quality gate code exists, but CI blocking enforcement is not yet guaranteed.

Primary files:

- `libs/evals-go/quality_gate.go`
- CI workflow files

Actionable tasks:

1. Add CI step to run golden eval suites on PRs.
2. Fail CI when thresholds drop below policy.
3. Publish eval metrics artifact for review.

How to verify:

1. Trigger CI with intentionally degraded threshold in a test branch and confirm failure.
2. Restore thresholds and confirm pass on baseline.

---

## 3. Medium Priority Hardening

### Task F: Full retrieval/inference separation enforcement

Current gap:

- New packages exist, but legacy logic still mixes inference behavior in retrieval/search paths.

Actionable tasks:

1. Move confidence/scoring logic into `libs/inference-go` exclusively.
2. Keep `libs/retrieval-go` limited to candidate retrieval and normalization.
3. Add architectural tests to prevent cross-layer leakage.

How to verify:

1. Unit tests assert retrieval package has no confidence threshold logic.
2. Integration tests confirm inference decisions unchanged after split.

---

### Task G: Replay/backfill and rollout controls operationalization

Current gap:

- Replay and rollout primitives exist but are not fully wired into day-2 operation flows.

Actionable tasks:

1. Add CLI entry points for stage-level replay/backfill.
2. Add shadow-mode execution path for new inference strategy.
3. Expose telemetry for side-by-side quality comparison.

How to verify:

1. Re-run only Stage 5/6 after doc changes without full reindex.
2. Run old/new strategy in shadow mode and compare metrics output.

---

## 4. Execution Sequence

1. Issue A (generation + verification gate)
2. Issue B (provenance enforcement)
3. Issue C (flow quality controls)
4. Task D (search fallback)
5. Task E (CI blocking quality gates)
6. Task F (retrieval/inference separation)
7. Task G (replay + rollout ops)

---

## 5. Verification Command Pack

Run after each milestone:

```bash
make build
go test ./libs/generation-go ./libs/verification-go ./libs/intelligence-go/provenance ./libs/retrieval-go ./libs/inference-go ./libs/query-go ./libs/search-go ./libs/evals-go -count=1
go test ./test/integration -run 'Delayed|Overlay|Chunk|TriStore' -count=1
./bin/codegraph index pipeline . --service codegraph --version audit --doc-paths docs
./bin/codegraph query flows --generate --max-depth 3
./bin/codegraph query search "flow summary" --limit 10
```

Graph validation checks to run each time:

1. No generated statement without citations.
2. No inferred edge missing mandatory provenance.
3. `GeneratedDoc` in PR scope includes `pr_summary`, `flow_summary`, `docstring_suggestion` where expected.
4. CI eval thresholds remain green.

---

## 6. Done Definition For This Plan

This action plan is complete when all of the following are true:

1. Generated context is evidence-backed and verifier-gated.
2. Provenance is complete and validated on all inferred/generated writes.
3. Flow insights are high-signal with measurable quality controls.
4. Retrieval and manual insight commands are operable in constrained environments.
5. CI quality gates are blocking and visible.
