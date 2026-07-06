# Pristine Execution Runbook (Task-By-Task, Verify-Once)

This runbook converts the pristine target into small, deterministic implementation slices with strict validation gates after each slice.

Primary goal:

- Move from prototype/stub heuristics to evidence-driven generation + provenance-enforced inference where graph facts are source of truth.

Use this as the execution source of truth for implementation and validation on this repository itself.

Related references:

- `docs/19-pristine-state-task-backlog.md`
- `docs/20-pristine-audit-action-plan.md`
- `rfc/009-evidence-driven-intelligence-architecture.md` (superseded by RFC-005)
- `docs/18-business-context-gap-closure-plan.md`

---

## Task Status Tracker

| # | Task | Priority | Status | Notes |
|---|------|----------|--------|-------|
| 1 | P0-A: Make Stage 6 the Single Generation Authority | P0 | ✅ Done | Removed generation from SCIP Step 11; Stage 6 is sole authority for PR summaries, docstrings, flow summaries |
| 2 | P0-B: Wire Generator + Verifier + Policy into Pipeline CLI | P0 | ✅ Done | Added Policy field to PipelineConfig; wired generator/verifier/policy in CLI pipeline+replay commands; graceful LLM fallback |
| 3 | P0-C: Persist Statement-Level Citations | P0 | ✅ Done | Added Statements/Citations fields to GeneratedDoc model; Store methods accept+serialize GenerationResult citations; pre-persistence validation rejects uncited docs as diagnostics |
| 4 | P0-D: Enforce Policy Gate and Persist Rejections as Diagnostics | P0 | ✅ Done | PR summaries routed through generateAndVerify; policy gate unit tests added |
| 5 | P1-A: Provenance Validator as Mandatory Write Gate | P1 | ✅ Done | Expanded ValidateMentionEdgeProps to require scopeId+model/strategy; added BuildMentionEdgeProps helper; fixed intelligent_linker, flow_linker, documents/indexer write sites |
| 6 | P1-B: Flow Quality Controls (Noise Reduction) | P1 | ✅ Done | Replaced hardcoded Cypher name patterns with StructuralSeedFinder; priority-sorted seeds; wired budget from CLI --max-depth; removed dead SeedScorer code |
| 7 | P1-C: CI Quality Gate as Blocking | P1 | ✅ Done | WriteGateReport + enforcement test + CI artifact upload |
| 8 | P2-A: Scope Contract Hardening in CLI and Pipeline | P2 | ⬜ TODO | |

---

## 0. Execution Rules (Important)

1. Implement one task at a time.
2. Run only that task's verification pack immediately after implementation.
3. If the task passes, freeze and move to the next task.
4. Do not run full-suite validation after every task; run milestone/full validation only at checkpoints.
5. Keep all validations on this repository (`.`) and PR scope (`pr-audit`) unless explicitly testing `main` behavior.

---

## 1. Task P0-A: Make Stage 6 the Single Generation Authority

### Why

Generation currently runs in two places:

1. Pipeline Stage 6 (`GenerateContextDocs`) in `libs/indexer-go/pipeline/stages.go`.
2. SCIP ingestion Step 11 in `libs/indexer-go/static/scip_indexer.go`.

This creates duplicated, inconsistent behavior and bypasses evidence/verification gates.

### Scope

- Remove or hard-disable generated-doc creation in `SCIPIndexer.IndexProject` Step 11.
- Keep PR node creation in ingestion if needed, but move generated doc creation to Stage 6 only.

### Code Paths

- `libs/indexer-go/static/scip_indexer.go`
- `libs/indexer-go/pipeline/stages.go`

### Implementation Snippet (target shape)

```go
// scip_indexer.go (Step 11)
// Keep only PullRequest node creation (optional), do NOT call:
// - StorePRSummary
// - GenerateDocstringSuggestionsForScope
// - GenerateFlowSummariesForScope
// These belong to pipeline Stage 6.
```

### Verify (Task-Local)

1. Run:

```bash
go test ./libs/indexer-go/static ./libs/indexer-go/pipeline ./libs/indexer-go/generated -count=1
```

2. Run pipeline in PR scope:

```bash
./bin/codegraph index pipeline . --service codegraph --version p0-a --doc-paths docs --scope pr --scope-id pr-audit
```

3. Confirm generated docs are created only after Stage 6 (not during IngestCode logs).

