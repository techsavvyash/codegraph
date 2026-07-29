/**
 * Flows data layer (RFC-012 R4) — typed wrappers around the MCP tools that
 * back the Flows screen: codegraph_cypher (service list), codegraph_entry_points,
 * and codegraph_flows. Each function validates its own params (throwing
 * ValidationError on bad input) and forwards the call through McpClient,
 * passing the ApiEnvelope through verbatim (McpClient.callTool already
 * returns {warnings, data}).
 */
import type { McpClient } from '$lib/server/mcp/client'
import type { ApiEnvelope } from '$lib/types/graph'
import type { EntryPointsResponse, FlowResponse, ServicesResponse } from '$lib/types/flows'

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
// services

interface CypherJsonResult {
  columns: string[]
  row_count: number
  rows: Array<{ name: string }> | null
}

export async function listServices(client: McpClient): Promise<ApiEnvelope<ServicesResponse>> {
  const envelope = await client.callTool<CypherJsonResult>('codegraph_cypher', {
    query: 'MATCH (s:Service) RETURN s.name AS name ORDER BY name',
    format: 'json'
  })

  const services = (envelope.data.rows ?? []).map((row) => row.name)
  return { warnings: envelope.warnings, data: { services } }
}

// ---------------------------------------------------------------------------
// entry points

export interface EntryPointsParams {
  service: string
  tier?: number
  limit?: number
}

export async function getEntryPoints(
  client: McpClient,
  params: EntryPointsParams
): Promise<ApiEnvelope<EntryPointsResponse>> {
  const service_name = requireNonEmptyString(params.service, 'service')

  const args: Record<string, unknown> = {
    service_name,
    limit: clampInt(params.limit, 'limit', 1, 200, 100),
    format: 'json'
  }
  if (params.tier !== undefined && params.tier !== null) {
    const n = typeof params.tier === 'number' ? params.tier : Number(params.tier)
    if (!Number.isFinite(n) || !Number.isInteger(n)) {
      throw new ValidationError('tier must be an integer')
    }
    if (n < 1 || n > 4) {
      throw new ValidationError('tier must be between 1 and 4')
    }
    args.tier = n
  }

  return client.callTool<EntryPointsResponse>('codegraph_entry_points', args)
}

// ---------------------------------------------------------------------------
// flow trace

export interface TraceFlowParams {
  node_id: string
  max_depth?: number
}

export async function traceFlow(
  client: McpClient,
  params: TraceFlowParams
): Promise<ApiEnvelope<FlowResponse>> {
  const from = requireNonEmptyString(params.node_id, 'node_id')
  const max_depth = clampInt(params.max_depth, 'max_depth', 1, 10, 4)

  return client.callTool<FlowResponse>('codegraph_flows', {
    from,
    max_depth,
    persist: false,
    format: 'json'
  })
}
