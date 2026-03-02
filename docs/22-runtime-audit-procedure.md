# Runtime Audit Procedure (Manual, Evidence-First)

This document defines the repeatable runtime audit process for assessing implementation quality against pristine-state goals.

It is intentionally execution-heavy: run the pipeline, inspect outputs in Neo4j, manually review generated content quality, and identify performance bottlenecks from real stage timings.

---

## 1. Goals Of This Audit

Use this procedure to answer all of the following in one pass:

1. Is generated context actually being produced at runtime (not just passing unit tests)?
2. Are generated docs evidence-backed with statement-level citations?
3. Are verification/policy failures persisted as diagnostics (not silently dropped)?
4. Are inferred `MENTIONS` edges fully provenance-complete?
5. Are flow outputs high-signal, or still dominated by generic entrypoints?
6. Which pipeline stages are the runtime bottlenecks?

---

## 2. Safety Rules (Do This First)

1. Always use a fresh PR scope ID per audit run, for example `pr-audit-20260301-a`.
2. Never run benchmark commands before manual quality inspection (`benchmark` paths may wipe/reset DB state).
3. Keep each run isolated; do not compare quality using mixed scopes.
4. Save logs to files when possible so stage timings and warnings are queryable.

Recommended naming pattern:

- `--scope pr --scope-id pr-audit-<date>-<suffix>`

---

## 3. Preconditions

1. Neo4j and dependent services are up.
2. `./bin/codegraph` is built from the branch under audit.
3. LLM credentials are loaded (`.env` or environment variables).
4. Provider/model are explicitly set for deterministic behavior when needed.

Quick checks:

```bash
./bin/codegraph status
./bin/codegraph index pipeline --help
./bin/codegraph index replay --help
```

If you want to force OpenAI in runtime audits, set provider-related env explicitly before execution (for example `LLM_PROVIDER=openai`).

---

## 4. Primary Runtime Audit Pass

Run full pipeline with logs captured:

```bash
set -a; source .env; set +a
./bin/codegraph index pipeline . \
  --service codegraph \
  --version audit-runtime \
  --doc-paths docs \
  --scope pr \
  --scope-id pr-audit-<id> \
  > /tmp/codegraph-audit-<id>.log 2>&1
```

Extract stage timings:

```bash
rg "\[pipeline\] Stage|\[pipeline\] Running stage|Pipeline:" /tmp/codegraph-audit-<id>.log
```

If only later stages need re-checking:

```bash
set -a; source .env; set +a
./bin/codegraph index replay . \
  --stages LinkDocumentChunks,GenerateContextDocs,RefreshRetrievalIndexes \
  --service codegraph \
  --version audit-runtime \
  --doc-paths docs \
  --scope pr \
  --scope-id pr-audit-<id> \
  > /tmp/codegraph-audit-replay-<id>.log 2>&1
```

---

## 5. Graph Validation Queries

Use `docker exec neo4j-full cypher-shell -u neo4j -p password123 "..."` for all queries below.

### 5.1 GeneratedDoc Coverage And Citation Integrity

```cypher
MATCH (gd:GeneratedDoc {scopeId:'pr-audit-<id>'})
RETURN count(gd) AS total,
       count(CASE WHEN gd.statements IS NOT NULL AND size(gd.statements) > 0 THEN 1 END) AS withStatements,
       count(CASE WHEN gd.citations IS NOT NULL AND size(gd.citations) > 0 THEN 1 END) AS withCitations;
```

```cypher
MATCH (gd:GeneratedDoc {scopeId:'pr-audit-<id>'})
WHERE gd.citations IS NULL OR size(gd.citations)=0
RETURN gd.nodeKey, gd.type, gd.title
LIMIT 25;
```

### 5.2 Manual Content Quality Inspection

```cypher
MATCH (gd:GeneratedDoc {scopeId:'pr-audit-<id>'})
RETURN gd.type, gd.title, gd.model, gd.strategy, gd.content, gd.statements, gd.citations
LIMIT 25;
```

Review for:

1. Generic filler language not grounded in concrete repo facts.
2. Claims about security/performance/compliance without evidence.
3. Citation repetition to the same placeholder node only.
4. Missing task diversity (`pr_summary` only, no useful flow/docstring output).

### 5.3 Diagnostics Completeness

