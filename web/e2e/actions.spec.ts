import { expect, test, type Page } from "@playwright/test";
import {
  approvalItems,
  createTitle,
  createWorkstream,
  done,
  messageLead,
  messageTitle,
  rejected,
  routeCards,
  routeDecision,
} from "./actions.js";
import { fakeMicrophone } from "./microphone.js";
import { routeInboxItems } from "./stops.js";

test.beforeEach(async ({ page }) => {
  await page.goto("/github");
  await page.getByLabel("Access password").fill("correct horse");
  await page.getByRole("button", { name: "Log in" }).click();
  await expect(page.getByLabel("Access password")).toBeHidden();
  await page.route("/api/chat/seen", (route) => route.fulfill({ status: 204 }));
});

// An open panel hides the main element from the role queries, so the card has no role query.
const card = (page: Page, id: number) => page.locator(`[data-action="${id}"]`);
const row = (page: Page, title: string) =>
  page.getByRole("main").getByRole("listitem").filter({ hasText: title });
const panel = (page: Page) => page.getByRole("dialog");
const note = (page: Page) => panel(page).getByRole("textbox", { name: "Note for the Coordinator" });
const approve = (page: Page) => panel(page).getByRole("button", { name: "Approve" });
const reject = (page: Page) => panel(page).getByRole("button", { name: "Reject" });

async function openChat(page: Page) {
  await routeCards(page, createWorkstream, messageLead);
  await routeInboxItems(page, approvalItems);
  await page.goto("/chat");
  await expect(page.getByRole("heading", { name: "Waits for you (2)" })).toBeVisible();
}

async function answer(page: Page, title: string) {
  await row(page, title).getByRole("button", { name: "Answer" }).click();
  await expect(panel(page)).toBeVisible();
}

test("the Owner approves an action with a note", async ({ page }) => {
  await openChat(page);
  const bodies = routeDecision(page, createWorkstream, "approve", done);
  const main = page.getByRole("main");

  await expect(card(page, createWorkstream.id).getByText("Waits for the approval")).toBeVisible();
  await expect(card(page, messageLead.id).getByText("Waits for the approval")).toBeVisible();
  await expect(row(page, createTitle).getByRole("button")).toHaveText("Answer");
  await expect(row(page, messageTitle).getByRole("button")).toHaveText("Answer");
  await expect(row(page, createTitle)).not.toContainText(createWorkstream.tool);
  await expect(main.getByRole("button", { name: "Approve" })).toHaveCount(0);
  await expect(main.getByRole("button", { name: "Reject" })).toHaveCount(0);

  await answer(page, createTitle);
  await expect(panel(page).getByText("A Playwright test buys a gift card")).toBeVisible();
  await expect(panel(page).getByText("Let customers buy a gift card")).toBeVisible();
  await note(page).fill("Go ahead.");
  await approve(page).click();

  await expect(panel(page)).toBeHidden();
  await expect(page.getByRole("heading", { name: "Waits for you (1)" })).toBeVisible();
  await expect(row(page, createTitle)).toBeHidden();
  const created = card(page, createWorkstream.id);
  await expect(created.locator('[data-slot="badge"]', { hasText: "Done" })).toBeVisible();
  await expect(created.getByText("Created the Workstream #790.")).toBeVisible();
  expect(bodies).toEqual([{ message: "Go ahead." }]);
});

test("the Owner rejects an action with a note", async ({ page }) => {
  await openChat(page);
  const bodies = routeDecision(page, createWorkstream, "reject", rejected);

  await answer(page, createTitle);
  await note(page).fill(rejected.message);
  await reject(page).click();

  await expect(panel(page)).toBeHidden();
  const created = card(page, createWorkstream.id);
  await expect(created.getByText("Rejected", { exact: true })).toBeVisible();
  await expect(created.getByText(rejected.message)).toBeVisible();
  await expect(row(page, createTitle)).toBeHidden();
  expect(bodies).toEqual([{ message: rejected.message }]);
});

test("a decision with an empty note goes out one time while the request runs", async ({ page }) => {
  await openChat(page);
  const bodies: unknown[] = [];
  const gate: { open?: () => void } = {};
  const held = new Promise<void>((resolve) => {
    gate.open = resolve;
  });
  await page.route(`**/api/actions/${createWorkstream.id}/approve`, async (route) => {
    bodies.push(route.request().postDataJSON());
    await held;
    await route.fulfill({ json: { data: done } });
  });

  await answer(page, createTitle);
  await approve(page).click();
  await expect(approve(page)).toBeDisabled();
  await expect(reject(page)).toBeDisabled();
  await approve(page).click({ force: true });
  await note(page).press("Enter");
  gate.open?.();

  await expect(panel(page)).toBeHidden();
  expect(bodies).toEqual([{ message: "" }]);
});

