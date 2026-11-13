import { expect, test } from "@playwright/test";
import {
  boardTasks,
  inboxItem,
  mergeQueueHeadTask,
  mergeQueueTask,
  mergeQueueThirdTask,
  mergingTask,
  ownerActionTask,
  refusedTask,
  routeTasks,
  tasksRoute,
} from "./tasks.js";

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

test("a task in owner_action is in Needs you with its state as the badge", async ({ page }) => {
  await routeTasks(page, [...boardTasks, ownerActionTask]);
  await page.goto("/tasks");

  const column = page.getByRole("region", { name: "Needs you" });
  await expect(column.getByRole("heading")).toHaveText("Needs you4");
  const card = column.getByRole("listitem").filter({ hasText: "Deploy the plan page" });
  await expect(card.getByText("owner_action", { exact: true })).toBeVisible();
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

test("the Tasks card and the Inbox card show 'Merge queue · 2 of 4' for a task at place 2 of 4, and no badge for a task that is not in the queue", async ({
  page,
}) => {
  const queuedTask = {
    ...boardTasks[0],
    title: "Merge queue task",
    number: 91,
    mergeQueuePlace: 2,
    mergeQueueSize: 4,
  };
  const unqueuedTask = {
    ...boardTasks[1],
    title: "Unqueued task",
    number: 92,
    mergeQueuePlace: null,
    mergeQueueSize: 0,
  };
  await routeTasks(page, [queuedTask, unqueuedTask]);
  await page.goto("/tasks");

  const queuedCard = page.getByRole("listitem").filter({ hasText: "Merge queue task" });
  await expect(queuedCard.getByText("Merge queue · 2 of 4")).toBeVisible();

  const unqueuedCard = page.getByRole("listitem").filter({ hasText: "Unqueued task" });
  await expect(unqueuedCard.getByText("Merge queue")).toHaveCount(0);

  const queuedInboxItem = {
    ...inboxItem,
    id: 11,
    kind: "ready for review",
    title: "Merge queue task",
    mergeQueuePlace: 2,
    mergeQueueSize: 4,
  };
  const unqueuedInboxItem = {
    ...inboxItem,
    id: 12,
    kind: "ready for review",
    title: "Unqueued task",
    mergeQueuePlace: null,
    mergeQueueSize: 0,
  };
  await page.route("/api/inbox", (route) =>
    route.fulfill({ json: { data: [queuedInboxItem, unqueuedInboxItem] } }),
  );
  await page.goto("/inbox");

  const queuedInboxRow = page.getByRole("listitem").filter({ hasText: "Merge queue task" });
  await expect(queuedInboxRow.getByText("Merge queue · 2 of 4")).toBeVisible();

  const unqueuedInboxRow = page.getByRole("listitem").filter({ hasText: "Unqueued task" });
  await expect(unqueuedInboxRow.getByText("Merge queue")).toHaveCount(0);
});

test("Waiting and Ready to merge show the queued tasks first, in the order of the queue, and then the tasks with no place", async ({
  page,
}) => {
  await routeTasks(page, [...boardTasks, mergeQueueHeadTask, mergeQueueThirdTask, mergeQueueTask]);
  await page.goto("/tasks");

  const waiting = page.getByRole("region", { name: "Waiting" }).getByRole("listitem");
  await expect(waiting).toContainText([
    "Show the plan limit on the invoice list",
    "Replace the old plan table",
    "Add the invoice export",
  ]);
  await expect(waiting.first().getByText("Merges next · 1 of 4")).toBeVisible();
  await expect(waiting.getByText("Merge queue")).toHaveCount(0);

  const ready = page.getByRole("region", { name: "Ready to merge" }).getByRole("listitem");
  await expect(ready).toContainText([
    "Show the plan limit on the invoice",
    "Show the plan limit on the receipt",
    "Remind the customer about the renewal",
  ]);
  await expect(ready.nth(0).getByText("Merge queue · 2 of 4")).toBeVisible();
  await expect(ready.nth(1).getByText("Merge queue · 3 of 4")).toBeVisible();
  await expect(ready.nth(2).getByText(/Merge/)).toHaveCount(0);
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

test("a task in the merge is in Ready to merge with the badge approved, and a refused task keeps its reason", async ({
  page,
}) => {
  await routeTasks(page, [...boardTasks, mergingTask, refusedTask]);
  await page.goto("/tasks");

  const headings = page.getByRole("main").getByRole("heading", { level: 3 });
  await expect(headings).toHaveText([
    "Queued3",
    "Working2",
    "Waiting2",
    "Needs you3",
    "Ready to merge3",
    "Done2",
  ]);
  const waiting = page.getByRole("region", { name: "Waiting" });
  const ready = page.getByRole("region", { name: "Ready to merge" });
  await expect(waiting.getByText("Show the renewal date")).toHaveCount(0);
  const merging = ready.getByRole("listitem").filter({ hasText: "Show the renewal date" });
  await expect(merging.getByText("approved", { exact: true })).toBeVisible();
  await expect(merging.getByText("ready_for_review", { exact: true })).toHaveCount(0);
  await expect(merging.getByText("merging", { exact: true })).toHaveCount(0);
  const refused = ready.getByRole("listitem").filter({ hasText: "Add the coupon field" });
  await expect(refused.getByText("Required status check is expected.")).toBeVisible();
  await expect(waiting.getByText("Required status check is expected.")).toHaveCount(0);
});

test("the board changes the badge of a task to approved after an inbox event, with no reload", async ({
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
  let tasks = [...boardTasks, { ...mergingTask, merging: false }];
  await page.route(tasksRoute, (route) => route.fulfill({ json: { data: tasks } }));
  await page.goto("/tasks");

  const ready = page.getByRole("region", { name: "Ready to merge" });
  const waiting = page.getByRole("region", { name: "Waiting" });
  const renewal = ready.getByRole("listitem").filter({ hasText: "Show the renewal date" });
  await expect(renewal.getByText("ready_for_review", { exact: true })).toBeVisible();
  await expect(renewal.getByText("approved", { exact: true })).toHaveCount(0);
  await expect(waiting.getByText("Show the renewal date")).toHaveCount(0);

  tasks = [...boardTasks, mergingTask];
  await page.evaluate(
    `window.sources.at(-1).dispatchEvent(new MessageEvent('inbox', { data: ${JSON.stringify(
      JSON.stringify(inboxItem),
    )} }))`,
  );
  await expect(renewal.getByText("approved", { exact: true })).toBeVisible();
  await expect(renewal.getByText("ready_for_review", { exact: true })).toHaveCount(0);
  await expect(waiting.getByText("Show the renewal date")).toHaveCount(0);
  await expect(waiting.getByRole("heading")).toHaveText("Waiting2");
  await expect(ready.getByRole("heading")).toHaveText("Ready to merge2");
});
