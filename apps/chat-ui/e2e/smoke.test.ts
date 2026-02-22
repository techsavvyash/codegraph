import { test, expect } from '@playwright/test'

// Canned ndjson responses for route mocking
function makeNdjson(events: object[]): string {
  return events.map(e => JSON.stringify(e)).join('\n') + '\n'
}

const SIMPLE_RESPONSE = makeNdjson([
  { type: 'text', delta: 'I am a code intelligence assistant for CodeGraph.' },
  { type: 'done' }
])

const TOOL_RESPONSE = makeNdjson([
  { type: 'tool_use', name: 'codegraph_hybrid_search', input: { query: 'hybrid search' } },
  { type: 'tool_result', name: 'codegraph_hybrid_search', result: 'Found HybridSearchManager.UnifiedSearch' },
  { type: 'text', delta: 'Based on the search, hybrid search is in HybridSearchManager.' },
  { type: 'done' }
])

// Helper: submit via the send button (more reliable than keyboard in Playwright)
async function sendMsg(page: import('@playwright/test').Page, text: string) {
  const input = page.getByRole('textbox', { name: 'Message input' })
  await input.fill(text)
  await page.getByRole('button', { name: 'Send message' }).click()
}

// Mock /api/chat before each test — no real OpenAI key needed
test.beforeEach(async ({ page }) => {
  await page.route('/api/chat', route =>
    route.fulfill({
      status: 200,
      contentType: 'application/x-ndjson',
      body: SIMPLE_RESPONSE
    })
  )
})

test.describe('Empty state', () => {
  test('page loads with correct title', async ({ page }) => {
    await page.goto('/')
    await expect(page).toHaveTitle('CodeGraph Intelligence')
  })

  test('shows CODEGRAPH INTELLIGENCE heading', async ({ page }) => {
    await page.goto('/')
    await expect(page.getByText('CODEGRAPH INTELLIGENCE')).toBeVisible()
  })

  test('shows three example prompts', async ({ page }) => {
    await page.goto('/')
    await expect(page.getByText('How does hybrid search work?')).toBeVisible()
    await expect(page.getByText('Find all callers of HybridSearchManager')).toBeVisible()
    await expect(page.getByText('What does the MCP server expose?')).toBeVisible()
  })

  test('send button is disabled when input is empty', async ({ page }) => {
    await page.goto('/')
    await expect(page.getByRole('button', { name: 'Send message' })).toBeDisabled()
  })

  test('clear button is not shown in empty state', async ({ page }) => {
    await page.goto('/')
    await expect(page.getByRole('button', { name: 'clear' })).not.toBeVisible()
  })
})

test.describe('Input behaviour', () => {
  test('send button enables when text is typed', async ({ page }) => {
    await page.goto('/')
    await page.getByRole('textbox', { name: 'Message input' }).fill('hello')
    await expect(page.getByRole('button', { name: 'Send message' })).toBeEnabled()
  })

  test('send button disables again after clearing input', async ({ page }) => {
    await page.goto('/')
    const input = page.getByRole('textbox', { name: 'Message input' })
    await input.fill('hello')
    await input.fill('')
    await expect(page.getByRole('button', { name: 'Send message' })).toBeDisabled()
  })

  test('Enter key sends message', async ({ page }) => {
    await page.goto('/')
    const input = page.getByRole('textbox', { name: 'Message input' })
    // Use locator-based press so the keydown is dispatched directly to the textarea element
    await input.pressSequentially('hello via enter')
    await input.press('Enter')
    // User bubble appears and input clears — proves Enter submitted the form
    await expect(page.getByText('hello via enter', { exact: true })).toBeVisible()
    await expect(input).toHaveValue('')
  })

  test('Shift+Enter inserts newline without submitting', async ({ page }) => {
    await page.goto('/')
    const input = page.getByRole('textbox', { name: 'Message input' })
    await input.focus()
    await input.type('line one')
    await input.press('Shift+Enter')
    await input.type('line two')
    // Message list should still be empty (not submitted)
    await expect(page.getByText('CODEGRAPH INTELLIGENCE')).toBeVisible()
  })
})

