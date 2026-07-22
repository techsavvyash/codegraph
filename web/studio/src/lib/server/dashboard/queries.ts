/**
 * Cypher query strings for the Studio dashboard (RFC-012 R7) plus the raw
 * row shapes each one returns. Every query is label- or relationship-type
 * anchored so the planner never falls back to an AllNodesScan.
 *
 * Content labels carrying `serviceName` (see MEMORY: verified graph facts).
 * Kept in one place so the per-label query and its row type can't drift.
 */
export const CONTENT_LABELS = [
  'File',
  'Function',
  'Method',
  'Class',
  'Interface',
  'Variable',
  'Symbol',
  'Document',
  'DocumentChunk',
  'Reference',
  'Module'
] as const

export type ContentLabel = (typeof CONTENT_LABELS)[number]

/** Relationship types counted for the global totals. */
export const EDGE_TYPES = [
  'CALLS',
  'CONTAINS',
  'DEFINES',
  'REFERENCES',
  'MENTIONS',
  'HAS_CHUNK',
  'EXPOSES_API',
  'IMPLEMENTS',
  'INHERITS_FROM',
  'DEPENDS_ON'
] as const

export type EdgeType = (typeof EDGE_TYPES)[number]

export interface ServiceRow {
  name: string
  scopeId: string | null
  language: string | null
  version: string | null
  repositoryUrl: string | null
  packageName: string | null
}

export const SERVICES_QUERY = `
MATCH (s:Service)
RETURN s.name AS name, s.scopeId AS scopeId, s.language AS language,
       s.version AS version, s.repositoryUrl AS repositoryUrl, s.packageName AS packageName
`

export interface NodesByLabelRow {
  label: ContentLabel
  svc: string | null
  c: number
}

/** UNION ALL over the content labels — each arm is anchored on its own label. */
export const NODES_BY_LABEL_QUERY = CONTENT_LABELS.map(
  (label) => `MATCH (n:${label}) RETURN '${label}' AS label, n.serviceName AS svc, count(*) AS c`
).join('\nUNION ALL\n')

export interface EdgesByTypeRow {
  t: EdgeType
  c: number
}

/** UNION ALL over relationship types — hits the count store, no warning. */
export const EDGES_BY_TYPE_QUERY = EDGE_TYPES.map(
  (t) => `MATCH ()-[r:${t}]->() RETURN '${t}' AS t, count(r) AS c`
).join('\nUNION ALL\n')

export interface CallsPerServiceRow {
  svc: string | null
  c: number
}

export const CALLS_PER_SERVICE_QUERY = `
MATCH (a:Function|Method)-[r:CALLS]->()
RETURN a.serviceName AS svc, count(r) AS c
`

export interface MentionsPerServiceFamilyRow {
  svc: string | null
  family: string | null
  c: number
}

export const MENTIONS_PER_SERVICE_FAMILY_QUERY = `
MATCH (c:DocumentChunk)-[r:MENTIONS]-()
RETURN c.serviceName AS svc, split(r.strategy, '/')[0] AS family, count(*) AS c
`

export interface SemanticStateRow {
  svc: string | null
  chunks: number
  embedded: number
  dims: number | null
  embeddingModel: string | null
  semlinkModel: string | null
  threshold: number | null
}

export const SEMANTIC_STATE_QUERY = `
MATCH (c:DocumentChunk)
RETURN c.serviceName AS svc,
       count(*) AS chunks,
       count(c.embedding) AS embedded,
       max(size(c.embedding)) AS dims,
       [m IN collect(DISTINCT c.embeddingModel) WHERE m IS NOT NULL][0] AS embeddingModel,
       [m IN collect(DISTINCT c.semlinkModel) WHERE m IS NOT NULL][0] AS semlinkModel,
       max(c.semlinkThreshold) AS threshold
`

export interface FlowsPerServiceRow {
  svc: string | null
  flows: number
}

export const FLOWS_PER_SERVICE_QUERY = `
MATCH (f:Flow)
WITH f.entrypointKey AS k
MATCH (n:Function|Method)
WHERE n.nodeKey = k
RETURN n.serviceName AS svc, count(*) AS flows
`

export interface ApiRoutesPerServiceRow {
  svc: string | null
  c: number
}

export const API_ROUTES_PER_SERVICE_QUERY = `
MATCH (a:APIRoute)-[:EXPOSES_API]-(n)
RETURN n.serviceName AS svc, count(DISTINCT a) AS c
`

export interface CallHubRow {
  name: string
  serviceName: string | null
  label: 'Function' | 'Method'
  inDegree: number
}

export const CALL_HUBS_QUERY = `
MATCH (n:Function|Method)
WHERE n.inDegree IS NOT NULL AND n.inDegree > 0
RETURN n.name AS name, n.serviceName AS serviceName, labels(n)[0] AS label, n.inDegree AS inDegree
ORDER BY n.inDegree DESC
LIMIT 8
`

export interface RecentDocLinkRow {
  docPath: string | null
  headingPath: string | null
  strategy: string | null
  confidence: number | null
  createdAt: string | null
  targetName: string | null
}

/**
 * Document -> DocumentChunk -> (MENTIONS) -> target code node, most recent
 * first. `docPath` tries `filePath` first (the likely property per the
 * brief) and falls back to `path`.
 */
export const RECENT_DOC_LINKS_QUERY = `
MATCH (d:Document)-[:HAS_CHUNK]->(c:DocumentChunk)-[r:MENTIONS]->(t)
RETURN coalesce(d.filePath, d.path) AS docPath,
       c.headingPath AS headingPath,
       r.strategy AS strategy,
       r.confidence AS confidence,
       toString(r.createdAt) AS createdAt,
       t.name AS targetName
ORDER BY r.createdAt DESC
LIMIT 6
`
