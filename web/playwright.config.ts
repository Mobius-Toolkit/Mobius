import { createServer } from "node:net";
import { defineConfig } from "@playwright/test";

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
const addr = `127.0.0.1:${process.env.MOBIUS_E2E_PORT}`;

export default defineConfig({
  testDir: "e2e",
  projects: [
    { name: "screenshots", testMatch: "screenshots.spec.ts" },
    // The pending tests need the Apps of the screenshots test. They use the data that the chat tests change.
    { name: "pending", testMatch: "pending.spec.ts", dependencies: ["screenshots"] },
    // The chat tests need the Apps of the screenshots test, and they change the data of the screenshots.
    { name: "chat", testMatch: "chat.spec.ts", dependencies: ["pending"] },
    // The navigation tests open the chats of the chat tests, and the page marks the messages of an open chat as seen.
    {
      name: "navigation",
      testMatch: "navigation.spec.ts",
      dependencies: ["chat"],
    },
    // The installation test adds a repository and removes it again, so it comes after the tests that show the repositories.
    {
      name: "installations",
      testMatch: "installations.spec.ts",
      dependencies: ["navigation"],
    },
    // The memory test edits the memory file of plants/garden and reverts the edit.
    { name: "memory", testMatch: "memory.spec.ts", dependencies: ["installations"] },
    { name: "agents", testMatch: "agents.spec.ts", dependencies: ["installations"] },
    // The Checkup tests give their own data with page.route, and they need the Apps of the screenshots test.
    { name: "checkup", testMatch: "checkup.spec.ts", dependencies: ["agents"] },
    // The Settings tests need the Apps of the screenshots test.
    { name: "settings", testMatch: "settings.spec.ts", dependencies: ["agents"] },
    // The Inbox tests give their own data with page.route, and they need the Apps of the screenshots test.
    { name: "inbox", testMatch: "inbox.spec.ts", dependencies: ["agents"] },
    // The Tasks tests give their own data with page.route, and they need the Apps of the screenshots test.
    { name: "tasks", testMatch: "tasks.spec.ts", dependencies: ["agents"] },
    // The answered tests give their own data with page.route, and they need the Apps of the screenshots test.
    { name: "answered", testMatch: "answered.spec.ts", dependencies: ["agents"] },
    // The action tests add actions and Inbox items, and each test decides its actions. They run after the tests that show the Inbox.
    {
      name: "actions",
      testMatch: "actions.spec.ts",
      dependencies: ["agents", "checkup", "inbox", "memory"],
    },
    // The voice tests change no data, but they need the Apps of the screenshots test.
    { name: "voice", testMatch: "voice.spec.ts", dependencies: ["screenshots"] },
    // The usage tests change no data, but they need the Apps of the screenshots test.
    { name: "usage", testMatch: "usage.spec.ts", dependencies: ["screenshots"] },
    // The live tests change no data, but they need the Apps of the screenshots test.
    { name: "live", testMatch: "live.spec.ts", dependencies: ["checkup", "actions"] },
    // The attachment tests give their own data with page.route, and they open chats, which marks their messages as seen.
    // Thus they run last.
    { name: "attachments", testMatch: "attachments.spec.ts", dependencies: ["live"] },
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
  webServer: {
    // A cached test result ends at once and serves nothing, so -count=1 runs the server each time.
    command: "go test -count=1 -timeout=0 -run=^TestServer$ ./e2e",
    url: `http://${addr}`,
    env: { MOBIUS_E2E_ADDR: addr },
    gracefulShutdown: { signal: "SIGTERM", timeout: 10_000 },
    timeout: 120_000,
  },
});
