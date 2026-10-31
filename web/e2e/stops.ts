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
  workstreamRepository: "owner/shop",
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
    title: `Lead of #12 Integrate loyalty plans did not get: "Start the work on the plan tiers."`,
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

// An approval of the Lead of owner/shop#12 for an edit of the Brief.
export const leadActionItem = {
  ...base,
  id: 210,
  issue: 0,
  kind: "approval",
  title: "Edit the Brief of #12 Integrate loyalty plans",
  reason: "The Lead waits for your approval",
  text: "",
  action: {
    id: 78,
    role: "lead",
    tool: "edit_brief",
    arguments: [{ name: "text", value: "Keep the prices in cents." }],
    state: "pending",
    error: "",
    message: "",
    result: "",
    decidedAt: null,
    time: "2026-09-28T09:30:00Z",
  },
};

// A question and an action that the Coordinator chat shows.
export const coordinatorQuestion = {
  ...base,
  id: 211,
  issue: 0,
  repository: "",
  workstreamRepository: "",
  workstream: 0,
  kind: "question",
  title: "Pick the shop currency",
  reason: "The Coordinator asks a question",
  text: "Which currency?",
  link: "",
};

export const coordinatorActionItem = {
  ...leadActionItem,
  id: 212,
  repository: "",
  workstreamRepository: "",
  workstream: 0,
  title: "Message to the Lead of #14 Early renewals",
  reason: "The Coordinator waits for your approval",
  action: { ...leadActionItem.action, id: 79, role: "coordinator", tool: "message_lead" },
};

// A message that the Coordinator did not get.
export const coordinatorFailedItem = {
  ...base,
  id: 216,
  issue: 0,
  repository: "",
  workstreamRepository: "",
  workstream: 0,
  kind: "Lead failed",
  title: `Coordinator did not get: "It shows this crossed O icon. Should be…"`,
  reason: "The Coordinator did not get your message",
  text: "It shows this crossed O icon. Should be a tick, not a cross.",
};

// A question of the Coordinator whose title is one long word, with the text and the options of a question.
export const longWordTitle =
  "Fix the CI failure of TestSearchIssuesGivesOpenAndClosedIssuesWithTheCloseReasonAndTheLastThreeCommentsOfTrustedAuthors";

export const longWordQuestionItem = {
  ...coordinatorQuestion,
  id: 215,
  title: longWordTitle,
  text: "The shop needs a store for the plans.",
  questions: [
    {
      text: "Which store?",
      multiple: false,
      options: [
        { title: "SQLite", details: "One file, no server." },
        { title: "Postgres", details: "A server that many writers share." },
      ],
    },
  ],
};

// A mandatory todo and an optional todo of the pull requests of owner/shop#12.
export const todoItems = [
  {
    ...base,
    id: 213,
    issue: 40,
    kind: "todo",
    title: "MOBIUS_SMTP_HOST",
    reason: "mandatory · Deployment needs",
    text: "env_var: MOBIUS_SMTP_HOST",
    link: `${link}/issues/40`,
    pullRequest: 1110,
    pullRequestUrl: `${link}/pull/1110`,
    todo: {
      kind: "env_var",
      name: "MOBIUS_SMTP_HOST",
      path: "internal/config/config.go",
      line: 42,
      mandatory: true,
      reason: "The server reads it at the start and has no default.",
      note: "",
    },
  },
  {
    ...base,
    id: 214,
    issue: 43,
    kind: "todo",
    title: "MOBIUS_SMTP_TIMEOUT",
    reason: "optional · Deployment needs",
    text: "env_var: MOBIUS_SMTP_TIMEOUT",
    link: `${link}/issues/43`,
    pullRequest: 1110,
    pullRequestUrl: `${link}/pull/1110`,
    todo: {
      kind: "env_var",
      name: "MOBIUS_SMTP_TIMEOUT",
      path: "internal/config/config.go",
      line: 57,
      mandatory: false,
      reason: "The server waits 10 seconds when the variable is not set.",
      note: "",
    },
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