```cypher
MATCH (d:GenerationDiagnostic {scopeId:'pr-audit-<id>'})
RETURN count(d) AS total,
       count(CASE WHEN d.docType IS NULL OR d.docType='' THEN 1 END) AS missingDocType,
       count(CASE WHEN d.reason IS NULL OR d.reason='' THEN 1 END) AS missingReason;
```

```cypher
MATCH (d:GenerationDiagnostic {scopeId:'pr-audit-<id>'})
RETURN d.docType, d.sourceKey, d.reason, d.unsupportedClaims
LIMIT 25;
```

### 5.4 Provenance Completeness For MENTIONS

```cypher
MATCH ()-[r:MENTIONS]->()
WHERE r.scopeId='pr-audit-<id>' AND (
  r.scope IS NULL OR r.scopeId IS NULL OR
  r.confidence IS NULL OR r.reasons IS NULL OR size(coalesce(r.reasons,[]))=0 OR
  r.createdAt IS NULL OR r.strategy IS NULL OR
  r.evidenceRefs IS NULL OR size(coalesce(r.evidenceRefs,[]))=0
)
RETURN count(r) AS invalidMentions;
```

---

## 6. Flow Quality Audit

Regenerate and inspect flows manually:

```bash
time -p ./bin/codegraph query flows --generate --scope-id pr-audit-<id> --max-depth 3
```

Use these graph metrics for noise estimation:

```cypher
MATCH (f:Flow {scopeId:'pr-audit-<id>'})
OPTIONAL MATCH (f)-[:HAS_STEP]->(s)
WITH f, count(s) AS stepCount
RETURN count(f) AS total,
       count(CASE WHEN stepCount = 1 THEN 1 END) AS singleStep,
       avg(toFloat(stepCount)) AS avgSteps,
       count(CASE WHEN toLower(coalesce(f.name,'')) CONTAINS 'run'
                    OR toLower(coalesce(f.name,'')) CONTAINS 'main'
                    OR toLower(coalesce(f.name,'')) CONTAINS 'newclient'
                  THEN 1 END) AS genericNamed;
```

Interpretation guidance:

1. Very high `singleStep/total` ratio indicates weak traversal signal.
2. High `genericNamed` count indicates seed/ranking noise is still present.
3. Large flows rooted at `Run`/`main`/utility constructors are usually low-value for users.

---

## 7. Bottleneck Diagnosis

Use stage timings from logs to classify where runtime is spent.

Typical pattern to document:

1. Heavy indexing stages (`IngestCode`, `IngestDocuments`) are data-volume dominated.
2. Linking (`LinkDocumentChunks`) can become dominant after data exists.
3. Generation (`GenerateContextDocs`) is latency + provider health sensitive.

Add a simple table to audit notes:

- Stage name
- Duration
- Items processed
- Bottleneck class (`CPU`, `I/O`, `LLM`, `graph query fanout`)

---

## 8. Gap Classification Template

Classify every discovered issue into one of these buckets:

1. **Code defect**: behavior violates intended contract (for example diagnostics not persisted).
2. **Provider/config defect**: runtime unavailable due to provider or key issues.
3. **Quality policy gap**: output passes schema but fails usefulness standards.
4. **Approach gap**: current architecture/path can’t meet target quality without redesign.

For each gap, record:

1. Reproduction command/query.
2. Scope ID where observed.
3. Expected behavior.
4. Actual behavior.
5. Suggested fix path (code vs policy vs operations).

---

## 9. Exit Criteria For A Clean Audit Pass

A run is considered healthy only if all are true:

1. `GeneratedDoc` exists in the audited scope with statement-level citations.
2. Generated content is materially useful (not generic filler) on manual review.
3. Generation failures are persisted as diagnostics with complete triage fields.
4. `invalidMentions = 0` for audited scope.
5. Flow output avoids dominant generic entrypoints and excessive one-step noise.
6. Stage timings and bottlenecks are documented from actual runtime logs.

---

## 10. Common Failure Modes Seen In Practice

1. Provider mismatch (for example defaulting to Gemini while key is expired) causes Stage 6 to emit warnings and produce zero generated docs.
2. Warnings without persisted diagnostics break auditability and incident triage.
3. Minimal evidence bundles produce generic PR summaries that are technically cited but low-value.
4. Flow generation can still over-index on broad symbols (`Run`, `main`) if ranking/seed filters are too permissive.

This procedure should be run after any major indexing, linking, generation, verification, or flow-quality change.
