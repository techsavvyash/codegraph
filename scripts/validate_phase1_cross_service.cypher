// Phase 1 Validator — Cross-Service Resolution
// Each query prints a PASS or FAIL label with counts.
// Run after re-indexing all services + proto repo.

// P1.1 — Every GRPCCall has protoService set
MATCH (c:GRPCCall)
RETURN CASE WHEN count(CASE WHEN c.protoService IS NULL OR c.protoService = '' THEN 1 END) = 0
            THEN 'PASS' ELSE 'FAIL' END AS result,
       count(c) AS totalGRPCCalls,
       count(CASE WHEN c.protoService IS NOT NULL AND c.protoService <> '' THEN 1 END) AS withProtoService;

// P1.2 — CALLS_SERVICE edge coverage (≥70% of GRPCCalls resolved to a service)
MATCH (c:GRPCCall)
OPTIONAL MATCH (c)-[:CALLS_SERVICE]->(svc:Service)
WITH count(c) AS total, count(svc) AS resolved
RETURN CASE WHEN total = 0 OR toFloat(resolved)/toFloat(total) >= 0.7
            THEN 'PASS' ELSE 'FAIL' END AS result,
       total, resolved,
       CASE WHEN total > 0 THEN round(toFloat(resolved)/toFloat(total)*100,1) ELSE 0 END AS pctResolved;

// P1.3 — RESOLVES_TO edges exist (resolver ran)
MATCH ()-[r:RESOLVES_TO]->()
RETURN CASE WHEN count(r) > 0 THEN 'PASS' ELSE 'FAIL' END AS result,
       count(r) AS resolvesToEdges;

// P1.4 — Proto-confidence RESOLVES_TO edges (basis='proto')
MATCH ()-[r:RESOLVES_TO]->()
WHERE r.resolutionMethod = 'proto'
RETURN CASE WHEN count(r) > 0 THEN 'PASS' ELSE 'WARN' END AS result,
       count(r) AS protoMatchedEdges;

// P1.5 — ProtoContract nodes indexed (proto repo was indexed)
MATCH (pc:ProtoContract)
RETURN CASE WHEN count(pc) > 0 THEN 'PASS' ELSE 'WARN' END AS result,
       count(pc) AS protoContracts;

// P1.6 — TRANSITIVE_CALLS_API edges (helper collapse ran)
MATCH ()-[r:TRANSITIVE_CALLS_API]->()
RETURN CASE WHEN count(r) > 0 THEN 'PASS' ELSE 'WARN' END AS result,
       count(r) AS transitiveEdges;

// P1.7 — Per-service RESOLVES_TO parity (no service at 0% when calls exist)
MATCH (c:GRPCCall)-[:CALLS_SERVICE]->(svc:Service)
OPTIONAL MATCH (c)-[:RESOLVES_TO]->(handler:Function)
WITH svc.name AS svc, count(c) AS calls, count(handler) AS resolved
WHERE calls > 0
RETURN svc, calls, resolved,
       round(toFloat(resolved)/toFloat(calls)*100,1) AS pctResolved,
       CASE WHEN resolved > 0 THEN 'PASS' ELSE 'FAIL' END AS result
ORDER BY pctResolved ASC;