### Exit Criteria

1. No docstring/flow summary creation inside `SCIPIndexer.IndexProject`.
2. Generated docs still created by Stage 6 in pipeline run.

---

## 2. Task P0-B: Wire Generator + Verifier + Policy into Pipeline CLI

### Why

`PipelineConfig` supports `Generator` and `Verifier`, but CLI pipeline commands do not currently inject concrete implementations. Stage 6 therefore falls back to `auto-stub` output.

### Scope

- In `apps/cli/main.go`, wire dependencies for:
  - `index pipeline`
  - `index replay`
- Add a policy evaluator injection path for Stage 6.

### Code Paths

- `apps/cli/main.go`
- `libs/indexer-go/pipeline/pipeline.go`
- `libs/indexer-go/pipeline/stages.go`
- `libs/generation-go/generator.go`
- `libs/verification-go/verifier.go`

### Implementation Snippet (target shape)

```go
// apps/cli/main.go when building PipelineConfig
gen := generation.NewGenerator(llmClient, responseParser)
ver := verification.NewVerifier(graphResolver)
policy := verification.NewPolicyEvaluator(verification.PolicyConfig{/* thresholds */})

cfg := &pipeline.PipelineConfig{ /* existing fields */ }
cfg.Generator = gen
cfg.Verifier = ver
cfg.Policy = policy // add field in PipelineConfig if missing
```

```go
// stages.go Stage 6
if cfg.Generator != nil { ctxGen.SetGenerator(cfg.Generator) }
if cfg.Verifier != nil { ctxGen.SetVerifier(cfg.Verifier) }
if cfg.Policy != nil   { ctxGen.SetPolicy(cfg.Policy) }
```

### Verify (Task-Local)

1. Run:

```bash
go test ./apps/cli ./libs/indexer-go/pipeline ./libs/generation-go ./libs/verification-go -count=1
```

2. Run PR pipeline:

```bash
./bin/codegraph index pipeline . --service codegraph --version p0-b --doc-paths docs --scope pr --scope-id pr-audit
```

3. Verify generated docs are no longer `auto-stub` model-only fallback.

### Exit Criteria

1. Stage 6 receives non-nil generator/verifier/policy in pipeline mode.
2. Stub-only generation path is no longer the default in pipeline command.

---

## 3. Task P0-C: Persist Statement-Level Citations (Evidence-First Schema)

### Why

Generated docs are still persisted as `content` blobs without statement/citation arrays, violating pristine requirements.

### Scope

- Extend `GeneratedDoc` persistence to include statement-level structure.
- Enforce that each statement has at least one citation.

### Code Paths

- `libs/indexer-go/generated/context.go`
- `libs/intelligence-go/contracts/*`
- `libs/generation-go/generator.go`
- `libs/verification-go/verifier.go`

### Implementation Snippet (target shape)

```go
docProps := map[string]any{
  "type":       docType,
  "title":      title,
  "content":    result.Content,
  "statements": result.Statements, // []Statement-like serialized payload
  "citations":  result.Citations,  // statement-indexed evidence refs
  "model":      result.Model,
  "strategy":   "evidence_backed",
  "createdAt":  now,
  "scope":      g.scopeCtx.Scope,
  "scopeId":    g.scopeCtx.ScopeID,
}
```

```go
// reject before persistence
if uncited := generation.ValidateGenerationResult(result); len(uncited) > 0 {
  // store diagnostic, do not publish GeneratedDoc
}
```

### Verify (Task-Local)

1. Run:

```bash
go test ./libs/generation-go ./libs/verification-go ./libs/indexer-go/generated -count=1
```

2. Reindex PR scope:

```bash
./bin/codegraph index pipeline . --service codegraph --version p0-c --doc-paths docs --scope pr --scope-id pr-audit
```

3. Graph checks:

```cypher
MATCH (gd:GeneratedDoc {scopeId:'pr-audit'})
RETURN count(gd) AS total,
       count(CASE WHEN gd.statements IS NOT NULL THEN 1 END) AS withStatements,
       count(CASE WHEN gd.citations IS NOT NULL AND size(gd.citations)>0 THEN 1 END) AS withCitations;
```

```cypher
MATCH (gd:GeneratedDoc {scopeId:'pr-audit'})
WHERE gd.citations IS NULL OR size(gd.citations)=0
RETURN gd.nodeKey LIMIT 10;
```

