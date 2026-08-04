/**
 * Explorer data layer (RFC-012 R1–R3) — typed wrappers around the four
 * graph-exploration MCP tools (codegraph_find/expand/path/source). Each
 * function validates its own params (throwing ValidationError on bad input)
 * and forwards the call through McpClient, passing the ApiEnvelope through
 * verbatim (McpClient.callTool already returns {warnings, data}).
 */
import type { McpClient } from '$lib/server/mcp/client'
import type {
  ApiEnvelope,
  ExpandResponse,
  FindResponse,
  PathResponse,
  SourceResponse
} from '$lib/types/graph'

export class ValidationError extends Error {
  constructor(message: string) {
    super(message)
    this.name = 'ValidationError'
  }
}

const DIRECTIONS = ['in', 'out', 'both'] as const
export type Direction = (typeof DIRECTIONS)[number]

function isDirection(v: unknown): v is Direction {
  return typeof v === 'string' && (DIRECTIONS as readonly string[]).includes(v)
}

function requireStringArray(v: unknown, field: string): string[] {
  if (!Array.isArray(v) || v.length === 0 || !v.every((x) => typeof x === 'string' && x.length > 0)) {
    throw new ValidationError(`${field} must be a non-empty array of non-empty strings`)
  }
  return v
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
  if (n < min || n > max) {
    throw new ValidationError(`${field} must be between ${min} and ${max}`)
  }
  return n
}

function optionalDirection(v: unknown, field: string, fallback: Direction): Direction {
  if (v === undefined || v === null) return fallback
  if (!isDirection(v)) {
    throw new ValidationError(`${field} must be one of: ${DIRECTIONS.join(', ')}`)
  }
  return v
}

// ---------------------------------------------------------------------------
// find

export interface FindParams {
  query?: string
  label?: string
  service?: string
  scope_id?: string
  limit?: number
  cursor?: string
  semantic?: boolean
}

export async function findNodes(
  client: McpClient,
  params: FindParams
): Promise<ApiEnvelope<FindResponse>> {
  const query = params.query?.trim() || undefined
  const label = params.label?.trim() || undefined
  if (!query && !label) {
    throw new ValidationError('find: either query or label must be provided')
  }

  const args: Record<string, unknown> = {}
  if (query) args.query = query
  if (label) args.label = label
  if (params.service) args.service = params.service
  if (params.scope_id) args.scope_id = params.scope_id
  if (params.limit !== undefined) args.limit = clampInt(params.limit, 'limit', 1, 200, 25)
  if (params.cursor) args.cursor = params.cursor
  if (params.semantic !== undefined) {
    if (typeof params.semantic !== 'boolean') {
      throw new ValidationError('semantic must be a boolean')
    }
    args.semantic = params.semantic
  }

  return client.callTool<FindResponse>('codegraph_find', args)
}

// ---------------------------------------------------------------------------
// expand

export interface ExpandParams {
  node_id: string
  rel_types: string[]
  direction?: Direction
  depth?: number
  max_nodes?: number
}

export async function expandNode(
  client: McpClient,
  params: ExpandParams
): Promise<ApiEnvelope<ExpandResponse>> {
  const node_id = requireNonEmptyString(params.node_id, 'node_id')
  const rel_types = requireStringArray(params.rel_types, 'rel_types')
  const direction = optionalDirection(params.direction, 'direction', 'out')
  const depth = clampInt(params.depth, 'depth', 1, 10, 3)
  const max_nodes = clampInt(params.max_nodes, 'max_nodes', 1, 2000, 500)

  return client.callTool<ExpandResponse>('codegraph_expand', {
    node_id,
    rel_types,
    direction,
    depth,
    max_nodes
  })
}

// ---------------------------------------------------------------------------
// path

export interface PathParams {
  from_id: string
  to_id: string
  rel_types: string[]
  max_hops?: number
  shortest?: boolean
  direction?: Direction
}

export async function findPath(
  client: McpClient,
  params: PathParams
): Promise<ApiEnvelope<PathResponse>> {
  const from_id = requireNonEmptyString(params.from_id, 'from_id')
  const to_id = requireNonEmptyString(params.to_id, 'to_id')
  const rel_types = requireStringArray(params.rel_types, 'rel_types')
  const max_hops = clampInt(params.max_hops, 'max_hops', 1, 20, 6)
  const direction = optionalDirection(params.direction, 'direction', 'out')

  let shortest = true
  if (params.shortest !== undefined) {
    if (typeof params.shortest !== 'boolean') {
      throw new ValidationError('shortest must be a boolean')
    }
    shortest = params.shortest
  }

  return client.callTool<PathResponse>('codegraph_path', {
    from_id,
    to_id,
    rel_types,
    max_hops,
    shortest,
    direction
  })
}

// ---------------------------------------------------------------------------
// source

export interface SourceParams {
  node_id: string
}

export async function getSource(
  client: McpClient,
  params: SourceParams
): Promise<ApiEnvelope<SourceResponse>> {
  const node_id = requireNonEmptyString(params.node_id, 'node_id')

  return client.callTool<SourceResponse>('codegraph_source', {
    node_id,
    format: 'json'
  })
}
