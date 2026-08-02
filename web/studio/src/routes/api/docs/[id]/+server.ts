import { json } from '@sveltejs/kit'
import type { RequestHandler } from './$types'
import { getDocumentDetail } from '$lib/server/docs/api'
import { mcp } from '$lib/server/mcp/client'
import { docsErrorResponse } from '../errors'

/**
 * GET /api/docs/<id> — one document with its chunks (ordered) and each chunk's
 * MENTIONS out-links. `id` is a Neo4j elementId (URL-encoded by the client);
 * the leaf path param arrives already decoded by SvelteKit.
 */
export const GET: RequestHandler = async ({ params }) => {
  try {
    const envelope = await getDocumentDetail(mcp, { docId: params.id })
    return json(envelope)
  } catch (e) {
    return docsErrorResponse(e)
  }
}
