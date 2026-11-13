import { expect, test, type Page } from "@playwright/test";

const phone = { width: 390, height: 844 };

test.beforeEach(async ({ page }) => {
  await page.goto("/github");
  await page.getByLabel("Access password").fill("correct horse");
  await page.getByRole("button", { name: "Log in" }).click();
  await expect(page.getByLabel("Access password")).toBeHidden();
});

const api = "/api/workstreams/plants/garden";

const query = (text: string) =>
  `cron=${encodeURIComponent("45 6 * * *")}&text=${encodeURIComponent(text)}`;

const call = (page: Page, path: string, init: { method: string; body?: string }) =>
  page.evaluate(
    async (input) => {
      const response = await fetch(input.path, {
        ...input.init,
        headers: { "Content-Type": "application/json" },
      });
      return {
        status: response.status,
        body: response.status === 200 ? await response.json() : null,
      };
    },
    { path, init },
  );

const taskRequest = (page: Page, method: string, path: RegExp) =>
  page.waitForResponse(
    (response) => path.test(response.url()) && response.request().method() === method,
  );

test("the Recurring tab lists the tasks with icon controls, and pauses and resumes one", async ({
  page,
}) => {
  await page.goto("/workstreams/plants/garden/50");
  await page.getByRole("tab", { name: "Recurring" }).filter({ visible: true }).click();
  const panel = page.getByRole("tabpanel", { name: "Recurring" });
  const rows = panel.getByRole("listitem");
  const active = rows.filter({ hasText: "Check the open pull requests" });
  const paused = rows.filter({ hasText: "Weekly cost report" });

  const tabs = page.getByRole("complementary").getByRole("tablist");
  await expect(tabs.getByRole("tab")).toHaveCount(5);
  expect(await tabs.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true);
  const [list, side] = await Promise.all([
    tabs.boundingBox(),
    page.getByRole("complementary").boundingBox(),
  ]);
  expect(list!.x + list!.width).toBeLessThanOrEqual(side!.x + side!.width);

  await expect(rows).toHaveCount(2);
  await expect(active).toContainText("0 8 * * 1-5 · Next Sep 29, 08:00 AM");
  await expect(active).toContainText("Last: Sep 28, 12:00 PM manual");
  await expect(paused).toContainText("Paused");
  await expect(paused).toContainText("0 9 * * 1");
  await expect(paused).not.toContainText("Next");
  for (const row of [active, paused]) {
    await expect(row.getByRole("button", { name: "Run now" })).toHaveAttribute("title", "Run now");
    await expect(row.getByRole("button", { name: "Delete" })).toHaveAttribute("title", "Delete");
    await expect(row.getByRole("button", { name: "Run now" })).toHaveText("");
    await expect(row.getByRole("button", { name: "Delete" })).toHaveText("");
  }
  await expect(active.getByRole("button", { name: "Pause" })).toHaveText("");
  await expect(paused.getByRole("button", { name: "Resume" })).toHaveText("");

  let response = taskRequest(page, "PUT", /\/recurring-tasks\/\d+\/paused$/);
  await active.getByRole("button", { name: "Pause" }).click();
  await response;
  await expect(active).toContainText("Paused");
  await expect(active.getByRole("button", { name: "Resume" })).toBeVisible();
  await expect(active).not.toContainText("Next");

  response = taskRequest(page, "PUT", /\/recurring-tasks\/\d+\/paused$/);
  await active.getByRole("button", { name: "Resume" }).click();
  await response;
  await expect(active).not.toContainText("Paused");
  await expect(active).toContainText("0 8 * * 1-5 · Next");
  await expect(active.getByRole("button", { name: "Pause" })).toBeVisible();
});

