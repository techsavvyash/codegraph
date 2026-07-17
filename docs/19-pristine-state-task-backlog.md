# Pristine State Task Backlog

This backlog defines the concrete tasks required to move from the current implementation to the target evidence-driven, production-grade intelligence platform.

Related design docs:

- `rfc/009-evidence-driven-intelligence-architecture.md` (superseded by RFC-005)
- `docs/18-business-context-gap-closure-plan.md`

---

## 0. Exit Criteria (Definition of Pristine)

The work is complete only when all of the following are true:

1. Linking, flow derivation, and generated documentation run through explicit stage contracts.
2. Scope correctness (`main` + `pr`) is enforced consistently across graph/vector/text retrieval.
3. Every inferred edge and generated sentence includes provenance and valid citations.
4. Golden eval suites run in CI with blocking thresholds.
5. Regressions are observable via quality + latency dashboards.

---

## 1. Foundation Tasks (Contracts + Safety)

### T1.1 Create shared contracts package

- Add shared interfaces and DTOs for retrieval, inference, bundle, generation, verification.
- Path: `libs/intelligence-go/contracts/`
- Output: compile-only interfaces (no behavior change).
- Done when:
  - Existing orchestrators can depend on contracts without cyclic imports.
  - Unit tests validate DTO serialization and backward-compatible fields.

### T1.2 Introduce scoped identity helpers

- Centralize `scopeId::nodeKey` identity construction and parsing.
- Path: `libs/intelligence-go/identity/`
- Output: one canonical helper used by graph/vector/text adapters.
- Done when:
  - No store builds scoped IDs ad hoc.
  - Collision tests for identical `nodeKey` in `main` and `pr-*` pass.

### T1.3 Add provenance schema validator

- Enforce mandatory fields: `scope`, `scopeId`, `confidence`, `reasons`, `createdAt`, `strategy`, `evidenceRefs`.
- Path: `libs/intelligence-go/provenance/`
- Done when:
  - All inference write paths pass validator before persistence.

---

## 2. Retrieval Stabilization Tasks

### T2.1 Unify retrieval output envelope

- Normalize results from graph/vector/text into a single candidate schema.
- Path: `libs/retrieval-go/`
- Done when:
  - `query search` and linker consume same candidate envelope.

### T2.2 Scope-safe retrieval end-to-end

- Ensure `scopeId` is threaded through:
  - CLI query flags,
  - vector filters,
  - text filters,
  - graph rehydration.
- Done when:
  - Overlay precedence tests pass.
  - Tombstoned main nodes are hidden in PR scope.

### T2.3 Add retrieval metrics hooks

- Capture per-source counts, merge behavior, latency and fallback reasons.
- Done when:
  - Eval runner can export retrieval diagnostics by query.

---

## 3. Inference Hardening Tasks

### T3.1 Split inference from retrieval

- Move link scoring and flow inference into dedicated package.
- Path: `libs/inference-go/`
- Done when:
  - Retrieval package has zero confidence-threshold logic.

### T3.2 Replace heuristic-only confidence with feature scoring

- Build feature extractor for:
  - lexical overlap,
  - vector score,
  - structural support (`CALLS`, `HAS_STEP`, ownership),
  - explicit references.
- Add calibrated confidence mapping.
- Done when:
  - Calibration error metric is tracked in eval suite.

### T3.3 Framework-agnostic flow seeds

- Add structural seed finder independent of framework detectors.
- Keep framework detectors as optional precision boosters.
- Done when:
  - Flow derivation works on repos without recognized route frameworks.

### T3.4 Flow quality controls

- Add traversal budgets (depth/fanout/allowlist) and dedupe logic.
- Done when:
  - Spurious-step rate is below threshold on flow golden set.

---

## 4. Context Bundle Tasks

### T4.1 Build bounded evidence bundle builder

- Add bundle assembly from anchors with strict graph expansion budgets.
- Path: `libs/context-bundles-go/`
- Done when:
  - Bundle outputs contain stable citation references and deterministic ordering.

### T4.2 Add task-specific bundle templates

- Templates for:
  - `flow_summary`,
  - `pr_summary`,
  - `docstring_suggestion`,
  - `feature_to_code` mapping.
- Done when:
  - Each generation task uses a dedicated bundle schema.

---

## 5. Generation and Verification Tasks

### T5.1 Generation adapter with strict schema

- Generator returns statement-level citation arrays.
- Path: `libs/generation-go/`
- Done when:
  - JSON schema validation rejects uncited statements.

### T5.2 Citation verifier

- Verify every citation resolves to existing graph evidence in visible scope.
- Path: `libs/verification-go/`
- Done when:
  - Unsupported claim rate is tracked and enforced in CI.

### T5.3 Persistence policy gate

- Persist generated docs only if verification passes policy.
- Done when:
  - Failed drafts are stored as diagnostics, not published context.

---

## 6. Evaluation and Feedback Loop Tasks

### T6.1 Build golden datasets

- Create datasets for:
  - linking,
  - flow derivation,
  - generated docs citation validity.
- Path: `libs/evals-go/testdata/`
- Done when:
  - Datasets cover at least 3 domains (api, data, async/messaging).

### T6.2 Add CI quality gates

- Gate on:
  - Recall@K,
  - nDCG,
  - linking precision,
  - citation coverage,
  - unsupported-claim rate.
- Done when:
  - PR fails if any metric drops below threshold.

### T6.3 Add ablation and drift checks

- Run feature ablations and shadow comparisons for scorer changes.
- Done when:
  - Any score-model change must include eval delta report.

---

## 7. Operationalization Tasks

### T7.1 Add run telemetry and dashboards

- Track stage durations, fail rates, quality metrics, token cost.
- Done when:
  - Dashboard shows trendlines by commit/build.

### T7.2 Stage-level replay and backfill

- Add ability to rerun only failed/changed derivation stages.
- Done when:
  - Backfill can re-derive flows/docs without full reindex.

### T7.3 Release strategy

- Add shadow mode and progressive rollout controls.
- Done when:
  - New inference strategy can run side-by-side before cutover.

---

## 8. Implementation Sequence (Recommended)

1. T1.1, T1.2, T1.3
2. T2.1, T2.2, T2.3
3. T3.1, T3.2
4. T3.3, T3.4
5. T4.1, T4.2
6. T5.1, T5.2, T5.3
7. T6.1, T6.2, T6.3
8. T7.1, T7.2, T7.3

---

## 9. Milestone Checkpoints

### M1: Safe and Scoped

- Contracts merged.
- Scope-safe retrieval fully passing.
- No cross-scope collisions.

### M2: Trustworthy Inference

- Feature-scored linker live.
- Framework-agnostic flow derivation live.
- Flow/linking golden suites green.

### M3: Evidence-First Documentation

- Bundle + generation + verification pipeline live.
- Statement-level citations mandatory.

### M4: Production Quality Loop

- CI quality gates active.
- Dashboards + shadow rollout active.
- Regression response playbook documented.
