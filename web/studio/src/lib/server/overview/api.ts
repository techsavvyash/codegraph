/**
 * Overview data layer — typed wrappers around codegraph_cypher backing the
 * whole-service visualizer on /graph. Two calls:
 *   fetchOverview(service, scope)  → every File + its symbol count, plus the
 *     file→file call graph (function-level CALLS and module-scope File-CALLS
 *     aggregated to file pairs, merged into one FileEdge per direction).
 *   fetchFileSymbols(fileNodeId)   → the Function/Method symbols in one file
 *     with their same-service out-calls and fan-in count (the lazy drilldown).
 *
 * Every query is read-only and label/rel-anchored so the planner never falls
 * back to an AllNodesScan (File carries the file_service_idx via serviceName).
 * The guardrail `warnings` envelope is always propagated, never dropped.
 * Pure helpers (query strings, row→model mappers, edge merge) are exported for
 * unit testing without a live graph.
 */
import type { McpClient } from '$lib/server/mcp/client'
import type { ApiEnvelope } from '$lib/types/graph'
import type {
  DeadCounts,
  DeadEntry,
  DeadReportResponse,
  FileEdge,
  FileSymbol,
  FileSymbolsResponse,
  OverviewFile,
  OverviewResponse,
  SymbolCaller,
  SymbolCallersResponse,
  SymbolOutCall
} from '$lib/types/overview'

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

// ---------------------------------------------------------------------------
// generic cypher result envelope (mirrors docs/api.ts)

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
  // row_limit must match PAGE_SIZE: the tool's default clamp is 100 rows, which
  // silently truncates a whole-service query (codegraph alone has 220 files).
  const args: Record<string, unknown> = { query, format: 'json', row_limit: PAGE_SIZE }
  if (params && Object.keys(params).length > 0) args.params = params
  return client.callTool<CypherJsonResult<Row>>('codegraph_cypher', args)
}

// The MCP cypher tool clamps row_limit to at most 1000, so whole-service result
// sets are fetched in SKIP/LIMIT pages. MAX_PAGES is a runaway backstop far
// above any real service (30k files/edge-pairs); hitting it surfaces a warning
// instead of silently dropping the tail.
const PAGE_SIZE = 1000
const MAX_PAGES = 30

/**
 * Fetches every row of a query by paging with SKIP/LIMIT. The query MUST end in
 * a deterministic ORDER BY — SKIP over an unordered result may repeat or drop
 * rows between pages. toInteger() because MCP params arrive as JS floats.
 */
async function fetchAllRows<Row>(
  client: McpClient,
  orderedQuery: string,
  params: Record<string, unknown>,
  what: string
): Promise<{ rows: Row[]; warnings: string[] }> {
  const rows: Row[] = []
  const warnings: string[] = []
  for (let pageIdx = 0; pageIdx < MAX_PAGES; pageIdx += 1) {
    const env = await runCypher<Row>(
      client,
      `${orderedQuery}\nSKIP toInteger($offset) LIMIT toInteger($pageSize)`,
      { ...params, offset: pageIdx * PAGE_SIZE, pageSize: PAGE_SIZE }
    )
    warnings.push(...env.warnings)
    const page = env.data.rows ?? []
    rows.push(...page)
    if (page.length < PAGE_SIZE) return { rows, warnings: [...new Set(warnings)] }
  }
  warnings.push(`${what} truncated at ${MAX_PAGES * PAGE_SIZE} rows`)
  return { rows, warnings: [...new Set(warnings)] }
}

// ---------------------------------------------------------------------------
// overview: files + file-pair call graph

interface FileRow {
  nodeId: string
  path: string | null
  language: string | null
  lineCount: number | null
  symbolCount: number | null
}

interface EdgeRow {
  fromPath: string | null
  toPath: string | null
  weight: number | null
}

/**
 * Files of one service with their contained Function/Method count. Anchored on
 * (:File {serviceName, scopeId}) so it's an index seek. The symbol count comes
 * from an OPTIONAL MATCH so files with zero symbols still appear.
 */
