import { createServer } from "node:net";
import { defineConfig, type PlaywrightTestProject } from "@playwright/test";

// Playwright loads this config again in each worker, and the worker gets the environment of the main process.
// Thus only the main process finds a free port.
async function freePort(): Promise<string> {
  const server = createServer();
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  const { port } = server.address() as { port: number };
  await new Promise((resolve) => server.close(resolve));
  return String(port);
}

process.env.MOBIUS_E2E_PORT ??= await freePort();
process.env.MOBIUS_E2E_APPS_PORT ??= await freePort();
const addr = `127.0.0.1:${process.env.MOBIUS_E2E_PORT}`;
const appsAddr = `127.0.0.1:${process.env.MOBIUS_E2E_APPS_PORT}`;

// The server of the project screenshots has no Apps, because that test creates them. The server of each other project
// has the two Apps from its start. The groups a, b and c need no other group: each can run alone on that server.
const withApps = (project: PlaywrightTestProject): PlaywrightTestProject => ({
  ...project,
  use: { baseURL: `http://${appsAddr}` },
});

const serverCommand = {
  // A cached test result ends at once and serves nothing, so -count=1 runs the server each time.
  command: "go test -count=1 -timeout=0 -run=^TestServer$ ./e2e",
  gracefulShutdown: { signal: "SIGTERM", timeout: 10_000 },
  timeout: 120_000,
} as const;

export default defineConfig({
  testDir: "e2e",
  // The list reporter gives the time of each test in the CI log.
  reporter: "list",
  projects: [
    // Group screenshots. The test creates the Apps in the UI, and it changes data that the other groups must not see.
    { name: "screenshots", testMatch: "screenshots.spec.ts" },

    // Group a.
    withApps({ name: "pending", testMatch: "pending.spec.ts" }),
    withApps({ name: "recurring", testMatch: "recurring.spec.ts" }),
    // The chat tests close plants/garden#30, so they come after the recurring tests that add tasks to it. The pending
    // tests use data that the chat tests change, so they come before.
    withApps({ name: "chat", testMatch: "chat.spec.ts", dependencies: ["pending", "recurring"] }),
    // The navigation tests open the chats of the chat tests, and the page marks the messages of an open chat as seen.
    withApps({ name: "navigation", testMatch: "navigation.spec.ts", dependencies: ["chat"] }),

    // Group b.
    // The installation tests add repositories and remove them again, so the tests that show the repositories of an
    // organization come after them.
    withApps({ name: "installations", testMatch: "installations.spec.ts" }),
    withApps({ name: "memory", testMatch: "memory.spec.ts", dependencies: ["installations"] }),
    withApps({ name: "agents", testMatch: "agents.spec.ts", dependencies: ["installations"] }),
    withApps({ name: "checkup", testMatch: "checkup.spec.ts", dependencies: ["agents"] }),
    withApps({ name: "inbox", testMatch: "inbox.spec.ts", dependencies: ["agents"] }),
    // The action tests add actions and Inbox items, and each test decides its actions. They run after the tests that show
    // the Inbox.
    withApps({
      name: "actions",
      testMatch: "actions.spec.ts",
      dependencies: ["agents", "checkup", "inbox", "memory"],
    }),
    // The live tests add two Owner messages to the Lead chat of plants/garden#12, which no later spec reads. They come
    // after the action tests, which also use plants/garden#12.
    withApps({ name: "live", testMatch: "live.spec.ts", dependencies: ["checkup", "actions"] }),
    // The setting tests set and clear the Harness settings of owner/shop#12 and #39. Each setting adds a record to the
    // chat of owner/shop#12, so they run after the tests that read that chat.
    withApps({
      name: "issue-settings",
      testMatch: "issue-settings.spec.ts",
      dependencies: ["live"],
    }),
    // The attachment tests open chats, which marks their messages as seen, so they run after the tests that read the
    // unread messages.
    withApps({ name: "attachments", testMatch: "attachments.spec.ts", dependencies: ["live"] }),

    // Group c.
    withApps({ name: "voice", testMatch: "voice.spec.ts" }),
    withApps({ name: "voice-language", testMatch: "voice-language.spec.ts" }),
    withApps({ name: "usage", testMatch: "usage.spec.ts" }),
    withApps({ name: "settings", testMatch: "settings.spec.ts" }),
    withApps({ name: "roles", testMatch: "roles.spec.ts" }),
    withApps({ name: "switches", testMatch: "switches.spec.ts" }),
    withApps({ name: "notice", testMatch: "notice.spec.ts" }),
    withApps({ name: "tasks", testMatch: "tasks.spec.ts" }),
    withApps({ name: "answered", testMatch: "answered.spec.ts" }),
    // The selection tests select the servers of plants/garden#18, which no other spec reads. The server tests add MCP
    // servers to the organization owner, and the selection tests use the organization plants.
    withApps({ name: "mcp-selection", testMatch: "mcp-selection.spec.ts" }),
    withApps({ name: "mcp-servers", testMatch: "mcp-servers.spec.ts" }),
  ],
  use: {
    baseURL: `http://${addr}`,
    trace: "retain-on-failure",
    userAgent: "Mobius screenshots",
    // The chat day separators and the clocks show the local time zone and the locale.
    timezoneId: "UTC",
    locale: "en-US",
    // page.route does not see the requests that pass through a service worker.
    serviceWorkers: "block",
    // With partial raster, Chrome paints only the changed part of a tile again, and the edges of that part can differ from run to run.
    launchOptions: { args: ["--disable-partial-raster"] },
  },
  webServer: [
    {
      ...serverCommand,
      url: `http://${addr}`,
      // The cron expressions of the recurring tasks use the time zone of the server.
      env: { MOBIUS_E2E_ADDR: addr, TZ: "UTC" },
    },
    {
      ...serverCommand,
      url: `http://${appsAddr}/e2e/ready`,
      env: { MOBIUS_E2E_ADDR: appsAddr, MOBIUS_E2E_APPS: "1", TZ: "UTC" },
    },
  ],
});
