import { json } from '@sveltejs/kit'
import type { RequestHandler } from './$types'
import { findNodes, ValidationError } from '$lib/server/explorer/api'
import { mcp, McpRequestError } from '$lib/server/mcp/client'
import type { ApiError } from '$lib/types/graph'

export const GET: RequestHandler = async ({ url }) => {
  const q = url.searchParams.get('q') ?? undefined
  const label = url.searchParams.get('label') ?? undefined
  const service = url.searchParams.get('service') ?? undefined
  const scope_id = url.searchParams.get('scope_id') ?? undefined
  const limitParam = url.searchParams.get('limit')
  const cursor = url.searchParams.get('cursor') ?? undefined
  const semanticParam = url.searchParams.get('semantic')

  try {
    const envelope = await findNodes(mcp, {
      query: q,
      label,
      service,
      scope_id,
      limit: limitParam !== null ? Number(limitParam) : undefined,
      cursor,
      semantic: semanticParam !== null ? semanticParam === 'true' : undefined
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
    if (e.kind === 'tool-error') return json(body, { status: 422 })
    return json(body, { status: 503 })
  }
  const body: ApiError = { error: e instanceof Error ? e.message : String(e), kind: 'internal' }
  return json(body, { status: 500 })
}
