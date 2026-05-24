// Phase 0 — Cross-Service Stitching Diagnostics
// Run each query individually. Results tell you which root cause is active.
// PASS thresholds are approximate; tune based on service count.

// 0.1 — Are CALLS_SERVICE edges being written at all?
// PASS: count > 0 (expect >50 for a 5-service graph)
MATCH ()-[r:CALLS_SERVICE]->() RETURN count(r) AS calls_service_count;

// 0.2 — Target service distribution — broken-resolver signature shows imbalanced counts
// PASS: multiple rows with non-empty names; FAIL: single row or empty name dominating
MATCH (c:GRPCCall)-[:CALLS_SERVICE]->(s:Service)
RETURN s.name AS targetService, count(*) AS callCount
ORDER BY callCount DESC;

// 0.3 — GRPCCall nodes missing targetService resolution (unresolved calls)
// PASS: count = 0 (all calls resolved); FAIL: any count > 0 indicates resolver gaps
MATCH (c:GRPCCall) WHERE c.targetService IS NULL OR c.targetService = ""
RETURN count(c) AS unresolved_grpc_calls;

// 0.3b — GRPCCall nodes missing CALLS_SERVICE edge (not resolved to any service)
MATCH (c:GRPCCall) WHERE NOT (c)-[:CALLS_SERVICE]->()
RETURN count(c) AS grpccalls_without_service_edge;

// 0.4 — Did the cross-service resolver run and write RESOLVES_TO edges?
// PASS: count > 0 (expect >10 for a 5-service graph); FAIL: 0 = resolver never wrote anything
MATCH ()-[r:RESOLVES_TO]->() RETURN count(r) AS resolves_to_count;

// 0.4b — RESOLVES_TO confidence distribution
MATCH ()-[r:RESOLVES_TO]->()
RETURN r.resolutionMethod AS method, count(*) AS cnt, avg(r.confidence) AS avgConf
ORDER BY cnt DESC;

// 0.5 — Is enrichment data present on CALLS edges?
// PASS: count > 0; FAIL: 0 = call_metadata.go not writing data
MATCH ()-[r:CALLS]->() WHERE r.literalArgs IS NOT NULL
RETURN count(r) AS calls_with_literal_args;

// 0.6 — Placeholder/stub Function nodes (no filePath = synthetic, Phase 2.1 target)
// PASS: count = 0 after Phase 2 fix; FAIL: any count = placeholder noise present
MATCH (f:Function) WHERE f.isPlaceholder = true OR f.filePath IS NULL OR f.filePath = ""
RETURN count(f) AS placeholder_count, collect(f.name)[..10] AS samples;

// 0.7 — ProtoContract nodes (Phase 1.4 indicator)
// PASS: count > 0 after proto repo indexed; FAIL: 0 = proto contracts not indexed yet
MATCH (pc:ProtoContract) RETURN count(pc) AS proto_contract_count;

// 0.8 — TRANSITIVE_CALLS_API edges (Phase 1.5 helper collapse indicator)
MATCH ()-[r:TRANSITIVE_CALLS_API]->() RETURN count(r) AS transitive_calls_api_count;

// 0.9 — Services and their GRPCCall outbound call counts
MATCH (s:Service)-[:CONTAINS*1..5]->(fn:Function)-[:CALLS_API]->(c:GRPCCall)
RETURN s.name AS service, count(c) AS grpcCallSites
ORDER BY grpcCallSites DESC;

// 0.10 — Cross-service resolution parity: calls vs resolved
MATCH (c:GRPCCall)-[:CALLS_SERVICE]->(svc:Service)
OPTIONAL MATCH (c)-[:RESOLVES_TO]->(handler:Function)
RETURN svc.name AS targetService,
       count(c) AS totalCalls,
       count(handler) AS resolvedCalls,
       round(toFloat(count(handler)) / toFloat(count(c)) * 100, 1) AS pctResolved
ORDER BY totalCalls DESC;
