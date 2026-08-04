/**
 * Docs data layer (RFC-012 R5 / RFC-011) — typed wrappers around
 * codegraph_cypher that back the Docs screen: document list (service-filtered),
 * document detail (chunks + their MENTIONS out-links), reverse lookup (chunks
 * that MENTION a code node), and docs search (fulltext over title/content).
 *
 * Every query is read-only and label/rel-anchored so the planner never falls
 * back to an AllNodesScan (see queries in $lib/server/dashboard). The guardrail
 * `warnings` envelope is always propagated up, never dropped (RFC-012 failure
 * honesty). Pure helpers (query builders, family/band derivation, grouping) are
 * exported for unit testing without a live graph.
 */
import type { McpClient } from '$lib/server/mcp/client'
import type { ApiEnvelope } from '$lib/types/graph'
import type {
  DocChunk,
  DocDetail,
  DocListResponse,
  DocSearchResponse,
  DocSummary,
  LinkBand,
  LinkFamily,
  MentionLink,
  ReverseMention,
  ReverseMentionsResponse
} from '$lib/types/docs'

export class ValidationError extends Error {
  constructor(message: string) {
    super(message)
    this.name = 'ValidationError'
  }
}

function requireNonEmptyString(v: unknown, field: string): string {
  if (typeof v !== 'string' || v.length === 0) {
    throw new ValidationError(`${field} is required and must be a non-empty string`)
  }
  return v
}

function clampInt(v: unknown, field: string, min: number, max: number, fallback: number): number {
  if (v === undefined || v === null) return fallback
  const n = typeof v === 'number' ? v : Number(v)
  if (!Number.isFinite(n) || !Number.isInteger(n)) {
    throw new ValidationError(`${field} must be an integer`)
  }
  if (n < min) return min
  if (n > max) return max
  return n
}

// ---------------------------------------------------------------------------
// provenance derivation (mirrors dashboard/build.ts family logic)

/**
 * A MENTIONS strategy string is "<family>/<detail>", e.g. "docmine/codespan",
 * "docmine/fence", "semlink/text-embedding-3-small". Only 'semlink' is treated
 * as the low-trust family; everything else (docmine/*, and any future miner) is
 * the higher-trust docmine family — same rule the dashboard uses.
 */
export function familyOf(strategy: string | null | undefined): LinkFamily {
  return (strategy ?? '').split('/')[0] === 'semlink' ? 'semlink' : 'docmine'
}

/**
 * Trust band from family + confidence. docmine links are code-token grounded
 * (confidence 0.7+ in practice) and band 'high'; semlink links are embedding
 * similarity and band by threshold — ≥0.6 'medium', below 'low'. Kept
 * conservative: nothing inferred is ever presented as ground truth, so even a
 * high-confidence semlink caps at 'medium'.
 */
export function bandOf(family: LinkFamily, confidence: number): LinkBand {
  if (family === 'docmine') return confidence >= 0.7 ? 'high' : 'medium'
  return confidence >= 0.6 ? 'medium' : 'low'
}

function toMentionLink(row: MentionRow): MentionLink {
  const strategy = row.strategy ?? ''
  const confidence = typeof row.confidence === 'number' ? row.confidence : 0
  const family = familyOf(strategy)
  return {
    nodeId: row.targetId,
    name: row.targetName,
    label: row.targetLabel,
    filePath: row.targetFile,
    strategy,
    confidence,
    family,
    band: bandOf(family, confidence)
  }
}

// ---------------------------------------------------------------------------
// generic cypher result envelope

interface CypherJsonResult<Row> {
  columns: string[]
  row_count: number
  rows: Row[] | null
  truncated?: boolean
}

async function runCypher<Row>(
  client: McpClient,
  query: string,
  params?: Record<string, unknown>
): Promise<ApiEnvelope<CypherJsonResult<Row>>> {
  const args: Record<string, unknown> = { query, format: 'json' }
  if (params && Object.keys(params).length > 0) args.params = params
  return client.callTool<CypherJsonResult<Row>>('codegraph_cypher', args)
}

// ---------------------------------------------------------------------------
// document list

interface DocSummaryRow {
  nodeId: string
  nodeKey: string
  title: string | null
  filePath: string | null
  service: string | null
  type: string | null
  chunkCount: number | null
}

/**
 * List documents, newest-titled first. Anchored on :Document (uses
 * document_service_idx when a service filter is present). $service is a
 * parameter, not interpolated — no injection surface, and the tool stays the
 * single authority on read-only enforcement.
 */
