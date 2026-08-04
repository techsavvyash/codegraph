import { test, expect } from '@playwright/test'

test.describe('flows', () => {
  test('entry rail → spine → inspector → load onto canvas journey against the live graph', async ({ page }) => {
    await page.goto('/flows')

    // Entry rail loads and shows at least one tier header
    await expect(page.locator('.tier .tname').filter({ hasText: /API-exposed|Exported roots/ }).first()).toBeVisible({
      timeout: 30000
    })

    // Select the first entry point
    const firstEntry = page.locator('.ep').first()
    await expect(firstEntry).toBeVisible({ timeout: 30000 })
    await firstEntry.click()

    // Spine renders at least one chip
    const firstChip = page.locator('.chip').first()
    await expect(firstChip).toBeVisible({ timeout: 30000 })

    // Click a chip to select it and populate the inspector
    const chipName = (await firstChip.locator('.id').innerText()).replace(/\(\)$/, '')
    await firstChip.click()

    const inspector = page.locator('aside').first()
    await expect(inspector).toBeVisible({ timeout: 20000 })
    await expect(inspector.locator('.insp-name')).toHaveText(chipName)
    await expect(inspector.locator('pre, code').first()).toBeVisible({ timeout: 20000 })

    // "Load onto canvas" becomes enabled once the flow has loaded
    const loadBtn = page.getByRole('button', { name: 'Load onto canvas' })
    await expect(loadBtn).toBeEnabled({ timeout: 20000 })

    // no fatal error banner anywhere
    await expect(page.locator('.errbar')).toHaveCount(0)
  })
})
