# T4 Integration Test — Full Pipeline with All Fixes

This document describes the integration test for the three critical bug fixes:
1. **Scope Contract** - no double-prefixing
2. **Provenance Validation** - all MENTIONS edges valid
3. **Evidence-First** - no auto-stub generation without LLM

## Prerequisites

1. **Running Neo4j instance** (default: `bolt://localhost:7687`)
   ```bash
   make docker-up  # Starts Neo4j 5.15 with APOC
   ```

2. **LLM Configuration** - One of:
   ```bash
   export GEMINI_API_KEY=<your-gemini-api-key>
   # OR
   export LLM_PROVIDER=openai LLM_API_KEY=<key>
   # OR
   export LLM_PROVIDER=litellm LLM_BASE_URL=<url>
   ```

3. **Fresh Database** (optional but recommended)
   ```bash
   make db-reset
   ```

## Test Execution

### Step 1: Run Full Pipeline with PR Scope

Execute the pipeline with all fixes in place, using PR scope to test scope isolation:

```bash
./bin/codegraph index pipeline . \
  --service=codegraph-test \
  --scope=pr \
  --scope-id=pr-audit \
  --version=v1.0.0 \
  --provider=gemini \
  --api-key=$GEMINI_API_KEY \
  --verbose
```

**Expected behavior:**
- All 7 stages execute in order
- Stage 6 (GenerateContextDocs) completes (no auto-stub fallback)
- No double-prefixed scopeIds created
- Scope isolation enforced: pr-audit nodes isolated from main scope

### Step 2: Validate Scope Contract (Bug 1)

After pipeline completes, run these Cypher validations:

```cypher
// MUST return 0 — no double-prefixed scopeIds
MATCH (n) WHERE n.scopeId STARTS WITH 'pr-pr-'
RETURN count(n) AS double_prefixed_nodes
```

```cypher
// MUST return > 0 — pr-audit scope has nodes
MATCH (n) WHERE n.scopeId = 'pr-audit'
RETURN count(n) AS pr_audit_nodes
```

```cypher
// MUST return main nodes with no pr prefix
MATCH (n) WHERE n.scopeId = 'main'
RETURN count(n) AS main_nodes
```

### Step 3: Validate Provenance (Bug 2)

```cypher
// MUST return 0 — all MENTIONS edges have required fields
MATCH ()-[r:MENTIONS]->()
WHERE r.confidence IS NULL
   OR r.reasons IS NULL
   OR r.createdAt IS NULL
   OR (r.model IS NULL AND r.strategy IS NULL)
   OR r.scopeId IS NULL
RETURN count(r) AS invalid_mentions_edges
```

```cypher
// SHOULD return > 0 — valid MENTIONS edges exist
MATCH ()-[r:MENTIONS]->()
WHERE r.confidence IS NOT NULL
  AND r.reasons IS NOT NULL
  AND r.createdAt IS NOT NULL
  AND r.scopeId IS NOT NULL
RETURN count(r) AS valid_mentions_edges
```

```cypher
// pr-audit scope MENTIONS edges should exist
MATCH ()-[r:MENTIONS]->()
WHERE r.scopeId = 'pr-audit'
RETURN count(r) AS pr_audit_mentions
```

### Step 4: Validate Evidence-First (Bug 3)

```cypher
// MUST return 0 — no auto-stub docs in final graph
MATCH (gd:GeneratedDoc)
WHERE gd.model IN ['stage6-auto', 'auto-stub']
RETURN count(gd) AS stub_generated_docs
```

```cypher
// SHOULD return > 0 — valid generated docs exist for pr-audit scope
MATCH (gd:GeneratedDoc)
WHERE gd.scopeId = 'pr-audit'
  AND gd.model IS NOT NULL
  AND gd.model NOT IN ['stage6-auto', 'auto-stub']
RETURN count(gd) AS valid_generated_docs_in_scope
```

```cypher
// SHOULD return > 0 — pr-audit docs have citations (provenance)
MATCH (gd:GeneratedDoc)
WHERE gd.scopeId = 'pr-audit'
  AND gd.citations IS NOT NULL
  AND size(gd.citations) > 0
RETURN count(gd) AS docs_with_citations
```

### Step 5: Query Flows (Scope Double-Prefix Bug)

Verify that queryFlowsCmd now returns results with correct PR scope:

```bash
./bin/codegraph query flows --scope-id=pr-audit
```

**Expected behavior:**
- Returns > 0 flows (previously returned 0 due to double-prefix bug)
- All flows have scopeId = 'pr-audit' (not 'pr-pr-audit')

## Success Criteria (All Must Pass)

### Scope Contract (Bug 1)
- [ ] `double_prefixed_nodes` = 0
- [ ] `pr_audit_nodes` > 0
- [ ] `main_nodes` > 0
- [ ] `query flows --scope-id=pr-audit` returns > 0 flows

### Provenance Validation (Bug 2)
- [ ] `invalid_mentions_edges` = 0
- [ ] `valid_mentions_edges` > 0
- [ ] `pr_audit_mentions` > 0

### Evidence-First (Bug 3)
- [ ] `stub_generated_docs` = 0
- [ ] `valid_generated_docs_in_scope` > 0
- [ ] `docs_with_citations` > 0

### Overall Code Quality
- [ ] All 7 pipeline stages complete without error
- [ ] No auto-stub fallback occurs
- [ ] Stage 6 runs and produces documents (proves LLM config was used)
- [ ] All data maintains scope isolation

## Troubleshooting

### Issue: Neo4j Connection Failed
```bash
# Check Neo4j is running
docker ps | grep neo4j

# Restart if needed
make docker-down
make docker-up
```

### Issue: LLM Provider Error
```bash
# Verify API key is set
echo $GEMINI_API_KEY

# Check provider availability
./bin/codegraph index pipeline --help | grep -A5 "provider"
```

### Issue: Stage 6 Fails Without LLM Key
This is expected behavior! The fix requires LLM to be configured.
```bash
# Verify error message contains "required for Stage 6"
# If it says "auto-stub fallback", the fix is not applied
```

### Issue: Double-Prefixed Nodes Still Appear
This indicates the NormalizePRID() fix is not being called. Check:
```bash
grep -n "NormalizePRID" apps/cli/main.go
# Should show line ~1220 in queryFlowsCmd
```

## Files Modified for Fixes

### T0: Foundation
- `libs/core-models-go/scope.go` - ParseScopeFlags, NormalizePRID

### T1: Scope Contract
- `apps/cli/main.go` lines 484, 623, 1220 - scope guard integration

### T2: Provenance Validation
- `libs/search-go/chunk_linker.go` lines 181, 228
- `libs/search-go/flow_linker.go` line 163
- `libs/indexer-go/documents/indexer.go` line 487

### T3: Evidence-First
- `apps/cli/main.go` line 3259 - wireGenerationDeps returns error
- `libs/indexer-go/pipeline/stages.go` lines 179, 182 - Optional=false, nil guard

## Performance Notes

- Full pipeline run with SCIP indexing: ~2-5 minutes (depends on codebase size)
- Cypher validations: <100ms each
- Total integration test time: ~5-10 minutes including pipeline

## Next Steps After Passing

1. **Run on main branch pipeline**
   ```bash
   ./bin/codegraph index pipeline . --scope=main --service=codegraph --version=production
   ```

2. **Run on multiple PR scopes**
   ```bash
   for PR in 1 2 3 4 5; do
     ./bin/codegraph index pipeline . --scope=pr --scope-id=pr-$PR ...
   done
   ```

3. **Benchmark Cypher validation queries** for performance regression testing

4. **Archive test results** for compliance/audit trail