export function buildDocListQuery(service: string | null): string {
  const where = service ? 'WHERE d.serviceName = $service\n' : ''
  return `MATCH (d:Document)
${where}RETURN elementId(d) AS nodeId, d.nodeKey AS nodeKey, d.title AS title,
       d.filePath AS filePath, d.serviceName AS service, d.type AS type,
       d.chunkCount AS chunkCount
ORDER BY toLower(coalesce(d.title, d.filePath, '')) ASC`
}

function toDocSummary(row: DocSummaryRow): DocSummary {
  return {
    nodeId: row.nodeId,
    nodeKey: row.nodeKey,
    title: row.title ?? row.filePath ?? '(untitled)',
    filePath: row.filePath,
    service: row.service,
    type: row.type,
    chunkCount: typeof row.chunkCount === 'number' ? row.chunkCount : 0
  }
}

export interface ListDocumentsParams {
  service?: string | null
}

export async function listDocuments(
  client: McpClient,
  params: ListDocumentsParams = {}
): Promise<ApiEnvelope<DocListResponse>> {
  const service = params.service && params.service.length > 0 ? params.service : null
  const query = buildDocListQuery(service)
  const envelope = await runCypher<DocSummaryRow>(client, query, service ? { service } : undefined)
  const documents = (envelope.data.rows ?? []).map(toDocSummary)
  return { warnings: envelope.warnings, data: { documents } }
}

/**
 * Groups a document list by service — services sorted alphabetically, docs
 * kept in the incoming (title-sorted) order. Documents with a null service
 * bucket under '(unassigned)'. Pure — the left rail renders straight off this.
 */
// groupDocumentsByService moved to $lib/components/docs/grouping.ts — the
// docs page groups client-side, and nothing from $lib/server may reach the
// browser bundle.

// ---------------------------------------------------------------------------
// document detail (chunks + MENTIONS out-links)

interface ChunkRow {
  nodeId: string
  nodeKey: string
  headingPath: string | null
  chunkIndex: number | null
  content: string | null
}

interface MentionRow {
  chunkId: string
  targetId: string
  targetName: string | null
  targetLabel: string | null
  targetFile: string | null
  strategy: string | null
  confidence: number | null
}

/**
 * Chunks of one document, ordered by chunkIndex. Anchored via the doc's
 * elementId ($docId) then :HAS_CHUNK. Chunk content can be large; it's
 * returned so the middle/right panes render without a second round-trip.
 */
export const DOC_CHUNKS_QUERY = `MATCH (d:Document)-[:HAS_CHUNK]->(c:DocumentChunk)
WHERE elementId(d) = $docId
RETURN elementId(c) AS nodeId, c.nodeKey AS nodeKey, c.headingPath AS headingPath,
       c.chunkIndex AS chunkIndex, c.content AS content
ORDER BY coalesce(c.chunkIndex, 0) ASC`

/**
 * All MENTIONS out-links for the chunks of one document, in one round-trip.
 * Grouped back onto chunks in JS by chunkId. Ordered so higher-confidence
 * links surface first within a chunk.
 */
export const DOC_MENTIONS_QUERY = `MATCH (d:Document)-[:HAS_CHUNK]->(c:DocumentChunk)-[r:MENTIONS]->(t)
WHERE elementId(d) = $docId
RETURN elementId(c) AS chunkId, elementId(t) AS targetId, t.name AS targetName,
       labels(t)[0] AS targetLabel, coalesce(t.filePath, t.path) AS targetFile,
       r.strategy AS strategy, r.confidence AS confidence
ORDER BY r.confidence DESC`

/**
 * Assembles chunks + their mention rows into DocChunks. Mentions are grouped by
 * chunk elementId; a chunk with no links gets an empty array. Pure — separated
 * so the two-query fan-in is unit-testable.
 */
export function assembleChunks(chunkRows: ChunkRow[], mentionRows: MentionRow[]): DocChunk[] {
  const mentionsByChunk = new Map<string, MentionLink[]>()
  for (const m of mentionRows) {
    const list = mentionsByChunk.get(m.chunkId) ?? []
    list.push(toMentionLink(m))
    mentionsByChunk.set(m.chunkId, list)
  }
  return chunkRows.map((c) => ({
    nodeId: c.nodeId,
    nodeKey: c.nodeKey,
    headingPath: c.headingPath,
    chunkIndex: typeof c.chunkIndex === 'number' ? c.chunkIndex : 0,
    content: c.content ?? '',
    mentions: mentionsByChunk.get(c.nodeId) ?? []
  }))
}

export interface DocDetailParams {
  docId: string
}