### Exit Criteria

1. `withStatements == total`.
2. `withCitations == total`.
3. Zero generated docs with empty citations.

---

## 4. Task P0-D: Enforce Policy Gate and Persist Rejections as Diagnostics

### Why

Verifier failures must not produce published docs. They must create diagnostics for triage.

### Scope

- Ensure fail path writes `GenerationDiagnostic`.
- Ensure pass path writes `GeneratedDoc` only.

### Code Paths

- `libs/indexer-go/generated/context.go`
- `libs/verification-go/policy.go`
- `libs/verification-go/verifier.go`

### Implementation Snippet (target shape)

```go
decision := g.policy.Evaluate(genResult, verResult)
if !decision.Allowed {
  g.storeDiagnostic(ctx, docType, sourceType, sourceKey, genResult, verResult, decision.PolicyViolations)
  return false, nil
}
```

### Verify (Task-Local)

1. Unit tests:

```bash
go test ./libs/verification-go ./libs/indexer-go/generated -count=1
```

2. Induce fail case using strict threshold in local config/test and run Stage 6.

3. Graph checks:

```cypher
MATCH (d:GenerationDiagnostic {scopeId:'pr-audit'}) RETURN count(d);
```

```cypher
MATCH (gd:GeneratedDoc {scopeId:'pr-audit'})
WHERE gd.citations IS NULL OR size(gd.citations)=0
RETURN count(gd) AS badPublished;
```

### Exit Criteria

1. Rejected outputs exist as diagnostics.
2. No rejected output is published as `GeneratedDoc`.

---

## 5. Task P1-A: Provenance Validator as Mandatory Write Gate

### Why

Many `MENTIONS` and inferred edges are missing required provenance fields.

### Scope

- Introduce shared validated write helper.
- Apply to all inferred/linking relationship writes.

### Code Paths

- `libs/intelligence-go/provenance/provenance.go`
- `libs/search-go/chunk_linker.go`
- `libs/search-go/intelligent_linker.go`
- Any inference edge persistence path

### Required Fields

1. `scope`
2. `scopeId`
3. `confidence`
4. `reasons`
5. `createdAt`
6. `strategy`
7. `evidenceRefs`

### Implementation Snippet (target shape)

```go
prov := provenance.Metadata{
  Scope: g.scopeCtx.Scope,
  ScopeID: g.scopeCtx.ScopeID,
  Confidence: conf,
  Reasons: reasons,
  CreatedAt: time.Now().UTC(),
  Strategy: "hybrid_linking",
  EvidenceRefs: refs,
}
if err := provenance.Validate(prov); err != nil {
  return fmt.Errorf("provenance validation failed: %w", err)
}
// only then create relationship
```

### Verify (Task-Local)

1. Run:

```bash
go test ./libs/intelligence-go/provenance ./libs/search-go -count=1
```

2. Re-run pipeline in PR scope.

3. Graph check:

```cypher
MATCH ()-[r:MENTIONS]->()
WHERE r.scopeId='pr-audit' AND (
  r.confidence IS NULL OR r.reasons IS NULL OR size(coalesce(r.reasons,[]))=0 OR
  r.createdAt IS NULL OR r.strategy IS NULL OR r.evidenceRefs IS NULL OR size(coalesce(r.evidenceRefs,[]))=0
)
RETURN count(r) AS invalidMentions;
```

### Exit Criteria

1. `invalidMentions = 0` in `pr-audit` scope.

---

## 6. Task P1-B: Flow Quality Controls (Noise Reduction)

### Why

Current flow output is overly broad and includes generic library entrypoints.

### Scope

- Introduce stronger seed scoring.
- Apply allow/deny policies.
- Tighten fanout/depth and deterministic ordering.

### Code Paths

- `libs/query-go/flow_spine.go`
- `libs/inference-go/flow_seeds.go`
- `libs/inference-go/flow_quality.go`

### Implementation Snippet (target shape)

```go
seedScore := scorer.ScoreSeed(candidate)
if seedScore < budget.MinSeedScore { continue }
if budget.IsNameBlocked(candidate.Name) { continue }
if !budget.IsNodeAllowed(candidate.Type) { continue }
```

### Verify (Task-Local)

1. Run:

```bash
go test ./libs/query-go ./libs/inference-go ./libs/evals-go -count=1
```

2. Generate flows:

