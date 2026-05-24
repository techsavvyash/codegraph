// Phase 2 Validator — Real-node-per-call-site, zero mock data
// Run after re-indexing.

// P2.1 — No placeholder Function nodes remain
MATCH (f:Function) WHERE f.isPlaceholder = true
RETURN CASE WHEN count(f) = 0 THEN 'PASS' ELSE 'FAIL' END AS result,
       count(f) AS placeholderCount,
       collect(f.name)[..10] AS samples;

// P2.2 — No Function nodes with filePath='' or NULL (external=synthetic)
// (Exclude built-in nodes that legitimately have no file)
MATCH (f:Function)
WHERE (f.filePath IS NULL OR f.filePath = '')
  AND f.isExternal IS NULL
RETURN CASE WHEN count(f) = 0 THEN 'PASS' ELSE 'FAIL' END AS result,
       count(f) AS syntheticFunctions,
       collect(f.name)[..10] AS samples;

// P2.3 — GRPCCall nodeKey uniqueness check (no duplicates)
MATCH (c:GRPCCall)
WITH c.nodeKey AS k, count(*) AS cnt
WHERE cnt > 1
RETURN CASE WHEN count(k) = 0 THEN 'PASS' ELSE 'FAIL' END AS result,
       count(k) AS duplicateNodeKeys,
       collect(k)[..5] AS samples;

// P2.4 — DBCall nodeKey uniqueness check
MATCH (c:DBCall)
WITH c.nodeKey AS k, count(*) AS cnt
WHERE cnt > 1
RETURN CASE WHEN count(k) = 0 THEN 'PASS' ELSE 'FAIL' END AS result,
       count(k) AS duplicateNodeKeys;

// P2.5 — All GRPCCall nodes have originFile (filePath set)
MATCH (c:GRPCCall)
WHERE c.filePath IS NULL OR c.filePath = ''
RETURN CASE WHEN count(c) = 0 THEN 'PASS' ELSE 'FAIL' END AS result,
       count(c) AS callsWithoutFile;

// P2.6 — All GRPCCall nodes have line > 0
MATCH (c:GRPCCall)
WHERE c.line IS NULL OR c.line <= 0
RETURN CASE WHEN count(c) = 0 THEN 'PASS' ELSE 'FAIL' END AS result,
       count(c) AS callsWithoutLine;
