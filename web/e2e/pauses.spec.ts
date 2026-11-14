import { expect, test, type Page } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.goto("/github");
  await page.getByLabel("Access password").fill("correct horse");
  await page.getByRole("button", { name: "Log in" }).click();
  await expect(page.getByLabel("Access password")).toBeHidden();
});

const banner = (page: Page, harness: string) => page.locator(`[data-pause="${harness}"]`);

const users = (page: Page, harness: string) => banner(page, harness).getByRole("listitem");

const workstreamUser = "Implementer setting of plants/garden#20 Plant tulips";
const taskUser = "Implementer setting of plants/garden#21 Dig the tulip beds";

test("a global screen shows the pause that a Role uses with its users, and not the pause of a Workstream setting", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.goto("/tasks");

  await expect(banner(page, "antigravity")).toContainText("Used by:");
  await expect(users(page, "antigravity")).toHaveText([
    "Researcher role",
    "Curator role",
    /^Implementer session of owner\/shop#45 /,
  ]);
  await expect(banner(page, "dsh")).toHaveCount(0);
});

test("the Lead chat of a Workstream shows the pause of its settings and the pause of a Role with the users of that Workstream", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.goto("/workstreams/plants/garden/20");

  await expect(banner(page, "dsh")).toContainText("dsh paused until Sep 29, 08:30");
  await expect(users(page, "dsh")).toHaveText([
    workstreamUser,
    taskUser,
    "Implementer setting of plants/garden#22 Buy tulip bulbs",
  ]);
  await expect(users(page, "antigravity")).toHaveText(["Researcher role", "Curator role"]);

  await page.goto("/workstreams/plants/garden/12");

  await expect(users(page, "antigravity")).toHaveText(["Researcher role", "Curator role"]);
  await expect(banner(page, "dsh")).toHaveCount(0);
});

test("a tap on a setting user opens the setting sheet of the Workstream and of the task on a desktop", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.goto("/workstreams/plants/garden/20");

  await banner(page, "dsh").getByRole("link", { name: workstreamUser }).click();

  await expect(page).toHaveURL(/\?tab=details&setting=implementer$/);
  await expect(page.getByRole("tab", { name: "Details", selected: true })).toBeVisible();
  const workstreamSheet = page.getByRole("dialog", { name: "Implementer of the Workstream" });
  await expect(workstreamSheet).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(workstreamSheet).toBeHidden();

  await banner(page, "dsh").getByRole("link", { name: taskUser }).click();

  await expect(page).toHaveURL(/\?tab=tasks&setting=implementer&task=plants\/garden\/21$/);
  await expect(page.getByRole("tab", { name: "Tasks", selected: true })).toBeVisible();
  await expect(page.getByRole("dialog", { name: "Implementer of #21" })).toBeVisible();
});

test("a phone shows two users and Show 1 more, and a tap on a setting user opens the setting sheet", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/workstreams/plants/garden/20");

  const more = banner(page, "dsh").getByRole("button", { name: "Show 1 more" });
  await expect(more).toBeVisible();
  await expect(users(page, "dsh").filter({ visible: true })).toHaveCount(2);

  await more.click();

  await expect(users(page, "dsh").filter({ visible: true })).toHaveCount(3);
  await expect(more).toBeHidden();

  await banner(page, "dsh").getByRole("link", { name: taskUser }).click();

  await expect(page.getByRole("dialog", { name: "Implementer of #21" })).toBeVisible();
});
