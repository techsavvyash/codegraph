# Code Review Checklist — Bug Fix Implementation

**Initiative:** Fix Three Critical Correctness Bugs in CodeGraph
**Date:** 2026-03-01
**Status:** Ready for Review

---

## Overview

This checklist guides reviewers through the implementation of three critical bug fixes:

1. **Scope Contract** — Validate `--scope-id` flag combinations
2. **Provenance Validation** — Gate all MENTIONS edges through validation
3. **Evidence-First** — Remove silent fallback to auto-stub generation

Each fix was implemented on a separate track with dedicated test coverage.

---

## Track 1: Scope Contract (T1)

### Files to Review

#### Core Implementation
- [ ] `libs/core-models-go/scope.go`
  - `ParseScopeFlags()` function implementation
  - `NormalizePRID()` function implementation
  - Proper error messages for invalid combinations
  - Example: --scope=pr requires --scope-id

- [ ] `apps/cli/main.go` (3 locations)
  - Line 484: `indexPipelineCmd` integration
    - Calls `ParseScopeFlags(scopeFlag, scopeIDFlag)`
    - Checks error and returns explicit message
    - Sets `scopeCtx` before using
  - Line 623: `indexReplayCmd` integration
    - Same pattern as indexPipelineCmd
    - Separate error handling
  - Line 1220: `queryFlowsCmd` uses `NormalizePRID()`
    - Prevents double-prefixing (pr-pr-*)
    - Called before `NewPRScope()`

#### Test Coverage
- [ ] `libs/core-models-go/scope_test.go`
  - `TestNormalizePRID()` — 5 test cases
    - Plain ID (42 → 42)
    - With prefix (pr-42 → 42)
    - Double prefix (pr-pr-42 → pr-42)
    - Empty string handling
    - Just "pr-" handling
  - `TestParseScopeFlags()` — 10 test cases
    - PR scope with ID (3 cases)
    - Default/main scope (3 cases)
    - Error cases (4 cases)

### Review Questions

1. **Does `ParseScopeFlags()` validate all invalid combinations?**
   - [ ] Yes, it rejects --scope-id without --scope=pr
   - [ ] Yes, it rejects --scope-id with non-pr scopes
   - [ ] Error messages are clear

2. **Is `NormalizePRID()` idempotent?**
   - [ ] Yes, calling it twice gives same result
   - [ ] Edge cases handled (empty, just "pr-")

3. **Are all CLI commands using the scope functions?**
   - [ ] indexPipelineCmd ✓
   - [ ] indexReplayCmd ✓
   - [ ] queryFlowsCmd uses NormalizePRID ✓

4. **Do tests cover happy and error paths?**
   - [ ] Happy paths tested ✓
   - [ ] Error paths tested ✓
   - [ ] Edge cases covered ✓

---

## Track 2: Provenance Validation (T2)

### Files to Review

#### Core Implementation — 4 MENTIONS Write Paths

- [ ] `libs/search-go/chunk_linker.go` — 2 paths
  - Line 181: `createMentionEdge()`
    - Calls `provenance.BuildMentionEdgeProps()`
    - Checks error and returns
    - Uses validated props in Cypher
    - Parameters: confidence, reasons, model, now, scopeID
  - Line 228: `createMentionEdgesBatch()`
    - Iterates through edges
    - Validates each with `BuildMentionEdgeProps()`
    - Logs warning for invalid edges
    - Continues processing valid edges
    - Collects valid edges into `edgeMaps`

- [ ] `libs/search-go/flow_linker.go` — 1 path
  - Line 163: `createFlowMention()`
    - Builds reasons from patterns
    - Calls `provenance.BuildMentionEdgeProps()`
      - confidence: 0.7
      - reasons: []string (flow_step_reference + patterns)
      - model: "flow_aware_linking"
      - now: timestamp
      - scopeID: fl.scopeID
    - Checks error and returns
    - Uses props from validation result

- [ ] `libs/indexer-go/documents/indexer.go` — 1 path
  - Line 487: `simpleLinkToCodeSymbols()`
    - Extracts code symbols from content
    - For each symbol found:
      - Calls `provenance.BuildMentionEdgeProps()`
        - confidence: 0.5
        - reasons: ["symbol_reference"]
        - model: "simple_symbol_extraction"
        - now: timestamp
        - scopeID: di.scopeCtx.ScopeID
      - Checks error and skips invalid with log
      - Adds context field to validated props
      - Creates relationship with validated props

