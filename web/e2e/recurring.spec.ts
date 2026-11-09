import { expect, test, type Page } from "@playwright/test";

const phone = { width: 390, height: 844 };

test.beforeEach(async ({ page }) => {
  await page.goto("/github");
  await page.getByLabel("Access password").fill("correct horse");
  await page.getByRole("button", { name: "Log in" }).click();
  await expect(page.getByLabel("Access password")).toBeHidden();
});

const pausedRequest = (page: Page) =>
  page.waitForResponse(
    (response) =>
      /\/recurring-tasks\/\d+\/paused$/.test(response.url()) &&
      response.request().method() === "PUT",
  );

test("the Recurring tab lists the tasks, pauses and resumes one, and shows the history", async ({
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
  await expect(active).toContainText("Last: Sep 28, 08:00 AM event");
  await expect(active.getByRole("button", { name: "Pause" })).toBeVisible();
  await expect(paused).toContainText("Paused");
  await expect(paused).toContainText("0 9 * * 1");
  await expect(paused).not.toContainText("Next");
  await expect(paused.getByRole("button", { name: "Resume" })).toBeVisible();

  let response = pausedRequest(page);
  await active.getByRole("button", { name: "Pause" }).click();
  await response;
  await expect(active).toContainText("Paused");
  await expect(active.getByRole("button", { name: "Resume" })).toBeVisible();
  await expect(active).not.toContainText("Next");

  response = pausedRequest(page);
  await active.getByRole("button", { name: "Resume" }).click();
  await response;
  await expect(active).not.toContainText("Paused");
  await expect(active).toContainText("0 8 * * 1-5 · Next");
  await expect(active.getByRole("button", { name: "Pause" })).toBeVisible();

  const history = active.getByRole("heading", { name: "History" });
  await expect(history).toBeHidden();
  await active.getByRole("button", { name: /Check the open pull requests/ }).click();
  await expect(history).toBeVisible();
  await expect(active.getByText("Sep 28, 08:00 AM")).toHaveCount(2);
  await expect(active.getByText("skipped: the earlier event is not delivered yet")).toBeVisible();
  await expect(active.getByText("Sep 24, 08:00 AM")).toBeVisible();
  await active.getByRole("button", { name: /Check the open pull requests/ }).click();
  await expect(history).toBeHidden();

  await paused.getByRole("button", { name: /Weekly cost report/ }).click();
  await expect(paused.getByText("No run yet")).toBeVisible();
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
