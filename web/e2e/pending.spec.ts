import { expect, test, type Locator, type Page } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.goto("/github");
  await page.getByLabel("Access password").fill("correct horse");
  await page.getByRole("button", { name: "Log in" }).click();
  await expect(page.getByLabel("Access password")).toBeHidden();
});

// Holds each request that matches until release. Then it answers with an error, so the request never reaches the
// server and the data stays.
async function hold(page: Page, url: string, method: string) {
  let requests = 0;
  let release!: () => void;
  const released = new Promise<void>((resolve) => (release = resolve));
  await page.route(url, async (route) => {
    if (route.request().method() !== method) {
      await route.fallback();
      return;
    }
    requests++;
    await released;
    await route.fulfill({ status: 500, json: { error: "The server failed." } });
  });
  return { requests: () => requests, release };
}

const spinner = (button: Locator) => button.locator('[data-slot="spinner"]');

// The button is disabled and shows the spinner while the request runs. A second tap sends no second request. After
// the error, the button is active again.
async function checkPending(button: Locator, held: Awaited<ReturnType<typeof hold>>) {
  await button.click();
  await expect(button).toBeDisabled();
  await expect(spinner(button)).toBeVisible();
  await button.click({ force: true });
  expect(held.requests()).toBe(1);
  held.release();
  await expect(button).toBeEnabled();
  await expect(spinner(button)).toHaveCount(0);
}

test("Resume in the Lead chat shows the spinner while the request runs", async ({ page }) => {
  await page.goto("/api/github/user-callback?code=user-code");
  await page.goto("/workstreams/plants/garden/20");
  const main = page.getByRole("main");
  const held = await hold(page, "**/api/repositories/*/*/issues/*/resume", "POST");
  const resume = main.getByRole("button", { name: "Resume" });
  await expect(resume).toHaveCount(2);

  await checkPending(resume.first(), held);
  await expect(resume.nth(1)).toBeEnabled();
  await expect(main.getByText("The server failed.")).toBeVisible();
});

test("Resume now in the Inbox shows the spinner while the request runs", async ({ page }) => {
  await page.goto("/inbox");
  const row = page.getByRole("listitem").filter({ hasText: "antigravity reached a usage limit." });
  const held = await hold(page, "**/api/inbox/*/resume", "POST");

  await checkPending(row.getByRole("button", { name: "Resume now" }), held);
  await expect(page.getByRole("main").getByText("The server failed.")).toBeVisible();
});

test("a tap on Resume now or Dismiss disables both buttons of the row", async ({ page }) => {
  await page.goto("/inbox");
  const row = page.getByRole("listitem").filter({ hasText: "antigravity reached a usage limit." });
  const resume = row.getByRole("button", { name: "Resume now" });
  const dismiss = row.getByRole("button", { name: "Dismiss" });
  const other = page
    .getByRole("listitem")
    .filter({ hasText: "#45 needs a decision" })
    .getByRole("button", { name: "Dismiss" });
  const held = await hold(page, "**/api/inbox/*/dismiss", "POST");

  await dismiss.click();
  await expect(dismiss).toBeDisabled();
  await expect(resume).toBeDisabled();
  await expect(spinner(dismiss)).toBeVisible();
  await expect(spinner(resume)).toHaveCount(0);
  await expect(other).toBeEnabled();
  held.release();
  await expect(dismiss).toBeEnabled();
  await expect(resume).toBeEnabled();
});

test("Start in the Tasks list shows the spinner in place of the icon", async ({ page }) => {
  await page.goto("/api/github/user-callback?code=user-code");
  await page.goto("/workstreams/plants/garden/19");
  await page.getByRole("tab", { name: "Tasks" }).filter({ visible: true }).click();
  const held = await hold(page, "**/api/repositories/*/*/issues/*/start", "POST");
  const start = page.getByRole("button", { name: "Start #70" });

  await checkPending(start, held);
  await expect(start.locator("svg")).toHaveCount(1);
});

test("Stop in the chat shows the spinner while the request runs", async ({ page }) => {
  await page.route("**/api/chat?*", async (route) => {
    const response = await route.fetch();
    const body = (await response.json()) as { data: { writing: boolean } };
    body.data.writing = true;
    await route.fulfill({ response, json: body });
  });
  await page.goto("/workstreams/owner/shop/12");
  const held = await hold(page, "**/api/chat/stop", "POST");

  await checkPending(
    page.getByRole("main").getByRole("button", { name: "Stop the reply", exact: true }),
    held,
  );
  await expect(page.getByRole("main").getByText("The server failed.")).toBeVisible();
});

test("Log out in Devices shows the spinner while the request runs", async ({ page }) => {
  await page.goto("/devices");
  const held = await hold(page, "**/api/devices/*", "DELETE");

  await checkPending(page.getByRole("button", { name: "Log out" }).first(), held);
  await expect(page.getByRole("main").getByText("The server failed.")).toBeVisible();
});

test("Cancel upgrade shows the spinner while the request runs", async ({ page }) => {
  await page.route("**/api/drain", (route) =>
    route.request().method() === "GET"
      ? route.fulfill({ json: { data: { on: true, waiting: 2 } } })
      : route.fallback(),
  );
  await page.goto("/workstreams");
  const held = await hold(page, "**/api/drain", "DELETE");

  await checkPending(
    page.getByRole("button", { name: "Cancel upgrade" }).filter({ visible: true }),
    held,
  );
  await expect(page.getByText("The server failed.").filter({ visible: true })).toBeVisible();
});
