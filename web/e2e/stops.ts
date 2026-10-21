import type { Page } from "@playwright/test";

export const longReason = [
  "**Question:** which plan model do you want?",
  "The task cannot go on with the old model, because the old table has no column for the price. Each plan needs a limit of seats and a price in cents.",
  "- Keep the old table and add a column.\n- Make a new table and copy the rows.\n- Ask the team that owns the billing code.",
  "Answer in the issue, then press **Resume**.",
].join("\n\n");

export const longTitle =
  "Replace the old plan table with a new table for the seat limit and the price of a plan";

const link = "https://github.com/owner/shop";

const base = {
  dismissedAt: null,
  questions: [],
  organization: "owner",
  repository: "owner/shop",
  workstream: 12,
  time: "2026-09-28T09:30:00Z",
  title: "",
  reason: "",
  link: "",
  pullRequest: 0,
  pullRequestUrl: "",
  reportUrl: "",
};

// The stopped issue #41 has the long text, and the stopped issue #42 a short text.
export const stoppedItems = [
  {
    ...base,
    id: 201,
    issue: 41,
    kind: "stopped",
    title: "Add plan model",
    reason: "The agent asked a question",
    text: longReason,
    link: `${link}/issues/41`,
  },
  {
    ...base,
    id: 202,
    issue: 42,
    kind: "stopped",
    title: "Let customers change plans",
    reason: "The agent did not push.",
    text: "The agent did not push.",
    link: `${link}/issues/42`,
  },
];

// A question with a long title, and a text that has Markdown.
export const longTitleItem = {
  ...base,
  id: 203,
  issue: 409,
  kind: "question",
  title: longTitle,
  reason: "The Lead asks a question",
  text: longReason,
  link: `${link}/issues/409`,
};

// An item of each other kind for the Workstream owner/shop#12.
export const otherItems = [
  {
    ...base,
    id: 204,
    issue: 36,
    kind: "lost",
    title: "Rename the plan table",
    reason: "Mobius lost the task",
    text: "Mobius lost the task.",
    link: `${link}/issues/36`,
  },
  {
    ...base,
    id: 205,
    issue: 30,
    kind: "ready for review",
    title: "Name the plan tiers",
    reason: "PR #44 is ready to merge",
    text: "The pull request waits.",
    link: `${link}/issues/30`,
    pullRequest: 44,
    pullRequestUrl: `${link}/pull/44`,
  },
  {
    ...base,
    id: 206,
    issue: 12,
    kind: "Lead failed",
    reason: "The Lead did not get your message",
    text: "Start the work on the plan tiers.",
  },
  {
    ...base,
    id: 207,
    issue: 46,
    kind: "full disk",
    reason: "The local check waits for free disk",
    text: "The disk is full.",
  },
];

export const inboxRoute = "**/api/inbox";

// Adds the items to the Inbox that the server gives.
export function routeInboxItems(page: Page, items: object[]) {
  return page.route(inboxRoute, async (route) => {
    const response = await route.fetch();
    const body = (await response.json()) as { data: object[] };
    body.data.push(...items);
    await route.fulfill({ response, json: body });
  });
}
