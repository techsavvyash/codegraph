import { json } from '@sveltejs/kit'
import type { RequestHandler } from './$types'
import { fetchSymbolCallers, ValidationError } from '$lib/server/overview/api'
import { mcp } from '$lib/server/mcp/client'
import { overviewErrorResponse } from '../errors'

/**
 * GET /api/overview/callers?node_id=<elementId> — the direct (1-hop) callers of
 * one symbol, for the Usage lens when a drilled symbol is the selection seed.
 */
export const GET: RequestHandler = async ({ url }) => {
  const nodeId = url.searchParams.get('node_id')
  try {
    if (!nodeId) throw new ValidationError('node_id query parameter is required')
    const envelope = await fetchSymbolCallers(mcp, { nodeId })
    return json(envelope)
  } catch (e) {
    return overviewErrorResponse(e)
  }
}
