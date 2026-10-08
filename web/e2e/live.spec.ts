import { expect, test, type Route } from "@playwright/test";

const viewports = {
  desktop: { width: 1280, height: 800 },
  phone: { width: 390, height: 844 },
};

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

  await page.clock.fastForward(36_000);
  await expect.poll(() => requests).toBe(2);
});

for (const [device, size] of Object.entries(viewports)) {
  test(`the page shows the state of the live connection on the ${device}`, async ({
    page,
    context,
  }) => {
    await page.setViewportSize(size);
    await page.clock.install();
    let hold = false;
    const held: Route[] = [];
    await page.route("/api/events", async (route) => {
      if (hold) {
        held.push(route);
        return;
      }
      await route.fallback();
    });
    const status = page.getByRole("status");
    const opened = page.waitForResponse("/api/events");
    await page.goto("/workstreams");
    await opened;
    await expect(page.getByRole("main")).toBeVisible();
    await expect(status).toBeEmpty();

    await context.setOffline(true);
    await expect(status).toHaveText("Offline");
    await context.setOffline(false);
    await expect(status).toBeEmpty();

    hold = true;
    await page.clock.fastForward(36_000);
    await expect(status).toHaveText("Connecting…");
    await page.screenshot({ path: `screenshots/connecting-${device}.png`, animations: "disabled" });
    await expect.poll(() => held.length).toBe(1);
    await Promise.all(held.map((route) => route.fallback()));
    await expect(status).toBeEmpty();
  });
}
