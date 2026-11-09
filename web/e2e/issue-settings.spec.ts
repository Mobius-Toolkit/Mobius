import { expect, test, type Locator, type Page } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.goto("/github");
  await page.getByLabel("Access password").fill("correct horse");
  await page.getByRole("button", { name: "Log in" }).click();
  await expect(page.getByLabel("Access password")).toBeHidden();
});

const workstream = "/api/workstreams/owner/shop/12";
const task = "/api/repositories/owner/shop/issues/39/implementer";

// The API request of the page carries the device login, but the request context of Playwright does not.
test.afterEach(async ({ page }) => {
  for (const path of [task, `${workstream}/lead`, `${workstream}/implementer`]) {
    const status = await page.evaluate(
      (url) => fetch(url, { method: "DELETE" }).then((response) => response.status),
      path,
    );
    expect(status).toBe(204);
  }
});

const openTab = async (page: Page, tab: "Tasks" | "Details") => {
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.goto("/workstreams/owner/shop/12");
  await page.getByRole("tab", { name: tab }).filter({ visible: true }).click();
};

const taskRow = (page: Page) =>
  page.getByRole("listitem").filter({ hasText: "#39 Send the plan receipts" });

const gear = (page: Page) => taskRow(page).getByRole("button", { name: "Implementer of #39" });

const line = (page: Page, term: "Lead" | "Implementer") =>
  page
    .getByRole("tabpanel", { name: "Details" })
    .locator("dd")
    .filter({ has: page.getByRole("button", { name: `Change ${term}` }) });

const pick = async (page: Page, sheet: Locator, label: string, value: string) => {
  await sheet.getByLabel(label, { exact: true }).click();
  await page.getByRole("menuitemradio", { name: value, exact: true }).click();
};

const written = (page: Page, method: "PUT" | "DELETE", path: string) =>
  page.waitForResponse((res) => res.url().endsWith(path) && res.request().method() === method);

const save = async (page: Page, sheet: Locator, path: string) => {
  const saved = written(page, "PUT", path);
  await sheet.getByRole("button", { name: "Save" }).click();
  expect((await saved).status()).toBe(204);
  await expect(sheet).toBeHidden();
};

const useDefault = async (page: Page, sheet: Locator, path: string) => {
  const cleared = written(page, "DELETE", path);
  await sheet.getByRole("button", { name: "Use the default" }).click();
  expect((await cleared).status()).toBe(204);
  await expect(sheet).toBeHidden();
};

test("a Save in the sheet of a task shows its setting on the row, and Use the default clears it", async ({
  page,
}) => {
  await openTab(page, "Tasks");
  await expect(taskRow(page)).toBeVisible();
  await expect(taskRow(page).getByText("claude-code")).toHaveCount(0);

  await gear(page).click();
  const sheet = page.getByRole("dialog", { name: "Implementer of #39" });
  await expect(sheet).toContainText("Default: devin · swe-1.5 · high (role)");
  await expect(sheet.getByRole("button", { name: "Use the default" })).toHaveCount(0);
  await pick(page, sheet, "Harness", "claude-code");
  await pick(page, sheet, "Model", "sonnet");
  await pick(page, sheet, "Effort", "medium");
  await save(page, sheet, task);
  await expect(taskRow(page).getByText("claude-code · sonnet · medium")).toBeVisible();
  await expect(
    page
      .getByRole("status")
      .filter({ hasText: "Set the Implementer of #39 to claude-code · sonnet · medium" }),
  ).toBeVisible();

  await page.reload();
  await page.getByRole("tab", { name: "Tasks" }).filter({ visible: true }).click();
  await expect(taskRow(page).getByText("claude-code · sonnet · medium")).toBeVisible();

  await gear(page).click();
  await expect(sheet.getByLabel("Model", { exact: true })).toHaveText("sonnet");
  await useDefault(page, sheet, task);
  await expect(taskRow(page).getByText("claude-code")).toHaveCount(0);
});

test("a refused value shows the reason in the sheet, and the task keeps no setting", async ({
  page,
}) => {
  await openTab(page, "Tasks");
  await gear(page).click();
  const sheet = page.getByRole("dialog", { name: "Implementer of #39" });
  await pick(page, sheet, "Harness", "dsh");
  const saved = written(page, "PUT", task);
  await sheet.getByRole("button", { name: "Save" }).click();
  expect((await saved).status()).toBe(422);

  await expect(sheet).toContainText("roles.implementer.model: must not be empty");
  await page.keyboard.press("Escape");
  await expect(sheet).toBeHidden();
  await expect(taskRow(page).getByText("dsh")).toHaveCount(0);
});

