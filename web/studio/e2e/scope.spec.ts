import { test, expect } from '@playwright/test'

// RFC-012 R9: the global service/scope selector in the topbar. Selecting a
// service must scope every pane and survive a reload. Runs against the live
// graph; 'codegraph' is always present in the dev graph.
test.describe('scope selector', () => {
  test('header selection scopes flows + dashboard and persists across reload', async ({ page }) => {
    await page.goto('/dashboard')

    // The selector fetches /api/services on mount; wait for it to be enabled.
    const selector = page.getByLabel('Service scope')
    await expect(selector).toBeEnabled({ timeout: 30_000 })

    // Defaults to "All services" (warned/unscoped state).
    await expect(page.locator('.scope .badge')).toBeVisible()

    // Scope to codegraph.
    await selector.selectOption('codegraph')
    await expect(selector).toHaveValue('codegraph')
    await expect(page.locator('.scope .badge')).toHaveCount(0)

    // Dashboard filters: only the codegraph card, plus the scope note. Wait for
    // the streamed dashboard to have resolved first (KPI numerals present).
    await expect(page.locator('.kpi .num').first()).toHaveText(/\d/, { timeout: 60_000 })
    await expect(page.locator('.scopenote')).toContainText('filtered to')
    await expect(page.locator('.scopenote')).toContainText('codegraph')
    const cards = page.locator('.scard')
    await expect(cards).toHaveCount(1)
    await expect(cards.locator('.nm')).toHaveText('codegraph')

    // Flows follows the global scope: its local service dropdown reads codegraph.
    await page.goto('/flows')
    const flowsSelect = page.locator('.svcselect')
    await expect(flowsSelect).toHaveValue('codegraph', { timeout: 30_000 })

    // Persistence: reload keeps the selection (localStorage-backed).
    await page.reload()
    await expect(page.getByLabel('Service scope')).toHaveValue('codegraph', { timeout: 30_000 })
    await expect(page.locator('.svcselect')).toHaveValue('codegraph', { timeout: 30_000 })

    // Switching back to All services restores the unscoped badge.
    await page.getByLabel('Service scope').selectOption('__all__')
    await expect(page.locator('.scope .badge')).toBeVisible()
  })
})
