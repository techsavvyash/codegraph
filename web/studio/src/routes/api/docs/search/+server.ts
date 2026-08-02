import { json } from '@sveltejs/kit'
import type { RequestHandler } from './$types'
import { searchDocs } from '$lib/server/docs/api'
import { mcp } from '$lib/server/mcp/client'
import { docsErrorResponse } from '../errors'

/**
 * GET /api/docs/search?q=<query>&service=<name>&limit=<n> — fulltext search
 * over document title + content, service-filterable. Response data:
 * { hits: DocSearchHit[], fallback: boolean } — `fallback` true when the
 * CONTAINS path served the query (no fulltext index).
 */
export const GET: RequestHandler = async ({ url }) => {
  const query = url.searchParams.get('q') ?? ''
  const service = url.searchParams.get('service')
  const limitParam = url.searchParams.get('limit')
  try {
    const envelope = await searchDocs(mcp, {
      query,
      service,
      limit: limitParam !== null ? Number(limitParam) : undefined
    })
    return json(envelope)
  } catch (e) {
    return docsErrorResponse(e)
  }
}
