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
import { routeInboxItems } from "./stops.js";

test.beforeEach(async ({ page }) => {
  await page.goto("/github");
  await page.getByLabel("Access password").fill("correct horse");
  await page.getByRole("button", { name: "Log in" }).click();
  await expect(page.getByLabel("Access password")).toBeHidden();
  await page.route("/api/chat/seen", (route) => route.fulfill({ status: 204 }));
});

const card = (page: Page, id: number) => page.getByRole("main").locator(`[data-action="${id}"]`);
const tray = (page: Page) => page.getByRole("list", { name: "Pending actions" });
const row = (page: Page, title: string) =>
  page.getByRole("main").getByRole("listitem").filter({ hasText: title });

async function openChat(page: Page) {
  await routeCards(page, createWorkstream, messageLead);
  await routeInboxItems(page, approvalItems);
  await page.goto("/chat");
  await expect(page.getByRole("heading", { name: "Waits for you (2)" })).toBeVisible();
}

test("the Owner approves a row with a note", async ({ page }) => {
  await openChat(page);
  const bodies = routeDecision(page, createWorkstream, "approve", done);
  const approve = row(page, createTitle).getByRole("button", { name: "Approve" });

  await expect(card(page, createWorkstream.id).getByText("Waits for the approval")).toBeVisible();
  await expect(card(page, createWorkstream.id).getByRole("button")).toHaveCount(1);
  await expect(
    card(page, createWorkstream.id).getByRole("button", { name: "Show all" }),
  ).toBeVisible();
  await approve.click();

  await expect(page.getByRole("heading", { name: `Approve: ${createTitle}` })).toBeVisible();
  await expect(page.getByPlaceholder("Add a note for the Coordinator (optional)")).toBeVisible();
  await expect(page.getByRole("button", { name: "Attach images" })).toBeHidden();
  await page.getByRole("textbox", { name: "Note for the Coordinator" }).fill("Go ahead.");
  await page.getByRole("button", { name: "Send" }).click();

  await expect(row(page, createTitle)).toBeHidden();
  await expect(page.getByRole("heading", { name: "Waits for you (1)" })).toBeVisible();
  const created = card(page, createWorkstream.id);
  await expect(created.locator('[data-slot="badge"]', { hasText: "Done" })).toBeVisible();
  await expect(created.getByText("Created the Workstream #790.")).toBeVisible();
  await expect(created.getByText("Go ahead.")).toBeVisible();
  await expect(page.getByRole("textbox", { name: "Message to the Coordinator" })).toHaveValue("");
  await expect(page.getByRole("button", { name: "Attach images" })).toBeVisible();
  expect(bodies).toEqual([{ message: "Go ahead." }]);
});

test("a decision goes out one time while the request runs", async ({ page }) => {
  await openChat(page);
  const bodies: unknown[] = [];
  let answer: (() => void) | undefined;
  const answered = new Promise<void>((resolve) => {
    answer = resolve;
  });
  await page.route(`**/api/actions/${createWorkstream.id}/approve`, async (route) => {
    bodies.push(route.request().postDataJSON());
    await answered;
    await route.fulfill({ json: { data: done } });
  });
  await row(page, createTitle).getByRole("button", { name: "Approve" }).click();
  const note = page.getByRole("textbox", { name: "Note for the Coordinator" });
  await page.getByRole("button", { name: "Send" }).click();

  await expect(page.getByRole("button", { name: "Send" })).toBeDisabled();
  await note.press("Enter");
  answer?.();

  await expect(row(page, createTitle)).toBeHidden();
  expect(bodies).toEqual([{ message: "" }]);
});

test("a choice shows only its row and the close button shows the full tray again", async ({
  page,
}) => {
  await openChat(page);
  const heading = page.getByRole("heading", { name: `Approve: ${createTitle}` });

  await row(page, createTitle).getByRole("button", { name: "Approve" }).click();

  await expect(heading).toBeVisible();
  await expect(tray(page).getByRole("listitem")).toHaveCount(1);
  await expect(page.getByRole("button", { name: "Approve" })).toBeHidden();
  await expect(page.getByRole("button", { name: "Reject" })).toBeHidden();
  await expect(page.getByRole("button", { name: "Show", exact: true })).toBeVisible();
  await expect(page.getByPlaceholder("Add a note for the Coordinator (optional)")).toBeVisible();
  await page.getByRole("textbox", { name: "Note for the Coordinator" }).fill("Go ahead.");

  await page.getByRole("button", { name: "Close" }).click();

  await expect(page.getByRole("heading", { name: "Waits for you (2)" })).toBeVisible();
  await expect(row(page, createTitle)).toBeVisible();
  await expect(row(page, messageTitle)).toBeVisible();
  await expect(page.getByRole("textbox", { name: "Message to the Coordinator" })).toBeVisible();
  await row(page, createTitle).getByRole("button", { name: "Approve" }).click();
  await expect(page.getByRole("textbox", { name: "Note for the Coordinator" })).toHaveValue(
    "Go ahead.",
  );
});

