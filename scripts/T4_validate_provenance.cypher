// T4 Integration Test: Provenance Validation (Bug 2)
//
// This validates that all MENTIONS edges have required provenance fields:
// - confidence
// - reasons
// - model (or strategy)
// - createdAt
// - scopeId

// FAIL: Must return 0 — all MENTIONS edges must have required fields
MATCH ()-[r:MENTIONS]->()
WHERE r.confidence IS NULL
   OR r.reasons IS NULL
   OR r.createdAt IS NULL
   OR (r.model IS NULL AND r.strategy IS NULL)
   OR r.scopeId IS NULL
RETURN 'FAIL: MENTIONS edges missing provenance fields' AS check, count(r) AS count
UNION ALL

// PASS: Must return > 0 — valid MENTIONS edges exist
MATCH ()-[r:MENTIONS]->()
WHERE r.confidence IS NOT NULL
  AND r.reasons IS NOT NULL
  AND r.createdAt IS NOT NULL
  AND r.scopeId IS NOT NULL
RETURN 'PASS: valid MENTIONS edges' AS check, count(r) AS count
UNION ALL

// PASS: MENTIONS edges in pr-audit scope
MATCH ()-[r:MENTIONS]->()
WHERE r.scopeId = 'pr-audit'
RETURN 'PASS: pr-audit scoped MENTIONS edges' AS check, count(r) AS count
UNION ALL

// DETAIL: MENTIONS edge model distribution in pr-audit
MATCH ()-[r:MENTIONS]->()
WHERE r.scopeId = 'pr-audit'
RETURN 'INFO: MENTIONS model types in pr-audit' AS check, coalesce(r.model, r.strategy, 'unknown') AS model, count(r) AS count
ORDER BY count DESC
UNION ALL

// FAIL: Check for invalid confidence values (should be 0-1)
MATCH ()-[r:MENTIONS]->()
WHERE r.confidence IS NOT NULL AND (r.confidence < 0 OR r.confidence > 1)
RETURN 'FAIL: MENTIONS with invalid confidence values' AS check, count(r) AS count
UNION ALL

// DETAIL: Confidence value distribution
MATCH ()-[r:MENTIONS]->()
WHERE r.confidence IS NOT NULL
RETURN 'INFO: confidence value distribution' AS check, r.confidence AS confidence, count(r) AS count
ORDER BY confidence
