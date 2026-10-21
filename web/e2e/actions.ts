import { expect, type Page } from "@playwright/test";

// The Owner messages that the fake Coordinator of TestServer answers with a tool call.
export const draftWorkstream = "Draft the Workstream for gift cards.";
export const keepPrices = "Tell the Lead of #14 to keep the prices in cents.";
export const dropColumn = "Tell the Lead of #14 to drop the old price column.";
export const addHistory = "Tell the Lead of #14 to add a price history.";
export const addHistoryTable = "Tell the Lead of #14 to add a price history table.";
export const stopUnknown = "Tell the Lead of #9999 to stop.";

// A text of the arguments of each call, to find its card.
export const draftText = "Let customers buy a gift card";
export const keepText = "The Owner decided that the prices stay in cents.";
export const dropText = "Drop the old price column of the plan table";
export const historyText = "Add a history of the plan prices.";
export const historyTableText = "Add a table with the history of the plan prices.";
export const unknownText = "Stop the work.";

export const createTitle = 'Create the Workstream "Gift cards"';
export const leadTitle = "Message to the Lead of #14";
export const unknownTitle = "Message to the Lead of #9999";

export async function ask(page: Page, text: string) {
  await page.getByRole("textbox", { name: "Message to the Coordinator" }).fill(text);
  await page.getByRole("button", { name: "Send" }).click();
}

// An action that the tool call of the fake Coordinator made, as the card and the tray row show it.
export const cardOf = (page: Page, text: string) =>
  page.locator("[data-action]").filter({ hasText: text }).last();

// The tests have no DOM types, so the call is a script. The device login of the page is a cookie that page.request does
// not have.
async function api<T>(page: Page, method: string, path: string, body?: object) {
  const init = JSON.stringify({
    method,
    headers: { "Content-Type": "application/json" },
    body: body && JSON.stringify(body),
  });
  return (await page.evaluate(
    `fetch(${JSON.stringify(path)}, ${init}).then(async (response) => ({ status: response.status, data: response.status === 204 ? null : (await response.json()).data }))`,
  )) as { status: number; data: T };
}

const coordinatorChat = "/api/chat?organization=owner&repository=&workstream=0";

// Sends the text to the Coordinator chat without the composer.
export async function sendToCoordinator(page: Page, text: string) {
  await page.evaluate(`(() => {
    const form = new FormData()
    form.append('organization', 'owner')
    form.append('repository', '')
    form.append('workstream', '0')
    form.append('id', crypto.randomUUID())
    form.append('text', ${JSON.stringify(text)})
    return fetch('/api/chat/messages', { method: 'POST', body: form })
  })()`);
}

// Waits until the Coordinator has answered each message, and gives the last message.
export async function idle(page: Page) {
  const read = async () =>
    (await api<{ writing: boolean; messages: { id: number }[] }>(page, "GET", coordinatorChat))
      .data;
  await expect.poll(async () => (await read()).writing).toBe(false);
  return (await read()).messages.at(-1);
}

// Marks each message of the Coordinator chat as seen, after the Coordinator has answered each message.
export async function seen(page: Page) {
  const last = await idle(page);
  if (last) {
    const res = await api(page, "POST", "/api/chat/seen", {
      organization: "owner",
      repository: "",
      workstream: 0,
      message: last.id,
    });
    expect(res.status).toBe(204);
  }
}

// Rejects each pending action, so that a test leaves no action and no unread message.
export async function settle(page: Page) {
  const inbox = await api<{ action: { id: number; state: string } | null }[]>(
    page,
    "GET",
    "/api/inbox",
  );
  for (const item of inbox.data) {
    if (item.action?.state === "pending") {
      const res = await api(page, "POST", `/api/actions/${item.action.id}/reject`, { message: "" });
      expect(res.status).toBe(200);
    }
  }
  await seen(page);
}

// The sample Inbox item of the Triager. The test server has no Triager session.
const triagerAction = {
  id: 33,
  role: "triager",
  tool: "message_lead",
  arguments: [
    { name: "workstream", value: "12" },
    { name: "text", value: "Change the price column of the plan table to an integer." },
  ],
  state: "pending",
  error: "",
  result: "",
  message: "",
  time: "2026-10-15T11:55:00Z",
  decidedAt: null,
};

export const triagerItem = {
  dismissedAt: null,
  id: 333,
  organization: "owner",
  repository: "owner/shop",
  workstreamRepository: "owner/shop",
  workstream: 12,
  issue: 61,
  kind: "approval",
  time: triagerAction.time,
  title: "Message to the Lead of #12",
  reason: "Waits for your approval",
  text: "workstream: 12",
  link: "https://github.com/owner/shop/issues/61",
  pullRequest: 0,
  pullRequestUrl: "",
  reportUrl: "",
  action: triagerAction,
};
