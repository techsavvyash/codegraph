import { test, expect } from '@playwright/test'

test.describe('dashboard', () => {
  test('renders index overview against the live graph', async ({ page }) => {
    await page.goto('/dashboard')

    await expect(page).toHaveTitle('CodeGraph Studio')
    await expect(page.getByRole('link', { name: /CodeGraph/ })).toBeVisible()

    // First data-dependent assertion: the dashboard promise streams in after
    // the MCP child spawns and 10 graph queries complete (~3s warm, longer on
    // a cold Neo4j page cache) — give it a generous window.
    const kpiValues = page.locator('.kpi .num')
    await expect(kpiValues).toHaveCount(4, { timeout: 60_000 })
    for (const kpi of await kpiValues.all()) {
      await expect(kpi).toHaveText(/\d/)
      await expect(kpi).not.toHaveText('—')
    }

    const codegraphCard = page
      .locator('.scard')
      .filter({ has: page.locator('.nm', { hasText: /^codegraph$/ }) })
    await expect(codegraphCard).toBeVisible()
    const functionsStat = codegraphCard.locator('.stat', { hasText: 'Functions' })
    await expect(functionsStat).toBeVisible()
    const functionsText = await functionsStat.innerText()
    const match = functionsText.match(/Functions\s+([\d,]+)/)
    expect(match).not.toBeNull()
    const functionsCount = Number(match![1].replace(/,/g, ''))
    expect(functionsCount).toBeGreaterThan(0)

    const healthFlags = page.locator('.flag .sev')
    await expect(healthFlags.first()).toBeVisible()
    const sevCount = await healthFlags.count()
    expect(sevCount).toBeGreaterThan(0)
    for (const sev of await healthFlags.all()) {
      await expect(sev).toHaveText(/^(ok|warn|err)$/)
    }

    await expect(page.locator('.error-banner')).toHaveCount(0)
  })
})
