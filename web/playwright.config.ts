import { defineConfig } from '@playwright/test'

const addr = '127.0.0.1:6464'

export default defineConfig({
  testDir: 'e2e',
  use: {
    baseURL: `http://${addr}`,
    userAgent: 'Mobius screenshots',
    // page.route does not see the requests that pass through a service worker.
    serviceWorkers: 'block',
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
