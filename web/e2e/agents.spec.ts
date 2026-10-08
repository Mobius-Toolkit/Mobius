import { expect, test } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.goto("/github");
  await page.getByLabel("Access password").fill("correct horse");
  await page.getByRole("button", { name: "Log in" }).click();
  await expect(page.getByLabel("Access password")).toBeHidden();
});

const states = [
  { reason: "runs .mobius/check", badge: "checks", dot: /bg-green-600/ },
  { reason: "waits for a check slot", badge: "waits for check", dot: /bg-amber-500/ },
  { reason: "paused until 2026-09-28 12:00 UTC", badge: "paused", dot: /bg-amber-500/ },
  { reason: "no free Implementer slot (2/2)", badge: "queued", dot: /bg-amber-500/ },
];

test("the Agents page shows the state of each agent with a queue reason", async ({ page }) => {
  const main = page.getByRole("main");
  await page.goto("/agents");
  for (const { reason, badge, dot } of states) {
    const entry = main.getByRole("button").filter({ hasText: reason });
    await expect(entry.getByText(badge, { exact: true })).toBeVisible();
    await expect(entry.locator("span.rounded-full")).toHaveClass(dot);
  }
  const running = main.getByRole("button", { name: /Ticket #41 Add plan model/ });
  await expect(running.locator("span.rounded-full")).toHaveClass(/bg-green-600/);
  await expect(running.getByText("queued")).toBeHidden();
});

test("the Agents tab of a Workstream shows the state of each agent with a queue reason", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.goto("/workstreams/owner/shop/12");
  const tree = page.getByRole("complementary");
  for (const { reason, badge, dot } of states) {
    const entry = tree.getByRole("button").filter({ hasText: reason });
    await expect(entry.getByText(badge, { exact: true })).toBeVisible();
    await expect(entry.locator("span.rounded-full")).toHaveClass(dot);
  }
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
    await expect(bar.getByRole("heading")).not.toHaveText("Agents");
    await expect(page.getByRole("main").getByRole("button", { name: "Agents" })).toBeHidden();
    await bar.getByRole("button", { name: "Back" }).click();
    await expect(page).toHaveURL("/agents");
    await expect(bar.getByRole("heading")).toHaveText("Agents");
  });
});
