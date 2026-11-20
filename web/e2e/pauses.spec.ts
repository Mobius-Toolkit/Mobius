import { expect, test, type Page } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.goto("/github");
  await page.getByLabel("Access password").fill("correct horse");
  await page.getByRole("button", { name: "Log in" }).click();
  await expect(page.getByLabel("Access password")).toBeHidden();
});

const banner = (page: Page, harness: string) => page.locator(`[data-pause="${harness}"]`);

const toggle = (page: Page, harness: string) =>
  banner(page, harness).getByRole("button", { name: new RegExp(`^${harness} paused until`) });

const resumeButton = (page: Page, harness: string) =>
  banner(page, harness).getByRole("button", { name: "Resume now" });

const users = (page: Page, harness: string) => banner(page, harness).getByRole("listitem");

const workstreamUser = "Implementer setting of plants/garden#20 Plant tulips";
const taskUser = "Implementer setting of plants/garden#21 Dig the tulip beds";
const secondTaskUser = "Implementer setting of plants/garden#22 Buy tulip bulbs";

test("each pause shows as one closed line with no Used by text, opens on tap, closes on a second tap, and Resume now does not open it", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/workstreams/plants/garden/20");

  await expect(toggle(page, "antigravity")).toHaveAttribute("aria-expanded", "false");
  await expect(toggle(page, "dsh")).toHaveAttribute("aria-expanded", "false");
  await expect(banner(page, "antigravity")).not.toContainText("Used by:");
  await expect(banner(page, "dsh")).not.toContainText("Used by:");
  await expect(users(page, "dsh")).toHaveCount(0);
  await expect(page.getByRole("button", { name: /Show \d+ more/ })).toHaveCount(0);

  // On a phone, the row stays one line and cuts a long text with an ellipsis while keeping Resume now uncut.
  const lineCheck = await toggle(page, "antigravity").evaluate((button) => {
    const resume = button.nextElementSibling;
    if (!resume) {
      return null;
    }
    const buttonBox = button.getBoundingClientRect();
    const resumeBox = resume.getBoundingClientRect();
    const style = getComputedStyle(button);
    return {
      sameRow:
        Math.abs(buttonBox.top + buttonBox.height / 2 - (resumeBox.top + resumeBox.height / 2)) < 4,
      singleLine: buttonBox.height < 28,
      ellipsis: style.textOverflow === "ellipsis" && style.overflowX === "hidden",
      truncated: button.scrollWidth > button.clientWidth,
      resumeUncut: resume.scrollWidth <= resume.clientWidth,
      fitsViewport: document.documentElement.scrollWidth <= window.innerWidth,
    };
  });
  expect(lineCheck).toEqual({
    sameRow: true,
    singleLine: true,
    ellipsis: true,
    truncated: true,
    resumeUncut: true,
    fitsViewport: true,
  });

  const resumed: string[] = [];
  await page.route("**/api/pauses/*/resume", (route) => {
    resumed.push(new URL(route.request().url()).pathname);
    return route.fulfill({ status: 204 });
  });
  await resumeButton(page, "dsh").click();
  await expect.poll(() => resumed).toEqual(["/api/pauses/dsh/resume"]);
  await expect(toggle(page, "dsh")).toHaveAttribute("aria-expanded", "false");
  await expect(banner(page, "dsh")).not.toContainText("Used by:");

  await toggle(page, "dsh").click();
  await expect(toggle(page, "dsh")).toHaveAttribute("aria-expanded", "true");
  await expect(banner(page, "dsh")).toContainText("Used by:");
  await expect(users(page, "dsh")).toHaveText([workstreamUser, taskUser, secondTaskUser]);
  await expect(page.getByRole("button", { name: /Show \d+ more/ })).toHaveCount(0);

  await toggle(page, "dsh").click();
  await expect(toggle(page, "dsh")).toHaveAttribute("aria-expanded", "false");
  await expect(banner(page, "dsh")).not.toContainText("Used by:");
  await expect(users(page, "dsh")).toHaveCount(0);
});

test("a pause of Workstream or task settings shows on each page of its organization and not in another organization", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.goto("/tasks");

  await expect(toggle(page, "antigravity")).toBeVisible();
  await expect(banner(page, "dsh")).toHaveCount(0);

  await toggle(page, "antigravity").click();
  await expect(users(page, "antigravity")).toHaveText([
    "Researcher role",
    "Curator role",
    /^Implementer session of owner\/shop#45 /,
  ]);

  await page.getByRole("button", { name: "owner" }).filter({ visible: true }).click();
  await page.getByRole("menuitemradio", { name: "plants" }).click();
  await expect(toggle(page, "antigravity")).toBeVisible();
  await expect(toggle(page, "dsh")).toBeVisible();

  await page.locator('nav a[href="/chat"]').filter({ visible: true }).click();
  await expect(page).toHaveURL(/\/chat$/);
  await expect(toggle(page, "antigravity")).toBeVisible();
  await expect(toggle(page, "dsh")).toBeVisible();

  await page.goto("/workstreams/plants/garden/12");
  await expect(toggle(page, "antigravity")).toBeVisible();
  await expect(toggle(page, "dsh")).toBeVisible();

  await toggle(page, "antigravity").click();
  await expect(users(page, "antigravity")).toHaveText(["Researcher role", "Curator role"]);

  // #12 has no dsh setting, so the open list falls back to all users of dsh.
  await toggle(page, "dsh").click();
  await expect(users(page, "dsh")).toHaveText([workstreamUser, taskUser, secondTaskUser]);
});

test("a tap on a setting user opens the setting sheet of the Workstream and of the task", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.goto("/workstreams/plants/garden/20");

  await toggle(page, "dsh").click();
  await expect(users(page, "dsh")).toHaveText([workstreamUser, taskUser, secondTaskUser]);
  await banner(page, "dsh").getByRole("link", { name: workstreamUser }).click();

  await expect(page).toHaveURL(/\?tab=details&setting=implementer$/);
  await expect(page.getByRole("tab", { name: "Details", selected: true })).toBeVisible();
  const workstreamSheet = page.getByRole("dialog", { name: "Implementer of the Workstream" });
  await expect(workstreamSheet).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(workstreamSheet).toBeHidden();

  await toggle(page, "dsh").click();
  await banner(page, "dsh").getByRole("link", { name: taskUser }).click();

  await expect(page).toHaveURL(/\?tab=tasks&setting=implementer&task=plants\/garden\/21$/);
  await expect(page.getByRole("tab", { name: "Tasks", selected: true })).toBeVisible();
  await expect(page.getByRole("dialog", { name: "Implementer of #21" })).toBeVisible();
});

test("a move to another screen closes an open pause list", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/tasks");

  await toggle(page, "antigravity").click();
  await expect(toggle(page, "antigravity")).toHaveAttribute("aria-expanded", "true");
  await expect(users(page, "antigravity")).toHaveCount(3);

  await page.locator('nav a[href="/inbox"]').filter({ visible: true }).click();

  await expect(page).toHaveURL(/\/inbox$/);
  await expect(toggle(page, "antigravity")).toHaveAttribute("aria-expanded", "false");
  await expect(banner(page, "antigravity")).not.toContainText("Used by:");
});
