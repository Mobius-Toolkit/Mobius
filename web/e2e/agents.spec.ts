import { expect, test, type Locator, type Page, type Route } from "@playwright/test";
import { routeLongNameOfPaused } from "./agents.js";
import { cancelDrain } from "./upgrade.js";

test.beforeEach(async ({ page }) => {
  await page.goto("/github");
  await page.getByLabel("Access password").fill("correct horse");
  await page.getByRole("button", { name: "Log in" }).click();
  await expect(page.getByLabel("Access password")).toBeHidden();
  await cancelDrain(page);
});

// The Curators of the repositories use the paused Harness too, so more than one entry shows the pause.
const states = [
  { reason: "runs .mobius/check", badge: "checks", dot: /bg-green-600/ },
  { reason: "waits for a check slot", badge: "waits for check", dot: /bg-amber-500/ },
  {
    reason: /paused until Sep 28, 12:00\sPM/,
    badge: /^paused until Sep 28, 12:00\sPM$/,
    dot: /bg-amber-500/,
  },
  { reason: "no free Implementer slot (2/2)", badge: "queued", dot: /bg-amber-500/ },
];

test("the Agents page shows the state of each agent with a queue reason", async ({ page }) => {
  const main = page.getByRole("main");
  await page.goto("/agents");
  for (const { reason, badge, dot } of states) {
    const entry = main.getByRole("button").filter({ hasText: reason }).first();
    await expect(entry.getByText(badge, { exact: true })).toBeVisible();
    await expect(entry.locator("span.rounded-full")).toHaveClass(dot);
  }
  const running = main.getByRole("button", { name: /Ticket #41 Add plan model/ });
  await expect(running.locator("span.rounded-full")).toHaveClass(/bg-green-600/);
  await expect(running.getByText("queued")).toBeHidden();
});

test("the Agents page shows the Harness, the repository and the start time of an agent in two lines, with no role", async ({
  page,
}) => {
  const main = page.getByRole("main");
  await page.goto("/agents");
  const card = main.getByRole("button", { name: /Ticket #41 Add plan model/ });
  await expect(card.getByText("devin · swe-1.5 · high", { exact: true })).toBeVisible();
  await expect(card.getByText(/^owner\/shop · Sep \d+, \d\d:\d\d [AP]M$/)).toBeVisible();
  await expect(card.getByText("implementer ·")).toHaveCount(0);
  await expect(card.getByText("owner ·")).toHaveCount(0);
});

test("the Agents page shows the organization of an agent that has no repository to show", async ({
  page,
}) => {
  await page.route("**/api/agents", async (route) => {
    const response = await route.fetch();
    const body = await response.json();
    for (const group of body.data.groups) {
      for (const row of group.agents) {
        if (row.agent.issue === 41) {
          row.agent.workstream = 0;
          row.agent.issue = null;
        }
      }
    }
    await route.fulfill({ response, json: body });
  });
  await page.goto("/agents");
  const card = page.getByRole("main").getByRole("button", { name: /Pull request #44/ });
  await expect(card.getByText(/^owner · Sep \d+, \d\d:\d\d [AP]M$/)).toBeVisible();
  await expect(card.getByText("owner/shop")).toHaveCount(0);
});

test("on a phone, a card of the Agents page puts a long badge under the name and a short badge in the first line", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  const wrappedTitle = "owner/very-long-repository-name-that-wraps-on-a-phone";
  await routeLongNameOfPaused(page, { issue: 41, title: wrappedTitle });
  await page.goto("/agents");
  const main = page.getByRole("main");
  const long = main.getByRole("button", { name: /Ticket #45 Pick the plan limits/ });
  const longName = long.getByText("implementer of the shop plans");
  const longBadge = long.getByText(/^paused until Sep 28, 12:00\sPM$/);
  await expect(longBadge).toBeVisible();
  const [nameBox, badgeBox, harnessBox, cardBox] = [
    await boxOf(longName),
    await boxOf(longBadge),
    await boxOf(long.getByText("antigravity · sonnet")),
    await boxOf(long),
  ];
  expect(badgeBox.y).toBeGreaterThanOrEqual(nameBox.y + nameBox.height);
  expect(badgeBox.x).toBe(harnessBox.x);
  expect(harnessBox.x).toBe(cardBox.x + 16);
  expect(await long.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true,
  );

  const short = main.getByRole("button", { name: /Ticket #42 Let customers change plans/ });
  const [shortNameBox, shortBadgeBox] = [
    await boxOf(short.getByText("implementer", { exact: true })),
    await boxOf(short.getByText("queued", { exact: true })),
  ];
  expect(shortBadgeBox.y).toBeLessThan(shortNameBox.y + shortNameBox.height);

  const wrapped = main.getByRole("button", { name: /Ticket #41 Add plan model/ });
  const [wrappedNameBox, wrappedDotBox, wrappedChevronBox] = [
    await boxOf(wrapped.getByText(`implementer ${wrappedTitle}`, { exact: true })),
    await boxOf(wrapped.locator("span.rounded-full")),
    await boxOf(wrapped.locator("svg")),
  ];
  expect(wrappedNameBox.height).toBeGreaterThan(shortNameBox.height * 1.5);
  const firstLineCenter = wrappedNameBox.y + shortNameBox.height / 2;
  expect(Math.abs(wrappedDotBox.y + wrappedDotBox.height / 2 - firstLineCenter)).toBeLessThan(1);
  expect(
    Math.abs(wrappedChevronBox.y + wrappedChevronBox.height / 2 - firstLineCenter),
  ).toBeLessThan(1);
});

test("a task that waits for a Harness pause shows the same badge on the Tasks board, in the Workstream tree and on the Agents page", async ({
  page,
}) => {
  const paused = /^paused until Sep 28, 12:00\sPM$/;

  await page.goto("/tasks");
  const card = page.getByRole("listitem").filter({ hasText: "Pick the plan limits" });
  await expect(card.getByText(paused)).toBeVisible();

  await page.setViewportSize({ width: 1280, height: 800 });
  await page.goto("/workstreams/owner/shop/12");
  await page.getByRole("tab", { name: "Tasks" }).filter({ visible: true }).click();
  const line = page
    .getByRole("complementary")
    .getByRole("listitem")
    .filter({ hasText: "#45 Pick the plan limits" });
  await expect(line.getByText(paused)).toBeVisible();

  await page.goto("/agents");
  const entry = page
    .getByRole("main")
    .getByRole("button", { name: /Ticket #45 Pick the plan limits/ });
  await expect(entry.getByText(paused)).toBeVisible();
});

test("the Agents page shows the count of each Role and no global count", async ({ page }) => {
  await page.goto("/agents");
  await expect(page.getByRole("heading", { name: "Implementer 2 / 2" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Reviewer 0 / 2" })).toBeVisible();
  await expect(page.getByRole("banner").getByText(/^\d+ \/ \d+$/)).toBeHidden();
  await expect(page.getByRole("main").getByText(/^\d+ \/ \d+$/)).toBeHidden();
});

test("the Agents page shows the agents after a move inside the app with an open live connection", async ({
  page,
}) => {
  const opened = page.waitForResponse("/api/events");
  await page.goto("/settings");
  await opened;
  await expect(page.getByRole("main").getByRole("link", { name: "MCP servers" })).toBeVisible();
  await expect(page.getByRole("status")).toBeEmpty();

  await page.getByRole("navigation").getByRole("link", { name: "Agents" }).click();
  await expect(page).toHaveURL("/agents");
  const entry = page
    .getByRole("main")
    .getByRole("button")
    .filter({ hasText: "no free Implementer slot (2/2)" })
    .first();
  await expect(entry.getByText("queued", { exact: true })).toBeVisible();
});

test("the Agents tab of a Workstream shows the state of each agent with a queue reason", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.goto("/workstreams/owner/shop/12");
  await page.getByRole("tab", { name: "Agents" }).filter({ visible: true }).click();
  const tree = page.getByRole("complementary");
  for (const { reason, badge, dot } of states) {
    const entry = tree.getByRole("listitem").filter({ hasText: reason }).first();
    await expect(entry.getByText(badge, { exact: true })).toBeVisible();
    await expect(entry.locator("span.rounded-full")).toHaveClass(dot);
  }
});

// The engine clock of TestServer is 2026-09-28 09:30 UTC, so the engine closes the kept session of plants/garden#85 at
// 10:30 UTC. The test closes it, so it runs one time for each server.
test("the Agents tab of a Workstream shows a kept session with a countdown, and removes it at the close", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.clock.install({ time: new Date("2026-09-28T09:40:00Z") });
  await page.goto("/api/github/user-callback?code=user-code");
  await page.goto("/workstreams/plants/garden/11");
  await page.getByRole("tab", { name: "Agents" }).filter({ visible: true }).click();
  const tree = page.getByRole("complementary");
  const kept = tree.getByRole("listitem").filter({ hasText: "closes in" });
  await expect(kept).toHaveCount(1);
  await expect(kept).toContainText("closes in 50 min");
  await expect(kept.getByText("kept", { exact: true })).toBeVisible();
  await expect(kept.getByText("stopped")).toBeHidden();
  await expect(kept.locator("span.rounded-full")).toHaveClass(/border-muted-foreground/);

  await page.clock.fastForward(60_000);
  await expect(kept).toContainText("closes in 49 min");

  await page.getByRole("tab", { name: "Tasks" }).filter({ visible: true }).click();
  const stopped = page.waitForResponse((response) => response.url().endsWith("/issues/85/stop"));
  await page.getByRole("button", { name: "Stop #85" }).click();
  expect((await stopped).ok()).toBe(true);
  await page.getByRole("tab", { name: "Agents" }).filter({ visible: true }).click();
  await expect(kept).toHaveCount(0);
  const implementer = tree.getByRole("listitem").filter({ hasText: /^Implementer.*devin/ });
  await expect(implementer).toHaveCount(0);
  await tree.getByRole("switch", { name: "Show stopped agents" }).click();
  await expect(implementer).toHaveCount(1);
  await expect(implementer.getByText("stopped", { exact: true })).toBeVisible();
  await expect(implementer).not.toContainText("closes in");
});

// Changes the live Implementer session of #41, and sets the old reason again at the end.
async function withQueueReason(page: Page, reason: string, wait: string, run: () => Promise<void>) {
  const put = (value: string, kind: string) =>
    page.request.put(
      `/e2e/agents/41/queue-reason?reason=${encodeURIComponent(value)}&wait=${kind}`,
    );
  expect((await put(reason, wait)).ok()).toBe(true);
  try {
    await run();
  } finally {
    expect((await put("", "")).ok()).toBe(true);
  }
}

test("the Agents page shows the state of a session that waits for a low load", async ({ page }) => {
  await withQueueReason(page, "waits for a low load", "low_load", async () => {
    await page.goto("/agents");
    const entry = page.getByRole("main").getByRole("button", { name: /Ticket #41/ });
    await expect(entry.getByText("waits for a low load")).toBeVisible();
    await expect(entry.getByText("waits for load", { exact: true })).toBeVisible();
    await expect(entry.locator("span.rounded-full")).toHaveClass(/bg-amber-500/);
  });
});

test("the Agents page shows the state of a session that runs .mobius/init", async ({ page }) => {
  await withQueueReason(page, "runs .mobius/init", "init", async () => {
    await page.goto("/agents");
    const entry = page.getByRole("main").getByRole("button", { name: /Ticket #41/ });
    await expect(entry.getByText("runs .mobius/init")).toBeVisible();
    await expect(entry.getByText("setup", { exact: true })).toBeVisible();
    await expect(entry.locator("span.rounded-full")).toHaveClass(/bg-green-600/);
  });
});

test("the Agents page shows the state of a session whose Harness starts", async ({ page }) => {
  await withQueueReason(page, "starts devin", "harness_start", async () => {
    await page.goto("/agents");
    const entry = page.getByRole("main").getByRole("button", { name: /Ticket #41/ });
    await expect(entry.getByText("starts devin")).toBeVisible();
    await expect(entry.getByText("starting", { exact: true })).toBeVisible();
    await expect(entry.locator("span.rounded-full")).toHaveClass(/bg-amber-500/);
  });
});

test("the Agents tab of a Workstream shows the state of a session whose Harness starts", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1280, height: 800 });
  await withQueueReason(page, "starts devin", "harness_start", async () => {
    await page.goto("/workstreams/owner/shop/12");
    await page.getByRole("tab", { name: "Agents" }).filter({ visible: true }).click();
    const entry = page
      .getByRole("complementary")
      .getByRole("listitem")
      .filter({ hasText: "starts devin" });
    await expect(entry).toHaveCount(1);
    await expect(entry.getByText("starting", { exact: true })).toBeVisible();
    await expect(entry.locator("span.rounded-full")).toHaveClass(/bg-amber-500/);
  });
});

test("the Agents tab of a Workstream shows the state of a session that waits for a low load", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1280, height: 800 });
  await withQueueReason(page, "waits for a low load", "low_load", async () => {
    await page.goto("/workstreams/owner/shop/12");
    await page.getByRole("tab", { name: "Agents" }).filter({ visible: true }).click();
    const entry = page
      .getByRole("complementary")
      .getByRole("listitem")
      .filter({ hasText: "waits for a low load" });
    await expect(entry).toHaveCount(1);
    await expect(entry.getByText("waits for load", { exact: true })).toBeVisible();
    await expect(entry.locator("span.rounded-full")).toHaveClass(/bg-amber-500/);
  });
});

test("the Agents tab of a Workstream names the issue of an agent with a link, and a tap on the row opens the Transcript", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.goto("/workstreams/owner/shop/12");
  await page.getByRole("tab", { name: "Agents" }).filter({ visible: true }).click();
  const panel = page.getByRole("complementary");
  const link = panel.getByRole("link", { name: "#41" });
  const entry = panel
    .getByRole("listitem")
    .filter({ has: page.getByRole("link", { name: "#41" }) });
  await expect(entry.getByText("Implementer", { exact: true })).toBeVisible();
  await expect(entry.getByText("Add plan model", { exact: true })).toBeVisible();
  await expect(entry.getByText("devin · swe-1.5 · high", { exact: true })).toBeVisible();
  await expect(link).toHaveAttribute("href", "https://github.com/owner/shop/issues/41");
  await expect(link).toHaveAttribute("target", "_blank");
  await link.click({ trial: true });

  await entry.getByRole("button").click({ position: { x: 4, y: 4 } });
  await expect(panel.getByRole("button", { name: "Back" })).toBeVisible();
});

test("the Agents tab of a Workstream keeps a long issue title on one line on a phone", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  const title = "Read the pull request of a task in checks again at each poll ".repeat(4).trim();
  const wrappedTitle = "owner/very-long-repository-name-that-wraps-on-a-phone";
  await page.route("**/api/workstreams/owner/shop/12/agents", async (route) => {
    const response = await route.fetch();
    const body = await response.json();
    for (const agent of body.data) {
      if (agent.issue === 41) {
        agent.title = wrappedTitle;
        agent.issueTitle = title;
      }
    }
    await route.fulfill({ response, json: body });
  });
  await page.goto("/workstreams/owner/shop/12");
  await page.getByRole("banner").getByRole("button", { name: "Tasks", exact: true }).click();
  await page.getByRole("tab", { name: "Agents" }).filter({ visible: true }).click();
  const entry = page
    .getByRole("listitem")
    .filter({ hasText: /Read the pull request/ })
    .filter({ visible: true });
  const issueTitle = entry.getByText(title, { exact: true });
  await expect(issueTitle).toBeVisible();
  const link = entry.getByRole("link", { name: "#41" });
  const [titleBox, linkBox] = [await boxOf(issueTitle), await boxOf(link)];
  expect(titleBox.height).toBeLessThan(linkBox.height * 1.5);
  expect(Math.abs(titleBox.y - linkBox.y)).toBeLessThan(linkBox.height);
  expect(await entry.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);
  expect(await issueTitle.evaluate((element) => element.scrollWidth > element.clientWidth)).toBe(
    true,
  );
  const nameElement = entry.getByText(`Implementer ${wrappedTitle}`, { exact: true });
  const [nameBox, dotBox] = [
    await boxOf(nameElement),
    await boxOf(entry.locator("span.rounded-full")),
  ];
  const lineHeight = await nameElement.evaluate((el) =>
    Number.parseFloat(getComputedStyle(el).lineHeight),
  );
  expect(nameBox.height).toBeGreaterThan(lineHeight * 1.5);
  expect(Math.abs(dotBox.y + dotBox.height / 2 - (nameBox.y + lineHeight / 2))).toBeLessThan(1);
});

test.describe("the transcript of an agent on a phone", () => {
  test.use({ viewport: { width: 390, height: 844 } });

  test("has the agent name and the back button in the top bar, and the back button closes it", async ({
    page,
  }) => {
    await page.goto("/agents");
    await page
      .getByRole("main")
      .getByRole("button", { name: /Ticket #41 Add plan model/ })
      .click();
    const bar = page.getByRole("banner");
    await expect(bar.getByRole("heading")).toHaveText("#41 Add plan model");
    await expect(page.getByRole("main").getByRole("button", { name: "Back" })).toBeHidden();
    await bar.getByRole("button", { name: "Back" }).click();
    await expect(page).toHaveURL("/agents");
    await expect(bar.getByRole("heading")).toHaveText("Agents");
  });
});

// The log of the Implementer of #41 has 30 entries, and a new entry comes as a server-sent event. The tests have no DOM
// types, so the checks are scripts.
const entry = (id: number) => ({
  id,
  session: 0,
  kind: "update",
  time: "2026-09-28T12:00:00Z",
  text: `Entry ${id} ${"word ".repeat(30)}`,
  body: "",
  folded: false,
  error: false,
  harnessToolName: "",
  raw: "{}",
});

function gate() {
  const resolvers: Array<() => void> = [];
  const wait = new Promise<void>((resolve) => resolvers.push(resolve));
  return { wait, open: () => resolvers[0]() };
}

async function mockLog(page: Page, secondRead?: Promise<void>) {
  await page.addInitScript(`
    window.sources = [];
    const add = EventSource.prototype.addEventListener;
    EventSource.prototype.addEventListener = function (type, listener, options) {
      if (this.url.endsWith('/transcript/events') && !window.sources.includes(this)) {
        window.sources.push(this);
      }
      return add.call(this, type, listener, options);
    };
  `);
  let session = 0;
  let reads = 0;
  await page.route(/\/api\/agents\/\d+\/transcript(\?.*)?$/, async (route) => {
    session = Number(/agents\/(\d+)\//.exec(route.request().url())?.[1]);
    reads += 1;
    if (reads > 1) {
      await secondRead;
    }
    return route.fulfill({
      json: {
        data: {
          lines: Array.from({ length: 30 }, (_, n) => ({ ...entry(n), session })),
          older: false,
        },
      },
    });
  });
  return (id: number) =>
    page.evaluate(
      `window.sources.at(-1).dispatchEvent(new MessageEvent('transcript', { data: ${JSON.stringify(
        JSON.stringify({ ...entry(id), session }),
      )} }))`,
    );
}

const log = `[...document.querySelectorAll('li')].find((element) => element.textContent.includes('Entry 0 ')).parentElement`;
const scrolls = `(() => { const list = ${log}; return list.scrollHeight > list.clientHeight })()`;
const gap = `(() => { const list = ${log}; return list.scrollHeight - list.scrollTop - list.clientHeight })()`;
// The scroll event of a script scroll comes after the script, and the log reads its place from that event.
const scrollTo = (page: Page, place: "top" | "end") =>
  page.evaluate(`new Promise((resolve) => {
    const list = ${log}
    list.addEventListener('scroll', () => requestAnimationFrame(resolve), { once: true })
    list.scrollTop = ${place === "top" ? "0" : "list.scrollHeight"}
  })`);

async function openLog(
  page: Page,
  path: string,
  open: (page: Page) => Promise<void>,
  secondRead?: Promise<void>,
) {
  const send = await mockLog(page, secondRead);
  await page.goto(path);
  await open(page);
  await expect(page.getByText("Entry 29 ")).toBeVisible();
  expect(await page.evaluate(scrolls)).toBe(true);
  if (!secondRead) {
    // The stream opens after the first read and starts the second read. Its lines pin the log to the end again.
    await expect.poll(() => page.evaluate(`${log}.dataset.version`)).toBe("2");
  }
  await expect.poll(() => page.evaluate(gap)).toBeLessThan(5);
  return send;
}

const button = (page: Page) => page.getByRole("button", { name: "New messages" });

const places = [
  {
    name: "the Agents page",
    path: "/agents",
    open: async (page: Page) => {
      await page
        .getByRole("main")
        .getByRole("button", { name: /Ticket #41 Add plan model/ })
        .click();
    },
  },
  {
    name: "the side panel",
    path: "/workstreams/owner/shop/12",
    open: async (page: Page) => {
      if (page.viewportSize()!.width < 768) {
        await page.getByRole("banner").getByRole("button", { name: "Tasks", exact: true }).click();
      }
      await page.getByRole("tab", { name: "Agents" }).filter({ visible: true }).click();
      await page
        .getByRole("button", {
          name: /^Implementer #41 Add plan model devin · swe-1.5 · high · Sep \d+,/,
        })
        .filter({ visible: true })
        .click();
    },
  },
];
const devices = [
  { name: "a desktop", size: { width: 1280, height: 720 } },
  { name: "a phone", size: { width: 390, height: 844 } },
];

for (const { name: place, path, open } of places) {
  for (const { name: device, size } of devices) {
    test.describe(`the agent log in ${place} on ${device}`, () => {
      test.use({ viewport: size });

      test("opens at the last entry and keeps the header in view", async ({ page }) => {
        await openLog(page, path, open);
        await expect.poll(() => page.evaluate(gap)).toBeLessThan(5);
        const phonePage = path === "/agents" && size.width < 768;
        await expect(
          phonePage
            ? page.getByRole("banner").getByRole("button", { name: "Back" })
            : path === "/agents"
              ? page.getByRole("main").getByRole("button", { name: "Back" })
              : page.locator('[data-slot="tabs-content"]').getByRole("button", { name: "Back" }),
        ).toBeInViewport();
        await expect(button(page)).toBeHidden();
      });

      test("opens at the end before the second read of the lines arrives", async ({ page }) => {
        const second = gate();
        // A slow page runs the mount after the first lines are in the list.
        const cdp = await page.context().newCDPSession(page);
        await cdp.send("Emulation.setCPUThrottlingRate", { rate: 20 });
        const version = `${log}.dataset.version`;
        await openLog(page, path, open, second.wait);
        const before = await page.evaluate(version);
        second.open();
        await expect.poll(() => page.evaluate(version)).not.toBe(before);
        await expect.poll(() => page.evaluate(gap)).toBeLessThan(5);
      });

      test("follows a new entry while the Owner is at the end", async ({ page }) => {
        const send = await openLog(page, path, open);
        await send(30);
        await expect(page.getByText("Entry 30 ")).toBeVisible();
        await expect.poll(() => page.evaluate(gap)).toBeLessThan(5);
        await expect(button(page)).toBeHidden();
      });

      test("stays in place and shows the button when the Owner scrolled up", async ({ page }) => {
        const send = await openLog(page, path, open);
        await scrollTo(page, "top");
        await expect.poll(() => page.evaluate(gap)).toBeGreaterThan(100);
        await send(30);
        await expect(button(page)).toBeVisible();
        expect(await page.evaluate(`${log}.scrollTop`)).toBe(0);
        await expect(page.getByText("Entry 0 ")).toBeInViewport();
      });

      test("stays in place when the lines change before the scroll event of the Owner", async ({
        page,
      }) => {
        await openLog(page, path, open);
        await page.evaluate(`new Promise((resolve) => {
          const list = ${log}
          list.scrollTop = 0
          list.dataset.version = String(Number(list.dataset.version) + 1)
          requestAnimationFrame(() => requestAnimationFrame(resolve))
        })`);
        await expect(button(page)).toBeVisible();
        expect(await page.evaluate(`${log}.scrollTop`)).toBe(0);
        await expect(page.getByText("Entry 0 ")).toBeInViewport();
      });

      test("follows a new entry after the box of the log becomes lower", async ({ page }) => {
        const send = await openLog(page, path, open);
        await page.setViewportSize({ width: size.width, height: size.height - 200 });
        await expect.poll(() => page.evaluate(gap)).toBeGreaterThan(100);
        await send(30);
        await expect(page.getByText("Entry 30 ")).toBeVisible();
        await expect.poll(() => page.evaluate(gap)).toBeLessThan(5);
        await expect(button(page)).toBeHidden();
      });

      test("moves to the end and removes the button when the Owner clicks it", async ({ page }) => {
        const send = await openLog(page, path, open);
        await scrollTo(page, "top");
        await send(30);
        await button(page).click();
        await expect(button(page)).toBeHidden();
        await expect.poll(() => page.evaluate(gap)).toBeLessThan(5);
        await expect(page.getByText("Entry 30 ")).toBeInViewport();
        await send(31);
        await expect(page.getByText("Entry 31 ")).toBeInViewport();
      });

      test("removes the button when the Owner scrolls to the end", async ({ page }) => {
        const send = await openLog(page, path, open);
        await scrollTo(page, "top");
        await send(30);
        await expect(button(page)).toBeVisible();
        await scrollTo(page, "end");
        await expect(button(page)).toBeHidden();
        await send(31);
        await expect(page.getByText("Entry 31 ")).toBeInViewport();
      });
    });
  }
}

for (const { name: device, size } of devices) {
  test.describe(`the agent log in the Agents tab on ${device}`, () => {
    test.use({ viewport: size });

    test("has no card and fills the height of the tab", async ({ page }) => {
      await openLog(page, places[1].path, places[1].open);
      const tab = page.locator('[data-slot="tabs-content"]').filter({ visible: true });
      await expect(tab.locator('[data-slot="card"]')).toHaveCount(0);
      await expect(tab.getByRole("button", { name: "Back" })).toBeVisible();
      await expect(tab.locator(".line-clamp-2")).toContainText("#41 Add plan model");
      const note = tab.getByText("Read only. The Owner talks only to the Lead.");
      const tabBox = await boxOf(tab);
      const noteBox = await boxOf(note);
      const padding = 8;
      expect(tabBox.y + tabBox.height - (noteBox.y + noteBox.height)).toBeGreaterThanOrEqual(0);
      expect(tabBox.y + tabBox.height - (noteBox.y + noteBox.height)).toBeLessThanOrEqual(
        padding + 1,
      );
      expect(await tab.evaluate((el) => el.scrollHeight <= el.clientHeight)).toBe(true);
      expect(
        await page.evaluate(
          `document.documentElement.scrollHeight <= document.documentElement.clientHeight`,
        ),
      ).toBe(true);
    });
  });
}

for (const { name: place, path, open } of places) {
  test(`the agent log in ${place} shows its lines at once, and Connecting… until its stream opens`, async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 720 });
    await mockLog(page);
    let stream: Route | undefined;
    await page.route("**/api/agents/*/transcript/events", (route) => {
      stream = route;
    });
    await page.goto(path);
    await open(page);
    const badge = page.getByText("Connecting…").filter({ visible: true });
    await expect(badge).toBeVisible();
    await expect(page.getByText("Entry 29 ")).toBeVisible();
    const list = page
      .getByText("Read only. The Owner talks only to the Lead.")
      .filter({ visible: true })
      .locator("xpath=preceding-sibling::div/ul");
    const placeOfList = await list.boundingBox();

    await page.unroute("**/api/agents/*/transcript/events");
    await stream!.continue();
    await expect(badge).toBeHidden();
    await expect(page.getByText("Entry 29 ")).toBeVisible();
    expect(await list.boundingBox()).toMatchObject({
      x: placeOfList!.x,
      y: placeOfList!.y,
      width: placeOfList!.width,
    });
  });
}

const retryDelay = 3000;

for (const { name: place, path, open } of places) {
  test(`the agent log in ${place} opens its stream again after the server refuses it`, async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 720 });
    const send = await mockLog(page);
    const times: number[] = [];
    await page.route("**/api/agents/*/transcript/events", (route) => {
      times.push(Date.now());
      return times.length === 1 ? route.fulfill({ status: 503 }) : route.continue();
    });
    await page.goto(path);
    await open(page);
    const badge = page.getByText("Connecting…").filter({ visible: true });
    await expect(badge).toBeVisible();
    await expect(page.getByText("Entry 29 ")).toBeVisible();
    await expect(badge).toBeHidden();
    expect(times).toHaveLength(2);
    expect(times[1] - times[0]).toBeGreaterThanOrEqual(retryDelay - 500);
    await send(30);
    await expect(page.getByText("Entry 30 ")).toBeVisible();
  });
}

test("the agent log sends no request of its stream after it closes while a retry waits", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1280, height: 720 });
  await mockLog(page);
  let requests = 0;
  await page.route("**/api/agents/*/transcript/events", (route) => {
    requests++;
    return route.fulfill({ status: 503 });
  });
  await page.goto("/agents");
  await page
    .getByRole("main")
    .getByRole("button", { name: /Ticket #41 Add plan model/ })
    .click();
  await expect.poll(() => requests).toBe(1);
  await page.getByRole("main").getByRole("button", { name: "Back" }).click();
  await expect(page.getByText("Read only. The Owner talks only to the Lead.")).toBeHidden();
  await page.waitForTimeout(retryDelay + 1000);
  expect(requests).toBe(1);
});

test("the Transcript of a session that waits for a low load has a button that starts the check", async ({
  page,
}) => {
  const main = page.getByRole("main");
  const requests: string[] = [];
  let answer: { status: number; body: string } = { status: 204, body: "" };
  await page.route(/\/api\/agents\/\d+\/start-check$/, (route) => {
    requests.push(`${route.request().method()} ${new URL(route.request().url()).pathname}`);
    return route.fulfill(answer);
  });
  await withQueueReason(page, "waits for a low load", "low_load", async () => {
    await page.goto("/agents");
    await main.getByRole("button", { name: /Ticket #41 Add plan model/ }).click();
    const start = main.getByRole("button", { name: "Start the check now" });

    answer = {
      status: 409,
      body: JSON.stringify({ error: "The check of session 3 does not wait for a low load." }),
    };
    await start.click();
    await expect(
      main.getByText("The check of session 3 does not wait for a low load."),
    ).toBeVisible();

    answer = { status: 204, body: "" };
    await start.click();
    await expect(main.getByText("does not wait for a low load.")).toBeHidden();
  });
  expect(requests).toHaveLength(2);
  expect(requests[0]).toMatch(/^POST \/api\/agents\/\d+\/start-check$/);
});

test("the Transcript of a session that does not wait for a low load has no start button", async ({
  page,
}) => {
  const main = page.getByRole("main");
  await page.goto("/agents");
  await main.getByRole("button", { name: /Ticket #39/ }).click();
  await expect(main.getByRole("button", { name: "Start the check now" })).toBeHidden();
});

async function openPanelTranscript(
  page: Page,
  name = /^Implementer #41 Add plan model devin · swe-1.5 · high · Sep \d+,/,
) {
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.goto("/workstreams/owner/shop/12");
  await page.getByRole("tab", { name: "Agents" }).filter({ visible: true }).click();
  const panel = page.getByRole("complementary");
  await panel.getByRole("button", { name }).filter({ visible: true }).click();
  return panel;
}

async function boxOf(locator: Locator) {
  const box = await locator.boundingBox();
  expect(box).not.toBeNull();
  return box!;
}

async function expectLeftOf(back: Locator, title: Locator) {
  const backBox = await boxOf(back);
  const titleBox = await boxOf(title);
  expect(backBox.x + backBox.width).toBeLessThanOrEqual(titleBox.x);
}

const roleSubtitle = "Implementer · devin · swe-1.5 · high";
const longIssueTitle = "Read the pull request of a task in checks again at each poll "
  .repeat(4)
  .trim();

async function changeAgent(page: Page, change: (agent: Record<string, unknown>) => void) {
  await page.route("**/api/agents", async (route) => {
    const response = await route.fetch();
    const body = await response.json();
    for (const group of body.data.groups) {
      for (const row of group.agents) {
        if (row.agent.issue === 41) {
          change(row.agent);
        }
      }
    }
    await route.fulfill({ response, json: body });
  });
}

const openAgentsCard = (page: Page, name: RegExp = /Ticket #41 Add plan model/) =>
  page.getByRole("main").getByRole("button", { name }).click();

test.describe("the header of the Transcript of an agent", () => {
  test("shows the task as the title and the role as the subtitle in the Workstream panel on a desktop", async ({
    page,
  }) => {
    const panel = await openPanelTranscript(page);
    await expect(panel.locator(".line-clamp-2")).toHaveText("#41 Add plan model");
    await expect(panel.getByText(roleSubtitle, { exact: true })).toBeVisible();
  });

  test("shows the task as the title and the role as the subtitle in the Workstream panel on a phone", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto("/workstreams/owner/shop/12");
    await page.getByRole("banner").getByRole("button", { name: "Tasks", exact: true }).click();
    await page.getByRole("tab", { name: "Agents" }).filter({ visible: true }).click();
    await page
      .getByRole("button", { name: /^Implementer #41 Add plan model/ })
      .filter({ visible: true })
      .click();
    const tab = page.locator('[data-slot="tabs-content"]').filter({ visible: true });
    await expect(tab.locator(".line-clamp-2")).toHaveText("#41 Add plan model");
    await expect(tab.getByText(roleSubtitle, { exact: true })).toBeVisible();
  });

  test("shows the task as the title and the role as the subtitle on the Agents page on a desktop", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await page.goto("/agents");
    await openAgentsCard(page);
    const main = page.getByRole("main");
    await expect(
      main.getByRole("heading", { name: "#41 Add plan model", exact: true }),
    ).toBeVisible();
    const subtitle = main.getByText(roleSubtitle, { exact: true }).filter({ visible: true });
    await expect(subtitle).toBeVisible();
    const [headingBox, subtitleBox] = [
      await boxOf(main.getByRole("heading")),
      await boxOf(subtitle),
    ];
    expect(subtitleBox.y).toBeGreaterThanOrEqual(headingBox.y + headingBox.height);
    expect(subtitleBox.x).toBe(headingBox.x);
  });

  test("shows the task in the top bar and the role in a muted line under it on the Agents page on a phone", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto("/agents");
    await openAgentsCard(page);
    const bar = page.getByRole("banner");
    await expect(bar.getByRole("heading")).toHaveText("#41 Add plan model");
    const subtitle = page
      .getByRole("main")
      .getByText(roleSubtitle, { exact: true })
      .filter({ visible: true });
    await expect(subtitle).toBeVisible();
    const [barBox, subtitleBox] = [await boxOf(bar), await boxOf(subtitle)];
    expect(subtitleBox.y).toBeGreaterThanOrEqual(barBox.y + barBox.height);
    await expect(page.getByRole("main").getByRole("heading")).toBeHidden();
  });

  test("has the repository in the title for an issue in another repository than its Workstream", async ({
    page,
  }) => {
    await changeAgent(page, (agent) => {
      agent.repository = "owner/site";
    });
    await page.goto("/agents");
    await openAgentsCard(page);
    await expect(
      page.getByRole("main").getByRole("heading", { name: "owner/site#41 Add plan model" }),
    ).toBeVisible();
  });

  test("is only the reference when the issue has no title", async ({ page }) => {
    await changeAgent(page, (agent) => {
      agent.issueTitle = null;
    });
    await page.goto("/agents");
    await openAgentsCard(page);
    await expect(
      page.getByRole("main").getByRole("heading", { name: "#41", exact: true }),
    ).toBeVisible();
  });

  test("shows the role as the title and only the Harness as the subtitle for an agent with no issue", async ({
    page,
  }) => {
    await changeAgent(page, (agent) => {
      agent.issue = null;
      agent.issueTitle = null;
      agent.name = "researcher";
      agent.title = "of the plans";
    });
    await page.goto("/agents");
    await openAgentsCard(page, /researcher of the plans/);
    const main = page.getByRole("main");
    await expect(
      main.getByRole("heading", { name: "Researcher of the plans", exact: true }),
    ).toBeVisible();
    await expect(
      main.getByText("devin · swe-1.5 · high", { exact: true }).filter({ visible: true }),
    ).toBeVisible();
  });

  for (const { name, size, issueTitle, start } of [
    { name: "a phone", size: { width: 390, height: 844 }, issueTitle: "Add plan model" },
    {
      name: "a phone with the start button",
      size: { width: 390, height: 844 },
      issueTitle: "Add plan model",
      start: true,
    },
    {
      name: "a desktop with a title of two lines",
      size: { width: 1280, height: 720 },
      issueTitle: longIssueTitle,
    },
    {
      name: "a desktop with a title of two lines and the start button",
      size: { width: 1280, height: 720 },
      issueTitle: longIssueTitle,
      start: true,
    },
  ]) {
    test(`keeps the log and the read only line in the viewport of the Agents page on ${name}`, async ({
      page,
    }) => {
      await page.setViewportSize(size);
      // The status line of a pause is above the page and takes height.
      await page.route("**/api/pauses", (route) => route.fulfill({ json: { data: [] } }));
      await changeAgent(page, (agent) => {
        agent.issueTitle = issueTitle;
        if (start) {
          agent.wait = "low_load";
        }
      });
      await openLog(page, "/agents", async () => openAgentsCard(page));
      const note = page.getByText("Read only. The Owner talks only to the Lead.");
      await expect(page.getByRole("button", { name: "Start the check now" })).toHaveCount(
        start ? 1 : 0,
      );
      if (start) {
        await expect(page.getByRole("button", { name: "Start the check now" })).toBeInViewport({
          ratio: 1,
        });
      }
      await expect(note).toBeInViewport({ ratio: 1 });
      if (size.width > 768) {
        const heading = page.getByRole("main").getByRole("heading");
        const lineHeight = await heading.evaluate((element) =>
          Number.parseFloat(getComputedStyle(element).lineHeight),
        );
        expect((await boxOf(heading)).height).toBeGreaterThan(lineHeight * 1.5);
      } else {
        const subtitle = page.getByRole("main").getByText(roleSubtitle, { exact: true });
        const lineHeight = await subtitle.evaluate((element) =>
          Number.parseFloat(getComputedStyle(element).lineHeight),
        );
        expect((await boxOf(subtitle)).height).toBeGreaterThanOrEqual(lineHeight);
      }
      expect(
        await page.evaluate(
          `document.documentElement.scrollHeight <= document.documentElement.clientHeight && document.getElementById('content').scrollHeight <= document.getElementById('content').clientHeight`,
        ),
      ).toBe(true);
    });
  }

  test("truncates a long subtitle on a phone and keeps the page from scrolling to the side", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await changeAgent(page, (agent) => {
      agent.title = "of-a-repository-with-a-very-long-name-that-has-no-break";
      agent.model = "a-model-with-a-very-long-name-that-has-no-break";
    });
    await page.goto("/agents");
    await openAgentsCard(page);
    const subtitle = page
      .getByRole("main")
      .getByText(/^Implementer of-a-repository/)
      .filter({ visible: true });
    await expect(subtitle).toBeVisible();
    expect(await subtitle.evaluate((element) => element.scrollWidth > element.clientWidth)).toBe(
      true,
    );
    expect(
      await page.evaluate(
        `document.getElementById('content').scrollWidth <= document.getElementById('content').clientWidth`,
      ),
    ).toBe(true);
  });
});

test.describe("the back button on a desktop", () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  test("of the transcript in the side panel is on the left of the title", async ({ page }) => {
    const panel = await openPanelTranscript(page);
    const back = panel.getByRole("button", { name: "Back" });
    const title = panel.locator(".line-clamp-2");
    await expect(title).toContainText("#41 Add plan model");
    await expectLeftOf(back, title);
    await back.click();
    await expect(
      panel.getByRole("button", {
        name: /^Implementer #41 Add plan model devin · swe-1.5 · high · Sep \d+,/,
      }),
    ).toBeVisible();
    await expect(back).toBeHidden();
  });

  test("of the transcript on the Agents page is on the left of the title", async ({ page }) => {
    await page.goto("/agents");
    const main = page.getByRole("main");
    await main.getByRole("button", { name: /Ticket #41 Add plan model/ }).click();
    const back = main.getByRole("button", { name: "Back" });
    await expectLeftOf(back, main.getByRole("heading", { name: "#41 Add plan model" }));
    await back.click();
    await expect(main.getByRole("button", { name: /Ticket #41 Add plan model/ })).toBeVisible();
    await expect(back).toBeHidden();
  });

  test("has the name Back and no text", async ({ page }) => {
    const panel = await openPanelTranscript(page);
    const back = panel.getByRole("button", { name: "Back", exact: true });
    await expect(back).toHaveText("");
    await expect(back.locator("svg")).toBeVisible();
  });

  for (const { page: subpage, title, parent, heading } of [
    {
      page: "/settings/checkup/tools",
      title: "Tools",
      parent: "/settings/checkup",
      heading: "Checkup",
    },
    {
      page: "/settings/memory/plants/garden",
      title: "Memory of plants/garden",
      parent: "/settings/memory",
      heading: "Memory",
    },
  ]) {
    test(`of ${title} is on the left of the title and opens its parent`, async ({ page }) => {
      await page.goto(subpage);
      const main = page.getByRole("main");
      const back = main.getByRole("button", { name: "Back" });
      await expectLeftOf(back, main.getByRole("heading", { name: title, level: 2 }));
      await back.click();
      await expect(page).toHaveURL(parent);
      await expect(main.getByRole("heading", { name: heading })).toBeVisible();
    });
  }

  for (const path of [
    "/github",
    "/devices",
    "/settings/checkup",
    "/settings/memory",
    "/agents",
    "/usage",
  ]) {
    test(`is not on ${path}`, async ({ page }) => {
      await page.goto(path);
      await expect(page.getByRole("main").getByRole("heading").first()).toBeVisible();
      await expect(page.getByRole("main").getByRole("button", { name: "Back" })).toBeHidden();
    });
  }

  test("of the transcript in the side panel shows the start button under the title row and a long title", async ({
    page,
  }) => {
    await withQueueReason(page, "waits for a low load", "low_load", async () => {
      const panel = await openPanelTranscript(page, /waits for a low load/);
      const title = panel.locator(".line-clamp-2");
      await expect(title).toContainText("#41 Add plan model");
      const titleBox = await boxOf(title);
      expect(titleBox.width).toBeGreaterThan(100);
      const startBox = await boxOf(panel.getByRole("button", { name: "Start the check now" }));
      expect(startBox.y).toBeGreaterThanOrEqual(titleBox.y + titleBox.height);
    });
  });
});

// The log of a long session has the rows 1 to total.rows. A page has 100 rows, as the server gives.
async function mockPages(page: Page, total: { rows: number }, requests: (number | undefined)[]) {
  await mockLog(page);
  await page.route(/\/api\/agents\/\d+\/transcript(\?.*)?$/, (route) => {
    const before = new URL(route.request().url()).searchParams.get("before");
    requests.push(before ? Number(before) : undefined);
    const last = before ? Number(before) - 1 : total.rows;
    const first = Math.max(1, last - 99);
    return route.fulfill({
      json: {
        data: {
          lines: Array.from({ length: last - first + 1 }, (_, n) => entry(first + n)),
          older: first > 1,
        },
      },
    });
  });
}

const openAgentLog = async (page: Page) => {
  await page.goto("/agents");
  await page
    .getByRole("main")
    .getByRole("button", { name: /Ticket #41 Add plan model/ })
    .click();
};

const lineIds = (page: Page) =>
  page
    .locator("li[data-line]")
    .evaluateAll((items) => items.map((item) => Number(item.getAttribute("data-line"))));

test("the agent log of a long session shows the newest rows and loads the older rows at the top", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1280, height: 720 });
  const requests: (number | undefined)[] = [];
  await mockPages(page, { rows: 250 }, requests);
  await openAgentLog(page);
  await expect(page.getByText("Entry 250 ")).toBeVisible();
  await expect(page.getByText("Entry 151 ")).toHaveCount(1);
  await expect(page.getByText("Entry 150 ")).toHaveCount(0);
  await expect.poll(() => requests).toEqual([undefined, undefined]);

  await page.evaluate(
    `new Promise((resolve) => {
      const list = document.querySelector('li[data-line]').parentElement
      list.addEventListener('scroll', () => requestAnimationFrame(resolve), { once: true })
      list.scrollTop = 0
    })`,
  );
  const first = page.locator('li[data-line="151"]');
  const listTop = (await page.locator("li[data-line]").first().locator("xpath=..").boundingBox())!
    .y;
  await expect(page.getByText("Entry 150 ")).toHaveCount(1);
  expect(requests).toEqual([undefined, undefined, 151]);
  expect(Math.abs((await first.boundingBox())!.y - listTop)).toBeLessThan(20);
  await expect(page.locator("li[data-line]")).toHaveCount(200);
  expect(await lineIds(page)).toHaveLength(200);
});

test("the agent log loads pages after a reconnect until it has the newest known row, and shows no row two times", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1280, height: 720 });
  const requests: (number | undefined)[] = [];
  const total = { rows: 250 };
  await mockPages(page, total, requests);
  await openAgentLog(page);
  await expect(page.getByText("Entry 250 ")).toBeVisible();
  await expect.poll(() => requests).toEqual([undefined, undefined]);

  total.rows = 350;
  await page.evaluate(`window.sources.at(-1).dispatchEvent(new Event('open'))`);

  await expect(page.getByText("Entry 350 ")).toBeVisible();
  expect(requests).toEqual([undefined, undefined, undefined, 251]);
  await expect(page.locator("li[data-line]")).toHaveCount(200);
  const ids = await lineIds(page);
  expect(new Set(ids).size).toBe(200);
  expect(ids.at(0)).toBe(151);
  expect(ids.at(-1)).toBe(350);
});

test("a live event with a longer raw text changes the raw text of a row with a body of 300 characters", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1280, height: 720 });
  await mockLog(page);
  const body = "b".repeat(300);
  const row = (raw: string) => ({ ...entry(5), body, raw });
  const before = `{"text":"${body}"}`;
  const after = `{"text":"${body} and more text"}`;
  await page.route(/\/api\/agents\/\d+\/transcript(\?.*)?$/, (route) =>
    route.fulfill({ json: { data: { lines: [row(before)], older: false } } }),
  );
  await openAgentLog(page);
  const item = page.locator('li[data-line="5"]');
  await item.getByRole("button", { name: "Raw" }).click();
  await expect(item.locator("pre").last()).toHaveText(before);

  await page.evaluate(
    `window.sources.at(-1).dispatchEvent(new MessageEvent('transcript', { data: ${JSON.stringify(
      JSON.stringify(row(after)),
    )} }))`,
  );

  await expect(item.locator("pre").last()).toHaveText(after);
});