test("the Owner rejects a row with a note", async ({ page }) => {
  await openChat(page);
  const bodies = routeDecision(page, createWorkstream, "reject", rejected);

  await row(page, createTitle).getByRole("button", { name: "Reject" }).click();
  await page.getByRole("textbox", { name: "Note for the Coordinator" }).fill(rejected.message);
  await page.getByRole("button", { name: "Send" }).click();

  const created = card(page, createWorkstream.id);
  await expect(created.getByText("Rejected", { exact: true })).toBeVisible();
  await expect(created.getByText(rejected.message)).toBeVisible();
  await expect(row(page, createTitle)).toBeHidden();
  expect(bodies).toEqual([{ message: rejected.message }]);
});

test("a failed action keeps its row and its choice", async ({ page }) => {
  await openChat(page);
  routeDecision(page, createWorkstream, "approve", {
    ...createWorkstream,
    error: "403 API rate limit exceeded",
  });

  await row(page, createTitle).getByRole("button", { name: "Approve" }).click();
  await page.getByRole("button", { name: "Send" }).click();

  await expect(tray(page).getByRole("alert")).toHaveText("403 API rate limit exceeded");
  await expect(card(page, createWorkstream.id).getByText("Failed")).toBeVisible();
  await expect(page.getByRole("heading", { name: `Approve: ${createTitle}` })).toBeVisible();
});

test("a chat message goes out as a message while no choice is selected", async ({ page }) => {
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

test("Show opens the full card", async ({ page }) => {
  await openChat(page);
  const created = card(page, createWorkstream.id);

  await row(page, createTitle).getByRole("button", { name: "Show" }).click();
  await expect(created.getByRole("button", { name: "Show less" })).toBeVisible();
  await expect(created.getByText("Let customers buy a gift card")).toBeInViewport();
});

test("the Inbox approves an item with one tap", async ({ page }) => {
  await routeInboxItems(page, approvalItems);
  const bodies = routeDecision(page, messageLead, "approve", {
    ...messageLead,
    state: "done",
    result: "Sent the message to the Lead of #12.",
  });

  await page.goto("/inbox");
  const main = page.getByRole("main");
  await expect(row(page, createTitle)).toBeVisible();
  await expect(row(page, messageTitle)).toBeVisible();

  await row(page, messageTitle).getByRole("button", { name: "Approve" }).click();

  await expect(row(page, messageTitle)).toBeHidden();
  await expect(row(page, createTitle)).toBeVisible();
  await expect(main.getByRole("tab", { name: "To do 2" })).toBeVisible();
  expect(bodies).toEqual([{ message: "" }]);
});

test("the Inbox keeps an item and shows the error when the action fails", async ({ page }) => {
  await routeInboxItems(page, approvalItems);
  routeDecision(page, messageLead, "approve", {
    ...messageLead,
    error: "403 API rate limit exceeded",
  });

  await page.goto("/inbox");
  const item = row(page, messageTitle);
  await item.getByRole("button", { name: "Approve" }).click();

  await expect(item.getByRole("alert")).toHaveText("403 API rate limit exceeded");
  await expect(item.getByRole("button", { name: "Approve" })).toBeVisible();
  await expect(page.getByRole("main").getByRole("tab", { name: "To do 3" })).toBeVisible();
});

test("Open goes to the chat of the agent", async ({ page }) => {
  await routeInboxItems(page, approvalItems);
  await page.goto("/inbox");

  await row(page, messageTitle).getByRole("link", { name: "Open" }).click();

  await expect(page).toHaveURL("/chat");
});

test("the phone shows the cards, the tray and the composer with no horizontal scroll", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await openChat(page);
  await row(page, createTitle).getByRole("button", { name: "Approve" }).click();
  await page.getByRole("textbox", { name: "Note for the Coordinator" }).fill("Go ahead.");
  await tray(page).getByRole("button", { name: "Show" }).click();

  await expect(
    card(page, createWorkstream.id).getByText("A Playwright test buys a gift card"),
  ).toBeVisible();
  const fits = (selector: string) =>
    page
      .locator(selector)
      .first()
      .evaluate((el) => el.scrollWidth <= el.clientWidth);
  expect(await fits("html")).toBe(true);
  expect(await fits(`[data-action="${createWorkstream.id}"]`)).toBe(true);
  expect(await fits("main div.overflow-y-auto")).toBe(true);
  expect(await fits("main ul")).toBe(true);
  expect(await fits("main form")).toBe(true);
});
