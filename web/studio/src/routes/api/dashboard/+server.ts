import { json } from '@sveltejs/kit'
import type { RequestHandler } from './$types'
import { collectDashboard } from '$lib/server/dashboard/collect'
import { McpRequestError } from '$lib/server/mcp/client'
import type { DashboardError } from '$lib/types/dashboard'

export const GET: RequestHandler = async () => {
  try {
    const dashboard = await collectDashboard()
    return json(dashboard)
  } catch (e) {
    const kind = e instanceof McpRequestError ? e.kind : 'unknown'
    const body: DashboardError = {
      error: e instanceof Error ? e.message : String(e),
      kind
    }
    return json(body, { status: 503 })
  }
}
