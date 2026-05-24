// Phase 3 Validator — Enrichment data present and surfaced
// Run after re-indexing.

// P3.1 — CALLS edges have orderIndex (enrichment was written)
MATCH ()-[r:CALLS]->()
WHERE r.orderIndex IS NOT NULL
RETURN CASE WHEN count(r) > 0 THEN 'PASS' ELSE 'WARN' END AS result,
       count(r) AS callsWithOrderIndex;

// P3.2 — CALLS edges have literalArgs (call_metadata.go running)
MATCH ()-[r:CALLS]->()
WHERE r.literalArgs IS NOT NULL
RETURN CASE WHEN count(r) > 0 THEN 'PASS' ELSE 'WARN' END AS result,
       count(r) AS callsWithLiteralArgs;

// P3.3 — ControlFlowScope nodes exist
MATCH (cfs:ControlFlowScope)
RETURN CASE WHEN count(cfs) > 0 THEN 'PASS' ELSE 'WARN' END AS result,
       count(cfs) AS controlFlowScopes;

// P3.4 — ConcurrentScope nodes exist (goroutines/errgroup detected)
MATCH (cs:ConcurrentScope)
RETURN CASE WHEN count(cs) > 0 THEN 'PASS' ELSE 'WARN' END AS result,
       count(cs) AS concurrentScopes;

// P3.5 — TxScope nodes exist (transactions detected)
MATCH (ts:TxScope)
RETURN CASE WHEN count(ts) > 0 THEN 'PASS' ELSE 'WARN' END AS result,
       count(ts) AS txScopes;

// P3.6 — CALLS edges have nearestComment (comment enrichment)
MATCH ()-[r:CALLS]->()
WHERE r.nearestComment IS NOT NULL AND r.nearestComment <> ''
RETURN CASE WHEN count(r) > 0 THEN 'PASS' ELSE 'WARN' END AS result,
       count(r) AS callsWithComment;

// P3.7 — Spot-check: a known RPC handler has downstream call structure
// Replace 'CreatePayout' with an actual RPC in your graph.
MATCH (f:Function)
WHERE toLower(f.name) CONTAINS 'createpayout'
OPTIONAL MATCH (f)-[:CALLS_API]->(c)
OPTIONAL MATCH (f)-[:CALLS*1..3]->(fn:Function)-[:CALLS_API]->(c2)
RETURN f.name AS handler,
       count(DISTINCT c) AS directCalls,
       count(DISTINCT c2) AS transitiveCalls,
       CASE WHEN count(DISTINCT c) + count(DISTINCT c2) > 0 THEN 'PASS' ELSE 'WARN' END AS result;