test.describe('Conversation flow', () => {
  test('sends message and shows user bubble', async ({ page }) => {
    await page.goto('/')
    await sendMsg(page, 'What tools do you have?')
    await expect(page.getByText('What tools do you have?')).toBeVisible()
  })

  test('clears input after sending', async ({ page }) => {
    await page.goto('/')
    const input = page.getByRole('textbox', { name: 'Message input' })
    await input.fill('test query')
    await page.getByRole('button', { name: 'Send message' }).click()
    await expect(input).toHaveValue('')
  })

  test('shows assistant response after send', async ({ page }) => {
    await page.goto('/')
    await sendMsg(page, 'hello')
    await expect(
      page.getByText('I am a code intelligence assistant for CodeGraph.')
    ).toBeVisible()
  })

  test('clear button appears after first message', async ({ page }) => {
    await page.goto('/')
    await sendMsg(page, 'hi')
    await expect(page.getByRole('button', { name: 'clear' })).toBeVisible()
  })

  test('clear button resets to empty state', async ({ page }) => {
    await page.goto('/')
    await sendMsg(page, 'hi')
    await expect(page.getByText('hi', { exact: true })).toBeVisible()

    await page.getByRole('button', { name: 'clear' }).click()
    await expect(page.getByText('CODEGRAPH INTELLIGENCE')).toBeVisible()
    await expect(page.getByRole('button', { name: 'clear' })).not.toBeVisible()
  })

  test('multiple messages accumulate in conversation', async ({ page }) => {
    await page.goto('/')

    await sendMsg(page, 'first question')
    await expect(page.getByText('first question')).toBeVisible()

    await sendMsg(page, 'second question')
    await expect(page.getByText('second question')).toBeVisible()
    await expect(page.getByText('first question')).toBeVisible()
  })
})

test.describe('Tool call display', () => {
  test.beforeEach(async ({ page }) => {
    await page.route('/api/chat', route =>
      route.fulfill({
        status: 200,
        contentType: 'application/x-ndjson',
        body: TOOL_RESPONSE
      })
    )
  })

  test('citations panel shows after tool_result events', async ({ page }) => {
    await page.goto('/')
    await sendMsg(page, 'How does hybrid search work?')
    await expect(page.getByText('1 tool call')).toBeVisible()
  })

  test('citations expand on click to show tool output', async ({ page }) => {
    await page.goto('/')
    await sendMsg(page, 'How does hybrid search work?')
    await page.getByText('1 tool call').click()
    await expect(page.getByText('Found HybridSearchManager.UnifiedSearch')).toBeVisible()
  })
})

test.describe('Error handling', () => {
  test('shows error banner on server error response', async ({ page }) => {
    await page.route('/api/chat', route =>
      route.fulfill({ status: 500, body: 'Internal Server Error' })
    )
    await page.goto('/')
    await sendMsg(page, 'hi')
    await expect(page.getByRole('alert')).toBeVisible()
  })

  test('error banner can be dismissed', async ({ page }) => {
    await page.route('/api/chat', route =>
      route.fulfill({ status: 500, body: 'Internal Server Error' })
    )
    await page.goto('/')
    await sendMsg(page, 'hi')

    const alert = page.getByRole('alert')
    await expect(alert).toBeVisible()
    await page.getByRole('button', { name: 'Dismiss error' }).click()
    await expect(alert).not.toBeVisible()
  })

  test('shows error banner on ndjson error event', async ({ page }) => {
    await page.route('/api/chat', route =>
      route.fulfill({
        status: 200,
        contentType: 'application/x-ndjson',
        body: makeNdjson([{ type: 'error', message: 'OPENAI_API_KEY is not set.' }])
      })
    )
    await page.goto('/')
    await sendMsg(page, 'hi')
    await expect(page.getByRole('alert')).toContainText('OPENAI_API_KEY')
  })
})
