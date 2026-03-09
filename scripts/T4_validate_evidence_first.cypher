// T4 Integration Test: Evidence-First Validation (Bug 3)
//
// This validates that Stage 6 does not create auto-stub documents:
// - No GeneratedDocs with model='stage6-auto' or 'auto-stub'
// - All generated docs have valid model values
// - GeneratedDocs have citations (provenance)

// FAIL: Must return 0 — no auto-stub generated docs
MATCH (gd:GeneratedDoc)
WHERE gd.model IN ['stage6-auto', 'auto-stub']
RETURN 'FAIL: auto-stub GeneratedDocs' AS check, count(gd) AS count
UNION ALL

// PASS: Must return > 0 — valid generated docs exist
MATCH (gd:GeneratedDoc)
WHERE gd.model IS NOT NULL
  AND gd.model NOT IN ['stage6-auto', 'auto-stub']
RETURN 'PASS: valid GeneratedDocs' AS check, count(gd) AS count
UNION ALL

// PASS: Must return > 0 — pr-audit scope has generated docs
MATCH (gd:GeneratedDoc)
WHERE gd.scopeId = 'pr-audit'
RETURN 'PASS: GeneratedDocs in pr-audit scope' AS check, count(gd) AS count
UNION ALL

// PASS: GeneratedDocs with citations (citations provided by LLM generation)
MATCH (gd:GeneratedDoc)
WHERE gd.scopeId = 'pr-audit'
  AND gd.citations IS NOT NULL
  AND size(gd.citations) > 0
RETURN 'PASS: GeneratedDocs with citations in pr-audit' AS check, count(gd) AS count
UNION ALL

// DETAIL: GeneratedDoc model distribution
MATCH (gd:GeneratedDoc)
WHERE gd.scopeId = 'pr-audit'
RETURN 'INFO: GeneratedDoc models in pr-audit' AS check, gd.model AS model, count(gd) AS count
ORDER BY count DESC
UNION ALL

// FAIL: Check for docs with null model in pr-audit (should not happen with valid Stage 6)
MATCH (gd:GeneratedDoc)
WHERE gd.scopeId = 'pr-audit'
  AND gd.model IS NULL
RETURN 'FAIL: GeneratedDocs with null model in pr-audit' AS check, count(gd) AS count
UNION ALL

// DETAIL: Citation count distribution
MATCH (gd:GeneratedDoc)
WHERE gd.scopeId = 'pr-audit'
  AND gd.citations IS NOT NULL
RETURN 'INFO: citation counts in pr-audit docs' AS check, size(gd.citations) AS citation_count, count(gd) AS count
ORDER BY citation_count DESC
