import { expect, type Page } from "@playwright/test";

// The poll of the server adds mobius:evergreen to the maintenance Workstream owner/shop#14. The page must be open.
export async function waitForEvergreen(page: Page) {
  await expect
    .poll(async () => {
      const body = (await page.evaluate(
        `fetch("/api/workstreams").then((response) => response.json())`,
      )) as { data: { repository: string; number: number; evergreen: boolean }[] };
      return body.data.some(
        (workstream) =>
          workstream.repository === "owner/shop" &&
          workstream.number === 14 &&
          workstream.evergreen,
      );
    })
    .toBe(true);
}
