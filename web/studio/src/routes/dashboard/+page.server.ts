import type { PageServerLoad } from './$types'
import { collectDashboard } from '$lib/server/dashboard/collect'

/**
 * Returns the collectDashboard() promise un-awaited so SvelteKit streams it:
 * the page renders immediately with a skeleton and resolves the dashboard
 * once the graph queries land. Rejections are intentionally left to the UI's
 * {:catch} block — this loader does not catch.
 */
export const load: PageServerLoad = () => {
  return { dashboard: collectDashboard() }
}
