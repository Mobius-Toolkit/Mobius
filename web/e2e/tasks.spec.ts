import { expect, test } from "@playwright/test";
import { boardTasks, mergingTask, refusedTask, routeTasks, tasksRoute } from "./tasks.js";

test.beforeEach(async ({ page }) => {
  await page.goto("/github");
  await page.getByLabel("Access password").fill("correct horse");
  await page.getByRole("button", { name: "Log in" }).click();
  await expect(page.getByLabel("Access password")).toBeHidden();
});

const columns = [
  { title: "Queued", count: 3, states: ["dispatched", "queued", "answered"] },
  { title: "Working", count: 2, states: ["working", "reviewed"] },
  { title: "Waiting", count: 2, states: ["checks", "approval"] },
  { title: "Needs you", count: 3, states: ["needs_human", "asked", "stopped"] },
  { title: "Ready to merge", count: 1, states: ["ready_for_review"] },
  { title: "Done", count: 2, states: ["ended"] },
];

test("each state goes to its column, and each header shows the count", async ({ page }) => {
  await routeTasks(page, boardTasks);
  await page.goto("/tasks");

  const headings = page.getByRole("main").getByRole("heading", { level: 3 });
  await expect(headings).toHaveText(columns.map(({ title, count }) => `${title}${count}`));
  for (const { title, count, states } of columns) {
    const column = page.getByRole("region", { name: title });
    await expect(column.getByRole("listitem")).toHaveCount(count);
    for (const state of states) {
      const badge = column.getByText(state, { exact: true });
      await expect(badge).toHaveCount(
        boardTasks.filter((task) => task.state === state && task.repository === "owner/shop")
          .length,
      );
    }
  }
  await expect(page.getByText("Water the lilies")).toHaveCount(0);
});

test("an empty column stays with the count 0", async ({ page }) => {
  await routeTasks(
    page,
    boardTasks.filter((task) => task.state !== "ended"),
  );
  await page.goto("/tasks");

  const done = page.getByRole("region", { name: "Done" });
  await expect(done.getByRole("heading")).toHaveText("Done0");
  await expect(done.getByText("No tasks")).toBeVisible();
});

test("the oldest task is first, and in Done the newest task is first", async ({ page }) => {
  await routeTasks(page, boardTasks);
  await page.goto("/tasks");

  await expect(page.getByRole("region", { name: "Queued" }).getByRole("listitem")).toContainText([
    "Show the plan limit on the account page",
    "Store the plan prices in cents",
    "Add a retry to the order sync",
  ]);
  await expect(page.getByRole("region", { name: "Done" }).getByRole("listitem")).toContainText([
    "Fix the plan name in the mail",
    "Round the prices to cents",
  ]);
});

