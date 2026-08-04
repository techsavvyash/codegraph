import { json } from '@sveltejs/kit'
import type { RequestHandler } from './$types'
import { runCypher, ValidationError } from '$lib/server/console/api'
import { mcp, McpRequestError } from '$lib/server/mcp/client'
import type { ApiError } from '$lib/types/graph'

/**
 * POST /api/cypher — runs a read-only Cypher query via codegraph_cypher and
 * returns the {warnings, data} envelope verbatim. The tool's guardrails
 * (write-keyword rejection, EXPLAIN failure, timeout, row cap) are the single
 * source of truth; this route never rewrites the query or the error.
 *
 *   200  { warnings: string[], data: { columns, row_count, rows, truncated } }
 *   400  { error, kind: 'validation' }            — empty/malformed request
 *   422  { error, kind: 'tool-error' }            — guardrail rejection, verbatim
 *   503  { error, kind: 'timeout'|'process'|... } — MCP transport failure
 *   500  { error, kind: 'internal' }
 */
export const POST: RequestHandler = async ({ request }) => {
  try {
    const body = await request.json().catch(() => {
      throw new ValidationError('request body must be valid JSON')
    })
    const envelope = await runCypher(mcp, {
      query: body?.query,
      params: body?.params,
      row_limit: body?.row_limit
    })
    return json(envelope)
  } catch (e) {
    return errorResponse(e)
  }
}

function errorResponse(e: unknown): Response {
  if (e instanceof ValidationError) {
    const body: ApiError = { error: e.message, kind: 'validation' }
    return json(body, { status: 400 })
  }
  if (e instanceof McpRequestError) {
    const body: ApiError = { error: e.message, kind: e.kind }
    // tool-error carries the guardrail/syntax message the user must see verbatim.
    if (e.kind === 'tool-error') return json(body, { status: 422 })
    return json(body, { status: 503 })
  }
  const body: ApiError = { error: e instanceof Error ? e.message : String(e), kind: 'internal' }
  return json(body, { status: 500 })
}
