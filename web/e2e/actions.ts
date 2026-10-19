import type { Page } from "@playwright/test";

const brief = [
  "## Goal",
  "Let customers buy a gift card in the shop and pay with it at the checkout. A gift card has a code, a balance and an end date.",
  "## Today",
  "- The shop has only the payment by card.\n- The checkout has no field for a code.\n- The balance of a customer is not stored.",
  "## Scope",
  "- Add the table `gift_cards` with the code, the balance in cents and the end date.\n- Add the page where a customer buys a gift card and gets the code by e-mail.\n- Add the field for a code at the checkout. The balance pays the order before the card does.",
  "## Limits",
  "- Do not change the payment by card.\n- Do not add a refund of a gift card.\n- Follow AGENTS.md: write the migration as a new file.",
  "## Done",
  "- A Go test: a customer buys a gift card and the table has the balance.\n- A Go test: an order that is larger than the balance charges the card for the rest.\n- A Playwright test buys a gift card and pays an order with its code.",
].join("\n\n");

export const createWorkstream = {
  id: 31,
  role: "coordinator",
  tool: "create_workstream",
  arguments: [
    { name: "title", value: "Gift cards" },
    { name: "brief", value: brief },
    { name: "labels", value: '["mobius:autopilot"]' },
  ],
  state: "pending",
  error: "",
  result: "",
  message: "",
  time: "2026-10-15T11:50:00Z",
};

export const messageLead = {
  id: 32,
  role: "coordinator",
  tool: "message_lead",
  arguments: [
    { name: "workstream", value: "12" },
    {
      name: "message",
      value: [
        "The Owner decided that the prices stay in cents.",
        "Change the price column of the plan table to an integer. Write the migration as a new file, and keep the old column until the next release.",
        "Tell the Owner when the pull request is ready.",
      ].join("\n\n"),
    },
  ],
  state: "pending",
  error: "",
  result: "",
  message: "",
  time: "2026-10-15T11:55:00Z",
};

export const done = {
  ...createWorkstream,
  state: "done",
  result: "Created the Workstream #790.",
  message: "Go ahead.",
  decidedAt: "2026-10-15T11:52:00Z",
};

export const failed = { ...createWorkstream, error: "403 API rate limit exceeded" };

export const rejected = {
  ...createWorkstream,
  state: "rejected",
  message: "Split the Brief in two Workstreams: one for the table, one for the checkout.",
  decidedAt: "2026-10-15T11:52:00Z",
};

type Action = typeof createWorkstream;

export function cardMessage(action: Action) {
  return {
    id: 9000 + action.id,
    author: "Coordinator",
    browserId: "",
    deliveredAt: null,
    images: 0,
    organization: "owner",
    repository: "",
    stoppedAt: null,
    text: "",
    time: action.time,
    workstream: 0,
    action,
  };
}

export const chatRoute = /\/api\/chat\?(?!.*before=)/;

// Adds the cards to the newest page of the Coordinator chat that the server gives.
export function routeCards(page: Page, ...actions: Action[]) {
  return page.route(chatRoute, async (route) => {
    const response = await route.fetch();
    const body = (await response.json()) as { data: { messages: object[] } };
    body.data.messages.push(...actions.map(cardMessage));
    await route.fulfill({ response, json: body });
  });
}

export const createTitle = 'Create the Workstream "Gift cards"';
export const messageTitle = "Message to the Lead of #12";

export const approvalItem = (action: Action) => ({
  dismissedAt: null,
  id: 300 + action.id,
  organization: "owner",
  repository: "owner/shop",
  workstream: action.tool === "message_lead" ? 12 : 0,
  issue: 0,
  kind: "approval",
  time: action.time,
  title: action.tool === "message_lead" ? messageTitle : createTitle,
  reason: "Waits for the approval",
  text: action.arguments
    .map((argument) => `**${argument.name}**\n\n${argument.value}`)
    .join("\n\n"),
  link: "",
  pullRequest: 0,
  pullRequestUrl: "",
  reportUrl: "",
  action,
});

export const approvalItems = [createWorkstream, messageLead].map(approvalItem);

// Answers the decision of the action with the action after the decision.
export function routeDecision(
  page: Page,
  action: Action,
  decision: "approve" | "reject",
  after: object,
) {
  const bodies: unknown[] = [];
  void page.route(`**/api/actions/${action.id}/${decision}`, async (route) => {
    bodies.push(route.request().postDataJSON());
    await route.fulfill({ json: { data: after } });
  });
  return bodies;
}
