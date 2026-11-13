import { expect, type Page } from "@playwright/test";

// The server answers a login after it writes the device login. That write can wait for the database lock longer than
// the 5 s of an assertion, so the helper waits for the answer.
export async function logIn(page: Page) {
  await page.goto("/github");
  await page.getByLabel("Access password").fill("correct horse");
  const answer = page.waitForResponse((response) => response.url().endsWith("/api/login"));
  await page.getByRole("button", { name: "Log in" }).click();
  expect((await answer).status()).toBe(204);
  await expect(page.getByLabel("Access password")).toBeHidden();
}
