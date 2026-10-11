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

test("Resume now in the status line shows the spinner while the request runs", async ({ page }) => {
  await page.goto("/inbox");
  const line = page.getByText(/antigravity paused until/).locator("..");
  const held = await hold(page, "**/api/pauses/*/resume", "POST");

  await checkPending(line.getByRole("button", { name: "Resume now" }), held);
  await expect(page.getByText("The server failed.")).toBeVisible();
});

test("the status line shows each pause, and Resume now ends the pause of its Harness", async ({
  page,
}) => {
  await page.route("**/api/pauses", (route) =>
    route.fulfill({
      json: {
        data: [
          { harness: "antigravity", pausedUntil: "2026-09-28T12:00:00Z" },
          { harness: "devin", pausedUntil: "2026-09-29T08:30:00Z" },
        ],
      },
    }),
  );
  const resumed: string[] = [];
  await page.route("**/api/pauses/*/resume", (route) => {
    resumed.push(new URL(route.request().url()).pathname);
    return route.fulfill({ status: 204 });
  });
  await page.goto("/workstreams");
  const antigravity = page.getByText(/^antigravity paused until Sep 28, 12:00\sPM$/);
  const devin = page.getByText(/^devin paused until Sep 29, 08:30\sAM$/);
  await expect(antigravity).toBeVisible();
  await expect(devin).toBeVisible();

  await devin.locator("..").getByRole("button", { name: "Resume now" }).click();

  await expect.poll(() => resumed).toEqual(["/api/pauses/devin/resume"]);
});

test("a tap on Dismiss disables the button of the row", async ({ page }) => {
  await page.goto("/inbox");
  const dismiss = page
    .getByRole("listitem")
    .filter({ hasText: "#45 needs a decision" })
    .getByRole("button", { name: "Dismiss" });
  const held = await hold(page, "**/api/inbox/*/dismiss", "POST");

  await dismiss.click();
  await expect(dismiss).toBeDisabled();
  await expect(spinner(dismiss)).toBeVisible();
  held.release();
  await expect(dismiss).toBeEnabled();
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
