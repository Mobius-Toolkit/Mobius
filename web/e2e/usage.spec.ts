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

test("the source filter of the tab Skills keeps only the Harness rows", async ({ page }) => {
  const main = page.getByRole("main");
  await page.goto("/usage?tab=skills");
  const table = main.getByRole("table");
  await expect(table.getByRole("rowheader", { name: "mobius-local-state" })).toBeVisible();
  await expect(table.getByRole("rowheader", { name: "write-issue" })).toBeVisible();

  await main.getByRole("button", { name: /^Source/ }).click();
  await page.getByRole("menuitemcheckbox", { name: "harness" }).click();
  await page.keyboard.press("Escape");

  await expect(page).toHaveURL(/source=/);
  await expect(table.getByRole("rowheader")).toHaveText(["commit", "frontend-design"]);
  await expect(table.getByRole("cell", { name: "Mobius" })).toHaveCount(0);

  await main.getByRole("button", { name: /^Source/ }).click();
  await page.getByRole("menuitemcheckbox", { name: "harness" }).click();
  await page.keyboard.press("Escape");

  await expect(page).not.toHaveURL(/source=/);
  await expect(table.getByRole("rowheader", { name: "write-issue" })).toBeVisible();
});

test("the tab Skills has no model filters and the other tabs have no source filter", async ({
  page,
}) => {
  const main = page.getByRole("main");
  await page.goto("/usage?tab=skills");
  await expect(main.getByRole("button", { name: /^Source/ })).toBeVisible();
  for (const name of ["Model", "Model version", "Effort"]) {
    await expect(main.getByRole("button", { name: new RegExp(`^${name}`) })).toHaveCount(0);
  }

  for (const path of ["/usage", "/usage?tab=tokens", "/usage?tab=times"]) {
    await page.goto(path);
    await expect(main.getByRole("button", { name: /^Model version/ })).toBeVisible();
    await expect(main.getByRole("button", { name: /^Source/ })).toHaveCount(0);
  }
});

test("the tab Skills shows no group that it does not offer", async ({ page }) => {
  const main = page.getByRole("main");
  await page.goto("/usage?tab=skills&group=model");
  const table = main.getByRole("table");
  await expect(table.getByRole("rowheader", { name: "mobius-local-state" })).toBeVisible();
  await expect(main.getByRole("button", { name: "Group by: None" })).toBeVisible();
  await expect(table.getByRole("columnheader")).toHaveText([
    "Skill",
    "Source",
    "Uses",
    "Had",
    "Used",
  ]);

  await main.getByRole("button", { name: /^Group by:/ }).click();
  await expect(page.getByRole("menuitemradio")).toHaveText([
    "None",
    "Harness",
    "Role",
    "Repository",
    "Source",
  ]);
});
