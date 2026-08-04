import { test, expect } from '@playwright/test'

/**
 * RFC-012 R6: the Chat dock. OPENAI_API_KEY may be ABSENT in the e2e
 * environment, so every assertion here is key-independent:
 *  - the dock toggles open and closed,
 *  - the input is present when open,
 *  - sending a message surfaces EITHER an assistant reply OR the missing-key
 *    error banner (both are acceptable, correct outcomes).
 * The dock is mounted globally in +layout.svelte, so any page will do.
 */
test.describe('chat dock', () => {
  test('toggles open/closed and exposes the input', async ({ page }) => {
    await page.goto('/dashboard')

    const fab = page.getByRole('button', { name: 'Open chat' })
    await expect(fab).toBeVisible({ timeout: 30_000 })

    await fab.click()
    const panel = page.getByRole('region', { name: 'Chat' })
    await expect(panel).toBeVisible()
    await expect(page.getByLabel('Chat message')).toBeVisible()

    // Scope indicator is always shown (a service name or "unscoped").
    await expect(panel.locator('.scope')).toBeVisible()

    // Close returns to the floating button.
    await page.getByRole('button', { name: 'Close chat' }).click()
    await expect(panel).toBeHidden()
    await expect(fab).toBeVisible()
  })

  test('sending a message surfaces either an assistant reply or the missing-key error', async ({
    page
  }) => {
    await page.goto('/dashboard')
    await page.getByRole('button', { name: 'Open chat' }).click()

    const input = page.getByLabel('Chat message')
    await input.fill('What does the MCP server expose?')
    await input.press('Enter')

    // The user bubble appears immediately regardless of backend state.
    await expect(page.locator('.user-bubble')).toContainText('What does the MCP server expose?')

    // Then EITHER an assistant turn renders (prose bubble) OR the error banner
    // reports the missing key / unavailable backend. Assert the disjunction
    // explicitly so a real regression (neither appears) fails the test.
    const assistantProse = page.locator('.dock .prose')
    const errorBanner = page.locator('.dock .banner')

    await expect
      .poll(
        async () => (await assistantProse.count()) > 0 || (await errorBanner.count()) > 0,
        { timeout: 60_000, message: 'expected an assistant reply or an error banner' }
      )
      .toBe(true)

    if ((await errorBanner.count()) > 0) {
      // Key-absent path: the banner must name the cause, not fail silently.
      await expect(errorBanner).toContainText(/OPENAI_API_KEY|unavailable|error/i)
    } else {
      // Key-present path: an assistant bubble exists (content may still stream).
      await expect(assistantProse.first()).toBeVisible()
    }
  })
})
