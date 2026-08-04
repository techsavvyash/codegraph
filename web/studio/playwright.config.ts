import { defineConfig, devices } from '@playwright/test'

export default defineConfig({
  testDir: 'e2e',
  fullyParallel: true,
  reporter: 'list',
  use: {
    baseURL: 'http://localhost:4174'
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
  webServer: {
    command:
      'pnpm build && PORT=4174 CODEGRAPH_MCP_BIN=/home/techsavvyash/sweatAndBlood/context/codegraph/bin/codegraph-mcp node build',
    url: 'http://localhost:4174/dashboard',
    reuseExistingServer: true,
    timeout: 180000
  }
})
