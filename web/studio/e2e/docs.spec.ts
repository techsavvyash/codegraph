import { test, expect } from '@playwright/test'

// RFC-012 R5: the Docs plane against the live graph. codegraph has 70
// documents with mined MENTIONS links (the RFC-011 walkthrough is a known
// doc with docmine/fence links). We scope to codegraph via the global header
// selector, open a document, and assert chunks + a doc→code link render with
// its confidence; then that search finds a known doc by title.
test.describe('docs plane', () => {
  test('scope → open doc → chunks + MENTIONS link with confidence; search finds a known doc', async ({
    page
  }) => {
    await page.goto('/docs')

    // Scope to codegraph via the global selector (present on every page).
    const selector = page.getByLabel('Service scope')
    await expect(selector).toBeEnabled({ timeout: 30_000 })
    await selector.selectOption('codegraph')
    await expect(selector).toHaveValue('codegraph')

    // The left rail lists documents grouped by service — the only in-scope
    // group is codegraph. Wait for at least one document to appear.
    await expect(page.locator('.rail .group-head .gname').filter({ hasText: 'codegraph' })).toBeVisible({
      timeout: 30_000
    })
    const firstDoc = page.locator('.rail .doc').first()
    await expect(firstDoc).toBeVisible({ timeout: 30_000 })

    // Search for the RFC-011 walkthrough by title — a document known to carry
    // mined docmine links — and open it. Searching narrows the rail to hits.
    await page.getByLabel('Search documents').fill('Walkthrough')
    const hit = page.locator('.rail .doc').filter({ hasText: /Walkthrough/i }).first()
    await expect(hit).toBeVisible({ timeout: 30_000 })
    await hit.click()

    // Middle pane: the document's chunks render (ordered).
    const firstChunk = page.locator('.chunks .chunk').first()
    await expect(firstChunk).toBeVisible({ timeout: 30_000 })

    // Open a chunk that carries code links (the "N links" pill marks them).
    const linkedChunk = page.locator('.chunks .chunk').filter({ has: page.locator('.links') }).first()
    await expect(linkedChunk).toBeVisible({ timeout: 30_000 })
    await linkedChunk.click()

    // Right pane: the chunk content renders and a doc→code link chip shows its
    // strategy + confidence percentage (nothing inferred without provenance).
    const linkChip = page.locator('.content .links-list .chip').first()
    await expect(linkChip).toBeVisible({ timeout: 20_000 })
    await expect(linkChip.locator('.strategy')).toContainText('/')
    await expect(linkChip.locator('.conf')).toHaveText(/\d+%/)

    // The chip is a live navigation to the code node on the canvas.
    await linkChip.click()
    await expect(page).toHaveURL(/\/graph\?nodes=/, { timeout: 20_000 })

    // No fatal error banner anywhere in the journey.
    await expect(page.locator('.errbar')).toHaveCount(0)
  })
})
