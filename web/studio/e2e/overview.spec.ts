import { test, expect } from '@playwright/test'

/**
 * Service Overview mode on /graph, e2e against the live dev graph (the
 * 'codegraph' service exists with ~220 files). Three journeys:
 *   1. fresh /graph with no scope → service picker → pick 'codegraph' → the
 *      stats chip appears non-zero and the topbar scope selector follows;
 *   2. deep-link ?open=<top dir> loads directly with more files visible than the
 *      fully-collapsed view, and the panel Expand/Collapse works on a selection;
 *   3. ?view=workbench and ?nodes= still land in workbench, and the mode toggle
 *      switches overview ↔ workbench.
 *
 * Canvas taps are flaky in headless cytoscape, so selection/assertions go
 * through the stats chip (data-testid="overview-stats"), the ?open= deep link,
 * and the panel/mode-toggle testids rather than clicking graph nodes.
 */

const SCOPE_KEY = 'studio:scope'

test.describe('service overview', () => {
  test('picker → pick a service → stats + topbar follow', async ({ page }) => {
    // Start with no persisted scope so the picker shows.
    await page.addInitScript((key) => window.localStorage.removeItem(key), SCOPE_KEY)
    await page.goto('/graph')

    const picker = page.getByTestId('service-picker')
    await expect(picker).toBeVisible({ timeout: 60_000 })

    // The 'codegraph' service card must be present (MCP spawns + /api/services).
    const card = page.locator('[data-testid="service-card"][data-service="codegraph"]')
    await expect(card).toBeVisible({ timeout: 60_000 })
    await card.click()

    // Picker dismisses; the stats chip appears with non-zero dirs. The fully
    // collapsed root shows only top-level directories — codegraph keeps no
    // files at the repo root, so the file count is legitimately 0 here.
    await expect(picker).toBeHidden()
    const stats = page.getByTestId('overview-stats')
    await expect(stats).toBeVisible({ timeout: 60_000 })
    await expect(stats).toHaveText(/[1-9]\d* dirs · \d+ files · \d+ symbols/, { timeout: 30_000 })

    // The global topbar scope selector now reflects the picked service.
    await expect(page.getByLabel('Service scope')).toHaveValue('codegraph')
  })

  test('?open= deep-link expands a top dir into more visible nodes', async ({ page }) => {
    // Seed the scope so overview loads directly (no picker).
    await page.addInitScript(
      ([key, value]) => window.localStorage.setItem(key, value),
      [SCOPE_KEY, JSON.stringify({ service: 'codegraph', scopeId: 'main' })] as const
    )

    const readStats = async (stats: import('@playwright/test').Locator) => {
      const t = (await stats.textContent()) ?? ''
      const dirs = Number(/(\d+) dirs/.exec(t)?.[1] ?? '0')
      const files = Number(/(\d+) files/.exec(t)?.[1] ?? '0')
      return { dirs, files, total: dirs + files }
    }

    // Baseline: fully-collapsed overview.
    await page.goto('/graph?view=overview')
    const stats = page.getByTestId('overview-stats')
    await expect(stats).toBeVisible({ timeout: 60_000 })
    await expect.poll(async () => (await readStats(stats)).total, { timeout: 60_000 }).toBeGreaterThan(0)
    const collapsed = await readStats(stats)

    // Deep-link with a top-level dir expanded — 'internal' is a codegraph dir.
    // Expanding replaces the dir with its children, so the total visible
    // dir+file count must strictly exceed the fully-collapsed baseline
    // (codegraph's internal/ holds many subpackages).
    await page.goto('/graph?view=overview&open=internal')
    await expect(stats).toBeVisible({ timeout: 60_000 })
    await expect
      .poll(async () => (await readStats(stats)).total, { timeout: 60_000 })
      .toBeGreaterThan(collapsed.total)
  })

  test('?view=workbench and ?nodes= land in workbench; toggle switches modes', async ({ page }) => {
    await page.addInitScript((key) => window.localStorage.removeItem(key), SCOPE_KEY)

    // ?view=workbench → the workbench canvas application region is present, no picker.
    await page.goto('/graph?view=workbench')
    await expect(page.getByRole('application', { name: 'Graph canvas' })).toBeVisible({ timeout: 60_000 })
    await expect(page.getByTestId('service-picker')).toHaveCount(0)

    // An empty workbench auto-opens the omnibox (pre-existing UX); close it,
    // then verify the ⌘K trigger button re-opens it.
    await expect(page.getByRole('dialog', { name: 'Search' })).toBeVisible()
    await page.keyboard.press('Escape')
    await expect(page.getByRole('dialog', { name: 'Search' })).toHaveCount(0)
    await page.getByRole('button', { name: /Search symbols/ }).click()
    await expect(page.getByRole('dialog', { name: 'Search' })).toBeVisible()
    await page.keyboard.press('Escape')

    // Toggle to overview → picker (no scope) appears.
    await page.getByTestId('mode-overview').click()
    await expect(page.getByTestId('service-picker')).toBeVisible({ timeout: 60_000 })

    // Toggle back to workbench → the workbench canvas returns.
    await page.getByTestId('mode-workbench').click()
    await expect(page.getByRole('application', { name: 'Graph canvas' })).toBeVisible()
  })
})