export const OVERVIEW_FILES_QUERY = `MATCH (f:File {serviceName: $service, scopeId: $scope})
OPTIONAL MATCH (f)-[:CONTAINS]->(fn) WHERE fn:Function OR fn:Method
WITH f, count(fn) AS symbolCount
RETURN elementId(f) AS nodeId, f.path AS path, f.language AS language,
       f.lineCount AS lineCount, symbolCount
ORDER BY path`

/**
 * Function-level CALLS aggregated to file pairs: a call from a symbol in f1 to a
 * symbol in f2 (both in-service) contributes one to weight(f1,f2). Self-pairs
 * excluded (f1 <> f2) — intra-file calls carry no cross-file signal.
 */
export const OVERVIEW_FN_EDGES_QUERY = `MATCH (f1:File {serviceName: $service, scopeId: $scope})-[:CONTAINS]->(a)
      -[:CALLS]->(b)<-[:CONTAINS]-(f2:File {serviceName: $service, scopeId: $scope})
WHERE f1 <> f2
RETURN f1.path AS fromPath, f2.path AS toPath, count(*) AS weight
ORDER BY fromPath, toPath`

/**
 * Module-scope File-CALLS aggregated to file pairs: a File node calling a symbol
 * contained in another file (load-time / module-scope calls). Kept separate from
 * function-level weight so the client can keep it at the aggregate level even
 * when a file is drilled into.
 */
export const OVERVIEW_MODULE_EDGES_QUERY = `MATCH (f1:File {serviceName: $service, scopeId: $scope})-[:CALLS]->(b)
      <-[:CONTAINS]-(f2:File {serviceName: $service, scopeId: $scope})
WHERE f1 <> f2
RETURN f1.path AS fromPath, f2.path AS toPath, count(*) AS weight
ORDER BY fromPath, toPath`

function toOverviewFile(row: FileRow): OverviewFile {
  return {
    nodeId: row.nodeId,
    path: row.path ?? '',
    language: row.language,
    lineCount: typeof row.lineCount === 'number' ? row.lineCount : 0,
    symbolCount: typeof row.symbolCount === 'number' ? row.symbolCount : 0
  }
}

/**
 * Merges the two edge query results into one FileEdge per directed pair: the fn
 * query fills fnWeight, the module query fills moduleWeight, missing side is 0.
 * Pure — the fan-in of two aggregations is unit-testable in isolation.
 */
export function mergeEdges(fnRows: EdgeRow[], moduleRows: EdgeRow[]): FileEdge[] {
  const byPair = new Map<string, FileEdge>()
  const key = (from: string, to: string) => `${from}\u0000${to}`

  const ensure = (from: string, to: string): FileEdge => {
    const k = key(from, to)
    let e = byPair.get(k)
    if (!e) {
      e = { fromPath: from, toPath: to, fnWeight: 0, moduleWeight: 0 }
      byPair.set(k, e)
    }
    return e
  }

  for (const r of fnRows) {
    if (!r.fromPath || !r.toPath) continue
    ensure(r.fromPath, r.toPath).fnWeight = typeof r.weight === 'number' ? r.weight : 0
  }
  for (const r of moduleRows) {
    if (!r.fromPath || !r.toPath) continue
    ensure(r.fromPath, r.toPath).moduleWeight = typeof r.weight === 'number' ? r.weight : 0
  }
  return [...byPair.values()]
}

export interface FetchOverviewParams {
  service: string
  scopeId: string
}

export async function fetchOverview(
  client: McpClient,
  params: FetchOverviewParams
): Promise<ApiEnvelope<OverviewResponse>> {
  const service = requireNonEmptyString(params.service, 'service')
  const scope = requireNonEmptyString(params.scopeId, 'scope')

  const [filesRes, fnRes, modRes] = await Promise.all([
    fetchAllRows<FileRow>(client, OVERVIEW_FILES_QUERY, { service, scope }, 'file list'),
    fetchAllRows<EdgeRow>(client, OVERVIEW_FN_EDGES_QUERY, { service, scope }, 'call edges'),
    fetchAllRows<EdgeRow>(client, OVERVIEW_MODULE_EDGES_QUERY, { service, scope }, 'module edges')
  ])

  const files = filesRes.rows.map(toOverviewFile)
  const edges = mergeEdges(fnRes.rows, modRes.rows)

  const warnings = [...new Set([...filesRes.warnings, ...fnRes.warnings, ...modRes.warnings])]
  return { warnings, data: { service, files, edges } }
}

