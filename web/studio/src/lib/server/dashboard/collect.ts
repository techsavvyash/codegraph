import { mcp } from '$lib/server/mcp/client'
import type { McpClient } from '$lib/server/mcp/client'
import {
  SERVICES_QUERY,
  NODES_BY_LABEL_QUERY,
  EDGES_BY_TYPE_QUERY,
  CALLS_PER_SERVICE_QUERY,
  MENTIONS_PER_SERVICE_FAMILY_QUERY,
  SEMANTIC_STATE_QUERY,
  FLOWS_PER_SERVICE_QUERY,
  ENTRY_CANDIDATES_PER_SERVICE_QUERY,
  API_ROUTES_PER_SERVICE_QUERY,
  CALL_HUBS_QUERY,
  RECENT_DOC_LINKS_QUERY,
  type ServiceRow,
  type NodesByLabelRow,
  type EdgesByTypeRow,
  type CallsPerServiceRow,
  type MentionsPerServiceFamilyRow,
  type SemanticStateRow,
  type FlowsPerServiceRow,
  type EntryCandidatesPerServiceRow,
  type ApiRoutesPerServiceRow,
  type CallHubRow,
  type RecentDocLinkRow
} from './queries'
import { buildDashboard, type RawDashboardRows } from './build'
import type { DashboardData } from '$lib/types/dashboard'

const CYPHER_TOOL = 'codegraph_cypher'
const HEAVY_TIMEOUT_MS = 60_000
const HEAVY_BRIDGE_TIMEOUT_MS = 65_000
const ROW_LIMIT = 1000 // the tool's hard cap; default (100) is too low for UNION ALL fan-outs

interface CypherResponse {
  columns: string[]
  row_count: number
  rows: Array<Record<string, unknown>> | null
  truncated: boolean
}

/**
 * Runs one cypher query through the MCP bridge and returns its rows (typed)
 * plus any guardrail warnings. `rows` comes back null from the tool when the
 * result set is empty — normalized to [] here so callers never null-check.
 */
async function runQuery<Row>(
  client: McpClient,
  query: string,
  warnings: Set<string>
): Promise<Row[]> {
  const { data, warnings: w } = await client.callTool<CypherResponse>(
    CYPHER_TOOL,
    { query, timeout_ms: HEAVY_TIMEOUT_MS, row_limit: ROW_LIMIT },
    HEAVY_BRIDGE_TIMEOUT_MS
  )
  for (const warning of w) warnings.add(warning)
  if (data.truncated) {
    warnings.add(
      `result truncated at ${ROW_LIMIT} rows — dashboard counts may be undercounted (query: ${query.trim().slice(0, 80)}…)`
    )
  }
  return (data.rows ?? []) as unknown as Row[]
}

/**
 * Collects every raw row set the dashboard needs from the live graph,
 * running independent queries concurrently through the MCP bridge, then
 * hands them to the pure aggregator in build.ts.
 */
export async function collectDashboard(client: McpClient = mcp): Promise<DashboardData> {
  const warnings = new Set<string>()

  const [
    services,
    nodesByLabel,
    edgesByType,
    callsPerService,
    mentionsPerServiceFamily,
    semanticState,
    flowsPerService,
    entryCandidatesPerService,
    apiRoutesPerService,
    callHubs,
    recentDocLinks
  ] = await Promise.all([
    runQuery<ServiceRow>(client, SERVICES_QUERY, warnings),
    runQuery<NodesByLabelRow>(client, NODES_BY_LABEL_QUERY, warnings),
    runQuery<EdgesByTypeRow>(client, EDGES_BY_TYPE_QUERY, warnings),
    runQuery<CallsPerServiceRow>(client, CALLS_PER_SERVICE_QUERY, warnings),
    runQuery<MentionsPerServiceFamilyRow>(client, MENTIONS_PER_SERVICE_FAMILY_QUERY, warnings),
    runQuery<SemanticStateRow>(client, SEMANTIC_STATE_QUERY, warnings),
    runQuery<FlowsPerServiceRow>(client, FLOWS_PER_SERVICE_QUERY, warnings),
    runQuery<EntryCandidatesPerServiceRow>(client, ENTRY_CANDIDATES_PER_SERVICE_QUERY, warnings),
    runQuery<ApiRoutesPerServiceRow>(client, API_ROUTES_PER_SERVICE_QUERY, warnings),
    runQuery<CallHubRow>(client, CALL_HUBS_QUERY, warnings),
    runQuery<RecentDocLinkRow>(client, RECENT_DOC_LINKS_QUERY, warnings)
  ])

  const raw: RawDashboardRows = {
    services,
    nodesByLabel,
    edgesByType,
    callsPerService,
    mentionsPerServiceFamily,
    semanticState,
    flowsPerService,
    entryCandidatesPerService,
    apiRoutesPerService,
    callHubs,
    recentDocLinks
  }

  const neo4jTarget = process.env.NEO4J_URI ?? 'bolt://localhost:7687'
  return buildDashboard(raw, {
    generatedAt: new Date().toISOString(),
    neo4jTarget,
    warnings: [...warnings]
  })
}
