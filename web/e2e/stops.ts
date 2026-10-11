import type { Page } from "@playwright/test";

export const longReason = [
  "**Question:** which plan model do you want?",
  "The task cannot go on with the old model, because the old table has no column for the price. Each plan needs a limit of seats and a price in cents.",
  "- Keep the old table and add a column.\n- Make a new table and copy the rows.\n- Ask the team that owns the billing code.",
  "Answer in the issue, then press **Resume**.",
].join("\n\n");

export const needsHumanRoute = "**/api/needs-human";

// Gives the stopped issue #41 the long reason, and the stopped issue #42 a short reason.
export function routeReasons(page: Page) {
  return page.route(needsHumanRoute, async (route) => {
    const response = await route.fetch();
    const body = (await response.json()) as { data: { number: number; reason: string }[] };
    for (const issue of body.data) {
      if (issue.number === 41) {
        issue.reason = longReason;
      }
      if (issue.number === 42) {
        issue.reason = "The agent did not push.";
      }
    }
    await route.fulfill({ response, json: body });
  });
}