export async function getDocumentDetail(
  client: McpClient,
  params: DocDetailParams
): Promise<ApiEnvelope<DocDetail>> {
  const docId = requireNonEmptyString(params.docId, 'docId')

  const [summaryEnv, chunksEnv, mentionsEnv] = await Promise.all([
    runCypher<DocSummaryRow>(
      client,
      `MATCH (d:Document) WHERE elementId(d) = $docId
RETURN elementId(d) AS nodeId, d.nodeKey AS nodeKey, d.title AS title,
       d.filePath AS filePath, d.serviceName AS service, d.type AS type,
       d.chunkCount AS chunkCount`,
      { docId }
    ),
    runCypher<ChunkRow>(client, DOC_CHUNKS_QUERY, { docId }),
    runCypher<MentionRow>(client, DOC_MENTIONS_QUERY, { docId })
  ])

  const summaryRow = (summaryEnv.data.rows ?? [])[0]
  if (!summaryRow) {
    throw new ValidationError(`no document with node id ${docId}`)
  }
  const document = toDocSummary(summaryRow)
  const chunks = assembleChunks(chunksEnv.data.rows ?? [], mentionsEnv.data.rows ?? [])

  const warnings = [
    ...new Set([...summaryEnv.warnings, ...chunksEnv.warnings, ...mentionsEnv.warnings])
  ]
  return { warnings, data: { document, chunks } }
}

// ---------------------------------------------------------------------------
// reverse lookup — chunks that MENTION a given code node

interface ReverseMentionRow {
  documentNodeId: string
  documentTitle: string | null
  documentService: string | null
  chunkNodeId: string
  headingPath: string | null
  chunkIndex: number | null
  strategy: string | null
  confidence: number | null
}

/**
 * Chunks (and their owning documents) that MENTION a code node identified by
 * elementId. Anchored on the target's elementId so it's a point lookup, not a
 * scan. Ordered highest-confidence first.
 */
export const REVERSE_MENTIONS_QUERY = `MATCH (d:Document)-[:HAS_CHUNK]->(c:DocumentChunk)-[r:MENTIONS]->(t)
WHERE elementId(t) = $nodeId
RETURN elementId(d) AS documentNodeId, d.title AS documentTitle,
       d.serviceName AS documentService, elementId(c) AS chunkNodeId,
       c.headingPath AS headingPath, c.chunkIndex AS chunkIndex,
       r.strategy AS strategy, r.confidence AS confidence
ORDER BY r.confidence DESC`

function toReverseMention(row: ReverseMentionRow): ReverseMention {
  const strategy = row.strategy ?? ''
  const confidence = typeof row.confidence === 'number' ? row.confidence : 0
  const family = familyOf(strategy)
  return {
    documentNodeId: row.documentNodeId,
    documentTitle: row.documentTitle ?? '(untitled)',
    documentService: row.documentService,
    chunkNodeId: row.chunkNodeId,
    headingPath: row.headingPath,
    chunkIndex: typeof row.chunkIndex === 'number' ? row.chunkIndex : 0,
    strategy,
    confidence,
    family,
    band: bandOf(family, confidence)
  }
}

export interface ReverseMentionsParams {
  nodeId: string
}

export async function getReverseMentions(
  client: McpClient,
  params: ReverseMentionsParams
): Promise<ApiEnvelope<ReverseMentionsResponse>> {
  const nodeId = requireNonEmptyString(params.nodeId, 'nodeId')
  const envelope = await runCypher<ReverseMentionRow>(client, REVERSE_MENTIONS_QUERY, { nodeId })
  const mentions = (envelope.data.rows ?? []).map(toReverseMention)
  return { warnings: envelope.warnings, data: { mentions } }
}

// ---------------------------------------------------------------------------
// docs search

interface SearchHitRow {
  nodeId: string
  nodeKey: string
  title: string | null
  filePath: string | null
  service: string | null
  matchedIn: 'title' | 'content'
  score: number | null
}

/**
 * Escapes a raw user query for a Lucene fulltext term. The codegraph fulltext
 * indexes are Lucene-backed; unescaped special chars ( +-&|!(){}[]^"~*?:\/ )
 * either throw a parse error or silently change semantics. We quote-wrap the
 * escaped phrase so multi-word queries match as a loose phrase rather than
 * erroring on the space. Empty input yields '' — callers guard against it.
 */