#### Import Verification
- [ ] `libs/search-go/flow_linker.go`
  - [ ] Has `import ... "github.com/context-maximiser/code-graph/libs/intelligence-go/provenance"`
- [ ] `libs/indexer-go/documents/indexer.go`
  - [ ] Has `import ... "github.com/context-maximiser/code-graph/libs/intelligence-go/provenance"`
  - (chunk_linker should already have it)

#### Test Coverage
- [ ] Tests verify validation gate is active
  - `libs/search-go/chunk_linker_test.go`
  - `libs/search-go/flow_linker_test.go`
  - Tests for error handling on invalid Model/scopeID

### Review Questions

1. **Do all 4 write paths call BuildMentionEdgeProps()?**
   - [ ] chunk_linker.createMentionEdge ✓
   - [ ] chunk_linker.createMentionEdgesBatch ✓
   - [ ] flow_linker.createFlowMention ✓
   - [ ] documents/indexer.simpleLinkToCodeSymbols ✓

2. **Are invalid edges handled gracefully?**
   - [ ] Single edges: return error
   - [ ] Batch edges: log warning, skip invalid, continue with valid
   - [ ] Symbol extraction: log warning, skip, continue

3. **Are all required fields being set from validation?**
   - [ ] confidence from props
   - [ ] reasons from props
   - [ ] model from props
   - [ ] createdAt from props
   - [ ] scopeId from props

4. **Do tests verify validation behavior?**
   - [ ] Tests for invalid Model values
   - [ ] Tests for missing scopeID
   - [ ] Tests for empty reasons array

---

## Track 3: Evidence-First (T3)

### Files to Review

#### Core Implementation

- [ ] `apps/cli/main.go` — wireGenerationDeps function
  - Line 3259: Function signature
    - Change from: `func wireGenerationDeps(...)`
    - Change to: `func wireGenerationDeps(...) error`
    - Check: Callers updated to check error
  - Lines 3260-3263: Error handling
    - On `createLLMProvider()` failure:
      - Return `fmt.Errorf("LLM provider is required for Stage 6: %w", err)`
      - NO silent fallback (no fmt.Printf with "auto-stub fallback")
  - Callers at lines 529 and 669:
    - [ ] `if err := wireGenerationDeps(cmd, cfg, client); err != nil { return ... }`
    - [ ] Error message includes "Stage 6 requires LLM configuration"

- [ ] `libs/indexer-go/pipeline/stages.go` — GenerateContextDocsStage
  - Line 179: Optional() method
    - [ ] Returns `false` (not optional)
  - Line 182-184: Run() method nil guard
    - [ ] `if cfg.Generator == nil { return 0, fmt.Errorf(...) }`
    - [ ] Explicit error message
    - [ ] No fallback to auto-stub

#### Test Coverage
- [ ] `libs/indexer-go/pipeline/pipeline_test.go`
  - [ ] `TestGenerateContextDocsStage_NilGeneratorFails`
    - Creates stage with nil generator
    - Calls Run()
    - Verifies error is returned (not skipped)
  - [ ] `TestGenerateContextDocsStage_NotOptional`
    - Calls stage.Optional()
    - Verifies it returns false
  - [ ] `TestPipeline_Stage6AbortsPipeline`
    - Runs pipeline without generator
    - Verifies pipeline stops at Stage 6
    - Confirms error message includes "generator"

### Review Questions

1. **Is wireGenerationDeps returning error correctly?**
   - [ ] Signature changed to return error
   - [ ] Returns error on LLM failure (no fallback)
   - [ ] Error message is clear and actionable

2. **Are all callers checking the error?**
   - [ ] indexPipelineCmd caller (line 529) ✓
   - [ ] indexReplayCmd caller (line 669) ✓
   - [ ] Both propagate error upward

3. **Is Stage 6 truly non-optional?**
   - [ ] Optional() returns false
   - [ ] Run() has nil guard
   - [ ] Pipeline aborts on failure

4. **Do tests verify abort behavior?**
   - [ ] NilGeneratorFails test ✓
   - [ ] NotOptional test ✓
   - [ ] AbortsPipeline test ✓

---

## Integration Test (T4)

### Files to Review

- [ ] `T4_INTEGRATION_TEST.md`
  - [ ] Prerequisites documented
  - [ ] Step-by-step execution instructions
  - [ ] Cypher validation queries
  - [ ] Success criteria clearly defined
  - [ ] Troubleshooting section

