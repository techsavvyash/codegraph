import { test, expect } from '@playwright/test'

/**
 * Cypher console (RFC-012 R8) e2e against the live dev graph. Two journeys:
 *   1. a read query renders a results table with rows;
 *   2. a write query is rejected and the tool's guardrail message renders
 *      verbatim in the error panel (never swallowed — R9 failure honesty).
 */
test.describe('cypher console', () => {
  test('runs a read query and renders rows', async ({ page }) => {
    await page.goto('/console')
    await expect(page).toHaveTitle('CodeGraph Studio — Console')

    const editor = page.locator('.editor textarea')
    await editor.fill('MATCH (s:Service) RETURN s.name ORDER BY s.name LIMIT 5')
    await page.getByRole('button', { name: 'Run' }).click()

    // First data-dependent assertion: the MCP child spawns + query runs.
    const table = page.locator('.results table')
    await expect(table).toBeVisible({ timeout: 60_000 })

    // Header echoes the returned column; body has the capped rows.
    await expect(table.locator('thead th', { hasText: 's.name' })).toBeVisible()
    const bodyRows = table.locator('tbody tr')
    const count = await bodyRows.count()
    expect(count).toBeGreaterThan(0)
    expect(count).toBeLessThanOrEqual(5)

    // Row count / elapsed meta is present and non-empty.
    await expect(page.locator('.results .count')).toHaveText(/\d+ rows?/)

    // No error panel on a good query.
    await expect(page.locator('.errpanel')).toHaveCount(0)
  })

  test('surfaces a write-guardrail rejection verbatim', async ({ page }) => {
    await page.goto('/console')

    const editor = page.locator('.editor textarea')
    await editor.fill('CREATE (n:StudioProbe) RETURN n')
    await page.getByRole('button', { name: 'Run' }).click()

    const errPanel = page.locator('.errpanel')
    await expect(errPanel).toBeVisible({ timeout: 60_000 })
    // The guardrail message from cmd/codegraph-mcp/handlers_cypher.go, verbatim.
    await expect(errPanel).toContainText('write keywords')
    await expect(errPanel).toContainText('are not allowed in this tool')

    // A rejected write must NOT render a results table.
    await expect(page.locator('.results table')).toHaveCount(0)
  })

  test('runs a parameterized query with params from the panel', async ({ page }) => {
    await page.goto('/console')

    await page
      .locator('.editor textarea')
      .fill('MATCH (s:Service) WHERE s.name = $name RETURN s.name AS name')

    // Expand the (collapsed-by-default) params panel and enter the JSON object.
    await page.locator('.params .head').click()
    await page.locator('.params .body textarea').fill('{"name": "codegraph"}')
    // The panel badge reflects one param key.
    await expect(page.locator('.params .badge').first()).toHaveText('1')

    await page.getByRole('button', { name: 'Run' }).click()

    const table = page.locator('.results table')
    await expect(table).toBeVisible({ timeout: 60_000 })
    const bodyRows = table.locator('tbody tr')
    await expect(bodyRows).toHaveCount(1)
    await expect(bodyRows.first()).toContainText('codegraph')
    await expect(page.locator('.errpanel')).toHaveCount(0)
  })
})