export function escapeLuceneQuery(raw: string): string {
  const escaped = raw.replace(/[+\-&|!(){}\[\]^"~*?:\\/]/g, '\\$&')
  return escaped
}

/**
 * Fulltext search over document title (via document_fulltext) plus document
 * content matched through its chunks (via documentchunk_fulltext), de-duplicated
 * to one hit per document with the best score. $q is the escaped Lucene query;
 * $service (optional) filters by owning service. Wildcard-suffixed so prefix
 * matches work ("docm" → "docmine").
 */
export function buildDocSearchQuery(service: string | null): string {
  const titleWhere = service ? 'WHERE d.serviceName = $service\n  ' : ''
  const contentWhere = service ? 'WHERE d.serviceName = $service\n  ' : ''
  return `CALL {
  CALL db.index.fulltext.queryNodes('document_fulltext', $q) YIELD node AS d, score
  ${titleWhere}RETURN d AS d, score AS score, 'title' AS matchedIn
  UNION
  CALL db.index.fulltext.queryNodes('documentchunk_fulltext', $q) YIELD node AS c, score
  MATCH (d:Document)-[:HAS_CHUNK]->(c)
  ${contentWhere}RETURN d AS d, score AS score, 'content' AS matchedIn
}
WITH d, matchedIn, score ORDER BY score DESC
WITH d, head(collect(matchedIn)) AS matchedIn, max(score) AS score
RETURN elementId(d) AS nodeId, d.nodeKey AS nodeKey, d.title AS title,
       d.filePath AS filePath, d.serviceName AS service, matchedIn, score
ORDER BY score DESC
LIMIT toInteger($limit)`
}

/**
 * CONTAINS fallback for when the fulltext indexes are absent (fresh graph,
 * dropped schema). Case-insensitive substring over title and content. Slower
 * (label scan) but correct; the route flags `fallback: true` so the UI can say
 * so. $needle is the lower-cased raw query.
 */
export function buildDocSearchFallbackQuery(service: string | null): string {
  const svcClause = service ? '\n  AND d.serviceName = $service' : ''
  return `MATCH (d:Document)
WITH d, toLower(coalesce(d.title, '')) AS t, toLower(coalesce(d.content, '')) AS body
WHERE (t CONTAINS $needle OR body CONTAINS $needle)${svcClause}
RETURN elementId(d) AS nodeId, d.nodeKey AS nodeKey, d.title AS title,
       d.filePath AS filePath, d.serviceName AS service,
       CASE WHEN t CONTAINS $needle THEN 'title' ELSE 'content' END AS matchedIn,
       null AS score
ORDER BY toLower(coalesce(d.title, '')) ASC
LIMIT toInteger($limit)`
}

function toSearchHit(row: SearchHitRow) {
  return {
    nodeId: row.nodeId,
    nodeKey: row.nodeKey,
    title: row.title ?? row.filePath ?? '(untitled)',
    filePath: row.filePath,
    service: row.service,
    matchedIn: row.matchedIn,
    score: typeof row.score === 'number' ? row.score : null
  }
}

export interface SearchDocsParams {
  query: string
  service?: string | null
  limit?: number
}

export async function searchDocs(
  client: McpClient,
  params: SearchDocsParams
): Promise<ApiEnvelope<DocSearchResponse>> {
  const raw = requireNonEmptyString(params.query, 'query').trim()
  if (raw.length === 0) throw new ValidationError('query must not be blank')
  const service = params.service && params.service.length > 0 ? params.service : null
  const limit = clampInt(params.limit, 'limit', 1, 100, 40)

  const luceneTerm = escapeLuceneQuery(raw)
  // A trailing wildcard turns the last token into a prefix match; skip it when
  // the query already ends in an operator we escaped, to avoid a double token.
  const q = `${luceneTerm}*`

  try {
    const envelope = await runCypher<SearchHitRow>(client, buildDocSearchQuery(service), {
      q,
      limit,
      ...(service ? { service } : {})
    })
    const hits = (envelope.data.rows ?? []).map(toSearchHit)
    return { warnings: envelope.warnings, data: { hits, fallback: false } }
  } catch (e) {
    // Fulltext index missing or Lucene parse error → CONTAINS fallback. Any
    // other tool error (timeout, process) re-throws for the route to map.
    if (!isMissingIndexError(e)) throw e
    const envelope = await runCypher<SearchHitRow>(client, buildDocSearchFallbackQuery(service), {
      needle: raw.toLowerCase(),
      limit,
      ...(service ? { service } : {})
    })
    const hits = (envelope.data.rows ?? []).map(toSearchHit)
    return { warnings: envelope.warnings, data: { hits, fallback: true } }
  }
}

/**
 * True when a tool error looks like a missing-fulltext-index (or its Lucene
 * parse) failure — the only case where the CONTAINS fallback is the right
 * response. Matched on message text since the MCP tool collapses Neo4j codes
 * into a tool-error string.
 */
export function isMissingIndexError(e: unknown): boolean {
  const msg = e instanceof Error ? e.message : String(e)
  return (
    /no such fulltext schema index/i.test(msg) ||
    /there is no such fulltext/i.test(msg) ||
    /unable to find valid index/i.test(msg) ||
    /fulltext.*not found/i.test(msg)
  )
}
