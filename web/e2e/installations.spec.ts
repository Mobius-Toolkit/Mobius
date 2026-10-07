import { expect, test } from "@playwright/test";

test("a change of the installations shows with no reload", async ({ page }) => {
  await page.goto("/github");
  await page.getByLabel("Access password").fill("correct horse");
  await page.getByRole("button", { name: "Log in" }).click();
  await expect(page.getByLabel("Access password")).toBeHidden();
  const main = page.getByRole("main");
  const switcher = page.getByRole("button", { name: "owner" }).filter({ visible: true });
  const organization = page.getByRole("menuitemradio", { name: "acme" });
  const slow = { timeout: 30_000 };
  await expect(main.getByText("owner/shop")).toBeVisible();
  await page.evaluate("window.sameLoad = true");

  await page.request.post("/e2e/repositories/acme/orchard");

  await expect(main.getByText("acme/orchard")).toBeVisible(slow);
  await switcher.click();
  await expect(organization).toBeVisible();
  await page.keyboard.press("Escape");

  await page.request.delete("/e2e/repositories/acme/orchard");

  await expect(main.getByText("acme/orchard")).toBeHidden(slow);
  await switcher.click();
  await expect(page.getByRole("menuitemradio", { name: "plants" })).toBeVisible();
  await expect(organization).toBeHidden();
  expect(await page.evaluate("window.sameLoad")).toBe(true);
});
