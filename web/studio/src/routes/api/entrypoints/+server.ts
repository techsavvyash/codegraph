import { json } from '@sveltejs/kit'
import type { RequestHandler } from './$types'
import { getEntryPoints, ValidationError } from '$lib/server/flows/api'
import { mcp, McpRequestError } from '$lib/server/mcp/client'
import type { ApiError } from '$lib/types/graph'

export const GET: RequestHandler = async ({ url }) => {
  const service = url.searchParams.get('service') ?? ''
  const tierParam = url.searchParams.get('tier')
  const limitParam = url.searchParams.get('limit')

  try {
    const envelope = await getEntryPoints(mcp, {
      service,
      tier: tierParam !== null ? Number(tierParam) : undefined,
      limit: limitParam !== null ? Number(limitParam) : undefined
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
