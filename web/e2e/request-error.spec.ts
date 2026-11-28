import { expect, test } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.goto("/github");
  await page.getByLabel("Access password").fill("correct horse");
  await page.getByRole("button", { name: "Log in" }).click();
  await expect(page.getByLabel("Access password")).toBeHidden();
});

test("the Workstreams page shows a clear error for an HTML 502 answer and removes it after a good answer", async ({
  page,
}) => {
  await page.addInitScript(`
    window.appSources = [];
    const add = EventSource.prototype.addEventListener;
    EventSource.prototype.addEventListener = function (type, listener, options) {
      if (this.url.endsWith('/api/events') && !window.appSources.includes(this)) {
        window.appSources.push(this);
      }
      return add.call(this, type, listener, options);
    };
  `);
  let unavailable = true;
  await page.route("**/api/workstreams", (route) =>
    unavailable
      ? route.fulfill({
          status: 502,
          contentType: "text/html",
          body: "<html><body>Bad Gateway</body></html>",
        })
      : route.continue(),
  );
  const main = page.getByRole("main");

  await page.goto("/workstreams");

  await expect(main.getByText("The server is not available (502).")).toBeVisible();
  await expect(main.getByText("SyntaxError")).toHaveCount(0);

  unavailable = false;
  const reloaded = page.waitForResponse("**/api/workstreams");
  await page.evaluate(
    `window.appSources.forEach((source) => source.dispatchEvent(new MessageEvent('workstreams', { data: '{}' })))`,
  );
  await reloaded;

  await expect(main.getByText("Seasonal prices")).toBeVisible();
  await expect(main.getByText("The server is not available (502).")).toHaveCount(0);
  await page.unroute("**/api/workstreams");
});
