import type { Page } from "@playwright/test";

// The name of the paused agent is long, so its badge does not fit in the first line on a phone.
export const routeLongNameOfPaused = (page: Page, extra?: { issue: number; title: string }) =>
  page.route("**/api/agents", async (route) => {
    const response = await route.fetch();
    const body = await response.json();
    for (const group of body.data.groups) {
      for (const row of group.agents) {
        if (row.agent.wait === "paused") {
          row.agent.title = "of the shop plans";
        }
        if (extra && row.agent.issue === extra.issue) {
          row.agent.title = extra.title;
        }
      }
    }
    await route.fulfill({ response, json: body });
  });
