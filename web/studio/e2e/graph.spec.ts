import { test, expect } from '@playwright/test'

test.describe('graph explorer', () => {
  test('search → canvas → expand → inspect journey against the live graph', async ({ page }) => {
    await page.goto('/graph')

    // Empty working set auto-opens the omnibox
    const input = page.locator('input').first()
    await expect(input).toBeVisible({ timeout: 15000 })

    await input.fill('matchChunks')
    // debounced find; wait for an actual result row (not the empty-state line)
    await expect(page.locator('.rrow').first()).toBeVisible({ timeout: 15000 })

    await input.press('Enter')

    // node lands on canvas: working-set chip + deep link + inspector
    await expect(page.getByText(/1 nodes? · 0 edges?/)).toBeVisible({ timeout: 10000 })
    await expect(page).toHaveURL(/nodes=/)
    const inspector = page.locator('aside')
    await expect(inspector).toBeVisible()
    await expect(inspector.getByText('matchChunks').first()).toBeVisible()

    // source pane loads highlighted code (Go-side format=json + rootPath)
    await expect(inspector.locator('pre, code').first()).toBeVisible({ timeout: 20000 })

    // keyboard expand grows the working set (edge count leaves 0 — the exact
    // node count is index-dependent, so don't assert a magnitude)
    await page.keyboard.press('e')
    await expect(page.getByText(/\d+ nodes · [1-9]\d* edges/)).toBeVisible({ timeout: 30000 })

    // relationship groups appear in the inspector
    await expect(inspector.getByText('CALLS', { exact: true })).toBeVisible()

    // no fatal error banner anywhere
    await expect(page.locator('.errbar')).toHaveCount(0)
  })
})
