import { expect, test, type Page } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.goto("/github");
  await page.getByLabel("Access password").fill("correct horse");
  await page.getByRole("button", { name: "Log in" }).click();
  await expect(page.getByLabel("Access password")).toBeHidden();
});

// The API request of the page carries the device login, but the request context of Playwright does not.
test.afterEach(async ({ page }) => {
  const status = await page.evaluate(() =>
    fetch("/api/voice/language", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ language: "en" }),
    }).then((response) => response.status),
  );
  expect(status).toBe(200);
});

const section = (page: Page, title: string) =>
  page
    .getByRole("main")
    .locator("section")
    .filter({ has: page.getByRole("heading", { name: title }) });

const names = (page: Page, title: string) =>
  section(page, title).getByRole("button").allInnerTexts();

const selected = (page: Page, title: string) =>
  section(page, title).locator('button[aria-current="true"]').allInnerTexts();

const saved = (page: Page) =>
  page.waitForResponse(
    (res) => res.url().endsWith("/api/voice/language") && res.request().method() === "PUT",
  );

const open = async (page: Page) => {
  await page.goto("/settings/voice");
  await expect(section(page, "Suggested").getByRole("button").first()).toBeVisible();
};

test("the Voice link of the Settings page opens the Voice page on a phone", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/settings");
  const link = page.getByRole("main").getByRole("link", { name: "Voice" });
  await expect(link).toContainText("English");
  await link.click();
  await expect(page).toHaveURL("/settings/voice");
  await expect(section(page, "Suggested")).toBeVisible();
});

test("the side bar of the desktop links the Voice page", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.goto("/settings");
  await page.locator('nav a[href="/settings/voice"]').filter({ visible: true }).click();
  await expect(page).toHaveURL("/settings/voice");
  await expect(section(page, "Suggested")).toBeVisible();
});

test("with English selected, Suggested has English and Detect automatically", async ({ page }) => {
  await open(page);
  expect(await names(page, "Suggested")).toEqual(["English", "Detect automatically"]);
  expect(await selected(page, "Suggested")).toEqual(["English"]);
  const all = await names(page, "All languages");
  expect(all[0]).toBe("Afrikaans");
  expect(all).toContain("English");
  expect(all).not.toContain("Detect automatically");
  expect(await selected(page, "All languages")).toEqual(["English"]);
});

test("a tap on German saves it, moves the check mark and keeps it after a reload", async ({
  page,
}) => {
  await open(page);
  const request = saved(page);
  await section(page, "All languages").getByRole("button", { name: "German" }).click();
  const res = await request;
  expect(res.request().postDataJSON()).toEqual({ language: "de" });
  await expect
    .poll(() => names(page, "Suggested"))
    .toEqual(["German", "English", "Detect automatically"]);
  expect(await selected(page, "Suggested")).toEqual(["German"]);
  expect(await selected(page, "All languages")).toEqual(["German"]);
  expect(await names(page, "All languages")).toContain("English");

  await page.reload();
  await expect.poll(() => selected(page, "Suggested")).toEqual(["German"]);
});

test("a tap on Detect automatically puts it first in Suggested", async ({ page }) => {
  await open(page);
  const request = saved(page);
  await section(page, "Suggested").getByRole("button", { name: "Detect automatically" }).click();
  expect((await request).ok()).toBe(true);
  await expect.poll(() => names(page, "Suggested")).toEqual(["Detect automatically", "English"]);
  expect(await selected(page, "Suggested")).toEqual(["Detect automatically"]);
});

test("the search shows one list with no section heading", async ({ page }) => {
  await open(page);
  const search = page.getByLabel("Search languages");
  const main = page.getByRole("main");

  await search.fill("GER");
  await expect(main.getByRole("button", { name: "German" })).toBeVisible();
  await expect(main.getByRole("heading", { name: "Suggested" })).toBeHidden();
  await expect(main.getByRole("heading", { name: "All languages" })).toBeHidden();

  await search.fill("auto");
  await expect(main.getByRole("button", { name: "Detect automatically" })).toBeVisible();

  await search.fill("xyz");
  await expect(main.getByText("No language has this name.")).toBeVisible();
  await expect(main.getByRole("button", { name: "English" })).toBeHidden();
});

test("a failed save shows the error and keeps the old check mark", async ({ page }) => {
  await open(page);
  await page.route("**/api/voice/language", (route) =>
    route.request().method() === "PUT"
      ? route.fulfill({ status: 500, json: { error: "disk full" } })
      : route.continue(),
  );
  const request = saved(page);
  await section(page, "All languages").getByRole("button", { name: "German" }).click();
  expect((await request).status()).toBe(500);
  await expect(page.getByText("Mobius could not save the language: disk full.")).toBeVisible();
  expect(await selected(page, "Suggested")).toEqual(["English"]);
  expect(await selected(page, "All languages")).toEqual(["English"]);
  await page.unroute("**/api/voice/language");
});
