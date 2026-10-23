import type { Page } from "@playwright/test";

const shop = "https://github.com/owner/shop";

function task(
  number: number,
  state: string,
  stateAt: string,
  title: string,
  extra: Record<string, unknown> = {},
) {
  return {
    repository: "owner/shop",
    number,
    title,
    url: `${shop}/issues/${number}`,
    workstreamRepository: "owner/shop",
    workstream: 12,
    workstreamTitle: "Integrate loyalty plans",
    state,
    stateAt,
    pullRequest: null,
    pullRequestUrl: null,
    fixRounds: 0,
    reviewRounds: 0,
    merging: false,
    mergeRefusal: "",
    ...extra,
  };
}

function pull(number: number, extra: Record<string, unknown> = {}) {
  return { pullRequest: number, pullRequestUrl: `${shop}/pull/${number}`, ...extra };
}

// The times are before 2026-10-15T12:00:00Z, the time that the screenshots test sets.
export const boardTasks = [
  task(51, "dispatched", "2026-10-15T11:55:00Z", "Add a retry to the order sync"),
  task(52, "queued", "2026-10-15T11:40:00Z", "Show the plan limit on the account page"),
  task(53, "answered", "2026-10-15T11:50:00Z", "Store the plan prices in cents"),
  task(54, "working", "2026-10-15T11:30:00Z", "Let customers change plans", {
    ...pull(44, { fixRounds: 1, reviewRounds: 2 }),
  }),
  task(55, "reviewed", "2026-10-15T11:45:00Z", "Send a mail when a plan changes", pull(45)),
  task(56, "checks", "2026-10-15T11:48:00Z", "Add the invoice export", pull(46)),
  task(
    57,
    "approval",
    "2026-10-15T11:20:00Z",
    "Replace the old plan table with a new table for the seat limit and the price of a plan",
    pull(47, { reviewRounds: 1 }),
  ),
  task(41, "needs_human", "2026-10-15T10:00:00Z", "Add plan model", pull(43, { fixRounds: 3 })),
  task(45, "asked", "2026-10-15T09:00:00Z", "Pick the plan limits"),
  task(42, "stopped", "2026-10-14T18:00:00Z", "Let customers cancel a plan"),
  task(58, "ready_for_review", "2026-10-15T11:10:00Z", "Remind the customer about the renewal", {
    ...pull(48),
    workstream: 14,
    workstreamTitle: "Early renewals",
  }),
  task(59, "ended", "2026-10-15T08:00:00Z", "Round the prices to cents", pull(49)),
  task(60, "ended", "2026-10-15T10:30:00Z", "Fix the plan name in the mail", pull(50)),
  {
    ...task(31, "queued", "2026-10-15T11:00:00Z", "Water the lilies"),
    repository: "plants/garden",
    workstreamRepository: "plants/garden",
    workstream: 20,
    workstreamTitle: "Seasonal prices",
  },
];

export const mergingTask = task(
  61,
  "ready_for_review",
  "2026-10-15T11:35:00Z",
  "Show the renewal date on the invoice",
  {
    ...pull(51),
    merging: true,
  },
);

export const refusedTask = task(
  62,
  "ready_for_review",
  "2026-10-15T11:25:00Z",
  "Add the coupon field to the checkout",
  {
    ...pull(52),
    mergeRefusal: "Required status check is expected.",
  },
);

export const tasksRoute = "/api/tasks";

export async function routeTasks(page: Page, tasks: unknown[]) {
  await page.route(tasksRoute, (route) => route.fulfill({ json: { data: tasks } }));
}
