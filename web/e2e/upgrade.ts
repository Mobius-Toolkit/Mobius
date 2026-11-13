import { expect, type Page } from "@playwright/test";

export const upgradeRoute = "**/api/upgrade";

export const retryFailure =
  "download of mobius-x86_64-unknown-linux-gnu.tar.gz of release v0.1.4 failed: GET https://api.github.com/repos/Mobius-Toolkit/Mobius/releases/assets/9: 504 Gateway Timeout";

const idle = { step: "idle", tries: 0, failure: "", nextTry: null };

// The state of the upgrade in the step retry. The page clock must read 40 s before nextTry.
export const retryUpgrade = {
  step: "retry",
  tries: 3,
  failure: retryFailure,
  nextTry: "2026-10-15T12:00:40Z",
};

// Gives the upgrade state to the page. A cancel of the upgrade ends it, as the server does.
export async function routeUpgrade(page: Page, upgrade: typeof retryUpgrade | typeof idle) {
  let current = upgrade;
  await page.route(upgradeRoute, async (route) => {
    if (route.request().method() === "DELETE") {
      current = idle;
      await route.fulfill({ status: 204 });
      return;
    }
    await route.fulfill({ json: { data: current } });
  });
}

// The server starts with a drain. The poll starts no task while the drain is on. The request of the page carries the
// device login, but the request context of Playwright does not.
export async function cancelDrain(page: Page) {
  const status = await page.evaluate(() =>
    fetch("/api/drain", { method: "DELETE" }).then((response) => response.status),
  );
  expect(status).toBe(204);
}
