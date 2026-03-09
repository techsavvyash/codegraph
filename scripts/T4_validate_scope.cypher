// T4 Integration Test: Scope Contract Validation
//
// This validates that Bug 1 (scope contract) is fixed:
// - No double-prefixed scopeIds (pr-pr-*)
// - PR scope nodes are isolated
// - Main scope nodes are present

// FAIL: Must return 0 — no double-prefixed scopeIds
MATCH (n) WHERE n.scopeId STARTS WITH 'pr-pr-'
RETURN 'FAIL: double-prefixed scopeIds' AS check, count(n) AS count
UNION ALL

// PASS: Must return > 0 — pr-audit scope has nodes
MATCH (n) WHERE n.scopeId = 'pr-audit'
RETURN 'PASS: pr-audit scoped nodes' AS check, count(n) AS count
UNION ALL

// PASS: Must return > 0 — main scope nodes exist
MATCH (n) WHERE n.scopeId = 'main'
RETURN 'PASS: main scoped nodes' AS check, count(n) AS count
UNION ALL

// DETAIL: Node type distribution in pr-audit scope
MATCH (n) WHERE n.scopeId = 'pr-audit'
RETURN 'INFO: node types in pr-audit' AS check, labels(n)[0] AS nodeType, count(n) AS count
ORDER BY count DESC
