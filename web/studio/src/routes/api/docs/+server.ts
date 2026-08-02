import { json } from '@sveltejs/kit'
import type { RequestHandler } from './$types'
import { listDocuments } from '$lib/server/docs/api'
import { mcp } from '$lib/server/mcp/client'
import { docsErrorResponse } from './errors'

/** GET /api/docs?service=<name> — documents, optionally filtered by service. */
export const GET: RequestHandler = async ({ url }) => {
  const service = url.searchParams.get('service')
  try {
    const envelope = await listDocuments(mcp, { service })
    return json(envelope)
  } catch (e) {
    return docsErrorResponse(e)
  }
}
