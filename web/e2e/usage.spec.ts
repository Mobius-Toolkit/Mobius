import { expect, test } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.clock.setFixedTime("2026-10-15T12:00:00Z");
  await page.goto("/github");
  await page.getByLabel("Access password").fill("correct horse");
  await page.getByRole("button", { name: "Log in" }).click();
  await expect(page.getByLabel("Access password")).toBeHidden();
});

test("the group menu has the model and effort groups and no effort group", async ({ page }) => {
  const main = page.getByRole("main");
  await page.goto("/usage");
  await main.getByRole("button", { name: /^Group by:/ }).click();

  await expect(page.getByRole("menuitemradio")).toHaveText([
    "None",
    "Harness",
    "Model",
    "Model and effort",
    "Model version",
    "Model version and effort",
    "Role",
    "Repository",
  ]);

  await page.getByRole("menuitemradio", { name: "Model and effort", exact: true }).click();

  const table = main.getByRole("table");
  await expect(table.getByRole("columnheader", { name: "Model and effort" })).toBeVisible();
  for (const group of ["opus medium", "sonnet high", "sonnet low", "swe-1.5"]) {
    await expect(table.getByRole("rowheader", { name: group, exact: true })).toBeVisible();
  }
  await expect(main.getByRole("button", { name: /^Effort/ })).toBeVisible();
});