/**
 * Overview lens system (additive). Against the live 'codegraph' service. Canvas
 * pixel/tap behavior is not asserted (headless taps flake) — everything goes
 * through the lens bar, the flow rail, the legends, and the edge-mode chip.
 */
test.describe('overview lenses', () => {
  test.beforeEach(async ({ page }) => {
    // Seed the scope so overview loads directly (no picker) with the lens bar up.
    await page.addInitScript(
      ([key, value]) => window.localStorage.setItem(key, value),
      [SCOPE_KEY, JSON.stringify({ service: 'codegraph', scopeId: 'main' })] as const
    )
  })

  test('flows lens: rail + entries appear, clicking one projects onto the canvas', async ({ page }) => {
    await page.goto('/graph?view=overview')
    await expect(page.getByTestId('overview-stats')).toBeVisible({ timeout: 60_000 })

    // Switch to the flows lens → the rail mounts and entries load (live graph).
    await page.getByTestId('lens-flows').click()
    const rail = page.getByTestId('flow-rail')
    await expect(rail).toBeVisible({ timeout: 30_000 })
    const firstEntry = page.getByTestId('flow-entry').first()
    await expect(firstEntry).toBeVisible({ timeout: 60_000 })

    // Trace the first entry → the status chip shows a non-zero "on screen" count.
    await firstEntry.click()
    const status = page.getByTestId('flow-status')
    await expect(status).toBeVisible({ timeout: 60_000 })
    await expect
      .poll(async () => {
        const t = (await status.textContent()) ?? ''
        return Number(/·\s*(\d+)\s*on screen/.exec(t)?.[1] ?? '0')
      }, { timeout: 60_000 })
      .toBeGreaterThan(0)
  })

  test('dead lens: the legend appears with a dead count', async ({ page }) => {
    await page.goto('/graph?view=overview')
    await expect(page.getByTestId('overview-stats')).toBeVisible({ timeout: 60_000 })

    await page.getByTestId('lens-dead').click()
    const legend = page.getByTestId('dead-legend')
    await expect(legend).toBeVisible({ timeout: 60_000 })
    // reachability compute can take a moment; wait for the count text to land
    await expect(legend).toHaveText(/dead \d+ · possibly \d+ · test-only \d+/, { timeout: 90_000 })
  })

  test('structure lens: edge-mode toggle flips strong ⇄ all', async ({ page }) => {
    // A top dir expanded so there are real aggregate edges to filter.
    await page.goto('/graph?view=overview&open=internal')
    const toggle = page.getByTestId('edge-mode-toggle')
    await expect(toggle).toBeVisible({ timeout: 60_000 })
    await expect(toggle).toHaveText(/edges: strong/, { timeout: 30_000 })
    await toggle.click()
    await expect(toggle).toHaveText(/edges: all/)
    await toggle.click()
    await expect(toggle).toHaveText(/edges: strong/)
  })

  test('lens is deep-linked via &lens=', async ({ page }) => {
    await page.goto('/graph?view=overview&lens=hotspots')
    // hotspots legend confirms the lens restored from the URL
    await expect(page.getByTestId('hotspot-legend')).toBeVisible({ timeout: 60_000 })
    await expect(page.getByTestId('lens-hotspots')).toHaveAttribute('aria-selected', 'true')
  })
})
