import { defineConfig } from '@playwright/test'

const addr = '127.0.0.1:6464'

export default defineConfig({
  testDir: 'e2e',
  projects: [
    { name: 'screenshots', testMatch: 'screenshots.spec.ts' },
    // The chat tests need the Apps of the screenshots test, and they change the data of the screenshots.
    { name: 'chat', testMatch: 'chat.spec.ts', dependencies: ['screenshots'] },
    // The navigation tests open the chats of the chat tests, and the page marks the messages of an open chat as seen.
    {
      name: 'navigation',
      testMatch: 'navigation.spec.ts',
      dependencies: ['chat'],
    },
  ],
  use: {
    baseURL: `http://${addr}`,
    userAgent: 'Mobius screenshots',
    // page.route does not see the requests that pass through a service worker.
    serviceWorkers: 'block',
    // With partial raster, Chrome paints only the changed part of a tile again, and the edges of that part can differ from run to run.
    launchOptions: { args: ['--disable-partial-raster'] },
  },
  webServer: {
    // A cached test result ends at once and serves nothing, so -count=1 runs the server each time.
    command: 'go test -count=1 -timeout=0 -run=^TestServer$ ./e2e',
    url: `http://${addr}`,
    env: { MOBIUS_E2E_ADDR: addr },
    gracefulShutdown: { signal: 'SIGTERM', timeout: 10_000 },
    timeout: 120_000,
  },
})
