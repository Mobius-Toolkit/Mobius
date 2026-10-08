import { expect, test } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.goto("/github");
  await page.getByLabel("Access password").fill("correct horse");
  await page.getByRole("button", { name: "Log in" }).click();
  await expect(page.getByLabel("Access password")).toBeHidden();
});

test("the page connects again after the live events answer is not an event stream", async ({
  page,
}) => {
  let requests = 0;
  await page.route("/api/events", (route) => {
    requests++;
    return requests === 1 ? route.fulfill({ status: 502 }) : route.fallback();
  });
  await page.reload();

  await expect.poll(() => requests, { timeout: 10_000 }).toBe(2);
  await expect(page.getByLabel("Access password")).toBeHidden();
});

test("the page does not connect again in a loop after a 401", async ({ page }) => {
  await page.clock.install();
  let events = 0;
  let apps = 0;
  await page.route("/api/events", (route) => {
    events++;
    return route.fulfill({ status: 502 });
  });
  await page.route("/api/github/apps", (route) => {
    apps++;
    return apps === 1
      ? route.fallback()
      : route.fulfill({ status: 401, json: { error: "login needed" } });
  });
  await page.reload();

  await expect(page.getByLabel("Access password")).toBeVisible({ timeout: 10_000 });
  expect(apps).toBe(2);
  for (let i = 0; i < 3; i++) {
    await page.clock.fastForward(60_000);
  }
  expect(events).toBe(1);
  expect(apps).toBe(2);
});

test("the page connects again when no ping comes", async ({ page }) => {
  await page.clock.install();
  let requests = 0;
  await page.route("/api/events", (route) => {
    requests++;
    return route.fallback();
  });
  const opened = page.waitForResponse("/api/events");
  await page.reload();
  await opened;

  await page.clock.fastForward(41_000);
  await expect.poll(() => requests).toBe(2);
});
