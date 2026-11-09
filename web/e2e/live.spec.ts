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

test("the page shows the UI again after the next load succeeds", async ({ page }) => {
  let failing = false;
  await page.route("/api/github/apps", (route) =>
    failing ? route.fulfill({ status: 500, json: { error: "apps failed" } }) : route.fallback(),
  );
  await page.goto("/workstreams/owner/shop/12");
  const input = page.getByLabel("Message to the Lead");
  await expect(input).toBeEditable();

  failing = true;
  await page.evaluate("window.dispatchEvent(new Event('online'))");
  await expect(page.getByText("apps failed")).toBeVisible();

  failing = false;
  await page.evaluate("window.dispatchEvent(new Event('online'))");
  await expect(page.getByText("apps failed")).toBeHidden();
  await expect(input).toBeEditable();
});

test("the error of one request of a load stays when the other request succeeds", async ({
  page,
}) => {
  let failing = false;
  await page.route("/api/github/apps", (route) =>
    failing ? route.fulfill({ status: 500, json: { error: "apps failed" } }) : route.fallback(),
  );
  await page.goto("/workstreams/owner/shop/12");
  await expect(page.getByLabel("Message to the Lead")).toBeEditable();

  failing = true;
  const organizations = page.waitForResponse("/api/organizations");
  await page.evaluate("window.dispatchEvent(new Event('online'))");
  expect((await organizations).status()).toBe(200);

  await expect(page.getByText("apps failed")).toBeVisible();
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
    await page.clock.fastForward(4_000);
    await expect(status).toBeEmpty();

    await context.setOffline(true);
    await expect(status).toHaveText("Offline");
    const reopened = page.waitForResponse("/api/events");
    await context.setOffline(false);
    await reopened;
    await expect(status).toBeEmpty();

    hold = true;
    await page.clock.fastForward(36_000);
    await expect(status).toHaveText("Connecting…");
    await expect.poll(() => held.length).toBe(1);
    await Promise.all(held.map((route) => route.fallback()));
    await expect(status).toBeEmpty();
  });
}

// The test adds two Owner messages to the Lead chat of plants/garden#12. The messages stay, and no later spec reads
// that chat.
test("the chat screen receives a live message after the live connection closes", async ({
  page,
  browser,
}) => {
  await page.goto("/workstreams/plants/garden/12");
  await expect(page.getByLabel("Message to the Lead")).toBeEditable();

  // An answer that is not an event stream closes the source for good, so the page must retry by itself.
  let refuse = true;
  await page.route("/api/events", (route) =>
    refuse ? route.fulfill({ status: 502 }) : route.fallback(),
  );
  await page.evaluate("window.dispatchEvent(new Event('online'))");

  const other = await browser.newPage();
  await other.goto("/github");
  await other.getByLabel("Access password").fill("correct horse");
  await other.getByRole("button", { name: "Log in" }).click();
  await expect(other.getByLabel("Access password")).toBeHidden();
  await other.goto("/workstreams/plants/garden/12");
  const send = async (text: string) => {
    await other.getByLabel("Message to the Lead").fill(text);
    await other.getByRole("button", { name: "Send" }).click();
    await expect(other.getByText(text)).toBeVisible();
  };
  await send("Water the roses in spring.");

  await expect(page.getByText("Water the roses in spring.")).toBeHidden();
  refuse = false;
  // The load after the new connection shows the first message.
  await expect(page.getByText("Water the roses in spring.")).toBeVisible({ timeout: 15_000 });

  // The new connection of the chat screen gets a live event with no reload of the page.
  const loads: string[] = [];
  await page.route("**/api/chat?*", (route) => {
    loads.push(route.request().url());
    return route.fallback();
  });
  await send("Mulch the beds in autumn.");
  await expect(page.getByText("Mulch the beds in autumn.")).toBeVisible();
  expect(loads).toHaveLength(0);
  await other.context().close();
});
