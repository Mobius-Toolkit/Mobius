import type { Page } from "@playwright/test";

export const longTaskTitle =
  "Fix the CI failure of TestApprovePullRequestRefusesANewHeadAndMovesTheTaskBackToChecks";

export const tasksUrl = (url: URL) => url.pathname === "/api/workstreams/owner/shop/12/tasks";

// Adds a task with a long word in its title to the Tasks list of owner/shop#12. Remove it with page.unroute(tasksUrl).
export async function routeLongTask(page: Page) {
  await page.route(tasksUrl, async (route) => {
    const response = await route.fetch();
    const body = await response.json();
    body.data.push({
      blockedBy: [],
      depth: 0,
      number: 1044,
      otherRepository: false,
      repository: "owner/shop",
      state: "working",
      title: longTaskTitle,
      url: "https://github.com/owner/shop/issues/1044",
    });
    await route.fulfill({ response, json: body });
  });
}
