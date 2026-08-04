import { json } from '@sveltejs/kit'
import type { RequestHandler } from './$types'
import { fetchDeadReport, ValidationError } from '$lib/server/overview/api'
import { mcp } from '$lib/server/mcp/client'
import { overviewErrorResponse } from '../errors'

/**
 * GET /api/overview/dead?service=X&scope=main — the RFC-014 reachability report
 * for one service (all non-live verdicts + service-wide counts), for the Dead
 * code lens. scope is forwarded to the deadcode tool only when non-default.
 */
export const GET: RequestHandler = async ({ url }) => {
  const service = url.searchParams.get('service')
  const scope = url.searchParams.get('scope') ?? undefined
  try {
    if (!service) throw new ValidationError('service query parameter is required')
    const envelope = await fetchDeadReport(mcp, { service, scopeId: scope })
    return json(envelope)
  } catch (e) {
    return overviewErrorResponse(e)
  }
}