// ---------------------------------------------------------------------------
// file drilldown: symbols + their calls

interface SymbolRow {
  nodeId: string
  name: string | null
  label: string | null
  startLine: number | null
  inCalls: number | null
  sameSvcOut: Array<{ targetId: string; targetName: string | null; targetPath: string | null }> | null
  externalOutCalls: number | null
}

interface FileHeadRow {
  nodeId: string
  path: string | null
}

/**
 * The Function/Method symbols in one file (by elementId) with, per symbol:
 * its distinct incoming CALLS count (any source), its same-service out-calls
 * with target identity (for symbol→symbol edges), and a count of out-calls to
 * other services. The two subqueries keep out-collection and in-count from
 * fanning into a cartesian product.
 */
export const FILE_SYMBOLS_QUERY = `MATCH (f:File) WHERE elementId(f) = $id
WITH f, f.serviceName AS svc
MATCH (f)-[:CONTAINS]->(fn) WHERE fn:Function OR fn:Method
CALL {
  WITH fn
  OPTIONAL MATCH (fn)-[:CALLS]->(t)<-[:CONTAINS]-(tf:File)
  WITH t, tf WHERE t IS NOT NULL
  RETURN collect({targetId: elementId(t), targetName: t.name, targetPath: tf.path, targetService: tf.serviceName}) AS outs
}
CALL {
  WITH fn
  OPTIONAL MATCH (src)-[:CALLS]->(fn)
  RETURN count(DISTINCT src) AS inCalls
}
RETURN elementId(fn) AS nodeId, fn.name AS name, labels(fn)[0] AS label, fn.startLine AS startLine,
       inCalls,
       [o IN outs WHERE o.targetService = svc] AS sameSvcOut,
       size([o IN outs WHERE o.targetService <> svc]) AS externalOutCalls
ORDER BY coalesce(fn.startLine, 0) ASC, name ASC`

export const FILE_HEAD_QUERY = `MATCH (f:File) WHERE elementId(f) = $id
RETURN elementId(f) AS nodeId, f.path AS path`

function toFileSymbol(row: SymbolRow): FileSymbol {
  const outCalls: SymbolOutCall[] = (row.sameSvcOut ?? [])
    .filter((o) => o.targetId && o.targetPath)
    .map((o) => ({
      targetId: o.targetId,
      targetName: o.targetName ?? '(anonymous)',
      targetPath: o.targetPath as string
    }))
  return {
    nodeId: row.nodeId,
    name: row.name ?? '(anonymous)',
    // labels(fn)[0] is Function or Method; default to Function for safety
    label: row.label === 'Method' ? 'Method' : 'Function',
    startLine: typeof row.startLine === 'number' ? row.startLine : 0,
    inCalls: typeof row.inCalls === 'number' ? row.inCalls : 0,
    outCalls,
    externalOutCalls: typeof row.externalOutCalls === 'number' ? row.externalOutCalls : 0
  }
}

export interface FetchFileSymbolsParams {
  fileNodeId: string
}

export async function fetchFileSymbols(
  client: McpClient,
  params: FetchFileSymbolsParams
): Promise<ApiEnvelope<FileSymbolsResponse>> {
  const id = requireNonEmptyString(params.fileNodeId, 'node_id')

  const [headEnv, symRes] = await Promise.all([
    runCypher<FileHeadRow>(client, FILE_HEAD_QUERY, { id }),
    fetchAllRows<SymbolRow>(client, FILE_SYMBOLS_QUERY, { id }, 'file symbols')
  ])

  const head = (headEnv.data.rows ?? [])[0]
  if (!head) {
    throw new ValidationError(`no file with node id ${id}`)
  }
  const symbols = symRes.rows.map(toFileSymbol)

  const warnings = [...new Set([...headEnv.warnings, ...symRes.warnings])]
  return {
    warnings,
    data: { file: { nodeId: head.nodeId, path: head.path ?? '' }, symbols }
  }
}

// ---------------------------------------------------------------------------
// symbol callers (Usage lens drilldown): 1-hop callers of one symbol

interface CallerRow {
  name: string | null
  label: string | null
  filePath: string | null
  service: string | null
}