test("a tap on a row opens the task on a subpage, which pauses, resumes and goes back to the list", async ({
  page,
}) => {
  await page.goto("/workstreams/plants/garden/50");
  await page.getByRole("tab", { name: "Recurring" }).filter({ visible: true }).click();
  const panel = page.getByRole("tabpanel", { name: "Recurring" });
  const rows = panel.getByRole("listitem");
  const subpage = panel.getByRole("heading", { name: "Recurring task" });
  await expect(rows).toHaveCount(2);

  await rows
    .filter({ hasText: "Check the open pull requests" })
    .getByRole("button")
    .first()
    .click();
  await expect(subpage).toBeVisible();
  await expect(rows.filter({ hasText: "Weekly cost report" })).toHaveCount(0);
  await expect(
    panel.getByText("Check the open pull requests and tell me which ones wait for a review."),
  ).toBeVisible();
  await expect(panel.getByRole("heading", { name: "History" })).toBeVisible();
  await expect(panel.getByText("Sep 28, 12:00 PM")).toBeVisible();
  await expect(panel.getByText("manual", { exact: true })).toBeVisible();
  await expect(panel.getByText("skipped: the earlier event is not delivered yet")).toBeVisible();
  await expect(panel.getByText("Sep 24, 08:00 AM")).toBeVisible();

  let response = taskRequest(page, "PUT", /\/recurring-tasks\/\d+\/paused$/);
  await panel.getByRole("button", { name: "Pause" }).click();
  await response;
  await expect(panel.getByText("Paused", { exact: true })).toBeVisible();
  response = taskRequest(page, "PUT", /\/recurring-tasks\/\d+\/paused$/);
  await panel.getByRole("button", { name: "Resume" }).click();
  await response;
  await expect(panel.getByText("Paused", { exact: true })).toBeHidden();
  await expect(panel.getByRole("button", { name: "Pause" })).toBeVisible();

  await panel.getByRole("button", { name: "Back" }).click();
  await expect(subpage).toBeHidden();
  await expect(rows).toHaveCount(2);

  await rows.filter({ hasText: "Weekly cost report" }).getByRole("button").first().click();
  await expect(panel.getByText("No run yet")).toBeVisible();
  await panel.getByRole("button", { name: "Back" }).click();
  await expect(rows).toHaveCount(2);
});

test("the subpage starts at the top, and the list keeps its scroll position after it", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 360 });
  await page.goto("/workstreams/plants/garden/50");
  await page.getByRole("banner").getByRole("button", { name: "Tasks", exact: true }).click();
  await page.getByRole("tab", { name: "Recurring" }).filter({ visible: true }).click();
  const panel = page.getByRole("tabpanel", { name: "Recurring" });
  const rows = panel.getByRole("listitem");
  await expect(rows).toHaveCount(2);
  const top = 10;
  await panel.evaluate((el, scrollTop) => {
    el.scrollTop = scrollTop;
  }, top);
  expect(await panel.evaluate((el) => el.scrollTop)).toBe(top);

  await rows
    .filter({ hasText: "Check the open pull requests" })
    .getByRole("button")
    .first()
    .click();
  await expect(panel.getByRole("heading", { name: "Recurring task" })).toBeVisible();
  // The subpage is taller than the offset, so the browser does not clamp it.
  expect(await panel.evaluate((el) => el.scrollHeight - el.clientHeight)).toBeGreaterThan(top);
  await expect.poll(() => panel.evaluate((el) => el.scrollTop)).toBe(0);
  await expect(panel.getByRole("button", { name: "Back" })).toBeInViewport();
  await panel.getByRole("button", { name: "Back" }).click();
  await expect(rows).toHaveCount(2);
  await expect.poll(() => panel.evaluate((el) => el.scrollTop)).toBe(top);
});

test("the list and the subpage have no horizontal scroll on the phone", async ({ page }) => {
  await page.setViewportSize(phone);
  await page.goto("/workstreams/plants/garden/50");
  await page.getByRole("banner").getByRole("button", { name: "Tasks", exact: true }).click();
  await page.getByRole("tab", { name: "Recurring" }).filter({ visible: true }).click();
  const panel = page.getByRole("tabpanel", { name: "Recurring" });
  const rows = panel.getByRole("listitem");
  const sheet = page.getByRole("dialog");
  const fits = () =>
    Promise.all(
      [panel, sheet].map((box) => box.evaluate((el) => el.scrollWidth <= el.clientWidth)),
    );
  await expect(rows).toHaveCount(2);
  expect(await fits()).toEqual([true, true]);

  await rows
    .filter({ hasText: "Check the open pull requests" })
    .getByRole("button")
    .first()
    .click();
  await expect(panel.getByRole("heading", { name: "Recurring task" })).toBeVisible();
  expect(await fits()).toEqual([true, true]);
});

