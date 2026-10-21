import { expect, test, type Locator, type Page } from "@playwright/test";
import {
  addHistory,
  addHistoryTable,
  ask,
  cardOf,
  createTitle,
  draftText,
  draftWorkstream,
  dropColumn,
  dropText,
  historyText,
  historyTableText,
  keepPrices,
  keepText,
  leadTitle,
  sendToCoordinator,
  settle,
  stopUnknown,
  triagerItem,
  unknownText,
  unknownTitle,
} from "./actions.js";
import { fakeMicrophone } from "./microphone.js";
import { routeInboxItems } from "./stops.js";

test.beforeEach(async ({ page }) => {
  await page.goto("/github");
  await page.getByLabel("Access password").fill("correct horse");
  await page.getByRole("button", { name: "Log in" }).click();
  await expect(page.getByLabel("Access password")).toBeHidden();
});

test.afterEach(async ({ page }) => {
  await page.unrouteAll({ behavior: "ignoreErrors" });
  await settle(page);
});

// An open panel hides the main element from the role queries, so the card has no role query.
const row = (page: Page, title: string) =>
  page.getByRole("main").getByRole("listitem").filter({ hasText: title });
const panel = (page: Page) => page.getByRole("dialog");
const note = (page: Page) => panel(page).getByRole("textbox", { name: "Note for the Coordinator" });
const approve = (page: Page) => panel(page).getByRole("button", { name: "Approve" });
const reject = (page: Page) => panel(page).getByRole("button", { name: "Reject" });
const waiting = (page: Page, count: number) =>
  page.getByRole("heading", { name: `Waits for you (${count})` });
const agentTimeout = { timeout: 30_000 };
const badge = (card: Locator, name: string) =>
  card.locator('[data-slot="badge"]', { hasText: name });
const link = (page: Page, path: string) =>
  page.locator(`nav a[href="${path}"]`).filter({ visible: true });

const fits = (locator: Locator) =>
  locator.first().evaluate((el) => el.scrollWidth <= el.clientWidth);

async function openChat(page: Page) {
  await page.goto("/chat");
  await expect(page.getByRole("textbox", { name: "Message to the Coordinator" })).toBeVisible();
}

async function answer(page: Page, title: string) {
  await row(page, title).getByRole("button", { name: "Answer" }).click();
  await expect(panel(page)).toBeVisible();
}

test("the Owner approves an action with a note", async ({ page }) => {
  await openChat(page);
  const main = page.getByRole("main");

  await ask(page, keepPrices);
  await expect(waiting(page, 1)).toBeVisible(agentTimeout);
  const card = cardOf(page, keepText);
  await expect(card.getByText("Waits for the approval")).toBeVisible();
  await expect(row(page, leadTitle).getByRole("button")).toHaveText("Answer");
  await expect(main.getByRole("button", { name: "Approve" })).toHaveCount(0);
  await expect(main.getByRole("button", { name: "Reject" })).toHaveCount(0);

  await link(page, "/inbox").click();
  await expect(row(page, leadTitle).getByRole("button")).toHaveText("Answer");
  await link(page, "/chat").click();
  await expect(waiting(page, 1)).toBeVisible(agentTimeout);

  await answer(page, leadTitle);
  await expect(panel(page).getByText("keep the old column until the next release")).toBeVisible();
  await note(page).fill("Go ahead.");
  await approve(page).click();

  await expect(panel(page)).toBeHidden();
  await expect(waiting(page, 1)).toBeHidden();
  await expect(badge(card, "Done")).toBeVisible();
  await expect(card.getByText("Sent the message to the Lead of #14.")).toBeVisible();
  await expect(card.getByText("Go ahead.")).toBeVisible();

  await link(page, "/inbox").click();
  await expect(main.getByRole("tab", { name: /^To do/ })).toBeVisible();
  await expect(row(page, leadTitle)).toBeHidden();
});

test("the Owner rejects an action with a note", async ({ page }) => {
  await openChat(page);

  await ask(page, dropColumn);
  await expect(waiting(page, 1)).toBeVisible(agentTimeout);
  await answer(page, leadTitle);
  await note(page).fill("Wait for the migration of #12.");
  await reject(page).click();

  await expect(panel(page)).toBeHidden();
  const card = cardOf(page, dropText);
  await expect(card.getByText("Rejected", { exact: true })).toBeVisible();
  await expect(card.getByText("Wait for the migration of #12.")).toBeVisible();
  await expect(waiting(page, 1)).toBeHidden();

  await link(page, "/inbox").click();
  await expect(row(page, leadTitle)).toBeHidden();
});

test("a second call of the tool replaces the action that waits", async ({ page }) => {
  await openChat(page);

  await ask(page, addHistory);
  await expect(waiting(page, 1)).toBeVisible(agentTimeout);
  await answer(page, leadTitle);

  await sendToCoordinator(page, addHistoryTable);

  await expect(panel(page)).toBeHidden();
  await expect(cardOf(page, historyText).getByText("Replaced", { exact: true })).toBeVisible();
  await expect(cardOf(page, historyTableText).getByText("Waits for the approval")).toBeVisible();
  await expect(waiting(page, 1)).toBeVisible(agentTimeout);
  await expect(row(page, leadTitle)).toHaveCount(1);
});

