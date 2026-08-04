import { json } from '@sveltejs/kit'
import type { RequestHandler } from './$types'
import { fetchOverview, ValidationError } from '$lib/server/overview/api'
import { mcp } from '$lib/server/mcp/client'
import { overviewErrorResponse } from './errors'

/**
 * GET /api/overview?service=X&scope=main — the whole-service file graph
 * (files + symbol counts + file-pair call edges) for the Overview visualizer.
 */
export const GET: RequestHandler = async ({ url }) => {
  const service = url.searchParams.get('service')
  const scope = url.searchParams.get('scope') ?? 'main'
  try {
    if (!service) throw new ValidationError('service query parameter is required')
    const envelope = await fetchOverview(mcp, { service, scopeId: scope })
    return json(envelope)
  } catch (e) {
    return overviewErrorResponse(e)
  }
}
