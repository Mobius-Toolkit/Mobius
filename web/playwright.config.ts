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
process.env.MOBIUS_E2E_PORT_A ??= await freePort();
process.env.MOBIUS_E2E_PORT_B ??= await freePort();
process.env.MOBIUS_E2E_PORT_C ??= await freePort();
const addr = `127.0.0.1:${process.env.MOBIUS_E2E_PORT}`;
const groupAddr = {
  a: `127.0.0.1:${process.env.MOBIUS_E2E_PORT_A}`,
  b: `127.0.0.1:${process.env.MOBIUS_E2E_PORT_B}`,
  c: `127.0.0.1:${process.env.MOBIUS_E2E_PORT_C}`,
};

// The server of the project screenshots has no Apps, because that test creates them. The groups a, b and c each have
// their own server with the two Apps from its start. The specs change the global state of a server (the Start switch,
// the role limits, the organizations, the Lead sessions that wait), so two groups must not share one server.
const inGroup =
  (group: keyof typeof groupAddr) =>
  (project: PlaywrightTestProject): PlaywrightTestProject => ({
    ...project,
    use: { baseURL: `http://${groupAddr[group]}` },
  });
const groupA = inGroup("a");
const groupB = inGroup("b");
const groupC = inGroup("c");

const serverCommand = {
  // A cached test result ends at once and serves nothing, so -count=1 runs the server each time.
  command: "go test -count=1 -timeout=0 -run=^TestServer$ ./e2e",
  gracefulShutdown: { signal: "SIGTERM", timeout: 10_000 },
  timeout: 120_000,
} as const;

// The test creates the Apps in the UI, and it changes data that the other groups must not see.
const projectsScreenshots = [{ name: "screenshots", testMatch: "screenshots.spec.ts" }];

const projectsA = [
  groupA({ name: "pending", testMatch: "pending.spec.ts" }),
  groupA({ name: "recurring", testMatch: "recurring.spec.ts" }),
  // The chat tests close plants/garden#30, so they come after the recurring tests that add tasks to it. The pending
  // tests use data that the chat tests change, so they come before.
  groupA({ name: "chat", testMatch: "chat.spec.ts", dependencies: ["pending", "recurring"] }),
  // The navigation tests open the chats of the chat tests, and the page marks the messages of an open chat as seen.
  groupA({ name: "navigation", testMatch: "navigation.spec.ts", dependencies: ["chat"] }),
];

const projectsB = [
  // The installation tests add repositories and remove them again. The memory tests list the repositories of each
  // organization, and the Checkup tests give the page the data of only the organizations owner and plants, so they
  // come after.
  groupB({ name: "installations", testMatch: "installations.spec.ts" }),
  groupB({ name: "memory", testMatch: "memory.spec.ts", dependencies: ["installations"] }),
  groupB({ name: "agents", testMatch: "agents.spec.ts" }),
  groupB({ name: "checkup", testMatch: "checkup.spec.ts", dependencies: ["installations"] }),
  groupB({ name: "inbox", testMatch: "inbox.spec.ts" }),
  // The action tests add actions and Inbox items, and each test decides its actions. They run after the inbox tests,
  // which read the Inbox. They also start Lead and Coordinator sessions, so they run after the agents tests, which
  // count the agents.
  groupB({ name: "actions", testMatch: "actions.spec.ts", dependencies: ["agents", "inbox"] }),
  // The live tests add two Owner messages to the Lead chat of plants/garden#12, which no later spec reads. They come
  // after the action tests, which also use plants/garden#12.
  groupB({ name: "live", testMatch: "live.spec.ts", dependencies: ["actions"] }),
  // The setting tests set and clear the Harness settings of owner/shop#12 and #39. Each setting adds a record to the
  // chat of owner/shop#12, so they run after the tests that read that chat.
  groupB({
    name: "issue-settings",
    testMatch: "issue-settings.spec.ts",
    dependencies: ["live"],
  }),
  // The attachment tests open chats, which marks their messages as seen, so they run after the tests that read the
  // unread messages.
  groupB({ name: "attachments", testMatch: "attachments.spec.ts", dependencies: ["live"] }),
];

const projectsC = [
  groupC({ name: "voice", testMatch: "voice.spec.ts" }),
  groupC({ name: "voice-language", testMatch: "voice-language.spec.ts" }),
  groupC({ name: "usage", testMatch: "usage.spec.ts" }),
  groupC({ name: "settings", testMatch: "settings.spec.ts" }),
  groupC({ name: "roles", testMatch: "roles.spec.ts" }),
  groupC({ name: "switches", testMatch: "switches.spec.ts" }),
  groupC({ name: "notice", testMatch: "notice.spec.ts" }),
  groupC({ name: "tasks", testMatch: "tasks.spec.ts" }),
  groupC({ name: "answered", testMatch: "answered.spec.ts" }),
  // The selection tests select the servers of plants/garden#18, which no other spec reads. The server tests add MCP
  // servers to the organization owner, and the selection tests use the organization plants.
  groupC({ name: "mcp-selection", testMatch: "mcp-selection.spec.ts" }),
  groupC({ name: "mcp-servers", testMatch: "mcp-servers.spec.ts" }),
];

const screenshotsServer = {
  ...serverCommand,
  url: `http://${addr}`,
  // The cron expressions of the recurring tasks use the time zone of the server.
  env: { MOBIUS_E2E_ADDR: addr, TZ: "UTC" },
};
const groupServer = (group: keyof typeof groupAddr) => ({
  ...serverCommand,
  url: `http://${groupAddr[group]}/e2e/ready`,
  env: { MOBIUS_E2E_ADDR: groupAddr[group], MOBIUS_E2E_APPS: "1", TZ: "UTC" },
});
const groups = {
  screenshots: { projects: projectsScreenshots, server: screenshotsServer },
  a: { projects: projectsA, server: groupServer("a") },
  b: { projects: projectsB, server: groupServer("b") },
  c: { projects: projectsC, server: groupServer("c") },
};

// MOBIUS_E2E_GROUP selects the projects and the server of one group. With no value, all groups run.
const selected = Object.entries(groups)
  .filter(
    ([name]) => process.env.MOBIUS_E2E_GROUP === undefined || name === process.env.MOBIUS_E2E_GROUP,
  )
  .map(([, group]) => group);

export default defineConfig({
  testDir: "e2e",
  // The list reporter gives the time of each test in the CI log.
  reporter: "list",
  projects: selected.flatMap((group) => group.projects),
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
  webServer: selected.map((group) => group.server),
});
