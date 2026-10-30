import { expect, test, type Locator, type Page } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.goto("/github");
  await page.getByLabel("Access password").fill("correct horse");
  await page.getByRole("button", { name: "Log in" }).click();
  await expect(page.getByLabel("Access password")).toBeHidden();
});

// The API request of the page carries the device login, but the request context of Playwright does not.
test.afterEach(async ({ page }) => {
  for (const role of ["judge", "reviewer", "lead"]) {
    const status = await page.evaluate(
      (path) => fetch(path, { method: "DELETE" }).then((response) => response.status),
      `/api/roles/${role}`,
    );
    expect(status).toBe(200);
  }
});

// An open sheet hides the page behind it from the accessibility tree.
const roleRow = (page: Page, role: string) =>
  page.getByRole("button", { name: new RegExp(`^${role}`), includeHidden: true });

const openRole = async (page: Page, role: string) => {
  await page.goto("/settings/roles");
  await expect(roleRow(page, "Lead")).toContainText("opus");
  await roleRow(page, role).click();
  return page.getByRole("dialog", { name: role });
};

const pick = async (page: Page, field: Locator, label: string, value: string) => {
  await field.getByLabel(label, { exact: true }).click();
  await page.getByRole("menuitemradio", { name: value, exact: true }).click();
};

const written = (page: Page, role: string, method: "PUT" | "DELETE") =>
  page.waitForResponse(
    (res) => res.url().endsWith(`/api/roles/${role}`) && res.request().method() === method,
  );

test("the Roles link of the Settings page opens the Roles page on a phone", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/settings");
  await page.getByRole("main").getByRole("link", { name: "Roles" }).click();
  await expect(page).toHaveURL("/settings/roles");
  await expect(roleRow(page, "Lead")).toBeVisible();
});

test("the side bar of the desktop links the Roles page", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.goto("/settings");
  await page.locator('nav a[href="/settings/roles"]').filter({ visible: true }).click();
  await expect(page).toHaveURL("/settings/roles");
  await expect(roleRow(page, "Lead")).toBeVisible();
});

test("a Save of the model of the judge shows the UI badge, and Reset shows the value of config.toml", async ({
  page,
}) => {
  const sheet = await openRole(page, "Judge");
  await expect(roleRow(page, "Judge")).toContainText("haiku");
  await expect(roleRow(page, "Judge").getByText("UI", { exact: true })).toHaveCount(0);

  await pick(page, sheet, "Model", "sonnet");
  const saved = written(page, "judge", "PUT");
  await sheet.getByRole("button", { name: "Save" }).click();
  expect((await saved).ok()).toBe(true);
  await expect(sheet).toBeHidden();
  await expect(roleRow(page, "Judge")).toContainText("sonnet");
  await expect(roleRow(page, "Judge").getByText("UI", { exact: true })).toHaveCount(1);

  await roleRow(page, "Judge").click();
  const reset = written(page, "judge", "DELETE");
  await sheet.getByRole("button", { name: "Reset" }).click();
  expect((await reset).ok()).toBe(true);
  await expect(sheet).toBeHidden();
  await expect(roleRow(page, "Judge")).toContainText("haiku");
  await expect(roleRow(page, "Judge").getByText("UI", { exact: true })).toHaveCount(0);
});

test("a pick of a Harness selects its first model, and the efforts follow the model", async ({
  page,
}) => {
  const sheet = await openRole(page, "Judge");

  await pick(page, sheet, "Harness", "antigravity");
  await expect(sheet.getByLabel("Model", { exact: true })).toHaveText("sonnet");
  await expect(sheet.getByLabel("Effort", { exact: true })).toBeHidden();

  await pick(page, sheet, "Harness", "claude-code");
  await pick(page, sheet, "Model", "haiku");
  await sheet.getByLabel("Effort", { exact: true }).click();
  await expect(page.getByRole("menuitemradio")).toHaveText(["low"]);
});

test("a pick of the checked Harness keeps the model and the effort", async ({ page }) => {
  const sheet = await openRole(page, "Judge");

  await pick(page, sheet, "Harness", "claude-code");
  await expect(sheet.getByLabel("Harness", { exact: true })).toHaveText("claude-code");
  await expect(sheet.getByLabel("Model", { exact: true })).toHaveText("haiku");
  await expect(sheet.getByLabel("Effort", { exact: true })).toHaveText("low");
});

test("a refused Save shows the reason in the open sheet and keeps the old values in the row", async ({
  page,
}) => {
  const sheet = await openRole(page, "Reviewer");

  await pick(page, sheet, "Harness", "devin");
  await pick(page, sheet, "Model", "swe-1.5");
  const refused = written(page, "reviewer", "PUT");
  await sheet.getByRole("button", { name: "Save" }).click();
  expect((await refused).status()).toBe(422);

  await expect(sheet.getByText("must not have the same harness and model")).toBeVisible();
  await expect(sheet.getByLabel("Harness", { exact: true })).toHaveText("devin");
  await expect(roleRow(page, "Reviewer")).toContainText("claude-code");
  await expect(roleRow(page, "Reviewer")).toContainText("opus");
  await expect(roleRow(page, "Reviewer")).toContainText("high");
});

test("the sheet of the Implementer shows its sentence as the accessible description", async ({
  page,
}) => {
  const sheet = await openRole(page, "Implementer");
  await expect(sheet).toHaveAccessibleDescription(
    "Writes the code of a task, and does the fix rounds, CI fix rounds and conflict rounds of its pull request.",
  );
});

test("Reset does not show for a role with no UI value", async ({ page }) => {
  const sheet = await openRole(page, "Lead");
  await expect(sheet.getByRole("button", { name: "Save" })).toBeVisible();
  await expect(sheet.getByRole("button", { name: "Reset" })).toBeHidden();
});

test("a Save with no model list keeps the effort of the role", async ({ page }) => {
  await page.route("**/api/harnesses", (route) => route.fulfill({ json: { data: [] } }));
  const sheet = await openRole(page, "Lead");
  await expect(sheet.getByLabel("Effort", { exact: true })).toHaveText("high");

  await sheet.getByLabel("Max", { exact: true }).fill("4");
  const saved = written(page, "lead", "PUT");
  await sheet.getByRole("button", { name: "Save" }).click();
  const response = await saved;
  expect(response.request().postDataJSON()).toMatchObject({ effort: "high" });
  expect(response.ok()).toBe(true);
});