test("a failed action keeps the panel with its error and its note, and a second approve sends again", async ({
  page,
}) => {
  await openChat(page);
  const approvals: string[] = [];
  page.on("request", (request) => {
    if (request.url().endsWith("/approve")) {
      approvals.push(request.postData() ?? "");
    }
  });

  await ask(page, stopUnknown);
  await expect(waiting(page, 1)).toBeVisible(agentTimeout);
  await answer(page, unknownTitle);
  await note(page).fill("Go ahead.");
  await approve(page).click();

  await expect(panel(page).getByRole("alert")).toHaveText(/#9999 is not an open Workstream/);
  await expect(note(page)).toHaveValue("Go ahead.");
  const card = cardOf(page, unknownText);
  await expect(badge(card, "Failed")).toBeVisible();
  await expect(card.getByText(/#9999 is not an open Workstream/)).toBeVisible();
  await expect(approve(page)).toBeEnabled();

  await approve(page).click();
  await expect.poll(() => approvals.length).toBe(2);
  await expect(panel(page).getByRole("alert")).toHaveText(/#9999 is not an open Workstream/);

  await reject(page).click();
  await expect(panel(page)).toBeHidden();
  await expect(card.getByText("Rejected", { exact: true })).toBeVisible();
  expect(approvals).toEqual([
    JSON.stringify({ message: "Go ahead." }),
    JSON.stringify({ message: "Go ahead." }),
  ]);
});

test("a decision with an empty note goes out one time and hides the row while the request runs", async ({
  page,
}) => {
  await openChat(page);
  const bodies: unknown[] = [];
  const gate: { open?: () => void } = {};
  const held = new Promise<void>((resolve) => {
    gate.open = resolve;
  });
  await page.route("**/api/actions/*/approve", async (route) => {
    bodies.push(route.request().postDataJSON());
    await held;
    await route.continue();
  });

  await ask(page, keepPrices);
  await expect(waiting(page, 1)).toBeVisible(agentTimeout);
  await answer(page, leadTitle);
  await approve(page).click();
  await expect(panel(page)).toBeHidden();
  await expect(row(page, leadTitle)).toBeHidden();
  await expect.poll(() => bodies.length).toBe(1);
  gate.open?.();

  await expect(waiting(page, 1)).toBeHidden();
  expect(bodies).toEqual([{ message: "" }]);
});

test("the panel has the microphone button and no attach button", async ({ page }) => {
  await page.addInitScript(fakeMicrophone);
  await openChat(page);
  await ask(page, keepPrices);
  await expect(waiting(page, 1)).toBeVisible(agentTimeout);

  await answer(page, leadTitle);

  await expect(panel(page).getByRole("button", { name: "Start voice input" })).toBeVisible();
  await expect(panel(page).getByRole("button", { name: "Attach images" })).toHaveCount(0);
  await expect(panel(page).getByRole("button", { name: "Send" })).toHaveCount(0);
});

test("a chat message goes out as a message while the tray has rows", async ({ page }) => {
  await openChat(page);
  await ask(page, keepPrices);
  await expect(waiting(page, 1)).toBeVisible(agentTimeout);
  const decisions: string[] = [];
  page.on("request", (request) => {
    if (request.url().includes("/api/actions/")) {
      decisions.push(request.url());
    }
  });
  const sent = page.waitForRequest("**/api/chat/messages");
  await page.route("/api/chat/messages", (route) => route.fulfill({ status: 204 }));

  await page.getByRole("textbox", { name: "Message to the Coordinator" }).fill("Hello");
  await page.getByRole("button", { name: "Send" }).click();
  await sent;

  await expect(waiting(page, 1)).toBeVisible(agentTimeout);
  expect(decisions).toEqual([]);
});

test("the Inbox approves an item in the panel", async ({ page }) => {
  await openChat(page);
  await ask(page, keepPrices);
  await expect(waiting(page, 1)).toBeVisible(agentTimeout);

  await link(page, "/inbox").click();
  await expect(row(page, leadTitle)).toBeVisible();
  await expect(row(page, leadTitle)).not.toContainText("message_lead");
  await expect(row(page, leadTitle).getByRole("button")).toHaveText("Answer");

  await answer(page, leadTitle);
  await approve(page).click();

  await expect(panel(page)).toBeHidden();
  await expect(row(page, leadTitle)).toBeHidden();
});

test("the Inbox item of the Triager links to its issue", async ({ page }) => {
  await routeInboxItems(page, [triagerItem]);

  await page.goto("/inbox");

  await expect(
    row(page, "Message to the Lead of #12").getByRole("link", { name: "#61" }),
  ).toHaveAttribute("href", "https://github.com/owner/shop/issues/61");
});

test("the phone shows the cards, the tray and the panel with no horizontal scroll", async ({
  page,
}) => {
  await page.addInitScript(fakeMicrophone);
  await page.setViewportSize({ width: 390, height: 844 });
  await openChat(page);
  await ask(page, draftWorkstream);
  await expect(waiting(page, 1)).toBeVisible(agentTimeout);
  await ask(page, keepPrices);
  await expect(waiting(page, 2)).toBeVisible(agentTimeout);

  expect(await fits(page.locator("html"))).toBe(true);
  expect(await fits(cardOf(page, draftText))).toBe(true);
  expect(await fits(cardOf(page, keepText))).toBe(true);
  expect(await fits(page.locator("main div.overflow-y-auto"))).toBe(true);
  expect(await fits(page.locator("main ul"))).toBe(true);

  await answer(page, createTitle);
  await note(page).fill("Go ahead.");

  expect(await fits(page.locator("html"))).toBe(true);
  expect(await fits(page.getByRole("dialog"))).toBe(true);
  const microphone = await panel(page)
    .getByRole("button", { name: "Start voice input" })
    .boundingBox();
  const rejectBox = await reject(page).boundingBox();
  const approveBox = await approve(page).boundingBox();
  expect(Math.abs((microphone?.y ?? 0) - (rejectBox?.y ?? 99))).toBeLessThan(4);
  expect(Math.abs((approveBox?.y ?? 0) - (rejectBox?.y ?? 99))).toBeLessThan(4);
  expect((approveBox?.x ?? 999) + (approveBox?.width ?? 0)).toBeLessThanOrEqual(390);
});