test("a failed action keeps the panel with its error and its note, and a second approve sends again", async ({
  page,
}) => {
  await openChat(page);
  const bodies: unknown[] = [];
  await page.route(`**/api/actions/${createWorkstream.id}/approve`, async (route) => {
    bodies.push(route.request().postDataJSON());
    const data =
      bodies.length === 1 ? { ...createWorkstream, error: "403 API rate limit exceeded" } : done;
    await route.fulfill({ json: { data } });
  });

  await answer(page, createTitle);
  await note(page).fill("Go ahead.");
  await approve(page).click();

  await expect(panel(page).getByRole("alert")).toHaveText("403 API rate limit exceeded");
  await expect(note(page)).toHaveValue("Go ahead.");
  await expect(card(page, createWorkstream.id).getByText("Failed")).toBeVisible();
  await expect(approve(page)).toBeEnabled();

  await approve(page).click();

  await expect(panel(page)).toBeHidden();
  await expect(
    card(page, createWorkstream.id).locator('[data-slot="badge"]', { hasText: "Done" }),
  ).toBeVisible();
  expect(bodies).toEqual([{ message: "Go ahead." }, { message: "Go ahead." }]);
});

test("the panel has the microphone button and no attach button", async ({ page }) => {
  await page.addInitScript(fakeMicrophone);
  await openChat(page);

  await answer(page, createTitle);

  await expect(panel(page).getByRole("button", { name: "Start voice input" })).toBeVisible();
  await expect(panel(page).getByRole("button", { name: "Attach images" })).toHaveCount(0);
  await expect(panel(page).getByRole("button", { name: "Send" })).toHaveCount(0);
});

test("a chat message goes out as a message while the tray has rows", async ({ page }) => {
  await openChat(page);
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

  await expect(page.getByRole("heading", { name: "Waits for you (2)" })).toBeVisible();
  expect(decisions).toEqual([]);
});

test("the Inbox approves an item in the panel", async ({ page }) => {
  await routeInboxItems(page, approvalItems);
  const bodies = routeDecision(page, messageLead, "approve", {
    ...messageLead,
    state: "done",
    result: "Sent the message to the Lead of #12.",
  });

  await page.goto("/inbox");
  const main = page.getByRole("main");
  await expect(row(page, createTitle)).toBeVisible();
  await expect(row(page, messageTitle).getByRole("button")).toHaveText("Answer");

  await row(page, messageTitle).getByRole("button", { name: "Answer" }).click();
  await approve(page).click();

  await expect(panel(page)).toBeHidden();
  await expect(row(page, messageTitle)).toBeHidden();
  await expect(row(page, createTitle)).toBeVisible();
  await expect(main.getByRole("tab", { name: "To do 2" })).toBeVisible();
  expect(bodies).toEqual([{ message: "" }]);
});

test("the phone shows the cards, the tray and the panel with no horizontal scroll", async ({
  page,
}) => {
  await page.addInitScript(fakeMicrophone);
  await page.setViewportSize({ width: 390, height: 844 });
  await openChat(page);
  const fits = (selector: string) =>
    page
      .locator(selector)
      .first()
      .evaluate((el) => el.scrollWidth <= el.clientWidth);

  expect(await fits("html")).toBe(true);
  expect(await fits(`[data-action="${createWorkstream.id}"]`)).toBe(true);
  expect(await fits("main div.overflow-y-auto")).toBe(true);
  expect(await fits("main ul")).toBe(true);

  await answer(page, createTitle);
  await note(page).fill("Go ahead.");

  expect(await fits("html")).toBe(true);
  expect(await fits('[role="dialog"]')).toBe(true);
  const microphone = await panel(page)
    .getByRole("button", { name: "Start voice input" })
    .boundingBox();
  const rejectBox = await reject(page).boundingBox();
  const approveBox = await approve(page).boundingBox();
  expect(Math.abs((microphone?.y ?? 0) - (rejectBox?.y ?? 99))).toBeLessThan(4);
  expect(Math.abs((approveBox?.y ?? 0) - (rejectBox?.y ?? 99))).toBeLessThan(4);
  expect((approveBox?.x ?? 999) + (approveBox?.width ?? 0)).toBeLessThanOrEqual(390);
});