```bash
./bin/codegraph query flows --generate --scope-id pr-audit --max-depth 3
```

3. Compare against golden metrics and record spurious-step rate.

### Exit Criteria

1. Spurious-step rate is below policy threshold.
2. Top results are domain-relevant, not framework utility-heavy.

---

## 7. Task P1-C: CI Quality Gate as Blocking

### Why

Without CI blocking, regressions can reintroduce non-pristine behavior.

### Scope

- Enforce golden eval thresholds on PR.
- Publish metrics artifact.

### Code Paths

- `libs/evals-go/quality_gate.go`
- `.github/workflows/quality-gate.yml`

### Verify (Task-Local)

1. Dry-run local evals:

```bash
go test ./libs/evals-go -count=1
```

2. In branch, intentionally degrade threshold and ensure CI fails.

3. Restore threshold and ensure CI passes.

### Exit Criteria

1. PR fails when thresholds are violated.
2. Metrics artifact is visible in workflow run.

---

## 8. Task P2-A: Scope Contract Hardening in CLI and Pipeline

### Why

`--scope-id` alone can be ambiguous; enforce explicit scope semantics.

### Scope

- If `--scope-id` starts with `pr-`, infer `scope=pr` when scope omitted.
- Or fail with clear error requiring `--scope pr`.
- Apply same behavior to both `index pipeline` and `index replay`.

### Code Paths

- `apps/cli/main.go`

### Verify (Task-Local)

1. `--scope-id pr-audit` without `--scope` behaves deterministically.
2. Tests for flag parsing and scope context creation.

### Exit Criteria

1. No accidental writes into `main`/empty scope when user intended PR scope.

---

## 9. Milestone Validation Packs (Run Less Often)

Run only after completing a full milestone block.

### Milestone A (after P0-A..P0-D)

```bash
make build
go test ./libs/generation-go ./libs/verification-go ./libs/indexer-go/generated ./libs/indexer-go/pipeline -count=1
./bin/codegraph index pipeline . --service codegraph --version milestone-a --doc-paths docs --scope pr --scope-id pr-audit
```

Graph checks:

1. All generated docs in `pr-audit` have citations.
2. No uncited published generated docs.
3. Failed generations appear as diagnostics.

### Milestone B (after P1-A..P1-C)

```bash
go test ./libs/intelligence-go/provenance ./libs/search-go ./libs/query-go ./libs/inference-go ./libs/evals-go -count=1
./bin/codegraph index pipeline . --service codegraph --version milestone-b --doc-paths docs --scope pr --scope-id pr-audit
./bin/codegraph query flows --generate --scope-id pr-audit --max-depth 3
```

Graph checks:

1. No inferred edge missing mandatory provenance.
2. Flow quality threshold holds.
3. CI gate remains green.

---

## 10. One-Pass Final Acceptance (Do Once)

Run this only at the end of the entire runbook.

```bash
make build
go test ./libs/generation-go ./libs/verification-go ./libs/intelligence-go/provenance ./libs/retrieval-go ./libs/inference-go ./libs/query-go ./libs/search-go ./libs/evals-go -count=1
go test ./test/integration -run 'Delayed|Overlay|Chunk|TriStore' -count=1
./bin/codegraph index pipeline . --service codegraph --version pristine-final --doc-paths docs --scope pr --scope-id pr-audit
./bin/codegraph query flows --generate --scope-id pr-audit --max-depth 3
env -u GEMINI_API_KEY -u GOOGLE_API_KEY -u OPENAI_API_KEY ./bin/codegraph query search "flow summary" --limit 10 --scope-id pr-audit
```

Must all be true:

1. Generated docs are evidence-backed (statement-level citations present).
2. Failed verification is diagnostic-only, not published.
3. Inferred/link edges have complete provenance.
4. Flow output quality is within thresholds.
5. Search works in constrained environment via fallback mode.

---

## 11. Suggested Commit Slices

1. `refactor: make stage6 the sole generated-context writer`
2. `feat: wire generator verifier policy into pipeline commands`
3. `feat: persist statement-level citations for generated docs`
4. `feat: gate generated-doc publish on verifier policy`
5. `feat: enforce provenance validation on mention/inference writes`
6. `feat: tighten flow seed and traversal quality controls`
7. `ci: enforce eval quality gates as blocking checks`
8. `fix: harden scope flag contract for pipeline and replay`
