/**
 * Cypher console data layer (RFC-012 R8) — a thin typed wrapper over the
 * codegraph_cypher MCP tool. It validates the request (throwing
 * ValidationError → HTTP 400) and forwards the call through McpClient,
 * returning the {warnings, data} envelope verbatim.
 *
 * There is deliberately NO client- or server-side query rewriting here. The
 * MCP server is the single authority on read-only enforcement (write-keyword
 * regex, EXPLAIN validation, ExecuteRead) and on row caps/truncation. Guardrail
 * rejections surface as McpRequestError('tool-error') and are mapped to HTTP
 * 422 with the tool's message UNALTERED by the route.
 */
import type { McpClient } from '$lib/server/mcp/client'
import type { ApiEnvelope } from '$lib/types/graph'
import type { CypherRequestBody, CypherResult } from '$lib/types/console'

export class ValidationError extends Error {
  constructor(message: string) {
    super(message)
    this.name = 'ValidationError'
  }
}

function requireNonEmptyString(v: unknown, field: string): string {
  if (typeof v !== 'string' || v.trim().length === 0) {
    throw new ValidationError(`${field} is required and must be a non-empty string`)
  }
  return v
}

/**
 * Validates and normalizes a raw request body into the arguments the
 * codegraph_cypher tool accepts. Exported so it can be unit-tested without a
 * live MCP process.
 */
export function buildCypherArgs(body: CypherRequestBody): Record<string, unknown> {
  const query = requireNonEmptyString(body?.query, 'query')

  const args: Record<string, unknown> = { query, format: 'json' }

  if (body.row_limit !== undefined && body.row_limit !== null) {
    const n = typeof body.row_limit === 'number' ? body.row_limit : Number(body.row_limit)
    if (!Number.isFinite(n) || !Number.isInteger(n)) {
      throw new ValidationError('row_limit must be an integer')
    }
    // The tool clamps to [1,1000] itself; we pass through the user's intent
    // rather than second-guessing it, but reject nonsense so the error is
    // a clean 400 instead of a tool round-trip.
    if (n < 1) {
      throw new ValidationError('row_limit must be at least 1')
    }
    args.row_limit = n
  }

  if (body.params !== undefined && body.params !== null) {
    if (typeof body.params !== 'object' || Array.isArray(body.params)) {
      throw new ValidationError('params must be an object')
    }
    args.params = body.params
  }

  return args
}

export async function runCypher(
  client: McpClient,
  body: CypherRequestBody
): Promise<ApiEnvelope<CypherResult>> {
  const args = buildCypherArgs(body)
  return client.callTool<CypherResult>('codegraph_cypher', args)
}
