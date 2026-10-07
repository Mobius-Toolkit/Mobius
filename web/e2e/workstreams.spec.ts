import { expect, test } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.goto("/github");
  await page.getByLabel("Access password").fill("correct horse");
  await page.getByRole("button", { name: "Log in" }).click();
  await expect(page.getByLabel("Access password")).toBeHidden();
});

test("a Workstream with Autopilot on shows the Autopilot icon", async ({ page }) => {
  const main = page.getByRole("main");
  const row = (title: string) => main.getByRole("link").filter({ hasText: title });

  await page.goto("/workstreams");
  await expect(row("Early renewals").getByRole("img", { name: "Autopilot" })).toBeVisible();
  await expect(row("Early renewals")).toContainText("#14");
  await expect(row("Seasonal prices")).toContainText("#13");
  await expect(row("Seasonal prices").getByRole("img", { name: "Autopilot" })).toHaveCount(0);
});