test("Run now gives the Lead one event, adds a manual run, and keeps a paused task paused", async ({
  page,
}) => {
  for (const text of ["Count the new tulips", "Rake the old leaves"]) {
    const added = await page.request.post(`/e2e/recurring-tasks/plants/garden/30?${query(text)}`);
    expect(added.ok()).toBe(true);
  }
  const tasks = await call(page, `${api}/30/recurring-tasks`, { method: "GET" });
  const [first, second] = tasks.body.data.tasks as { id: number; text: string }[];
  const paused = await call(page, `${api}/30/recurring-tasks/${second.id}/paused`, {
    method: "PUT",
    body: JSON.stringify({ paused: true }),
  });
  expect(paused.status).toBe(200);

  await page.goto("/workstreams/plants/garden/30");
  await page.getByRole("tab", { name: "Recurring" }).filter({ visible: true }).click();
  const panel = page.getByRole("tabpanel", { name: "Recurring" });
  const rows = panel.getByRole("listitem");
  const notice = page.getByRole("status");
  await expect(rows).toHaveCount(2);

  await rows.filter({ hasText: second.text }).getByRole("button", { name: "Run now" }).click();
  await expect(notice.filter({ hasText: "The Lead got the event." })).toBeVisible();
  await expect(rows.filter({ hasText: second.text })).toContainText("manual");
  await expect(rows.filter({ hasText: second.text })).toContainText("Paused");
  await expect(
    rows.filter({ hasText: second.text }).getByRole("button", { name: "Resume" }),
  ).toBeVisible();

  await rows.filter({ hasText: first.text }).getByRole("button").first().click();
  await expect(panel.getByRole("heading", { name: "Recurring task" })).toBeVisible();
  await expect(panel.getByText("No run yet")).toBeVisible();
  let run = taskRequest(page, "POST", /\/recurring-tasks\/\d+\/runs$/);
  await panel.getByRole("button", { name: "Run now" }).click();
  expect((await run).status()).toBe(200);
  await expect(panel.getByText("manual", { exact: true })).toBeVisible();
  await expect(panel.getByText("No run yet")).toBeHidden();

  run = taskRequest(page, "POST", /\/recurring-tasks\/\d+\/runs$/);
  await panel.getByRole("button", { name: "Run now" }).click();
  expect((await run).status()).toBe(409);
  await expect(
    notice.filter({ hasText: "The earlier event of this recurring task is not delivered yet." }),
  ).toBeVisible();
  await expect(panel.getByText("manual", { exact: true })).toHaveCount(1);

  for (const task of [first, second]) {
    const deleted = await call(page, `${api}/30/recurring-tasks/${task.id}`, { method: "DELETE" });
    expect(deleted.status).toBe(204);
  }
});

test("Delete asks first, and the confirmation removes the task", async ({ page }) => {
  const added = await page.request.post(
    `/e2e/recurring-tasks/plants/garden/55?cron=${encodeURIComponent("30 7 * * *")}&text=${encodeURIComponent("Count the new asters")}`,
  );
  expect(added.ok()).toBe(true);
  await page.goto("/workstreams/plants/garden/55");
  await page.getByRole("tab", { name: "Recurring" }).filter({ visible: true }).click();
  const panel = page.getByRole("tabpanel", { name: "Recurring" });
  const dialog = page.getByRole("dialog", { name: "Delete the recurring task" });
  await expect(panel.getByRole("listitem")).toHaveCount(1);

  await panel.getByRole("button", { name: "Delete" }).click();
  await expect(dialog).toBeVisible();
  await expect(dialog.getByText("Count the new asters")).toBeVisible();
  await dialog.getByRole("button", { name: "Cancel" }).click();
  await expect(dialog).toBeHidden();
  await expect(panel.getByRole("listitem")).toHaveCount(1);

  await panel.getByRole("button", { name: "Delete" }).click();
  await expect(dialog).toBeVisible();
  const deleted = page.waitForResponse(
    (response) =>
      /\/recurring-tasks\/\d+$/.test(response.url()) && response.request().method() === "DELETE",
  );
  await dialog.getByRole("button", { name: "Delete", exact: true }).click();
  expect((await deleted).status()).toBe(204);
  await expect(dialog).toBeHidden();
  await expect(panel.getByRole("listitem")).toHaveCount(0);
  await expect(
    panel.getByText("No recurring task. Ask the Lead in the chat to add one."),
  ).toBeVisible();
});

test("the five tab buttons fit the width of the sheet on the phone", async ({ page }) => {
  await page.setViewportSize(phone);
  await page.goto("/workstreams/plants/garden/50");
  await page.getByRole("banner").getByRole("button", { name: "Tasks", exact: true }).click();
  const sheet = page.getByRole("dialog");
  const tabs = sheet.getByRole("tablist");
  await expect(tabs.getByRole("tab")).toHaveText([
    "Tasks",
    "Agents",
    "Details",
    "Brief",
    "Recurring",
  ]);
  await expect
    .poll(async () => {
      const [list, outer] = await Promise.all([tabs.boundingBox(), sheet.boundingBox()]);
      return (
        list !== null &&
        outer !== null &&
        list.x >= outer.x &&
        list.x + list.width <= outer.x + outer.width
      );
    })
    .toBe(true);
  expect(await tabs.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true);
  expect(await sheet.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true);
});