- [ ] `BUGFIX_COMPLETION_SUMMARY.md`
  - [ ] Executive summary clear
  - [ ] Implementation details documented
  - [ ] Build & test results shown
  - [ ] Deployment checklist provided

- [ ] `scripts/T4_validate_scope.cypher`
  - [ ] Checks for double-prefixed nodes (must = 0)
  - [ ] Checks pr-audit scope nodes (must > 0)
  - [ ] Checks main scope nodes (must > 0)

- [ ] `scripts/T4_validate_provenance.cypher`
  - [ ] Checks for missing fields (must = 0)
  - [ ] Checks valid edges (must > 0)
  - [ ] Checks pr-audit MENTIONS (must > 0)

- [ ] `scripts/T4_validate_evidence_first.cypher`
  - [ ] Checks for auto-stub docs (must = 0)
  - [ ] Checks valid generated docs (must > 0)
  - [ ] Checks docs with citations (must > 0)

- [ ] `scripts/T4_integration_test.sh`
  - [ ] Executable and well-documented
  - [ ] Takes correct command-line arguments
  - [ ] Checks prerequisites
  - [ ] Runs pipeline with proper flags
  - [ ] Runs validation queries

---

## Cross-Track Verification

### No Silent Failures
- [ ] All error paths return explicit errors
- [ ] No fallback/retry logic
- [ ] All error messages are actionable

### No Heuristics
- [ ] Scope contract: requires explicit flag combinations
- [ ] Provenance: requires all 5 fields validated
- [ ] Evidence-first: requires LLM config (no auto-stub)

### Backward Compatibility
- [ ] CLI commands have same signatures (args only added)
- [ ] Neo4j schema unchanged
- [ ] Cypher queries work with existing databases
- [ ] No breaking API changes

### Performance Impact
- [ ] ParseScopeFlags() O(1)
- [ ] NormalizePRID() O(1)
- [ ] BuildMentionEdgeProps validation ~1ms per edge
- [ ] Overall <1% slowdown expected

### Test Coverage
- [ ] Unit tests: 100+ passing
- [ ] Integration test infrastructure: complete
- [ ] All code paths tested (happy + error)
- [ ] Edge cases covered

---

## Security Review

### No Command Injection
- [ ] No shell commands in string handling
- [ ] Scope values properly scoped (SQL/Cypher-safe)
- [ ] API key handling: passed directly, not logged

### No Information Disclosure
- [ ] Error messages don't leak sensitive data
- [ ] API keys not in logs
- [ ] Debug output doesn't expose internals

### Validation
- [ ] Input validation at all boundaries
- [ ] Type safety maintained
- [ ] No type coercion surprises

---

## Deployment Readiness

### Before Staging
- [ ] All code reviewed and approved
- [ ] All tests passing
- [ ] Documentation complete
- [ ] No open TODOs or FIXMEs

### Staging Testing
- [ ] Run T4 integration test
- [ ] Verify all Cypher validations pass
- [ ] Monitor logs for errors
- [ ] Test with real data

### Production Rollout
- [ ] Backup database
- [ ] Blue-green deployment preferred
- [ ] Monitor Stage 6 error rates
- [ ] Monitor MENTIONS edge counts
- [ ] Have rollback plan ready

---

## Final Checklist

### Code Quality ✅
- [ ] All 9 files reviewed
- [ ] No code duplication
- [ ] Naming is clear
- [ ] Comments explain non-obvious logic
- [ ] Error messages are helpful

### Testing ✅
- [ ] 100+ unit tests pass
- [ ] Integration test infrastructure complete
- [ ] No flaky tests
- [ ] Coverage includes error paths

### Documentation ✅
- [ ] README updated (if needed)
- [ ] T4 integration guide complete
- [ ] Code review checklist (this document)
- [ ] Deployment instructions clear

### Compliance ✅
- [ ] No silent failures
- [ ] No heuristics/workarounds
- [ ] No technical debt introduced
- [ ] Meets architectural standards

---

## Sign-Off

**Reviewed by:** __________________ **Date:** __________

**Approved by:** __________________ **Date:** __________

**Deployed to staging:** __________ **Date:** __________

**Deployed to production:** _______ **Date:** __________

---

## Notes

Use this section to document any concerns, questions, or deviations from the plan:

```
[Add notes here]
```

---

**Next Step:** Schedule code review meeting with all three track leads (Alpha, Beta, Gamma)
