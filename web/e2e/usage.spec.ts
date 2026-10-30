import { expect, test } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.clock.setFixedTime("2026-10-15T12:00:00Z");
  await page.goto("/github");
  await page.getByLabel("Access password").fill("correct horse");
  await page.getByRole("button", { name: "Log in" }).click();
  await expect(page.getByLabel("Access password")).toBeHidden();
});

test("the desktop group menu selects several fields in the field order", async ({ page }) => {
  const main = page.getByRole("main");
  await page.goto("/usage");
  await main.getByRole("button", { name: /^Group by:/ }).click();

  await expect(page.getByRole("menuitemcheckbox")).toHaveText([
    "Harness",
    "Model",
    "Model version",
    "Effort",
    "Role",
    "Repository",
  ]);

  await page.getByRole("menuitemcheckbox", { name: "Harness", exact: true }).click();
  await page.getByRole("menuitemcheckbox", { name: "Role", exact: true }).click();
  await page.keyboard.press("Escape");

  await expect(page).toHaveURL(/group=%5B%22harness%22%2C%22role%22%5D/);
  await expect(main.getByRole("button", { name: "Group by: Harness · Role" })).toBeVisible();
  await expect(
    main.locator(".recharts-legend-wrapper").getByText("claude-code · lead_chat", { exact: true }),
  ).toBeVisible();

  await main.getByRole("button", { name: "Group by: Harness · Role" }).click();
  await page.getByRole("menuitemcheckbox", { name: "Harness", exact: true }).click();
  await page.getByRole("menuitemcheckbox", { name: "Role", exact: true }).click();
  await page.keyboard.press("Escape");

  await expect(page).not.toHaveURL(/group=/);
  await expect(main.getByRole("button", { name: "Group by: None" })).toBeVisible();
});

test("the phone group sheet selects several fields", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  const main = page.getByRole("main");
  await page.goto("/usage");
  await main.getByRole("button", { name: "Group by: None" }).click();

  const sheet = page.getByRole("dialog", { name: "Group by" });
  await expect(sheet.getByRole("checkbox")).toHaveCount(6);
  await sheet.getByRole("checkbox", { name: "Harness" }).click();
  await sheet.getByRole("checkbox", { name: "Role" }).click();
  await page.keyboard.press("Escape");

  await expect(page).toHaveURL(/group=%5B%22harness%22%2C%22role%22%5D/);
  await expect(main.getByRole("button", { name: "Group by: Harness · Role" })).toBeVisible();
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
  await expect(page.getByRole("menuitemcheckbox")).toHaveText([
    "Harness",
    "Role",
    "Repository",
    "Source",
  ]);
});
