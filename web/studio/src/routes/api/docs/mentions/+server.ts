import { json } from '@sveltejs/kit'
import type { RequestHandler } from './$types'
import { getReverseMentions } from '$lib/server/docs/api'
import { mcp } from '$lib/server/mcp/client'
import { docsErrorResponse } from '../errors'

/**
 * GET /api/docs/mentions?node_id=<elementId> — reverse lookup: the chunks
 * (and their documents) that MENTION the given code node. Response data:
 * { mentions: ReverseMention[] }.
 */
export const GET: RequestHandler = async ({ url }) => {
  const nodeId = url.searchParams.get('node_id') ?? ''
  try {
    const envelope = await getReverseMentions(mcp, { nodeId })
    return json(envelope)
  } catch (e) {
    return docsErrorResponse(e)
  }
}