test("a card shows the repository, the Workstream, the PR and the rounds", async ({ page }) => {
  await routeTasks(page, boardTasks);
  await page.goto("/tasks");

  const working = page.getByRole("listitem").filter({ hasText: "Let customers change plans" });
  await expect(working.filter({ hasText: "shop#54 · Integrate loyalty plans" })).toHaveCount(1);
  await expect(working.getByText("fix 1 · review 2")).toBeVisible();
  const approval = page.getByRole("listitem").filter({ hasText: "Replace the old plan table" });
  await expect(approval.getByText("review 1", { exact: true })).toBeVisible();
  await expect(approval.getByText("fix", { exact: false })).toHaveCount(0);
  const queued = page.getByRole("listitem").filter({ hasText: "Add a retry to the order sync" });
  await expect(queued.getByText(/fix|review|PR #/)).toHaveCount(0);
});

test("a click on a card opens its Workstream, and the PR number links to GitHub", async ({
  page,
}) => {
  await routeTasks(page, boardTasks);
  await page.goto("/tasks");

  const card = page.getByRole("listitem").filter({ hasText: "Let customers change plans" });
  const pull = card.getByRole("link", { name: "PR #44" });
  await expect(pull).toHaveAttribute("href", "https://github.com/owner/shop/pull/44");
  await expect(pull).toHaveAttribute("target", "_blank");

  await card.click({ position: { x: 4, y: 4 } });
  await expect(page).toHaveURL("/workstreams/owner/shop/12");
});

test("the Workstream of a card opens in the organization of the card", async ({ page }) => {
  await page.addInitScript("localStorage.setItem('organization', 'plants')");
  await routeTasks(page, boardTasks);
  await page.goto("/tasks");

  await expect(page.getByText("Water the lilies")).toBeVisible();
  await expect(page.getByText("Add a retry to the order sync")).toHaveCount(0);
  await expect(page.getByRole("region", { name: "Queued" }).getByRole("heading")).toHaveText(
    "Queued1",
  );
});

test("the tab bar of a phone and the sidebar of a desktop have the Tasks link", async ({
  page,
}) => {
  await routeTasks(page, boardTasks);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/tasks");
  const tabs = page.getByRole("navigation").last().getByRole("link");
  await expect(tabs).toHaveText([/^Chat/, /^Workstreams$/, /^Tasks$/, /^Inbox/, /^Settings/]);
  await expect(tabs.nth(2)).toHaveAttribute("aria-current", "page");

  await page.setViewportSize({ width: 1280, height: 800 });
  const links = page.getByRole("navigation").first().getByRole("link");
  await expect(links.nth(1)).toHaveText(/^Inbox/);
  await expect(links.nth(2)).toHaveText(/^Tasks$/);
});

test("the board shows the new state after a workstreams event, with no reload", async ({
  page,
}) => {
  await page.addInitScript(`
    window.sources = [];
    const add = EventSource.prototype.addEventListener;
    EventSource.prototype.addEventListener = function (type, listener, options) {
      if (this.url.endsWith('/api/events') && !window.sources.includes(this)) {
        window.sources.push(this);
      }
      return add.call(this, type, listener, options);
    };
  `);
  let tasks = boardTasks;
  await page.route(tasksRoute, (route) => route.fulfill({ json: { data: tasks } }));
  await page.goto("/tasks");

  const queued = page.getByRole("region", { name: "Queued" });
  const working = page.getByRole("region", { name: "Working" });
  await expect(queued.getByText("Add a retry to the order sync")).toBeVisible();
  await expect(working.getByText("Add a retry to the order sync")).toHaveCount(0);

  tasks = boardTasks.map((task) => (task.number === 51 ? { ...task, state: "working" } : task));
  await page.evaluate(
    `window.sources.at(-1).dispatchEvent(new MessageEvent('workstreams', { data: '{}' }))`,
  );
  await expect(working.getByText("Add a retry to the order sync")).toBeVisible();
  await expect(queued.getByText("Add a retry to the order sync")).toHaveCount(0);
  await expect(queued.getByRole("heading")).toHaveText("Queued2");
  await expect(working.getByRole("heading")).toHaveText("Working3");
});

test("on a phone the columns scroll horizontally and the page does not", async ({ page }) => {
  await routeTasks(page, boardTasks);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/tasks");

  const queued = page.getByRole("region", { name: "Queued" });
  await expect(queued).toBeVisible();
  const row = queued.locator("..");
  expect(await row.evaluate((el) => el.scrollWidth > el.clientWidth)).toBe(true);
  expect(await page.locator("#content").evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(
    true,
  );
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true,
  );

  const first = await queued.boundingBox();
  const next = await page.getByRole("region", { name: "Working" }).boundingBox();
  expect(first?.width).toBeGreaterThan(390 * 0.8);
  expect(next?.x).toBeLessThan(390);
});

test("on a desktop the six columns fit the page", async ({ page }) => {
  await routeTasks(page, boardTasks);
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.goto("/tasks");

  const queued = page.getByRole("region", { name: "Queued" });
  await expect(queued).toBeVisible();
  const row = queued.locator("..");
  expect(await row.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true);
  await expect(page.getByRole("region", { name: "Done" })).toBeInViewport();
});

test("a task in the merge is in Waiting with two badges, and a refused task is in Ready to merge with its reason", async ({
  page,
}) => {
  await routeTasks(page, [...boardTasks, mergingTask, refusedTask]);
  await page.goto("/tasks");

  const headings = page.getByRole("main").getByRole("heading", { level: 3 });
  await expect(headings).toHaveText([
    "Queued3",
    "Working2",
    "Waiting3",
    "Needs you3",
    "Ready to merge2",
    "Done2",
  ]);
  const waiting = page.getByRole("region", { name: "Waiting" });
  const merging = waiting.getByRole("listitem").filter({ hasText: "Show the renewal date" });
  await expect(merging.getByText("ready_for_review", { exact: true })).toBeVisible();
  await expect(merging.getByText("merging", { exact: true })).toBeVisible();
  const ready = page.getByRole("region", { name: "Ready to merge" });
  await expect(ready.getByText("Show the renewal date")).toHaveCount(0);
  await expect(ready.getByText("merging", { exact: true })).toHaveCount(0);
  const refused = ready.getByRole("listitem").filter({ hasText: "Add the coupon field" });
  await expect(refused.getByText("Required status check is expected.")).toBeVisible();
  await expect(waiting.getByText("Required status check is expected.")).toHaveCount(0);
});

test("the board moves a task to Waiting after an inbox event, with no reload", async ({ page }) => {
  await page.addInitScript(`
    window.sources = [];
    const add = EventSource.prototype.addEventListener;
    EventSource.prototype.addEventListener = function (type, listener, options) {
      if (this.url.endsWith('/api/events') && !window.sources.includes(this)) {
        window.sources.push(this);
      }
      return add.call(this, type, listener, options);
    };
  `);
  let tasks = [...boardTasks, { ...mergingTask, merging: false }];
  await page.route(tasksRoute, (route) => route.fulfill({ json: { data: tasks } }));
  await page.goto("/tasks");

  const ready = page.getByRole("region", { name: "Ready to merge" });
  const waiting = page.getByRole("region", { name: "Waiting" });
  await expect(ready.getByText("Show the renewal date")).toBeVisible();
  await expect(waiting.getByText("Show the renewal date")).toHaveCount(0);

  tasks = [...boardTasks, mergingTask];
  await page.evaluate(
    `window.sources.at(-1).dispatchEvent(new MessageEvent('inbox', { data: '{}' }))`,
  );
  await expect(waiting.getByText("Show the renewal date")).toBeVisible();
  await expect(ready.getByText("Show the renewal date")).toHaveCount(0);
  await expect(waiting.getByRole("heading")).toHaveText("Waiting3");
  await expect(ready.getByRole("heading")).toHaveText("Ready to merge1");
});
