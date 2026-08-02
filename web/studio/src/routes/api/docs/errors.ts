import { json } from '@sveltejs/kit'
import { ValidationError } from '$lib/server/docs/api'
import { McpRequestError } from '$lib/server/mcp/client'
import type { ApiError } from '$lib/types/graph'

/**
 * Shared error mapping for the /api/docs routes, identical to the entrypoints
 * route: ValidationError → 400, McpRequestError tool-error → 422 (bad query the
 * user can fix), any other MCP failure (timeout/process/cooldown) → 503, and an
 * unexpected error → 500.
 */
export function docsErrorResponse(e: unknown): Response {
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
