import { json } from '@sveltejs/kit'
import type { RequestHandler } from './$types'
import { expandNode, ValidationError } from '$lib/server/explorer/api'
import { mcp, McpRequestError } from '$lib/server/mcp/client'
import type { ApiError } from '$lib/types/graph'

export const POST: RequestHandler = async ({ request }) => {
  try {
    const body = await request.json()
    const envelope = await expandNode(mcp, {
      node_id: body?.node_id,
      rel_types: body?.rel_types,
      direction: body?.direction,
      depth: body?.depth,
      max_nodes: body?.max_nodes
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
