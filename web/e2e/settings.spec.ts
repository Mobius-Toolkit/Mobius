import { expect, test } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.goto("/github");
  await page.getByLabel("Access password").fill("correct horse");
  await page.getByRole("button", { name: "Log in" }).click();
  await expect(page.getByLabel("Access password")).toBeHidden();
});

test("the Settings page shows the server and browser versions", async ({ page }) => {
  await page.goto("/settings");
  const versions = page.getByRole("main").locator("dl");
  await expect(versions.locator("div").filter({ hasText: "Server" })).toHaveText("Serverv0.1.0");
  await expect(versions.locator("div").filter({ hasText: "Browser" })).toHaveText("Browserv0.1.3");
});

test("the Settings page hides the server line when the server has no version", async ({ page }) => {
  await page.route("**/api/version", (route) => route.fulfill({ json: { data: { release: "" } } }));
  await page.goto("/settings");
  const versions = page.getByRole("main").locator("dl");
  await expect(versions.getByText("Browser")).toBeVisible();
  await expect(versions.getByText("v0.1.3")).toBeVisible();
  await expect(versions.getByText("Server")).toBeHidden();
});