test("a Save of the Lead shows Workstream on the Details tab, and Use the default shows role", async ({
  page,
}) => {
  await openTab(page, "Details");
  await expect(line(page, "Lead")).toContainText("claude-code · opus · high");
  await expect(line(page, "Lead")).toContainText("role");

  await line(page, "Lead").getByRole("button", { name: "Change Lead" }).click();
  const sheet = page.getByRole("dialog", { name: "Lead" });
  await expect(sheet).toContainText("Default: claude-code · opus · high (role)");
  await pick(page, sheet, "Model", "sonnet");
  await pick(page, sheet, "Effort", "low");
  await save(page, sheet, `${workstream}/lead`);
  await expect(line(page, "Lead")).toContainText("claude-code · sonnet · low");
  await expect(line(page, "Lead")).toContainText("Workstream");
  await expect(line(page, "Implementer")).toContainText("role");

  await line(page, "Lead").getByRole("button", { name: "Change Lead" }).click();
  await useDefault(page, sheet, `${workstream}/lead`);
  await expect(line(page, "Lead")).toContainText("claude-code · opus · high");
  await expect(line(page, "Lead")).toContainText("role");
});

test("the Implementer setting of the Workstream is the default in the sheet of a task, and Use the default clears it", async ({
  page,
}) => {
  await openTab(page, "Details");
  await expect(line(page, "Implementer")).toContainText("devin · swe-1.5 · high");
  await expect(line(page, "Implementer")).toContainText("role");

  await line(page, "Implementer").getByRole("button", { name: "Change Implementer" }).click();
  const sheet = page.getByRole("dialog", { name: "Implementer of the Workstream" });
  await expect(sheet).toContainText("Default: devin · swe-1.5 · high (role)");
  await pick(page, sheet, "Harness", "claude-code");
  await pick(page, sheet, "Model", "sonnet");
  await pick(page, sheet, "Effort", "low");
  await save(page, sheet, `${workstream}/implementer`);
  await expect(line(page, "Implementer")).toContainText("claude-code · sonnet · low");
  await expect(line(page, "Implementer")).toContainText("Workstream");

  await page.getByRole("tab", { name: "Tasks" }).filter({ visible: true }).click();
  await expect(taskRow(page).getByText("claude-code")).toHaveCount(0);
  await gear(page).click();
  const taskSheet = page.getByRole("dialog", { name: "Implementer of #39" });
  await expect(taskSheet).toContainText("Default: claude-code · sonnet · low (Workstream)");
  await page.keyboard.press("Escape");
  await expect(taskSheet).toBeHidden();

  await page.getByRole("tab", { name: "Details" }).filter({ visible: true }).click();
  await line(page, "Implementer").getByRole("button", { name: "Change Implementer" }).click();
  await useDefault(page, sheet, `${workstream}/implementer`);
  await expect(line(page, "Implementer")).toContainText("devin · swe-1.5 · high");
  await expect(line(page, "Implementer")).toContainText("role");

  await page.getByRole("tab", { name: "Tasks" }).filter({ visible: true }).click();
  await gear(page).click();
  await expect(taskSheet).toContainText("Default: devin · swe-1.5 · high (role)");
});

test("on a phone, Escape closes the setting sheet and the close dialog, and keeps the Tasks sheet", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/workstreams/owner/shop/12");
  await page.getByRole("banner").getByRole("button", { name: "Tasks", exact: true }).click();
  const tasks = page.getByRole("dialog", { name: "Tasks" });
  await expect(taskRow(page)).toBeVisible();

  await gear(page).click();
  const sheet = page.getByRole("dialog", { name: "Implementer of #39" });
  await expect(sheet).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(sheet).toBeHidden();
  await expect(tasks).toBeVisible();

  await tasks.getByRole("tab", { name: "Details" }).click();
  await tasks
    .getByRole("tabpanel", { name: "Details" })
    .getByRole("button", { name: "Close" })
    .click();
  const closing = page.getByRole("dialog", { name: "Close the Workstream" });
  await expect(closing).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(closing).toBeHidden();
  await expect(tasks).toBeVisible();

  await page.keyboard.press("Escape");
  await expect(tasks).toBeHidden();
});
