import { expect, test } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.clock.setFixedTime("2026-10-15T12:00:00Z");
  await page.goto("/github");
  await page.getByLabel("Access password").fill("correct horse");
  await page.getByRole("button", { name: "Log in" }).click();
  await expect(page.getByLabel("Access password")).toBeHidden();
});

test("the group menu offers each field and sets the list to one field", async ({ page }) => {
  const main = page.getByRole("main");
  await page.goto("/usage");
  await main.getByRole("button", { name: /^Group by:/ }).click();

  await expect(page.getByRole("menuitemradio")).toHaveText([
    "None",
    "Harness",
    "Model",
    "Model version",
    "Effort",
    "Role",
    "Repository",
  ]);

  await page.getByRole("menuitemradio", { name: "Model", exact: true }).click();

  await expect(page).toHaveURL(/group=%5B%22model%22%5D/);
  const table = main.getByRole("table");
  await expect(table.getByRole("columnheader", { name: "Model" })).toBeVisible();
  for (const group of ["opus", "sonnet", "swe-1.5"]) {
    await expect(table.getByRole("rowheader", { name: group, exact: true })).toBeVisible();
  }
  await expect(main.getByRole("button", { name: "Group by: Model" })).toBeVisible();
});

test("two group fields show their values in the field order", async ({ page }) => {
  const main = page.getByRole("main");
  await page.goto("/usage?group=%5B%22effort%22%2C%22model%22%5D");

  const table = main.getByRole("table");
  await expect(table.getByRole("columnheader", { name: "Model · Effort" })).toBeVisible();
  for (const group of ["opus · medium", "sonnet · high", "sonnet · low", "swe-1.5 · —"]) {
    await expect(table.getByRole("rowheader", { name: group, exact: true })).toBeVisible();
  }
  await expect(main.getByRole("button", { name: "Group by: Model · Effort" })).toBeVisible();
});

test("an old group key gives no group", async ({ page }) => {
  const main = page.getByRole("main");
  await page.goto("/usage?group=modelEffort");

  await expect(main.getByRole("button", { name: "Group by: None" })).toBeVisible();
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
  await page.goto("/usage?tab=skills&group=%5B%22model%22%5D");
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