/**
 * The direct callers of one node (by elementId). Module-scope callers are File
 * nodes calling the target directly, so the caller's file path is coalesced onto
 * the caller's own path (a File caller has no CONTAINS parent File). ORDER BY is
 * present and the row set is 1-hop, so a single row_limit=500 page suffices — no
 * SKIP/LIMIT paging needed.
 */
export const SYMBOL_CALLERS_QUERY = `MATCH (t) WHERE elementId(t) = $id
MATCH (caller)-[:CALLS]->(t)
OPTIONAL MATCH (f:File)-[:CONTAINS]->(caller)
RETURN caller.name AS name,
       head(labels(caller)) AS label,
       coalesce(f.path, caller.path) AS filePath,
       coalesce(f.serviceName, caller.serviceName) AS service
ORDER BY filePath, name`

function toSymbolCaller(row: CallerRow): SymbolCaller {
  return {
    name: row.name ?? '(anonymous)',
    label: row.label ?? 'Function',
    filePath: row.filePath ?? '',
    service: row.service ?? null
  }
}

export interface FetchSymbolCallersParams {
  nodeId: string
}

export async function fetchSymbolCallers(
  client: McpClient,
  params: FetchSymbolCallersParams
): Promise<ApiEnvelope<SymbolCallersResponse>> {
  const id = requireNonEmptyString(params.nodeId, 'node_id')
  const env = await runCypher<CallerRow>(client, SYMBOL_CALLERS_QUERY, { id })
  const callers = (env.data.rows ?? []).map(toSymbolCaller)
  return { warnings: [...new Set(env.warnings)], data: { callers } }
}

// ---------------------------------------------------------------------------
// dead-code report (Dead lens): RFC-014 reachability via codegraph_deadcode

/**
 * The raw shape the codegraph_deadcode MCP tool returns: service-wide counts at
 * the top level plus an `entries` array of the non-live verdicts. We pass a high
 * limit so the whole actionable set comes back for one service.
 */
interface DeadToolPayload {
  service?: string
  total?: number
  live?: number
  testOnly?: number
  dead?: number
  deadCluster?: number
  possiblyLive?: number
  unknown?: number
  entries?: Array<{
    name?: string
    label?: string
    filePath?: string
    startLine?: number
    verdict?: string
    deadCluster?: boolean
    isExported?: boolean
  }> | null
}

function toDeadEntry(raw: NonNullable<DeadToolPayload['entries']>[number]): DeadEntry {
  return {
    name: raw.name ?? '(anonymous)',
    label: raw.label ?? 'Function',
    filePath: raw.filePath ?? '',
    startLine: typeof raw.startLine === 'number' ? raw.startLine : 0,
    verdict: raw.verdict ?? 'unknown',
    deadCluster: raw.deadCluster ?? false,
    isExported: raw.isExported ?? false
  }
}

function toDeadCounts(p: DeadToolPayload): DeadCounts {
  const num = (v: number | undefined) => (typeof v === 'number' ? v : 0)
  return {
    total: num(p.total),
    live: num(p.live),
    testOnly: num(p.testOnly),
    dead: num(p.dead),
    deadCluster: num(p.deadCluster),
    possiblyLive: num(p.possiblyLive),
    unknown: num(p.unknown)
  }
}

export interface FetchDeadReportParams {
  service: string
  scopeId?: string
}

/**
 * The service's dead-code report (default view: all non-live verdicts). Anchors
 * on service_name; scope_id is forwarded only when set. limit is pushed to 1000
 * (the tool's default is 100) so a whole service's actionable set is returned.
 */
export async function fetchDeadReport(
  client: McpClient,
  params: FetchDeadReportParams
): Promise<ApiEnvelope<DeadReportResponse>> {
  const service = requireNonEmptyString(params.service, 'service')
  const args: Record<string, unknown> = { service_name: service, limit: 1000 }
  if (params.scopeId) args.scope_id = params.scopeId

  const env = await client.callTool<DeadToolPayload>('codegraph_deadcode', args)
  const payload = env.data
  const entries = (payload.entries ?? []).map(toDeadEntry)
  return {
    warnings: [...new Set(env.warnings)],
    data: { service: payload.service ?? service, counts: toDeadCounts(payload), entries }
  }
}
