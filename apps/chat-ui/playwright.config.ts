import { defineConfig, devices } from '@playwright/test'

export default defineConfig({
  testDir: './e2e',
  timeout: 30_000,
  expect: { timeout: 8_000 },

  // Fail fast on CI; allow retries for flaky tests
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  workers: process.env.CI ? 1 : undefined,

  reporter: process.env.CI ? 'github' : 'list',

  use: {
    baseURL: 'http://localhost:4321',
    trace: 'on-first-retry'
  },

  // Start dev server before running tests
  webServer: {
    command: 'pnpm dev --port 4321',
    port: 4321,
    reuseExistingServer: !process.env.CI,
    timeout: 60_000,
    env: {
      // Dummy key so the server starts without error.
      // E2e tests mock /api/chat at network level so OpenAI is never called.
      OPENAI_API_KEY: process.env.OPENAI_API_KEY ?? 'e2e-mock-key-not-used'
    }
  },

  projects: [
    { name: 'chromium', use: { ...devices['Desktop Chrome'] } }
  ]
})
