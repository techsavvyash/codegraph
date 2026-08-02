import { json } from '@sveltejs/kit'
import type { RequestHandler } from './$types'
import { fetchFileSymbols, ValidationError } from '$lib/server/overview/api'
import { mcp } from '$lib/server/mcp/client'
import { overviewErrorResponse } from '../errors'

/**
 * GET /api/overview/file?node_id=… — the drilldown for one file: its
 * Function/Method symbols with call fan-in/out. Lazily fetched when the user
 * expands a file in the Overview visualizer.
 */
export const GET: RequestHandler = async ({ url }) => {
  const nodeId = url.searchParams.get('node_id')
  try {
    if (!nodeId) throw new ValidationError('node_id query parameter is required')
    const envelope = await fetchFileSymbols(mcp, { fileNodeId: nodeId })
    return json(envelope)
  } catch (e) {
    return overviewErrorResponse(e)
  }
}
