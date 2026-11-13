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

test("a Save of the context of a claude-code role shows its short form, and Reset removes it", async ({
  page,
}) => {
  const sheet = await openRole(page, "Lead");
  await expect(sheet.getByLabel("Context", { exact: true })).toHaveValue("");
  await sheet.getByLabel("Context", { exact: true }).fill("200000");

  const saved = written(page, "lead", "PUT");
  await sheet.getByRole("button", { name: "Save" }).click();
  expect((await saved).ok()).toBe(true);
  await expect(sheet).toBeHidden();
  await expect(roleRow(page, "Lead")).toContainText("200k");
  await expect(roleRow(page, "Lead").getByText("UI", { exact: true })).toHaveCount(1);

  await roleRow(page, "Lead").click();
  const reset = written(page, "lead", "DELETE");
  await sheet.getByRole("button", { name: "Reset" }).click();
  expect((await reset).ok()).toBe(true);
  await expect(sheet).toBeHidden();
  await expect(roleRow(page, "Lead")).not.toContainText("200k");
});

test("a Harness with no context limit has a disabled Context field and a reason", async ({
  page,
}) => {
  const sheet = await openRole(page, "Implementer");
  await expect(sheet.getByLabel("Context", { exact: true })).toBeDisabled();
  await expect(sheet.getByText("This Harness has no context limit.")).toBeVisible();
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

  await pick(page, sheet, "Harness", "dsh");
  const refused = written(page, "reviewer", "PUT");
  await sheet.getByRole("button", { name: "Save" }).click();
  expect((await refused).status()).toBe(422);

  await expect(sheet.getByText("roles.reviewer.model: must not be empty")).toBeVisible();
  await expect(sheet.getByLabel("Harness", { exact: true })).toHaveText("dsh");
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

test("the sheet of the Operator shows its sentence as the accessible description", async ({
  page,
}) => {
  const sheet = await openRole(page, "Operator");
  await expect(sheet).toHaveAccessibleDescription(
    "Does work outside code for one Workstream, through the MCP servers of the Workstream, and reports to the Lead.",
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

const bindingsUrl = /\/api\/roles\/implementer\/bindings(\/\d+)?$/;

const widths = (locator: Locator) =>
  locator.evaluate((element) => ({ scroll: element.scrollWidth, client: element.clientWidth }));

const bindingWritten = (page: Page, method: "POST" | "PUT" | "DELETE") =>
  page.waitForResponse((res) => bindingsUrl.test(res.url()) && res.request().method() === method);

// Returns the id of the new binding, which is the last of moreBindings.
const addBindingByApi = async (page: Page, model: string, max: number) => {
  const { status, id } = await page.evaluate(
    async (binding) => {
      const response = await fetch("/api/roles/implementer/bindings", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(binding),
      });
      const json = await response.json();
      return { status: response.status, id: json.data.moreBindings.at(-1).id as number };
    },
    { harness: "claude-code", model, effort: "low", context: 0, max },
  );
  expect(status).toBe(200);
  return id;
};

const removeBindingByApi = async (page: Page, id: number) => {
  const status = await page.evaluate(
    (path) => fetch(path, { method: "DELETE" }).then((response) => response.status),
    `/api/roles/implementer/bindings/${id}`,
  );
  expect(status).toBe(200);
};

test("the Owner adds a binding with the plus tab, saves it, changes its max and removes it", async ({
  page,
}) => {
  const sheet = await openRole(page, "Implementer");
  const tabs = sheet.getByRole("tab");
  await expect(tabs).toHaveCount(2);
  await expect(sheet.getByRole("button", { name: "Remove" })).toBeHidden();
  const firstLabel = (await tabs.first().textContent()) ?? "";
  await expect(tabs.first()).toHaveAttribute("aria-selected", "true");

  await sheet.getByRole("tab", { name: "Add binding" }).click();
  await expect(tabs).toHaveCount(3);
  await expect(tabs.nth(1)).toHaveText(firstLabel);
  await expect(tabs.nth(1)).toHaveAttribute("aria-selected", "true");
  await sheet.getByRole("tab", { name: "Add binding" }).click();
  await expect(tabs).toHaveCount(3);

  await pick(page, sheet, "Harness", "claude-code");
  await pick(page, sheet, "Model", "haiku");
  await expect(tabs.nth(1)).toHaveText("claude-code · haiku");
  const added = bindingWritten(page, "POST");
  await sheet.getByRole("button", { name: "Save" }).click();
  expect((await added).ok()).toBe(true);
  await expect(tabs).toHaveCount(3);
  await expect(tabs.nth(1)).toHaveText("claude-code · haiku");
  await expect(tabs.nth(1)).toHaveAttribute("aria-selected", "true");
  const row = roleRow(page, "Implementer");
  await expect(row).toContainText("claude-code");
  await expect(row.getByText("haiku")).toBeVisible();

  await sheet.getByLabel("Max", { exact: true }).fill("3");
  const changed = bindingWritten(page, "PUT");
  await sheet.getByRole("button", { name: "Save" }).click();
  expect((await changed).ok()).toBe(true);
  await expect(row).toContainText("max 3");
  await expect(sheet.getByLabel("Max", { exact: true })).toHaveValue("3");
  await expect(sheet.getByRole("button", { name: "Reset" })).toBeHidden();

  const removed = bindingWritten(page, "DELETE");
  await sheet.getByRole("button", { name: "Remove" }).click();
  expect((await removed).ok()).toBe(true);
  await expect(tabs).toHaveCount(2);
  await expect(tabs.first()).toHaveAttribute("aria-selected", "true");
  await expect(row).not.toContainText("max 3");
  await expect(sheet.getByRole("button", { name: "Remove" })).toBeHidden();
});

test("a close of the sheet drops the new tab, and a Remove of the new tab sends no request", async ({
  page,
}) => {
  const sheet = await openRole(page, "Implementer");
  const requests: string[] = [];
  page.on("request", (request) => {
    if (bindingsUrl.test(request.url())) {
      requests.push(request.method());
    }
  });
  await sheet.getByRole("tab", { name: "Add binding" }).click();
  await expect(sheet.getByRole("tab")).toHaveCount(3);
  await sheet.getByRole("button", { name: "Remove" }).click();
  await expect(sheet.getByRole("tab")).toHaveCount(2);
  await expect(sheet.getByRole("tab").first()).toHaveAttribute("aria-selected", "true");

  await sheet.getByRole("tab", { name: "Add binding" }).click();
  await expect(sheet.getByRole("tab")).toHaveCount(3);
  await sheet.getByRole("button", { name: "Close" }).click();
  await expect(sheet).toBeHidden();
  await roleRow(page, "Implementer").click();
  await expect(sheet.getByRole("tab")).toHaveCount(2);
  expect(requests).toEqual([]);
});

test("a Remove of the first tab of two moves the second binding to the first tab, and Reset sets the data back", async ({
  page,
}) => {
  await page.goto("/settings/roles");
  await expect(roleRow(page, "Lead")).toContainText("opus");
  await addBindingByApi(page, "opus", 4);
  await page.reload();
  const sheet = await openRole(page, "Implementer");
  const tabs = sheet.getByRole("tab");
  await expect(tabs).toHaveCount(3);
  await expect(tabs.nth(1)).toHaveText("claude-code · opus");
  await expect(sheet.getByRole("button", { name: "Reset" })).toBeHidden();

  const removed = bindingWritten(page, "DELETE");
  await sheet.getByRole("button", { name: "Remove" }).click();
  const response = await removed;
  expect(response.ok()).toBe(true);
  expect(response.url()).toMatch(/\/bindings\/0$/);
  await expect(tabs).toHaveCount(2);
  await expect(tabs.first()).toHaveText("claude-code · opus");
  await expect(sheet.getByLabel("Max", { exact: true })).toHaveValue("4");
  await expect(sheet.getByRole("button", { name: "Remove" })).toBeHidden();

  const reset = written(page, "implementer", "DELETE");
  await sheet.getByRole("button", { name: "Reset" }).click();
  expect((await reset).ok()).toBe(true);
  await expect(sheet).toBeHidden();
  await expect(roleRow(page, "Implementer")).not.toContainText("claude-code");
});

test("on a phone the tab strip scrolls sideways and the sheet does not", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/settings/roles");
  await expect(roleRow(page, "Lead")).toContainText("opus");
  const ids = [await addBindingByApi(page, "sonnet", 1), await addBindingByApi(page, "haiku", 1)];
  try {
    await page.reload();
    const sheet = await openRole(page, "Implementer");
    await expect(sheet.getByRole("tab")).toHaveCount(4);
    const tabList = sheet.getByRole("tablist");
    const list = await widths(tabList);
    expect(list.scroll).toBeGreaterThan(list.client);
    const whole = await widths(sheet);
    expect(whole.scroll).toBeLessThanOrEqual(whole.client);
  } finally {
    for (const id of ids) {
      await removeBindingByApi(page, id);
    }
  }
});
